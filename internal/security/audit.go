package security

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/storage"
)

// GenesisHash is the prev_hash of the first audit row.
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// ErrChainBroken is returned (wrapped in a VerifyResult) when the audit chain does not verify.
var ErrChainBroken = errors.New("security: audit chain broken")

// AuditEntry is what a caller appends. Detail is any JSON-marshalable value; it is scanned and redacted before storage.
type AuditEntry struct {
	Actor   string // "human:<name>", an agent id, "system", ...
	Action  string // "approval.requested", "pathguard.denied", ...
	Subject string
	Detail  any
}

// AuditRow is a stored audit row.
type AuditRow struct {
	Seq      int64           `json:"seq"`
	TS       int64           `json:"ts"`
	Actor    string          `json:"actor"`
	Action   string          `json:"action"`
	Subject  string          `json:"subject"`
	Detail   json.RawMessage `json:"detail"`
	PrevHash string          `json:"prevHash"`
	Hash     string          `json:"hash"`
}

// VerifyResult is the outcome of Audit.Verify.
type VerifyResult struct {
	OK     bool   `json:"ok"`
	Rows   int64  `json:"rows"`
	BadSeq int64  `json:"badSeq,omitempty"`
	Reason string `json:"reason,omitempty"`
	Head   string `json:"head"` // hash of the last row (record it elsewhere to detect tail truncation)
}

// Audit is the append-only, hash-chained audit log (docs/security.md s12). Rows are written through the storage
// writer, so Append inside AppendTx shares the caller's transaction with the effect it records. The chain detects
// edits, deletions and reordering; it does not stop a same-user process rewriting the whole file (T9), and truncating
// the tail is detected only against a previously recorded Head.
type Audit struct {
	db   *storage.DB
	scan SecretScanner
	now  func() int64
}

// NewAudit builds the audit log over db. scan may be nil only in tests; the product always passes a Scanner.
func NewAudit(db *storage.DB, scan SecretScanner) *Audit {
	return &Audit{db: db, scan: scan, now: storage.Now}
}

// Append writes one row in its own transaction.
func (a *Audit) Append(ctx context.Context, e AuditEntry) (AuditRow, error) {
	var row AuditRow
	err := a.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		row, err = a.AppendTx(ctx, tx, e)
		return err
	})
	return row, err
}

// AppendTx writes one row inside tx (which must be a storage writer transaction).
func (a *Audit) AppendTx(ctx context.Context, tx *sql.Tx, e AuditEntry) (AuditRow, error) {
	if e.Actor == "" || e.Action == "" {
		return AuditRow{}, errors.New("security: audit entry needs actor and action")
	}
	detail, err := a.cleanDetail(e.Detail)
	if err != nil {
		return AuditRow{}, err
	}
	prevSeq, prevHash := int64(0), GenesisHash
	err = tx.QueryRowContext(ctx, `SELECT seq, hash FROM audit_log ORDER BY seq DESC LIMIT 1`).Scan(&prevSeq, &prevHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AuditRow{}, err
	}
	row := AuditRow{Seq: prevSeq + 1, TS: a.now(), Actor: a.redact(e.Actor), Action: e.Action, Subject: a.redact(e.Subject), Detail: detail, PrevHash: prevHash}
	row.Hash, err = rowHash(row)
	if err != nil {
		return AuditRow{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_log(seq,ts,actor,action,subject,detail,prev_hash,hash) VALUES(?,?,?,?,?,?,?,?)`,
		row.Seq, row.TS, row.Actor, row.Action, row.Subject, string(row.Detail), row.PrevHash, row.Hash)
	return row, err
}

func (a *Audit) redact(s string) string {
	if a.scan == nil || len(s) < MinKnownSecretLen {
		return s
	}
	return a.scan.Redact(s)
}

// cleanDetail marshals detail, redacts every string leaf through the scanner, and returns canonical JSON.
func (a *Audit) cleanDetail(d any) (json.RawMessage, error) {
	if d == nil {
		return json.RawMessage(`{}`), nil
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("security: audit detail: %w", err)
	}
	var x any
	if err := json.Unmarshal(raw, &x); err != nil {
		return nil, err
	}
	x = a.redactTree(x)
	if x == nil {
		x = map[string]any{}
	}
	canon, err := CanonicalJSON(x)
	if err != nil || a.scan == nil {
		return canon, err
	}
	// Second pass over the whole document catches key/value context that single string leaves do not carry.
	if red := a.scan.Redact(string(canon)); red != string(canon) {
		if again, err := canonicalizeRaw([]byte(red)); err == nil {
			return again, nil
		}
		return json.RawMessage(`{"detail":"[withheld: secret in detail]"}`), nil
	}
	return canon, nil
}

func (a *Audit) redactTree(x any) any {
	switch t := x.(type) {
	case string:
		return a.redact(t)
	case []any:
		for i := range t {
			t[i] = a.redactTree(t[i])
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, v := range t {
			out[a.redact(k)] = a.redactTree(v)
		}
		return out
	default:
		return x
	}
}

// rowHash is SHA-256(prev_hash || canonicalJSON(row without hash)).
func rowHash(r AuditRow) (string, error) {
	body, err := CanonicalJSON(map[string]any{
		"seq": r.Seq, "ts": r.TS, "actor": r.Actor, "action": r.Action, "subject": r.Subject, "detail": json.RawMessage(r.Detail),
		"prevHash": r.PrevHash,
	})
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(r.PrevHash))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// List returns rows with seq > after, oldest first (limit 0 = 1000).
func (a *Audit) List(ctx context.Context, after int64, limit int) ([]AuditRow, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := a.db.Reader().QueryContext(ctx, `SELECT seq,ts,actor,action,subject,detail,prev_hash,hash FROM audit_log WHERE seq > ? ORDER BY seq LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditRow
	for rows.Next() {
		var r AuditRow
		var d string
		if err := rows.Scan(&r.Seq, &r.TS, &r.Actor, &r.Action, &r.Subject, &d, &r.PrevHash, &r.Hash); err != nil {
			return nil, err
		}
		r.Detail = json.RawMessage(d)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Verify walks the whole chain and reports the first inconsistency: a wrong hash (edited row), a wrong prev_hash
// (deleted or reordered row), or a gap in seq.
func (a *Audit) Verify(ctx context.Context) (VerifyResult, error) {
	res := VerifyResult{Head: GenesisHash}
	rows, err := a.db.Reader().QueryContext(ctx, `SELECT seq,ts,actor,action,subject,detail,prev_hash,hash FROM audit_log ORDER BY seq`)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	prev, prevSeq := GenesisHash, int64(0)
	for rows.Next() {
		var r AuditRow
		var d string
		if err := rows.Scan(&r.Seq, &r.TS, &r.Actor, &r.Action, &r.Subject, &d, &r.PrevHash, &r.Hash); err != nil {
			return res, err
		}
		r.Detail = json.RawMessage(d)
		fail := func(why string) (VerifyResult, error) {
			res.BadSeq, res.Reason = r.Seq, why
			return res, fmt.Errorf("%w at seq %d: %s", ErrChainBroken, r.Seq, why)
		}
		if r.Seq != prevSeq+1 {
			return fail(fmt.Sprintf("sequence gap (expected %d)", prevSeq+1))
		}
		if r.PrevHash != prev {
			return fail("prev_hash does not match the previous row")
		}
		canon, err := canonicalizeRaw([]byte(d))
		if err != nil || string(canon) != d {
			return fail("detail is not canonical JSON")
		}
		want, err := rowHash(r)
		if err != nil {
			return fail("row cannot be encoded: " + err.Error())
		}
		if !strings.EqualFold(want, r.Hash) {
			return fail("row hash mismatch (row was modified)")
		}
		prev, prevSeq = r.Hash, r.Seq
		res.Rows++
	}
	if err := rows.Err(); err != nil {
		return res, err
	}
	res.OK, res.Head = true, prev
	return res, nil
}

// Head returns the latest (seq, hash), or (0, GenesisHash) for an empty log.
func (a *Audit) Head(ctx context.Context) (int64, string, error) {
	var seq int64
	h := GenesisHash
	err := a.db.Reader().QueryRowContext(ctx, `SELECT seq, hash FROM audit_log ORDER BY seq DESC LIMIT 1`).Scan(&seq, &h)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, GenesisHash, nil
	}
	return seq, h, err
}

package security

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/storage"
)

// DefaultApprovalTTL is the default approval lifetime (design assumption, docs/security.md s5 rule 4).
const DefaultApprovalTTL = 30 * time.Minute

// Decision is a human's answer to an approval.
type Decision string

// Decisions.
const (
	DecisionApprove Decision = "approve"
	DecisionReject  Decision = "reject"
)

// Approval errors. Methods return an *ApprovalError that wraps exactly one of these.
var (
	ErrApprovalNotFound = errors.New("approval not found")
	ErrNotHuman         = errors.New("only a human principal can decide an approval")
	ErrWorkflowBound    = errors.New("approval is bound to a workflow run: answer it with the workflow engine")
	ErrAlreadyDecided   = errors.New("approval already decided")
	ErrApprovalExpired  = errors.New("approval expired")
	ErrNotApproved      = errors.New("approval has not been approved")
	ErrRejected         = errors.New("approval was rejected")
	ErrCancelled        = errors.New("approval was cancelled")
	ErrConsumed         = errors.New("approval already consumed")
	ErrHashMismatch     = errors.New("action does not match the approved action")
	ErrNoActionHash     = errors.New("approval carries no action hash and cannot authorize anything")
	ErrNoAuthority      = errors.New("request type carries no execution authority")
)

// ApprovalError is the typed error of the approvals service.
type ApprovalError struct {
	ID     string
	Err    error  // sentinel
	Detail string // never contains command text or secrets
}

func (e *ApprovalError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("approval %s: %s (%s)", e.ID, e.Err, e.Detail)
	}
	return fmt.Sprintf("approval %s: %s", e.ID, e.Err)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *ApprovalError) Unwrap() error { return e.Err }

func aerr(id string, err error, detail string) error {
	return &ApprovalError{ID: id, Err: err, Detail: detail}
}

// IsHumanPrincipal reports whether by is a "human:<name>" principal. Agents, tools, MCP servers and integrations never
// qualify; the agent registry must refuse agent ids that start with "human:".
func IsHumanPrincipal(by string) bool {
	name, ok := strings.CutPrefix(by, "human:")
	if !ok || name == "" || len(name) > 128 || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ApprovalInfo is the row view of a service-created approval.
type ApprovalInfo struct {
	ID         string           `json:"id"`
	Status     string           `json:"status"`
	ActionKind string           `json:"actionKind"`
	ActionHash string           `json:"actionHash"`
	Title      string           `json:"title"`
	Reason     string           `json:"reason"`
	Action     ActionDescriptor `json:"action"` // display copy: secrets in Command/Argv are redacted
	DecidedBy  string           `json:"decidedBy,omitempty"`
	Created    int64            `json:"created"`
	Resolved   int64            `json:"resolved,omitempty"`
	ExpiresAt  int64            `json:"expiresAt,omitempty"`
	RunID      string           `json:"runId,omitempty"` // non-empty for workflow-bound rows
}

// Approvals is the approvals service over the approvals table, for approvals that are not bound to a workflow run
// (run_id ”). Workflow-bound rows are answered by the workflow engine; Consume works for both, because the executor
// of an action always proves the action to this service (docs/security.md s5 rule 1).
type Approvals struct {
	db    *storage.DB
	bus   events.Bus
	audit *Audit
	scan  SecretScanner
	ttl   time.Duration
	now   func() time.Time
}

// ApprovalOption configures NewApprovals.
type ApprovalOption func(*Approvals)

// WithApprovalTTL sets the default lifetime.
func WithApprovalTTL(d time.Duration) ApprovalOption { return func(a *Approvals) { a.ttl = d } }

// WithApprovalClock replaces time.Now (tests).
func WithApprovalClock(now func() time.Time) ApprovalOption {
	return func(a *Approvals) { a.now = now }
}

// WithApprovalRedactor redacts secrets from the stored display copy of the action and from titles.
func WithApprovalRedactor(s SecretScanner) ApprovalOption { return func(a *Approvals) { a.scan = s } }

// NewApprovals builds the service. bus and audit may be nil only in tests.
func NewApprovals(db *storage.DB, bus events.Bus, audit *Audit, opts ...ApprovalOption) *Approvals {
	a := &Approvals{db: db, bus: bus, audit: audit, ttl: DefaultApprovalTTL, now: time.Now}
	for _, o := range opts {
		o(a)
	}
	return a
}

// RequestOption configures a Request.
type RequestOption func(*requestCfg)

type requestCfg struct {
	ttl   time.Duration
	title string
}

// WithTTL overrides the lifetime of one request.
func WithTTL(d time.Duration) RequestOption { return func(c *requestCfg) { c.ttl = d } }

// WithTitle replaces the default title ("<kind>: <command>"). It is redacted and capped like the default.
func WithTitle(title string) RequestOption { return func(c *requestCfg) { c.title = title } }

func (a *Approvals) nowMS() int64 { return a.now().UTC().UnixMilli() }

func (a *Approvals) redact(s string) string {
	if a.scan == nil {
		return s
	}
	return a.scan.Redact(s)
}

type approvalPayload struct {
	SecurityKind string           `json:"securityKind"`
	Reason       string           `json:"reason"`
	Action       ActionDescriptor `json:"action"`
	ExpiresAt    int64            `json:"expiresAt"`
}

// Request creates a REQUESTED approval bound to the hash of action and publishes approval.requested. The row's
// run_id is ” (not workflow-bound); the descriptor's own RunID/NodeID/Step/Attempt stay in the hash, which keeps the
// approval on its checkpoint lineage. The stored payload is a display copy with secrets redacted; the hash is of the
// exact action.
func (a *Approvals) Request(ctx context.Context, action ActionDescriptor, reason string, opts ...RequestOption) (string, error) {
	if action.Kind == "" {
		return "", errors.New("security: action descriptor has no kind")
	}
	cfg := requestCfg{ttl: a.ttl}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.ttl <= 0 {
		cfg.ttl = DefaultApprovalTTL
	}
	id := ids.New("apr")
	hash := action.Hash()
	created := a.nowMS()
	expires := created + cfg.ttl.Milliseconds()

	display := action.normalized()
	display.Command = a.redact(display.Command)
	display.Cwd = a.redact(display.Cwd)
	for i := range display.Argv {
		display.Argv[i] = a.redact(display.Argv[i])
	}
	payload, err := json.Marshal(approvalPayload{SecurityKind: action.Kind, Reason: a.redact(reason), Action: display, ExpiresAt: expires})
	if err != nil {
		return "", err
	}
	title := action.Kind
	if action.Command != "" {
		title += ": " + a.redact(action.Command)
	} else if action.Tool != "" {
		title += ": " + action.Tool
	}
	if cfg.title != "" {
		title = a.redact(cfg.title)
	}
	title = truncate(title, 200)

	err = a.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO approvals(id,run_id,node_id,task_id,project_id,kind,title,payload,action_hash,status,created_at,expires_at,action_kind)
			VALUES(?,?,?,?,?,'approval',?,?,?,'REQUESTED',?,?,?)`, id, "", action.NodeID, action.TaskID, action.Project, title, payload, hash, created, expires, action.Kind); err != nil {
			return err
		}
		return a.auditTx(ctx, tx, "system", "approval.requested", id, map[string]any{"kind": action.Kind, "actionHash": hash, "expiresAt": expires, "agent": action.AgentID, "run": action.RunID})
	})
	if err != nil {
		return "", err
	}
	a.emit(ctx, "approval.requested", action, map[string]any{"request": id, "kind": "approval", "actionKind": action.Kind, "title": title, "actionHash": hash, "expiresAt": expires})
	return id, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (a *Approvals) auditTx(ctx context.Context, tx *sql.Tx, actor, action, subject string, detail any) error {
	if a.audit == nil {
		return nil
	}
	_, err := a.audit.AppendTx(ctx, tx, AuditEntry{Actor: actor, Action: action, Subject: subject, Detail: detail})
	return err
}

// emit publishes best-effort: the approval row and audit entry are already durable, and a bus failure must not undo
// a security state change.
func (a *Approvals) emit(ctx context.Context, typ string, scope ActionDescriptor, data map[string]any) {
	if a.bus == nil {
		return
	}
	_, _ = events.Emit(ctx, a.bus, typ, events.Event{ProjectID: scope.Project, RunID: scope.RunID, TaskID: scope.TaskID, AgentID: scope.AgentID}, data)
}

type approvalRow struct {
	id, runID, kind, status, hash, actionKind, project, task, decidedBy, title string
	payload                                                                    []byte
	expiresAt, createdAt, version                                              int64
}

func (r approvalRow) scope() ActionDescriptor {
	var p approvalPayload
	_ = json.Unmarshal(r.payload, &p)
	d := p.Action
	if d.Project == "" {
		d.Project = r.project
	}
	if d.TaskID == "" {
		d.TaskID = r.task
	}
	return d
}

func loadRow(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (approvalRow, error) {
	r := approvalRow{id: id}
	err := q.QueryRowContext(ctx, `SELECT run_id,kind,status,action_hash,action_kind,project_id,task_id,decided_by,title,payload,expires_at,created_at,version
		FROM approvals WHERE id=?`, id).Scan(&r.runID, &r.kind, &r.status, &r.hash, &r.actionKind, &r.project, &r.task, &r.decidedBy, &r.title, &r.payload, &r.expiresAt, &r.createdAt, &r.version)
	if errors.Is(err, sql.ErrNoRows) {
		return r, aerr(id, ErrApprovalNotFound, "")
	}
	return r, err
}

// transition CASes the row from `from` to `to` and writes the audit row, inside tx.
func (a *Approvals) transition(ctx context.Context, tx *sql.Tx, r approvalRow, from, to, decision, by string, newExpiry int64, audit string) (bool, error) {
	set := `status=?, version=version+1`
	args := []any{to}
	if decision != "" {
		set += `, decision=?, decided_by=?, resolved_at=?`
		args = append(args, decision, by, a.nowMS())
	} else if to == "EXPIRED" || to == "CANCELLED" {
		set += `, resolved_at=?`
		args = append(args, a.nowMS())
	}
	if newExpiry >= 0 {
		set += `, expires_at=?`
		args = append(args, newExpiry)
	}
	args = append(args, r.id, from, r.version)
	res, err := tx.ExecContext(ctx, `UPDATE approvals SET `+set+` WHERE id=? AND status=? AND version=?`, args...)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return false, nil
	}
	actor := by
	if actor == "" {
		actor = "system"
	}
	return true, a.auditTx(ctx, tx, actor, audit, r.id, map[string]any{"kind": r.actionKind, "actionHash": r.hash, "from": from, "to": to})
}

// Decide records a human's answer (REQUESTED -> APPROVED | REJECTED) with a compare-and-swap. by must be a
// "human:<name>" principal supplied by the UI service layer; any other author, including every agent, is refused and
// the attempt is audited. Approving restarts the lifetime so the executor gets a full window to Consume.
func (a *Approvals) Decide(ctx context.Context, id string, d Decision, by string) error {
	if d != DecisionApprove && d != DecisionReject {
		return aerr(id, ErrNoAuthority, "unknown decision")
	}
	if !IsHumanPrincipal(by) {
		_ = a.db.WriteTx(ctx, func(tx *sql.Tx) error {
			return a.auditTx(ctx, tx, "system", "approval.decide_denied", id, map[string]any{"attemptedBy": truncate(by, 128), "decision": string(d)})
		})
		a.emit(ctx, "approval.decide_denied", ActionDescriptor{}, map[string]any{"request": id, "attemptedBy": truncate(by, 128)})
		return aerr(id, ErrNotHuman, "")
	}
	var outcome error
	var row approvalRow
	var newStatus string
	err := a.db.WriteTx(ctx, func(tx *sql.Tx) error {
		outcome = nil
		var err error
		row, err = loadRow(ctx, tx, id)
		if err != nil {
			outcome = err
			return nil
		}
		switch {
		case row.runID != "":
			outcome = aerr(id, ErrWorkflowBound, "")
			return nil
		case row.kind != "approval":
			outcome = aerr(id, ErrNoAuthority, row.kind)
			return nil
		case row.status == "EXPIRED":
			outcome = aerr(id, ErrApprovalExpired, "")
			return nil
		case row.status != "REQUESTED":
			outcome = aerr(id, ErrAlreadyDecided, row.status)
			return nil
		}
		if row.expiresAt > 0 && a.nowMS() > row.expiresAt {
			if _, err := a.transition(ctx, tx, row, "REQUESTED", "EXPIRED", "", "", -1, "approval.expired"); err != nil {
				return err
			}
			outcome = aerr(id, ErrApprovalExpired, "")
			newStatus = "EXPIRED"
			return nil
		}
		to, decision, expiry := "REJECTED", "rejected", int64(-1)
		audit := "approval.rejected"
		if d == DecisionApprove {
			to, decision, audit = "APPROVED", "approved", "approval.approved"
			if row.expiresAt > 0 {
				expiry = a.nowMS() + (row.expiresAt - row.createdAt)
			}
		}
		newStatus = to
		ok, err := a.transition(ctx, tx, row, "REQUESTED", to, decision, by, expiry, audit)
		if err != nil {
			return err
		}
		if !ok {
			outcome = aerr(id, ErrAlreadyDecided, "lost the compare-and-swap")
			newStatus = ""
		}
		return nil
	})
	if err != nil {
		return err
	}
	if outcome == nil {
		typ := "approval.approved"
		if newStatus == "REJECTED" {
			typ = "approval.rejected"
		}
		a.emit(ctx, typ, row.scope(), map[string]any{"request": id, "decision": strings.ToLower(string(d)), "by": by, "actionHash": row.hash})
	} else if newStatus == "EXPIRED" {
		a.emit(ctx, "approval.expired", row.scope(), map[string]any{"request": id})
	}
	return outcome
}

// Consume proves that the action about to run is the approved one and spends the approval: it recomputes the hash of
// actual, compares it in constant time with the approved hash, and moves APPROVED -> CONSUMED exactly once. It returns
// an *ApprovalError wrapping ErrHashMismatch, ErrConsumed (reuse), ErrApprovalExpired, ErrRejected, ErrCancelled,
// ErrNotApproved or ErrNoActionHash. A mismatch leaves the approval APPROVED for the action it was granted for.
func (a *Approvals) Consume(ctx context.Context, id string, actual ActionDescriptor) error {
	var outcome error
	var row approvalRow
	var event string
	err := a.db.WriteTx(ctx, func(tx *sql.Tx) error {
		outcome, event = nil, ""
		var err error
		row, err = loadRow(ctx, tx, id)
		if err != nil {
			outcome = err
			return nil
		}
		deny := func(e error, audit string, detail string) {
			outcome = aerr(id, e, detail)
			_ = a.auditTx(ctx, tx, "system", audit, id, map[string]any{"kind": row.actionKind, "status": row.status, "reason": e.Error()})
		}
		if row.kind != "approval" {
			deny(ErrNoAuthority, "approval.consume_denied", row.kind)
			return nil
		}
		switch row.status {
		case "APPROVED":
		case "CONSUMED":
			deny(ErrConsumed, "approval.reuse_denied", "")
			event = "approval.reuse_denied"
			return nil
		case "REJECTED":
			deny(ErrRejected, "approval.consume_denied", "")
			return nil
		case "CANCELLED":
			deny(ErrCancelled, "approval.consume_denied", "")
			return nil
		case "EXPIRED":
			deny(ErrApprovalExpired, "approval.consume_denied", "")
			return nil
		default:
			deny(ErrNotApproved, "approval.consume_denied", "")
			return nil
		}
		if row.hash == "" {
			deny(ErrNoActionHash, "approval.consume_denied", "")
			return nil
		}
		if row.expiresAt > 0 && a.nowMS() > row.expiresAt {
			if _, err := a.transition(ctx, tx, row, "APPROVED", "EXPIRED", "", "", -1, "approval.expired"); err != nil {
				return err
			}
			outcome = aerr(id, ErrApprovalExpired, "")
			event = "approval.expired"
			return nil
		}
		got := actual.Hash()
		if subtle.ConstantTimeCompare([]byte(got), []byte(row.hash)) != 1 {
			outcome = aerr(id, ErrHashMismatch, fmt.Sprintf("approved %s, presented %s", short(row.hash), short(got)))
			event = "approval.mismatch"
			return a.auditTx(ctx, tx, "system", "approval.mismatch", id, map[string]any{"kind": row.actionKind, "approvedHash": row.hash, "presentedHash": got, "presentedKind": actual.Kind})
		}
		ok, err := a.transition(ctx, tx, row, "APPROVED", "CONSUMED", "", "", -1, "approval.consumed")
		if err != nil {
			return err
		}
		if !ok {
			outcome = aerr(id, ErrConsumed, "lost the compare-and-swap")
			event = "approval.reuse_denied"
		} else {
			event = "approval.consumed"
		}
		return nil
	})
	if err != nil {
		return err
	}
	if event != "" {
		data := map[string]any{"request": id}
		if event == "approval.mismatch" {
			data["approvedHash"], data["presentedHash"] = row.hash, actual.Hash()
		}
		a.emit(ctx, event, row.scope(), data)
	}
	return outcome
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// Cancel moves a REQUESTED or APPROVED approval to CANCELLED. Cancelling only removes authority, so any caller may do
// it (for example a lineage branch or a stopped task); it is audited with the caller.
func (a *Approvals) Cancel(ctx context.Context, id, by string) error {
	var outcome error
	var row approvalRow
	err := a.db.WriteTx(ctx, func(tx *sql.Tx) error {
		outcome = nil
		var err error
		row, err = loadRow(ctx, tx, id)
		if err != nil {
			outcome = err
			return nil
		}
		if row.runID != "" {
			outcome = aerr(id, ErrWorkflowBound, "")
			return nil
		}
		if row.status != "REQUESTED" && row.status != "APPROVED" {
			outcome = aerr(id, ErrAlreadyDecided, row.status)
			return nil
		}
		ok, err := a.transition(ctx, tx, row, row.status, "CANCELLED", "", by, -1, "approval.cancelled")
		if err != nil {
			return err
		}
		if !ok {
			outcome = aerr(id, ErrAlreadyDecided, "lost the compare-and-swap")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if outcome == nil {
		a.emit(ctx, "approval.cancelled", row.scope(), map[string]any{"request": id})
	}
	return outcome
}

// CancelForRun cancels every outstanding service-created approval whose descriptor carries runID. The workflow layer
// calls it when a time-travel branch is created (docs/security.md s5 rule 3). Returns how many were cancelled.
func (a *Approvals) CancelForRun(ctx context.Context, runID, by string) (int, error) {
	rows, err := a.db.Reader().QueryContext(ctx, `SELECT id FROM approvals WHERE run_id='' AND status IN ('REQUESTED','APPROVED') AND json_extract(payload,'$.action.runId') = ?`, runID)
	if err != nil {
		return 0, err
	}
	var idl []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		idl = append(idl, id)
	}
	rows.Close()
	n := 0
	for _, id := range idl {
		switch err := a.Cancel(ctx, id, by); {
		case err == nil:
			n++
		case errors.Is(err, ErrAlreadyDecided):
		default:
			return n, err
		}
	}
	return n, nil
}

// ExpireDue marks every REQUESTED or APPROVED service-created approval past its expiry as EXPIRED. Decide and Consume
// also enforce expiry lazily, so correctness never depends on this sweep running.
func (a *Approvals) ExpireDue(ctx context.Context) (int, error) {
	var rows []approvalRow
	err := a.db.WriteTx(ctx, func(tx *sql.Tx) error {
		rows = nil
		rs, err := tx.QueryContext(ctx, `SELECT id FROM approvals WHERE run_id='' AND expires_at > 0 AND expires_at < ? AND status IN ('REQUESTED','APPROVED')`, a.nowMS())
		if err != nil {
			return err
		}
		var idl []string
		for rs.Next() {
			var id string
			if err := rs.Scan(&id); err != nil {
				rs.Close()
				return err
			}
			idl = append(idl, id)
		}
		rs.Close()
		for _, id := range idl {
			r, err := loadRow(ctx, tx, id)
			if err != nil {
				return err
			}
			ok, err := a.transition(ctx, tx, r, r.status, "EXPIRED", "", "", -1, "approval.expired")
			if err != nil {
				return err
			}
			if ok {
				rows = append(rows, r)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		a.emit(ctx, "approval.expired", r.scope(), map[string]any{"request": r.id})
	}
	return len(rows), nil
}

// Get returns one approval.
func (a *Approvals) Get(ctx context.Context, id string) (ApprovalInfo, error) {
	r, err := loadRow(ctx, a.db.Reader(), id)
	if err != nil {
		return ApprovalInfo{}, err
	}
	var p approvalPayload
	_ = json.Unmarshal(r.payload, &p)
	var resolved int64
	_ = a.db.Reader().QueryRowContext(ctx, `SELECT resolved_at FROM approvals WHERE id=?`, id).Scan(&resolved)
	return ApprovalInfo{ID: id, Status: r.status, ActionKind: r.actionKind, ActionHash: r.hash, Title: r.title, Reason: p.Reason, Action: p.Action,
		DecidedBy: r.decidedBy, Created: r.createdAt, Resolved: resolved, ExpiresAt: r.expiresAt, RunID: r.runID}, nil
}

// RequireHuman is the guard for every code path that answers an approval, including the workflow engine's Resume
// (whose Response.By is otherwise unchecked): it returns ErrNotHuman unless by is a "human:<name>" principal.
func RequireHuman(by string) error {
	if !IsHumanPrincipal(by) {
		return aerr("", ErrNotHuman, "")
	}
	return nil
}

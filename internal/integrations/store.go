package integrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Store is the SQLite persistence of the package. It holds no secrets: a connection row carries only a credential reference.
type Store struct {
	DB *storage.DB
	// BodyDir is <app data>/integrations; sanitized item bodies are written under it.
	BodyDir string
	Now     func() int64
}

func (s *Store) now() int64 {
	if s.Now != nil {
		return s.Now()
	}
	return storage.Now()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- connections ----

const connCols = `id,kind,name,project_id,status,status_detail,auth_kind,credential_ref,base_url,config_json,poll_interval_s,enabled,created_at,updated_at,version`

type rowScanner interface{ Scan(dest ...any) error }

func scanConn(r rowScanner) (Connection, error) {
	var c Connection
	var cfg string
	var en int
	if err := r.Scan(&c.ID, &c.Kind, &c.Name, &c.ProjectID, &c.Status, &c.StatusDetail, &c.AuthKind, &c.CredentialRef, &c.BaseURL, &cfg, &c.PollIntervalS, &en, &c.Created, &c.Updated, &c.Version); err != nil {
		return c, err
	}
	c.Enabled = en != 0
	c.Config = map[string]string{}
	_ = json.Unmarshal([]byte(cfg), &c.Config)
	return c, nil
}

// InsertConnection stores a new connection.
func (s *Store) InsertConnection(ctx context.Context, c Connection) (Connection, error) {
	if c.ID == "" {
		c.ID = ids.New("int")
	}
	c.Created, c.Updated, c.Version = s.now(), s.now(), 1
	cfg, _ := json.Marshal(nz(c.Config))
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO integrations(`+connCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.ID, c.Kind, c.Name, c.ProjectID, c.Status, c.StatusDetail, c.AuthKind, c.CredentialRef, c.BaseURL, string(cfg), c.PollIntervalS, b2i(c.Enabled), c.Created, c.Updated, c.Version)
		return err
	})
	return c, err
}

func nz(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// GetConnection returns one connection.
func (s *Store) GetConnection(ctx context.Context, id string) (Connection, error) {
	c, err := scanConn(s.DB.Reader().QueryRowContext(ctx, `SELECT `+connCols+` FROM integrations WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return c, fmt.Errorf("%w: connection %s", ErrNotFound, id)
	}
	return c, err
}

// ListConnections returns every connection, oldest first.
func (s *Store) ListConnections(ctx context.Context) ([]Connection, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+connCols+` FROM integrations ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		c, err := scanConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetStatus records the connection status and its plain-language detail.
func (s *Store) SetStatus(ctx context.Context, id, status, detail string) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE integrations SET status=?, status_detail=?, updated_at=? WHERE id=?`, status, detail, s.now(), id)
		return err
	})
}

// UpdateConnection replaces the editable fields with a compare-and-swap on version.
func (s *Store) UpdateConnection(ctx context.Context, c Connection, expectVersion int64) (Connection, error) {
	cfg, _ := json.Marshal(nz(c.Config))
	var n int64
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE integrations SET name=?, project_id=?, status=?, status_detail=?, base_url=?, config_json=?, poll_interval_s=?, enabled=?, credential_ref=?, updated_at=?, version=version+1
			WHERE id=? AND version=?`, c.Name, c.ProjectID, c.Status, c.StatusDetail, c.BaseURL, string(cfg), c.PollIntervalS, b2i(c.Enabled), c.CredentialRef, s.now(), c.ID, expectVersion)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	if err != nil {
		return c, err
	}
	if n == 0 {
		return c, ErrConflict
	}
	return s.GetConnection(ctx, c.ID)
}

// DeleteConnection removes a connection, its cursors, rules and outbox, and the items that never became a task. Items that
// did become a task stay (marked gone) so the task keeps its provenance.
func (s *Store) DeleteConnection(ctx context.Context, id string) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM integration_cursors WHERE integration_id=?`,
			`DELETE FROM integration_rule_matches WHERE rule_id IN (SELECT id FROM integration_rules WHERE integration_id=?)`,
			`DELETE FROM integration_rules WHERE integration_id=?`,
			`DELETE FROM external_items WHERE integration_id=? AND task_id=''`,
			`UPDATE external_items SET gone=1 WHERE integration_id=?`,
			`DELETE FROM integrations WHERE id=?`,
		} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- cursors ----

// Cursor is the polling state of one (integration, scope).
type Cursor struct {
	IntegrationID       string
	Scope               string
	After               string // vendor cursor of an interrupted sweep ("" = start at the newest)
	Pending             int64  // max updated time seen by the running sweep; becomes the watermark when the sweep completes
	Watermark           int64
	LastSuccessAt       int64
	LastAttemptAt       int64
	LastError           string
	LastErrorCode       string
	ItemsLastSync       int
	ConsecutiveFailures int
	NextAttemptAt       int64
	// SweepSeen counts items of the running sweep, SweepStart marks its start (full-sweep adapters mark missing items gone).
	SweepSeen  int
	SweepStart int64
}

type cursorJSON struct {
	After      string `json:"after,omitempty"`
	Pending    int64  `json:"pending,omitempty"`
	SweepSeen  int    `json:"sweepSeen,omitempty"`
	SweepStart int64  `json:"sweepStart,omitempty"`
}

const defaultScope = "default"

// GetCursor returns the cursor of a scope (a zero cursor when none was stored).
func (s *Store) GetCursor(ctx context.Context, integrationID, scope string) (Cursor, error) {
	return getCursor(ctx, s.DB.Reader(), integrationID, scope)
}

type queryRower interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

func getCursor(ctx context.Context, q queryRower, integrationID, scope string) (Cursor, error) {
	c := Cursor{IntegrationID: integrationID, Scope: scope}
	var cj string
	err := q.QueryRowContext(ctx, `SELECT cursor_json,watermark,last_success_at,last_attempt_at,last_error,last_error_code,items_last_sync,consecutive_failures,next_attempt_at
		FROM integration_cursors WHERE integration_id=? AND scope=?`, integrationID, scope).
		Scan(&cj, &c.Watermark, &c.LastSuccessAt, &c.LastAttemptAt, &c.LastError, &c.LastErrorCode, &c.ItemsLastSync, &c.ConsecutiveFailures, &c.NextAttemptAt)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	var j cursorJSON
	_ = json.Unmarshal([]byte(cj), &j)
	c.After, c.Pending, c.SweepSeen, c.SweepStart = j.After, j.Pending, j.SweepSeen, j.SweepStart
	return c, nil
}

func putCursor(ctx context.Context, tx *sql.Tx, c Cursor) error {
	cj, _ := json.Marshal(cursorJSON{After: c.After, Pending: c.Pending, SweepSeen: c.SweepSeen, SweepStart: c.SweepStart})
	_, err := tx.ExecContext(ctx, `INSERT INTO integration_cursors(integration_id,scope,cursor_json,watermark,last_success_at,last_attempt_at,last_error,last_error_code,items_last_sync,consecutive_failures,next_attempt_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(integration_id,scope) DO UPDATE SET cursor_json=excluded.cursor_json, watermark=excluded.watermark, last_success_at=excluded.last_success_at,
		last_attempt_at=excluded.last_attempt_at, last_error=excluded.last_error, last_error_code=excluded.last_error_code, items_last_sync=excluded.items_last_sync,
		consecutive_failures=excluded.consecutive_failures, next_attempt_at=excluded.next_attempt_at`,
		c.IntegrationID, c.Scope, string(cj), c.Watermark, c.LastSuccessAt, c.LastAttemptAt, c.LastError, c.LastErrorCode, c.ItemsLastSync, c.ConsecutiveFailures, c.NextAttemptAt)
	return err
}

// PutCursor stores a cursor on its own (failure bookkeeping).
func (s *Store) PutCursor(ctx context.Context, c Cursor) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error { return putCursor(ctx, tx, c) })
}

// ---- items ----

// ItemRecord is a sanitized item ready to store, with its body text.
type ItemRecord struct {
	Item
	Body string
}

// UpsertOutcome says what an upsert did.
type UpsertOutcome int

// Upsert outcomes.
const (
	Unchanged UpsertOutcome = iota
	Inserted
	Updated
)

const itemCols = `id,integration_id,source,project_id,external_id,kind,issue_ref,url,title,excerpt,body_ref,state,labels_json,assignee,priority,parent_external_id,meta_json,
external_created_at,external_updated_at,etag,content_hash,fetched_at,risk,risk_flags,redactions,truncated,task_id,imported_by,imported_at,gone,version`

func scanItem(r rowScanner) (Item, error) {
	var it Item
	var labels, meta, flags string
	var trunc, gone int
	if err := r.Scan(&it.ID, &it.IntegrationID, &it.Source, &it.ProjectID, &it.ExternalID, &it.Kind, &it.IssueRef, &it.URL, &it.Title, &it.Excerpt, &it.BodyRef, &it.State, &labels,
		&it.Assignee, &it.Priority, &it.ParentExternalID, &meta, &it.ExternalCreated, &it.ExternalUpdated, &it.Etag, &it.ContentHash, &it.FetchedAt, &it.Risk, &flags, &it.Redactions,
		&trunc, &it.TaskID, &it.ImportedBy, &it.ImportedAt, &gone, &it.Version); err != nil {
		return it, err
	}
	it.Untrusted = true
	it.Truncated, it.Gone = trunc != 0, gone != 0
	it.Labels, it.RiskFlags = []string{}, []string{}
	it.Meta = map[string]any{}
	_ = json.Unmarshal([]byte(labels), &it.Labels)
	_ = json.Unmarshal([]byte(flags), &it.RiskFlags)
	_ = json.Unmarshal([]byte(meta), &it.Meta)
	return it, nil
}

// ContentHash computes the idempotency hash of a sanitized item.
func ContentHash(it Item, body string) string {
	lb, _ := json.Marshal(it.Labels)
	mb, _ := json.Marshal(it.Meta) // map keys are sorted by encoding/json
	return hashHex(it.Kind, it.IssueRef, it.URL, it.Title, hashHex(body), it.State, string(lb), it.Assignee, it.Priority, it.ParentExternalID, string(mb), it.Etag)
}

func (s *Store) bodyPath(integrationID, itemID string) string {
	return filepath.Join(s.BodyDir, integrationID, itemID+".txt")
}

// upsertTx stores one sanitized item inside tx.
func (s *Store) upsertTx(ctx context.Context, tx *sql.Tx, rec ItemRecord) (Item, UpsertOutcome, error) {
	it := rec.Item
	it.ContentHash = ContentHash(it, rec.Body)
	now := s.now()
	var id, oldHash string
	err := tx.QueryRowContext(ctx, `SELECT id, content_hash FROM external_items WHERE integration_id=? AND external_id=?`, it.IntegrationID, it.ExternalID).Scan(&id, &oldHash)
	lb, _ := json.Marshal(nzs(it.Labels))
	mb, _ := json.Marshal(it.Meta)
	fb, _ := json.Marshal(nzs(it.RiskFlags))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		it.ID = ids.New("ext")
		if rec.Body != "" && s.BodyDir != "" {
			it.BodyRef = s.writeBody(it.IntegrationID, it.ID, rec.Body)
		}
		it.FetchedAt = now
		_, err := tx.ExecContext(ctx, `INSERT INTO external_items(`+itemCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			it.ID, it.IntegrationID, it.Source, it.ProjectID, it.ExternalID, it.Kind, it.IssueRef, it.URL, it.Title, it.Excerpt, it.BodyRef, it.State, string(lb), it.Assignee, it.Priority,
			it.ParentExternalID, string(mb), it.ExternalCreated, it.ExternalUpdated, it.Etag, it.ContentHash, it.FetchedAt, it.Risk, string(fb), it.Redactions, b2i(it.Truncated), "", "", 0, 0, 1)
		it.Version = 1
		return it, Inserted, err
	case err != nil:
		return it, Unchanged, err
	}
	it.ID = id
	if oldHash == it.ContentHash {
		_, err := tx.ExecContext(ctx, `UPDATE external_items SET fetched_at=?, gone=0 WHERE id=?`, now, id)
		return it, Unchanged, err
	}
	if rec.Body != "" && s.BodyDir != "" {
		it.BodyRef = s.writeBody(it.IntegrationID, id, rec.Body)
	}
	_, err = tx.ExecContext(ctx, `UPDATE external_items SET project_id=?, kind=?, issue_ref=?, url=?, title=?, excerpt=?, body_ref=CASE WHEN ?<>'' THEN ? ELSE body_ref END, state=?, labels_json=?, assignee=?, priority=?,
		parent_external_id=?, meta_json=?, external_created_at=?, external_updated_at=?, etag=?, content_hash=?, fetched_at=?, risk=?, risk_flags=?, redactions=?, truncated=?, gone=0, version=version+1 WHERE id=?`,
		it.ProjectID, it.Kind, it.IssueRef, it.URL, it.Title, it.Excerpt, it.BodyRef, it.BodyRef, it.State, string(lb), it.Assignee, it.Priority, it.ParentExternalID, string(mb),
		it.ExternalCreated, it.ExternalUpdated, it.Etag, it.ContentHash, now, it.Risk, string(fb), it.Redactions, b2i(it.Truncated), id)
	return it, Updated, err
}

func nzs(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// writeBody stores the sanitized, redacted body (never the raw text) and returns its path relative to BodyDir.
func (s *Store) writeBody(integrationID, itemID, body string) string {
	p := s.bodyPath(integrationID, itemID)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return ""
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		return ""
	}
	rel, err := filepath.Rel(s.BodyDir, p)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

// ReadBody returns the stored sanitized body of an item ("" when none).
func (s *Store) ReadBody(it Item) string {
	if it.BodyRef == "" || s.BodyDir == "" {
		return ""
	}
	clean := filepath.Clean(filepath.FromSlash(it.BodyRef))
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(s.BodyDir, clean))
	if err != nil {
		return ""
	}
	return string(b)
}

// PageCommit is everything one polled page changes, committed atomically with the cursor it advances.
type PageCommit struct {
	Items         []ItemRecord
	Cursor        Cursor
	IntegrationID string
	// Touch lists external ids seen again but not re-fetched (their vendor version marker is unchanged).
	Touch []string
}

// PageResult counts what a commit did and lists the items that were new or changed.
type PageResult struct {
	New, Changed, Unchanged int
	Touched                 []Item
	Outcomes                []UpsertOutcome
}

// CommitPage upserts the items and stores the cursor in one transaction, so a crash leaves either both or neither.
func (s *Store) CommitPage(ctx context.Context, pc PageCommit) (PageResult, error) {
	var res PageResult
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res = PageResult{}
		for _, ext := range pc.Touch {
			if _, err := tx.ExecContext(ctx, `UPDATE external_items SET fetched_at=?, gone=0 WHERE integration_id=? AND external_id=?`, s.now(), pc.IntegrationID, ext); err != nil {
				return err
			}
		}
		for _, rec := range pc.Items {
			it, out, err := s.upsertTx(ctx, tx, rec)
			if err != nil {
				return err
			}
			switch out {
			case Inserted:
				res.New++
			case Updated:
				res.Changed++
			default:
				res.Unchanged++
			}
			if out != Unchanged {
				res.Touched = append(res.Touched, it)
				res.Outcomes = append(res.Outcomes, out)
			}
		}
		return putCursor(ctx, tx, pc.Cursor)
	})
	return res, err
}

// MarkGoneNotSeenSince marks items of an integration not fetched since `since` as gone (full-sweep adapters).
func (s *Store) MarkGoneNotSeenSince(ctx context.Context, integrationID string, since int64) (int, error) {
	var n int64
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE external_items SET gone=1, version=version+1 WHERE integration_id=? AND gone=0 AND fetched_at<?`, integrationID, since)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}

// GetItem returns one item by its id.
func (s *Store) GetItem(ctx context.Context, id string) (Item, error) {
	it, err := scanItem(s.DB.Reader().QueryRowContext(ctx, `SELECT `+itemCols+` FROM external_items WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return it, fmt.Errorf("%w: item %s", ErrNotFound, id)
	}
	return it, err
}

// GetItemByExternal returns one item by (integration, external id).
func (s *Store) GetItemByExternal(ctx context.Context, integrationID, externalID string) (Item, error) {
	it, err := scanItem(s.DB.Reader().QueryRowContext(ctx, `SELECT `+itemCols+` FROM external_items WHERE integration_id=? AND external_id=?`, integrationID, externalID))
	if errors.Is(err, sql.ErrNoRows) {
		return it, fmt.Errorf("%w: item %s/%s", ErrNotFound, integrationID, externalID)
	}
	return it, err
}

// ItemByTask returns the item a task was created from.
func (s *Store) ItemByTask(ctx context.Context, taskID string) (Item, error) {
	it, err := scanItem(s.DB.Reader().QueryRowContext(ctx, `SELECT `+itemCols+` FROM external_items WHERE task_id=? LIMIT 1`, taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return it, fmt.Errorf("%w: no external item for task %s", ErrNotFound, taskID)
	}
	return it, err
}

// ListItems returns items newest first.
func (s *Store) ListItems(ctx context.Context, f ItemFilter) ([]Item, error) {
	q := `SELECT ` + itemCols + ` FROM external_items WHERE 1=1`
	var args []any
	if f.IntegrationID != "" {
		q += ` AND integration_id=?`
		args = append(args, f.IntegrationID)
	}
	if f.Kind != "" {
		q += ` AND kind=?`
		args = append(args, f.Kind)
	}
	if f.State != "" {
		q += ` AND state=?`
		args = append(args, f.State)
	}
	if f.Imported != nil {
		if *f.Imported {
			q += ` AND task_id<>''`
		} else {
			q += ` AND task_id=''`
		}
	}
	if f.Gone == nil {
		q += ` AND gone=0`
	} else {
		q += ` AND gone=?`
		args = append(args, b2i(*f.Gone))
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q += ` ORDER BY external_updated_at DESC, id LIMIT ? OFFSET ?`
	args = append(args, limit, f.Offset)
	rows, err := s.DB.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// CountItems counts the stored, not-gone items of a connection.
func (s *Store) CountItems(ctx context.Context, integrationID string) (int, error) {
	var n int
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM external_items WHERE integration_id=? AND gone=0`, integrationID).Scan(&n)
	return n, err
}

// ClaimItem links a task to an item exactly once: it succeeds only while the item has no task.
func (s *Store) ClaimItem(ctx context.Context, itemID, taskID, by string) (bool, error) {
	var n int64
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE external_items SET task_id=?, imported_by=?, imported_at=?, version=version+1 WHERE id=? AND task_id=''`, taskID, by, s.now(), itemID)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return n == 1, err
}

// ---- rules ----

// Rule is a stored, human-authored task rule.
type Rule struct {
	ID            string         `json:"id"`
	IntegrationID string         `json:"integrationId"`
	ProjectID     string         `json:"projectId"`
	Name          string         `json:"name"`
	When          string         `json:"when"`
	Action        string         `json:"action"`
	Params        map[string]any `json:"params"`
	MaxPerDay     int            `json:"maxPerDay"`
	DryRun        bool           `json:"dryRun"`
	Enabled       bool           `json:"enabled"`
	CreatedBy     string         `json:"createdBy"`
	UpdatedBy     string         `json:"updatedBy"`
	Created       int64          `json:"created"`
	Updated       int64          `json:"updated"`
	Version       int64          `json:"version"`
}

// RuleMatch records what a rule did with an item.
type RuleMatch struct {
	RuleID  string `json:"ruleId"`
	ItemID  string `json:"itemId"`
	Outcome string `json:"outcome"`
	Detail  string `json:"detail"`
	FirstAt int64  `json:"firstAt"`
	LastAt  int64  `json:"lastAt"`
}

const ruleCols = `id,integration_id,project_id,name,when_cel,action,params_json,max_per_day,dry_run,enabled,created_by,updated_by,created_at,updated_at,version`

func scanRule(r rowScanner) (Rule, error) {
	var ru Rule
	var params string
	var dry, en int
	if err := r.Scan(&ru.ID, &ru.IntegrationID, &ru.ProjectID, &ru.Name, &ru.When, &ru.Action, &params, &ru.MaxPerDay, &dry, &en, &ru.CreatedBy, &ru.UpdatedBy, &ru.Created, &ru.Updated, &ru.Version); err != nil {
		return ru, err
	}
	ru.DryRun, ru.Enabled = dry != 0, en != 0
	ru.Params = map[string]any{}
	_ = json.Unmarshal([]byte(params), &ru.Params)
	return ru, nil
}

// InsertRule stores a new rule.
func (s *Store) InsertRule(ctx context.Context, r Rule) (Rule, error) {
	r.ID = ids.New("rule")
	r.Created, r.Updated, r.Version = s.now(), s.now(), 1
	pb, _ := json.Marshal(r.Params)
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO integration_rules(`+ruleCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.ID, r.IntegrationID, r.ProjectID, r.Name, r.When, r.Action, string(pb), r.MaxPerDay, b2i(r.DryRun), b2i(r.Enabled), r.CreatedBy, r.UpdatedBy, r.Created, r.Updated, r.Version)
		return err
	})
	return r, err
}

// GetRule returns one rule.
func (s *Store) GetRule(ctx context.Context, id string) (Rule, error) {
	r, err := scanRule(s.DB.Reader().QueryRowContext(ctx, `SELECT `+ruleCols+` FROM integration_rules WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("%w: rule %s", ErrNotFound, id)
	}
	return r, err
}

// ListRules returns the rules of an integration ("" = all).
func (s *Store) ListRules(ctx context.Context, integrationID string) ([]Rule, error) {
	q, args := `SELECT `+ruleCols+` FROM integration_rules`, []any{}
	if integrationID != "" {
		q += ` WHERE integration_id=?`
		args = append(args, integrationID)
	}
	rows, err := s.DB.Reader().QueryContext(ctx, q+` ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateRule replaces a rule with a compare-and-swap on version.
func (s *Store) UpdateRule(ctx context.Context, r Rule, expectVersion int64) (Rule, error) {
	pb, _ := json.Marshal(r.Params)
	var n int64
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE integration_rules SET project_id=?, name=?, when_cel=?, action=?, params_json=?, max_per_day=?, dry_run=?, enabled=?, updated_by=?, updated_at=?, version=version+1
			WHERE id=? AND version=?`, r.ProjectID, r.Name, r.When, r.Action, string(pb), r.MaxPerDay, b2i(r.DryRun), b2i(r.Enabled), r.UpdatedBy, s.now(), r.ID, expectVersion)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	if err != nil {
		return r, err
	}
	if n == 0 {
		return r, ErrConflict
	}
	return s.GetRule(ctx, r.ID)
}

// DeleteRule removes a rule and its match history (CAS on version).
func (s *Store) DeleteRule(ctx context.Context, id string, expectVersion int64) error {
	var n int64
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM integration_rules WHERE id=? AND version=?`, id, expectVersion)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		if n > 0 {
			_, err = tx.ExecContext(ctx, `DELETE FROM integration_rule_matches WHERE rule_id=?`, id)
		}
		return err
	})
	if err == nil && n == 0 {
		return ErrConflict
	}
	return err
}

// RecordMatch upserts the outcome of a rule for an item.
func (s *Store) RecordMatch(ctx context.Context, m RuleMatch) error {
	now := s.now()
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO integration_rule_matches(rule_id,item_id,outcome,detail,first_at,last_at) VALUES(?,?,?,?,?,?)
			ON CONFLICT(rule_id,item_id) DO UPDATE SET outcome=excluded.outcome, detail=excluded.detail, last_at=excluded.last_at`, m.RuleID, m.ItemID, m.Outcome, m.Detail, now, now)
		return err
	})
}

// Matches lists a rule's recorded outcomes, newest first.
func (s *Store) Matches(ctx context.Context, ruleID string, limit int) ([]RuleMatch, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT rule_id,item_id,outcome,detail,first_at,last_at FROM integration_rule_matches WHERE rule_id=? ORDER BY last_at DESC, item_id LIMIT ?`, ruleID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RuleMatch{}
	for rows.Next() {
		var m RuleMatch
		if err := rows.Scan(&m.RuleID, &m.ItemID, &m.Outcome, &m.Detail, &m.FirstAt, &m.LastAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountRuleTasks counts the tasks a rule created at or after since.
func (s *Store) CountRuleTasks(ctx context.Context, ruleID string, since int64) (int, error) {
	var n int
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM external_items WHERE imported_by=? AND imported_at>=?`, "rule:"+ruleID, since).Scan(&n)
	return n, err
}

// ---- outbox ----

// Outbox is one proposed outbound write.
type Outbox struct {
	ID              string         `json:"id"`
	IntegrationID   string         `json:"integrationId"`
	ProjectID       string         `json:"projectId"`
	TaskID          string         `json:"taskId,omitempty"`
	Op              string         `json:"op"`
	Target          string         `json:"target"`
	Tool            string         `json:"tool"`
	Payload         map[string]any `json:"payload"`
	PayloadHash     string         `json:"payloadHash"`
	Preview         string         `json:"preview"`
	ApprovalID      string         `json:"approvalId"`
	Status          string         `json:"status"`
	IdempotencyKey  string         `json:"idempotencyKey"`
	CreatedBy       string         `json:"createdBy"`
	Created         int64          `json:"created"`
	DecidedBy       string         `json:"decidedBy,omitempty"`
	SentAt          int64          `json:"sentAt,omitempty"`
	Receipt         string         `json:"receipt,omitempty"`
	Error           string         `json:"error,omitempty"`
	Version         int64          `json:"version"`
	rawPayload      string
}

// Outbox statuses.
const (
	OutPlanned   = "planned"
	OutAwaiting  = "awaiting_approval"
	OutApproved  = "approved"
	OutExecuting = "executing"
	OutSent      = "sent"
	OutFailed    = "failed"
	OutRejected  = "rejected"
	OutExpired   = "expired"
	OutCancelled = "cancelled"
)

const outCols = `id,integration_id,project_id,task_id,op,target_external_id,tool,payload_json,payload_hash,preview,approval_id,status,idempotency_key,created_by,created_at,decided_by,sent_at,receipt,error,version`

func scanOutbox(r rowScanner) (Outbox, error) {
	var o Outbox
	if err := r.Scan(&o.ID, &o.IntegrationID, &o.ProjectID, &o.TaskID, &o.Op, &o.Target, &o.Tool, &o.rawPayload, &o.PayloadHash, &o.Preview, &o.ApprovalID, &o.Status, &o.IdempotencyKey,
		&o.CreatedBy, &o.Created, &o.DecidedBy, &o.SentAt, &o.Receipt, &o.Error, &o.Version); err != nil {
		return o, err
	}
	o.Payload = map[string]any{}
	_ = json.Unmarshal([]byte(o.rawPayload), &o.Payload)
	return o, nil
}

// InsertOutbox stores a proposal. When a live row with the same idempotency key exists it is returned instead (existing=true).
func (s *Store) InsertOutbox(ctx context.Context, o Outbox) (out Outbox, existing bool, err error) {
	pb := []byte(o.rawPayload) // the exact bytes the hash was computed over
	if len(pb) == 0 {
		pb, _ = json.Marshal(o.Payload)
	}
	o.ID = ids.New("out")
	o.Created, o.Version = s.now(), 1
	err = s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		existing = false
		var id string
		qerr := tx.QueryRowContext(ctx, `SELECT id FROM integration_outbox WHERE idempotency_key=? AND status IN ('planned','awaiting_approval','approved','executing','sent')`, o.IdempotencyKey).Scan(&id)
		if qerr == nil {
			ex, e := scanOutbox(tx.QueryRowContext(ctx, `SELECT `+outCols+` FROM integration_outbox WHERE id=?`, id))
			out, existing = ex, true
			return e
		}
		if !errors.Is(qerr, sql.ErrNoRows) {
			return qerr
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO integration_outbox(`+outCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			o.ID, o.IntegrationID, o.ProjectID, o.TaskID, o.Op, o.Target, o.Tool, string(pb), o.PayloadHash, o.Preview, o.ApprovalID, o.Status, o.IdempotencyKey, o.CreatedBy, o.Created, "", 0, "", "", 1)
		out = o
		return e
	})
	return out, existing, err
}

// GetOutbox returns one proposal.
func (s *Store) GetOutbox(ctx context.Context, id string) (Outbox, error) {
	o, err := scanOutbox(s.DB.Reader().QueryRowContext(ctx, `SELECT `+outCols+` FROM integration_outbox WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return o, fmt.Errorf("%w: outbound action %s", ErrNotFound, id)
	}
	return o, err
}

// ListOutbox returns proposals newest first.
func (s *Store) ListOutbox(ctx context.Context, integrationID, status string, limit int) ([]Outbox, error) {
	q, args := `SELECT `+outCols+` FROM integration_outbox WHERE 1=1`, []any{}
	if integrationID != "" {
		q += ` AND integration_id=?`
		args = append(args, integrationID)
	}
	if status != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.Reader().QueryContext(ctx, q+` ORDER BY created_at DESC, id LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Outbox{}
	for rows.Next() {
		o, err := scanOutbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// OutboxUpdate is a compare-and-swap on status: the row changes only while its status is one of from.
type OutboxUpdate struct {
	From      []string
	To        string
	ApprovalID *string
	DecidedBy *string
	SentAt    *int64
	Receipt   *string
	Error     *string
}

// UpdateOutbox applies u and reports whether the row was in an allowed state.
func (s *Store) UpdateOutbox(ctx context.Context, id string, u OutboxUpdate) (bool, error) {
	set, args := []string{"status=?", "version=version+1"}, []any{u.To}
	if u.ApprovalID != nil {
		set, args = append(set, "approval_id=?"), append(args, *u.ApprovalID)
	}
	if u.DecidedBy != nil {
		set, args = append(set, "decided_by=?"), append(args, *u.DecidedBy)
	}
	if u.SentAt != nil {
		set, args = append(set, "sent_at=?"), append(args, *u.SentAt)
	}
	if u.Receipt != nil {
		set, args = append(set, "receipt=?"), append(args, *u.Receipt)
	}
	if u.Error != nil {
		set, args = append(set, "error=?"), append(args, *u.Error)
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(u.From)), ",")
	args = append(args, id)
	for _, f := range u.From {
		args = append(args, f)
	}
	var n int64
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE integration_outbox SET `+strings.Join(set, ", ")+` WHERE id=? AND status IN (`+ph+`)`, args...)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return n == 1, err
}

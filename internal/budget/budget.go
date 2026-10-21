package budget

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Scope is what a budget limits.
type Scope string

const (
	ScopeProject Scope = "project"
	ScopeTask    Scope = "task"
	ScopeRun     Scope = "run"
	ScopeAgent   Scope = "agent"
)

// Any as a ScopeID makes the budget a ceiling for every task, run or agent (each judged on its own), optionally limited to
// one project through Budget.ProjectID. A project budget cannot be a template.
const Any = "*"

// Budget is a stored limit. A nil limit means that dimension is not limited.
type Budget struct {
	ID          string   `json:"id"`
	Scope       Scope    `json:"scope"`
	ScopeID     string   `json:"scopeId"`
	ProjectID   string   `json:"projectId,omitempty"`
	LimitUSD    *float64 `json:"limitUsd,omitempty"`
	LimitTokens *int64   `json:"limitTokens,omitempty"`
	LimitTurns  *int64   `json:"limitTurns,omitempty"`
	WarnPct     float64  `json:"warnPct"`
	Enabled     bool     `json:"enabled"`
	Note        string   `json:"note,omitempty"`
	Version     int64    `json:"version"`
	CreatedAt   int64    `json:"createdAt"`
	UpdatedAt   int64    `json:"updatedAt"`
	UpdatedBy   string   `json:"updatedBy"`
}

// Errors.
var (
	ErrNotHuman = errors.New("budget: only a human: identity may change a budget")
	ErrConflict = errors.New("budget: the budget changed since you read it (version mismatch)")
	ErrNotFound = errors.New("budget: not found")
	ErrInvalid  = errors.New("budget: invalid budget")
)

// Store persists budgets.
type Store struct{ DB *storage.DB }

// IsHuman reports whether an identity may change budgets or approve a pause.
func IsHuman(by string) bool { return strings.HasPrefix(by, "human:") && len(by) > len("human:") }

func (b *Budget) normalize() error {
	switch b.Scope {
	case ScopeProject, ScopeTask, ScopeRun, ScopeAgent:
	default:
		return fmt.Errorf("%w: scope must be project, task, run or agent", ErrInvalid)
	}
	b.ScopeID = strings.TrimSpace(b.ScopeID)
	if b.ScopeID == "" {
		return fmt.Errorf("%w: scopeId is required (an id, or %q for every %s)", ErrInvalid, Any, b.Scope)
	}
	if b.ScopeID == Any && b.Scope == ScopeProject {
		return fmt.Errorf("%w: a project budget needs a project id", ErrInvalid)
	}
	if b.ScopeID != Any {
		b.ProjectID = "" // an explicit id needs no project filter
	}
	if b.LimitUSD == nil && b.LimitTokens == nil && b.LimitTurns == nil {
		return fmt.Errorf("%w: set at least one of limitUsd, limitTokens, limitTurns", ErrInvalid)
	}
	if b.LimitUSD != nil && *b.LimitUSD <= 0 {
		return fmt.Errorf("%w: limitUsd must be positive", ErrInvalid)
	}
	if b.LimitTokens != nil && *b.LimitTokens <= 0 {
		return fmt.Errorf("%w: limitTokens must be positive", ErrInvalid)
	}
	if b.LimitTurns != nil && *b.LimitTurns <= 0 {
		return fmt.Errorf("%w: limitTurns must be positive", ErrInvalid)
	}
	if b.WarnPct == 0 {
		b.WarnPct = 0.8
	}
	if b.WarnPct <= 0 || b.WarnPct >= 1 {
		return fmt.Errorf("%w: warnPct must be between 0 and 1 (exclusive)", ErrInvalid)
	}
	return nil
}

const cols = `id,scope,scope_id,project_id,limit_usd,limit_tokens,limit_turns,warn_pct,enabled,note,version,created_at,updated_at,updated_by`

type rowScanner interface{ Scan(dest ...any) error }

func scan(r rowScanner) (Budget, error) {
	var b Budget
	var usd sql.NullFloat64
	var tok, turns sql.NullInt64
	var en int
	if err := r.Scan(&b.ID, &b.Scope, &b.ScopeID, &b.ProjectID, &usd, &tok, &turns, &b.WarnPct, &en, &b.Note, &b.Version, &b.CreatedAt, &b.UpdatedAt, &b.UpdatedBy); err != nil {
		return Budget{}, err
	}
	if usd.Valid {
		v := usd.Float64
		b.LimitUSD = &v
	}
	if tok.Valid {
		v := tok.Int64
		b.LimitTokens = &v
	}
	if turns.Valid {
		v := turns.Int64
		b.LimitTurns = &v
	}
	b.Enabled = en != 0
	return b, nil
}

// Get returns one budget.
func (s *Store) Get(ctx context.Context, id string) (Budget, error) {
	b, err := scan(s.DB.Reader().QueryRowContext(ctx, `SELECT `+cols+` FROM budgets WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Budget{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return b, err
}

// Filter narrows List.
type Filter struct {
	Scope     Scope
	ScopeID   string
	ProjectID string
}

// List returns budgets, project scope first.
func (s *Store) List(ctx context.Context, f Filter) ([]Budget, error) {
	// a project filter keeps the budgets that belong to the project: its own, its ceiling templates, and those on its
	// tasks, runs and agents
	q := `SELECT ` + cols + ` FROM budgets WHERE (?='' OR scope=?) AND (?='' OR scope_id=?) AND (?='' OR project_id=?
		OR (scope='project' AND scope_id=?)
		OR (scope='task' AND scope_id IN (SELECT id FROM tasks WHERE project_id=?))
		OR (scope='agent' AND scope_id IN (SELECT id FROM agents WHERE project_id=?))
		OR (scope='run' AND scope_id IN (SELECT id FROM workflow_runs WHERE project_id=?)))
		ORDER BY CASE scope WHEN 'project' THEN 0 WHEN 'task' THEN 1 WHEN 'run' THEN 2 ELSE 3 END, scope_id, id`
	rows, err := s.DB.Reader().QueryContext(ctx, q, string(f.Scope), string(f.Scope), f.ScopeID, f.ScopeID, f.ProjectID, f.ProjectID, f.ProjectID, f.ProjectID, f.ProjectID, f.ProjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Budget{}
	for rows.Next() {
		b, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Set creates a budget (ID empty) or updates one by compare-and-swap on Version. A change to any limit or the warn
// percentage clears the budget's fired alerts so the thresholds arm again. The caller is a human or nothing happens.
func (s *Store) Set(ctx context.Context, b Budget, by string) (Budget, error) {
	if !IsHuman(by) {
		return Budget{}, ErrNotHuman
	}
	if err := b.normalize(); err != nil {
		return Budget{}, err
	}
	now := storage.Now()
	b.UpdatedAt, b.UpdatedBy = now, by
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if b.ID == "" {
			b.ID, b.Version, b.CreatedAt = ids.New("bud"), 1, now
			_, err := tx.ExecContext(ctx, `INSERT INTO budgets(`+cols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				b.ID, b.Scope, b.ScopeID, b.ProjectID, b.LimitUSD, b.LimitTokens, b.LimitTurns, b.WarnPct, boolInt(b.Enabled), b.Note, b.Version, b.CreatedAt, b.UpdatedAt, b.UpdatedBy)
			if err != nil && strings.Contains(err.Error(), "UNIQUE") {
				return fmt.Errorf("%w: a %s budget for %q already exists; update it instead", ErrInvalid, b.Scope, b.ScopeID)
			}
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE budgets SET scope=?,scope_id=?,project_id=?,limit_usd=?,limit_tokens=?,limit_turns=?,warn_pct=?,enabled=?,note=?,
			version=version+1,updated_at=?,updated_by=? WHERE id=? AND version=?`,
			b.Scope, b.ScopeID, b.ProjectID, b.LimitUSD, b.LimitTokens, b.LimitTurns, b.WarnPct, boolInt(b.Enabled), b.Note, b.UpdatedAt, b.UpdatedBy, b.ID, b.Version)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM budgets WHERE id=?`, b.ID).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				return fmt.Errorf("%w: %s", ErrNotFound, b.ID)
			}
			return ErrConflict
		}
		b.Version++
		_, err = tx.ExecContext(ctx, `DELETE FROM budget_alerts WHERE budget_id=?`, b.ID)
		return err
	})
	if err != nil {
		return Budget{}, err
	}
	return s.Get(ctx, b.ID)
}

// Delete removes a budget.
func (s *Store) Delete(ctx context.Context, id, by string) error {
	if !IsHuman(by) {
		return ErrNotHuman
	}
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM budgets WHERE id=?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil
	})
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

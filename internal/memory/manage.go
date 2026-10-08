package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is returned for an unknown entry.
var ErrNotFound = errors.New("memory: entry not found")

// ValidKind reports whether k is one of the memory kinds.
func ValidKind(k string) bool {
	switch k {
	case Decision, Convention, APIContract, Rejected, KnownIssue, Preference:
		return true
	}
	return false
}

// Get returns one entry.
func (s Store) Get(ctx context.Context, id string) (Entry, error) {
	rows, err := s.query(ctx, `SELECT id,project_id,kind,title,body,status,source,created_at,updated_at FROM memory WHERE id=?`, id)
	if err != nil {
		return Entry{}, err
	}
	if len(rows) == 0 {
		return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return rows[0], nil
}

// Delete removes an entry for good (the FTS index follows through its trigger). Generated instruction files stop mentioning
// it the next time they are generated.
func (s Store) Delete(ctx context.Context, id string) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM memory WHERE id=?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil
	})
}

// Find is Search for the memory page: any status (empty means every status), or every project when projectID is empty
// (workspace scope). Search, used by agents, stays limited to accepted entries of one project.
func (s Store) Find(ctx context.Context, projectID, query, status string, k int) ([]Entry, error) {
	if k <= 0 {
		k = 20
	}
	match := ftsQuery(query)
	if match == "" {
		return nil, nil
	}
	q := `SELECT m.id,m.project_id,m.kind,m.title,m.body,m.status,m.source,m.created_at,m.updated_at
		FROM memory_fts f JOIN memory m ON m.rowid=f.rowid WHERE memory_fts MATCH ?`
	args := []any{match}
	if projectID != "" {
		q += ` AND m.project_id=?`
		args = append(args, projectID)
	}
	if status != "" {
		q += ` AND m.status=?`
		args = append(args, status)
	}
	q += ` ORDER BY bm25(memory_fts, 4.0, 1.0) LIMIT ?`
	args = append(args, k)
	return s.query(ctx, q, args...)
}

// SearchWorkspace searches accepted entries across all projects (the search_memory tool's workspace scope).
func (s Store) SearchWorkspace(ctx context.Context, query string, k int) ([]Entry, error) {
	if k <= 0 {
		k = 5
	}
	return s.Find(ctx, "", query, Accepted, k)
}

// Normalize trims a proposal's text fields.
func Normalize(e Entry) Entry {
	e.Title = strings.TrimSpace(e.Title)
	e.Body = strings.TrimSpace(e.Body)
	return e
}

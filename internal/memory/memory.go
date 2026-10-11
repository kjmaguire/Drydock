// Package memory is project memory: architecture decisions, conventions, API contracts, rejected approaches, known
// issues and preferences. Accepted entries go into the generated instruction files up to a budget, newest first
// (internal/context.MemoryBudget); the rest is queried on demand with search_memory. Entries start as proposals; a human
// accepts them.
package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/fts"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Kinds.
const (
	Decision    = "decision"
	Convention  = "convention"
	APIContract = "api_contract"
	Rejected    = "rejected"
	KnownIssue  = "known_issue"
	Preference  = "preference"
)

// Statuses.
const (
	Proposed   = "proposed"
	Accepted   = "accepted"
	RejectedSt = "rejected"
	Superseded = "superseded"
)

// Entry is one memory item.
type Entry struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Status    string `json:"status"`
	Source    string `json:"source,omitempty"`
	Created   int64  `json:"created"`
	Updated   int64  `json:"updated"`
}

// Store persists memory.
type Store struct{ DB *storage.DB }

// Add creates an entry (status "proposed" unless accepted is true).
func (s Store) Add(ctx context.Context, e Entry, accepted bool) (Entry, error) {
	if strings.TrimSpace(e.Title) == "" || strings.TrimSpace(e.Body) == "" {
		return e, errors.New("memory: title and body are required")
	}
	e.ID = ids.New("mem")
	e.Status = Proposed
	if accepted {
		e.Status = Accepted
	}
	e.Created, e.Updated = storage.Now(), storage.Now()
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO memory(id,project_id,kind,title,body,status,source,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			e.ID, e.ProjectID, e.Kind, e.Title, e.Body, e.Status, e.Source, e.Created, e.Updated)
		return err
	})
	return e, err
}

// SetStatus accepts, rejects or supersedes an entry.
func (s Store) SetStatus(ctx context.Context, id, status string) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE memory SET status=?, updated_at=?, version=version+1 WHERE id=?`, status, storage.Now(), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("memory: %s not found", id)
		}
		return nil
	})
}

// List returns entries, optionally filtered by status and kind, oldest first (stable for cache-friendly prompts).
func (s Store) List(ctx context.Context, projectID, status, kind string) ([]Entry, error) {
	q := `SELECT id,project_id,kind,title,body,status,source,created_at,updated_at FROM memory WHERE project_id=?`
	args := []any{projectID}
	if status != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	if kind != "" {
		q += ` AND kind=?`
		args = append(args, kind)
	}
	q += ` ORDER BY created_at, id`
	return s.query(ctx, q, args...)
}

// Search runs a full-text query over accepted entries with a top-k limit (retrieve, don't preload).
func (s Store) Search(ctx context.Context, projectID, query string, k int) ([]Entry, error) {
	if k <= 0 {
		k = 5
	}
	match := ftsQuery(query)
	if match == "" {
		return nil, nil
	}
	return s.query(ctx, `SELECT m.id,m.project_id,m.kind,m.title,m.body,m.status,m.source,m.created_at,m.updated_at
		FROM memory_fts f JOIN memory m ON m.rowid=f.rowid
		WHERE memory_fts MATCH ? AND m.project_id=? AND m.status='accepted' ORDER BY bm25(memory_fts, 4.0, 1.0) LIMIT ?`, match, projectID, k)
}

// ftsQuery turns free text into a safe FTS5 query: any of its terms, common words left out (fts.BuildMatch, as the expert
// packs search).
func ftsQuery(q string) string {
	_, anyTerm, _ := fts.BuildMatch(q)
	return anyTerm
}

func (s Store) query(ctx context.Context, q string, args ...any) ([]Entry, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.Kind, &e.Title, &e.Body, &e.Status, &e.Source, &e.Created, &e.Updated); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

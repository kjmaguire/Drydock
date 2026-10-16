package integrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kjmaguire/drydock/internal/ids"
)

// Assignment gives a workspace connection to one Drydock project, with that project's remote scope (decision P42): which
// Linear team or project, which Notion data source, which Sentry organization and project, which Aikido repository, which
// GitHub repository. One connection can be assigned to several projects; each assignment has its own cursor and items.
type Assignment struct {
	ID            string            `json:"id"`
	IntegrationID string            `json:"integrationId"`
	ProjectID     string            `json:"projectId"`
	Config        map[string]string `json:"config"`
	Enabled       bool              `json:"enabled"`
	CreatedBy     string            `json:"createdBy"`
	Created       int64             `json:"created"`
	Updated       int64             `json:"updated"`
	Version       int64             `json:"version"`
}

const asgCols = `id,integration_id,project_id,config_json,enabled,created_by,created_at,updated_at,version`

func scanAssignment(r rowScanner) (Assignment, error) {
	var a Assignment
	var cfg string
	var en int
	if err := r.Scan(&a.ID, &a.IntegrationID, &a.ProjectID, &cfg, &en, &a.CreatedBy, &a.Created, &a.Updated, &a.Version); err != nil {
		return a, err
	}
	a.Enabled = en != 0
	a.Config = map[string]string{}
	_ = json.Unmarshal([]byte(cfg), &a.Config)
	return a, nil
}

// InsertAssignment stores a new assignment and marks the connection as assigned (legacy_adopted). adoptLegacy moves the
// connection's project-less items, its pre-P42 cursor and its project-less rules to it (the first assignment ever of a
// connection migrated without a project), in the same transaction.
func (s *Store) InsertAssignment(ctx context.Context, a Assignment, adoptLegacy bool) (Assignment, error) {
	if a.ID == "" {
		a.ID = ids.New("asg")
	}
	a.Created, a.Updated, a.Version = s.now(), s.now(), 1
	cfg, _ := json.Marshal(nz(a.Config))
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM integration_assignments WHERE integration_id=? AND project_id=?`, a.IntegrationID, a.ProjectID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return errf(ErrConflict, "this connection is already assigned to the project")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO integration_assignments(`+asgCols+`) VALUES(?,?,?,?,?,?,?,?,?)`,
			a.ID, a.IntegrationID, a.ProjectID, string(cfg), b2i(a.Enabled), a.CreatedBy, a.Created, a.Updated, a.Version); err != nil {
			return err
		}
		// the connection has been assigned: whatever it held from before P42 is adopted now or never
		if _, err := tx.ExecContext(ctx, `UPDATE integrations SET legacy_adopted=1 WHERE id=?`, a.IntegrationID); err != nil {
			return err
		}
		if adoptLegacy {
			if _, err := tx.ExecContext(ctx, `UPDATE external_items SET assignment_id=?, project_id=? WHERE integration_id=? AND assignment_id=''`, a.ID, a.ProjectID, a.IntegrationID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE integration_cursors SET scope=? WHERE integration_id=? AND scope=?`, a.ID, a.IntegrationID, defaultScope); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE integration_rules SET project_id=? WHERE integration_id=? AND project_id=''`, a.ProjectID, a.IntegrationID); err != nil {
				return err
			}
		}
		return nil
	})
	return a, err
}

// GetAssignment returns one assignment.
func (s *Store) GetAssignment(ctx context.Context, id string) (Assignment, error) {
	a, err := scanAssignment(s.DB.Reader().QueryRowContext(ctx, `SELECT `+asgCols+` FROM integration_assignments WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, fmt.Errorf("%w: assignment %s", ErrNotFound, id)
	}
	return a, err
}

// AssignmentFor returns the assignment of a connection to a project.
func (s *Store) AssignmentFor(ctx context.Context, integrationID, projectID string) (Assignment, error) {
	a, err := scanAssignment(s.DB.Reader().QueryRowContext(ctx, `SELECT `+asgCols+` FROM integration_assignments WHERE integration_id=? AND project_id=?`, integrationID, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return a, fmt.Errorf("%w: %s is not assigned to project %s", ErrNotFound, integrationID, projectID)
	}
	return a, err
}

// ListAssignments returns the assignments of a connection ("" = every connection), oldest first.
func (s *Store) ListAssignments(ctx context.Context, integrationID string) ([]Assignment, error) {
	q, args := `SELECT `+asgCols+` FROM integration_assignments`, []any{}
	if integrationID != "" {
		q += ` WHERE integration_id=?`
		args = append(args, integrationID)
	}
	rows, err := s.DB.Reader().QueryContext(ctx, q+` ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Assignment{}
	for rows.Next() {
		a, err := scanAssignment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateAssignment replaces an assignment's scope and enabled flag (compare-and-swap on version). A changed scope restarts
// the assignment's cursor: the next sync reads the new scope from the start.
func (s *Store) UpdateAssignment(ctx context.Context, a Assignment, expectVersion int64, scopeChanged bool) (Assignment, error) {
	cfg, _ := json.Marshal(nz(a.Config))
	var n int64
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE integration_assignments SET config_json=?, enabled=?, updated_at=?, version=version+1 WHERE id=? AND version=?`,
			string(cfg), b2i(a.Enabled), s.now(), a.ID, expectVersion)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		if n == 1 && scopeChanged {
			// the old scope's items are no longer this project's: those without a task go, the others stay (gone) with their task
			for _, q := range []string{`DELETE FROM integration_cursors WHERE integration_id=? AND scope=?`,
				`DELETE FROM external_items WHERE integration_id=? AND assignment_id=? AND task_id=''`,
				`UPDATE external_items SET gone=1 WHERE integration_id=? AND assignment_id=?`} {
				if _, err := tx.ExecContext(ctx, q, a.IntegrationID, a.ID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return a, err
	}
	if n == 0 {
		return a, ErrConflict
	}
	return s.GetAssignment(ctx, a.ID)
}

// DeleteAssignment removes an assignment, its cursor, the rules of its project for this connection, and its items that never
// became a task. Items that became a task stay (marked gone) so the task keeps its provenance.
func (s *Store) DeleteAssignment(ctx context.Context, a Assignment) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, st := range []struct {
			q    string
			args []any
		}{
			{`DELETE FROM integration_cursors WHERE integration_id=? AND scope=?`, []any{a.IntegrationID, a.ID}},
			{`DELETE FROM integration_rule_matches WHERE rule_id IN (SELECT id FROM integration_rules WHERE integration_id=? AND project_id=?)`, []any{a.IntegrationID, a.ProjectID}},
			{`DELETE FROM integration_rules WHERE integration_id=? AND project_id=?`, []any{a.IntegrationID, a.ProjectID}},
			{`DELETE FROM external_items WHERE assignment_id=? AND task_id=''`, []any{a.ID}},
			{`UPDATE external_items SET gone=1 WHERE assignment_id=?`, []any{a.ID}},
			{`DELETE FROM integration_assignments WHERE id=?`, []any{a.ID}},
		} {
			if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
				return err
			}
		}
		return nil
	})
}

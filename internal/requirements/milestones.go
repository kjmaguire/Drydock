package requirements

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/security"
)

// Milestone groups tasks toward a date (brief section 21, Project section).
type Milestone struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"projectId"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	DueAt       time.Time `json:"dueAt,omitempty"` // zero: no date
	Status      string    `json:"status"`          // open | closed
	CreatedBy   string    `json:"createdBy,omitempty"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
	Closed      time.Time `json:"closed,omitempty"`
	TaskIDs     []string  `json:"taskIds"`
	Progress    Progress  `json:"progress"`
	Overdue     bool      `json:"overdue"`
}

// Progress counts a milestone's tasks by state. Percent is completed tasks over non-cancelled tasks.
type Progress struct {
	Total      int `json:"total"`
	Completed  int `json:"completed"`
	InProgress int `json:"inProgress"`
	Blocked    int `json:"blocked"`
	Open       int `json:"open"` // backlog, ready, claimed
	Cancelled  int `json:"cancelled"`
	Failed     int `json:"failed"`
	Percent    int `json:"percent"`
}

// MilestoneReq creates or patches a milestone. DueAt zero leaves/sets no date; ClearDue removes it.
type MilestoneReq struct {
	ProjectID   string     `json:"projectId"`
	Title       string     `json:"title"`
	Description *string    `json:"description,omitempty"`
	DueAt       *time.Time `json:"dueAt,omitempty"`
	ClearDue    bool       `json:"clearDue,omitempty"`
	TaskIDs     []string   `json:"taskIds,omitempty"`
}

// CreateMilestone creates an open milestone. Human only.
func (s *Service) CreateMilestone(ctx context.Context, by string, r MilestoneReq) (Milestone, error) {
	if err := security.RequireHuman(by); err != nil {
		return Milestone{}, err
	}
	if strings.TrimSpace(r.Title) == "" || len(r.Title) > 200 {
		return Milestone{}, errors.New("requirements: a milestone needs a title of at most 200 characters")
	}
	id := ids.New("ms")
	now := s.now().UnixMilli()
	var due int64
	if r.DueAt != nil && !r.DueAt.IsZero() {
		due = r.DueAt.UnixMilli()
	}
	desc := ""
	if r.Description != nil {
		desc = *r.Description
	}
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=?`, r.ProjectID).Scan(&n); err != nil || n == 0 {
			return fmt.Errorf("requirements: project %q not found", r.ProjectID)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO milestones(id,project_id,title,description,due_at,status,created_by,created_at,updated_at) VALUES(?,?,?,?,?,'open',?,?,?)`,
			id, r.ProjectID, strings.TrimSpace(r.Title), desc, due, by, now, now); err != nil {
			return err
		}
		for _, t := range r.TaskIDs {
			if err := s.addTaskTx(ctx, tx, id, r.ProjectID, t); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Milestone{}, err
	}
	s.emit(ctx, EventMilestoneCreated, r.ProjectID, "", map[string]any{"id": id, "title": r.Title, "tasks": len(r.TaskIDs), "by": by})
	return s.GetMilestone(ctx, id)
}

func (s *Service) addTaskTx(ctx context.Context, tx *sql.Tx, msID, projectID, taskID string) error {
	var tp string
	if err := tx.QueryRowContext(ctx, `SELECT project_id FROM tasks WHERE id=?`, taskID).Scan(&tp); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: task %s", ErrNotFound, taskID)
		}
		return err
	}
	if tp != projectID {
		return fmt.Errorf("requirements: task %s belongs to another project", taskID)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO milestone_tasks(milestone_id,task_id) VALUES(?,?) ON CONFLICT DO NOTHING`, msID, taskID)
	return err
}

// UpdateMilestone patches title, description or due date. Human only.
func (s *Service) UpdateMilestone(ctx context.Context, by, id string, r MilestoneReq) (Milestone, error) {
	if err := security.RequireHuman(by); err != nil {
		return Milestone{}, err
	}
	m, err := s.GetMilestone(ctx, id)
	if err != nil {
		return Milestone{}, err
	}
	title, desc, due := m.Title, m.Description, int64(0)
	if !m.DueAt.IsZero() {
		due = m.DueAt.UnixMilli()
	}
	if t := strings.TrimSpace(r.Title); t != "" {
		if len(t) > 200 {
			return Milestone{}, errors.New("requirements: the title is longer than 200 characters")
		}
		title = t
	}
	if r.Description != nil {
		desc = *r.Description
	}
	if r.ClearDue {
		due = 0
	}
	if r.DueAt != nil && !r.DueAt.IsZero() {
		due = r.DueAt.UnixMilli()
	}
	if err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE milestones SET title=?,description=?,due_at=?,updated_at=? WHERE id=?`, title, desc, due, s.now().UnixMilli(), id)
		return err
	}); err != nil {
		return Milestone{}, err
	}
	s.emit(ctx, EventMilestoneUpdated, m.ProjectID, "", map[string]any{"id": id, "by": by})
	return s.GetMilestone(ctx, id)
}

// SetMilestoneStatus closes or reopens a milestone. Closing with unfinished tasks needs force. Human only.
func (s *Service) SetMilestoneStatus(ctx context.Context, by, id, status string, force bool) (Milestone, error) {
	if err := security.RequireHuman(by); err != nil {
		return Milestone{}, err
	}
	m, err := s.GetMilestone(ctx, id)
	if err != nil {
		return Milestone{}, err
	}
	now := s.now().UnixMilli()
	switch status {
	case "closed":
		unfinished := m.Progress.Total - m.Progress.Completed - m.Progress.Cancelled - m.Progress.Failed
		if unfinished > 0 && !force {
			return m, fmt.Errorf("requirements: %d task(s) are not finished; close with force to accept that", unfinished)
		}
		err = s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE milestones SET status='closed',closed_at=?,updated_at=? WHERE id=?`, now, now, id)
			return err
		})
		if err == nil {
			s.emit(ctx, EventMilestoneClosed, m.ProjectID, "", map[string]any{"id": id, "unfinished": max(unfinished, 0), "by": by})
		}
	case "open":
		err = s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE milestones SET status='open',closed_at=0,updated_at=? WHERE id=?`, now, id)
			return err
		})
		if err == nil {
			s.emit(ctx, EventMilestoneOpened, m.ProjectID, "", map[string]any{"id": id, "by": by})
		}
	default:
		return m, errors.New("requirements: status must be open or closed")
	}
	if err != nil {
		return m, err
	}
	return s.GetMilestone(ctx, id)
}

// SetMilestoneTasks adds and removes tasks. Human only.
func (s *Service) SetMilestoneTasks(ctx context.Context, by, id string, add, remove []string) (Milestone, error) {
	if err := security.RequireHuman(by); err != nil {
		return Milestone{}, err
	}
	m, err := s.GetMilestone(ctx, id)
	if err != nil {
		return Milestone{}, err
	}
	err = s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, t := range add {
			if err := s.addTaskTx(ctx, tx, id, m.ProjectID, t); err != nil {
				return err
			}
		}
		for _, t := range remove {
			if _, err := tx.ExecContext(ctx, `DELETE FROM milestone_tasks WHERE milestone_id=? AND task_id=?`, id, t); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE milestones SET updated_at=? WHERE id=?`, s.now().UnixMilli(), id)
		return err
	})
	if err != nil {
		return Milestone{}, err
	}
	s.emit(ctx, EventMilestoneTask, m.ProjectID, "", map[string]any{"id": id, "added": len(add), "removed": len(remove), "by": by})
	return s.GetMilestone(ctx, id)
}

// DeleteMilestone deletes a milestone (its tasks are untouched). Human only.
func (s *Service) DeleteMilestone(ctx context.Context, by, id string) error {
	if err := security.RequireHuman(by); err != nil {
		return err
	}
	m, err := s.GetMilestone(ctx, id)
	if err != nil {
		return err
	}
	if err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM milestone_tasks WHERE milestone_id=?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM milestones WHERE id=?`, id)
		return err
	}); err != nil {
		return err
	}
	s.emit(ctx, EventMilestoneDeleted, m.ProjectID, "", map[string]any{"id": id, "title": m.Title, "by": by})
	return nil
}

// GetMilestone returns one milestone with its tasks and progress.
func (s *Service) GetMilestone(ctx context.Context, id string) (Milestone, error) {
	var m Milestone
	var due, created, updated, closed int64
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT id,project_id,title,description,due_at,status,created_by,created_at,updated_at,closed_at FROM milestones WHERE id=?`, id).
		Scan(&m.ID, &m.ProjectID, &m.Title, &m.Description, &due, &m.Status, &m.CreatedBy, &created, &updated, &closed)
	if errors.Is(err, sql.ErrNoRows) {
		return m, fmt.Errorf("%w: milestone %s", ErrNotFound, id)
	}
	if err != nil {
		return m, err
	}
	if due > 0 {
		m.DueAt = time.UnixMilli(due).UTC()
	}
	m.Created, m.Updated = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	if closed > 0 {
		m.Closed = time.UnixMilli(closed).UTC()
	}
	m.TaskIDs = []string{}
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT t.id, t.status FROM milestone_tasks mt JOIN tasks t ON t.id=mt.task_id WHERE mt.milestone_id=? ORDER BY t.seq, t.id`, id)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var tid, st string
		if err := rows.Scan(&tid, &st); err != nil {
			return m, err
		}
		m.TaskIDs = append(m.TaskIDs, tid)
		m.Progress.Total++
		switch st {
		case "COMPLETED":
			m.Progress.Completed++
		case "IN_PROGRESS", "REVIEW", "VERIFICATION", "WAITING_HUMAN":
			m.Progress.InProgress++
		case "BLOCKED":
			m.Progress.Blocked++
		case "CANCELLED":
			m.Progress.Cancelled++
		case "FAILED":
			m.Progress.Failed++
		default:
			m.Progress.Open++
		}
	}
	if err := rows.Err(); err != nil {
		return m, err
	}
	if denom := m.Progress.Total - m.Progress.Cancelled; denom > 0 {
		m.Progress.Percent = m.Progress.Completed * 100 / denom
	}
	m.Overdue = m.Status == "open" && !m.DueAt.IsZero() && s.now().After(m.DueAt)
	return m, nil
}

// Milestones lists a project's milestones, open ones first by due date.
func (s *Service) Milestones(ctx context.Context, projectID string) ([]Milestone, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT id FROM milestones WHERE project_id=? ORDER BY status DESC, CASE WHEN due_at=0 THEN 1 ELSE 0 END, due_at, created_at`, projectID)
	if err != nil {
		return nil, err
	}
	var idsList []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		idsList = append(idsList, id)
	}
	rows.Close()
	out := []Milestone{}
	for _, id := range idsList {
		m, err := s.GetMilestone(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

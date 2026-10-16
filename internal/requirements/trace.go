package requirements

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/security"
)

// Link is one acceptance criterion of one task, written against one version of a requirement.
type Link struct {
	ID            int64     `json:"id"`
	RequirementID string    `json:"requirementId"`
	Version       int       `json:"version"`
	TaskID        string    `json:"taskId"`
	Criterion     string    `json:"criterion"`
	CreatedBy     string    `json:"createdBy,omitempty"`
	Created       time.Time `json:"created"`
	// Stale is true when the requirement has moved past the linked version.
	Stale bool `json:"stale"`
}

var gwt = regexp.MustCompile(`(?is)\bgiven\b.+\bwhen\b.+\bthen\b.+`)

// ValidateCriterion checks that a criterion is Given/When/Then (case-insensitive, in that order, each with content).
func ValidateCriterion(c string) error {
	c = strings.TrimSpace(c)
	if c == "" {
		return errors.New("empty criterion")
	}
	if len(c) > 2000 {
		return errors.New("criterion is longer than 2000 characters")
	}
	if !gwt.MatchString(c) {
		return fmt.Errorf("criterion %q is not Given/When/Then", truncate(c, 60))
	}
	return nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

func criterionHash(c string) string {
	h := sha256.Sum256([]byte(strings.Join(strings.Fields(strings.ToLower(c)), " ")))
	return hex.EncodeToString(h[:])
}

// LinkCriteria links Given/When/Then criteria to a task against the requirement's current version (the spec gate calls this
// when the human accepts a spec). Linking the same criterion twice is a no-op. The task must belong to the requirement's
// project. Human only. With no criteria, the task's own acceptance criteria are linked.
func (s *Service) LinkCriteria(ctx context.Context, by, reqID, taskID string, criteria []string) ([]Link, error) {
	if err := security.RequireHuman(by); err != nil {
		return nil, err
	}
	req, err := s.Get(ctx, reqID)
	if err != nil {
		return nil, err
	}
	var taskProject, acceptance string
	if err := s.DB.Reader().QueryRowContext(ctx, `SELECT project_id, acceptance FROM tasks WHERE id=?`, taskID).Scan(&taskProject, &acceptance); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: task %s", ErrNotFound, taskID)
		}
		return nil, err
	}
	if taskProject != req.ProjectID {
		return nil, errors.New("requirements: the task belongs to another project")
	}
	if len(criteria) == 0 {
		_ = json.Unmarshal([]byte(acceptance), &criteria)
		if len(criteria) == 0 {
			return nil, errors.New("requirements: the task has no acceptance criteria to link (write Given/When/Then criteria first)")
		}
	}
	for _, c := range criteria {
		if err := ValidateCriterion(c); err != nil {
			return nil, fmt.Errorf("requirements: %w", err)
		}
	}
	now := s.now().UnixMilli()
	added := 0
	err = s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, c := range criteria {
			c = strings.TrimSpace(c)
			res, err := tx.ExecContext(ctx, `INSERT INTO requirement_links(requirement_id,version,task_id,criterion,criterion_hash,created_by,created_at) VALUES(?,?,?,?,?,?,?)
				ON CONFLICT(requirement_id,task_id,criterion_hash) DO NOTHING`, reqID, req.Version, taskID, c, criterionHash(c), by, now)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				added++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if added > 0 {
		s.emit(ctx, EventLinked, req.ProjectID, taskID, map[string]any{"id": reqID, "task": taskID, "version": req.Version, "criteria": added, "by": by})
	}
	return s.TaskLinks(ctx, taskID)
}

// Unlink removes every link between a requirement and a task. Human only.
func (s *Service) Unlink(ctx context.Context, by, reqID, taskID string) error {
	if err := security.RequireHuman(by); err != nil {
		return err
	}
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM requirement_links WHERE requirement_id=? AND task_id=?`, reqID, taskID)
		return err
	})
}

const linkCols = `l.id,l.requirement_id,l.version,l.task_id,l.criterion,l.created_by,l.created_at,l.version < r.current_version`

func scanLinks(rows *sql.Rows) ([]Link, error) {
	defer rows.Close()
	out := []Link{}
	for rows.Next() {
		var l Link
		var ts int64
		if err := rows.Scan(&l.ID, &l.RequirementID, &l.Version, &l.TaskID, &l.Criterion, &l.CreatedBy, &ts, &l.Stale); err != nil {
			return nil, err
		}
		l.Created = time.UnixMilli(ts).UTC()
		out = append(out, l)
	}
	return out, rows.Err()
}

// TaskLinks lists the criteria linked to a task.
func (s *Service) TaskLinks(ctx context.Context, taskID string) ([]Link, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+linkCols+` FROM requirement_links l JOIN requirements r ON r.id=l.requirement_id WHERE l.task_id=? ORDER BY l.id`, taskID)
	if err != nil {
		return nil, err
	}
	return scanLinks(rows)
}

// Links lists the criteria linked to a requirement.
func (s *Service) Links(ctx context.Context, reqID string) ([]Link, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+linkCols+` FROM requirement_links l JOIN requirements r ON r.id=l.requirement_id WHERE l.requirement_id=? ORDER BY l.task_id, l.id`, reqID)
	if err != nil {
		return nil, err
	}
	return scanLinks(rows)
}

// StaleLinks lists links, project-wide, whose requirement version was superseded: those tasks were specified against text that
// has since changed and need a human look.
func (s *Service) StaleLinks(ctx context.Context, projectID string) ([]Link, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+linkCols+` FROM requirement_links l JOIN requirements r ON r.id=l.requirement_id
		WHERE r.project_id=? AND r.archived=0 AND l.version < r.current_version ORDER BY l.task_id, l.id`, projectID)
	if err != nil {
		return nil, err
	}
	return scanLinks(rows)
}

// EvidenceRef is a piece of task evidence relevant to traceability.
type EvidenceRef struct {
	ID      int64     `json:"id"`
	Kind    string    `json:"kind"`
	Passed  *bool     `json:"passed,omitempty"`
	Summary string    `json:"summary"`
	Ref     string    `json:"ref,omitempty"`
	Attempt int       `json:"attempt"`
	Created time.Time `json:"created"`
}

// TraceTask is one task under a requirement with its criteria and test evidence.
type TraceTask struct {
	TaskID        string        `json:"taskId"`
	Title         string        `json:"title"`
	Status        string        `json:"status"`
	LinkedVersion int           `json:"linkedVersion"`
	Stale         bool          `json:"stale"`
	Criteria      []string      `json:"criteria"`
	Evidence      []EvidenceRef `json:"evidence"`
	// TestsPassing is true when the task's most recent test evidence passed.
	TestsPassing bool `json:"testsPassing"`
	HasTests     bool `json:"hasTests"`
}

// Trace is requirement -> tasks -> evidence.
type Trace struct {
	Requirement Requirement `json:"requirement"`
	Tasks       []TraceTask `json:"tasks"`
	Criteria    int         `json:"criteria"`
	TasksDone   int         `json:"tasksCompleted"`
	TasksStale  int         `json:"tasksStale"`
	// Verified is true when the requirement has linked tasks, none is stale, and every linked task has passing test evidence.
	Verified bool `json:"verified"`
}

// Trace builds the traceability view of one requirement. Test evidence is the task's evidence of kind "test" (and "check"
// rows named test by the checks runner); a task counts as passing when its latest test evidence passed.
func (s *Service) Trace(ctx context.Context, reqID string) (Trace, error) {
	req, err := s.Get(ctx, reqID)
	if err != nil {
		return Trace{}, err
	}
	links, err := s.Links(ctx, reqID)
	if err != nil {
		return Trace{}, err
	}
	t := Trace{Requirement: req, Tasks: []TraceTask{}}
	idx := map[string]int{}
	for _, l := range links {
		i, ok := idx[l.TaskID]
		if !ok {
			var tt TraceTask
			tt.TaskID, tt.LinkedVersion = l.TaskID, l.Version
			if err := s.DB.Reader().QueryRowContext(ctx, `SELECT title, status FROM tasks WHERE id=?`, l.TaskID).Scan(&tt.Title, &tt.Status); err != nil {
				return Trace{}, err
			}
			tt.Criteria = []string{}
			t.Tasks = append(t.Tasks, tt)
			i = len(t.Tasks) - 1
			idx[l.TaskID] = i
		}
		tt := &t.Tasks[i]
		tt.Criteria = append(tt.Criteria, l.Criterion)
		if l.Stale {
			tt.Stale = true
		}
		if l.Version < tt.LinkedVersion {
			tt.LinkedVersion = l.Version
		}
		t.Criteria++
	}
	for i := range t.Tasks {
		tt := &t.Tasks[i]
		rows, err := s.DB.Reader().QueryContext(ctx, `SELECT id,kind,passed,summary,ref,attempt,created_at FROM task_evidence WHERE task_id=? AND kind IN ('test','check') ORDER BY id`, tt.TaskID)
		if err != nil {
			return Trace{}, err
		}
		for rows.Next() {
			var e EvidenceRef
			var p sql.NullInt64
			var ts int64
			if err := rows.Scan(&e.ID, &e.Kind, &p, &e.Summary, &e.Ref, &e.Attempt, &ts); err != nil {
				rows.Close()
				return Trace{}, err
			}
			if p.Valid {
				b := p.Int64 == 1
				e.Passed = &b
			}
			e.Created = time.UnixMilli(ts).UTC()
			if e.Kind == "test" {
				tt.HasTests = true
				tt.TestsPassing = e.Passed != nil && *e.Passed
			}
			tt.Evidence = append(tt.Evidence, e)
		}
		rows.Close()
		if tt.Evidence == nil {
			tt.Evidence = []EvidenceRef{}
		}
		if tt.Status == "COMPLETED" {
			t.TasksDone++
		}
		if tt.Stale {
			t.TasksStale++
		}
	}
	t.Verified = len(t.Tasks) > 0 && t.TasksStale == 0
	for _, tt := range t.Tasks {
		if !tt.TestsPassing {
			t.Verified = false
		}
	}
	return t, nil
}

// Coverage summarizes every requirement of a project (for the Requirements page).
func (s *Service) Coverage(ctx context.Context, projectID string) ([]Trace, error) {
	reqs, err := s.List(ctx, projectID, false)
	if err != nil {
		return nil, err
	}
	out := make([]Trace, 0, len(reqs))
	for _, r := range reqs {
		t, err := s.Trace(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

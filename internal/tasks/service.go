package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/redact"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Task is the row view with its related lists loaded on demand.
type Task struct {
	ID             string   `json:"id"`
	ProjectID      string   `json:"projectId"`
	Seq            int      `json:"seq"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Status         Status   `json:"status"`
	Priority       int      `json:"priority"`
	Assignee       string   `json:"assignee,omitempty"`
	Reviewer       string   `json:"reviewer,omitempty"`
	Template       string   `json:"template,omitempty"`
	Files          []string `json:"files,omitempty"`
	Acceptance     []string `json:"acceptance,omitempty"` // Given/When/Then criteria
	Worktree       string   `json:"worktree,omitempty"`
	Branch         string   `json:"branch,omitempty"`
	BaseBranch     string   `json:"baseBranch,omitempty"`
	Iteration      int      `json:"iteration"`
	MaxIterations  int      `json:"maxIterations"`
	ReviewFeedback string   `json:"reviewFeedback,omitempty"`
	Verdict        string   `json:"verdict,omitempty"`
	Model          string   `json:"model,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	CostUSD        float64  `json:"costUsd"`
	IssueRef       string   `json:"issueRef,omitempty"`
	PRRef          string   `json:"prRef,omitempty"`
	RunID          string   `json:"runId,omitempty"`
	BlockedReason  string   `json:"blockedReason,omitempty"`
	AcceptedBy     string   `json:"acceptedBy,omitempty"`
	Created        int64    `json:"created"`
	Updated        int64    `json:"updated"`
	Started        int64    `json:"started,omitempty"`
	Completed      int64    `json:"completed,omitempty"`
	Version        int64    `json:"version"`
	DependsOn      []string `json:"dependsOn,omitempty"`
}

// Imported reports whether the task was made from text outside Drydock (an issue, an error report, a finding): such a task
// carries the reference of its source.
func (t Task) Imported() bool { return t.IssueRef != "" }

// Evidence is one attached proof (brief section 12: a task enters REVIEW only with evidence).
type Evidence struct {
	ID      int64           `json:"id"`
	TaskID  string          `json:"taskId"`
	Attempt int             `json:"attempt"`
	Kind    string          `json:"kind"`
	Passed  *bool           `json:"passed,omitempty"`
	Summary string          `json:"summary"`
	Ref     string          `json:"ref,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Created int64           `json:"created"`
}

// ReviewRecord is a verdict by a reviewer.
type ReviewRecord struct {
	ID       int64  `json:"id"`
	TaskID   string `json:"taskId"`
	Attempt  int    `json:"attempt"`
	Reviewer string `json:"reviewer"`
	Verdict  string `json:"verdict"` // SHIP | REVISE
	Feedback string `json:"feedback"`
	Model    string `json:"model,omitempty"`
	// Commit is the task commit the reviewer was shown (its diff against the base); '' when unknown.
	Commit  string `json:"commit,omitempty"`
	Created int64  `json:"created"`
}

// Errors.
var (
	ErrNotFound = errors.New("tasks: not found")
	ErrConflict = errors.New("tasks: concurrent modification (version mismatch)")
)

// Hooks let other subsystems react without this package importing them.
type Hooks struct {
	// OnCreate is called after a task is created (the room creates the task channel).
	OnCreate func(ctx context.Context, t Task)
	// OnTerminal is called when a task reaches COMPLETED, FAILED or CANCELLED (the room archives the channel).
	OnTerminal func(ctx context.Context, t Task)
}

// Service owns task persistence and transitions.
type Service struct {
	DB    *storage.DB
	Bus   events.Bus
	Hooks Hooks
	// Redact, if set, removes secrets from the text an agent or a tool wrote that a task keeps: review feedback, the reason a
	// task is blocked, the summary and data of evidence (the scanner's Redact; audit 2026-10-16, H7).
	Redact func(string) string
}

func (s *Service) red(v string) string {
	if s.Redact == nil {
		return v
	}
	return redact.Lines(v, s.Redact)
}

// CreateReq creates a task.
type CreateReq struct {
	ProjectID     string
	Title         string
	Description   string
	Priority      int
	Template      string
	Files         []string
	Acceptance    []string
	DependsOn     []string
	IssueRef      string
	MaxIterations int
}

// Limits of a task a request may set.
const (
	MaxTitleLen      = 300
	MaxIterationsCap = 20 // every iteration is a paid work and review turn
	MaxPriority      = 1000
)

func (r CreateReq) validate() error {
	switch title := strings.TrimSpace(r.Title); {
	case strings.TrimSpace(r.ProjectID) == "":
		return errors.New("tasks: project required")
	case title == "":
		return errors.New("tasks: title required")
	case len(title) > MaxTitleLen:
		return fmt.Errorf("tasks: the title is %d characters; keep it under %d and put the rest in the description", len(title), MaxTitleLen)
	case r.MaxIterations > MaxIterationsCap:
		return fmt.Errorf("tasks: at most %d iterations (each is a paid work and review turn), not %d", MaxIterationsCap, r.MaxIterations)
	case r.Priority < -MaxPriority || r.Priority > MaxPriority:
		return fmt.Errorf("tasks: priority %d is outside -%d to %d", r.Priority, MaxPriority, MaxPriority)
	}
	return nil
}

// Create inserts a BACKLOG task.
func (s *Service) Create(ctx context.Context, req CreateReq) (Task, error) {
	if err := req.validate(); err != nil {
		return Task{}, err
	}
	id := ids.New("task")
	now := storage.Now()
	maxIt := req.MaxIterations
	if maxIt <= 0 {
		maxIt = 5
	}
	files, _ := json.Marshal(nz(req.Files))
	acc, _ := json.Marshal(nz(req.Acceptance))
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		var seq int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM tasks WHERE project_id=?`, req.ProjectID).Scan(&seq); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO tasks(id,project_id,seq,title,description,status,priority,template,files,acceptance,issue_ref,max_iterations,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, req.ProjectID, seq, req.Title, req.Description, string(Backlog), req.Priority, req.Template, string(files), string(acc), req.IssueRef, maxIt, now, now); err != nil {
			return err
		}
		for _, d := range req.DependsOn {
			if d == id {
				return errors.New("tasks: a task cannot depend on itself")
			}
			var dp string
			if err := tx.QueryRowContext(ctx, `SELECT project_id FROM tasks WHERE id=?`, d).Scan(&dp); err != nil {
				return fmt.Errorf("tasks: dependency %s: %w", d, ErrNotFound)
			}
			if dp != req.ProjectID {
				return fmt.Errorf("tasks: dependency %s belongs to another project", d)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO task_deps(task_id,depends_on) VALUES(?,?)`, id, d); err != nil {
				return fmt.Errorf("dependency %s: %w", d, err)
			}
		}
		return nil
	})
	if err != nil {
		return Task{}, err
	}
	t, err := s.Get(ctx, id)
	if err != nil {
		return t, err
	}
	s.emit(ctx, "task.created", t, map[string]any{"title": t.Title, "seq": t.Seq})
	if s.Hooks.OnCreate != nil {
		s.Hooks.OnCreate(ctx, t)
	}
	return t, nil
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

const taskCols = `id,project_id,seq,title,description,status,priority,assignee,reviewer,template,files,acceptance,worktree,branch,base_branch,
	iteration,max_iterations,review_feedback,verdict,model,effort,cost_usd,issue_ref,pr_ref,run_id,blocked_reason,accepted_by,
	created_at,updated_at,started_at,completed_at,version`

type rower interface{ Scan(dest ...any) error }

func scanTask(r rower) (Task, error) {
	var t Task
	var status, files, acc string
	err := r.Scan(&t.ID, &t.ProjectID, &t.Seq, &t.Title, &t.Description, &status, &t.Priority, &t.Assignee, &t.Reviewer, &t.Template, &files, &acc,
		&t.Worktree, &t.Branch, &t.BaseBranch, &t.Iteration, &t.MaxIterations, &t.ReviewFeedback, &t.Verdict, &t.Model, &t.Effort, &t.CostUSD,
		&t.IssueRef, &t.PRRef, &t.RunID, &t.BlockedReason, &t.AcceptedBy, &t.Created, &t.Updated, &t.Started, &t.Completed, &t.Version)
	t.Status = Status(status)
	_ = json.Unmarshal([]byte(files), &t.Files)
	_ = json.Unmarshal([]byte(acc), &t.Acceptance)
	return t, err
}

// Get loads a task with its dependencies.
func (s *Service) Get(ctx context.Context, id string) (Task, error) {
	t, err := scanTask(s.DB.Reader().QueryRowContext(ctx, `SELECT `+taskCols+` FROM tasks WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT depends_on FROM task_deps WHERE task_id=? ORDER BY depends_on`, id)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return t, err
		}
		t.DependsOn = append(t.DependsOn, d)
	}
	return t, rows.Err()
}

// Filter selects tasks.
type Filter struct {
	ProjectID string
	Statuses  []Status
	Assignee  string
	Limit     int
}

// List returns tasks, newest first.
func (s *Service) List(ctx context.Context, f Filter) ([]Task, error) {
	q := `SELECT ` + taskCols + ` FROM tasks WHERE 1=1`
	var args []any
	if f.ProjectID != "" {
		q += ` AND project_id=?`
		args = append(args, f.ProjectID)
	}
	if f.Assignee != "" {
		q += ` AND assignee=?`
		args = append(args, f.Assignee)
	}
	if len(f.Statuses) > 0 {
		q += ` AND status IN (` + strings.TrimSuffix(strings.Repeat("?,", len(f.Statuses)), ",") + `)`
		for _, st := range f.Statuses {
			args = append(args, string(st))
		}
	}
	q += ` ORDER BY seq DESC`
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, f.Limit)
	}
	rows, err := s.DB.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Count returns how many tasks match the filter (its Limit is ignored).
func (s *Service) Count(ctx context.Context, f Filter) (int, error) {
	q := `SELECT COUNT(*) FROM tasks WHERE 1=1`
	var args []any
	if f.ProjectID != "" {
		q += ` AND project_id=?`
		args = append(args, f.ProjectID)
	}
	if f.Assignee != "" {
		q += ` AND assignee=?`
		args = append(args, f.Assignee)
	}
	if len(f.Statuses) > 0 {
		q += ` AND status IN (` + strings.TrimSuffix(strings.Repeat("?,", len(f.Statuses)), ",") + `)`
		for _, st := range f.Statuses {
			args = append(args, string(st))
		}
	}
	var n int
	err := s.DB.Reader().QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

// Update changes task fields under compare-and-swap.
type Update struct {
	Assignee   *string
	Reviewer   *string
	Worktree   *string
	Branch     *string
	Base       *string
	Model      *string
	Effort     *string
	RunID      *string
	PRRef      *string
	IssueRef   *string
	Files      *[]string
	Acceptance *[]string
	AddCost    float64
}

// Patch sets fields atomically. Setting independent columns does not depend on the previous row state, so it needs no
// compare-and-swap (it still bumps version so readers holding an older version fail their own CAS); AddCost is a
// commutative increment, so concurrent cost updates from several agents never lose each other. Transitions, whose result
// does depend on the current state, use CAS (see Transition).
func (s *Service) Patch(ctx context.Context, id string, u Update) (Task, error) {
	set, args := []string{"updated_at=?", "version=version+1"}, []any{storage.Now()}
	add := func(col string, v any) { set = append(set, col+"=?"); args = append(args, v) }
	if u.Assignee != nil {
		add("assignee", *u.Assignee)
	}
	if u.Reviewer != nil {
		add("reviewer", *u.Reviewer)
	}
	if u.Worktree != nil {
		add("worktree", *u.Worktree)
	}
	if u.Branch != nil {
		add("branch", *u.Branch)
	}
	if u.Base != nil {
		add("base_branch", *u.Base)
	}
	if u.Model != nil {
		add("model", *u.Model)
	}
	if u.Effort != nil {
		add("effort", *u.Effort)
	}
	if u.RunID != nil {
		add("run_id", *u.RunID)
	}
	if u.PRRef != nil {
		add("pr_ref", *u.PRRef)
	}
	if u.IssueRef != nil {
		add("issue_ref", *u.IssueRef)
	}
	if u.Files != nil {
		b, _ := json.Marshal(nz(*u.Files))
		add("files", string(b))
	}
	if u.Acceptance != nil {
		b, _ := json.Marshal(nz(*u.Acceptance))
		add("acceptance", string(b))
	}
	if u.AddCost != 0 {
		set = append(set, "cost_usd=cost_usd+?")
		args = append(args, u.AddCost)
	}
	args = append(args, id)
	var n int64
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE tasks SET `+strings.Join(set, ",")+` WHERE id=?`, args...)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	if err != nil {
		return Task{}, err
	}
	if n == 0 {
		return Task{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

// TransitionOpts carries context for a transition.
type TransitionOpts struct {
	By       string // 'human:<name>', agent id, or 'system'
	Reason   string
	Feedback string // for REVIEW/VERIFICATION -> IN_PROGRESS: what to fix (becomes the next iteration's only context)
	// Resync reopens a REVIEW/VERIFICATION task only because the base branch moved: nothing was wrong with the work, so it needs
	// no feedback and does not use up an iteration. The workflow graph bounds how often this can repeat.
	Resync bool
}

// Transition moves a task through the state machine, enforcing guards, with compare-and-swap on version.
func (s *Service) Transition(ctx context.Context, id string, to Status, o TransitionOpts) (Task, error) {
	for try := 0; try < 5; try++ {
		t, err := s.Get(ctx, id)
		if err != nil {
			return t, err
		}
		if t.Status == to {
			return t, nil // idempotent
		}
		if !CanTransition(t.Status, to) {
			return t, &TransitionError{From: t.Status, To: to, Reason: "not allowed by the state machine"}
		}
		if err := s.guard(ctx, t, to, o); err != nil {
			return t, err
		}
		now := storage.Now()
		set := []string{"status=?", "updated_at=?", "version=version+1"}
		args := []any{string(to), now}
		switch to {
		case InProgress:
			if t.Started == 0 {
				set = append(set, "started_at=?")
				args = append(args, now)
			}
			if (t.Status == Review || t.Status == Verification) && !o.Resync {
				set = append(set, "iteration=iteration+1", "review_feedback=?")
				args = append(args, s.red(o.Feedback))
			}
			set = append(set, "blocked_reason=''")
		case Blocked:
			set = append(set, "blocked_reason=?")
			args = append(args, s.red(o.Reason))
		case Completed:
			set = append(set, "completed_at=?", "accepted_by=?")
			args = append(args, now, o.By)
		case Failed, Cancelled:
			set = append(set, "completed_at=?")
			args = append(args, now)
		}
		args = append(args, id, t.Version)
		var n int64
		err = s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, `UPDATE tasks SET `+strings.Join(set, ",")+` WHERE id=? AND version=?`, args...)
			if err != nil {
				return err
			}
			n, _ = res.RowsAffected()
			if n == 1 && to == Failed {
				_, err = tx.ExecContext(ctx, `INSERT INTO dead_letters(id,task_id,template,reason,detail,created_at) VALUES(?,?,?,?,?,?)`,
					ids.New("dl"), id, t.Template, nonEmpty(o.Reason, "task failed"), "{}", now)
			}
			return err
		})
		if err != nil {
			return t, err
		}
		if n == 0 {
			continue // lost a race; re-read and re-validate
		}
		nt, err := s.Get(ctx, id)
		if err != nil {
			return nt, err
		}
		s.emit(ctx, eventFor(t.Status, to), nt, map[string]any{"from": t.Status, "to": to, "by": o.By, "reason": o.Reason})
		if to.Terminal() && s.Hooks.OnTerminal != nil {
			s.Hooks.OnTerminal(ctx, nt)
		}
		return nt, nil
	}
	return Task{}, ErrConflict
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func eventFor(from, to Status) string {
	switch to {
	case Claimed:
		return "task.claimed"
	case InProgress:
		if from == Claimed {
			return "task.started"
		}
		return "task.resumed"
	case Blocked:
		return "task.blocked"
	case WaitingHuman:
		return "task.waiting_human"
	case Completed:
		return "task.completed"
	case Failed:
		return "task.failed"
	case Cancelled:
		return "task.cancelled"
	case Review:
		return "task.review"
	case Verification:
		return "task.verification"
	case Ready:
		return "task.ready"
	}
	return "task.updated"
}

// guard enforces the rules that depend on more than the state graph.
func (s *Service) guard(ctx context.Context, t Task, to Status, o TransitionOpts) error {
	bad := func(reason string) error { return &TransitionError{From: t.Status, To: to, Reason: reason} }
	switch to {
	case Ready:
		for _, d := range t.DependsOn {
			dep, err := s.Get(ctx, d)
			if err != nil {
				return err
			}
			if dep.Status != Completed {
				return bad(fmt.Sprintf("dependency %s is %s", d, dep.Status))
			}
		}
	case Claimed:
		if t.Assignee == "" {
			return bad("no assignee")
		}
	case Review:
		ev, err := s.Evidence(ctx, t.ID)
		if err != nil {
			return err
		}
		var haveTest, haveDiff, haveCommit bool
		for _, e := range ev {
			switch e.Kind {
			case "test":
				// the latest test evidence must have passed
				haveTest = e.Passed != nil && *e.Passed
			case "diff":
				haveDiff = true
			case "commit":
				haveCommit = true
			}
		}
		if !haveTest || !haveDiff || !haveCommit {
			return bad(fmt.Sprintf("a task enters REVIEW only with evidence: passing tests=%v diff=%v commit=%v", haveTest, haveDiff, haveCommit))
		}
		if t.Reviewer == "" {
			return bad("no reviewer assigned")
		}
		if t.Reviewer == t.Assignee {
			return bad("the reviewer must be a different agent instance from the assignee")
		}
	case Verification:
		rv, err := s.Reviews(ctx, t.ID)
		if err != nil {
			return err
		}
		if len(rv) == 0 || rv[len(rv)-1].Verdict != "SHIP" {
			return bad("review has not returned SHIP")
		}
		if rv[len(rv)-1].Reviewer == t.Assignee {
			return bad("the SHIP verdict must come from a different agent instance than the assignee")
		}
	case Completed:
		if !strings.HasPrefix(o.By, "human:") {
			return bad("only a human can accept a task (VERIFICATION -> COMPLETED requires the human's acceptance)")
		}
		ev, err := s.Evidence(ctx, t.ID)
		if err != nil {
			return err
		}
		passed := false
		for _, e := range ev {
			if e.Kind == "test" {
				passed = e.Passed != nil && *e.Passed
			}
		}
		if !passed {
			return bad("the latest test evidence is not passing")
		}
	case InProgress:
		if o.Resync && (!strings.HasPrefix(o.By, "system")) {
			return bad("only the system reopens a task because the base branch moved")
		}
		if o.Resync {
			return nil
		}
		if (t.Status == Review || t.Status == Verification) && strings.TrimSpace(o.Feedback) == "" {
			return bad("changes requested without feedback: the next iteration needs the review feedback")
		}
		if (t.Status == Review || t.Status == Verification) && t.Iteration+1 >= t.MaxIterations {
			return bad(fmt.Sprintf("iteration limit %d reached: move to WAITING_HUMAN or FAILED", t.MaxIterations))
		}
	}
	return nil
}

// AddEvidence attaches proof to a task.
func (s *Service) AddEvidence(ctx context.Context, taskID string, e Evidence) (Evidence, error) {
	if len(e.Data) == 0 {
		e.Data = json.RawMessage(`{}`)
	}
	e.TaskID = taskID
	e.Created = storage.Now()
	if s.Redact != nil {
		e.Summary = s.red(e.Summary)
		e.Data = redact.JSON(e.Data, s.Redact)
	}
	var passed any
	if e.Passed != nil {
		passed = boolInt(*e.Passed)
	}
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO task_evidence(task_id,attempt,kind,passed,summary,ref,data,created_at) VALUES(?,?,?,?,?,?,?,?)`,
			taskID, e.Attempt, e.Kind, passed, e.Summary, e.Ref, string(e.Data), e.Created)
		if err != nil {
			return err
		}
		e.ID, _ = res.LastInsertId()
		return nil
	})
	if err == nil {
		if t, gerr := s.Get(ctx, taskID); gerr == nil {
			s.emit(ctx, "task.evidence", t, map[string]any{"kind": e.Kind, "passed": e.Passed, "summary": e.Summary})
		}
	}
	return e, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Evidence lists a task's evidence, oldest first.
func (s *Service) Evidence(ctx context.Context, taskID string) ([]Evidence, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT id,task_id,attempt,kind,passed,summary,ref,data,created_at FROM task_evidence WHERE task_id=? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Evidence
	for rows.Next() {
		var e Evidence
		var passed sql.NullInt64
		var data string
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Attempt, &e.Kind, &passed, &e.Summary, &e.Ref, &data, &e.Created); err != nil {
			return nil, err
		}
		if passed.Valid {
			b := passed.Int64 != 0
			e.Passed = &b
		}
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}

// AddReview records a reviewer verdict and updates the task's verdict/feedback fields.
func (s *Service) AddReview(ctx context.Context, taskID string, r ReviewRecord) (ReviewRecord, error) {
	if r.Verdict != "SHIP" && r.Verdict != "REVISE" {
		return r, fmt.Errorf("tasks: verdict must be SHIP or REVISE, got %q", r.Verdict)
	}
	r.TaskID, r.Created = taskID, storage.Now()
	r.Feedback = s.red(r.Feedback)
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO task_reviews(task_id,attempt,reviewer,verdict,feedback,model,commit_sha,created_at) VALUES(?,?,?,?,?,?,?,?)`,
			taskID, r.Attempt, r.Reviewer, r.Verdict, r.Feedback, r.Model, r.Commit, r.Created)
		if err != nil {
			return err
		}
		r.ID, _ = res.LastInsertId()
		_, err = tx.ExecContext(ctx, `UPDATE tasks SET verdict=?, review_feedback=?, updated_at=?, version=version+1 WHERE id=?`, r.Verdict, r.Feedback, r.Created, taskID)
		return err
	})
	return r, err
}

// Reviews lists a task's reviews, oldest first.
func (s *Service) Reviews(ctx context.Context, taskID string) ([]ReviewRecord, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT id,task_id,attempt,reviewer,verdict,feedback,model,commit_sha,created_at FROM task_reviews WHERE task_id=? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReviewRecord
	for rows.Next() {
		var r ReviewRecord
		if err := rows.Scan(&r.ID, &r.TaskID, &r.Attempt, &r.Reviewer, &r.Verdict, &r.Feedback, &r.Model, &r.Commit, &r.Created); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PromoteReady moves BACKLOG tasks whose dependencies are all COMPLETED to READY. Returns the promoted ids. A task imported
// from outside (an issue, an error report, a finding: IssueRef is set) stays in BACKLOG until a human moves it on, so text
// from outside never starts paid work by itself, even with automatic scheduling on.
func (s *Service) PromoteReady(ctx context.Context, projectID string) ([]string, error) {
	backlog, err := s.List(ctx, Filter{ProjectID: projectID, Statuses: []Status{Backlog}})
	if err != nil {
		return nil, err
	}
	var promoted []string
	for _, t := range backlog {
		if t.Imported() {
			continue
		}
		if _, err := s.Transition(ctx, t.ID, Ready, TransitionOpts{By: "system", Reason: "dependencies met"}); err == nil {
			promoted = append(promoted, t.ID)
		}
	}
	return promoted, nil
}

// DeadLetter records a task that exhausted its iteration limit or tripped the watchdog twice, without failing it.
func (s *Service) DeadLetter(ctx context.Context, taskID, reason string, detail any) error {
	t, err := s.Get(ctx, taskID)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(detail)
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO dead_letters(id,task_id,template,reason,detail,created_at) VALUES(?,?,?,?,?,?)`,
			ids.New("dl"), taskID, t.Template, reason, string(b), storage.Now())
		return err
	})
}

func (s *Service) emit(ctx context.Context, typ string, t Task, data map[string]any) {
	if s.Bus == nil {
		return
	}
	b, _ := json.Marshal(data)
	_, _ = s.Bus.Publish(ctx, events.Event{Type: typ, ProjectID: t.ProjectID, TaskID: t.ID, Data: b})
}

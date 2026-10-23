package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// task.summaries (decision P97): one row per open or recently finished task with what the board, the inbox and the crew show on
// a card, so no screen reads task.get per card. Every field comes from what is recorded (the task, its evidence, its reviews,
// its open requests) and from presence; nothing is estimated. The rows are read with a few queries for all the tasks at once.

// SummaryEndedWithin is how long a finished task stays in task.summaries when the caller does not say (finishedSince).
const SummaryEndedWithin = 7 * 24 * time.Hour

// TaskSummary is one task as a card shows it.
type TaskSummary struct {
	TaskID     string       `json:"taskId"`
	ProjectID  string       `json:"projectId"`
	ProjectKey string       `json:"projectKey"` // with Seq, the task's identifier: GOC-41
	Seq        int          `json:"seq"`
	Title      string       `json:"title"`
	Status     tasks.Status `json:"status"`
	Template   string       `json:"template,omitempty"`
	Labels     []string     `json:"labels"`
	Assignee   string       `json:"assignee,omitempty"`
	Reviewer   string       `json:"reviewer,omitempty"`
	// Criteria is how many acceptance criteria the task has.
	Criteria int `json:"criteria"`
	// Step is where the task is (tasks.StepIndex, the task page's rule): spec, code, checks, review, you, or done. StepIndex of
	// StepCount is how far along it is: 0 to 5 of 5.
	Step      string `json:"step"`
	StepIndex int    `json:"stepIndex"`
	StepCount int    `json:"stepCount"`
	// Attempt is the iteration the task is on, from 1; MaxAttempts is its limit (each one a work and review turn).
	Attempt     int `json:"attempt"`
	MaxAttempts int `json:"maxAttempts"`
	// Checks is the newest attempt's result of each kind of check (pipeline.LatestChecks), in the order they first ran.
	Checks  []pipeline.CheckState `json:"checks"`
	Diff    *SummaryDiff          `json:"diff,omitempty"`    // the newest diff's size; absent before the first
	Verdict string                `json:"verdict,omitempty"` // the newest reviewer verdict: SHIP or REVISE
	// Pending is the oldest request that waits for a person, PendingCount how many wait.
	Pending      *SummaryRequest `json:"pending,omitempty"`
	PendingCount int             `json:"pendingCount"`
	StartedAt    int64           `json:"startedAt,omitempty"`
	CompletedAt  int64           `json:"completedAt,omitempty"`
	UpdatedAt    int64           `json:"updatedAt"`
	// Presence and Doing are the assignee's while it works on this task: its presence (Working, Thinking, Waiting...) and its
	// latest tool call with its argument (room.DoingLine: "Editing limiter.go"). Absent when it is not on this task.
	Presence string `json:"presence,omitempty"`
	Doing    string `json:"doing,omitempty"`
}

// SummaryDiff is the size of a diff.
type SummaryDiff struct {
	Files      int `json:"files"`
	Insertions int `json:"insertions"`
	Deletions  int `json:"deletions"`
}

// SummaryRequest is a request that waits for a person. Phase says what it asks: "spec" (accept the spec), "merge" (review and
// merge), else the kind (approval, question, review). run.respond {runId, requestId} answers it.
type SummaryRequest struct {
	Kind      string `json:"kind"`
	Phase     string `json:"phase"`
	Title     string `json:"title"`
	RequestID string `json:"requestId"`
	RunID     string `json:"runId"`
	CreatedAt int64  `json:"createdAt"`
}

// TaskSummaries is task.summaries' answer. Like task.list it holds at most TaskListDefault (or the limit asked) of the newest
// tasks: Total is how many match, and Partial says the list is cut.
type TaskSummaries struct {
	Summaries []TaskSummary `json:"summaries"`
	Total     int           `json:"total"`
	Partial   bool          `json:"partial"`
}

// SummaryReq selects the tasks of task.summaries.
type SummaryReq struct {
	ProjectID string `json:"projectId"` // '' is every project
	// IDs, when given, are the tasks to describe whatever their state (unknown ids are left out).
	IDs []string `json:"ids"`
	// FinishedSince keeps the tasks that ended at or after it (ms since the epoch); 0 is SummaryEndedWithin ago. Ignored with IDs.
	FinishedSince int64 `json:"finishedSince"`
	Limit         int   `json:"limit"`
}

// summaryChunk bounds the ids in one IN list.
const summaryChunk = 500

func init() {
	RegisterExtension(Extension{Name: "tasksummary", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		cr := &checkRuns{m: map[string]checkRun{}}
		checkRunsOf.Store(c, cr)
		events.Watch(ctx, c.Bus, events.Filter{Types: []string{pipeline.CheckStartedEvent, "task.evidence", "task.check_inconclusive", "task.review",
			"task.verification", "task.blocked", "task.waiting_human", "task.resumed", "task.completed", "task.failed", "task.cancelled"}}, cr.apply)
		return map[string]Method{
			"task.summaries": bind(func(ctx context.Context, p SummaryReq) (any, error) { return c.TaskSummaries(ctx, p) }),
		}, func() { checkRunsOf.Delete(c) }, nil
	}})
}

// checkRuns is the check each task's pass is running now, from pipeline.CheckStartedEvent until the result is recorded or the
// task moves on. Kept in memory: a restart ends every pass (a recovered run starts its pass again, and announces it).
type checkRuns struct {
	mu sync.Mutex
	m  map[string]checkRun
}

type checkRun struct {
	pipeline.RunningCheck
	at time.Time
}

// checkRunStale is how long a check counts as running without its result (a check is stopped after ten minutes by default).
const checkRunStale = 30 * time.Minute

var checkRunsOf sync.Map // *Core -> *checkRuns

func (r *checkRuns) apply(e events.Event) {
	if e.TaskID == "" {
		return
	}
	var d struct {
		Kind  string `json:"kind"`
		Round string `json:"round"`
	}
	_ = json.Unmarshal(e.Data, &d)
	r.mu.Lock()
	defer r.mu.Unlock()
	switch e.Type {
	case pipeline.CheckStartedEvent:
		r.m[e.TaskID] = checkRun{pipeline.RunningCheck{Kind: d.Kind, Round: d.Round}, time.Now()}
	case "task.evidence", "task.check_inconclusive":
		if cur, ok := r.m[e.TaskID]; ok && cur.Kind == d.Kind {
			delete(r.m, e.TaskID)
		}
	case "task.review", "task.verification", "task.blocked", "task.waiting_human", "task.resumed", "task.completed", "task.failed",
		"task.cancelled": // the task moved on: no pass of it is running
		delete(r.m, e.TaskID)
	}
}

func (c *Core) runningCheck(taskID string) *pipeline.RunningCheck {
	v, ok := checkRunsOf.Load(c)
	if !ok {
		return nil
	}
	r := v.(*checkRuns)
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, ok := r.m[taskID]
	if !ok || time.Since(cur.at) > checkRunStale {
		return nil
	}
	rc := cur.RunningCheck
	return &rc
}

// TaskSummaries describes the open tasks and those that ended since FinishedSince (or the tasks named by IDs), newest first.
func (c *Core) TaskSummaries(ctx context.Context, req SummaryReq) (TaskSummaries, error) {
	out := TaskSummaries{Summaries: []TaskSummary{}}
	f := tasks.Filter{ProjectID: req.ProjectID, IDs: req.IDs}
	if len(req.IDs) == 0 {
		f.OpenOrEndedSince = req.FinishedSince
		if f.OpenOrEndedSince <= 0 {
			f.OpenOrEndedSince = storage.Now() - SummaryEndedWithin.Milliseconds()
		}
	}
	total, err := c.Tasks.Count(ctx, f)
	if err != nil {
		return out, err
	}
	f.Limit = taskListLimit(req.Limit)
	list, err := c.Tasks.List(ctx, f)
	if err != nil {
		return out, err
	}
	out.Total, out.Partial = total, total > len(list)
	if len(list) == 0 {
		return out, nil
	}
	idl := make([]string, len(list))
	for i, t := range list {
		idl[i] = t.ID
	}
	keys, err := c.projectKeys(ctx)
	if err != nil {
		return out, err
	}
	ev, err := c.summaryEvidence(ctx, idl)
	if err != nil {
		return out, err
	}
	rv, err := c.summaryReviews(ctx, idl)
	if err != nil {
		return out, err
	}
	pend, err := c.summaryPending(ctx, idl)
	if err != nil {
		return out, err
	}
	for _, t := range list {
		s := TaskSummary{TaskID: t.ID, ProjectID: t.ProjectID, ProjectKey: keys[t.ProjectID], Seq: t.Seq, Title: t.Title, Status: t.Status,
			Template: t.Template, Labels: t.Labels, Assignee: t.Assignee, Reviewer: t.Reviewer, Criteria: len(t.Acceptance),
			Attempt: t.Iteration + 1, MaxAttempts: t.MaxIterations, StepCount: len(tasks.StepNames),
			StartedAt: t.Started, CompletedAt: t.Completed, UpdatedAt: t.Updated, PendingCount: len(pend[t.ID])}
		e := ev[t.ID]
		var running *pipeline.RunningCheck
		if !t.Status.Terminal() {
			running = c.runningCheck(t.ID)
		}
		s.Checks = pipeline.LatestChecks(e, running)
		for i := len(e) - 1; i >= 0; i-- {
			if e[i].Kind == "diff" {
				var st struct{ Files, Insertions, Deletions int }
				if json.Unmarshal(e[i].Data, &st) == nil {
					s.Diff = &SummaryDiff{Files: st.Files, Insertions: st.Insertions, Deletions: st.Deletions}
				}
				break
			}
		}
		r := rv[t.ID]
		s.Verdict = r.verdict
		specGate := false
		if ps := pend[t.ID]; len(ps) > 0 {
			p := ps[0]
			s.Pending = &p
			for _, q := range ps {
				specGate = specGate || q.Phase == "spec"
			}
		}
		s.StepIndex = tasks.StepIndex(t, e, r.count, specGate)
		s.Step = tasks.StepName(s.StepIndex)
		if t.Assignee != "" && !t.Status.Terminal() && c.Presence != nil {
			if p, ok := c.Presence.Get(t.Assignee); ok && p.TaskID == t.ID && working(p.Presence) {
				s.Presence, s.Doing = string(p.Presence), p.Doing
			}
		}
		out.Summaries = append(out.Summaries, s)
	}
	return out, nil
}

// working reports a presence of an agent in a turn.
func working(p room.Presence) bool {
	return p == room.PresenceWorking || p == room.PresenceThinking || p == room.PresenceWaiting
}

func (c *Core) projectKeys(ctx context.Context) (map[string]string, error) {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT id, key FROM projects`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, k string
		if err := rows.Scan(&id, &k); err != nil {
			return nil, err
		}
		out[id] = k
	}
	return out, rows.Err()
}

// inChunks runs q once per summaryChunk of ids: q has one %s where their placeholders go, after those of the extra arguments.
func (c *Core) inChunks(ctx context.Context, idl []string, q string, extra []any, scan func(*sql.Rows) error) error {
	for len(idl) > 0 {
		n := min(len(idl), summaryChunk)
		args := append([]any(nil), extra...)
		for _, id := range idl[:n] {
			args = append(args, id)
		}
		rows, err := c.DB.Reader().QueryContext(ctx, strings.Replace(q, "%s", strings.TrimSuffix(strings.Repeat("?,", n), ","), 1), args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			if err := scan(rows); err != nil {
				rows.Close()
				return err
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		idl = idl[n:]
	}
	return nil
}

// summaryEvidence reads the tasks' evidence, oldest first, with the summary and data only of the kinds a summary reads them of
// (checks and diffs): a spec's or a review request's data can be large.
func (c *Core) summaryEvidence(ctx context.Context, idl []string) (map[string][]tasks.Evidence, error) {
	var kinds []string
	for k := range pipeline.CheckKinds {
		kinds = append(kinds, "'"+k+"'")
	}
	kinds = append(kinds, "'diff'")
	in := strings.Join(kinds, ",")
	out := map[string][]tasks.Evidence{}
	err := c.inChunks(ctx, idl, `SELECT id, task_id, attempt, kind, passed,
		CASE WHEN kind IN (`+in+`) THEN summary ELSE '' END, CASE WHEN kind IN (`+in+`) THEN data ELSE '{}' END, created_at
		FROM task_evidence WHERE task_id IN (%s) ORDER BY id`, nil, func(rows *sql.Rows) error {
		var e tasks.Evidence
		var passed sql.NullInt64
		var data string
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Attempt, &e.Kind, &passed, &e.Summary, &data, &e.Created); err != nil {
			return err
		}
		if passed.Valid {
			b := passed.Int64 != 0
			e.Passed = &b
		}
		e.Data = json.RawMessage(data)
		out[e.TaskID] = append(out[e.TaskID], e)
		return nil
	})
	return out, err
}

type reviewTally struct {
	count   int
	verdict string // the newest
}

func (c *Core) summaryReviews(ctx context.Context, idl []string) (map[string]reviewTally, error) {
	out := map[string]reviewTally{}
	err := c.inChunks(ctx, idl, `SELECT task_id, verdict FROM task_reviews WHERE task_id IN (%s) ORDER BY id`, nil, func(rows *sql.Rows) error {
		var id, v string
		if err := rows.Scan(&id, &v); err != nil {
			return err
		}
		r := out[id]
		r.count++
		r.verdict = v
		out[id] = r
		return nil
	})
	return out, err
}

// summaryPending reads the requests that wait for a person, oldest first: those of a run that has not ended, and a worker's
// permission requests (not bound to a run; answered with the task's run) while the task's run goes on and the request has not
// expired, as task.get lists them.
func (c *Core) summaryPending(ctx context.Context, idl []string) (map[string][]SummaryRequest, error) {
	out := map[string][]SummaryRequest{}
	now := storage.Now()
	err := c.inChunks(ctx, idl, `SELECT a.id, a.run_id, a.task_id, a.kind, a.title, CAST(a.payload AS TEXT), a.created_at, COALESCE(t.run_id, '')
		FROM approvals a LEFT JOIN tasks t ON t.id = a.task_id
		WHERE a.status = 'REQUESTED'
		  AND (a.expires_at = 0 OR a.expires_at > ?)
		  AND ((a.run_id <> '' AND EXISTS (SELECT 1 FROM workflow_runs r WHERE r.id = a.run_id AND r.status NOT IN ('COMPLETED','FAILED','CANCELLED')))
		    OR (a.run_id = '' AND a.title LIKE ? AND EXISTS (SELECT 1 FROM workflow_runs r WHERE r.id = t.run_id AND r.status NOT IN ('COMPLETED','FAILED','CANCELLED'))))
		  AND a.task_id IN (%s)
		ORDER BY a.created_at, a.rowid`, []any{now, security.PermissionTitlePrefix + "%"}, func(rows *sql.Rows) error {
		var r SummaryRequest
		var taskID, payload, taskRun string
		if err := rows.Scan(&r.RequestID, &r.RunID, &taskID, &r.Kind, &r.Title, &payload, &r.CreatedAt, &taskRun); err != nil {
			return err
		}
		if r.RunID == "" {
			r.RunID = taskRun
		}
		r.Phase = pipeline.RequestPhase(r.Kind, r.Title, json.RawMessage(payload))
		out[taskID] = append(out[taskID], r)
		return nil
	})
	return out, err
}

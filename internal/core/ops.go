package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/runfiles"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// postTurn posts an agent's final text for a turn in the task channel, authored by that agent.
func (c *Core) postTurn(ctx context.Context, t tasks.Task, agentID, role, text string) {
	rm, err := c.Room.RoomByTask(ctx, t.ID)
	if err != nil {
		return
	}
	typ := room.TypeChat
	if role == "review" {
		typ = room.TypeResult
	}
	if _, err := c.Room.Post(ctx, room.PostParams{RoomID: rm.ID, Type: typ, Author: room.Agent(agentID), Body: text, TaskID: t.ID}); err != nil {
		c.logf("post turn: %v", err)
	}
}

// CreateTask adds a BACKLOG task.
func (c *Core) CreateTask(ctx context.Context, req tasks.CreateReq) (tasks.Task, error) {
	if _, err := c.GetProject(ctx, req.ProjectID); err != nil {
		return tasks.Task{}, err
	}
	return c.Tasks.Create(ctx, req)
}

// AssignTask sets a task's worker and independent reviewer. They must be different agents of the project.
func (c *Core) AssignTask(ctx context.Context, taskID, assignee, reviewer string) (tasks.Task, error) {
	t, err := c.Tasks.Get(ctx, taskID)
	if err != nil {
		return t, err
	}
	if assignee == "" || reviewer == "" || assignee == reviewer {
		return t, errors.New("a task needs a worker and a different agent as reviewer")
	}
	for _, id := range []string{assignee, reviewer} {
		a, err := c.agent(ctx, id)
		if err != nil || a.ProjectID != t.ProjectID || a.Archived {
			return t, fmt.Errorf("agent %s is not on this project's team", id)
		}
	}
	nt, err := c.Tasks.Patch(ctx, taskID, tasks.Update{Assignee: &assignee, Reviewer: &reviewer})
	if err != nil {
		return nt, err
	}
	if rm, err := c.Room.RoomByTask(ctx, taskID); err == nil {
		for id, role := range map[string]string{assignee: "assignee", reviewer: "reviewer"} {
			if err := c.Room.AddMember(ctx, rm.ID, room.Agent(id), role, room.StateActive, room.Human(c.cfg.User)); err != nil {
				c.logf("add %s to task channel: %v", id, err)
			}
		}
	}
	return nt, nil
}

// requireRepo refuses work that changes code in a project without a repository, with an error that says what to do.
func (c *Core) requireRepo(ctx context.Context, projectID string) error {
	p, err := c.GetProject(ctx, projectID)
	if err != nil {
		return err
	}
	if !p.HasRepo {
		return ErrNoRepository
	}
	return nil
}

type taskStartKey struct{ id string }

// lockTaskStart holds the task's start lock until the returned unlock: the check for an active run and the start that records
// the new run happen under it, so two starts of one task cannot both pass the check. StartTask and StartTemplate call each
// other; the lock travels in the context, so the inner call does not wait on its own caller.
func (c *Core) lockTaskStart(ctx context.Context, taskID string) (context.Context, func()) {
	if ctx.Value(taskStartKey{taskID}) != nil {
		return ctx, func() {}
	}
	v, _ := c.taskStarts.LoadOrStore(taskID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return context.WithValue(ctx, taskStartKey{taskID}, true), mu.Unlock
}

// StartTask starts the task's workflow: local_merge projects use iterate-until-verified, pr_flow projects the PR variant.
func (c *Core) StartTask(ctx context.Context, taskID string) (workflow.RunID, error) {
	if err := c.RequireLicensed(ctx, "start_task"); err != nil {
		return "", err
	}
	ctx, unlock := c.lockTaskStart(ctx, taskID)
	defer unlock()
	t, err := c.Tasks.Get(ctx, taskID)
	if err != nil {
		return "", err
	}
	if err := c.requireRepo(ctx, t.ProjectID); err != nil {
		return "", err
	}
	if t.Assignee == "" || t.Reviewer == "" {
		return "", errors.New("assign a worker and a reviewer before starting the task")
	}
	if t.RunID != "" {
		if info, err := c.Engine.Status(ctx, workflow.RunID(t.RunID)); err == nil && !info.Status.Terminal() {
			return "", fmt.Errorf("task already has an active run (%s)", info.Status)
		}
	}
	// a task created with a template (spec-gated feature, bug, security...) starts through it: starting it any other way
	// would skip the spec gate
	if t.Template != "" && t.Template != "iterate-until-verified" {
		if _, known := pipeline.TemplateByName(t.Template); known {
			res, err := c.StartTemplate(ctx, StartTemplateReq{TaskID: t.ID, Template: t.Template})
			if err != nil {
				return "", err
			}
			return res.RunID, nil
		}
	}
	p, err := c.GetProject(ctx, t.ProjectID)
	if err != nil {
		return "", err
	}
	name := "iterate-until-verified"
	if p.Mode == "pr_flow" {
		name = "iterate-until-verified-pr"
	}
	id, err := c.Engine.Start(ctx, name, pipeline.Input{TaskID: t.ID}, workflow.StartOptions{ProjectID: t.ProjectID, TaskID: t.ID})
	if err != nil {
		return "", err
	}
	rid := string(id)
	_, _ = c.Tasks.Patch(ctx, t.ID, tasks.Update{RunID: &rid})
	return id, nil
}

// TaskDetail is everything the task view shows.
type TaskDetail struct {
	Task     tasks.Task                `json:"task"`
	Evidence []tasks.Evidence          `json:"evidence"`
	Reviews  []tasks.ReviewRecord      `json:"reviews"`
	Run      *workflow.RunInfo         `json:"run,omitempty"`
	Pending  []workflow.PendingRequest `json:"pending"`
	RoomID   string                    `json:"roomId,omitempty"`
	// AllowRules are the person's "approve for this task" rules in force for the task's agents (P97); they end with the task.
	AllowRules []security.TaskAllowRule `json:"allowRules"`
}

// GetTask returns the task with its evidence, reviews, run and pending human requests.
func (c *Core) GetTask(ctx context.Context, id string) (TaskDetail, error) {
	t, err := c.Tasks.Get(ctx, id)
	if err != nil {
		return TaskDetail{}, err
	}
	d := TaskDetail{Task: t, Pending: []workflow.PendingRequest{}, AllowRules: []security.TaskAllowRule{}}
	if rules, err := c.TaskRules(ctx, id); err == nil {
		d.AllowRules = rules
	}
	d.Evidence, _ = c.Tasks.Evidence(ctx, id)
	d.Reviews, _ = c.Tasks.Reviews(ctx, id)
	if t.RunID != "" {
		if info, err := c.Engine.Status(ctx, workflow.RunID(t.RunID)); err == nil {
			d.Run = &info
			// a finished, failed or cancelled run waits for nobody: its last checkpoint may still list a request, which can no
			// longer be answered ("run is in a terminal state")
			if p, err := c.Engine.Pending(ctx, info.ID); err == nil && p != nil && !info.Status.Terminal() {
				d.Pending = p
			}
			// a worker's permission requests wait in its live turn, not in the run's checkpoint (ext_permprompt.go)
			if !info.Status.Terminal() {
				d.Pending = append(d.Pending, c.permissionPending(ctx, t.ID)...)
			}
		}
	}
	if r, err := c.Room.RoomByTask(ctx, id); err == nil {
		d.RoomID = r.ID
	}
	return d, nil
}

// TaskDiff returns the diff evidence of the task (the change reviewers and the human look at).
func (c *Core) TaskDiff(ctx context.Context, id string) (string, error) {
	return c.TaskDiffAt(ctx, id, 0)
}

// TaskDiffAt returns the diff evidence with that id, or the newest one when evidenceID is 0. A screen that shows a patch beside an
// Approve button asks for the evidence it keyed the card on, so a patch recorded since is never shown as that one (audit
// 2026-10-16, L36); an id that is not a diff evidence of this task is an error.
func (c *Core) TaskDiffAt(ctx context.Context, id string, evidenceID int64) (string, error) {
	ev, err := c.Tasks.Evidence(ctx, id)
	if err != nil {
		return "", err
	}
	for i := len(ev) - 1; i >= 0; i-- {
		if ev[i].Kind == "diff" && (evidenceID == 0 || ev[i].ID == evidenceID) {
			b, err := runfiles.ReadConfined(c.cfg.DataDir, ev[i].Ref)
			return string(b), err
		}
	}
	if evidenceID != 0 {
		return "", fmt.Errorf("evidence %d is not a diff of this task", evidenceID)
	}
	return "", nil
}

// Respond answers a pending request of a run as the human. Only the human identity is ever attached here: an agent has no
// path to this method.
func (c *Core) Respond(ctx context.Context, runID, requestID, decision string, data []byte) error {
	return c.RespondScoped(ctx, runID, requestID, decision, "", data)
}

// RespondScoped is Respond with the scope of an approval of an agent's tool permission request: "" or "once" for this request
// only, "task" for the rest of its task (P97). Any other request takes no scope.
func (c *Core) RespondScoped(ctx context.Context, runID, requestID, decision, scope string, data []byte) error {
	// Rejecting only refuses work, so it stays available in read-only mode (an approval does not).
	if decision != "rejected" {
		if err := c.RequireLicensed(ctx, "approve"); err != nil {
			return err
		}
	}
	if taskID, ok := c.permissionRequestTask(ctx, requestID); ok {
		return c.respondPermission(ctx, runID, requestID, taskID, decision, scope, data)
	}
	if scope != "" && scope != ScopeOnce {
		return errors.New("only an agent's tool permission request can be approved for the rest of the task")
	}
	c.trustOnApproval(ctx, runID, requestID, decision)
	review := c.isReviewRequest(ctx, runID, requestID)
	var d []byte = data
	if err := c.Engine.Resume(ctx, workflow.RunID(runID), workflow.Response{RequestID: workflow.RequestID(requestID), Decision: decision, By: c.Human(), Data: d}); err != nil {
		return err
	}
	if review && decision == "rejected" {
		c.learnFromReview(ctx, runID, data)
	}
	return nil
}

// isReviewRequest reports whether a pending request of a run is a review (where a person asks for changes in their own words).
func (c *Core) isReviewRequest(ctx context.Context, runID, requestID string) bool {
	ps, err := c.Engine.Pending(ctx, workflow.RunID(runID))
	if err != nil {
		return false
	}
	for _, p := range ps {
		if string(p.ID) == requestID {
			return p.Spec.Kind == "review"
		}
	}
	return false
}

// minLessonChars is the shortest change request kept as a memory proposal ("no", "redo" teach nothing).
const minLessonChars = 20

// learnFromReview turns a person's change request into a memory proposal (P92): what they asked for in their own words is
// what later work in the project should know. It is only a proposal: it reaches no agent until the person accepts it on
// the Memory page. A request too short to teach anything, or a project without memory, proposes nothing.
func (c *Core) learnFromReview(ctx context.Context, runID string, data []byte) {
	var r struct {
		Feedback string `json:"feedback"`
	}
	if json.Unmarshal(data, &r) != nil || len([]rune(strings.TrimSpace(r.Feedback))) < minLessonChars {
		return
	}
	k := c.Knowledge()
	if k == nil {
		return
	}
	info, err := c.Engine.Status(ctx, workflow.RunID(runID))
	if err != nil || info.TaskID == "" {
		return
	}
	t, err := c.Tasks.Get(ctx, info.TaskID)
	if err != nil {
		return
	}
	title := fmt.Sprintf("From the review of #%d %s", t.Seq, t.Title)
	if r := []rune(title); len(r) > 200 {
		title = string(r[:199]) + "…"
	}
	body := strings.TrimSpace(r.Feedback)
	if r := []rune(body); len(r) > 7900 {
		body = string(r[:7900])
	}
	if _, err := k.ProposeMemory(ctx, t.ProjectID, "preference", title, body, "review:"+t.ID); err != nil {
		c.logf("core: a memory proposal from the review of %s: %v", t.ID, err)
	}
}

// CancelTask stops the task's run and cancels the task.
func (c *Core) CancelTask(ctx context.Context, id string) error {
	t, err := c.Tasks.Get(ctx, id)
	if err != nil {
		return err
	}
	if t.RunID != "" {
		_ = c.Engine.Cancel(ctx, workflow.RunID(t.RunID))
	}
	_, err = c.Tasks.Transition(ctx, id, tasks.Cancelled, tasks.TransitionOpts{By: c.Human(), Reason: "cancelled by the human"})
	return err
}

// PostMessage posts a human message in a room.
func (c *Core) PostMessage(ctx context.Context, roomID, body, threadRoot string) (room.Posted, error) {
	return c.Room.Post(ctx, room.PostParams{RoomID: roomID, Type: room.TypeChat, Author: room.Human(c.cfg.User), Body: body, ThreadRootID: threadRoot})
}

// Replay returns persisted events after a sequence so the UI can catch up after a reconnect.
func (c *Core) Replay(ctx context.Context, after uint64, f events.Filter, limit int) ([]events.Event, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	return c.Bus.Replay(ctx, after, f, limit)
}

// DescribeAdapters reports every adapter's detection and verified capabilities.
func (c *Core) DescribeAdapters(ctx context.Context) []map[string]any {
	// detection starts each CLI: it runs outside c.mu so one slow or hung CLI does not stall the rest of the app
	c.mu.Lock()
	adapters := make(map[string]agents.Adapter, len(c.adapters))
	for id, a := range c.adapters {
		adapters[id] = a
	}
	c.mu.Unlock()
	var out []map[string]any
	for id, a := range adapters {
		det, err := a.Detect(ctx)
		m := map[string]any{"id": id, "detection": det, "capabilities": a.Capabilities()}
		if err != nil {
			m["error"] = err.Error()
		}
		if !det.Installed {
			// installed only inside a WSL distro: available there (decision P58)
			if ready := c.wslReady(ctx, id, false); len(ready) > 0 {
				m["wslDistro"] = ready[0].Distro
			}
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["id"].(string) < out[j]["id"].(string) })
	return out
}

// Activity counts what an update or shutdown would interrupt: workflow runs that are executing (a run waiting for the human
// survives a restart, so it does not count) and agent processes that are alive.
func (c *Core) Activity(ctx context.Context) (runs, liveAgents int, err error) {
	if err = c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM workflow_runs WHERE status IN ('RUNNING','RECOVERING','PENDING')`).Scan(&runs); err != nil {
		return
	}
	err = c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_runs WHERE ended_at=0`).Scan(&liveAgents)
	return
}

// UsageRow is one model usage record (token counts and cost with their source).
type UsageRow struct {
	TS         int64   `json:"ts"`
	TaskID     string  `json:"taskId"`
	AgentID    string  `json:"agentId"`
	Provider   string  `json:"provider"`
	Model      string  `json:"model"`
	Tier       string  `json:"tier"`
	Input      int     `json:"inputTokens"`
	Output     int     `json:"outputTokens"`
	Cached     int     `json:"cachedTokens"`
	CacheWrite int     `json:"cacheWriteTokens"`
	Reasoning  int     `json:"reasoningTokens"`
	CostUSD    float64 `json:"costUsd"`
	Source     string  `json:"source"` // reported | estimated
	CacheHit   bool    `json:"cacheHit"`
}

// Usage lists usage rows for a task or an agent (newest first). Cost and token numbers carry their source so the UI never
// presents an estimate as a reported value (brief 7).
func (c *Core) Usage(ctx context.Context, taskID, agentID string, limit int) ([]UsageRow, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT ts,task_id,agent_id,provider,model,tier,input_tokens,output_tokens,cached_tokens,cache_write_tokens,reasoning_tokens,cost_usd,source,cache_hit
		FROM model_usage WHERE (?='' OR task_id=?) AND (?='' OR agent_id=?) ORDER BY id DESC LIMIT ?`, taskID, taskID, agentID, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UsageRow{}
	for rows.Next() {
		var u UsageRow
		var hit int
		if err := rows.Scan(&u.TS, &u.TaskID, &u.AgentID, &u.Provider, &u.Model, &u.Tier, &u.Input, &u.Output, &u.Cached, &u.CacheWrite, &u.Reasoning, &u.CostUSD, &u.Source, &hit); err != nil {
			return nil, err
		}
		u.CacheHit = hit != 0
		out = append(out, u)
	}
	return out, rows.Err()
}

// AgentRun is one process lifetime of an agent (for the Agent Inspector).
type AgentRun struct {
	ID      string `json:"id"`
	TaskID  string `json:"taskId"`
	PID     int    `json:"pid"`
	Cwd     string `json:"cwd"`
	Status  string `json:"status"`
	Started int64  `json:"startedAt"`
	Ended   int64  `json:"endedAt"`
	Error   string `json:"error,omitempty"`
}

// UsageTotals is the sum of all of an agent's usage rows, not of the newest few: the Inspector lists the last 50 rows and
// says what was spent in all (audit 2026-10-16, M21).
type UsageTotals struct {
	Rows         int     `json:"rows"`
	CostUSD      float64 `json:"costUsd"`
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	// Estimated is how many of the rows have a cost that was estimated, not reported.
	Estimated int `json:"estimated"`
}

// AgentDetail is what the Agent Inspector shows.
type AgentDetail struct {
	Agent Agent      `json:"agent"`
	Runs  []AgentRun `json:"runs"`
	Usage []UsageRow `json:"usage"`
	// UsageTotals sums every usage row of the agent; Usage is only the newest rows.
	UsageTotals UsageTotals `json:"usageTotals"`
	// Isolation says, in plain words, what keeps this agent inside its project. It is never a promise of a sandbox.
	Isolation Isolation `json:"isolation"`
}

// GetAgent returns an agent with its recent runs and usage.
func (c *Core) GetAgent(ctx context.Context, id string) (AgentDetail, error) {
	a, err := c.agent(ctx, id)
	if err != nil {
		return AgentDetail{}, fmt.Errorf("agent %s not found", id)
	}
	d := AgentDetail{Agent: a, Runs: []AgentRun{}, Isolation: isolationOf(a, pipeline.LoadDevSettings(ctx, c.DB, a.ProjectID).WorkerChecks, c.sandboxSwitch(ctx, a.ProjectID).Enabled)}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT id,task_id,pid,cwd,status,started_at,ended_at,error FROM agent_runs WHERE agent_id=? ORDER BY started_at DESC LIMIT 20`, id)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var r AgentRun
		if err := rows.Scan(&r.ID, &r.TaskID, &r.PID, &r.Cwd, &r.Status, &r.Started, &r.Ended, &r.Error); err != nil {
			return d, err
		}
		d.Runs = append(d.Runs, r)
	}
	if d.Usage, err = c.Usage(ctx, "", id, 50); err != nil {
		return d, err
	}
	if err := c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(cost_usd),0), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(CASE WHEN source='estimated' THEN 1 ELSE 0 END),0) FROM model_usage WHERE agent_id=?`, id).
		Scan(&d.UsageTotals.Rows, &d.UsageTotals.CostUSD, &d.UsageTotals.InputTokens, &d.UsageTotals.OutputTokens, &d.UsageTotals.Estimated); err != nil {
		return d, err
	}
	return d, rows.Err()
}

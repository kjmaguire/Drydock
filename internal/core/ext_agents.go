package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/responder"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// This extension is Slice 4, "autonomous multi-agent development" (brief section 24): agents that answer in the Engineering
// Room, the workflow templates with the spec gate, PlanTeam, parallel execution within the concurrency limits, and the
// e2e and preview checks. It adds methods and shutdown hooks only; it changes none of the base services.

func init() {
	RegisterExtension(Extension{Name: "agents-dev", Start: startAgentsDev})
}

type agentsDev struct {
	c        *Core
	limiter  *pipeline.Limiter
	resp     *responder.Responder
	sched    *pipeline.Scheduler
	td       pipeline.TemplateDeps
	pd       pipeline.PlanDeps
	previews *previewManager
}

func startAgentsDev(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	a := &agentsDev{c: c, previews: &previewManager{c: c, running: map[string]*checks.PreviewHandle{}}}

	// concurrency: the same limiter bounds workflow agent turns and room replies
	a.limiter = pipeline.NewLimiter(
		func(ctx context.Context, projectID string) int {
			return pipeline.LoadDevSettings(ctx, c.DB, projectID).MaxParallel
		},
		func(ctx context.Context, providerID string) (func(), error) { return c.Health.Acquire(ctx, providerID) })
	// outermost: the wait for a slot must not count against the watchdog's per-turn wall clock, which starts inside it
	c.Engine.UseOutermost(workflow.KindAgentTask, a.limiter.Middleware(a.providerOfNode))
	// a preview serves from the task worktree; it must be gone before the merge removes that worktree
	c.Engine.Use(workflow.KindRoute, func(next workflow.NodeHandler) workflow.NodeHandler {
		return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
			if in.NodeID == "merge" && in.TaskID != "" {
				a.previews.stop(in.TaskID)
			}
			return next(ctx, in)
		}
	})

	// templates: a copy of the base dependencies with the template-aware callbacks
	d := c.Deps
	d.Scanner = c.Scanner
	d.OnCheck = a.onCheck
	a.td = pipeline.TemplateDeps{Deps: d, Project: c.projectInfo, RoleConfig: c.roleConfig,
		Dev: func(ctx context.Context, projectID string) pipeline.DevSettings {
			return pipeline.LoadDevSettings(ctx, c.DB, projectID)
		},
		OnInconclusive: a.onInconclusive, Preview: a.previews.run, MaxLoops: 5, PR: c.prSource}
	if err := pipeline.RegisterTemplates(c.Engine, a.td); err != nil {
		return nil, nil, err
	}
	a.pd = pipeline.PlanDeps{Deps: d, PlannerConfig: a.plannerConfig, Team: a.team, Assign: a.assignByRole}
	if err := c.Engine.RegisterNew(a.pd.PlanGraph()); err != nil {
		return nil, nil, err
	}

	// the scheduler: starts READY tasks within the limits; automatic only when a project opts in
	a.sched = pipeline.NewScheduler(pipeline.SchedulerDeps{Tasks: c.Tasks, DB: c.DB, Bus: c.Bus,
		Dev: func(ctx context.Context, projectID string) pipeline.DevSettings {
			return pipeline.LoadDevSettings(ctx, c.DB, projectID)
		},
		Start: a.startScheduled,
		Provider: func(ctx context.Context, agentID string) string {
			if ag, err := c.agent(ctx, agentID); err == nil {
				return ag.ProviderID
			}
			return ""
		},
		ProviderSlots: func(ctx context.Context, providerID string) (int, bool, error) {
			st, err := c.Health.Status(ctx, providerID)
			if err != nil {
				return 0, false, err
			}
			if !st.Enabled {
				return 0, true, nil
			}
			if st.MaxConcurrent == 0 {
				return 0, false, nil
			}
			return st.MaxConcurrent - st.InUse, true, nil
		}})
	loopCtx, cancelLoop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.sched.Loop(loopCtx, 5*time.Second, func(ctx context.Context) []string {
			ps, err := c.Projects(ctx)
			if err != nil {
				return nil
			}
			ids := make([]string, len(ps))
			for i, p := range ps {
				ids[i] = p.ID
			}
			return ids
		})
	}()
	wg.Add(1)
	go func() { defer wg.Done(); a.previews.watchTerminal(loopCtx) }()

	// agents in the room
	a.resp = responder.New(responder.Deps{
		Room: c.Room, Runtime: c.Runtime, Context: c.Context, DB: c.DB, Bus: c.Bus,
		LookupAgent: func(ctx context.Context, id string) (responder.Agent, error) {
			ag, err := c.agent(ctx, id)
			if err != nil {
				return responder.Agent{}, err
			}
			if ag.Archived {
				return responder.Agent{}, errors.New("this agent was removed from the team")
			}
			return responder.Agent{ID: ag.ID, ProjectID: ag.ProjectID, Name: ag.Name, Adapter: ag.Adapter, Role: ag.Role, Model: ag.Model, Effort: ag.Effort, ProviderID: ag.ProviderID}, nil
		},
		Cwd:   a.replyCwd,
		Brief: c.ProjectBrief,
		Gate: func(ctx context.Context, projectID string, ag responder.Agent) (func(), error) {
			return a.limiter.Acquire(ctx, projectID, ag.ProviderID)
		},
		Budget: func(ctx context.Context, projectID, taskID string, ag responder.Agent) error {
			return EconomicsBudgetBlock(ctx, c, projectID, taskID, ag.ID)
		},
		Enabled: func(ctx context.Context, projectID string) bool {
			// an agent reply is work started on the human's behalf: off while the license is expired (read-only)
			return c.RequireLicensed(ctx, "room_reply") == nil && pipeline.LoadDevSettings(ctx, c.DB, projectID).Respond
		},
		OnCost: func(ctx context.Context, taskID string, usd float64) {
			_, _ = c.Tasks.Patch(ctx, taskID, tasks.Update{AddCost: usd})
		},
		Logf: c.logf,
	}, responder.Options{})
	stopResp, err := a.resp.Start(ctx)
	if err != nil {
		cancelLoop()
		wg.Wait()
		return nil, nil, err
	}
	closeFn := func() {
		cancelLoop()
		stopResp()
		wg.Wait()
		a.previews.stopAll()
	}
	return a.methods(), closeFn, nil
}

// ---- callbacks ----

// providerOfNode names the provider of the agent a node is about to run, for the per-provider concurrency limit.
func (a *agentsDev) providerOfNode(ctx context.Context, in workflow.NodeInput) string {
	agentID := ""
	switch in.Meta["agent"] {
	case "work", "review":
		if in.TaskID == "" {
			return ""
		}
		t, err := a.c.Tasks.Get(ctx, in.TaskID)
		if err != nil {
			return ""
		}
		agentID = t.Assignee
		if in.Meta["agent"] == "review" {
			agentID = t.Reviewer
		}
	case "planner":
		var pi pipeline.PlanInput
		if json.Unmarshal(in.Input, &pi) == nil {
			agentID = pi.PlannerAgent
		}
	default:
		var ti pipeline.TemplateInput
		if json.Unmarshal(in.Input, &ti) == nil {
			agentID = ti.Roles[in.Meta["agent"]]
		}
	}
	if agentID == "" {
		return ""
	}
	if ag, err := a.c.agent(ctx, agentID); err == nil {
		return ag.ProviderID
	}
	return ""
}

// onCheck posts a check as its tool member. The e2e and preview results carry their summary or URL even when they pass.
func (a *agentsDev) onCheck(ctx context.Context, t tasks.Task, r checks.Result) {
	if r.Name != "preview" && r.Name != "e2e" {
		a.c.postCheck(ctx, t, r)
		return
	}
	rm, err := a.c.Room.RoomByTask(ctx, t.ID)
	if err != nil {
		return
	}
	status := room.CheckPass
	if !r.Passed {
		status = room.CheckFail
	}
	if _, err := a.c.Room.PostCheckResult(ctx, rm.ID, room.CheckResult{Kind: r.Name, Status: status, DurationMS: r.Duration.Milliseconds(), CommandDigest: digest(strings.Join(r.Argv, " ")),
		Summary: r.Output, LogRef: nonEmpty(r.LogRef, "(no log)"), DiffHash: r.DiffHash, TaskID: t.ID}); err != nil {
		a.c.logf("post %s: %v", r.Name, err)
	}
}

// onInconclusive shows a skipped or errored check in the room: the member says it did not run, never that it passed.
func (a *agentsDev) onInconclusive(ctx context.Context, t tasks.Task, r checks.Result) {
	if _, ok := room.ToolForCheck(r.Name); !ok {
		return
	}
	rm, err := a.c.Room.RoomByTask(ctx, t.ID)
	if err != nil {
		return
	}
	status := room.CheckSkipped
	if r.Errored {
		status = room.CheckError
	}
	if _, err := a.c.Room.PostCheckResult(ctx, rm.ID, room.CheckResult{Kind: r.Name, Status: status, Summary: nonEmpty(r.SkipReason, r.Output), LogRef: nonEmpty(r.LogRef, "(not run)"), TaskID: t.ID}); err != nil {
		a.c.logf("post %s %s: %v", r.Name, status, err)
	}
}

// replyCwd is the task worktree in a task room (when it exists), otherwise the project root (or the project's private workspace
// folder while it has no repository).
func (a *agentsDev) replyCwd(ctx context.Context, rm room.Room) (string, error) {
	if rm.TaskID != "" {
		if t, err := a.c.Tasks.Get(ctx, rm.TaskID); err == nil && t.Worktree != "" {
			if st, err := os.Stat(t.Worktree); err == nil && st.IsDir() {
				return t.Worktree, nil
			}
		}
	}
	p, err := a.c.GetProject(ctx, rm.ProjectID)
	if err != nil {
		return "", err
	}
	return a.c.projectDir(p)
}

// roleConfig builds the runtime config for an agent filling a template role.
func (c *Core) roleConfig(ctx context.Context, t tasks.Task, role, agentID string) (rt.Config, error) {
	if agentID == "" {
		return rt.Config{}, fmt.Errorf("no agent is pinned for the %s role of this run", role)
	}
	a, err := c.agent(ctx, agentID)
	if err != nil || a.ProjectID != t.ProjectID {
		return rt.Config{}, fmt.Errorf("the agent %s for the %s role is not on this project's team", agentID, role)
	}
	spec := agents.RunSpec{AgentID: a.ID, Role: a.Role, Model: a.Model, Effort: a.Effort, PermissionMode: a.PermissionMode}
	return rt.Config{AgentID: a.ID, Adapter: a.Adapter, Spec: spec, Provider: providerLabel(a), Tier: "premium"}, nil
}

func (a *agentsDev) team(ctx context.Context, projectID string) ([]pipeline.TeamMember, error) {
	ags, err := a.c.Agents(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]pipeline.TeamMember, len(ags))
	for i, ag := range ags {
		out[i] = pipeline.TeamMember{ID: ag.ID, Name: ag.Name, Role: ag.Role, Adapter: ag.Adapter}
	}
	return out, nil
}

func (a *agentsDev) plannerConfig(ctx context.Context, projectID, agentID string) (rt.Config, error) {
	if agentID == "" {
		return rt.Config{}, errors.New("no planner agent")
	}
	ag, err := a.c.agent(ctx, agentID)
	if err != nil || ag.ProjectID != projectID {
		return rt.Config{}, fmt.Errorf("the planner agent %s is not on this project's team", agentID)
	}
	return rt.Config{AgentID: ag.ID, Adapter: ag.Adapter, Spec: agents.RunSpec{AgentID: ag.ID, Role: ag.Role, Model: ag.Model, Effort: ag.Effort, PermissionMode: "plan", ReadOnly: true},
		Provider: providerLabel(ag), Tier: "premium"}, nil
}

// assignByRole sets the worker (the least loaded agent whose role matches) and an independent reviewer of a planned task.
func (a *agentsDev) assignByRole(ctx context.Context, t tasks.Task, role string) error {
	ags, err := a.c.Agents(ctx, t.ProjectID)
	if err != nil {
		return err
	}
	load := func(id string) int {
		ts, _ := a.c.Tasks.List(ctx, tasks.Filter{ProjectID: t.ProjectID, Assignee: id})
		n := 0
		for _, x := range ts {
			if !x.Status.Terminal() {
				n++
			}
		}
		return n
	}
	var worker, reviewer string
	best := 1 << 30
	for _, ag := range ags {
		if pipeline.NormalizePlanRole(ag.Role) == role {
			if l := load(ag.ID); l < best {
				worker, best = ag.ID, l
			}
		}
	}
	if worker == "" {
		return fmt.Errorf("no agent with the role %s", role)
	}
	for _, ag := range ags {
		if ag.ID != worker && pipeline.NormalizePlanRole(ag.Role) == "reviewer" {
			reviewer = ag.ID
			break
		}
	}
	if reviewer == "" {
		for _, ag := range ags {
			if ag.ID != worker && pipeline.NormalizePlanRole(ag.Role) != "pm" && !pipeline.IsLeadRole(ag.Role) { // the lead supervises, never reviews
				reviewer = ag.ID
				break
			}
		}
	}
	if reviewer == "" {
		return errors.New("no second agent to review the work")
	}
	_, err = a.c.AssignTask(ctx, t.ID, worker, reviewer)
	return err
}

// startScheduled starts a READY task: its template when it has one, otherwise the default workflow.
func (a *agentsDev) startScheduled(ctx context.Context, t tasks.Task) (workflow.RunID, error) {
	if t.Template != "" && t.Template != pipeline.WorkflowIUV {
		res, err := a.c.StartTemplate(ctx, StartTemplateReq{TaskID: t.ID, Template: t.Template})
		if err != nil {
			return "", err
		}
		return res.RunID, nil
	}
	return a.c.StartTask(ctx, t.ID)
}

// ---- templates ----

// StartTemplateReq starts a task in a workflow template.
type StartTemplateReq struct {
	TaskID   string            `json:"taskId"`
	Template string            `json:"template"`
	Agents   map[string]string `json:"agents,omitempty"` // role -> agent id overrides: work, review, pm, tests, architect, security
	Scans    []string          `json:"scans,omitempty"`  // security template: scan checks to run
}

// StartTemplateResult is what the UI shows after starting.
type StartTemplateResult struct {
	RunID    workflow.RunID    `json:"runId"`
	Template string            `json:"template"`
	Workflow string            `json:"workflow"`
	Roles    map[string]string `json:"roles"`
	Notes    []string          `json:"notes,omitempty"`
}

// StartTemplate resolves the template's roles from the team (overrides win), pins them in the run input and starts the
// workflow. Templates set defaults, not locks: the human can override any role and the settings can switch checks.
func (c *Core) StartTemplate(ctx context.Context, req StartTemplateReq) (StartTemplateResult, error) {
	if err := c.RequireLicensed(ctx, "start_template"); err != nil {
		return StartTemplateResult{}, err
	}
	tpl, ok := pipeline.TemplateByName(req.Template)
	if !ok {
		names := []string{}
		for _, t := range pipeline.Templates() {
			names = append(names, t.Name)
		}
		return StartTemplateResult{}, fmt.Errorf("unknown template %q (have %s)", req.Template, strings.Join(names, ", "))
	}
	ctx, unlock := c.lockTaskStart(ctx, req.TaskID)
	defer unlock()
	t, err := c.Tasks.Get(ctx, req.TaskID)
	if err != nil {
		return StartTemplateResult{}, err
	}
	if err := c.requireRepo(ctx, t.ProjectID); err != nil {
		return StartTemplateResult{}, err
	}
	if w, r := req.Agents[pipeline.RoleWork], req.Agents[pipeline.RoleReview]; w != "" || r != "" {
		w, r = firstNonEmpty(w, t.Assignee), firstNonEmpty(r, t.Reviewer)
		if t, err = c.AssignTask(ctx, t.ID, w, r); err != nil {
			return StartTemplateResult{}, err
		}
	}
	if tpl.Workflow == pipeline.WorkflowIUV {
		id, err := c.StartTask(ctx, t.ID)
		return StartTemplateResult{RunID: id, Template: tpl.Name, Workflow: pipeline.WorkflowIUV, Roles: map[string]string{}}, err
	}
	if t.Assignee == "" || t.Reviewer == "" {
		return StartTemplateResult{}, errors.New("assign a worker and a reviewer before starting the task (task.assign, or agents.work and agents.review here)")
	}
	p, err := c.GetProject(ctx, t.ProjectID)
	if err != nil {
		return StartTemplateResult{}, err
	}
	workflowName := tpl.Workflow
	if p.Mode == "pr_flow" {
		workflowName += pipeline.PRSuffix // push, PR, CI and the merge through GitHub instead of a local merge
	}
	if t.RunID != "" {
		if info, err := c.Engine.Status(ctx, workflow.RunID(t.RunID)); err == nil && !info.Status.Terminal() {
			return StartTemplateResult{}, fmt.Errorf("task already has an active run (%s)", info.Status)
		}
	}
	ags, err := c.Agents(ctx, t.ProjectID)
	if err != nil {
		return StartTemplateResult{}, err
	}
	team := make([]pipeline.TeamMember, len(ags))
	for i, ag := range ags {
		team[i] = pipeline.TeamMember{ID: ag.ID, Name: ag.Name, Role: ag.Role}
	}
	overrides := map[string]string{}
	for k, v := range req.Agents {
		if k != pipeline.RoleWork && k != pipeline.RoleReview && v != "" {
			overrides[k] = v
		}
	}
	res, err := pipeline.ResolveRoles(tpl.Roles, t.Assignee, t.Reviewer, team, overrides)
	if err != nil {
		return StartTemplateResult{}, err
	}
	// the role agents post their turns in the task channel, so they join it (a human action: starting the template)
	if rm, err := c.Room.RoomByTask(ctx, t.ID); err == nil {
		for role, id := range res.Roles {
			if err := c.Room.AddMember(ctx, rm.ID, room.Agent(id), role, room.StateActive, room.Human(c.cfg.User)); err != nil {
				c.logf("add %s to the task channel: %v", id, err)
			}
		}
	}
	id, err := c.Engine.Start(ctx, workflowName, pipeline.TemplateInput{TaskID: t.ID, Roles: res.Roles, Scans: req.Scans}, workflow.StartOptions{ProjectID: t.ProjectID, TaskID: t.ID})
	if err != nil {
		return StartTemplateResult{}, err
	}
	rid := string(id)
	_, _ = c.Tasks.Patch(ctx, t.ID, tasks.Update{RunID: &rid})
	return StartTemplateResult{RunID: id, Template: tpl.Name, Workflow: workflowName, Roles: res.Roles, Notes: res.Notes}, nil
}

// ---- plans ----

// ProposePlan starts the planner: a read-only agent turn whose reply is validated and then put to the human.
func (a *agentsDev) proposePlan(ctx context.Context, projectID, goal, requirements, plannerID string) (map[string]any, error) {
	c := a.c
	if _, err := c.GetProject(ctx, projectID); err != nil {
		return nil, err
	}
	ags, err := c.Agents(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if plannerID == "" {
		for _, want := range []string{"pm", "architect"} {
			for _, ag := range ags {
				if pipeline.RoleMatches(ag.Role, want) {
					plannerID = ag.ID
					break
				}
			}
			if plannerID != "" {
				break
			}
		}
	}
	if plannerID == "" {
		return nil, errors.New("no planner: add an agent with the pm or architect role, or pass plannerAgentId")
	}
	pi, err := pipeline.NewPlan(ctx, c.DB, projectID, goal, requirements, plannerID)
	if err != nil {
		return nil, err
	}
	id, err := c.Engine.Start(ctx, pipeline.WorkflowPlan, pi, workflow.StartOptions{ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	_ = pipeline.SetPlanRun(ctx, c.DB, pi.PlanID, string(id))
	return map[string]any{"planId": pi.PlanID, "runId": id, "plannerAgentId": plannerID}, nil
}

// answerPlan answers the plan's review request as the human.
func (a *agentsDev) answerPlan(ctx context.Context, planID, decision string, data any) (pipeline.Plan, error) {
	p, err := pipeline.GetPlan(ctx, a.c.DB, planID)
	if err != nil {
		return p, err
	}
	if p.RunID == "" {
		return p, errors.New("the plan has no run")
	}
	// the proposal is stored a moment before the run commits the review request: wait for the request rather than refuse
	var req workflow.RequestID
	deadline := time.Now().Add(15 * time.Second)
	for {
		pend, err := a.c.Engine.Pending(ctx, workflow.RunID(p.RunID))
		if err != nil {
			return p, err
		}
		for _, pr := range pend {
			if pr.NodeID == "planreview" {
				req = pr.ID
			}
		}
		if req != "" || time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
		if info, err := a.c.Engine.Status(ctx, workflow.RunID(p.RunID)); err == nil && info.Status.Terminal() {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if req == "" {
		cur, _ := pipeline.GetPlan(ctx, a.c.DB, planID)
		return cur, fmt.Errorf("the plan is %s and is not waiting for a review", cur.Status)
	}
	raw, _ := json.Marshal(data)
	if err := a.c.Respond(ctx, p.RunID, string(req), decision, raw); err != nil {
		return p, err
	}
	// the create step runs in the background; wait for it so the caller sees the tasks
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		cur, err := pipeline.GetPlan(ctx, a.c.DB, planID)
		if err != nil {
			return cur, err
		}
		if cur.Status == "ACCEPTED" || cur.Status == "REJECTED" || cur.Status == "FAILED" {
			return cur, nil
		}
		if info, err := a.c.Engine.Status(ctx, workflow.RunID(p.RunID)); err == nil && info.Status.Terminal() {
			return cur, nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return pipeline.GetPlan(ctx, a.c.DB, planID)
}

// ---- previews ----

// previewManager keeps the running previews, one per task, and stops them when the task ends or the app closes.
type previewManager struct {
	c       *Core
	mu      sync.Mutex
	running map[string]*checks.PreviewHandle
}

func (m *previewManager) run(ctx context.Context, t tasks.Task, dev pipeline.DevSettings) (checks.PreviewResult, error) {
	m.stop(t.ID)
	timeout := time.Duration(dev.Preview.TimeoutSec) * time.Second
	h, res, err := checks.StartPreview(ctx, m.c.cfg.Procs, t.Worktree, checks.PreviewOptions{Enabled: dev.Preview.Enabled, Command: dev.Preview.Command, HealthURL: dev.Preview.URL,
		Timeout: timeout, LogDir: filepath.Join(m.c.cfg.DataDir, "runs", "previews", t.ID)}, nil)
	if err != nil {
		return res, err
	}
	if h != nil {
		m.mu.Lock()
		m.running[t.ID] = h
		m.mu.Unlock()
		m.c.publish(ctx, "preview.started", t.ProjectID, map[string]any{"taskId": t.ID, "url": res.URL, "pid": h.PID})
	}
	return res, nil
}

func (m *previewManager) stop(taskID string) bool {
	m.mu.Lock()
	h := m.running[taskID]
	delete(m.running, taskID)
	m.mu.Unlock()
	if h == nil {
		return false
	}
	h.Stop()
	select {
	case <-h.Exited():
	case <-time.After(5 * time.Second):
	}
	return true
}

func (m *previewManager) stopAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.running))
	for id := range m.running {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.stop(id)
	}
}

func (m *previewManager) list() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []map[string]any{}
	for id, h := range m.running {
		out = append(out, map[string]any{"taskId": id, "url": h.URL, "pid": h.PID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["taskId"].(string) < out[j]["taskId"].(string) })
	return out
}

// watchTerminal stops the preview of a task that completed, failed or was cancelled.
func (m *previewManager) watchTerminal(ctx context.Context) {
	f := events.Filter{Types: []string{"task.completed", "task.failed", "task.cancelled"}}
	ch, cancel := m.c.Bus.Subscribe(f)
	defer func() { cancel() }() // the subscription taken last, after a re-subscribe
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				cancel()
				ch, cancel = m.c.Bus.Subscribe(f)
				continue
			}
			if e.TaskID != "" {
				m.stop(e.TaskID)
			}
		}
	}
}

// ---- methods ----

func (a *agentsDev) methods() map[string]Method {
	c := a.c
	return map[string]Method{
		// templates
		"template.list":      bind(func(ctx context.Context, _ struct{}) (any, error) { return pipeline.Templates(), nil }),
		"task.startTemplate": bind(func(ctx context.Context, p StartTemplateReq) (any, error) { return c.StartTemplate(ctx, p) }),

		// agents in the room
		"room.setMode": bind(func(ctx context.Context, p struct {
			RoomID      string   `json:"roomId"`
			Mode        string   `json:"mode"`
			Order       []string `json:"order"`
			Moderator   string   `json:"moderator"`
			MaxRounds   int      `json:"maxRounds"`
			MaxParallel int      `json:"maxParallel"`
		}) (any, error) {
			return a.setMode(ctx, p.RoomID, p.Mode, room.ModeParams{Order: p.Order, Moderator: p.Moderator, MaxRounds: p.MaxRounds, MaxParallel: p.MaxParallel})
		}),
		"room.invite": bind(func(ctx context.Context, p struct {
			RoomID  string `json:"roomId"`
			AgentID string `json:"agentId"`
			Role    string `json:"role"`
			State   string `json:"state"`
		}) (any, error) {
			ag, err := c.agent(ctx, p.AgentID)
			if err != nil {
				return nil, fmt.Errorf("agent %s not found", p.AgentID)
			}
			st := room.MemberState(firstNonEmpty(p.State, string(room.StateActive)))
			if err := c.Room.AddMember(ctx, p.RoomID, room.Agent(ag.ID), firstNonEmpty(p.Role, ag.Role), st, room.Human(c.cfg.User)); err != nil {
				return nil, err
			}
			return c.Room.Members(ctx, p.RoomID)
		}),
		"room.removeMember": bind(func(ctx context.Context, p struct {
			RoomID  string `json:"roomId"`
			AgentID string `json:"agentId"`
		}) (any, error) {
			if err := c.Room.RemoveMember(ctx, p.RoomID, room.Agent(p.AgentID), room.Human(c.cfg.User)); err != nil {
				return nil, err
			}
			return c.Room.Members(ctx, p.RoomID)
		}),
		"room.turns": bind(func(ctx context.Context, p struct {
			RoomID string `json:"roomId"`
			Limit  int    `json:"limit"`
		}) (any, error) {
			return a.resp.Turns(ctx, p.RoomID, p.Limit)
		}),
		"room.reply": bind(func(ctx context.Context, p struct {
			MessageID string `json:"messageId"`
		}) (any, error) {
			return a.resp.Handle(ctx, p.MessageID)
		}),

		// PlanTeam
		"plan.propose": bind(func(ctx context.Context, p struct {
			ProjectID      string `json:"projectId"`
			Goal           string `json:"goal"`
			Requirements   string `json:"requirements"`
			PlannerAgentID string `json:"plannerAgentId"`
		}) (any, error) {
			return a.proposePlan(ctx, p.ProjectID, p.Goal, p.Requirements, p.PlannerAgentID)
		}),
		"plan.get": bind(func(ctx context.Context, p struct {
			PlanID string `json:"planId"`
		}) (any, error) {
			return pipeline.GetPlan(ctx, c.DB, p.PlanID)
		}),
		"plan.list": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			return pipeline.ListPlans(ctx, c.DB, p.ProjectID)
		}),
		"plan.accept": bind(func(ctx context.Context, p struct {
			PlanID string                           `json:"planId"`
			Accept []string                         `json:"accept"`
			Edits  map[string]pipeline.ProposedTask `json:"edits"`
		}) (any, error) {
			return a.answerPlan(ctx, p.PlanID, "approved", pipeline.PlanAcceptData{Accept: p.Accept, Edits: p.Edits})
		}),
		"plan.reject": bind(func(ctx context.Context, p struct {
			PlanID string `json:"planId"`
		}) (any, error) {
			return a.answerPlan(ctx, p.PlanID, "rejected", map[string]string{})
		}),

		// parallel execution and project settings
		"scheduler.tick": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			return a.sched.Tick(ctx, p.ProjectID)
		}),
		"scheduler.status": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			dev := pipeline.LoadDevSettings(ctx, c.DB, p.ProjectID)
			var active int
			_ = c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM workflow_runs WHERE project_id=? AND task_id<>'' AND status IN ('PENDING','RUNNING','RECOVERING')`, p.ProjectID).Scan(&active)
			return map[string]any{"settings": dev, "activeRuns": active, "agentSlotsInUse": a.limiter.InUse(p.ProjectID), "peakAgentSlots": a.limiter.Peak(p.ProjectID)}, nil
		}),
		// a task waits for an earlier one that changes the same code (pipeline.Guard); the human can let it go on
		"task.guard.release": bind(func(ctx context.Context, p idParam) (any, error) {
			if err := pipeline.ReleaseGuard(ctx, c.Deps, p.ID, c.Human()); err != nil {
				return nil, err
			}
			return map[string]bool{"released": true}, nil
		}),
		"projectdev.get": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			return pipeline.LoadDevSettings(ctx, c.DB, p.ProjectID), nil
		}),
		"projectdev.set": bind(func(ctx context.Context, p struct {
			ProjectID string               `json:"projectId"`
			Settings  pipeline.DevSettings `json:"settings"`
		}) (any, error) {
			if _, err := c.GetProject(ctx, p.ProjectID); err != nil {
				return nil, err
			}
			if err := pipeline.SaveDevSettings(ctx, c.DB, p.ProjectID, p.Settings, c.Human()); err != nil {
				return nil, err
			}
			return pipeline.LoadDevSettings(ctx, c.DB, p.ProjectID), nil
		}),

		// the e2e and preview checks, on demand
		"check.e2e": bind(func(ctx context.Context, p idParam) (any, error) { return a.runE2E(ctx, p.ID) }),
		"preview.start": bind(func(ctx context.Context, p idParam) (any, error) {
			t, err := c.Tasks.Get(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			if t.Worktree == "" {
				return nil, errors.New("the task has no worktree yet")
			}
			if c.sandboxSwitch(ctx, t.ProjectID).Enabled {
				return nil, errSandboxedHere
			}
			res, err := a.previews.run(ctx, t, pipeline.LoadDevSettings(ctx, c.DB, t.ProjectID))
			if err != nil {
				return nil, err
			}
			if res.Skipped {
				a.onInconclusive(ctx, t, res.Result)
			} else {
				a.onCheck(ctx, t, res.Result)
				pass := res.Passed
				_, _ = c.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: "preview", Passed: &pass, Summary: nonEmpty(res.Output, "preview"), Ref: res.URL})
			}
			return res, nil
		}),
		"preview.stop": bind(func(ctx context.Context, p idParam) (any, error) {
			return map[string]bool{"stopped": a.previews.stop(p.ID)}, nil
		}),
		"preview.list": bind(func(ctx context.Context, _ struct{}) (any, error) { return a.previews.list(), nil }),
	}
}

func (a *agentsDev) setMode(ctx context.Context, roomID, mode string, params room.ModeParams) (room.Room, error) {
	rm, err := a.c.Room.GetRoom(ctx, roomID)
	if err != nil {
		return rm, err
	}
	members, err := a.c.Room.Members(ctx, roomID)
	if err != nil {
		return rm, err
	}
	have := map[string]bool{}
	for _, m := range members {
		have[m.Author.Key()] = true
	}
	for _, k := range params.Order {
		if !have[k] {
			return rm, fmt.Errorf("%s is not a member of this room, so it cannot be in the speaking order", k)
		}
	}
	if params.Moderator != "" && !have[params.Moderator] {
		return rm, fmt.Errorf("the moderator %s is not a member of this room", params.Moderator)
	}
	if room.Mode(mode) == room.ModeModerator && params.Moderator == "" {
		return rm, errors.New("moderator mode needs a moderator (an agent or the human)")
	}
	return a.c.Room.SetMode(ctx, roomID, room.Mode(mode), params, rm.Version, room.Human(a.c.cfg.User))
}

// runE2E runs the project's e2e check in the task worktree now, records the evidence and posts it as the Tests member.
func (a *agentsDev) runE2E(ctx context.Context, taskID string) (checks.E2EResult, error) {
	c := a.c
	t, err := c.Tasks.Get(ctx, taskID)
	if err != nil {
		return checks.E2EResult{}, err
	}
	if t.Worktree == "" {
		return checks.E2EResult{}, errors.New("the task has no worktree yet")
	}
	if c.sandboxSwitch(ctx, t.ProjectID).Enabled {
		return checks.E2EResult{}, errSandboxedHere
	}
	dev := pipeline.LoadDevSettings(ctx, c.DB, t.ProjectID)
	logDir := filepath.Join(c.cfg.DataDir, "runs", "e2e", t.ID)
	res, err := checks.RunE2E(ctx, c.cfg.Procs, t.Worktree, checks.E2EOptions{Enabled: dev.E2E.Enabled, Command: dev.E2E.Command, Timeout: time.Duration(dev.E2E.TimeoutSec) * time.Second, LogDir: logDir}, nil)
	if err != nil {
		return res, err
	}
	if res.Inconclusive() {
		a.onInconclusive(ctx, t, res.Result)
		_, _ = c.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: "e2e", Summary: "e2e skipped: " + nonEmpty(res.SkipReason, res.Output)})
		return res, nil
	}
	pass := res.Passed
	_, _ = c.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: "e2e", Passed: &pass, Summary: nonEmpty(res.Output, "e2e"), Ref: res.LogRef})
	for _, s := range res.Screenshots {
		_, _ = c.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: "screenshot", Summary: filepath.Base(s), Ref: s})
	}
	a.onCheck(ctx, t, res.Result)
	return res, nil
}

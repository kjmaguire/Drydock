package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/watchdog"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The controls a person uses on a task and its agents (the task page and the command menu): steer the running agent, pause
// and resume the task, hand it to another agent or take it on themselves (task.assign, ops.go), ask the team lead to split it
// (lead.split, ext_lead.go), and see what an assignment would do before making it. Each is a person's action (isPerson).
//
// Pause is the engine's own pause (workflow.Engine.Pause): the run stops at once, an agent turn in flight is stopped, the
// worktree stays as it is, and resume goes on from the run's latest checkpoint, redoing the step that was in flight. The task's
// status does not change: BLOCKED is reachable only from IN_PROGRESS, and the step redone on resume makes the transitions its
// status calls for (IN_PROGRESS to REVIEW, REVIEW to VERIFICATION), which a task parked in BLOCKED would refuse. The run's
// PAUSED status is what says the task is paused (TaskDetail.Paused), with presence Paused for its worker; the scheduler and the
// team lead's monitor already leave a paused run alone.

func init() {
	RegisterExtension(Extension{Name: "control", After: []string{"agents-dev"}, Start: startControl})
}

type controlExt struct {
	c *Core
	// sched applies the scheduler's rules to one task without starting anything (WouldWait); it never runs a pass
	sched *pipeline.Scheduler
}

func startControl(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	x := &controlExt{c: c, sched: pipeline.NewScheduler(pipeline.SchedulerDeps{Tasks: c.Tasks, DB: c.DB, Bus: c.Bus,
		Dev: func(ctx context.Context, projectID string) pipeline.DevSettings {
			return pipeline.LoadDevSettings(ctx, c.DB, projectID)
		}})}
	return map[string]Method{
		"task.steer": bind(func(ctx context.Context, p struct {
			ID      string `json:"id"`
			Message string `json:"message"`
		}) (any, error) {
			return c.SteerTask(ctx, p.ID, p.Message, c.Human())
		}),
		"task.pause":  bind(func(ctx context.Context, p idParam) (any, error) { return c.PauseTask(ctx, p.ID, c.Human()) }),
		"task.resume": bind(func(ctx context.Context, p idParam) (any, error) { return c.ResumeTask(ctx, p.ID, c.Human()) }),
		"task.assignPreview": bind(func(ctx context.Context, p struct {
			ID       string `json:"id"`
			Assignee string `json:"assignee"`
		}) (any, error) {
			return x.assignPreview(ctx, p.ID, p.Assignee)
		}),
	}, nil, nil
}

// errNoRun refuses a control that needs the task's run.
func errNoRun(t tasks.Task) error {
	return fmt.Errorf("nothing is running on task #%d: start it first", t.Seq)
}

// activeRun is the task's run when it has one that has not ended.
func (c *Core) activeRun(ctx context.Context, t tasks.Task) (workflow.RunInfo, bool) {
	if t.RunID == "" {
		return workflow.RunInfo{}, false
	}
	info, err := c.Engine.Status(ctx, workflow.RunID(t.RunID))
	if err != nil || info.Status.Terminal() {
		return workflow.RunInfo{}, false
	}
	return info, true
}

// executing reports whether a run is being driven now (as opposed to waiting for a person or paused).
func executing(s workflow.RunStatus) bool {
	return s == workflow.StatusRunning || s == workflow.StatusRecovering || s == workflow.StatusPending
}

// personName is the name in a person's identity ("human:kyle" is kyle).
func personName(by string) string { return strings.TrimPrefix(by, tasks.PersonPrefix) }

// publishTask publishes a task's event.
func (c *Core) publishTask(ctx context.Context, typ string, t tasks.Task, agentID string, data map[string]any) {
	b, _ := json.Marshal(data)
	_, _ = c.Bus.Publish(ctx, events.Event{Type: typ, ProjectID: t.ProjectID, TaskID: t.ID, RunID: t.RunID, AgentID: agentID, Data: b})
}

// noteInRoom posts a line of what a person did in the task's channel, as the system, so the history shows it.
func (c *Core) noteInRoom(ctx context.Context, t tasks.Task, body string, payload map[string]any) {
	rm, err := c.Room.RoomByTask(ctx, t.ID)
	if err != nil {
		return
	}
	if _, err := c.Room.Post(ctx, room.PostParams{RoomID: rm.ID, Type: room.TypeSystem, Author: room.System, Body: body, TaskID: t.ID, Payload: payload}); err != nil {
		c.logf("note in the task channel: %v", err)
	}
}

// ---- steer ----

// The ways task.steer delivers a message (SteerResult.Delivered).
const (
	SteerNow  = "now"  // the running turn was interrupted at its current step and goes on, in the same session, with the message
	SteerNext = "next" // no turn was running (the checks were, or the task is paused): the message goes with the task's next turn
)

// SteerResult is what task.steer did.
type SteerResult struct {
	Delivered string `json:"delivered"`
	// MessageID is the message in the task's channel, posted as the person's, so the history shows it.
	MessageID string `json:"messageId"`
}

// SteerTask sends a person's message to the task's running agent, which reads it before its next step (Watchdog.Nudge, the
// team lead's path without escalation): a turn that is running is interrupted and continues in the same session with the
// message; between turns it is queued for the next one. Refused when the task has no run that has not ended. The message is
// scanned for secrets as every room message is (what is found is redacted, in the channel and in what the agent reads), and
// posted in the task's channel as the person's; that post wakes no agent of the room (the responder leaves a steer to this
// path).
func (c *Core) SteerTask(ctx context.Context, id, message, by string) (SteerResult, error) {
	if !isPerson(by) {
		return SteerResult{}, errors.New("only a person steers an agent")
	}
	msg := strings.TrimSpace(message)
	if msg == "" {
		return SteerResult{}, errors.New("write what the agent should know or do differently")
	}
	if len(msg) > room.MaxBody {
		return SteerResult{}, fmt.Errorf("the message is %d bytes; at most %d fit", len(msg), room.MaxBody)
	}
	t, err := c.Tasks.Get(ctx, id)
	if err != nil {
		return SteerResult{}, err
	}
	if _, ok := c.activeRun(ctx, t); !ok {
		return SteerResult{}, errNoRun(t)
	}
	if c.Watch == nil {
		return SteerResult{}, errors.New("steering is not available in this build")
	}
	rm, err := c.Room.RoomByTask(ctx, t.ID)
	if err != nil {
		return SteerResult{}, err
	}
	posted, err := c.Room.Post(ctx, room.PostParams{RoomID: rm.ID, Type: room.TypeChat, Author: room.Human(c.cfg.User), Body: msg, TaskID: t.ID,
		Payload: map[string]any{"via": room.ViaSteer, "runId": t.RunID}})
	if err != nil {
		return SteerResult{}, err
	}
	// what the agent reads is what the channel stored: the body after the room's secret scan
	text := fmt.Sprintf("Message from %s (the person you work for), sent while you work. Read it before your next step:\n%s", personName(by), posted.Message.Body)
	res := SteerResult{Delivered: SteerNext, MessageID: posted.Message.ID}
	if c.Watch.Nudge(ctx, t.ID, text) == watchdog.SteerDelivered {
		res.Delivered = SteerNow
	}
	c.publishTask(ctx, "task.steered", t, "", map[string]any{"taskId": t.ID, "by": by, "delivered": res.Delivered, "messageId": res.MessageID})
	return res, nil
}

// ---- pause and resume ----

// TaskPause says who paused a task's run, and when (Unix ms).
type TaskPause struct {
	By string `json:"by"`
	At int64  `json:"at"`
}

const pausedBy = "by "

// pauseOf reads a person's pause from the run (workflow.PausedPrefix + "by <identity>"); nil when the run is not paused so.
func pauseOf(info workflow.RunInfo) *TaskPause {
	if info.Status != workflow.StatusPaused || !strings.HasPrefix(info.FailedReason, workflow.PausedPrefix+pausedBy) {
		return nil
	}
	return &TaskPause{By: strings.TrimPrefix(info.FailedReason, workflow.PausedPrefix+pausedBy), At: info.Updated}
}

// PauseTask stops the task's run where it is (workflow.Engine.Pause): an agent turn in flight is stopped, the worktree keeps
// what it has, and nothing of the task runs or is scheduled until a person resumes it. A run that waits for a person has
// nothing running to stop, so it is refused, as is a task with no run that has not ended.
func (c *Core) PauseTask(ctx context.Context, id, by string) (TaskDetail, error) {
	if !isPerson(by) {
		return TaskDetail{}, errors.New("only a person pauses a task")
	}
	ctx, unlock := c.lockTaskStart(ctx, id)
	defer unlock()
	t, err := c.Tasks.Get(ctx, id)
	if err != nil {
		return TaskDetail{}, err
	}
	info, ok := c.activeRun(ctx, t)
	switch {
	case !ok:
		return TaskDetail{}, errNoRun(t)
	case info.Status == workflow.StatusPaused:
		return TaskDetail{}, fmt.Errorf("task #%d is already paused", t.Seq)
	case info.Status == workflow.StatusWaitingHuman:
		return TaskDetail{}, fmt.Errorf("task #%d is waiting for you, so nothing of it is running: answer it, or cancel the task", t.Seq)
	}
	parked, err := c.Engine.Pause(ctx, info.ID, pausedBy+by)
	if err != nil {
		return TaskDetail{}, err
	}
	if !parked {
		now, _ := c.Engine.Status(ctx, info.ID)
		return TaskDetail{}, fmt.Errorf("task #%d was no longer running when it was to be paused (its run is %s)", t.Seq, now.Status)
	}
	c.publishTask(ctx, "task.paused", t, "", map[string]any{"taskId": t.ID, "runId": t.RunID, "by": by})
	if t.Assignee != "" && !t.OwnedByPerson() {
		c.publishTask(ctx, "agent.paused", t, t.Assignee, map[string]any{"message": "paused by " + personName(by)})
	}
	c.noteInRoom(ctx, t, fmt.Sprintf("%s paused the task. Nothing of it runs until it is resumed.", personName(by)), map[string]any{"code": "task_paused", "by": by})
	return c.GetTask(ctx, id)
}

// ResumeTask goes on with a task a person paused, from its run's latest checkpoint (workflow.Engine.ResumePaused): the step
// that was in flight runs again, in the same worktree.
func (c *Core) ResumeTask(ctx context.Context, id, by string) (TaskDetail, error) {
	if !isPerson(by) {
		return TaskDetail{}, errors.New("only a person resumes a task")
	}
	if err := c.RequireLicensed(ctx, "start_task"); err != nil {
		return TaskDetail{}, err
	}
	ctx, unlock := c.lockTaskStart(ctx, id)
	defer unlock()
	t, err := c.Tasks.Get(ctx, id)
	if err != nil {
		return TaskDetail{}, err
	}
	info, ok := c.activeRun(ctx, t)
	if !ok || !strings.HasPrefix(info.FailedReason, workflow.PausedPrefix) || info.Status != workflow.StatusPaused {
		return TaskDetail{}, fmt.Errorf("task #%d is not paused", t.Seq)
	}
	resumed, err := c.Engine.ResumePaused(ctx, info.ID)
	if err != nil {
		return TaskDetail{}, err
	}
	if !resumed {
		return TaskDetail{}, fmt.Errorf("task #%d could not be resumed: its run is no longer paused", t.Seq)
	}
	c.publishTask(ctx, "task.unpaused", t, "", map[string]any{"taskId": t.ID, "runId": t.RunID, "by": by})
	if t.Assignee != "" && !t.OwnedByPerson() {
		c.publishTask(ctx, "agent.resumed", t, t.Assignee, map[string]any{"message": "resumed by " + personName(by)})
	}
	c.noteInRoom(ctx, t, fmt.Sprintf("%s resumed the task.", personName(by)), map[string]any{"code": "task_resumed", "by": by})
	return c.GetTask(ctx, id)
}

// ---- what an assignment does ----

// AssignPreview is what assigning a task would do, for the command menu's "What happens" panel. It changes nothing.
type AssignPreview struct {
	TaskID   string `json:"taskId"`
	Assignee string `json:"assignee"`
	// Person is true when the assignee is the person (c.Human()): no agent works on the task, and the fields about the work
	// below say what would happen once it is handed to an agent.
	Person bool `json:"person"`
	// Branch is the branch the task's worktree is (or will be) on.
	Branch string `json:"branch"`
	// HasWorktree is true when the task already has its worktree: the work goes on there.
	HasWorktree bool   `json:"hasWorktree"`
	Template    string `json:"template"`
	SpecGate    bool   `json:"specGate"`
	TestFirst   bool   `json:"testFirst"`
	// Checks are the check kinds that will run on the work, in order (detected build/test/lint/typecheck, the enabled secrets
	// and deps checks, then the template's own: e2e, scans, preview; ci in a pull-request project).
	Checks []string `json:"checks"`
	// Mode is the project's mode (local_merge or pr_flow); Pushes says the branch is pushed to GitHub (pr_flow). Nothing merges
	// before a person approves the change (every template ends at that approval): AsksBeforeMerge is always true. A pull-request
	// project pushes the branch after the independent review without asking: AsksBeforePush is false, except that a push that
	// changes CI definitions waits for a person's approval (pipeline.GateCIPush) whatever this says.
	Mode            string `json:"mode"`
	Pushes          bool   `json:"pushes"`
	AsksBeforePush  bool   `json:"asksBeforePush"`
	AsksBeforeMerge bool   `json:"asksBeforeMerge"`
	// Reviewer is who would review the work (task.assign's rule when no reviewer is named); "" when nobody can.
	Reviewer string `json:"reviewer"`
	// StopsRun is true when the task has a run being driven now: assigning stops its current turn, and the new assignee takes
	// over in the same worktree (a person taking it on ends the run).
	StopsRun bool          `json:"stopsRun"`
	Agent    *PreviewAgent `json:"agent,omitempty"`
	// Queue is the agent's other open tasks (it works on them side by side, each in its own session and worktree).
	Queue []QueuedTask `json:"queue"`
	// Waits says the task, started now, would wait before its agent works, and why; WaitsFor is the task it waits for when
	// that is one (an earlier task changing the same code), absent when it waits for a free agent slot of the project.
	Waits      bool        `json:"waits"`
	WaitReason string      `json:"waitReason,omitempty"`
	WaitsFor   *QueuedTask `json:"waitsFor,omitempty"`
}

// PreviewAgent is the agent an assignment names, as the panel shows it.
type PreviewAgent struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Adapter   string `json:"adapter"`
	Model     string `json:"model"`
	RunMode   string `json:"runMode"`             // where its CLI runs: native or wsl (the project's default applied)
	WSLDistro string `json:"wslDistro,omitempty"` // with runMode wsl
	Status    string `json:"status"`              // the agent's stored status
	Presence  string `json:"presence,omitempty"`  // what it is doing now (Working, Idle, Paused, ...), when known
}

// QueuedTask is a task in an agent's queue.
type QueuedTask struct {
	ID     string       `json:"id"`
	Seq    int          `json:"seq"`
	Title  string       `json:"title"`
	Status tasks.Status `json:"status"`
}

func queued(t tasks.Task) QueuedTask {
	return QueuedTask{ID: t.ID, Seq: t.Seq, Title: t.Title, Status: t.Status}
}

// openStatuses are a task's statuses while it is not finished and past the backlog.
var openStatuses = []tasks.Status{tasks.Ready, tasks.Claimed, tasks.InProgress, tasks.Blocked, tasks.WaitingHuman, tasks.Review, tasks.Verification}

func (x *controlExt) assignPreview(ctx context.Context, id, assignee string) (AssignPreview, error) {
	c := x.c
	t, err := c.Tasks.Get(ctx, id)
	if err != nil {
		return AssignPreview{}, err
	}
	p, err := c.GetProject(ctx, t.ProjectID)
	if err != nil {
		return AssignPreview{}, err
	}
	out := AssignPreview{TaskID: t.ID, Assignee: assignee, Person: assignee == c.Human(), Branch: t.Branch, HasWorktree: t.Worktree != "",
		Mode: p.Mode, Pushes: p.Mode == "pr_flow", AsksBeforeMerge: true, Queue: []QueuedTask{}, Checks: []string{}}
	if out.Branch == "" {
		out.Branch = git.BranchName(t.ID, t.Title)
	}
	tplName := t.Template
	if tplName == "" {
		tplName = pipeline.WorkflowIUV
	}
	tpl, ok := pipeline.TemplateByName(tplName)
	if !ok {
		tpl, _ = pipeline.TemplateByName(pipeline.WorkflowIUV)
	}
	out.Template, out.SpecGate, out.TestFirst = tpl.Name, tpl.SpecGate, tpl.TestFirst
	out.Checks = c.previewChecks(ctx, t, p, tpl)
	if info, ok := c.activeRun(ctx, t); ok && executing(info.Status) {
		out.StopsRun = true
	}
	if out.Person {
		return out, nil
	}
	if assignee == "" {
		return out, errors.New("name the agent (or yourself) the task would go to")
	}
	a, err := c.agent(ctx, assignee)
	if err != nil || a.ProjectID != t.ProjectID || a.Archived {
		return out, fmt.Errorf("agent %s is not on this project's team", assignee)
	}
	mode, distro := c.effectiveRunMode(ctx, a)
	pa := &PreviewAgent{ID: a.ID, Name: a.Name, Role: a.Role, Adapter: a.Adapter, Model: a.Model, RunMode: mode, WSLDistro: distro, Status: a.Status}
	if ps, ok := c.Presence.Get(a.ID); ok {
		pa.Presence = string(ps.Presence)
	}
	out.Agent = pa
	out.Reviewer, _ = c.reviewerFor(ctx, t, a.ID, "")
	open, err := c.Tasks.List(ctx, tasks.Filter{ProjectID: t.ProjectID, Assignee: a.ID, Statuses: openStatuses})
	if err != nil {
		return out, err
	}
	for i := len(open) - 1; i >= 0; i-- { // oldest first: the order the agent took them on
		if open[i].ID != t.ID {
			out.Queue = append(out.Queue, queued(open[i]))
		}
	}
	w, err := x.sched.WouldWait(ctx, t)
	if err != nil {
		return out, err
	}
	if w != nil {
		out.Waits, out.WaitReason = true, w.Reason
		if w.Behind != nil {
			q := queued(*w.Behind)
			out.WaitsFor = &q
		}
	}
	return out, nil
}

// previewChecks lists the check kinds the task's work will be checked with, in the order they run.
func (c *Core) previewChecks(ctx context.Context, t tasks.Task, p Project, tpl pipeline.Template) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	dir := p.RepoPath
	if t.Worktree != "" {
		if st, err := os.Stat(t.Worktree); err == nil && st.IsDir() {
			dir = t.Worktree
		}
	}
	detect := c.Deps.Checks
	if detect == nil {
		detect = checks.Detect
	}
	if dir != "" {
		for _, s := range detect(dir) {
			add(s.Name)
		}
	}
	set := checks.LoadSettings(ctx, c.DB, t.ProjectID)
	if set.Secrets {
		add("secrets")
	}
	if set.Deps {
		add("deps")
	}
	if set.Scan {
		add(scan.CheckName)
	}
	dev := pipeline.LoadDevSettings(ctx, c.DB, t.ProjectID)
	switch tpl.Workflow {
	case pipeline.WorkflowSaaS:
		if dev.E2E.Enabled {
			add("e2e")
		}
		for _, s := range pipeline.DefaultScanNames {
			add(s)
		}
		if dev.Preview.Enabled {
			add("preview")
		}
	case pipeline.WorkflowSecurity:
		for _, s := range pipeline.DefaultScanNames {
			add(s)
		}
	}
	if p.Mode == "pr_flow" {
		add("ci")
	}
	return out
}

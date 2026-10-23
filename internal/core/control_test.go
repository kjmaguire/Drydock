package core_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The controls a person uses on tasks and agents (ext_control.go, HandTask, lead.split), through the methods the UI calls.
// Every agent is the scripted stand-in (fakeagent: a labelled test double, a real process speaking Claude's dialect).

type crew struct {
	a        *app
	prompts  string
	p        core.Project
	worker   core.Agent
	reviewer core.Agent
}

// newCrew opens the app with one script for every agent, a gocalc project, a worker "Builder" (model sonnet) and a reviewer.
func newCrew(t *testing.T, sc fakeagent.Script) *crew {
	t.Helper()
	a, pl := openDev(t, sc)
	c := &crew{a: a, prompts: pl}
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &c.p)
	a.call("agent.create", core.AgentSpec{ProjectID: c.p.ID, Name: "Builder", Adapter: "claude", Role: "backend", Model: "sonnet"}, &c.worker)
	a.call("agent.create", core.AgentSpec{ProjectID: c.p.ID, Name: "Reviewer", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &c.reviewer)
	return c
}

func (c *crew) task(title string) tasks.Task {
	c.a.t.Helper()
	var tk tasks.Task
	c.a.call("task.create", tasks.CreateReq{ProjectID: c.p.ID, Title: title, Description: "Subtract returns 0; make it return a - b.", Acceptance: []string{"Given 5 and 3 When Subtract is called Then 2"}}, &tk)
	return tk
}

func (c *crew) assignAndStart(tk tasks.Task) string {
	c.a.t.Helper()
	c.a.call("task.assign", map[string]string{"id": tk.ID, "assignee": c.worker.ID, "reviewer": c.reviewer.ID}, nil)
	var runID string
	c.a.call("task.start", map[string]string{"id": tk.ID}, &runID)
	return runID
}

func (c *crew) detail(id string) core.TaskDetail {
	var d core.TaskDetail
	c.a.call("task.get", map[string]string{"id": id}, &d)
	return d
}

// liveAgents is how many agent processes of the task are running.
func (c *crew) liveAgents(taskID string) int {
	var n int
	_ = c.a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE task_id=? AND ended_at=0`, taskID).Scan(&n)
	return n
}

// turnsStarted is how many agent turns of the task have begun (agent.waiting {state: working}: the turn's controller is set).
func (c *crew) turnsStarted(taskID string) int {
	evs, _ := c.a.c.Bus.Replay(context.Background(), 0, events.Filter{TaskID: taskID, Types: []string{"agent.waiting"}}, 1000)
	n := 0
	for _, e := range evs {
		if strings.Contains(string(e.Data), `"working"`) {
			n++
		}
	}
	return n
}

func (c *crew) eventCount(taskID, typ string) int {
	evs, _ := c.a.c.Bus.Replay(context.Background(), 0, events.Filter{TaskID: taskID, Types: []string{typ}}, 1000)
	return len(evs)
}

func (c *crew) awaitApproval(taskID string) core.TaskDetail {
	c.a.t.Helper()
	var d core.TaskDetail
	c.a.waitFor("the approval", func() bool { d = c.detail(taskID); return len(d.Pending) == 1 })
	if d.Pending[0].NodeID != "approve" {
		c.a.t.Fatalf("the run should reach the approval, pending %+v", d.Pending[0])
	}
	return d
}

func mustErr(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("want an error with %q, got %v", want, err)
	}
}

var reviewerShips = fakeagent.Rule{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}

// Item 1: a person's steer reaches the running worker before its next step: the blocked turn is interrupted and the same
// session goes on with the message (the worker then does what it says). The message is in the task's channel as the person's,
// a secret in it is redacted there and in what the agent reads, and it wakes no room conversation.
func TestSteerReachesTheRunningAgentBeforeItsNextStep(t *testing.T) {
	c := newCrew(t, fakeagent.Script{
		Rules: []fakeagent.Rule{reviewerShips, {Match: "STEERMARK", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "did as asked"}}}},
		Turns: [][]fakeagent.Step{{{Block: true}}},
	})
	a := c.a
	tk := c.task("Implement Subtract")
	mustErr(t, a.callErr("task.steer", map[string]string{"id": tk.ID, "message": "hello"}), "nothing is running on task #1")
	c.assignAndStart(tk)
	a.waitFor("the worker's turn", func() bool { return c.turnsStarted(tk.ID) >= 1 })
	mustErr(t, a.callErr("task.steer", map[string]string{"id": tk.ID, "message": "   "}), "write what the agent should know")

	secret := "ghp_" + strings.Repeat("aB3dE5fG7h", 3) + "aB3dE5"
	var res core.SteerResult
	a.call("task.steer", map[string]string{"id": tk.ID, "message": "STEERMARK: make Subtract return a - b. The token is " + secret}, &res)
	if res.Delivered != core.SteerNow || res.MessageID == "" {
		t.Fatalf("steer %+v", res)
	}
	c.awaitApproval(tk.ID)

	m, err := a.c.Room.GetMessage(context.Background(), res.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsSteer() || m.Author.Key() != "human:kyle" || m.TaskID != tk.ID || !strings.Contains(m.Body, "STEERMARK") || strings.Contains(m.Body, secret) || !strings.Contains(m.Body, "[REDACTED:") {
		t.Fatalf("the channel's message %+v", m)
	}
	b, _ := os.ReadFile(c.prompts)
	if !strings.Contains(string(b), "Message from kyle (the person you work for)") || !strings.Contains(string(b), "STEERMARK") || strings.Contains(string(b), secret) {
		t.Fatalf("what the agents read:\n%s", b)
	}
	var turns int
	a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM room_turns WHERE trigger_id=?`, res.MessageID).Scan(&turns)
	if turns != 0 {
		t.Fatalf("the steer started %d room turn(s)", turns)
	}
	if n := c.eventCount(tk.ID, "task.steered"); n != 1 {
		t.Fatalf("task.steered events %d", n)
	}
}

// Item 2: pause stops the worker at once (its process ends, the worktree stays) and parks the run; the task shows as paused
// (TaskDetail.Paused, presence Paused) with its status unchanged; resume goes on from the checkpoint with the step that was in
// flight. Paused twice on the same turn, the third run of it is not held for the interrupted-turn approval.
func TestPauseStopsTheWorkerAndResumeGoesOnWhereItWas(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "go-on")
	c := newCrew(t, fakeagent.Script{Rules: []fakeagent.Rule{reviewerShips},
		Default: []fakeagent.Step{{WaitFile: gate}, {Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "implemented Subtract"}}})
	a := c.a
	tk := c.task("Implement Subtract")
	mustErr(t, a.callErr("task.pause", map[string]string{"id": tk.ID}), "nothing is running")
	c.assignAndStart(tk)
	var worktree string
	for round := 1; round <= 2; round++ {
		a.waitFor("the worker's turn", func() bool { return c.turnsStarted(tk.ID) >= round && c.liveAgents(tk.ID) > 0 })
		var d core.TaskDetail
		a.call("task.pause", map[string]string{"id": tk.ID}, &d)
		if d.Paused == nil || d.Paused.By != "human:kyle" || d.Paused.At == 0 || d.Run == nil || d.Run.Status != workflow.StatusPaused || d.Task.Status != tasks.InProgress {
			t.Fatalf("paused detail %+v run %+v", d.Paused, d.Run)
		}
		if worktree == "" {
			worktree = d.Task.Worktree
		}
		a.waitFor("the worker's process to end", func() bool { return c.liveAgents(tk.ID) == 0 })
		a.waitFor("presence Paused", func() bool { ps, ok := a.c.Presence.Get(c.worker.ID); return ok && ps.Presence == room.PresencePaused })
		mustErr(t, a.callErr("task.pause", map[string]string{"id": tk.ID}), "already paused")
		if st, err := os.Stat(worktree); err != nil || !st.IsDir() {
			t.Fatalf("the worktree must stay: %v", err)
		}
		if round == 2 {
			os.WriteFile(gate, []byte("go"), 0o644)
		}
		var r core.TaskDetail
		a.call("task.resume", map[string]string{"id": tk.ID}, &r)
		if r.Paused != nil || r.Run == nil || r.Run.Status == workflow.StatusPaused {
			t.Fatalf("resumed detail %+v run %+v", r.Paused, r.Run)
		}
	}
	d := c.awaitApproval(tk.ID)
	if d.Task.Worktree != worktree {
		t.Fatalf("worktree %s, was %s", d.Task.Worktree, worktree)
	}
	mustErr(t, a.callErr("task.resume", map[string]string{"id": tk.ID}), "is not paused")
	mustErr(t, a.callErr("task.pause", map[string]string{"id": tk.ID}), "waiting for you")
	if p, u := c.eventCount(tk.ID, "task.paused"), c.eventCount(tk.ID, "task.unpaused"); p != 2 || u != 2 {
		t.Fatalf("task.paused %d task.unpaused %d", p, u)
	}
}

// Item 3: a task is handed to another agent while its worker runs: the worker's turn is stopped and the new agent takes the
// same step over in the same worktree; the reviewer is kept when none is named. A refused hand-over leaves the run going.
func TestHandingARunningTaskToAnotherAgentGoesOnInTheSameWorktree(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "go-on")
	c := newCrew(t, fakeagent.Script{Rules: []fakeagent.Rule{reviewerShips},
		Default: []fakeagent.Step{{WaitFile: gate}, {Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "implemented Subtract"}}})
	a := c.a
	var second core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: c.p.ID, Name: "Second", Adapter: "claude", Role: "backend", Model: "fake-1"}, &second)
	tk := c.task("Implement Subtract")
	runID := c.assignAndStart(tk)
	a.waitFor("the worker's turn", func() bool { return c.turnsStarted(tk.ID) >= 1 && c.liveAgents(tk.ID) > 0 })
	before := c.detail(tk.ID).Task.Worktree
	mustErr(t, a.callErr("task.assign", map[string]string{"id": tk.ID, "assignee": "agt_nobody"}), "not on this project's team")
	if d := c.detail(tk.ID); d.Run == nil || d.Run.Status == workflow.StatusPaused || d.Task.Assignee != c.worker.ID {
		t.Fatalf("a refused hand-over must leave the run going: %+v %s", d.Run, d.Task.Assignee)
	}
	var nt tasks.Task
	a.call("task.assign", map[string]string{"id": tk.ID, "assignee": second.ID}, &nt)
	if nt.Assignee != second.ID || nt.Reviewer != c.reviewer.ID {
		t.Fatalf("assignee %s reviewer %s", nt.Assignee, nt.Reviewer)
	}
	a.waitFor("the new worker's turn", func() bool {
		var n int
		a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE task_id=? AND agent_id=? AND ended_at=0`, tk.ID, second.ID).Scan(&n)
		return n == 1
	})
	os.WriteFile(gate, []byte("go"), 0o644)
	d := c.awaitApproval(tk.ID)
	if d.Run.ID != workflow.RunID(runID) || d.Task.Worktree != before {
		t.Fatalf("the same run and worktree: run %s (was %s), worktree %s (was %s)", d.Run.ID, runID, d.Task.Worktree, before)
	}
	evs, _ := a.c.Bus.Replay(context.Background(), 0, events.Filter{TaskID: tk.ID, Types: []string{"task.handed"}}, 10)
	// the first is the assignment before the start
	if len(evs) != 2 || !strings.Contains(string(evs[1].Data), `"restarted":true`) || !strings.Contains(string(evs[1].Data), `"to":"`+second.ID) {
		t.Fatalf("task.handed %+v", evs)
	}
}

// Item 3: the reviewer is optional when a task is assigned: the task keeps its reviewer unless that is the new worker; then the
// project's reviewer-role agent reviews, else the first other agent (never a lead); with nobody else it is refused.
func TestAssignWithoutAReviewerKeepsOrPicksOne(t *testing.T) {
	a, _ := openDev(t, fakeagent.Script{})
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	var builder, rev, other, lead core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Builder", Adapter: "claude", Role: "backend"}, &builder)
	var tk tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "x"}, &tk)
	mustErr(t, a.callErr("task.assign", map[string]string{"id": tk.ID, "assignee": builder.ID}), "no other agent on the team can review")
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Lead", Adapter: "claude", Role: "lead"}, &lead)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Other", Adapter: "claude", Role: "backend"}, &other)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Rev", Adapter: "claude", Role: "reviewer"}, &rev)
	a.call("task.assign", map[string]string{"id": tk.ID, "assignee": builder.ID}, &tk)
	if tk.Assignee != builder.ID || tk.Reviewer != rev.ID {
		t.Fatalf("the reviewer-role agent reviews: %s / %s", tk.Assignee, tk.Reviewer)
	}
	a.call("task.assign", map[string]string{"id": tk.ID, "assignee": other.ID}, &tk)
	if tk.Assignee != other.ID || tk.Reviewer != rev.ID {
		t.Fatalf("the reviewer is kept: %s / %s", tk.Assignee, tk.Reviewer)
	}
	a.call("task.assign", map[string]string{"id": tk.ID, "assignee": rev.ID}, &tk)
	if tk.Assignee != rev.ID || tk.Reviewer != builder.ID {
		t.Fatalf("the worker cannot review itself; the first other agent (not the lead) reviews: %s / %s", tk.Assignee, tk.Reviewer)
	}
	mustErr(t, a.callErr("task.assign", map[string]string{"id": tk.ID, "assignee": rev.ID, "reviewer": rev.ID}), "different agent as reviewer")
}

// Item 4: a person takes a task on: it is listed as theirs, nothing starts agent work on it (start, templates, the scheduler),
// the views read it without error, a run that was going is ended (the worktree stays), and handed back to an agent it starts
// again in the same worktree.
func TestATaskThePersonTakesOnIsTheirsAndNoAgentWorksOnIt(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "go-on")
	c := newCrew(t, fakeagent.Script{Rules: []fakeagent.Rule{reviewerShips},
		Default: []fakeagent.Step{{WaitFile: gate}, {Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "implemented Subtract"}}})
	a := c.a
	var info struct {
		Human string `json:"human"`
	}
	a.call("app.info", nil, &info)
	me := info.Human
	notes := c.task("Write the release notes")
	var nt tasks.Task
	a.call("task.assign", map[string]string{"id": notes.ID, "assignee": me}, &nt)
	if nt.Assignee != "human:kyle" || !nt.OwnedByPerson() {
		t.Fatalf("assignee %q", nt.Assignee)
	}
	mustErr(t, a.callErr("task.assign", map[string]string{"id": notes.ID, "assignee": "human:bob"}), "is not you")
	var mine []tasks.Task
	a.call("task.list", map[string]any{"projectId": "", "assignee": me}, &mine)
	var count map[string]int
	a.call("task.count", map[string]any{"assignee": me}, &count)
	if len(mine) != 1 || mine[0].ID != notes.ID || count["total"] != 1 {
		t.Fatalf("my tasks %+v count %v", mine, count)
	}
	mustErr(t, a.callErr("task.start", map[string]string{"id": notes.ID}), "a person owns this task")
	mustErr(t, a.callErr("task.startTemplate", map[string]string{"taskId": notes.ID, "template": "bug"}), "a person owns this task")
	if _, err := a.c.Tasks.Transition(context.Background(), notes.ID, tasks.Ready, tasks.TransitionOpts{By: me}); err != nil {
		t.Fatal(err)
	}
	var tick pipeline.TickResult
	a.call("scheduler.tick", map[string]string{"projectId": c.p.ID}, &tick)
	if len(tick.Started) != 0 || len(tick.Skipped) != 1 || !strings.Contains(tick.Skipped[0].Reason, "a person owns this task") {
		t.Fatalf("tick %+v", tick)
	}
	// the views read a person's task as any other
	c.detail(notes.ID)
	a.call("task.activity", map[string]string{"taskId": notes.ID}, nil)
	a.call("needsyou.list", map[string]string{"projectId": c.p.ID}, nil)
	var pv core.AssignPreview
	a.call("task.assignPreview", map[string]string{"id": notes.ID, "assignee": me}, &pv)
	if !pv.Person || pv.Agent != nil {
		t.Fatalf("preview for me %+v", pv)
	}

	// taking on a task whose worker runs ends the run; handed back, it starts again in the same worktree
	tk := c.task("Implement Subtract")
	runID := c.assignAndStart(tk)
	a.waitFor("the worker's turn", func() bool { return c.turnsStarted(tk.ID) >= 1 && c.liveAgents(tk.ID) > 0 })
	a.call("task.assign", map[string]string{"id": tk.ID, "assignee": me}, &nt)
	d := c.detail(tk.ID)
	if d.Run == nil || d.Run.Status != workflow.StatusCancelled || d.Task.Status.Terminal() || d.Task.Assignee != me {
		t.Fatalf("after taking it on: run %+v task %s %s", d.Run, d.Task.Status, d.Task.Assignee)
	}
	worktree := d.Task.Worktree
	a.waitFor("the worker's process to end", func() bool { return c.liveAgents(tk.ID) == 0 })
	a.call("task.assign", map[string]string{"id": tk.ID, "assignee": c.worker.ID}, &nt)
	if nt.Reviewer != c.reviewer.ID {
		t.Fatalf("reviewer %s", nt.Reviewer)
	}
	os.WriteFile(gate, []byte("go"), 0o644)
	var again string
	a.call("task.start", map[string]string{"id": tk.ID}, &again)
	d = c.awaitApproval(tk.ID)
	if again == runID || d.Task.Worktree != worktree {
		t.Fatalf("a new run (%s, was %s) in the same worktree (%s, was %s)", again, runID, d.Task.Worktree, worktree)
	}
}

// Item 5: lead.split runs the team lead's split on a person's request (trigger requested); without a lead it is refused.
func TestLeadSplitOnAPersonsRequest(t *testing.T) {
	c := newCrew(t, fakeagent.Script{Rules: []fakeagent.Rule{{Match: "Drydock phase: supervise", Steps: []fakeagent.Step{{Say: `{"action":"split","message":"Only Subtract here.","tasks":[{"title":"Document Subtract","description":"Add a doc comment.","acceptanceCriteria":["Given calc.go, when a reader opens it, then Subtract has a doc comment"]},{"title":"Add Multiply","description":"Add it.","acceptanceCriteria":["Given 2 and 3, when Multiply is called, then it returns 6"]}],"reason":"the extra work is separate"}`}}}}})
	a := c.a
	tk := c.task("Implement Subtract and document it")
	mustErr(t, a.callErr("lead.split", map[string]string{"taskId": tk.ID}), "no team lead")
	var lead core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: c.p.ID, Name: "Lead", Adapter: "claude", Role: "lead", Model: "fake-1"}, &lead)
	var res core.SplitResult
	a.call("lead.split", map[string]string{"taskId": tk.ID}, &res)
	if res.Action != pipeline.LeadSplit || res.LeadID != lead.ID || len(res.CreatedTasks) != 2 || res.InterventionID == "" {
		t.Fatalf("split %+v", res)
	}
	for _, nt := range res.CreatedTasks {
		if nt.Status != tasks.Backlog || nt.Origin != tasks.LeadOrigin(1) {
			t.Fatalf("follow-up %+v", nt)
		}
	}
	var ivs []pipeline.Intervention
	a.call("lead.interventions", map[string]string{"projectId": c.p.ID, "taskId": tk.ID}, &ivs)
	if len(ivs) != 1 || ivs[0].Trigger != pipeline.TriggerRequested || ivs[0].Action != pipeline.LeadSplit {
		t.Fatalf("interventions %+v", ivs)
	}
}

// Item 6: each thread of a task's activity says what its agent ran as (from agent.started), and from the agent's settings
// when that event was not read.
func TestActivityThreadsSayTheAdapterAndModel(t *testing.T) {
	c := newCrew(t, fakeagent.Script{Rules: []fakeagent.Rule{reviewerShips},
		Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "implemented Subtract"}}})
	a := c.a
	tk := c.task("Implement Subtract")
	c.assignAndStart(tk)
	c.awaitApproval(tk.ID)
	var act struct {
		Threads []struct {
			AgentID string `json:"agentId"`
			Adapter string `json:"adapter"`
			Model   string `json:"model"`
		} `json:"threads"`
	}
	a.call("task.activity", map[string]string{"taskId": tk.ID}, &act)
	byAgent := map[string]string{}
	for _, th := range act.Threads {
		byAgent[th.AgentID] = th.Adapter + "/" + th.Model
	}
	if byAgent[c.worker.ID] != "claude/sonnet" || byAgent[c.reviewer.ID] != "claude/fake-1" {
		t.Fatalf("threads %+v", act.Threads)
	}
	old := core.ActivityEventCap
	core.ActivityEventCap = 3 // the newest few: the agent.started events are cut off
	defer func() { core.ActivityEventCap = old }()
	a.call("task.activity", map[string]string{"taskId": tk.ID}, &act)
	if len(act.Threads) == 0 {
		t.Fatal("no thread")
	}
	for _, th := range act.Threads {
		if th.Adapter != "claude" || th.Model == "" {
			t.Fatalf("a thread without its agent.started: %+v", th)
		}
	}
}

// Item 7: task.assignPreview says what assigning a task would do, and changes nothing.
func TestAssignPreviewSaysWhatAnAssignmentDoes(t *testing.T) {
	c := newCrew(t, fakeagent.Script{})
	a := c.a
	var tk, other, feature tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: c.p.ID, Title: "Add Subtract!", Description: "x", Files: []string{"calc.go"}}, &tk)
	a.call("task.create", tasks.CreateReq{ProjectID: c.p.ID, Title: "Tidy calc.go", Description: "x", Files: []string{"calc.go"}}, &other)
	a.call("task.create", tasks.CreateReq{ProjectID: c.p.ID, Title: "A feature", Description: "x", Template: "feature"}, &feature)
	a.call("task.assign", map[string]string{"id": other.ID, "assignee": c.worker.ID}, nil)
	// the other task is under way on the same file
	if _, err := a.c.DB.Writer().Exec(`UPDATE tasks SET status='IN_PROGRESS', started_at=1, version=version+1 WHERE id=?`, other.ID); err != nil {
		t.Fatal(err)
	}
	var pv core.AssignPreview
	a.call("task.assignPreview", map[string]string{"id": tk.ID, "assignee": c.worker.ID}, &pv)
	if pv.Branch != "drydock/"+tk.ID+"-add-subtract" || pv.HasWorktree || pv.Template != "iterate-until-verified" || pv.SpecGate || pv.TestFirst {
		t.Fatalf("preview %+v", pv)
	}
	if strings.Join(pv.Checks, ",") != "build,test,lint,secrets" || pv.Mode != "local_merge" || pv.Pushes || pv.AsksBeforePush || !pv.AsksBeforeMerge || pv.StopsRun {
		t.Fatalf("checks and gates %+v", pv)
	}
	ag := pv.Agent
	if ag == nil || ag.ID != c.worker.ID || ag.Name != "Builder" || ag.Adapter != "claude" || ag.Model != "sonnet" || ag.RunMode != "native" || ag.Status == "" {
		t.Fatalf("agent %+v", ag)
	}
	if pv.Reviewer != c.reviewer.ID {
		t.Fatalf("reviewer %s", pv.Reviewer)
	}
	if len(pv.Queue) != 1 || pv.Queue[0].ID != other.ID || pv.Queue[0].Status != tasks.InProgress {
		t.Fatalf("queue %+v", pv.Queue)
	}
	if !pv.Waits || pv.WaitsFor == nil || pv.WaitsFor.ID != other.ID || !strings.Contains(pv.WaitReason, "changes the same code") {
		t.Fatalf("wait %+v %+v", pv.WaitReason, pv.WaitsFor)
	}
	var fp core.AssignPreview
	a.call("task.assignPreview", map[string]string{"id": feature.ID, "assignee": c.reviewer.ID}, &fp)
	if fp.Template != "feature" || !fp.SpecGate || !fp.TestFirst || fp.Waits || len(fp.Queue) != 0 || fp.Reviewer != c.worker.ID {
		t.Fatalf("feature preview %+v", fp)
	}
	mustErr(t, a.callErr("task.assignPreview", map[string]string{"id": tk.ID, "assignee": "agt_nobody"}), "not on this project's team")
	if got, _ := a.c.Tasks.Get(context.Background(), tk.ID); got.Assignee != "" {
		t.Fatalf("a preview assigned the task: %q", got.Assignee)
	}
}

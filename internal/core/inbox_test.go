package core_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// needsItem returns the inbox item with the key, listed for projectID ("" for the whole workspace).
func (a *app) needsItem(projectID, key string) (room.NeedsYouItem, bool) {
	a.t.Helper()
	var needs []room.NeedsYouItem
	a.call("needsyou.list", map[string]string{"projectId": projectID}, &needs)
	for _, n := range needs {
		if n.Key == key {
			return n, true
		}
	}
	return room.NeedsYouItem{}, false
}

// The inbox tells a spec gate from a review and merge by the phase of the request that made it, and carries the task's number
// and the agent's name; the workspace's inbox (projectId "") lists every project's items. approval.get gives the request's
// detail for the right pane.
func TestTheInboxCarriesThePhaseTheTaskNumberAndTheAgent(t *testing.T) {
	a, _ := openDev(t, devScript())
	repo := newGreenRepo(t)
	var p, other core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	a.call("project.create", map[string]any{"name": "shop"}, &other)
	a.agent(p.ID, "Priya", "pm")
	a.agent(p.ID, "Quinn", "qa")
	dev := a.agent(p.ID, "Dana", "backend")
	rev := a.agent(p.ID, "Rex", "reviewer")
	var first, task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Write the README", Description: "words"}, &first)
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Add Multiply", Description: "Add a Multiply function to the gocalc package.", Template: "feature"}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": dev.ID, "reviewer": rev.ID}, &task)
	var started core.StartTemplateResult
	a.call("task.startTemplate", map[string]any{"taskId": task.ID, "template": "feature"}, &started)

	// an approval of the other project, to see the workspace's inbox list both
	ap := security.NewApprovals(a.c.DB, a.c.Bus, nil)
	otherID, err := ap.Request(context.Background(), security.ActionDescriptor{Kind: security.KindShellCommand, Project: other.ID, Tool: "shell", Command: "make"}, "test")
	if err != nil {
		t.Fatal(err)
	}

	d := a.waitPending(task.ID, "specreview")
	key := "approval:" + string(d.Pending[0].ID)
	spec, ok := a.needsItem(p.ID, key)
	if !ok || spec.Kind != room.NeedsReview || spec.Phase != "spec" || spec.TaskSeq != 2 || spec.AgentID != dev.ID || spec.AgentName != "Dana" || spec.ExpiresAt != nil {
		t.Fatalf("spec gate item %+v", spec)
	}
	if all, ok := a.needsItem("", key); !ok || all.Phase != "spec" || all.TaskSeq != 2 {
		t.Fatalf("the workspace's inbox must list the project's item: %+v", all)
	}
	if o, ok := a.needsItem("", "approval:"+otherID); !ok || o.ProjectID != other.ID || o.ExpiresAt == nil || o.Kind != room.NeedsApproval {
		t.Fatalf("the workspace's inbox must list the other project's item: %+v", o)
	}
	if _, ok := a.needsItem(p.ID, "approval:"+otherID); ok {
		t.Fatal("a project's inbox listed another project's item")
	}
	var det core.ApprovalDetail
	a.call("approval.get", map[string]string{"id": string(d.Pending[0].ID)}, &det)
	if det.Kind != "review" || det.Phase != "spec" || det.Status != "REQUESTED" || det.RunID != string(d.Run.ID) || det.Task == nil || det.Task.Seq != 2 ||
		det.Task.Title != "Add Multiply" || det.Agent == nil || det.Agent.Name != "Dana" || det.Agent.Adapter != "claude" || det.Agent.Model != "fake-1" ||
		det.ProjectName != "gocalc" || !strings.Contains(det.Description, "spec") || len(det.Payload) == 0 || det.ApproveForTask || det.Worktree == "" {
		t.Fatalf("spec gate detail %+v", det)
	}
	// a workflow request takes no scope
	if err := a.callErr("run.respond", map[string]any{"runId": d.Run.ID, "requestId": d.Pending[0].ID, "decision": "approved", "scope": "task"}); err == nil {
		t.Fatal("a spec gate was approved for the rest of the task")
	}

	a.call("run.respond", map[string]any{"runId": d.Run.ID, "requestId": d.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	d = a.waitPending(task.ID, "approve")
	merge, ok := a.needsItem("", "approval:"+string(d.Pending[0].ID))
	if !ok || merge.Phase != "merge" || merge.TaskSeq != 2 || merge.AgentName != "Dana" {
		t.Fatalf("review and merge item %+v", merge)
	}
	a.call("approval.get", map[string]string{"id": string(d.Pending[0].ID)}, &det)
	if det.Phase != "merge" || !strings.Contains(det.Description, "merge") {
		t.Fatalf("merge detail %+v", det)
	}
	if err := a.callErr("approval.get", map[string]string{"id": "apr_nonesuch"}); err == nil {
		t.Fatal("an unknown request was described")
	}
}

// Approve for this task (P97): a worker's first "curl https://example.com" is approved for the rest of the task, and recorded
// as approved for the task with its rule and its approval; its second is approved without a request and recorded as approved
// for the task; "curl https://example.org" still asks. A rule can be removed, and the one left dies with the task.
func TestApprovingForTheTaskApprovesTheSameCallUntilTheTaskEnds(t *testing.T) {
	ask := func(cmd, why string) fakeagent.Step {
		in, _ := json.Marshal(map[string]string{"command": cmd, "description": why})
		return fakeagent.Step{Tool: "Bash", Input: in, Result: "ran: " + cmd, Ask: true}
	}
	permLog := filepath.Join(t.TempDir(), "answers.jsonl")
	t.Setenv("DRYDOCK_FAKE_PERMISSION_LOG", permLog)
	a := openScripted(t, nil, fakeagent.Script{
		Rules: []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
		Default: []fakeagent.Step{ask("curl https://example.com", "fetch"), ask("curl https://example.com", "fetch again"), ask("curl https://example.org", "another"),
			{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "implemented Subtract"}},
	})
	p, _ := a.setup("gocalc")
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	permission := func(input string) string {
		var id string
		a.waitFor("the permission request for "+input, func() bool {
			for _, p := range a.detail(task.ID).Pending {
				var pl map[string]any
				_ = json.Unmarshal(p.Spec.Payload, &pl)
				if pl["gate"] == core.PermissionGate && pl["input"] == input {
					id = string(p.ID)
					return true
				}
			}
			return false
		})
		return id
	}

	id := permission("curl https://example.com")
	item, ok := a.needsItem("", "approval:"+id)
	if !ok || item.AgentName != "Backend #1" || item.TaskSeq != 1 || item.ExpiresAt == nil || item.Phase != "" {
		t.Fatalf("permission item %+v", item)
	}
	var det core.ApprovalDetail
	a.call("approval.get", map[string]string{"id": id}, &det)
	if det.Gate != core.PermissionGate || det.Tool != "Bash" || det.Input != "curl https://example.com" || !det.Shell || det.ActionKind != security.KindShellCommand ||
		det.Agent == nil || det.Agent.ID != worker.ID || det.Agent.Name != "Backend #1" || det.Agent.Adapter != "claude" || det.Agent.Model != "fake-1" ||
		det.Task == nil || det.Task.ID != task.ID || det.Task.Seq != 1 || det.RunID != runID || det.Worktree == "" || det.Cwd == "" || det.ExpiresAt == 0 ||
		det.ActionHash == "" || det.Status != "REQUESTED" || !det.ApproveForTask || det.AgentRunID == "" || len(det.Rules) != 0 ||
		!strings.Contains(det.Description, "curl https://example.com") || det.Reason == "" || det.CreatedAt == 0 {
		t.Fatalf("permission detail %+v", det)
	}
	if err := a.callErr("run.respond", map[string]any{"runId": runID, "requestId": id, "decision": "rejected", "scope": "task"}); err == nil {
		t.Fatal("a refusal was given for the rest of the task")
	}
	if err := a.callErr("run.respond", map[string]any{"runId": runID, "requestId": id, "decision": "approved", "scope": "forever"}); err == nil {
		t.Fatal("an unknown scope was taken")
	}
	a.call("run.respond", map[string]any{"runId": runID, "requestId": id, "decision": "approved", "scope": "task", "data": map[string]string{}}, nil)

	// the second, identical call is not asked; the third, another command, is
	id3 := permission("curl https://example.org")
	d := a.detail(task.ID)
	if len(d.AllowRules) != 1 {
		t.Fatalf("task.get allow rules %+v", d.AllowRules)
	}
	rule := d.AllowRules[0]
	if rule.TaskID != task.ID || rule.AgentID != worker.ID || rule.Program != "curl" || rule.Summary != "curl https://example.com" || rule.ApprovalID != id || rule.CreatedBy != "human:kyle" {
		t.Fatalf("rule %+v", rule)
	}
	var outcomes []string
	for _, e := range a.eventsOf("permission.decided") {
		outcomes = append(outcomes, e["outcome"].(string))
		if e["outcome"] == security.PermissionApprovedForTask && (e["rule"] != rule.ID || e["_task"] != task.ID || e["_agent"] != worker.ID) {
			t.Fatalf("an approval for the task is recorded with its rule, task and agent: %v", e)
		}
	}
	// the answered request is approved for the task too (with its approval), not once: the grant covers the whole task
	if strings.Join(outcomes, ",") != "approved_for_task,approved_for_task" {
		t.Fatalf("outcomes %v", outcomes)
	}
	if evs := a.eventsOf("permission.decided"); evs[0]["approval"] != id || evs[1]["approval"] != nil {
		t.Fatalf("the answered request carries its approval and the covered call none: %v", evs)
	}
	var audited int
	_ = a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='permission.decided' AND detail LIKE ? AND detail LIKE ?`,
		"%"+security.PermissionApprovedForTask+"%", "%"+rule.ID+"%").Scan(&audited)
	if audited != 2 {
		t.Fatalf("%d audit entries of approvals for the task, want the answered request and the covered call", audited)
	}
	if n := len(a.eventsOf(core.EventTaskRuleAdded)); n != 1 {
		t.Fatalf("%d rule added events", n)
	}
	a.call("approval.get", map[string]string{"id": id3}, &det)
	if len(det.Rules) != 1 || det.Rules[0].ID != rule.ID || !det.ApproveForTask {
		t.Fatalf("the detail lists the task's rules: %+v", det.Rules)
	}

	// the third is approved for the task too, then that rule is removed by the person
	a.call("run.respond", map[string]any{"runId": runID, "requestId": id3, "decision": "approved", "scope": "task"}, nil)
	var second string
	a.waitFor("the second rule", func() bool {
		rs := a.detail(task.ID).AllowRules
		if len(rs) == 2 {
			second = rs[1].ID
		}
		return second != ""
	})
	var removed map[string]bool
	a.call("approval.rule.remove", map[string]string{"id": second}, &removed)
	if !removed["removed"] || len(a.detail(task.ID).AllowRules) != 1 {
		t.Fatal("the rule was not removed")
	}
	if err := a.callErr("approval.rule.remove", map[string]string{"id": second}); err == nil {
		t.Fatal("a removed rule was removed again")
	}

	// the review and merge; the task ends, and its rule with it
	a.waitFor("the review request", func() bool {
		d = a.detail(task.ID)
		return len(d.Pending) == 1 && d.Pending[0].Spec.Kind == "review"
	})
	a.call("run.respond", map[string]any{"runId": runID, "requestId": d.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("completion", func() bool { return a.detail(task.ID).Task.Status == tasks.Completed })
	if rs := a.detail(task.ID).AllowRules; len(rs) != 0 { // an ended task's rules are never read, deleted or not yet
		t.Fatalf("rules outlived the task: %+v", rs)
	}
	// the task's end hook runs just after its status is written: its event may come a moment after the status
	var reasons []string
	a.waitFor("the rule's end", func() bool {
		reasons = nil
		for _, e := range a.eventsOf(core.EventTaskRuleRemoved) {
			reasons = append(reasons, e["reason"].(string))
		}
		return len(reasons) == 2
	})
	if strings.Join(reasons, ",") != "removed,task ended" {
		t.Fatalf("rule removed events %v", reasons)
	}
	var left int
	_ = a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM permission_allow_rules WHERE task_id=?`, task.ID).Scan(&left)
	if left != 0 {
		t.Fatalf("%d rules outlived the task", left)
	}
	raw, _ := os.ReadFile(permLog)
	if n := strings.Count(string(raw), `"behavior":"allow"`); n != 3 {
		t.Fatalf("the worker was allowed %d calls, want 3: %s", n, raw)
	}
}

// A permission mode change is recorded with the mode it came from, and its detail says so.
func TestAPermissionModeChangeShowsFromAndTo(t *testing.T) {
	a := open(t, "")
	p, _ := a.setup("gocalc")
	var ag core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "W", Adapter: "claude", Role: "backend", Model: "fake-1"}, &ag)
	a.call("agent.update", map[string]any{"id": ag.ID, "permissionMode": "bypassPermissions", "confirmSkipsApprovals": true}, &ag)
	var id string
	if err := a.c.DB.Reader().QueryRow(`SELECT id FROM approvals WHERE action_kind=?`, security.KindPermissionEscalation).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var det core.ApprovalDetail
	a.call("approval.get", map[string]string{"id": id}, &det)
	if det.PermissionMode == nil || det.PermissionMode.From != "acceptEdits" || det.PermissionMode.To != "bypassPermissions" || det.Status != "CONSUMED" ||
		det.Agent == nil || det.Agent.ID != ag.ID || det.ProjectID != p.ID || det.ApproveForTask || det.RunID != "" ||
		!strings.Contains(det.Description, "from acceptEdits to bypassPermissions") {
		t.Fatalf("escalation detail %+v", det)
	}
}

// needsyou.ack marks an item done: it leaves the inbox and an event says so. A request is answered, not marked done.
func TestMarkingAnInboxItemDoneTakesItOut(t *testing.T) {
	a := open(t, "")
	p, _ := a.setup("gocalc")
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "x"}, &task)
	var trip int64
	err := a.c.DB.WriteTx(context.Background(), func(tx *sql.Tx) error {
		r, err := tx.Exec(`INSERT INTO watchdog_trips(task_id,signal,stage,created_at) VALUES(?,?,2,?)`, task.ID, "no_progress", storage.Now())
		if err == nil {
			trip, _ = r.LastInsertId()
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	key := "watchdog:" + jsonNumber(trip)
	if it, ok := a.needsItem("", key); !ok || it.TaskSeq != 1 {
		t.Fatalf("the pause is not in the inbox: %+v", it)
	}
	var res map[string]bool
	a.call("needsyou.ack", map[string]string{"key": key}, &res)
	if !res["acked"] {
		t.Fatalf("%v", res)
	}
	if _, ok := a.needsItem(p.ID, key); ok {
		t.Fatal("the item is still in the inbox")
	}
	evs := a.eventsOf(core.EventNeedsYouAcked)
	if len(evs) != 1 || evs[0]["key"] != key || evs[0]["kind"] != "watchdog_pause" || evs[0]["_task"] != task.ID {
		t.Fatalf("events %v", evs)
	}
	for _, k := range []string{key, "approval:apr_1", "chat-ask:x", "nonsense"} {
		if err := a.callErr("needsyou.ack", map[string]string{"key": k}); err == nil {
			t.Fatalf("%s was marked done", k)
		}
	}
}

func jsonNumber(n int64) string { b, _ := json.Marshal(n); return string(b) }

// agent.listAll lists every project's agents, each with its project.
func TestTheWorkspaceListsEveryProjectsAgents(t *testing.T) {
	a := open(t, "")
	p1, _ := a.setup("gocalc")
	p2, _ := a.setup("shop")
	a1 := a.agent(p1.ID, "Dana", "backend")
	a2 := a.agent(p2.ID, "Rex", "reviewer")
	var all []core.Agent
	a.call("agent.listAll", nil, &all)
	seen := map[string]string{}
	for _, ag := range all {
		seen[ag.ID] = ag.ProjectID
	}
	if len(all) != 2 || seen[a1.ID] != p1.ID || seen[a2.ID] != p2.ID {
		t.Fatalf("%+v", all)
	}
	var one []core.Agent
	a.call("agent.list", map[string]string{"projectId": p1.ID}, &one)
	if len(one) != 1 || one[0].ID != a1.ID {
		t.Fatalf("agent.list of one project %+v", one)
	}
}

// The crew's timeline: each agent's runs and waits that overlap the window, clipped to it, with the run's outcome and task.
func TestTheCrewTimelineClipsRunsAndWaitsToTheWindow(t *testing.T) {
	a := open(t, "")
	p1, _ := a.setup("gocalc")
	p2, _ := a.setup("shop")
	dana := a.agent(p1.ID, "Dana", "backend")
	rex := a.agent(p1.ID, "Rex", "reviewer")
	sam := a.agent(p2.ID, "Sam", "backend")
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p1.ID, Title: "Implement Subtract", Description: "x"}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": dana.ID, "reviewer": rex.ID}, &task)

	const h = int64(time.Hour / time.Millisecond)
	now := storage.Now()
	since, until := now-10*h, now-2*h
	exec := func(q string, args ...any) {
		t.Helper()
		if err := a.c.DB.WriteTx(context.Background(), func(tx *sql.Tx) error { _, err := tx.Exec(q, args...); return err }); err != nil {
			t.Fatal(err)
		}
	}
	run := func(id, agent, taskID, status string, start, end int64) {
		exec(`INSERT INTO agent_runs(id,agent_id,task_id,status,started_at,ended_at) VALUES(?,?,?,?,?,?)`, id, agent, taskID, status, start, end)
	}
	run("arun_before", dana.ID, task.ID, "STOPPED", since-3*h, since-2*h) // ends before the window: left out
	run("arun_edge", dana.ID, task.ID, "STOPPED", since-h, since+h)       // starts before it
	run("arun_fail", dana.ID, task.ID, "FAILED", since+2*h, since+3*h)    // inside
	run("arun_cancel", dana.ID, "", "CANCELLED", since+4*h, since+5*h)    // inside, no task
	run("arun_late", dana.ID, task.ID, "STOPPED", until-h, until+h)       // ends after it
	run("arun_live", rex.ID, task.ID, "WORKING", until-h, 0)              // still running
	run("arun_after", rex.ID, task.ID, "STOPPED", until+h/2, until+h)     // starts after it: left out
	run("arun_other", sam.ID, "", "STOPPED", since+h, since+2*h)          // another project
	payload, _ := json.Marshal(map[string]any{"action": map[string]any{"agentId": dana.ID}})
	exec(`INSERT INTO approvals(id,run_id,task_id,project_id,kind,title,payload,status,created_at,resolved_at,expires_at,action_kind) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		"apr_perm", "", task.ID, p1.ID, "approval", "Permission request: Bash: curl x", payload, "CONSUMED", since-h/2, since+h/2, since+h, security.KindShellCommand)
	exec(`INSERT INTO approvals(id,run_id,task_id,project_id,kind,title,phase,status,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"apr_review", "run_x", task.ID, p1.ID, "review", "Review and merge: Implement Subtract", "merge", "REQUESTED", until-h/2)
	exec(`INSERT INTO approvals(id,run_id,task_id,project_id,kind,title,payload,status,created_at,resolved_at,action_kind) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		"apr_mode", "", "", p1.ID, "approval", "Permission mode", payload, "CONSUMED", since+h, since+h, security.KindPermissionEscalation)

	var tl core.CrewTimeline
	a.call("agent.timeline", map[string]any{"projectId": p1.ID, "since": since, "until": until}, &tl)
	if tl.Since != since || tl.Until != until || len(tl.Agents) != 2 {
		t.Fatalf("timeline %+v", tl)
	}
	lane := map[string]core.AgentLane{}
	for _, l := range tl.Agents {
		lane[l.AgentID] = l
	}
	d, r := lane[dana.ID], lane[rex.ID]
	if d.Name != "Dana" || d.ProjectID != p1.ID || d.Adapter != "claude" || len(d.Runs) != 4 || len(r.Runs) != 1 {
		t.Fatalf("lanes %+v %+v", d, r)
	}
	want := []struct {
		id, outcome string
		start, end  int64
		seq         int
	}{
		{"arun_edge", core.RunFinished, since, since + h, 1},
		{"arun_fail", core.RunFailed, since + 2*h, since + 3*h, 1},
		{"arun_cancel", core.RunCancelled, since + 4*h, since + 5*h, 0},
		{"arun_late", core.RunFinished, until - h, until, 1},
	}
	for i, w := range want {
		g := d.Runs[i]
		if g.RunID != w.id || g.Outcome != w.outcome || g.Start != w.start || g.End == nil || *g.End != w.end || g.TaskSeq != w.seq {
			t.Fatalf("run %d: %+v (end %v), want %+v", i, g, g.End, w)
		}
	}
	if d.Runs[0].StartedAt != since-h || d.Runs[0].TaskTitle != "Implement Subtract" || d.Runs[3].EndedAt != until+h {
		t.Fatalf("the run's own times and task: %+v", d.Runs)
	}
	// a window that ends in the past closes a run still going at its end
	if lv := r.Runs[0]; lv.RunID != "arun_live" || lv.Outcome != core.RunWorking || lv.End == nil || *lv.End != until || lv.EndedAt != 0 {
		t.Fatalf("live run %+v", lv)
	}
	if len(d.Waits) != 2 {
		t.Fatalf("waits %+v", d.Waits)
	}
	if w := d.Waits[0]; w.ApprovalID != "apr_perm" || w.Start != since || w.End == nil || *w.End != since+h/2 || w.TaskSeq != 1 || w.Status != "CONSUMED" {
		t.Fatalf("permission wait %+v", w)
	}
	if w := d.Waits[1]; w.ApprovalID != "apr_review" || w.Phase != "merge" || w.Start != until-h/2 || w.End == nil || *w.End != until {
		t.Fatalf("review wait (the assignee's) %+v", w)
	}

	// up to now: what has not ended is open; the whole workspace has every project's lanes
	a.call("agent.timeline", map[string]any{"projectId": "", "since": since}, &tl)
	lane = map[string]core.AgentLane{}
	for _, l := range tl.Agents {
		lane[l.AgentID] = l
	}
	if len(tl.Agents) != 3 || len(lane[sam.ID].Runs) != 1 || lane[sam.ID].ProjectID != p2.ID {
		t.Fatalf("workspace timeline %+v", tl.Agents)
	}
	rr := lane[rex.ID].Runs
	if len(rr) != 2 || rr[0].RunID != "arun_live" || rr[0].End != nil || rr[1].RunID != "arun_after" {
		t.Fatalf("rex up to now %+v", rr)
	}
	if w := lane[dana.ID].Waits; len(w) != 2 || w[1].End != nil {
		t.Fatalf("an open wait up to now %+v", w)
	}
	if err := a.callErr("agent.timeline", map[string]any{"since": 0}); err == nil {
		t.Fatal("a timeline without a start")
	}
}

// security.scan.running lists every scan in progress, of a project or a folder, with its name and engines.
func TestRunningScansAreListedEverywhere(t *testing.T) {
	a, _ := securityCore(t, "--sleep-ms=20000")
	var p core.Project
	a.call("project.create", map[string]any{"name": "shop", "repoPath": newRepo(t)}, &p)
	var none []core.RunningScan
	a.call("security.scan.running", nil, &none)
	if len(none) != 0 {
		t.Fatalf("%+v", none)
	}
	folder := newRepo(t)
	var rep scan.Report
	a.call("security.scan.start", map[string]any{"projectId": p.ID}, &rep)
	a.call("security.scan.start", map[string]any{"folder": folder}, &rep)
	var running []core.RunningScan
	a.call("security.scan.running", nil, &running)
	if len(running) != 2 {
		t.Fatalf("%+v", running)
	}
	names := map[string]core.RunningScan{}
	for _, r := range running {
		names[r.Name] = r
		if len(r.Engines) != 3 || r.By != "human:kyle" || r.StartedAt == 0 || r.ID == "" {
			t.Fatalf("%+v", r)
		}
	}
	if pr := names["shop"]; pr.ProjectID != p.ID || pr.Folder != "" {
		t.Fatalf("project scan %+v", running)
	}
	if fr := names[filepath.Base(folder)]; fr.Folder == "" || fr.ProjectID != "" {
		t.Fatalf("folder scan %+v", running)
	}
	a.call("security.scan.cancel", map[string]any{"projectId": p.ID}, nil)
	a.call("security.scan.cancel", map[string]any{"folder": folder}, nil)
	a.waitFor("the scans to end", func() bool {
		a.call("security.scan.running", nil, &running)
		return len(running) == 0
	})
}

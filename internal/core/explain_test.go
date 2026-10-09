package core_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The run explanation: every node execution of the task's run with its manifest, prompt, usage and routing decision, the
// graph it ran on, its checkpoints and its events.
func TestRunExplanationExplainsEveryTurn(t *testing.T) {
	a := open(t, "")
	p, _ := a.setup("gocalc")
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &r)
	var task, other tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "make Subtract return a - b"}, &task)
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Other"}, &other)

	var none core.RunExplanation
	a.call("run.explain", map[string]string{"taskId": task.ID}, &none)
	if none.Run != nil || len(none.Executions) != 0 || len(none.Runs) != 0 {
		t.Fatalf("a task that never ran: %+v", none)
	}

	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	a.waitFor("the human request", func() bool {
		var d core.TaskDetail
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		return len(d.Pending) == 1
	})

	var x core.RunExplanation
	a.call("run.explain", map[string]string{"taskId": task.ID}, &x)
	if x.Run == nil || string(x.Run.ID) != runID || len(x.Runs) != 1 || x.Graph == nil || len(x.Graph.Nodes) == 0 || len(x.Graph.Edges) == 0 {
		t.Fatalf("run %+v runs %+v graph %+v", x.Run, x.Runs, x.Graph)
	}
	var work *core.ExecView
	for i := range x.Executions {
		if x.Executions[i].NodeID == "work" {
			work = &x.Executions[i]
		}
	}
	if work == nil || work.Kind != "agent_task" || work.AgentID != w.ID || !work.HasPrompt || work.LogID == "" || work.Started == 0 || work.Finished == 0 {
		t.Fatalf("work execution %+v", work)
	}
	var promptItem bool
	for _, it := range work.Manifest {
		promptItem = promptItem || (strings.HasPrefix(it.Source, "prompt") && it.Included)
	}
	if !promptItem || work.Included == 0 {
		t.Fatalf("manifest %+v", work.Manifest)
	}
	if work.Decision == nil || work.Decision.DecidedBy == "" {
		t.Fatalf("no routing decision on the work turn: %+v", work.Decision)
	}
	latest := 0
	for _, cp := range x.Checkpoints {
		if cp.Latest {
			latest++
		}
	}
	if len(x.Checkpoints) == 0 || latest != 1 || len(x.Events) == 0 {
		t.Fatalf("checkpoints %+v events %d", x.Checkpoints, len(x.Events))
	}

	var prompt string
	a.call("run.prompt", map[string]any{"taskId": task.ID, "runId": runID, "nodeId": "work", "step": work.Step, "attempt": work.Attempt}, &prompt)
	if !strings.Contains(prompt, "Subtract") {
		t.Fatalf("prompt %q", prompt)
	}
	for _, bad := range []struct {
		method string
		params map[string]any
	}{
		{"run.prompt", map[string]any{"taskId": other.ID, "runId": runID, "nodeId": "work", "step": work.Step, "attempt": work.Attempt}},
		{"run.prompt", map[string]any{"taskId": task.ID, "runId": runID, "nodeId": "../../x", "step": 1, "attempt": 1}},
		{"run.explain", map[string]any{"taskId": other.ID, "runId": runID}},
	} {
		if _, err := a.c.Call(t.Context(), bad.method, mustJSON(bad.params)); err == nil {
			t.Errorf("%s %v was answered", bad.method, bad.params)
		}
	}
}

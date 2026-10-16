package core_test

import (
	"fmt"
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

// An explanation of a run with many turns reads the same things as before and attaches to each execution its own manifest and the
// usage recorded while it ran, in the order it was recorded (audit 2026-10-16, L18: they were asked of the database once per
// execution). The events are oldest first and the newest are kept when there are too many.
func TestAnExplanationOfALongRunAttachesEachTurnItsOwnManifestAndUsage(t *testing.T) {
	a := open(t, "")
	p, _ := a.setup("gocalc")
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "A long run"}, &task)
	const run = "run_aaaaaaaaaaaa"
	a.sql(`INSERT INTO workflow_runs(id,workflow,status,topology_hash,task_id,project_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, run, "loop", "COMPLETED", "h", task.ID, p.ID, 1000, 1000)
	a.sql(`UPDATE tasks SET run_id=? WHERE id=?`, run, task.ID)
	const turns = 40
	for i := 0; i < turns; i++ {
		start := int64(10000 + i*100)
		a.sql(`INSERT INTO node_executions(run_id,node_id,step,attempt,status,started_at,finished_at) VALUES(?,?,?,?,?,?,?)`, run, "work", i+1, i+1, "done", start, start+50)
		a.sql(`INSERT INTO context_manifests(run_id,node_id,attempt,task_id,agent_id,items,total_bytes,created_at) VALUES(?,?,?,?,?,?,?,?)`,
			run, "work", i+1, task.ID, fmt.Sprintf("agent_%02d", i), fmt.Sprintf(`[{"source":"memory:item-%d","included":true,"bytes":%d}]`, i, 100+i), 100+i, start)
		// two usage rows inside this turn, one between turns (belongs to none)
		a.sql(`INSERT INTO model_usage(ts,task_id,agent_id,provider,model,tier,input_tokens,cost_usd) VALUES(?,?,?,?,?,?,?,?)`, start+10, task.ID, fmt.Sprintf("agent_%02d", i), "anthropic", "m", "cheap", i, 0.01)
		a.sql(`INSERT INTO model_usage(ts,task_id,agent_id,provider,model,tier,input_tokens,cost_usd) VALUES(?,?,?,?,?,?,?,?)`, start+20, task.ID, fmt.Sprintf("agent_%02d", i), "anthropic", "m", "cheap", i+1000, 0.02)
		a.sql(`INSERT INTO model_usage(ts,task_id,agent_id,provider,model,tier,input_tokens,cost_usd) VALUES(?,?,?,?,?,?,?,?)`, start+75, task.ID, "between", "anthropic", "m", "cheap", 7, 0.5)
	}
	// 305 events of the run: only the newest 300 are listed, oldest first; a few are MCP calls
	for i := 0; i < 305; i++ {
		typ := "test.event"
		if i%100 == 7 {
			typ = "mcp.call"
		}
		a.sql(`INSERT INTO events(ts,type,project_id,run_id,task_id,agent_id,data) VALUES(?,?,?,?,?,?,?)`, int64(20000+i), typ, p.ID, run, task.ID, "", fmt.Sprintf(`{"n":%d}`, i))
	}

	var x core.RunExplanation
	a.call("run.explain", map[string]string{"taskId": task.ID, "runId": run}, &x)
	if len(x.Executions) != turns {
		t.Fatalf("%d executions", len(x.Executions))
	}
	for i, ex := range x.Executions {
		if ex.Attempt != i+1 || ex.AgentID != fmt.Sprintf("agent_%02d", i) || ex.Included != 100+i || len(ex.Manifest) != 1 || ex.Manifest[0].Source != fmt.Sprintf("memory:item-%d", i) {
			t.Fatalf("execution %d: %+v", i, ex)
		}
		if len(ex.Usage) != 2 || ex.Usage[0].Input != i || ex.Usage[1].Input != i+1000 || ex.Usage[0].TS != int64(10000+i*100+10) {
			t.Fatalf("execution %d usage: %+v", i, ex.Usage)
		}
	}
	if !x.EventsTruncated || len(x.Events) != 300 {
		t.Fatalf("events: truncated %v, %d listed", x.EventsTruncated, len(x.Events))
	}
	for i := 1; i < len(x.Events); i++ {
		if x.Events[i].Seq <= x.Events[i-1].Seq {
			t.Fatalf("events are not oldest first at %d", i)
		}
	}
	if string(x.Events[len(x.Events)-1].Data) != "" && x.Events[len(x.Events)-1].Seq == 0 {
		t.Fatal("the newest event is missing")
	}
	// the mcp calls among the newest 300 (events 5.. of 305), oldest first
	if len(x.MCPCalls) != 3 || x.MCPCalls[0].Seq >= x.MCPCalls[1].Seq || x.MCPCalls[1].Seq >= x.MCPCalls[2].Seq {
		t.Fatalf("mcp calls: %+v", x.MCPCalls)
	}
}

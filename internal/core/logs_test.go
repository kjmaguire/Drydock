package core_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The Logs tab lists a task's check logs and agent streams and reads them by range; an id is only honoured for its own task.
func TestTaskLogsListAndReadByRange(t *testing.T) {
	a := open(t, "")
	p, _ := a.setup("gocalc")
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &r)
	var task, other tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "make Subtract return a - b"}, &task)
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Other"}, &other)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	a.waitFor("the human request", func() bool {
		var d core.TaskDetail
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		return len(d.Pending) == 1
	})
	var logs []core.LogSource
	a.call("logs.list", map[string]string{"taskId": task.ID}, &logs)
	var check, agent *core.LogSource
	for i := range logs {
		switch {
		case logs[i].Kind == "check" && logs[i].Title == "test":
			check = &logs[i]
		case logs[i].Kind == "agent" && agent == nil:
			agent = &logs[i]
		}
	}
	if check == nil || agent == nil || check.Passed == nil || !*check.Passed {
		t.Fatalf("logs %+v", logs)
	}
	var txt core.LogText
	a.call("logs.read", map[string]any{"taskId": task.ID, "id": check.ID}, &txt)
	if !strings.Contains(txt.Text, "ok") || txt.Next != txt.Size {
		t.Fatalf("check log %+v", txt)
	}
	a.call("logs.read", map[string]any{"taskId": task.ID, "id": agent.ID}, &txt)
	if !strings.Contains(txt.Text, "implemented Subtract") || strings.Contains(txt.Text, `"kind"`) {
		t.Fatalf("agent stream not rendered: %q", txt.Text)
	}
	// a small range ends on a line boundary
	var part core.LogText
	a.call("logs.read", map[string]any{"taskId": task.ID, "id": agent.ID, "limit": 40}, &part)
	if part.Next > 40 {
		t.Fatalf("range %+v", part)
	}
	var tail core.LogText
	a.call("logs.read", map[string]any{"taskId": task.ID, "id": check.ID, "tail": true, "limit": 10}, &tail)
	if tail.Next != tail.Size || tail.Start == 0 && tail.Size > 10 {
		t.Fatalf("tail %+v", tail)
	}
	for _, bad := range []map[string]any{
		{"taskId": other.ID, "id": check.ID},
		{"taskId": other.ID, "id": agent.ID},
		{"taskId": task.ID, "id": "ev:999999"},
		{"taskId": task.ID, "id": "/etc/passwd"},
	} {
		if _, err := a.c.Call(t.Context(), "logs.read", mustJSON(bad)); err == nil {
			t.Errorf("%v was read", bad)
		}
	}
}

// A finished task's raw output stays readable after retention compacts it (30 days) and is gone after it is deleted (90
// days); the run's row and the task stay. The sizes are logged: they are the growth measurement docs/packaging.md quotes.
func TestRetentionKeepsAFinishedTaskReadableThenDeletesItsRawOutput(t *testing.T) {
	a := open(t, "")
	ctx := t.Context()
	p, _ := a.setup("gocalc")
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &r)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "make Subtract return a - b"}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var d core.TaskDetail
	a.waitFor("the human request", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		return len(d.Pending) == 1
	})
	a.call("run.respond", map[string]any{"runId": runID, "requestId": d.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("the run to end", func() bool {
		var st string
		_ = a.c.DB.Reader().QueryRowContext(ctx, `SELECT status FROM workflow_runs WHERE id=?`, runID).Scan(&st)
		return st == "COMPLETED"
	})
	readAll := func() (logs []core.LogSource, texts []string) {
		a.call("logs.list", map[string]string{"taskId": task.ID}, &logs)
		for _, l := range logs {
			var txt core.LogText
			a.call("logs.read", map[string]any{"taskId": task.ID, "id": l.ID}, &txt)
			texts = append(texts, txt.Text)
		}
		return logs, texts
	}
	logsBefore, textsBefore := readAll()
	var ex core.RunExplanation
	a.call("run.explain", map[string]string{"taskId": task.ID, "runId": runID}, &ex)
	var prompted *core.ExecView
	for i := range ex.Executions {
		if ex.Executions[i].HasPrompt {
			prompted = &ex.Executions[i]
			break
		}
	}
	if len(logsBefore) < 2 || prompted == nil {
		t.Fatalf("logs %d, a turn with a prompt %v", len(logsBefore), prompted != nil)
	}
	promptBefore, err := a.c.TurnPrompt(ctx, task.ID, runID, prompted.NodeID, prompted.Step, prompted.Attempt)
	if err != nil || promptBefore == "" {
		t.Fatalf("prompt: %v", err)
	}

	var events, eventBytes, cps, cpBytes, execs, execBytes int64
	_ = a.c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(length(data)),0) FROM events WHERE task_id=? OR run_id=?`, task.ID, runID).Scan(&events, &eventBytes)
	_ = a.c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(length(state)),0) FROM workflow_checkpoints WHERE run_id=?`, runID).Scan(&cps, &cpBytes)
	_ = a.c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(length(output)),0) FROM node_executions WHERE run_id=?`, runID).Scan(&execs, &execBytes)
	t.Logf("one task in the database: %d events (%d bytes of data), %d checkpoints (%d bytes of state), %d node executions (%d bytes of output)",
		events, eventBytes, cps, cpBytes, execs, execBytes)

	// 31 days later: compacted, and every log and the prompt read exactly as before
	rep, err := a.c.RunRetention(ctx, time.Now().Add(31*24*time.Hour))
	if err != nil || rep.RunsCompacted != 1 || rep.FilesCompressed == 0 {
		t.Fatalf("compaction %+v %v", rep, err)
	}
	t.Logf("one task's raw output: %d files, %d bytes, compacted to %d bytes; %d checkpoints deleted, the latest kept",
		rep.FilesCompressed, rep.BytesBefore, rep.BytesAfter, rep.CheckpointsDeleted)
	logsAfter, textsAfter := readAll()
	if len(logsAfter) != len(logsBefore) || strings.Join(textsAfter, "\x00") != strings.Join(textsBefore, "\x00") {
		t.Fatalf("logs changed by compaction: %d -> %d", len(logsBefore), len(logsAfter))
	}
	for i := range logsAfter {
		if logsAfter[i].Size != logsBefore[i].Size {
			t.Errorf("%s size %d -> %d", logsAfter[i].ID, logsBefore[i].Size, logsAfter[i].Size)
		}
	}
	if got, err := a.c.TurnPrompt(ctx, task.ID, runID, prompted.NodeID, prompted.Step, prompted.Attempt); err != nil || got != promptBefore {
		t.Fatalf("prompt after compaction: %v", err)
	}

	// 91 days later: the raw output is gone, the task and its run stay
	rep, err = a.c.RunRetention(ctx, time.Now().Add(91*24*time.Hour))
	if err != nil || rep.RunsDeleted != 1 {
		t.Fatalf("deletion %+v %v", rep, err)
	}
	t.Logf("deleted after 90 days: %d bytes on disk", rep.BytesDeleted)
	var left []core.LogSource
	a.call("logs.list", map[string]string{"taskId": task.ID}, &left)
	if len(left) != 0 {
		t.Fatalf("logs still listed after deletion: %+v", left)
	}
	a.call("task.get", map[string]string{"id": task.ID}, &d)
	if d.Task.Status != tasks.Completed {
		t.Fatalf("task %s", d.Task.Status)
	}
	if _, err := a.c.TurnPrompt(ctx, task.ID, runID, prompted.NodeID, prompted.Step, prompted.Attempt); err == nil {
		t.Fatal("a deleted prompt was read")
	}
}

package core_test

import (
	"strings"
	"testing"

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

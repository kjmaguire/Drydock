package core_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// Item 5 through the app: a worker that spins twice is nudged, then paused; the pause writes a handoff artifact; the human
// rejects the pause and the task stops cleanly (CANCELLED, run completed), instead of the answer flowing on as work output.
func TestWatchdogPauseWritesAHandoffAndARejectStopsTheTask(t *testing.T) {
	spin := []fakeagent.Step{{Tool: "Read", Input: json.RawMessage(`{"file_path":"calc.go"}`), Result: "package gocalc // identical", Repeat: 3}, {Block: true}}
	a, _ := openDev(t, fakeagent.Script{Turns: [][]fakeagent.Step{spin, spin}})
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "x", Acceptance: []string{"Given 5 and 3 When Subtract is called Then 2"}}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var detail core.TaskDetail
	a.waitFor("the watchdog pause", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1
	})
	pend := detail.Pending[0]
	if !strings.Contains(pend.Spec.Title, "Watchdog paused") || !pend.Spec.DeliverAnswer {
		t.Fatalf("pending %+v", pend)
	}
	var handoff string
	if err := a.c.DB.Reader().QueryRow(`SELECT artifact FROM handoffs WHERE task_id=? AND reason='watchdog'`, task.ID).Scan(&handoff); err != nil {
		t.Fatalf("no handoff artifact: %v", err)
	}
	if !strings.Contains(handoff, "tool_spin") || !strings.Contains(handoff, "Implement Subtract") || !strings.Contains(string(pend.Spec.Payload), "hnd_") {
		t.Fatalf("handoff %s payload %s", handoff, pend.Spec.Payload)
	}
	a.call("run.respond", map[string]any{"runId": runID, "requestId": pend.ID, "decision": "rejected", "data": map[string]string{}}, nil)
	a.waitFor("the task stops", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return detail.Task.Status == tasks.Cancelled && detail.Run != nil && detail.Run.Status == "COMPLETED"
	})
}

// Item 9: the brief hook (Deps.ProjectBrief, wired by the core's owner) also feeds the generated AGENTS.md.
func TestProjectBriefReachesAgentsMD(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	a.c.Deps.ProjectBrief = func(ctx context.Context, projectID string) string {
		if projectID == p.ID {
			return "A calculator library for primary-school pupils."
		}
		return ""
	}
	dir := t.TempDir()
	res, _, err := a.c.Context.Write(context.Background(), p.ID, dir)
	if err != nil || len(res.Written) == 0 {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(string(b), "## Standing instructions") || !strings.Contains(string(b), "Project brief, written by the human who owns this project") || !strings.Contains(string(b), "primary-school pupils") {
		t.Fatalf("AGENTS.md:\n%s", b)
	}
}

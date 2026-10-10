package core_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/contract"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The product loop with REAL Claude Code as worker and reviewer, through the same UI methods, then the eval report from the
// real database: this is where "cost per accepted task" gets its first measured number. It spends real money (haiku, a few
// cents to a few tens of cents), so it runs only when DRYDOCK_REAL_CLAUDE=1 and `claude` is installed and logged in.
func TestRealClaudeLoopThroughTheCoreAndTheEvalReport(t *testing.T) {
	if why := contract.RealClaudeSkip(); why != "" {
		t.Skip(why)
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude is not installed")
	}
	dir := t.TempDir()
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "d"), User: "kyle", LeaseTTL: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a := &app{t: t, c: c}
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Worker", Adapter: "claude", Role: "backend", Model: "haiku"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer", Adapter: "claude", Role: "reviewer", Model: "haiku"}, &r)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract",
		Description: "calc.go has a stub Subtract that returns 0. Implement it so a - b is returned and all tests pass.",
		Acceptance:  []string{"Given 5 and 3 When Subtract is called Then it returns 2"}}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var d core.TaskDetail
	deadline := time.Now().Add(8 * time.Minute)
	for time.Now().Before(deadline) {
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		if len(d.Pending) == 1 || (d.Run != nil && d.Run.Status.Terminal()) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if len(d.Pending) != 1 {
		t.Fatalf("no human request; run %+v task %s", d.Run, d.Task.Status)
	}
	a.call("run.respond", map[string]any{"runId": runID, "requestId": d.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("completion", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		return d.Task.Status == tasks.Completed
	})

	var rep struct {
		Markdown string `json:"markdown"`
	}
	a.call("evals.report", map[string]any{}, &rep)
	var usage []core.UsageRow
	a.call("usage.list", map[string]any{"taskId": task.ID}, &usage)
	t.Logf("task cost (sum of reported usage): $%.4f over %d iteration(s), %d usage row(s)", d.Task.CostUSD, d.Task.Iteration+1, len(usage))
	for _, u := range usage {
		t.Logf("  usage: agent=%s model=%s tier=%s in=%d out=%d cached=%d cost=$%.4f source=%s", u.AgentID, u.Model, u.Tier, u.Input, u.Output, u.Cached, u.CostUSD, u.Source)
	}
	t.Logf("\n%s", rep.Markdown)
	if d.Task.CostUSD <= 0 || len(usage) == 0 {
		t.Fatal("a real run must have recorded reported usage")
	}
	if !strings.Contains(rep.Markdown, "accepted") && !strings.Contains(rep.Markdown, "Accepted") {
		t.Fatal("the report should speak about accepted tasks")
	}
}

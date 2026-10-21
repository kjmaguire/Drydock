package core_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/contract"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/sandbox/sandboxtest"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// REAL Claude Code as a sandboxed worker against a real container engine: restricted mode leaves it no shell, so the only way it
// can run the tests is the gateway's sandbox_run, which runs them in the container. It is also asked to run a command that
// takes longer than two minutes, which shows the CLI waits for a long tool call. Paid (haiku, cents), so it runs only with
// DRYDOCK_REAL_CLAUDE=1, claude installed and logged in, and a container engine.
func TestRealClaudeWorksInTheSandbox(t *testing.T) {
	if why := contract.RealClaudeSkip(); why != "" {
		t.Skip(why)
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude is not installed")
	}
	bin := sandboxtest.Engine(t)
	img := sandboxtest.Go(t, bin)
	dir := t.TempDir()
	if keep := os.Getenv("DRYDOCK_KEEP_TEST_DATA"); keep != "" { // to read the agents' transcripts afterwards
		dir = keep
	}
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "d"), User: "kyle", LeaseTTL: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a := &app{t: t, c: c}
	repo := newRepo(t)
	def, _ := json.Marshal(map[string]any{"image": img, "network": "none",
		"env": map[string]string{"CGO_ENABLED": "0", "GOTOOLCHAIN": "local", "GOCACHE": "/tmp/gocache", "GOPATH": "/tmp/gopath", "GOFLAGS": "-mod=mod"}})
	if err := os.MkdirAll(filepath.Join(repo, ".drydock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".drydock", "sandbox.json"), def, 0o644); err != nil {
		t.Fatal(err)
	}
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	gitIn(t, repo, "add", ".drydock/sandbox.json")
	gitIn(t, repo, "commit", "-q", "-m", "sandbox definition")
	a.call("sandbox.set", map[string]any{"projectId": p.ID, "enabled": true}, nil)
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Worker", Adapter: "claude", Role: "backend", Model: "haiku"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer", Adapter: "claude", Role: "reviewer", Model: "haiku"}, &r)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract",
		Description: "calc.go has a stub Subtract that returns 0. Implement it so a - b is returned. Before you finish, run `go test ./...` in the sandbox and make sure it passes. Also run `sleep 130 && echo LONG-COMMAND-DONE` in the sandbox once and say in your summary whether you saw LONG-COMMAND-DONE.",
		Acceptance:  []string{"Given 5 and 3 When Subtract is called Then it returns 2"}}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var d core.TaskDetail
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		if len(d.Pending) == 1 || (d.Run != nil && d.Run.Status.Terminal()) {
			break
		}
		time.Sleep(time.Second)
	}
	if len(d.Pending) != 1 || d.Pending[0].Spec.Kind != "review" {
		t.Fatalf("no merge approval: run %+v task %s pending %+v", d.Run, d.Task.Status, d.Pending)
	}
	evs, err := c.Replay(context.Background(), 0, events.Filter{Types: []string{"sandbox.command"}, TaskID: task.ID}, 200)
	if err != nil {
		t.Fatal(err)
	}
	var cmds []string
	var ranTests, longDone bool
	for _, e := range evs {
		var m struct {
			Command    string `json:"command"`
			ExitCode   int    `json:"exitCode"`
			DurationMs int64  `json:"durationMs"`
			TimedOut   bool   `json:"timedOut"`
		}
		_ = json.Unmarshal(e.Data, &m)
		cmds = append(cmds, m.Command)
		if strings.Contains(m.Command, "go test") && m.ExitCode == 0 {
			ranTests = true
		}
		if strings.Contains(m.Command, "sleep 130") && m.ExitCode == 0 && m.DurationMs >= 129000 && !m.TimedOut {
			longDone = true
		}
	}
	t.Logf("sandbox commands the worker ran: %q", cmds)
	if !ranTests {
		t.Fatalf("the worker did not run the tests in the sandbox: %q", cmds)
	}
	if !longDone {
		t.Errorf("the long command did not finish through the gateway: %q", cmds)
	}
	for _, e := range d.Evidence {
		if e.Kind == "test" && (e.Passed == nil || !*e.Passed) {
			t.Fatalf("the project's tests did not pass in the container: %+v", e)
		}
	}
	a.call("run.respond", map[string]any{"runId": runID, "requestId": d.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("completion and cleanup", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		return d.Task.Status == tasks.Completed && sandboxtest.Containers(t, bin, "drydock.task="+task.ID) == 0
	})
	b, _ := os.ReadFile(filepath.Join(repo, "calc.go"))
	if !strings.Contains(string(b), "a - b") {
		t.Fatalf("main does not have the change:\n%s", b)
	}
}

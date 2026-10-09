package core_test

import (
	"os"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// worker-commands.md, tier 1: a project can let its Claude workers run its checks. Off by default. The reviewer never gets
// them. The rules are exact command lines, and the worker is told which ones Drydock will run after its turn.

var goCheckLines = []string{"go build ./...", "go test ./...", "go vet ./..."}

// turn is what one captured turn was started with.
type turn struct {
	allowed []string
	system  string
}

// runOneTask runs a task with a worker and a reviewer on a captured Claude adapter and returns the worker's and the
// reviewer's turns, failing unless each ran at least once.
func runOneTask(t *testing.T, workerChecks bool) (worker, reviewer []turn, a *app, workerID string) {
	t.Helper()
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	a = openWith(t, map[string]agents.Adapter{"claude": capt})
	p, _ := a.setup("gocalc")
	if workerChecks {
		var s pipeline.DevSettings
		a.call("projectdev.get", map[string]string{"projectId": p.ID}, &s)
		s.WorkerChecks = true
		a.call("projectdev.set", map[string]any{"projectId": p.ID, "settings": s}, &s)
		if !s.WorkerChecks {
			t.Fatal("the switch was not stored")
		}
	}
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &r)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	a.waitFor("the human request", func() bool {
		var d core.TaskDetail
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		return len(d.Pending) == 1
	})
	for _, s := range capt.all() {
		switch s.Spec.AgentID {
		case r.ID:
			reviewer = append(reviewer, turn{s.Spec.AllowedTools, s.Spec.SystemAppend})
		case w.ID:
			worker = append(worker, turn{s.Spec.AllowedTools, s.Spec.SystemAppend})
		}
	}
	if len(worker) == 0 || len(reviewer) == 0 {
		t.Fatalf("turns captured: %d worker, %d reviewer; want at least one of each", len(worker), len(reviewer))
	}
	return worker, reviewer, a, w.ID
}

func bashRules(ts []turn) []string {
	var out []string
	for _, x := range ts {
		for _, r := range x.allowed {
			if strings.HasPrefix(r, "Bash") {
				out = append(out, r)
			}
		}
	}
	return out
}

func TestWorkersGetNoCommandsUnlessTheProjectAllowsIt(t *testing.T) {
	worker, reviewer, a, wid := runOneTask(t, false)
	if got := bashRules(append(worker, reviewer...)); len(got) != 0 {
		t.Fatalf("Bash rules without the switch: %v", got)
	}
	for _, x := range worker {
		if strings.Contains(x.system, "Bash tool") {
			t.Fatalf("the worker was told about commands without the switch: %q", x.system)
		}
	}
	var d core.AgentDetail
	a.call("agent.get", map[string]string{"id": wid}, &d)
	if !strings.Contains(d.Isolation.Summary, "hold shell commands for approval") {
		t.Fatalf("isolation without the switch: %+v", d.Isolation)
	}
}

func TestWorkerChecksAreExactCommandsTheWorkerIsToldAboutAndNeverForTheReviewer(t *testing.T) {
	worker, reviewer, a, wid := runOneTask(t, true)
	want := checks.WorkerRules()
	for _, x := range worker {
		var got []string
		for _, r := range x.allowed {
			if strings.HasPrefix(r, "Bash") {
				got = append(got, r)
			}
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("worker rules %v, want %v", got, want)
		}
		if !strings.Contains(x.system, pipeline.WorkerChecksText(goCheckLines)) {
			t.Fatalf("the worker was not told the commands Drydock runs: %q", x.system)
		}
	}
	if got := bashRules(reviewer); len(got) != 0 {
		t.Fatalf("the reviewer was given %v", got)
	}
	for _, x := range reviewer {
		if strings.Contains(x.system, "Bash tool") {
			t.Fatalf("the reviewer was told about commands: %q", x.system)
		}
	}
	var d core.AgentDetail
	a.call("agent.get", map[string]string{"id": wid}, &d)
	if strings.Contains(d.Isolation.Summary, "hold shell commands for approval") || !strings.Contains(d.Isolation.Summary, "any program with your rights") {
		t.Fatalf("the isolation panel must say what the switch allows: %+v", d.Isolation)
	}
}

func TestOnlyAHumanCanSwitchWorkerChecksOn(t *testing.T) {
	err := pipeline.SaveDevSettings(t.Context(), nil, "p", pipeline.DevSettings{WorkerChecks: true}, "agent:x")
	if err == nil {
		t.Fatal("an agent identity changed the settings")
	}
}

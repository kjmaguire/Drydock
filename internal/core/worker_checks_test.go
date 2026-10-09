package core_test

import (
	"os"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// worker-commands.md, tier 1: a project can let its Claude workers run exactly the checks Drydock runs itself. Off by default.
// The reviewer never gets them. The rules are the exact detected command lines.

var goCheckRules = []string{"Bash(go build ./...)", "Bash(go test ./...)", "Bash(go vet ./...)"}

// runOneTask runs a task with a worker and a reviewer on a captured Claude adapter and returns what each turn was started with.
func runOneTask(t *testing.T, workerChecks bool) (worker, reviewer []string) {
	t.Helper()
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	a := openWith(t, map[string]agents.Adapter{"claude": capt})
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
		if s.Spec.Role == "reviewer" {
			reviewer = append(reviewer, s.Spec.AllowedTools...)
		} else if s.Spec.AgentID == w.ID {
			worker = append(worker, s.Spec.AllowedTools...)
		}
	}
	return worker, reviewer
}

func TestWorkersGetNoCommandsUnlessTheProjectAllowsIt(t *testing.T) {
	worker, reviewer := runOneTask(t, false)
	for _, r := range append(worker, reviewer...) {
		if strings.HasPrefix(r, "Bash") {
			t.Fatalf("a Bash rule without the switch: %q", r)
		}
	}
}

func TestWorkerChecksAreTheExactDetectedCommandsAndNeverForTheReviewer(t *testing.T) {
	worker, reviewer := runOneTask(t, true)
	var got []string
	for _, r := range worker {
		if strings.HasPrefix(r, "Bash") {
			got = append(got, r)
		}
	}
	if strings.Join(got, "|") != strings.Join(goCheckRules, "|") {
		t.Fatalf("worker rules %v, want %v", got, goCheckRules)
	}
	for _, r := range reviewer {
		if strings.HasPrefix(r, "Bash") {
			t.Fatalf("the reviewer was given %q", r)
		}
	}
}

func TestOnlyAHumanCanSwitchWorkerChecksOn(t *testing.T) {
	err := pipeline.SaveDevSettings(t.Context(), nil, "p", pipeline.DevSettings{WorkerChecks: true}, "agent:x")
	if err == nil {
		t.Fatal("an agent identity changed the settings")
	}
}

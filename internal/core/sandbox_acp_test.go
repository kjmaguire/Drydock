package core_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/sandbox/sandboxtest"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// Decision P131: an ACP worker (the scripted ACP agent, a labelled test double that is a real separate process on the real ACP
// SDK, standing in for a CLI whose manifest says it asks before its own commands) works in a sandboxed project. Its terminal
// runs in the task's container (the working folder at /workspace, the file it writes lands in the worktree), the command its
// CLI would run itself is refused, an edit in the working folder is allowed, and the checks run in the container. Real engine
// only (skipped without one).
func TestAnACPWorkerRunsItsTerminalInTheTasksContainer(t *testing.T) {
	bin := sandboxtest.Engine(t)
	img := sandboxtest.Go(t, bin)
	script, _ := json.Marshal(fakeacp.Script{Default: []fakeacp.Step{
		{Perm: &fakeacp.Perm{Tool: fakeacp.Tool{ID: "t1", Title: "Shell", Kind: "execute", Input: map[string]any{"command": "go test ./..."}}}},
		{Perm: &fakeacp.Perm{Tool: fakeacp.Tool{ID: "t2", Title: "Edit calc.go", Kind: "edit", Input: map[string]any{"path": "calc.go"}, Locations: []string{"calc.go"}}}},
		{Term: &fakeacp.TermOp{Command: "sh", Args: []string{"-c", "echo IN-THE-CONTAINER > from-term.txt; pwd"}}},
		{FSWrite: &fakeacp.FSOp{Path: "calc.go", Content: impl}},
		{Say: "implemented Subtract"},
	}})
	sp := filepath.Join(t.TempDir(), "acp-script.json")
	if err := os.WriteFile(sp, script, 0o644); err != nil {
		t.Fatal(err)
	}
	cli := acp.New(acp.Config{ID: "goose", Command: os.Args[0], Env: []string{"DRYDOCK_FAKE_ACP=1", "DRYDOCK_FAKE_ACP_SCRIPT=" + sp}, Sandbox: &acp.SandboxSupport{}})
	a := openWith(t, map[string]agents.Adapter{"goose": cli})
	p, repo := a.setup("gocalc")
	def := map[string]any{"image": img, "network": "none",
		"env": map[string]string{"CGO_ENABLED": "0", "GOTOOLCHAIN": "local", "GOCACHE": "/tmp/gocache", "GOPATH": "/tmp/gopath", "GOFLAGS": "-mod=mod"}}
	b, _ := json.MarshalIndent(def, "", "  ")
	if err := os.MkdirAll(filepath.Join(repo, ".drydock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".drydock", "sandbox.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".drydock/sandbox.json")
	gitIn(t, repo, "commit", "-q", "-m", "sandbox definition")
	a.call("sandbox.set", map[string]any{"projectId": p.ID, "enabled": true}, nil)

	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Goose worker", Adapter: "goose", Role: "backend"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &r)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var d core.TaskDetail
	deadline := time.Now().Add(3 * time.Minute)
	for {
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		if len(d.Pending) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no human request: task %s, evidence %+v\nfailed %v\noutput %v", d.Task.Status, d.Evidence, a.eventsOf("agent.failed"), a.eventsOf("agent.output"))
		}
		time.Sleep(200 * time.Millisecond)
	}
	if d.Pending[0].Spec.Kind != "review" {
		t.Fatalf("the human was asked something else: %+v %s", d.Pending[0], d.Pending[0].Spec.Title)
	}

	var said []string
	for _, e := range a.eventsOf("agent.output") {
		if dd, _ := e["data"].(map[string]any); e["kind"] == "message_end" && dd != nil {
			said = append(said, fmt.Sprint(dd["text"]))
		}
	}
	got := strings.Join(said, " | ")
	if !strings.Contains(got, "permission: selected reject | permission: selected allow") || !strings.Contains(got, "term OK exit=0 out=/workspace") {
		t.Fatalf("the CLI's own command is refused, the edit allowed, the terminal runs in the container: %s", got)
	}
	if b, err := os.ReadFile(filepath.Join(d.Task.Worktree, "from-term.txt")); err != nil || strings.TrimSpace(string(b)) != "IN-THE-CONTAINER" {
		t.Fatalf("what the terminal wrote is in the worktree: %q %v", b, err)
	}
	var viaTerminal bool
	for _, e := range a.eventsOf("sandbox.command") {
		if e["via"] == "terminal" && strings.Contains(fmt.Sprint(e["command"]), "IN-THE-CONTAINER") {
			viaTerminal = true
		}
	}
	if !viaTerminal {
		t.Fatalf("the terminal's command is on the task's timeline: %v", a.eventsOf("sandbox.command"))
	}
	if len(a.eventsOf(core.EventSandboxEscape)) != 0 {
		t.Fatal("nothing ran on this machine")
	}
	checked := 0
	for _, e := range d.Evidence {
		if e.Kind != "build" && e.Kind != "test" && e.Kind != "lint" {
			continue
		}
		var data struct {
			Argv []string `json:"argv"`
		}
		_ = json.Unmarshal(e.Data, &data)
		if e.Passed == nil || !*e.Passed || len(data.Argv) < 2 || data.Argv[0] != bin || data.Argv[1] != "exec" {
			t.Fatalf("check %s did not pass in the container: %+v argv=%v", e.Kind, e, data.Argv)
		}
		checked++
	}
	if checked < 2 {
		t.Fatalf("expected the project's checks in the container, got %d: %+v", checked, d.Evidence)
	}
	label := "drydock.task=" + task.ID
	a.call("run.respond", map[string]any{"runId": runID, "requestId": d.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("the task's sandbox is removed after it completes", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		return d.Task.Status == tasks.Completed && sandboxtest.Containers(t, bin, label) == 0
	})
}

package core_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/sandbox/sandboxtest"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// sandboxProbe wraps the scripted Claude stand-in (a labelled test double). For a sandboxed worker turn it first does what a
// real Claude worker does: it connects to the gateway named in the session's MCP config with a real MCP client and runs a
// command with sandbox_run and reads sandbox_info, recording the answers. Then the scripted agent does its scripted work.
type sandboxProbe struct {
	agents.Adapter
	mu      sync.Mutex
	specs   []agents.RunSpec
	tools   []string
	runOut  string
	infoOut string
	probeEr string
}

func (a *sandboxProbe) Start(ctx context.Context, pm proc.Manager, spec agents.RunSpec) (agents.Session, error) {
	a.mu.Lock()
	a.specs = append(a.specs, spec)
	a.mu.Unlock()
	if spec.Sandboxed && spec.MCPConfigPath != "" {
		a.probe(ctx, spec.MCPConfigPath)
	}
	return a.Adapter.Start(ctx, pm, spec)
}

func (a *sandboxProbe) probe(ctx context.Context, cfgPath string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		a.probeEr = err.Error()
		return
	}
	var f mcpConfigFile
	_ = json.Unmarshal(raw, &f)
	env := f.MCPServers["drydock"].Env
	pctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	s, err := connectMCP(pctx, env["DRYDOCK_MCP_URL"], env["DRYDOCK_MCP_TOKEN"])
	if err != nil {
		a.probeEr = err.Error()
		return
	}
	defer s.Close()
	if lt, err := s.ListTools(pctx, nil); err == nil {
		for _, tl := range lt.Tools {
			a.tools = append(a.tools, tl.Name)
		}
	}
	text := func(name string, args map[string]any) string {
		r, err := s.CallTool(pctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			return "ERROR " + err.Error()
		}
		var b strings.Builder
		for _, c := range r.Content {
			if tc, ok := c.(*sdk.TextContent); ok {
				b.WriteString(tc.Text)
			}
		}
		return b.String()
	}
	a.runOut = text("sandbox_run", map[string]any{"command": "go version && pwd && test -f /.dockerenv && echo IN-THE-CONTAINER && ls"})
	a.infoOut = text("sandbox_info", map[string]any{})
}

func (a *sandboxProbe) snapshot() ([]agents.RunSpec, []string, string, string, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]agents.RunSpec(nil), a.specs...), a.tools, a.runOut, a.infoOut, a.probeEr
}

// The whole sandboxed loop against a real container engine: the definition comes from the default branch, the switch is the
// human's, the worker turn starts restricted with the sandbox tools and instructions, a command it runs through the gateway
// runs in the container, the project's checks run in the container, the reviewer is not sandboxed, and the container goes away
// when the task completes.
func TestASandboxedTaskRunsItsCommandsAndChecksInAContainer(t *testing.T) {
	bin := sandboxtest.Engine(t)
	img := sandboxtest.Go(t, bin)
	probe := &sandboxProbe{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	a := openWith(t, map[string]agents.Adapter{"claude": probe})
	p, repo := a.setup("gocalc")

	var st core.SandboxStatus
	a.call("sandbox.status", map[string]string{"projectId": p.ID}, &st)
	if st.Switch.Enabled || st.Found || st.Starter == "" || !strings.Contains(st.Starter, "golang") || st.Engine == nil {
		t.Fatalf("before the definition: %+v", st)
	}
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
	var st2 core.SandboxStatus // a fresh value: the answer leaves out empty fields
	a.call("sandbox.status", map[string]string{"projectId": p.ID}, &st2)
	if !st2.Found || st2.Config == nil || st2.Config.Image != img || st2.ConfigErr != "" || st2.Starter != "" {
		t.Fatalf("with the definition: %+v", st2)
	}
	var sw core.SandboxSwitch
	a.call("sandbox.set", map[string]any{"projectId": p.ID, "enabled": true}, &sw)
	if !sw.Enabled || !strings.HasPrefix(sw.By, "human:") {
		t.Fatalf("switch %+v", sw)
	}

	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &r)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var d core.TaskDetail
	deadline := time.Now().Add(5 * time.Minute)
	for {
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		if len(d.Pending) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no human request: task %s, evidence %+v", d.Task.Status, d.Evidence)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if d.Pending[0].Spec.Kind != "review" {
		t.Fatalf("the human was asked something else: %+v", d.Pending[0])
	}

	specs, tools, runOut, infoOut, probeErr := probe.snapshot()
	var worker, reviewer []agents.RunSpec
	for _, s := range specs {
		switch s.AgentID {
		case w.ID:
			worker = append(worker, s)
		case r.ID:
			reviewer = append(reviewer, s)
		}
	}
	if len(worker) == 0 || len(reviewer) == 0 {
		t.Fatalf("turns: %d worker, %d reviewer", len(worker), len(reviewer))
	}
	if !worker[0].Sandboxed || !strings.Contains(worker[0].SystemAppend, "Commands run in this task's sandbox") || !strings.Contains(worker[0].SystemAppend, img) {
		t.Fatalf("worker turn: sandboxed=%v\n%s", worker[0].Sandboxed, worker[0].SystemAppend)
	}
	if !strings.Contains(strings.Join(worker[0].Env, " "), "MCP_TOOL_TIMEOUT=") {
		t.Fatalf("the worker waits long enough for a sandbox command: %v", worker[0].Env)
	}
	if args := strings.Join((&claude.Adapter{}).Args(worker[0]), " "); !strings.Contains(args, "--restricted") {
		t.Fatalf("the worker CLI starts restricted: %s", args)
	}
	for _, s := range reviewer {
		if s.Sandboxed {
			t.Fatal("a reviewer turn is never sandboxed")
		}
	}
	if probeErr != "" || !strings.Contains(strings.Join(tools, " "), "sandbox_run") {
		t.Fatalf("gateway: %s tools=%v", probeErr, tools)
	}
	for _, want := range []string{"Exit code 0", "go version go1.", "/workspace", "IN-THE-CONTAINER", "calc.go"} {
		if !strings.Contains(runOut, want) {
			t.Fatalf("sandbox_run through the gateway lacks %q:\n%s", want, runOut)
		}
	}
	if !strings.Contains(infoOut, "Sandbox image: "+img) {
		t.Fatalf("sandbox_info:\n%s", infoOut)
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
	if sandboxtest.Containers(t, bin, label) == 0 {
		t.Fatal("the task's sandbox runs while the task waits for the human")
	}

	a.call("run.respond", map[string]any{"runId": runID, "requestId": d.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("the task's sandbox is removed after it completes", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		return d.Task.Status == tasks.Completed && sandboxtest.Containers(t, bin, label) == 0
	})
}

// The switch is a human decision, and a project without the definition says what to add.
func TestTheSandboxSwitchIsHumanOnly(t *testing.T) {
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	if _, err := a.c.SetSandbox(context.Background(), p.ID, true, "agt_1"); err == nil {
		t.Fatal("an agent identity switched the sandbox")
	}
	if _, err := a.c.SetSandbox(context.Background(), p.ID, true, "human:"); err == nil {
		t.Fatal("an empty human identity switched the sandbox")
	}
	if err := a.callErr("sandbox.set", map[string]any{"projectId": "prj_nope", "enabled": true}); err == nil {
		t.Fatal("an unknown project")
	}
	_, _, found, err := a.c.SandboxConfig(context.Background(), p.ID)
	if found || err == nil || !strings.Contains(err.Error(), ".drydock/sandbox.json") {
		t.Fatalf("no definition: found=%v err=%v", found, err)
	}
}

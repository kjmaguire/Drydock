package core_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/wsl"
	"github.com/kjmaguire/drydock/internal/wsl/wsltest"
)

// openWSL starts a Core whose WSL is the labelled stand-in (internal/wsl/wsltest) with the stand-in claude in its distro, and
// whose claude adapter is the product one (from the manifests), so the WSL start path is the real one.
func openWSL(t *testing.T) (*app, *wsltest.Fake) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in wsl.exe runs on a Linux development host; Windows CI drives a real distro (internal/agents/claude TestRealWSLSession)")
	}
	for _, tool := range []string{"/usr/bin/setsid", "/usr/bin/bash", "/usr/bin/getent"} {
		if _, err := os.Stat(tool); err != nil {
			t.Skipf("%s is missing on this host", tool)
		}
	}
	f := wsltest.NewFake(t, true, "Ubuntu", "docker-desktop")
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		WSL: &wsl.Host{Exe: f.Exe}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return &app{t: t, c: c}, f
}

func TestAnAgentChoosesWSLAndTheSandboxIsRefusedWithIt(t *testing.T) {
	a, _ := openWSL(t)
	p, repo := a.setup("wsl")

	var st core.WSLStatus
	a.call("wsl.distros", struct{}{}, &st)
	if !st.Available || len(st.Distros) != 1 || st.Distros[0].Name != "Ubuntu" {
		t.Fatalf("the container engine's own distro is not offered: %+v", st)
	}
	var adapters []map[string]any
	a.call("adapter.list", struct{}{}, &adapters)
	for _, ad := range adapters {
		modes := ad["capabilities"].(map[string]any)["runModes"]
		if ad["id"] == "claude" && !strings.Contains(strings.Join(toStrings(modes), ","), "wsl") {
			t.Fatalf("claude offers %v", modes)
		}
		if ad["id"] == "codex" && strings.Contains(strings.Join(toStrings(modes), ","), "wsl") {
			t.Fatal("an ACP adapter cannot run in WSL")
		}
	}

	if err := a.callErr("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "X", Adapter: "claude", Role: "backend", RunMode: "wsl"}); err == nil || !strings.Contains(err.Error(), "choose the WSL distro") {
		t.Fatalf("no distro: %v", err)
	}
	if err := a.callErr("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "X", Adapter: "claude", Role: "backend", RunMode: "wsl", WSLDistro: "Nope"}); err == nil {
		t.Fatal("an unknown distro was accepted")
	}
	if err := a.callErr("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "X", Adapter: "codex", Role: "backend", RunMode: "wsl", WSLDistro: "Ubuntu"}); err == nil {
		t.Fatal("codex cannot run in WSL")
	}
	var ag core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Linux", Adapter: "claude", Role: "backend", RunMode: "wsl", WSLDistro: "ubuntu"}, &ag)
	if ag.RunMode != "wsl" || ag.WSLDistro != "Ubuntu" {
		t.Fatalf("%+v", ag)
	}

	// a session of this agent starts inside the distro, wherever it is started from: the runtime asks the agent's run mode
	m, err := a.c.Runtime.Spawn(context.Background(), rt.Config{AgentID: ag.ID, ProjectID: p.ID, Adapter: "claude", Spec: agents.RunSpec{Role: "backend", Cwd: repo}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := m.RunTurn(context.Background(), "hello spawn", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = m.Stop(context.Background())
	for _, want := range []string{"standin turn=1", "cwd=" + repo + " ", "run=set", "mcp=yes", "mcpcmdok=yes", "spawned=yes"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("the agent's answer lacks %q: %s", want, res.Text)
		}
	}

	// the sandbox and WSL are refused together, from either side
	if err := a.callErr("sandbox.set", map[string]any{"projectId": p.ID, "enabled": true}); err == nil || !strings.Contains(err.Error(), "Linux") {
		t.Fatalf("the sandbox was switched on with a WSL agent: %v", err)
	}
	native := "native"
	a.call("agent.update", core.AgentUpdate{ID: ag.ID, RunMode: &native}, &ag)
	if ag.RunMode != "native" || ag.WSLDistro != "" {
		t.Fatalf("%+v", ag)
	}
	a.call("sandbox.set", map[string]any{"projectId": p.ID, "enabled": true}, nil)
	wslMode, distro := "wsl", "Ubuntu"
	if err := a.callErr("agent.update", core.AgentUpdate{ID: ag.ID, RunMode: &wslMode, WSLDistro: &distro}); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("an agent moved into WSL in a sandboxed project: %v", err)
	}
	if err := a.callErr("project.setRunMode", map[string]any{"projectId": p.ID, "runMode": "wsl", "wslDistro": "Ubuntu"}); err == nil {
		t.Fatal("the project's default moved into WSL with its sandbox on")
	}
}

func TestTheProjectsRunModeIsTheDefaultOfAgentsThatDoNotChoose(t *testing.T) {
	a, _ := openWSL(t)
	p, _ := a.setup("wsl-default")
	var pr core.ProjectRunMode
	a.call("project.runMode", map[string]any{"projectId": p.ID}, &pr)
	if pr.RunMode != "native" {
		t.Fatalf("%+v", pr)
	}
	a.call("project.setRunMode", map[string]any{"projectId": p.ID, "runMode": "wsl", "wslDistro": "Ubuntu"}, &pr)
	if pr.RunMode != "wsl" || pr.WSLDistro != "Ubuntu" || pr.By != "human:kyle" {
		t.Fatalf("%+v", pr)
	}
	var follows, own core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Follows", Adapter: "claude", Role: "backend"}, &follows)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Own", Adapter: "claude", Role: "backend", RunMode: "native"}, &own)
	if err := a.callErr("sandbox.set", map[string]any{"projectId": p.ID, "enabled": true}); err == nil || !strings.Contains(err.Error(), "Follows") ||
		strings.Contains(err.Error(), "Own") {
		t.Fatalf("the refusal names the agents in WSL: %v", err)
	}
}

// A template role resolves its agent's run mode the way the worker does (decision P41): a sandboxed project's test writer that
// would run in WSL (it follows the project's default, set before the sandbox was switched on and before the agent existed) is
// refused with the sandbox+WSL reason, never handed to the sandbox or started inside the distro.
func TestATemplateRoleInWSLIsRefusedInASandboxedProject(t *testing.T) {
	a, _ := openWSL(t)
	p, _ := a.setup("wsl-role")
	a.call("project.setRunMode", map[string]any{"projectId": p.ID, "runMode": "wsl", "wslDistro": "Ubuntu"}, nil)
	a.call("sandbox.set", map[string]any{"projectId": p.ID, "enabled": true}, nil) // no agent runs in WSL yet
	var w, r, qa core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "W", Adapter: "claude", Role: "backend", RunMode: "native"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "R", Adapter: "claude", Role: "reviewer", RunMode: "native"}, &r)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "QA", Adapter: "claude", Role: "qa"}, &qa) // follows the project: WSL
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Fix Subtract", Description: "Subtract returns 0; it must return a - b.", Template: "bug"}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var d core.TaskDetail
	a.waitFor("the test writer's turn to end", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		return d.Run != nil && (d.Run.Status.Terminal() || len(d.Pending) > 0)
	})
	if d.Run.Workflow != "bug" {
		t.Fatalf("the bug template did not run: %+v", d.Run)
	}
	why := d.Run.FailedReason
	for _, pr := range d.Pending {
		why += " " + pr.Spec.Title + " " + string(pr.Spec.Payload)
	}
	if !strings.Contains(why, "needs the agent to run on Windows; this turn's agent runs in WSL (Ubuntu)") {
		t.Fatalf("the test writer in WSL was not refused with the sandbox+WSL reason: status=%s why=%s", d.Run.Status, why)
	}
}

func toStrings(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

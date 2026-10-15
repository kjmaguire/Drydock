package core_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/terminal"
	"github.com/kjmaguire/drydock/internal/wsl"
	"github.com/kjmaguire/drydock/internal/wsl/wsltest"
)

// openWSL starts a Core whose WSL is the labelled stand-in (internal/wsl/wsltest) with the stand-in claude in its distro, and
// whose claude adapter is the product one (from the manifests), so the WSL start path is the real one.
func openWSL(t *testing.T) (*app, *wsltest.Fake) {
	t.Helper()
	return openWSLTTL(t, 0)
}

// openWSLTTL is openWSL with the probe cache kept for ttl (0: the default five minutes). Claude Code is "installed on Windows"
// too (the stand-in claude first on PATH), so a new project's agents run on Windows by default whatever the host has
// (decision P58); openWSLOnly leaves it out.
func openWSLTTL(t *testing.T, ttl time.Duration) (*app, *wsltest.Fake) {
	t.Helper()
	return openWSLHost(t, ttl, true)
}

// openWSLOnly is openWSL on a "Windows" without Claude Code: no claude on PATH or in the native installer's folders, so it
// is installed only inside the stand-in distro (decision P58).
func openWSLOnly(t *testing.T, distros ...string) (*app, *wsltest.Fake) {
	t.Helper()
	return openWSLHost(t, 0, false, distros...)
}

// withoutClaudeOnPath is PATH without the folders that hold a claude program (the development host's own install).
func withoutClaudeOnPath() string {
	var keep []string
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if st, err := os.Stat(filepath.Join(d, "claude")); err == nil && !st.IsDir() {
			continue
		}
		keep = append(keep, d)
	}
	return strings.Join(keep, string(os.PathListSeparator))
}

// openWSLHost opens the core with the stand-in WSL whose distros are distros (default Ubuntu and a container engine's own).
func openWSLHost(t *testing.T, ttl time.Duration, nativeClaude bool, distros ...string) (*app, *wsltest.Fake) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in wsl.exe runs on a Linux development host; Windows CI drives a real distro (internal/agents/claude TestRealWSLSession)")
	}
	for _, tool := range []string{"/usr/bin/setsid", "/usr/bin/bash", "/usr/bin/getent"} {
		if _, err := os.Stat(tool); err != nil {
			t.Skipf("%s is missing on this host", tool)
		}
	}
	if len(distros) == 0 {
		distros = []string{"Ubuntu", "docker-desktop"}
	}
	f := wsltest.NewFake(t, true, distros...)
	if nativeClaude {
		// LABELLED TEST DOUBLE: the stand-in claude (internal/wsl/wsltest) as the Windows install
		bin := t.TempDir()
		if err := os.WriteFile(filepath.Join(bin, "claude"), wsltest.Script(), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	} else {
		t.Setenv("PATH", withoutClaudeOnPath())
		t.Setenv("HOME", t.TempDir()) // nor in ~/.local/bin or ~/.claude/local
	}
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		WSL: &wsl.Host{Exe: f.Exe, TTL: ttl}})
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

// signInMarker is the run marker of the newest sign-in the stand-in wsl.exe was asked to start (its log has every argv).
func signInMarker(t *testing.T, f *wsltest.Fake) string {
	t.Helper()
	b, err := os.ReadFile(f.Log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.Contains(lines[i], `"login"`) {
			continue
		}
		var argv []string
		if err := json.Unmarshal([]byte(lines[i]), &argv); err != nil {
			t.Fatal(err)
		}
		for _, a := range argv {
			if m, ok := strings.CutPrefix(a, wsl.RunEnv+"="); ok {
				return m
			}
		}
	}
	t.Fatal("no sign-in in the stand-in wsl.exe's log")
	return ""
}

func (a *app) readTerm(id, want string) string {
	a.t.Helper()
	var all string
	var from int64
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(all, want) && time.Now().Before(deadline) {
		var c terminal.Chunk
		a.call("terminal.read", map[string]any{"id": id, "from": from, "waitMs": 500}, &c)
		all += string(c.Data)
		from = c.Next
	}
	if !strings.Contains(all, want) {
		a.t.Fatalf("waiting for %q in the terminal: %q", want, all)
	}
	return all
}

// Decision P50 through the core and the stand-in WSL: Sign in on a distro's card runs the CLI's own login inside the distro, in
// a terminal, with the login shell's PATH, the run marker and a terminal for its input; asking again while it runs returns the
// same terminal; answering it ends it with connection.sign_in_finished; closing it mid-login leaves nothing running inside
// the distro (killing wsl.exe alone does not end what it started there).
func TestSignInInsideADistroRunsInATerminalAndLeavesNothingBehind(t *testing.T) {
	a, f := openWSL(t)
	if err := os.WriteFile(filepath.Join(f.Home, ".drydock-standin-signed-out"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sub, stop := a.c.Bus.Subscribe(events.Filter{Types: []string{"connection.sign_in_*"}})
	defer stop()

	var in terminal.Info
	a.call("connection.signIn", map[string]string{"adapter": "claude", "distro": "ubuntu"}, &in)
	if in.Title != "Sign in: claude in Ubuntu" || in.Shell != f.Exe {
		t.Fatalf("%+v", in)
	}
	out := a.readTerm(in.ID, "Press Enter")
	if !strings.Contains(out, "marker=yes login-path=yes tty=yes") {
		t.Fatalf("the login does not run as an agent runs in the distro, or has no terminal: %q", out)
	}
	var again terminal.Info
	a.call("connection.signIn", map[string]string{"adapter": "claude", "distro": "Ubuntu"}, &again)
	if again.ID != in.ID {
		t.Fatalf("a second sign-in started while the first runs: %s and %s", in.ID, again.ID)
	}
	a.call("terminal.write", map[string]any{"id": in.ID, "data": "\r"}, nil)
	a.readTerm(in.ID, "Signed in to the stand-in.")
	var started, finished bool
	for deadline := time.After(20 * time.Second); !finished; {
		select {
		case e := <-sub:
			var d struct {
				Key      string `json:"key"`
				ExitCode int    `json:"exitCode"`
			}
			_ = json.Unmarshal(e.Data, &d)
			if d.Key != "claude@wsl:Ubuntu" {
				t.Fatalf("%s for %q", e.Type, d.Key)
			}
			started = started || e.Type == "connection.sign_in_started"
			if e.Type == "connection.sign_in_finished" {
				finished = d.ExitCode == 0
				if !finished {
					t.Fatalf("finished with %d", d.ExitCode)
				}
			}
		case <-deadline:
			t.Fatal("no connection.sign_in_finished")
		}
	}
	if !started {
		t.Fatal("no connection.sign_in_started")
	}
	if _, err := os.Stat(filepath.Join(f.Home, ".drydock-standin-signed-out")); !os.IsNotExist(err) {
		t.Fatal("the CLI's own login did not run")
	}

	// a new sign-in once the first ended; closed mid-login, nothing of it is left in the distro
	if err := os.WriteFile(filepath.Join(f.Home, ".drydock-standin-login-helper"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var second terminal.Info
	a.call("connection.signIn", map[string]string{"adapter": "claude", "distro": "Ubuntu"}, &second)
	if second.ID == in.ID {
		t.Fatal("the ended sign-in's terminal was returned")
	}
	a.readTerm(second.ID, "Press Enter")
	marker := signInMarker(t, f)
	host := &wsl.Host{Exe: f.Exe}
	a.waitFor("the login and its helper to be marked", func() bool {
		pids, err := host.Marked(context.Background(), "Ubuntu", marker)
		return err == nil && len(pids) >= 2
	})
	a.call("terminal.close", map[string]string{"id": second.ID}, nil)
	a.waitFor("nothing of the sign-in left in the distro", func() bool {
		pids, err := host.Marked(context.Background(), "Ubuntu", marker)
		return err == nil && len(pids) == 0
	})
}

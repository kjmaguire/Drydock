package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingSandbox is a labelled test double for the sandbox provider: it records what the gateway hands it and answers with
// fixed text. The real provider (internal/core, internal/sandbox) is tested against a real container engine.
type recordingSandbox struct {
	mu    sync.Mutex
	calls []string
}

func (s *recordingSandbox) note(f string, a ...any) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := fmt.Sprintf(f, a...)
	s.calls = append(s.calls, c)
	return c
}

func (s *recordingSandbox) Run(_ context.Context, run RunInfo, command, dir string, timeout time.Duration) (string, error) {
	return "Exit code 0\n" + s.note("run task=%s cmd=%s dir=%s timeout=%s", run.TaskID, command, dir, timeout), nil
}
func (s *recordingSandbox) Start(_ context.Context, run RunInfo, name, command, dir string) (string, error) {
	return s.note("start %s %s %s", name, command, dir), nil
}
func (s *recordingSandbox) Logs(_ context.Context, run RunInfo, name string, max int) (string, error) {
	return s.note("logs %s %d", name, max), nil
}
func (s *recordingSandbox) Stop(_ context.Context, run RunInfo, name string) (string, error) {
	return s.note("stop %s", name), nil
}
func (s *recordingSandbox) Info(_ context.Context, run RunInfo) (string, error) {
	return s.note("info %s", run.TaskID), nil
}

func TestSandboxToolsReachTheProviderWithTheTaskAndValidatedArguments(t *testing.T) {
	sb := &recordingSandbox{}
	_, cs, _, _ := wsEnv(t, Workspace{Sandbox: sb}, nil)
	res := callTool(t, cs, "sandbox_run", map[string]any{"command": "npm test", "dir": "web", "timeout_seconds": 120})
	if res.IsError || !strings.Contains(textOf(res), "run task=task-1 cmd=npm test dir=web timeout=2m0s") {
		t.Fatalf("sandbox_run: %v %q", res.IsError, textOf(res))
	}
	if res := callTool(t, cs, "sandbox_run", map[string]any{"command": "ls"}); !strings.Contains(textOf(res), "timeout=10m0s") {
		t.Fatalf("default timeout: %q", textOf(res))
	}
	for _, c := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"sandbox_start", map[string]any{"name": "web-dev", "command": "npm run dev"}, "start web-dev npm run dev"},
		{"sandbox_logs", map[string]any{"name": "web-dev"}, "logs web-dev 8000"},
		{"sandbox_stop", map[string]any{"name": "web-dev"}, "stop web-dev"},
		{"sandbox_info", map[string]any{}, "info task-1"},
	} {
		if res := callTool(t, cs, c.tool, c.args); res.IsError || !strings.Contains(textOf(res), c.want) {
			t.Errorf("%s: %v %q", c.tool, res.IsError, textOf(res))
		}
	}
	// the schema refuses what the provider must never see
	for _, bad := range []struct {
		tool string
		args map[string]any
	}{
		{"sandbox_run", map[string]any{}},
		{"sandbox_run", map[string]any{"command": "x", "timeout_seconds": 99999}},
		{"sandbox_start", map[string]any{"name": "Bad Name", "command": "x"}},
		{"sandbox_logs", map[string]any{"name": "../etc"}},
	} {
		if res := callTool(t, cs, bad.tool, bad.args); !res.IsError {
			t.Errorf("%s %v was accepted", bad.tool, bad.args)
		}
	}
	if n := len(sb.calls); n != 6 {
		t.Fatalf("the provider saw %d calls: %v", n, sb.calls)
	}
}

func TestSandboxToolsSayWhenThereIsNoSandbox(t *testing.T) {
	_, cs, _, _ := wsEnv(t, Workspace{}, nil)
	if res := callTool(t, cs, "sandbox_run", map[string]any{"command": "ls"}); !res.IsError || !strings.Contains(textOf(res), "no sandbox") {
		t.Fatalf("no provider: %v %q", res.IsError, textOf(res))
	}
	// a session without a task (a chat, a room reply) has no sandbox either
	sb := &recordingSandbox{}
	e := newEnv(t, func(o *Options) { o.Workspace = Workspace{Sandbox: sb} })
	ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-chat", ProjectID: "p", AgentID: "ag", Worktree: t.TempDir()},
		Policy: allow(map[string][]string{WorkspaceServerID: {"*"}})})
	cs2 := e.connect(ep)
	if res := callTool(t, cs2, "sandbox_run", map[string]any{"command": "ls"}); !res.IsError || !strings.Contains(textOf(res), "has none") {
		t.Fatalf("no task: %v %q", res.IsError, textOf(res))
	}
	if len(sb.calls) != 0 {
		t.Fatalf("the provider was called without a task: %v", sb.calls)
	}
}

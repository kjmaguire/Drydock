package claude

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/proc"
)

// A worker outside a sandbox that was given the gateway's permission tool starts with --permission-prompt-tool naming it, so
// Claude Code asks Drydock instead of refusing a tool its rules do not cover (docs/security.md s4). Read-only and sandboxed
// sessions, and a session without the gateway, never get the flag.
func TestWorkerAsksDrydockThroughThePermissionPromptTool(t *testing.T) {
	a := &Adapter{UserSettingsOnly: true}
	worker := agents.RunSpec{PermissionMode: "acceptEdits", MCPConfigPath: "/tmp/mcp.json", PermissionPrompts: true, PermissionPromptTool: PermissionPromptTool}
	args := a.argsWith(worker, "")
	i := slices.Index(args, "--permission-prompt-tool")
	if i < 0 || i+1 >= len(args) || args[i+1] != "mcp__drydock__permission_prompt" {
		t.Fatalf("worker args: %v", args)
	}
	if slices.Index(args[i+1:], "--permission-prompt-tool") >= 0 {
		t.Fatal("the flag is passed twice")
	}
	for name, mod := range map[string]func(*agents.RunSpec){
		"read-only":  func(s *agents.RunSpec) { s.ReadOnly, s.PermissionMode = true, "plan" },
		"sandboxed":  func(s *agents.RunSpec) { s.Sandboxed = true },
		"no gateway": func(s *agents.RunSpec) { s.MCPConfigPath = "" },
		"not given":  func(s *agents.RunSpec) { s.PermissionPromptTool = "" },
	} {
		s := worker
		mod(&s)
		if got := strings.Join(a.argsWith(s, ""), " "); strings.Contains(got, "--permission-prompt-tool") {
			t.Errorf("%s session must not ask Drydock: %s", name, got)
		}
	}

	// the started process really receives it (the stand-in records its argv; on Windows this is where argument passing breaks)
	out := filepath.Join(t.TempDir(), "seen")
	cfg := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(cfg, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := worker
	spec.AgentID, spec.Cwd, spec.MCPConfigPath, spec.Env = "a1", t.TempDir(), cfg, standinSpecEnv(out)
	s, err := (&Adapter{Exe: os.Args[0]}).Start(context.Background(), proc.New(), spec)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Wait():
	case <-time.After(10 * time.Second):
		t.Fatal("the stand-in did not exit")
	}
	b, _ := os.ReadFile(out + ".args")
	if !strings.Contains(string(b), "--permission-prompt-tool\nmcp__drydock__permission_prompt\n") {
		t.Fatalf("argv seen by the process:\n%s", b)
	}
}

package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/proc"
)

// A sandboxed worker starts in restricted mode (no command tools on this machine; its commands go to its container through the
// gateway), without --setting-sources, and with the project's CLAUDE.md appended by Drydock: restricted mode does not put it in
// the model's context (contract CLAUDE-RESTRICTED-02; measured on 2.1.296 with an instruction the model only follows when the
// file is appended), and a model that has to read it with a tool may not.
func TestASandboxedSessionStartsRestrictedAndGetsTheProjectMemory(t *testing.T) {
	a := &Adapter{UserSettingsOnly: true}
	args := strings.Join(a.argsWith(agents.RunSpec{Sandboxed: true, PermissionMode: "acceptEdits", MCPConfigPath: "/tmp/mcp.json"}, ""), " ")
	if !strings.Contains(args, "--restricted") || strings.Contains(args, "--setting-sources") || !strings.Contains(args, "--strict-mcp-config") {
		t.Fatalf("sandboxed args: %s", args)
	}
	if open := strings.Join(a.argsWith(agents.RunSpec{Sandboxed: true, PermissionMode: "bypassPermissions"}, ""), " "); !strings.Contains(open, "--permission-mode acceptEdits") || strings.Contains(open, "bypassPermissions") {
		t.Fatalf("restricted mode refuses bypassPermissions, so a sandboxed session runs in acceptEdits: %s", open)
	}
	plain := strings.Join(a.argsWith(agents.RunSpec{PermissionMode: "acceptEdits"}, ""), " ")
	if strings.Contains(plain, "--restricted") || !strings.Contains(plain, "--setting-sources user") {
		t.Fatalf("an ordinary session is unchanged: %s", plain)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("PROJECT-MEMORY-MARKER\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := os.Args[0] // the stand-in (standin_test.go)
	read := func(sandboxed bool) string {
		out := filepath.Join(t.TempDir(), "seen")
		s, err := (&Adapter{Exe: script, UserSettingsOnly: !sandboxed}).Start(context.Background(), proc.New(),
			agents.RunSpec{AgentID: "a1", Cwd: dir, SystemAppend: "Worker instructions.", Sandboxed: sandboxed, Env: standinSpecEnv(out)})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-s.Wait():
		case <-time.After(10 * time.Second):
			t.Fatal("the script did not exit")
		}
		b, _ := os.ReadFile(out + ".content")
		return string(b)
	}
	if got := read(false); !strings.Contains(got, "PROJECT-MEMORY-MARKER") {
		t.Fatalf("an ordinary session gets the project memory appended: %q", got)
	}
	if got := read(true); !strings.Contains(got, "PROJECT-MEMORY-MARKER") || !strings.Contains(got, "Worker instructions.") {
		t.Fatalf("a sandboxed session gets the project memory appended: %q", got)
	}
}

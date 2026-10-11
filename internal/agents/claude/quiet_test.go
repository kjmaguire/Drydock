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

// Every kind of session Drydock starts carries no skills or slash commands of the user's Claude Code and denies the tools no
// Drydock session uses, in one deny list with the read-only rule (a read-only session) and the plugin rule (a session that
// loads plugins: every one but a sandboxed one).
func TestEverySessionGetsNoSkillsAndTheUnattendedDenyList(t *testing.T) {
	for _, c := range []struct {
		name              string
		spec              agents.RunSpec
		readOnly, plugins bool
	}{
		{"worker", agents.RunSpec{Model: "sonnet", PermissionMode: "acceptEdits", AllowedTools: []string{"Bash(go test ./...)"}}, false, true},
		{"read-only", agents.RunSpec{Model: "haiku", PermissionMode: "plan", ReadOnly: true}, true, true},
		{"sandboxed", agents.RunSpec{Sandboxed: true}, false, false},
		{"resumed chat", agents.RunSpec{PermissionMode: "plan", ResumeID: "s1"}, false, true},
		{"no options", agents.RunSpec{}, false, true},
	} {
		args := (&Adapter{UserSettingsOnly: true}).Args(c.spec)
		count := func(flag string) int {
			n := 0
			for _, a := range args {
				if a == flag {
					n++
				}
			}
			return n
		}
		if count("--disable-slash-commands") != 1 {
			t.Errorf("%s: the user's skills and slash commands are not switched off: %v", c.name, args)
		}
		i := slices.Index(args, "--disallowedTools")
		if i < 0 || count("--disallowedTools") != 1 {
			t.Errorf("%s: want exactly one deny list: %v", c.name, args)
			continue
		}
		denied := strings.Split(args[i+1], ",")
		for _, name := range UnattendedDenied {
			if !slices.Contains(denied, name) {
				t.Errorf("%s: %s is not denied: %v", c.name, name, denied)
			}
		}
		if slices.Contains(denied, PluginToolsDenied) != c.plugins {
			t.Errorf("%s: plugin rule %v, want %v: %v", c.name, !c.plugins, c.plugins, denied)
		}
		for _, name := range ReadOnlyDenied {
			if slices.Contains(denied, name) != c.readOnly {
				t.Errorf("%s: %s denied %v, want %v", c.name, name, !c.readOnly, c.readOnly)
			}
		}
	}
}

// The quiet values replace what the human's environment says for the same names (CLAUDE_* passes through to agents), with
// names compared as Windows compares them, and nothing else is touched.
func TestQuietEnvReplacesInheritedValues(t *testing.T) {
	parent := []string{"PATH=/bin", "CLAUDE_AUTO_BACKGROUND_TASKS=1", "claude_code_disable_cron=0", "CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=true",
		"CLAUDE_CODE_OAUTH_TOKEN=t", "CLAUDE_CODE_DISABLE_AUTO_MEMORY_EXTRA=x"}
	got := map[string][]string{}
	for _, kv := range Quiet(parent) {
		k, v, _ := strings.Cut(kv, "=")
		got[strings.ToUpper(k)] = append(got[strings.ToUpper(k)], v)
	}
	for _, kv := range QuietEnv {
		k, v, _ := strings.Cut(kv, "=")
		if len(got[k]) != 1 || got[k][0] != v {
			t.Errorf("%s = %v, want only %q", k, got[k], v)
		}
	}
	for _, keep := range []string{"PATH", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_DISABLE_AUTO_MEMORY_EXTRA"} {
		if len(got[keep]) != 1 {
			t.Errorf("%s was changed: %v", keep, got[keep])
		}
	}
	if len(parent) != 6 || parent[1] != "CLAUDE_AUTO_BACKGROUND_TASKS=1" {
		t.Fatalf("the parent slice was modified: %v", parent)
	}
}

// The process really starts with the quiet values even when the app's own environment says otherwise, and a run's own
// variables still reach it.
func TestStartRunsClaudeWithTheQuietEnvironment(t *testing.T) {
	t.Setenv("CLAUDE_AUTO_BACKGROUND_TASKS", "1")
	t.Setenv("CLAUDE_CODE_DISABLE_CRON", "0")
	dir := t.TempDir()
	out := filepath.Join(dir, "seen")
	script := os.Args[0] // the stand-in (standin_test.go)
	spec := agents.RunSpec{AgentID: "a1", Cwd: dir, Env: append(standinSpecEnv(out), "MCP_TOOL_TIMEOUT=60000")}
	s, err := (&Adapter{Exe: script, ScrubEnv: true, UserSettingsOnly: true}).Start(context.Background(), proc.New(), spec)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Wait():
	case <-time.After(10 * time.Second):
		t.Fatal("the script did not exit")
	}
	raw, err := os.ReadFile(out + ".env")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			seen[k] = v
		}
	}
	for _, kv := range append(slices.Clone(QuietEnv), "MCP_TOOL_TIMEOUT=60000") {
		k, v, _ := strings.Cut(kv, "=")
		if seen[k] != v {
			t.Errorf("the process saw %s=%q, want %q", k, seen[k], v)
		}
	}
	args, _ := os.ReadFile(out + ".args")
	if !slices.Contains(strings.Split(string(args), "\n"), "--disable-slash-commands") {
		t.Errorf("argv: %s", args)
	}
}

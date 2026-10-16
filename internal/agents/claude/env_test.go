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

func agentsRunSpec() agents.RunSpec { return agents.RunSpec{AgentID: "a1", Cwd: "/x"} }

func TestChildEnvIsScrubbedAndTakesNamesToPassFromTheHuman(t *testing.T) {
	parent := []string{"PATH=/bin", "HOME=/h", "GITHUB_TOKEN=ghp_x", "AWS_PROFILE=work", "CLAUDE_CODE_OAUTH_TOKEN=t", "ANTHROPIC_BASE_URL=u"}
	has := func(env []string, name string) bool {
		for _, kv := range env {
			if strings.HasPrefix(kv, name+"=") {
				return true
			}
		}
		return false
	}
	open := (&Adapter{}).childEnv(parent)
	if len(open) != len(parent) {
		t.Fatalf("without ScrubEnv the environment is inherited whole: %v", open)
	}
	// started from inside a Claude Code session: the agent is never made part of it, scrubbed or not
	inner := append(append([]string(nil), parent...), "CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=outer", "CLAUDE_CODE_ENTRYPOINT=cli",
		"CLAUDE_CODE_REMOTE_SESSION_ID=outer-remote", "CLAUDE_CODE_MESSAGING_SOCKET=/tmp/outer.sock", "CLAUDE_CODE_MESSAGING_TOKEN=outer-token")
	for _, a := range []*Adapter{{}, {ScrubEnv: true}} {
		env := a.childEnv(inner)
		for _, drop := range []string{"CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_REMOTE_SESSION_ID", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN"} {
			if has(env, drop) {
				t.Fatalf("%s reached the agent (scrub %v)", drop, a.ScrubEnv)
			}
		}
		if !has(env, "CLAUDE_CODE_OAUTH_TOKEN") {
			t.Fatalf("the sign-in was removed with them (scrub %v)", a.ScrubEnv)
		}
	}
	a := &Adapter{ScrubEnv: true}
	env := a.childEnv(parent)
	for _, keep := range []string{"PATH", "HOME", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL"} {
		if !has(env, keep) {
			t.Errorf("%s was removed: claude needs it to run and sign in", keep)
		}
	}
	for _, drop := range []string{"GITHUB_TOKEN", "AWS_PROFILE"} {
		if has(env, drop) {
			t.Errorf("%s reached the agent", drop)
		}
	}
	// a Bedrock login the allowlist cannot know about: the human names the variable and it is read at every start
	pass := []string{}
	a.PassEnv = func() []string { return pass }
	if has(a.childEnv(parent), "AWS_PROFILE") {
		t.Fatal("passed before the human asked")
	}
	pass = []string{"aws_profile"}
	env = a.childEnv(parent)
	if !has(env, "AWS_PROFILE") || has(env, "GITHUB_TOKEN") {
		t.Fatalf("pass-through: %v", env)
	}
}

// The CLI reaches the network through the proxy address it is given, not with the password in that address (audit 2026-10-16,
// L8), unless the person passes the variable on by name.
func TestChildEnvTakesTheCredentialsOutOfAProxyAddress(t *testing.T) {
	parent := []string{"PATH=/bin", "HTTPS_PROXY=http://alice:s3cret@proxy.corp:8080", "NO_PROXY=localhost"}
	value := func(env []string, name string) string {
		for _, kv := range env {
			if k, v, ok := strings.Cut(kv, "="); ok && k == name {
				return v
			}
		}
		return "<missing>"
	}
	a := &Adapter{ScrubEnv: true}
	if got := value(a.childEnv(parent), "HTTPS_PROXY"); got != "http://proxy.corp:8080" {
		t.Fatalf("HTTPS_PROXY = %q", got)
	}
	a.PassEnv = func() []string { return []string{"https_proxy"} }
	if got := value(a.childEnv(parent), "HTTPS_PROXY"); got != "http://alice:s3cret@proxy.corp:8080" {
		t.Fatalf("a variable the person passes on by name stays as it is: %q", got)
	}
}

func TestUserSettingsOnlyAddsTheSettingSourcesFlag(t *testing.T) {
	has := func(args []string) bool {
		for i, a := range args {
			if a == "--setting-sources" && i+1 < len(args) && args[i+1] == "user" {
				return true
			}
		}
		return false
	}
	if has((&Adapter{}).Args(agentsRunSpec())) {
		t.Fatal("the flag must be opt-in on the adapter")
	}
	if !has((&Adapter{UserSettingsOnly: true}).Args(agentsRunSpec())) {
		t.Fatal("a repository's own hooks would run in an unattended agent")
	}
}

// The instructions reach the CLI through a file, never as one long multi-line argument (claude.cmd on Windows runs through
// cmd.exe, which ends the command line at the first newline), the flags after them still arrive, and the file is gone once
// the process has exited.
func TestStartPassesTheInstructionsInAFileAndRemovesIt(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "seen")
	script := os.Args[0] // the stand-in (standin_test.go)
	text := "Line one of the instructions.\nLine two & \"quoted\" | piped\n\nThe end."
	spec := agents.RunSpec{AgentID: "a1", Cwd: dir, SystemAppend: text, AllowedTools: []string{"Bash(npm test)", "Edit"}, Env: standinSpecEnv(out)}
	s, err := (&Adapter{Exe: script}).Start(context.Background(), proc.New(), spec)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Wait():
	case <-time.After(10 * time.Second):
		t.Fatal("the stand-in did not exit")
	}
	got, err := os.ReadFile(out + ".content")
	if err != nil || string(got) != text {
		t.Fatalf("the CLI read %q (%v), want the instructions", got, err)
	}
	if allowed, _ := os.ReadFile(out + ".allowed"); string(allowed) != "Bash(npm test),Edit" {
		t.Fatalf("the flags after the instructions: --allowedTools %q", allowed)
	}
	if _, err := os.Stat(out + ".inline"); err == nil {
		t.Fatal("the instructions were also passed inline")
	}
	path, _ := os.ReadFile(out + ".path")
	if _, err := os.Stat(string(path)); !os.IsNotExist(err) {
		t.Fatalf("the instructions file %s is still there after the exit (%v)", path, err)
	}
	// no instructions: no file and no flag
	if args := (&Adapter{}).argsWith(agents.RunSpec{}, ""); strings.Contains(strings.Join(args, " "), "system-prompt") {
		t.Fatalf("args without instructions: %v", args)
	}
	if f, err := writePromptFile("  \n"); err != nil || f != "" {
		t.Fatalf("blank instructions made a file: %q %v", f, err)
	}
}

// A read-only session runs in Claude's default mode with every writing tool denied, never in plan mode (where Claude Code
// answers a haiku session with Sonnet), and keeps its model.
func TestAReadOnlySessionIsDefaultModeWithWritingToolsDenied(t *testing.T) {
	args := (&Adapter{}).Args(agents.RunSpec{Model: "haiku", PermissionMode: "plan", ReadOnly: true, AllowedTools: []string{"Read", "Grep", "Glob"}})
	line := strings.Join(args, " ")
	for _, want := range []string{"--model haiku", "--permission-mode default", "--disallowedTools Edit,Write,MultiEdit,NotebookEdit,Bash", "--allowedTools Read,Grep,Glob"} {
		if !strings.Contains(line, want) {
			t.Fatalf("missing %q in %v", want, args)
		}
	}
	if strings.Contains(line, "plan") {
		t.Fatalf("a read-only session must not use plan mode: %v", args)
	}
	// a human's own choice of plan mode (a chat) is kept
	if line := strings.Join((&Adapter{}).Args(agents.RunSpec{PermissionMode: "plan"}), " "); !strings.Contains(line, "--permission-mode plan") || strings.Contains(line, "Edit,Write") || !strings.Contains(line, "--disallowedTools "+PluginToolsDenied) {
		t.Fatalf("plain plan mode: %v", line)
	}
}

package claude

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as the CLI")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "seen")
	script := filepath.Join(dir, "claude")
	body := `#!/bin/sh
prev=""
for a in "$@"; do
  if [ "$prev" = "--append-system-prompt-file" ]; then cat "$a" > "$OUT.content"; printf '%s' "$a" > "$OUT.path"; fi
  if [ "$prev" = "--allowedTools" ]; then printf '%s' "$a" > "$OUT.allowed"; fi
  if [ "$a" = "--append-system-prompt" ]; then echo inline > "$OUT.inline"; fi
  prev="$a"
done
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	text := "Line one of the instructions.\nLine two & \"quoted\" | piped\n\nThe end."
	spec := agents.RunSpec{AgentID: "a1", Cwd: dir, SystemAppend: text, AllowedTools: []string{"Bash(npm test)", "Edit"}, Env: []string{"OUT=" + out}}
	s, err := (&Adapter{Exe: script}).Start(context.Background(), proc.New(), spec)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Wait():
	case <-time.After(10 * time.Second):
		t.Fatal("the script did not exit")
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

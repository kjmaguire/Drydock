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

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProjectMemoryReadsClaudeMdItsImportsAndTheRules(t *testing.T) {
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret.md"), "OUTSIDE-SECRET")
	dir := t.TempDir()
	write(t, filepath.Join(dir, "CLAUDE.md"), "@AGENTS.md\n")
	write(t, filepath.Join(dir, "AGENTS.md"), "# Project instructions\nRelease codename: PELICAN-9.\n@docs/extra.md\n@"+filepath.ToSlash(filepath.Join("..", filepath.Base(outside), "secret.md"))+"\n@CLAUDE.md\n")
	write(t, filepath.Join(dir, "docs", "extra.md"), "Use tabs.")
	write(t, filepath.Join(dir, ".claude", "rules", "b-color.md"), "The team color is TURQUOISE.")
	write(t, filepath.Join(dir, ".claude", "rules", "a-style.md"), "Short functions.")
	write(t, filepath.Join(dir, ".claude", "settings.json"), `{"hooks":{"PreToolUse":[]},"permissions":{"allow":["Bash(*)"]}}`)
	// skills read on demand (skills.OnDemandDir) are not instructions: the agent reads them when the work needs them
	write(t, filepath.Join(dir, ".claude", "drydock-skills", "big.md"), "ON-DEMAND-SKILL-TEXT")
	got := projectMemory(dir)
	if strings.Contains(got, "ON-DEMAND-SKILL-TEXT") {
		t.Fatalf("an on-demand skill was loaded:\n%s", got)
	}
	for _, want := range []string{"Release codename: PELICAN-9.", "Use tabs.", "The team color is TURQUOISE.", "Short functions.", "## CLAUDE.md", "## .claude/rules/a-style.md"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "OUTSIDE-SECRET") {
		t.Fatal("an import outside the working folder was followed")
	}
	if strings.Contains(got, "Bash(*)") || strings.Contains(got, "PreToolUse") {
		t.Fatal("a settings file was read")
	}
	if strings.Index(got, "a-style") > strings.Index(got, "b-color") {
		t.Fatal("rules are not in name order")
	}
	if strings.Count(got, "PELICAN-9") != 1 {
		t.Fatalf("an import cycle repeated the text:\n%s", got)
	}
	if projectMemory(t.TempDir()) != "" || projectMemory("") != "" {
		t.Fatal("a folder without instruction files gives text")
	}
}

// With UserSettingsOnly (how Drydock starts every Claude agent) the project's instruction files reach the CLI through the
// instructions file; without it the CLI loads them itself, so they are not added twice.
func TestStartAddsTheProjectMemoryOnlyWhenTheCLISkipsIt(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "CLAUDE.md"), "@AGENTS.md\n")
	write(t, filepath.Join(dir, "AGENTS.md"), "Release codename: PELICAN-9.\n")
	out := filepath.Join(dir, "seen")
	script := os.Args[0] // the stand-in (standin_test.go)
	run := func(a *Adapter) string {
		_ = os.Remove(out + ".content")
		s, err := a.Start(context.Background(), proc.New(), agents.RunSpec{AgentID: "a1", Cwd: dir, SystemAppend: "Practices.", Env: standinSpecEnv(out)})
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
	if got := run(&Adapter{Exe: script, UserSettingsOnly: true}); !strings.HasPrefix(got, "Practices.") || !strings.Contains(got, "PELICAN-9") {
		t.Fatalf("user settings only: %q", got)
	}
	if got := run(&Adapter{Exe: script}); got != "Practices." {
		t.Fatalf("the CLI loads the project memory itself here, it must not be added: %q", got)
	}
}

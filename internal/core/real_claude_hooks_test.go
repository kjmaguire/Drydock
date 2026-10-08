package core_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/core"
)

// A repository can carry hooks in .claude/settings.json, and `claude -p` runs them without asking (measured with claude 2.1.295).
// An unattended agent must not run commands that someone else wrote into a repository, so Drydock starts Claude Code with
// --setting-sources user. This test uses the real CLI: the control run shows the hook really runs when nothing prevents it.
func TestRealClaudeDoesNotRunARepositorysOwnHooks(t *testing.T) {
	if os.Getenv("DRYDOCK_NO_REAL_AGENTS") == "1" {
		t.Skip("DRYDOCK_NO_REAL_AGENTS=1")
	}
	t.Setenv("DRYDOCK_FAKE_AGENT", "")
	det, err := claude.New().Detect(context.Background())
	if err != nil || det.Status != agents.StatusReady {
		t.Skipf("real claude not usable here: status=%s problems=%v err=%v", det.Status, det.Problems, err)
	}
	dir := t.TempDir()
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "data"), User: "kyle", LeaseTTL: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	p, repo := a.setup("hooked")
	marker := filepath.Join(dir, "hook-ran.txt")
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	hook := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"echo ran >> ` + filepath.ToSlash(marker) + `"}]}]}}`
	if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), []byte(hook), 0o644); err != nil {
		t.Fatal(err)
	}

	// control: the plain CLI, in the same repository, runs the hook
	cmd := exec.Command("claude", "-p", "Say OK.", "--model", "haiku", "--effort", "low", "--no-session-persistence")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("the control run failed (%v): %s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Skip("the control run did not run the hook, so this claude does not behave as measured; nothing to prove here")
	}
	_ = os.Remove(marker)

	// through Drydock: the same repository, the same prompt, no hook
	var ag core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Haiku", Adapter: "claude", Role: "backend", Model: "haiku", Effort: "low"}, &ag)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	m, err := c.Runtime.Spawn(ctx, rt.Config{AgentID: ag.ID, ProjectID: p.ID, Adapter: "claude", Provider: "anthropic", Tier: "premium",
		Spec: agents.RunSpec{AgentID: ag.ID, Role: "backend", Cwd: repo, Model: "haiku", Effort: "low", PermissionMode: "acceptEdits", ExtraArgs: []string{"--no-session-persistence"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop(context.Background())
	if _, err := m.RunTurn(ctx, "Say OK.", nil, nil); err != nil {
		t.Fatalf("turn: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the repository's own hook ran inside a Drydock agent run")
	}
}

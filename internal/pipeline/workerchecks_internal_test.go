package pipeline

import (
	"context"
	"strings"
	"testing"

	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The worker is told the commands Verify will run, found with the same detection (Deps.Checks when set), limited to the ones
// it may run; the allow rules are the fixed list whatever was detected.
func TestWorkerToolsFollowTheSameDetectionAsVerify(t *testing.T) {
	d := Deps{
		WorkerChecks: func(context.Context, tasks.Task, rt.Config) bool { return true },
		Checks: func(string) []checks.Spec {
			return []checks.Spec{{Name: "test", Argv: []string{"task", "test"}}, {Name: "custom", Argv: []string{"make", "check"}}}
		},
	}
	var cfg rt.Config
	cfg.Spec.AllowedTools = []string{"Edit"}
	cfg.Spec.SystemAppend = "caller text"
	d.addWorkerTools(context.Background(), tasks.Task{Worktree: t.TempDir()}, &cfg)
	if strings.Join(cfg.Spec.AllowedTools, "|") != strings.Join(append([]string{"Edit"}, checks.WorkerRules()...), "|") {
		t.Fatalf("allowed %v", cfg.Spec.AllowedTools)
	}
	if !strings.HasPrefix(cfg.Spec.SystemAppend, "caller text\n\n") || !strings.Contains(cfg.Spec.SystemAppend, "- task test\n") || strings.Contains(cfg.Spec.SystemAppend, "make check") {
		t.Fatalf("system prompt %q", cfg.Spec.SystemAppend)
	}
}

func TestWorkerToolsNeedTheSwitchAndAWorktree(t *testing.T) {
	for name, d := range map[string]Deps{
		"no hook": {},
		"off":     {WorkerChecks: func(context.Context, tasks.Task, rt.Config) bool { return false }},
	} {
		var cfg rt.Config
		d.addWorkerTools(context.Background(), tasks.Task{Worktree: t.TempDir()}, &cfg)
		if len(cfg.Spec.AllowedTools) != 0 || cfg.Spec.SystemAppend != "" {
			t.Errorf("%s: %+v", name, cfg.Spec)
		}
	}
	d := Deps{WorkerChecks: func(context.Context, tasks.Task, rt.Config) bool { return true }}
	var cfg rt.Config
	d.addWorkerTools(context.Background(), tasks.Task{}, &cfg)
	if len(cfg.Spec.AllowedTools) != 0 {
		t.Fatalf("no worktree: %v", cfg.Spec.AllowedTools)
	}
}

func TestWorkerChecksTextWithNothingDetected(t *testing.T) {
	s := WorkerChecksText(nil)
	if !strings.Contains(s, "No checks are set up") || !strings.Contains(s, "go test ./...") {
		t.Fatal(s)
	}
}

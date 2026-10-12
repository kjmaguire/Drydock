package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// fakeSandbox is a labelled test double for the sandbox hook: it says on or off, describes itself, rewrites checks in a way
// a test can see, and fails when told to. The real hook is tested against a real container engine in internal/core.
type fakeSandbox struct {
	on      bool
	prepErr error
	checked []string
}

func (f *fakeSandbox) On(context.Context, string) bool { return f.on }
func (f *fakeSandbox) Prepare(context.Context, tasks.Task) (string, error) {
	return "Sandbox image: test-image.", f.prepErr
}
func (f *fakeSandbox) Check(_ context.Context, _ tasks.Task, s checks.Spec) (checks.Spec, error) {
	f.checked = append(f.checked, s.Name)
	s.Argv = append([]string{"engine", "exec"}, s.Argv...)
	return s, nil
}

func TestASandboxedWorkerTurnHasNoShellAndGetsTheSandbox(t *testing.T) {
	ctx := context.Background()
	task := tasks.Task{ID: "task_1", ProjectID: "prj_1", Worktree: "/wt/task_1"}
	sb := &fakeSandbox{on: true}
	d := Deps{Sandbox: sb}

	cfg := rt.Config{Adapter: "claude"}
	q, err := d.prepareWorker(ctx, workflow.NodeInput{}, task, &cfg)
	if q != nil || err != nil {
		t.Fatalf("question %+v err %v", q, err)
	}
	if !cfg.Spec.Sandboxed || cfg.Spec.PermissionPrompts || !strings.Contains(cfg.Spec.SystemAppend, "sandbox_run") || !strings.Contains(cfg.Spec.SystemAppend, "Sandbox image: test-image.") {
		t.Fatalf("spec %+v", cfg.Spec)
	}
	env := strings.Join(cfg.Spec.Env, " ")
	for _, want := range []string{"MCP_TOOL_TIMEOUT=" + SandboxToolTimeoutMS, "CLAUDE_AUTO_BACKGROUND_TASKS=false", "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1"} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %s: %s", want, env)
		}
	}
	if len(cfg.Spec.AllowedTools) != 0 {
		t.Fatalf("no command rules for a sandboxed worker: %v", cfg.Spec.AllowedTools)
	}

	// another CLI cannot run sandboxed yet: the human is asked rather than the commands going to this machine
	other := rt.Config{Adapter: "gemini"}
	q, err = d.prepareWorker(ctx, workflow.NodeInput{}, task, &other)
	if err != nil || q == nil || q.Request == nil || !strings.Contains(q.Request.Title, "Claude Code") || other.Spec.Sandboxed {
		t.Fatalf("other CLI: %+v %v", q, err)
	}

	// a Claude Code agent running inside WSL cannot run sandboxed (decision P41): the human is asked, nothing runs
	inWSL := rt.Config{Adapter: "claude", Spec: agents.RunSpec{RunMode: agents.RunModeWSL, WSLDistro: "Ubuntu"}}
	q, err = d.prepareWorker(ctx, workflow.NodeInput{}, task, &inWSL)
	if err != nil || q == nil || q.Request == nil || !strings.Contains(q.Request.Title, "runs in WSL (Ubuntu)") || inWSL.Spec.Sandboxed {
		t.Fatalf("WSL agent: %+v %v", q, err)
	}

	// a sandbox that cannot start becomes a question the human answers to try again
	sb.prepErr = errors.New("the container engine is not running")
	cfg = rt.Config{Adapter: "claude"}
	q, err = d.prepareWorker(ctx, workflow.NodeInput{}, task, &cfg)
	if err != nil || q == nil || q.Request == nil || !q.Request.Retry || !strings.Contains(q.Request.Title, "engine is not running") || cfg.Spec.Sandboxed {
		t.Fatalf("broken sandbox: %+v %v", q, err)
	}
	// and a human who rejects it stops the run
	answer, _ := json.Marshal(map[string]any{"decision": "rejected", "by": "human:kyle"})
	_, err = d.prepareWorker(ctx, workflow.NodeInput{Inbox: []workflow.Message{{Value: answer}}}, task, &cfg)
	if err == nil || !strings.Contains(err.Error(), "stopped the run") {
		t.Fatalf("rejected: %v", err)
	}

	// with the sandbox off nothing changes
	sb.on, sb.prepErr = false, nil
	cfg = rt.Config{Adapter: "claude"}
	if q, err := d.prepareWorker(ctx, workflow.NodeInput{}, task, &cfg); q != nil || err != nil || cfg.Spec.Sandboxed {
		t.Fatalf("off: %+v %v %+v", q, err, cfg.Spec)
	}
	// ... except that the worker, which now runs on this machine, asks Drydock about what its rules do not cover
	if !cfg.Spec.PermissionPrompts {
		t.Fatal("a worker outside a sandbox must send its permission prompts to Drydock")
	}
}

func TestSandboxedChecksRunInTheSandboxExceptTheInProcessOnes(t *testing.T) {
	ctx := context.Background()
	task := tasks.Task{ID: "task_1", ProjectID: "prj_1", Worktree: "/wt/task_1"}
	sb := &fakeSandbox{on: true}
	d := Deps{Sandbox: sb}
	inProc := func(context.Context, proc.Manager, checks.Spec) (checks.Result, error) { return checks.Result{}, nil }
	specs := []checks.Spec{{Name: "build", Argv: []string{"go", "build", "./..."}}, {Name: "secrets", InProcess: inProc}, {Name: "test", Argv: []string{"go", "test", "./..."}}}
	out, err := d.sandboxChecks(ctx, task, specs)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Argv[0] != "engine" || out[2].Argv[0] != "engine" || out[1].InProcess == nil || len(sb.checked) != 2 {
		t.Fatalf("rewritten %+v, hook saw %v", out, sb.checked)
	}
	sb.on = false
	if out, _ := d.sandboxChecks(ctx, task, specs); out[0].Argv[0] != "go" {
		t.Fatal("with the sandbox off the checks are unchanged")
	}
	sb.on, sb.prepErr = true, errors.New("no engine")
	if _, err := d.sandboxChecks(ctx, task, specs); err == nil {
		t.Fatal("checks do not run on this machine when the sandbox cannot start")
	}
}

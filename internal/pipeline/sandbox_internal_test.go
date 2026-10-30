package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
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
func (f *fakeSandbox) Terminals(t tasks.Task, _ string) agents.TerminalHost { return fakeTerminals{task: t.ID} }

// fakeTerminals is the labelled test double of the container's terminals: it names the task it was made for.
type fakeTerminals struct{ task string }

func (fakeTerminals) Run(context.Context, agents.TerminalRequest, io.Writer) (int, error) { return 0, nil }

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

	// a CLI whose manifest names no way to make it ask first cannot run sandboxed: the human is asked rather than the commands
	// going to this machine
	d.Runtime = &rt.Runtime{Adapters: map[string]agents.Adapter{
		"codex":  acp.New(acp.Config{ID: "codex", Command: "codex-acp"}),
		"gemini": acp.New(acp.Config{ID: "gemini", Command: "gemini", Sandbox: &acp.SandboxSupport{Mode: "default"}}),
		"goose":  acp.New(acp.Config{ID: "goose", Command: "goose", Sandbox: &acp.SandboxSupport{}}),
	}}
	for _, name := range []string{"codex", "unknown-cli"} {
		other := rt.Config{Adapter: name}
		q, err = d.prepareWorker(ctx, workflow.NodeInput{}, task, &other)
		if err != nil || q == nil || q.Request == nil || !strings.Contains(q.Request.Title, "cannot be kept from running commands on this machine") || other.Spec.Sandboxed {
			t.Fatalf("%s: %+v %v", name, q, err)
		}
	}
	// one that can (decision P131): its terminals go to the task's container, it runs in the mode in which it asks first, and
	// it is told how to run commands there
	gem := rt.Config{Adapter: "gemini"}
	if q, err := d.prepareWorker(ctx, workflow.NodeInput{}, task, &gem); q != nil || err != nil {
		t.Fatalf("gemini: %+v %v", q, err)
	}
	if !gem.Spec.Sandboxed || gem.Spec.PermissionPrompts || gem.Spec.PermissionMode != "default" || gem.Spec.Terminals != (fakeTerminals{task: "task_1"}) ||
		!strings.Contains(gem.Spec.SystemAppend, "in a terminal Drydock gives you") || !strings.Contains(gem.Spec.SystemAppend, "Sandbox image: test-image.") {
		t.Fatalf("gemini spec %+v", gem.Spec)
	}
	if strings.Contains(strings.Join(gem.Spec.Env, " "), "MCP_TOOL_TIMEOUT") {
		t.Fatal("Claude Code's variables are Claude Code's")
	}
	goose := rt.Config{Adapter: "goose", Spec: agents.RunSpec{PermissionMode: "auto"}}
	if q, err := d.prepareWorker(ctx, workflow.NodeInput{}, task, &goose); q != nil || err != nil || !goose.Spec.Sandboxed || goose.Spec.PermissionMode != "" || goose.Spec.Terminals == nil {
		t.Fatalf("goose: %+v %v %+v", q, err, goose.Spec)
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

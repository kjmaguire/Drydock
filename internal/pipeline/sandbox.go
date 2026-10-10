package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// SandboxHook runs a task's commands in a container instead of on the human's machine (docs/sandbox.md). Deps.Sandbox nil:
// never.
type SandboxHook interface {
	// On reports whether the project runs its commands in a sandbox.
	On(ctx context.Context, projectID string) bool
	// Prepare makes sure the task's sandbox runs (pulling images, starting services and running setup the first time) and
	// returns what the worker should know about it.
	Prepare(ctx context.Context, t tasks.Task) (string, error)
	// Check rewrites a check so it runs in the task's sandbox.
	Check(ctx context.Context, t tasks.Task, s checks.Spec) (checks.Spec, error)
}

// sandboxed reports whether the task's commands run in a sandbox.
func (d Deps) sandboxed(ctx context.Context, t tasks.Task) bool {
	return d.Sandbox != nil && t.Worktree != "" && d.Sandbox.On(ctx, t.ProjectID)
}

// prepareWorker sets up a worker turn. In a sandboxed project the worker's commands go to the task's container: its CLI starts
// without any command tool (Claude Code's restricted mode) and gets the sandbox tools instead. Otherwise the project's check
// commands may be allowed (addWorkerTools). A sandbox that cannot start, or a worker CLI the sandbox does not support, becomes
// a question for the human (out is then set).
func (d Deps) prepareWorker(ctx context.Context, in workflow.NodeInput, t tasks.Task, cfg *rt.Config) (out *workflow.NodeOutput, err error) {
	if !d.sandboxed(ctx, t) {
		d.addWorkerTools(ctx, t, cfg)
		return nil, nil
	}
	if serr := d.sandboxTurn(ctx, t, cfg); serr != nil {
		o, err := sandboxTrouble(in, serr)
		return &o, err
	}
	return nil, nil
}

// sandboxTurn makes a turn that may change the worktree run sandboxed: no command tool on this machine, the sandbox tools and
// instructions instead. Only Claude Code workers can run sandboxed in this build.
func (d Deps) sandboxTurn(ctx context.Context, t tasks.Task, cfg *rt.Config) error {
	if cfg.Adapter != "claude" {
		return fmt.Errorf("this project runs commands in a sandbox, which works with Claude Code agents in this build; this turn's agent uses %s. Assign a Claude Code agent, or turn the sandbox off in the project settings", cfg.Adapter)
	}
	desc, err := d.Sandbox.Prepare(ctx, t)
	if err != nil {
		return err
	}
	cfg.Spec.Sandboxed = true
	cfg.Spec.SystemAppend = strings.TrimSpace(cfg.Spec.SystemAppend + "\n\n" + SandboxText(desc))
	// a build or a test suite run through sandbox_run can take long: the CLI must wait for the tool past the sandbox's own
	// 60-minute limit, and must not move a long call to the background and end the turn before its result is in (observed with
	// 2.1.296 when CLAUDE_AUTO_BACKGROUND_TASKS was on: a 130-second command went to the background after 120 seconds and the
	// worker finished without its result). Long-running servers have sandbox_start for that.
	cfg.Spec.Env = append(append([]string(nil), cfg.Spec.Env...), "MCP_TOOL_TIMEOUT="+SandboxToolTimeoutMS,
		"CLAUDE_AUTO_BACKGROUND_TASKS=false", "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1")
	return nil
}

// SandboxToolTimeoutMS is how long a sandboxed worker's CLI waits for one tool call: longer than the sandbox's own 60-minute
// limit on a command (Claude Code reads MCP_TOOL_TIMEOUT, in milliseconds).
const SandboxToolTimeoutMS = "3900000"

// SandboxText tells a worker how to run commands in its sandbox.
func SandboxText(describe string) string {
	var b strings.Builder
	b.WriteString("## Commands run in this task's sandbox\n")
	b.WriteString("You have no shell on this machine. Run every command (installs, builds, tests, migrations, scripts) with the ")
	b.WriteString("sandbox_run tool of the drydock MCP server: it runs `sh -c` in a Linux container where your working folder is ")
	b.WriteString("mounted, and you may do anything there. Start long-running processes (a dev server, a watcher) with sandbox_start, ")
	b.WriteString("read their output with sandbox_logs and end them with sandbox_stop; sandbox_info describes the sandbox again. ")
	b.WriteString("Edit files with your file tools as usual: the container sees your changes at once. `git` does not work in ")
	b.WriteString("the container (Drydock records your changes when the turn ends). Run the project's tests before you finish.\n")
	b.WriteString(strings.TrimSpace(describe))
	return b.String()
}

// sandboxTrouble turns a sandbox that cannot start into a question for the human instead of a failed run: answering runs the
// same step again (after the human fixed the engine or the definition); rejecting stops the run.
func sandboxTrouble(in workflow.NodeInput, err error) (workflow.NodeOutput, error) {
	if h := humanResponse(in); h.decision == "rejected" && strings.HasPrefix(h.by, "human:") {
		return workflow.NodeOutput{}, fmt.Errorf("the sandbox could not start and %s stopped the run: %w", h.by, err)
	}
	msg := err.Error()
	if len(msg) > 600 {
		msg = msg[:600] + "..."
	}
	p, _ := json.Marshal(map[string]any{"step": "sandbox", "error": msg})
	return workflow.NodeOutput{Request: &workflow.RequestSpec{Kind: "question", Retry: true, DeliverAnswer: true, Payload: p,
		Title: "The task's sandbox could not start: " + msg + ". Fix it (is the container engine running? is " + ".drydock/sandbox.json right?), then answer to try again."}}, nil
}

// sandboxChecks rewrites the command checks of a sandboxed task so they run in its container. In-process checks (the secret
// scan of the diff, the dependency audit) read files and stay on this machine.
func (d Deps) sandboxChecks(ctx context.Context, t tasks.Task, specs []checks.Spec) ([]checks.Spec, error) {
	if !d.sandboxed(ctx, t) {
		return specs, nil
	}
	if _, err := d.Sandbox.Prepare(ctx, t); err != nil {
		return nil, err
	}
	out := make([]checks.Spec, 0, len(specs))
	for _, s := range specs {
		if s.InProcess != nil {
			out = append(out, s)
			continue
		}
		w, err := d.Sandbox.Check(ctx, t, s)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

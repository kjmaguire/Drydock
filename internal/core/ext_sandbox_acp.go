package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/sandbox"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// An ACP worker in a sandboxed project (decision P131, docs/sandbox.md s7): its terminal requests run in the task's container
// with the limits of sandbox_run, its permission requests are answered by sandboxAsker, and a command its CLI ran on this
// machine anyway is recorded (the adapter has stopped the turn).

// Terminals is pipeline.SandboxHook.Terminals.
func (h sandboxHook) Terminals(t tasks.Task, agentID string) agents.TerminalHost {
	return sandboxTerminals{c: h.c, task: t, agentID: agentID}
}

// sandboxTerminals runs one session's terminal requests in its task's sandbox.
type sandboxTerminals struct {
	c       *Core
	task    tasks.Task
	agentID string
}

// terminalTimeout bounds one terminal command: the sandbox's own most (an ACP terminal names none; the agent may kill it
// sooner, and the turn's end stops it).
const terminalTimeout = sandbox.MaxTimeout

func (s sandboxTerminals) Run(ctx context.Context, req agents.TerminalRequest, out io.Writer) (int, error) {
	b, err := s.c.sandboxBox(ctx, s.task)
	if err != nil {
		return -1, err
	}
	// a command with its arguments apart is quoted word by word; a command alone is a command line, as an agent that sends
	// "npm test" means it
	command := req.Command
	if len(req.Args) > 0 {
		if command, err = sandbox.ShellJoin(append([]string{req.Command}, req.Args...)); err != nil {
			return -1, err
		}
	}
	res, err := s.c.sandboxes().Exec(ctx, b, sandbox.ExecRequest{Command: command, Dir: req.Dir, Env: req.Env, Timeout: terminalTimeout, Output: out})
	if err != nil {
		return -1, err
	}
	s.c.recordSandboxCommand(ctx, s.task, s.agentID, "", "terminal", command, res)
	if res.TimedOut {
		fmt.Fprintf(out, "\n[Drydock: the command timed out after %s and was stopped, with everything it started]\n", terminalTimeout)
		return -1, nil
	}
	return res.ExitCode, nil
}

// recordSandboxCommand puts a command that ran in a task's sandbox on the task's and the agent's timeline (sandbox.command),
// shortened and redacted; via says how it was asked for (sandbox_run, terminal).
func (c *Core) recordSandboxCommand(ctx context.Context, t tasks.Task, agentID, runID, via, command string, res sandbox.ExecResult) {
	shown := command
	if c.Scanner != nil {
		shown = c.Scanner.Redact(shown)
	}
	if len(shown) > 300 {
		shown = shown[:300] + "..."
	}
	data, _ := json.Marshal(map[string]any{"command": shown, "exitCode": res.ExitCode, "timedOut": res.TimedOut, "durationMs": res.Duration.Milliseconds(), "bytes": res.Bytes, "via": via})
	if _, err := c.Bus.Publish(ctx, events.Event{Type: "sandbox.command", ProjectID: t.ProjectID, TaskID: t.ID, AgentID: agentID, RunID: runID, Data: data}); err != nil {
		c.logf("sandbox: event: %v", err)
	}
}

// sandboxAsker answers a sandboxed ACP worker's permission requests. Its commands run in the container through Drydock's
// terminal or sandbox_run, so a command its CLI would run itself, on this machine, is refused with where to run it instead. A
// read or search of paths inside the worktree is allowed, and an edit, delete or move there too except inside .git (the
// sandbox's file rule for Claude Code's restricted mode). Anything else (a fetch, a call that names no path, a path outside) is
// refused. No person is asked: the container is the boundary, as for a sandboxed Claude Code worker.
func sandboxAsker(root string) agents.PermissionAsker {
	return func(_ context.Context, q agents.ToolAsk) (bool, string) {
		switch q.Kind {
		case "execute":
			return false, "This project runs its commands in a sandbox: run this in a terminal Drydock gives you or with the sandbox_run tool. Drydock does not run it on this machine."
		case "read", "search", "edit", "delete", "move":
			if len(q.Locations) == 0 {
				return false, "This project runs its commands in a sandbox, and this call names no path in the working folder, so Drydock refused it."
			}
			writes := q.Kind != "read" && q.Kind != "search"
			for _, l := range q.Locations {
				rel, ok := insideWorktree(root, l)
				if !ok {
					return false, "This project runs its commands in a sandbox, and " + l + " is outside the working folder, so Drydock refused it."
				}
				if writes && (rel == ".git" || strings.HasPrefix(rel, ".git/")) {
					return false, "This project runs its commands in a sandbox, and .git is Drydock's: it records your changes when the turn ends."
				}
			}
			return true, ""
		}
		return false, "This project runs its commands in a sandbox, and Drydock allows only reads and edits in the working folder there; this call (" + strings.TrimSpace(q.Kind+" "+q.Tool) + ") was refused."
	}
}

// insideWorktree reports whether p (absolute, or relative to root) is inside root once links are followed as far as the path
// exists, and gives it relative to root with "/" separators.
func insideWorktree(root, p string) (string, bool) {
	if p == "" || strings.ContainsRune(p, 0) {
		return "", false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p = filepath.Clean(p)
	if _, ok := relWithin(filepath.Clean(root), p); !ok {
		if _, ok := relWithin(realRoot, p); !ok {
			return "", false
		}
	}
	// follow links in the part that exists: a link the agent made in the container may name a file of this machine
	existing, rest := p, ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", false
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", false
	}
	rel, ok := relWithin(realRoot, filepath.Join(resolved, rest))
	if !ok {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func relWithin(base, p string) (string, bool) {
	rel, err := filepath.Rel(base, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

// EventSandboxEscape is the event and audit action of a command a sandboxed worker's CLI ran on this machine without asking
// (or of the session leaving the mode in which its CLI asks).
const EventSandboxEscape = "sandbox.escape"

// recordSandboxEscape is RunSpec.OnSandboxEscape of a sandboxed ACP worker: the audit log and the task's events keep the tool,
// its kind and the call's id, never its input.
func (c *Core) recordSandboxEscape(cfg rt.Config, runID string) func(agents.ToolAsk) {
	return func(q agents.ToolAsk) {
		ctx := context.Background()
		detail := map[string]any{"tool": q.Tool, "kind": q.Kind, "toolCallId": q.ToolCallID, "adapter": cfg.Adapter, "agentRun": runID}
		if _, err := security.NewAudit(c.DB, c.Scanner).Append(ctx, security.AuditEntry{Actor: "system", Action: EventSandboxEscape, Subject: runID, Detail: detail}); err != nil {
			c.logf("audit %s: %v", EventSandboxEscape, err)
		}
		b, _ := json.Marshal(detail)
		_, _ = c.Bus.Publish(ctx, events.Event{Type: EventSandboxEscape, ProjectID: cfg.ProjectID, TaskID: cfg.TaskID, AgentID: cfg.AgentID, RunID: runID, Data: b})
	}
}

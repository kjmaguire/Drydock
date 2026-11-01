package acp

import (
	"context"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// A sandboxed session (decision P131, docs/sandbox.md): an ACP worker in a project whose commands run in a container. Its
// terminal requests go to the container (terminal.go, RunSpec.Terminals); it runs in the mode in which its CLI asks before each
// command it would run itself (requireMode), and the session's asker refuses those. Both rest on the CLI doing what it says,
// so the session also watches: a command the CLI reports having run without asking and without Drydock's terminal ran on this
// machine, and leaving the mode would let the next one through. Either stops the turn at once and is recorded
// (RunSpec.OnSandboxEscape). That is detection after the fact, not prevention: the command has already run.

// sandboxTool reports whether a call is one of Drydock's own sandbox tools on the gateway (sandbox_run and the rest), which an
// agent may report with any kind.
func sandboxTool(st *toolState) bool {
	n := strings.ToLower(st.metaName + " " + st.title)
	return strings.Contains(n, "sandbox_") || strings.Contains(n, "drydock")
}

// checkEscape stops the turn when a sandboxed session's CLI reports a command that ended without its permission request and
// without a terminal of this session: the CLI ran it on this machine. It is judged when the call has ended, so a CLI that made
// its terminal after announcing the call is not mistaken for one that ran it itself.
func (s *Session) checkEscape(st *toolState) {
	if !s.spec.Sandboxed {
		return
	}
	s.mu.Lock()
	ran := st.kind == "execute" && isTerminal(st.status) && !st.asked && !st.escapeSeen && !sandboxTool(st)
	if ran {
		st.escapeSeen = true
	}
	ids := append([]string(nil), st.terminals...)
	before := st.termsAt
	q := agents.ToolAsk{ToolCallID: st.id, Tool: st.toolName(), Title: st.title, Kind: st.kind, Args: st.args(), Locations: append([]string(nil), st.locations...)}
	s.mu.Unlock()
	if !ran {
		return
	}
	for _, id := range ids {
		if s.terms.created(id) {
			return
		}
	}
	if s.terms.count() > before {
		return // a terminal was made while the call ran: the CLI ran it there
	}
	s.escape(q, "the agent's CLI ran this command itself, on this machine and outside the sandbox, without asking: Drydock stopped the turn")
}

// checkMode stops the turn when a sandboxed session leaves the mode it was put in (the CLI changed it, or the agent asked it to).
func (s *Session) checkMode(mode string) {
	s.mu.Lock()
	want := s.sandboxMode
	s.mu.Unlock()
	if !s.spec.Sandboxed || want == "" || mode == want {
		return
	}
	s.escape(agents.ToolAsk{Tool: "session mode", Title: mode, Kind: "mode"},
		"the session left the "+want+" mode, in which its CLI asks before each command: Drydock stopped the turn")
}

func (s *Session) escape(q agents.ToolAsk, why string) {
	s.emitKind(workflow.ChunkPermissionDenied, workflow.PermissionDenial{ToolName: q.Tool, ToolUseID: q.ToolCallID, Input: q.Args, Reason: why})
	s.emitKind(workflow.ChunkError, workflow.ErrorChunk{Message: q.Tool + ": " + why})
	if f := s.spec.OnSandboxEscape; f != nil {
		f(q)
	}
	safe.Go("acp.Session.escape", func() {
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		defer cancel()
		_ = s.Interrupt(ctx)
	})
}

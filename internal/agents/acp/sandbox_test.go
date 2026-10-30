package acp_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// recordingHost is a labelled test double of the container's terminals (RunSpec.Terminals, decision P131): it records each
// request, writes what it was asked to run and answers with exit code 3.
type recordingHost struct {
	mu   sync.Mutex
	reqs []agents.TerminalRequest
}

func (h *recordingHost) Run(_ context.Context, req agents.TerminalRequest, out io.Writer) (int, error) {
	h.mu.Lock()
	h.reqs = append(h.reqs, req)
	h.mu.Unlock()
	fmt.Fprintf(out, "ran: %s %s in %q", req.Command, strings.Join(req.Args, " "), req.Dir)
	return 3, nil
}

// A sandboxed session's terminals run through its host, never on this machine: the capability is offered although the adapter
// offers no terminals of its own, the folder goes as the working folder's own, nothing of this machine's environment goes with
// it, and a folder outside the working folder is refused as before.
func TestASandboxedSessionsTerminalsRunThroughItsHost(t *testing.T) {
	work, _ := layout(t)
	host := &recordingHost{}
	ad := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{{
		{ReportTerminal: true},
		{Term: &fakeacp.TermOp{Command: "go", Args: []string{"test", "./..."}, Cwd: "sub"}},
		{Term: &fakeacp.TermOp{Command: "npm test"}},
		{Term: &fakeacp.TermOp{Command: "go", Args: []string{"version"}, Cwd: ".."}},
	}}})
	cs := turn(t, startSession(t, ad, agents.RunSpec{AgentID: "a", Cwd: work, Sandboxed: true, Terminals: host, Env: []string{"DRYDOCK_SECRET_ISH=1"}}), "go")
	got := text(cs)
	if !strings.Contains(got, "terminal=true") {
		t.Fatalf("the terminal capability is offered to a sandboxed session: %q", got)
	}
	if !strings.Contains(got, `term OK exit=3 out=ran: go test ./... in "sub"`) || !strings.Contains(got, `term OK exit=3 out=ran: npm test  in ""`) {
		t.Fatalf("the host ran the commands: %q", got)
	}
	if !strings.Contains(got, "term.create ERR") || !strings.Contains(got, "outside the session working directory") {
		t.Fatalf("a folder outside the working folder is refused: %q", got)
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if len(host.reqs) != 2 {
		t.Fatalf("requests %+v", host.reqs)
	}
	for _, r := range host.reqs {
		if len(r.Env) != 0 {
			t.Fatalf("nothing of this machine's environment goes to the container: %v", r.Env)
		}
	}

	// without a host, the same adapter offers no terminal at all
	off := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{{{ReportTerminal: true}}}})
	if got := text(turn(t, startSession(t, off, agents.RunSpec{AgentID: "a", Cwd: work}), "go")); !strings.Contains(got, "terminal=false") {
		t.Fatalf("%q", got)
	}
}

type escapes struct {
	mu  sync.Mutex
	got []agents.ToolAsk
}

func (e *escapes) record(q agents.ToolAsk) { e.mu.Lock(); e.got = append(e.got, q); e.mu.Unlock() }
func (e *escapes) list() []agents.ToolAsk {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]agents.ToolAsk(nil), e.got...)
}

func denials(t *testing.T, cs []workflow.Chunk) []workflow.PermissionDenial {
	var out []workflow.PermissionDenial
	for _, c := range ofKind(cs, workflow.ChunkPermissionDenied) {
		out = append(out, decode[workflow.PermissionDenial](t, c))
	}
	return out
}

// A command the CLI of a sandboxed session reports having run itself (no permission request, no terminal of the session) ran
// on this machine: the turn is stopped and it is recorded. A command run in the session's terminal, Drydock's own sandbox tool
// and a session outside a sandbox are left alone.
func TestASandboxedSessionStopsATurnWhoseCLIRanACommandItself(t *testing.T) {
	work, _ := layout(t)
	shell := &fakeacp.Tool{ID: "t1", Title: "Shell", Kind: "execute", Input: map[string]any{"command": "go test ./..."}}
	ad := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{{{Tool: shell}, {SleepMS: 2000}, {Say: "after"}}}})
	esc := &escapes{}
	cs := turn(t, startSession(t, ad, agents.RunSpec{AgentID: "a", Cwd: work, Sandboxed: true, Terminals: &recordingHost{}, OnSandboxEscape: esc.record}), "go")
	d := denials(t, cs)
	if len(d) != 1 || d[0].ToolUseID != "t1" || !strings.Contains(d[0].Reason, "outside the sandbox") {
		t.Fatalf("denials %+v", d)
	}
	if got := esc.list(); len(got) != 1 || got[0].ToolCallID != "t1" || got[0].Kind != "execute" {
		t.Fatalf("recorded %+v", got)
	}
	if stopReason(t, cs) != "cancelled" || strings.Contains(text(cs), "after") {
		t.Fatalf("the turn is stopped: %s %q", stopReason(t, cs), text(cs))
	}

	viaTerm := &fakeacp.Tool{ID: "t2", Title: "Shell", Kind: "execute", Input: map[string]any{"command": "go test"}, Term: &fakeacp.TermOp{Command: "go", Args: []string{"test"}}}
	tool := &fakeacp.Tool{ID: "t3", Title: "drydock: sandbox_run", Kind: "execute", Input: map[string]any{"command": "go test"}}
	ok := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{{{Tool: viaTerm}, {Tool: tool}, {Say: "done"}}}})
	esc = &escapes{}
	cs = turn(t, startSession(t, ok, agents.RunSpec{AgentID: "a", Cwd: work, Sandboxed: true, Terminals: &recordingHost{}, OnSandboxEscape: esc.record}), "go")
	if len(denials(t, cs)) != 0 || len(esc.list()) != 0 || !strings.Contains(text(cs), "done") {
		t.Fatalf("a terminal's command and sandbox_run are not escapes: %+v %+v %q", denials(t, cs), esc.list(), text(cs))
	}

	outside := fakeAdapter(t, fakeacp.Script{Turns: [][]fakeacp.Step{{{Tool: shell}, {Say: "done"}}}})
	cs = turn(t, startSession(t, outside, agents.RunSpec{AgentID: "a", Cwd: work, OnSandboxEscape: esc.record}), "go")
	if len(denials(t, cs)) != 0 || len(esc.list()) != 0 {
		t.Fatal("outside a sandbox the CLI's own commands are its own rules' business")
	}
}

// A sandboxed session runs in the mode its manifest names, in which its CLI asks before each command: a CLI that does not
// offer it does not start, and a session that leaves it has its turn stopped.
func TestASandboxedSessionKeepsTheModeInWhichItsCLIAsks(t *testing.T) {
	work, _ := layout(t)
	ad := fakeAdapter(t, fakeacp.Script{Modes: []string{"auto", "default"}, Turns: [][]fakeacp.Step{{{ReportConfig: true}}, {{SwitchMode: "auto"}, {SleepMS: 2000}, {Say: "after"}}}})
	esc := &escapes{}
	s := startSession(t, ad, agents.RunSpec{AgentID: "a", Cwd: work, Sandboxed: true, PermissionMode: "default", Terminals: &recordingHost{}, OnSandboxEscape: esc.record})
	if got := text(turn(t, s, "go")); !strings.Contains(got, "mode=default") {
		t.Fatalf("the session runs in its mode: %q", got)
	}
	cs := turn(t, s, "again")
	if d := denials(t, cs); len(d) != 1 || !strings.Contains(d[0].Reason, "left the default mode") || strings.Contains(text(cs), "after") {
		t.Fatalf("leaving the mode stops the turn: %+v %q", d, text(cs))
	}
	if got := esc.list(); len(got) != 1 || got[0].Kind != "mode" || got[0].Title != "auto" {
		t.Fatalf("recorded %+v", got)
	}

	missing := fakeAdapter(t, fakeacp.Script{Modes: []string{"auto", "yolo"}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if sess, err := missing.Start(ctx, proc.New(), agents.RunSpec{AgentID: "a", Cwd: work, Sandboxed: true, PermissionMode: "default"}); err == nil {
		_ = sess.Stop(ctx)
		t.Fatal("a sandboxed session whose CLI does not offer its mode must not start")
	} else if !strings.Contains(err.Error(), `"default" mode`) || !strings.Contains(err.Error(), "auto, yolo") {
		t.Fatalf("the refusal names the mode and what is offered: %v", err)
	}
}

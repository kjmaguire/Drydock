package acp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/coder/acp-go-sdk"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/safe"
)

// DefaultTerminalOutputLimit bounds captured terminal output when the agent gives no outputByteLimit.
const DefaultTerminalOutputLimit = 1 << 20

// terminals serves the client-side terminal methods. Commands run as argv through proc.Manager (never a shell string),
// so on Windows each terminal sits in its own Job Object and Kill takes its tree. The working directory must be inside the
// session cwd (the same guard as fs). The command itself is not confined: it is whatever the agent asked for and the user
// approved, so the capability is advertised only when Config.Terminal is set.
type terminals struct {
	s    *Session
	mu   sync.Mutex
	next int
	m    map[string]*terminal
}

type terminal struct {
	kill    func() // ends the command and everything it started
	mu      sync.Mutex
	buf     []byte
	limit   int
	trunc   bool
	done    chan struct{} // closed when the command has ended and its output was drained
	exit    int
	readers sync.WaitGroup
}

// termWriter feeds a terminal's buffer (the output of a command that runs elsewhere, RunSpec.Terminals).
type termWriter struct{ t *terminal }

func (w termWriter) Write(b []byte) (int, error) { w.t.write(b); return len(b), nil }

func newTerminals(s *Session) *terminals { return &terminals{s: s, m: map[string]*terminal{}} }

func (t *terminal) write(b []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > t.limit {
		// keep the tail on a rune boundary, as ACP specifies
		cut := len(t.buf) - t.limit
		for cut < len(t.buf) && t.buf[cut]&0xC0 == 0x80 {
			cut++
		}
		t.buf = append([]byte(nil), t.buf[cut:]...)
		t.trunc = true
	}
}

// terminalsOffered reports whether the session serves terminals: the adapter offers them on this machine, or the session
// routes them elsewhere (a sandboxed worker's container).
func (s *Session) terminalsOffered() bool { return s.adapter.cfg.Terminal || s.spec.Terminals != nil }

func (ts *terminals) create(ctx context.Context, p acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	if !ts.s.terminalsOffered() {
		return acp.CreateTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalCreate)
	}
	if strings.TrimSpace(p.Command) == "" {
		return acp.CreateTerminalResponse{}, &acp.RequestError{Code: -32602, Message: "command is required"}
	}
	cwd := ""
	if p.Cwd != nil {
		cwd = *p.Cwd
	}
	dir, err := ts.s.guard.dir(cwd)
	if err != nil {
		return acp.CreateTerminalResponse{}, reqErr(err)
	}
	limit := DefaultTerminalOutputLimit
	if p.OutputByteLimit != nil && *p.OutputByteLimit > 0 {
		limit = *p.OutputByteLimit
	}
	if host := ts.s.spec.Terminals; host != nil {
		return ts.routed(host, p, dir, limit)
	}
	env := os.Environ()
	if ts.s.adapter.cfg.ScrubEnv {
		env = buildEnv(env, true)
	}
	env = append(env, ts.s.spec.Env...)
	for _, e := range p.Env {
		env = append(env, e.Name+"="+e.Value)
	}
	// The terminal outlives this request, so it must not be tied to the request context; the session context ends it.
	pr, err := ts.s.pm.Start(ts.s.ctx, proc.Spec{Path: p.Command, Args: p.Args, Dir: dir, Env: env, Label: "terminal:" + ts.s.spec.AgentID})
	if err != nil {
		return acp.CreateTerminalResponse{}, &acp.RequestError{Code: -32603, Message: "start: " + err.Error()}
	}
	t := &terminal{kill: func() { _ = pr.Kill(true) }, limit: limit, done: make(chan struct{})}
	_ = pr.Stdin().Close()
	for _, r := range []io.Reader{pr.Stdout(), pr.Stderr()} {
		t.readers.Add(1)
		safe.Go("acp.terminals.create", func() {
			func(r io.Reader) {
				defer t.readers.Done()
				b := make([]byte, 4096)
				for {
					n, err := r.Read(b)
					if n > 0 {
						t.write(b[:n])
					}
					if err != nil {
						return
					}
				}
			}(r)
		})
	}
	safe.Go("acp.terminals.create#2", func() {
		t.exit = (<-pr.Wait()).Code
		t.readers.Wait()
		close(t.done)
	})
	return acp.CreateTerminalResponse{TerminalId: ts.add(t)}, nil
}

func (ts *terminals) add(t *terminal) string {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.next++
	id := fmt.Sprintf("term_%d", ts.next)
	ts.m[id] = t
	return id
}

// created reports whether the session made the terminal (a tool call that names it ran its command through Drydock).
func (ts *terminals) created(id string) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	_, ok := ts.m[id]
	return ok
}

// count is how many terminals the session made so far.
func (ts *terminals) count() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.next
}

// routed runs a terminal request through the session's TerminalHost (decision P131: a sandboxed worker's commands run in the
// task's container). Nothing of this machine's environment goes with it: only the variables the agent asked for. The folder is
// the request's, relative to the working folder, which the container mounts.
func (ts *terminals) routed(host agents.TerminalHost, p acp.CreateTerminalRequest, dir string, limit int) (acp.CreateTerminalResponse, error) {
	rel, ok := within(ts.s.guard.real, dir)
	if !ok {
		return acp.CreateTerminalResponse{}, reqErr(ErrOutsideCwd)
	}
	if rel == "." {
		rel = ""
	}
	req := agents.TerminalRequest{Command: p.Command, Args: append([]string(nil), p.Args...), Dir: filepath.ToSlash(rel)}
	for _, e := range p.Env {
		req.Env = append(req.Env, e.Name+"="+e.Value)
	}
	ctx, cancel := context.WithCancel(ts.s.ctx) // the terminal outlives the request; the session's end stops it
	t := &terminal{kill: cancel, limit: limit, done: make(chan struct{})}
	safe.Go("acp.terminals.routed", func() {
		defer cancel()
		code, err := host.Run(ctx, req, termWriter{t})
		if err != nil && ctx.Err() == nil {
			t.write([]byte("\n[Drydock: the command could not run in the sandbox: " + err.Error() + "]\n"))
			if code == 0 {
				code = -1
			}
		}
		t.exit = code
		close(t.done)
	})
	return acp.CreateTerminalResponse{TerminalId: ts.add(t)}, nil
}

func (ts *terminals) get(id string) (*terminal, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	t, ok := ts.m[id]
	if !ok {
		return nil, &acp.RequestError{Code: -32602, Message: "unknown terminal " + id}
	}
	return t, nil
}

func (t *terminal) exitStatus() *acp.TerminalExitStatus {
	select {
	case <-t.done:
		code := t.exit
		return &acp.TerminalExitStatus{ExitCode: &code}
	default:
		return nil
	}
}

func (ts *terminals) output(p acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	t, err := ts.get(p.TerminalId)
	if err != nil {
		return acp.TerminalOutputResponse{}, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return acp.TerminalOutputResponse{Output: string(t.buf), Truncated: t.trunc, ExitStatus: t.exitStatus()}, nil
}

func (ts *terminals) wait(ctx context.Context, p acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	t, err := ts.get(p.TerminalId)
	if err != nil {
		return acp.WaitForTerminalExitResponse{}, err
	}
	select {
	case <-t.done:
		code := t.exit
		return acp.WaitForTerminalExitResponse{ExitCode: &code}, nil
	case <-ctx.Done():
		return acp.WaitForTerminalExitResponse{}, ctx.Err()
	}
}

func (ts *terminals) kill(p acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	t, err := ts.get(p.TerminalId)
	if err != nil {
		return acp.KillTerminalResponse{}, err
	}
	t.kill()
	return acp.KillTerminalResponse{}, nil
}

func (ts *terminals) release(p acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	ts.mu.Lock()
	t, ok := ts.m[p.TerminalId]
	delete(ts.m, p.TerminalId)
	ts.mu.Unlock()
	if !ok {
		return acp.ReleaseTerminalResponse{}, &acp.RequestError{Code: -32602, Message: "unknown terminal " + p.TerminalId}
	}
	t.kill()
	return acp.ReleaseTerminalResponse{}, nil
}

// killAll ends every terminal; called when the session stops.
func (ts *terminals) killAll() {
	ts.mu.Lock()
	all := make([]*terminal, 0, len(ts.m))
	for _, t := range ts.m {
		all = append(all, t)
	}
	ts.m = map[string]*terminal{}
	ts.mu.Unlock()
	for _, t := range all {
		t.kill()
	}
}

package acp

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/coder/acp-go-sdk"

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
	p       proc.Proc
	mu      sync.Mutex
	buf     []byte
	limit   int
	trunc   bool
	done    chan struct{} // closed when the process has exited and its output was drained
	exit    proc.ExitInfo
	readers sync.WaitGroup
}

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

func (ts *terminals) create(ctx context.Context, p acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	if !ts.s.adapter.cfg.Terminal {
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
	limit := DefaultTerminalOutputLimit
	if p.OutputByteLimit != nil && *p.OutputByteLimit > 0 {
		limit = *p.OutputByteLimit
	}
	t := &terminal{p: pr, limit: limit, done: make(chan struct{})}
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
		t.exit = <-pr.Wait()
		t.readers.Wait()
		close(t.done)
	})
	ts.mu.Lock()
	ts.next++
	id := fmt.Sprintf("term_%d", ts.next)
	ts.m[id] = t
	ts.mu.Unlock()
	return acp.CreateTerminalResponse{TerminalId: id}, nil
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
		code := t.exit.Code
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
		code := t.exit.Code
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
	_ = t.p.Kill(true)
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
	_ = t.p.Kill(true)
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
		_ = t.p.Kill(true)
	}
}

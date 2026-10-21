//go:build !windows

package proc

import (
	"context"
	"errors"
	"github.com/kjmaguire/drydock/internal/safe"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// Development-host fallback (see package doc): direct-child kill only; no process-tree guarantees.

type manager struct{}

func newManager() Manager { return manager{} }

// EnterAppJob is a no-op off Windows.
func EnterAppJob() error { return nil }

type devProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.Reader
	stderr io.Reader
	exit   chan ExitInfo
	done   chan struct{}
	mu     sync.Mutex
	killed bool
}

func (manager) Start(ctx context.Context, spec Spec) (Proc, error) {
	path, err := exec.LookPath(spec.Path)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = WithSafeExeSearch(spec.Env)
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW
	if err := cmd.Start(); err != nil {
		for _, f := range []*os.File{inR, inW, outR, outW, errR, errW} {
			f.Close()
		}
		return nil, err
	}
	inR.Close()
	outW.Close()
	errW.Close()
	p := &devProc{cmd: cmd, stdin: inW, stdout: outR, stderr: errR, exit: make(chan ExitInfo, 1), done: make(chan struct{})}
	safe.Go("proc.manager.Start", func() {
		err := cmd.Wait()
		ei := ExitInfo{Code: cmd.ProcessState.ExitCode()}
		p.mu.Lock()
		ei.Killed = p.killed
		p.mu.Unlock()
		var ee *exec.ExitError
		if err != nil && !errors.As(err, &ee) {
			ei.Err = err
		}
		p.exit <- ei
		close(p.done)
	})
	safe.Go("proc.manager.Start#2", func() {
		select {
		case <-ctx.Done():
			_ = p.Kill(true)
		case <-p.done:
		}
	})
	return p, nil
}

func (p *devProc) PID() int              { return p.cmd.Process.Pid }
func (p *devProc) Stdin() io.WriteCloser { return p.stdin }
func (p *devProc) Stdout() io.Reader     { return p.stdout }
func (p *devProc) Stderr() io.Reader     { return p.stderr }
func (p *devProc) Wait() <-chan ExitInfo { return p.exit }

func (p *devProc) Signal(sig Signal) error {
	switch sig {
	case Interrupt:
		return p.cmd.Process.Signal(syscall.SIGINT)
	case Terminate:
		return p.cmd.Process.Signal(syscall.SIGTERM)
	}
	return ErrNotSupported
}

func (p *devProc) Kill(tree bool) error {
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	return p.cmd.Process.Kill() // direct child only on the development host
}

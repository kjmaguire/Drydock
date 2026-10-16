// Package proc is the process manager: PID, stdio, signals, environment, working directory and process trees.
//
// On Windows (the only product target) every agent runs in its own Job Object created with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, assigned while the process is still suspended, so Kill(tree=true)
// reaps grandchildren (npx.cmd, node, test runners). The Drydock process also joins a kill-on-close job at
// startup (EnterAppJob) so that nothing it spawned can outlive it.
//
// The non-Windows implementation exists only so the rest of the Go core can be developed and tested on a
// non-Windows development host. It is not a product path and cannot kill grandchildren (decisions.md, P1).
package proc

import (
	"context"
	"errors"
	"io"
	"time"
)

// Spec describes a process to start.
type Spec struct {
	Path string   // executable; resolved with exec.LookPath (so npx -> npx.cmd on Windows)
	Args []string // arguments after the executable (not including argv[0])
	Dir  string   // working directory
	Env  []string // full environment; nil means inherit
	// Label is used in logs and Job Object names ("agent:<id>").
	Label string
}

// ExitInfo describes how a process ended.
type ExitInfo struct {
	Code   int
	Err    error // non-nil for abnormal termination (killed, wait failure)
	Killed bool  // true if Kill was called
}

// Signal is a portable request. Windows has no POSIX signals; see Proc.Signal.
type Signal int

const (
	// Interrupt asks the process to stop its current work (CTRL_BREAK_EVENT where a console exists).
	// A GUI-subsystem host has no console, so callers must prefer protocol-level cancel (ACP session/cancel,
	// Claude Code's stream-json interrupt control message) and treat ErrNotSupported as "use the protocol".
	Interrupt Signal = iota
	// Terminate asks politely; on Windows this is equivalent to Kill(false).
	Terminate
)

// ErrNotSupported is returned when a signal cannot be delivered on this platform or host.
var ErrNotSupported = errors.New("proc: signal not supported")

// Proc is a running process.
type Proc interface {
	PID() int
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Stderr() io.Reader
	Signal(sig Signal) error
	// Kill terminates the process. With tree=true it terminates every descendant (Windows: closes the job).
	Kill(tree bool) error
	// Wait returns a channel that yields exactly one ExitInfo after the process has exited and its job
	// (if any) has been released.
	Wait() <-chan ExitInfo
}

// Manager starts processes.
type Manager interface {
	Start(ctx context.Context, spec Spec) (Proc, error)
}

// New returns the platform implementation.
func New() Manager { return newManager() }

// WaitTimeout is a helper for tests and shutdown paths.
func WaitTimeout(p Proc, d time.Duration) (ExitInfo, bool) {
	select {
	case ei := <-p.Wait():
		return ei, true
	case <-time.After(d):
		return ExitInfo{}, false
	}
}

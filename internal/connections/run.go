package connections

import (
	"bytes"
	"context"
	"io"
	"os"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/proc"
)

// maxProbeOutput bounds what a probe may return.
const maxProbeOutput = 64 << 10

// runResult is the outcome of one probe command.
type runResult struct {
	Stdout   string
	Stderr   string
	Exit     int
	Err      error // could not start, or killed after the timeout
	TimedOut bool
}

// runProbe runs path with args as an argv array through the process manager (never a shell string), with stdin closed, in a
// throwaway working directory, and kills the whole tree if it outlives the timeout. This is how every detection and auth probe
// runs (docs/connections.md section 2).
func runProbe(ctx context.Context, pm proc.Manager, path string, args, env []string, timeout time.Duration) runResult {
	dir, err := os.MkdirTemp("", "drydock-probe-")
	if err != nil {
		return runResult{Err: err, Exit: -1}
	}
	defer os.RemoveAll(dir)
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	p, err := pm.Start(cctx, proc.Spec{Path: path, Args: args, Dir: dir, Env: env, Label: "probe"})
	if err != nil {
		return runResult{Err: err, Exit: -1}
	}
	_ = p.Stdin().Close()
	var out, errb limited
	var wg sync.WaitGroup
	for _, c := range []struct {
		r io.Reader
		w *limited
	}{{p.Stdout(), &out}, {p.Stderr(), &errb}} {
		wg.Add(1)
		go func(r io.Reader, w *limited) {
			defer wg.Done()
			_, _ = io.Copy(w, r)
		}(c.r, c.w)
	}
	var res runResult
	select {
	case ei := <-p.Wait():
		res.Exit = ei.Code
		res.Err = ei.Err
	case <-cctx.Done():
		_ = p.Kill(true)
		<-p.Wait()
		res.TimedOut, res.Exit, res.Err = true, -1, cctx.Err()
	}
	// A grandchild that inherited the pipes can keep them open after the probe exited; do not wait for it forever.
	drained := make(chan struct{})
	go func() { wg.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
	}
	res.Stdout, res.Stderr = out.String(), errb.String()
	return res
}

// limited is a bytes.Buffer that stops growing at maxProbeOutput.
type limited struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *limited) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if room := maxProbeOutput - l.b.Len(); room > 0 {
		if len(p) > room {
			l.b.Write(p[:room])
		} else {
			l.b.Write(p)
		}
	}
	return len(p), nil
}

func (l *limited) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

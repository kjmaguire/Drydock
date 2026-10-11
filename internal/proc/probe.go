package proc

import (
	"bytes"
	"context"
	"io"
	"os"
	"sync"
	"time"
)

// MaxProbeOutput bounds what a probe keeps of each output stream.
const MaxProbeOutput = 64 << 10

// ProbeResult is the outcome of one probe command.
type ProbeResult struct {
	Stdout   string
	Stderr   string
	Exit     int
	Err      error // could not start, or killed after the timeout
	TimedOut bool
}

// Probe runs a short command (a version or sign-in check) through pm: an argv array (never a shell string), stdin closed, in a
// throwaway working directory when spec.Dir is empty, the whole process tree killed if it outlives timeout (an npm shim on
// Windows is cmd.exe then node: killing only the first would leave node running), and each output capped at MaxProbeOutput.
// Every detection and sign-in probe runs this way (docs/connections.md section 2).
func Probe(ctx context.Context, pm Manager, spec Spec, timeout time.Duration) ProbeResult {
	if spec.Dir == "" {
		dir, err := os.MkdirTemp("", "drydock-probe-")
		if err != nil {
			return ProbeResult{Err: err, Exit: -1}
		}
		defer os.RemoveAll(dir)
		spec.Dir = dir
	}
	if spec.Label == "" {
		spec.Label = "probe"
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	p, err := pm.Start(cctx, spec)
	if err != nil {
		return ProbeResult{Err: err, Exit: -1}
	}
	_ = p.Stdin().Close()
	var out, errb capped
	var wg sync.WaitGroup
	for _, c := range []struct {
		r io.Reader
		w *capped
	}{{p.Stdout(), &out}, {p.Stderr(), &errb}} {
		wg.Add(1)
		go func(r io.Reader, w *capped) {
			defer wg.Done()
			_, _ = io.Copy(w, r)
		}(c.r, c.w)
	}
	var res ProbeResult
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

// capped is a bytes.Buffer that stops growing at MaxProbeOutput.
type capped struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *capped) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if room := MaxProbeOutput - l.b.Len(); room > 0 {
		if len(p) > room {
			l.b.Write(p[:room])
		} else {
			l.b.Write(p)
		}
	}
	return len(p), nil
}

func (l *capped) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

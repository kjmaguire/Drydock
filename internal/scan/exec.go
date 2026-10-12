package scan

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/proc"
)

// Tool is how an external tool is started: its executable (looked up on PATH when it is a bare name) and the arguments
// that always come first. The product leaves Args empty; the test harnesses start their labelled stand-ins through it.
type Tool struct {
	Path string
	Args []string
}

// resolve finds the executable. A Tool with no Path is the named default.
func (t Tool) resolve(name string, look func(string) (string, error)) (string, error) {
	if look == nil {
		look = exec.LookPath
	}
	p := t.Path
	if p == "" {
		p = name
	}
	return look(p)
}

// toolResult is what an external tool did.
type toolResult struct {
	Stdout, Stderr []byte
	Code           int
	TimedOut       bool
	StartErr       error
}

// runTool starts the tool through the process manager with a scrubbed environment and waits for it, at most timeout.
func runTool(ctx context.Context, pm proc.Manager, path string, args []string, dir string, env []string, label string, timeout time.Duration) toolResult {
	var r toolResult
	if pm == nil {
		pm = proc.New()
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	p, err := pm.Start(cctx, proc.Spec{Path: path, Args: args, Dir: dir, Env: append(env, "NO_COLOR=1"), Label: label})
	if err != nil {
		r.StartErr = err
		return r
	}
	var stdout, stderr bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(&stdout, io.LimitReader(p.Stdout(), 64<<20)) }()
	go func() { defer wg.Done(); _, _ = io.Copy(&stderr, io.LimitReader(p.Stderr(), 1<<20)) }()
	_ = p.Stdin().Close()
	var ei proc.ExitInfo
	select {
	case ei = <-p.Wait():
	case <-cctx.Done():
		_ = p.Kill(true)
		ei = <-p.Wait()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	r.Stdout, r.Stderr, r.Code = stdout.Bytes(), stderr.Bytes(), ei.Code
	r.TimedOut = errors.Is(cctx.Err(), context.DeadlineExceeded)
	return r
}

// firstStderrLine is the first non-blank line of a tool's stderr, cleaned and capped, for a reason.
func firstStderrLine(b []byte) string {
	for _, l := range strings.Split(string(b), "\n") {
		if s := strings.TrimSpace(l); s != "" {
			return cleanLine(s, 160)
		}
	}
	return ""
}

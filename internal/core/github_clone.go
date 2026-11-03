package core

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/safe"
)

// A clone's progress and its Cancel (B12). git prints its progress ("Receiving objects:  42% (21/50)") on stderr when asked
// with --progress; the core reads it as it comes, and publishes github.clone_progress with the repository, the phase and the
// percent only (counts, never a path or a line of git's), at most once a second unless the phase changes. github.cloneCancel
// ends a running clone: the process tree is killed (on Windows gh and the git it started share a job, proc.Manager), the
// half-made folder is removed, and the clone answers that it was cancelled.

// errCloneCancelled is a clone the person cancelled.
var errCloneCancelled = errors.New("cancelled")

// cloneProgressEvery is the least time between two progress events of one phase.
var cloneProgressEvery = time.Second

// cloneRuns is the clones running now, by repository (owner/name, lower case), each with the function that cancels it.
type cloneRuns struct {
	mu   sync.Mutex
	runs map[string]context.CancelCauseFunc
}

func (r *cloneRuns) add(repo string, cancel context.CancelCauseFunc) (remove func(), ok bool) {
	k := strings.ToLower(repo)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runs == nil {
		r.runs = map[string]context.CancelCauseFunc{}
	}
	if _, busy := r.runs[k]; busy {
		return nil, false
	}
	r.runs[k] = cancel
	return func() {
		r.mu.Lock()
		delete(r.runs, k)
		r.mu.Unlock()
	}, true
}

// cancel ends the running clone of repo; false when none runs.
func (r *cloneRuns) cancel(repo string) bool {
	r.mu.Lock()
	f, ok := r.runs[strings.ToLower(repo)]
	r.mu.Unlock()
	if ok {
		f(errCloneCancelled)
	}
	return ok
}

// CancelClone ends the running clone of repo (owner/name or its address). It answers whether one was running.
func (c *Core) CancelClone(repo string) bool {
	return c.clones.cancel(normalizeRepo(host.FullNameOf(repo)))
}

func normalizeRepo(repo string) string {
	return strings.ToLower(strings.TrimSpace(repo))
}

// cloneProgressRe is one line of git's progress: "Receiving objects:  42% (21/50), 1.2 MiB | 3 MiB/s", maybe after "remote: ".
var cloneProgressRe = regexp.MustCompile(`^(?:remote:\s*)?([A-Z][a-z]+(?: [a-z]+){0,3}):\s+(\d{1,3})%`)

// parseCloneProgress reads the phase and percent of one line of git's progress.
func parseCloneProgress(line string) (phase string, percent int, ok bool) {
	m := cloneProgressRe.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil || n > 100 {
		return "", 0, false
	}
	return m[1], n, true
}

// splitProgress splits git's stderr at a carriage return as well as a newline: git redraws a progress line in place with \r.
func splitProgress(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// runClone runs one clone (gh repo clone, or git clone for tests) through the process manager, publishing its progress and
// letting github.cloneCancel end it. It returns what the program printed on stderr, for the error when it fails.
func (c *Core) runClone(ctx context.Context, fullName, exe string, args []string) (string, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	remove, ok := c.clones.add(normalizeRepo(fullName), cancel)
	if !ok {
		return "", errors.New("this repository is being cloned already: wait for that clone or cancel it")
	}
	defer remove()
	env := append(os.Environ(), "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0", "NO_COLOR=1")
	p, err := c.cfg.Procs.Start(ctx, proc.Spec{Path: exe, Args: args, Env: env, Label: "clone:" + fullName})
	if err != nil {
		return "", err
	}
	_ = p.Stdin().Close()
	safe.Go("core.cloneStdout", func() { _, _ = io.Copy(io.Discard, p.Stdout()) })
	tail := &proc.CapWriter{Max: 1 << 20}
	done := make(chan struct{})
	safe.Go("core.cloneProgress", func() {
		defer close(done)
		sc := bufio.NewScanner(p.Stderr())
		sc.Buffer(make([]byte, 0, 4096), 64<<10)
		sc.Split(splitProgress)
		lastPhase, lastPct, lastAt := "", -1, time.Time{}
		for sc.Scan() {
			line := sc.Text()
			phase, pct, ok := parseCloneProgress(line)
			if !ok {
				if strings.TrimSpace(line) != "" {
					_, _ = tail.Write([]byte(line + "\n"))
				}
				continue
			}
			now := time.Now()
			if phase == lastPhase && (pct == lastPct || now.Sub(lastAt) < cloneProgressEvery && pct < 100) {
				continue
			}
			lastPhase, lastPct, lastAt = phase, pct, now
			c.publish(context.WithoutCancel(ctx), "github.clone_progress", "", map[string]any{"repo": fullName, "phase": phase, "percent": pct})
		}
		_, _ = io.Copy(io.Discard, p.Stderr())
	})
	var ei proc.ExitInfo
	select {
	case ei = <-p.Wait():
	case <-ctx.Done():
		_ = p.Kill(true)
		ei = <-p.Wait()
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second): // a grandchild holding the pipe (see proc.Harden's pipeGrace)
	}
	if errors.Is(context.Cause(ctx), errCloneCancelled) {
		return tail.String(), errCloneCancelled
	}
	if ctx.Err() != nil {
		return tail.String(), ctx.Err()
	}
	if ei.Err != nil {
		return tail.String(), ei.Err
	}
	if ei.Code != 0 {
		return tail.String(), errors.New("exit status " + strconv.Itoa(ei.Code))
	}
	return tail.String(), nil
}

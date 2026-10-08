package checks

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/proc"
)

// This file is the `preview` check kind (brief section 13a, decision D6): start the project's own preview command under the
// process manager, wait until a URL answers, and report that URL (the Preview tool member posts it). Production
// deployment is never a check: a command that looks like one is refused before it starts. The refusal is lexical
// (a worktree and a command allowlist are not a sandbox, security.md); the starter's own preview script is what enforces
// the account separation of D6.

// ErrPreviewDeploy is returned for a command that looks like a production deployment.
var ErrPreviewDeploy = errors.New("checks: refusing to run a command that looks like a production deployment as a preview")

// PreviewOptions configures StartPreview.
type PreviewOptions struct {
	// Enabled is the per-project setting; false yields a skipped result.
	Enabled bool
	// Command overrides detection (argv, no shell), from the per-project settings.
	Command []string
	// HealthURL is probed until it answers. Empty: the first http(s) URL the command prints is probed instead (a preview
	// deployment command prints where it deployed).
	HealthURL string
	// Timeout bounds the wait for the URL to answer (default 90 s).
	Timeout time.Duration
	LogDir  string
	Env     []string
	// HTTP overrides the probe client (tests).
	HTTP *http.Client
}

// PreviewResult is the outcome of StartPreview.
type PreviewResult struct {
	Result
	URL    string `json:"url,omitempty"`
	Source string `json:"source,omitempty"`
}

// PreviewHandle is a running preview. Stop kills its process tree; it is idempotent.
type PreviewHandle struct {
	URL    string
	PID    int
	LogRef string
	p      proc.Proc
	done   <-chan proc.ExitInfo
	once   sync.Once
	exited chan struct{}
}

// Stop ends the preview and its descendants.
func (h *PreviewHandle) Stop() {
	if h == nil {
		return
	}
	h.once.Do(func() {
		if h.p != nil {
			_ = h.p.Kill(true)
		}
	})
}

// Exited is closed when the preview process has ended.
func (h *PreviewHandle) Exited() <-chan struct{} { return h.exited }

// DetectPreview finds the project's preview command: a Taskfile target `preview` (when go-task is installed) or a
// package.json script `preview`. Nothing is guessed beyond that.
func DetectPreview(dir string) (argv []string, source string, ok bool) {
	if tf := e2eTaskfileTargets(dir); tf["preview"] {
		if _, err := exec.LookPath("task"); err == nil {
			return []string{"task", "preview"}, "taskfile:preview", true
		}
	}
	if packageScripts(dir)["preview"] {
		return []string{"npm", "run", "preview", "--silent"}, "package.json:preview", true
	}
	return nil, "", false
}

var deployWords = map[string]bool{"deploy": true, "publish": true, "release": true}

// RefusePreviewCommand returns ErrPreviewDeploy when any argument is a deployment verb or targets a production
// environment. It is a guard against configuration mistakes, not a security boundary.
func RefusePreviewCommand(argv []string) error {
	for i, a := range argv {
		l := strings.ToLower(strings.TrimSpace(a))
		l = strings.TrimLeft(l, "-")
		if deployWords[l] {
			return fmt.Errorf("%w: %q", ErrPreviewDeploy, a)
		}
		if (l == "env" || l == "environment") && i+1 < len(argv) && strings.EqualFold(argv[i+1], "production") {
			return fmt.Errorf("%w: --env production", ErrPreviewDeploy)
		}
		if strings.Contains(l, "env=production") || strings.Contains(l, "environment=production") {
			return fmt.Errorf("%w: %q", ErrPreviewDeploy, a)
		}
	}
	return nil
}

var urlRE = regexp.MustCompile(`https?://[^\s"'<>)\]]+`)

// StartPreview starts the preview command and waits for its URL to answer. On success the returned handle owns the
// running process: the caller stops it. A disabled or undefined preview is a skipped result with a nil handle.
func StartPreview(ctx context.Context, pm proc.Manager, dir string, o PreviewOptions, onLine func(string)) (*PreviewHandle, PreviewResult, error) {
	res := PreviewResult{}
	res.Name = "preview"
	skip := func(reason string) (*PreviewHandle, PreviewResult, error) {
		res.Result = SkippedResult("preview", reason)
		return nil, res, nil
	}
	if !o.Enabled {
		return skip("preview is switched off in this project's settings")
	}
	argv, source := o.Command, "settings"
	if len(argv) == 0 {
		var ok bool
		if argv, source, ok = DetectPreview(dir); !ok {
			return skip("the project defines no preview command (a Taskfile target or package.json script named preview, or the preview.command setting)")
		}
	}
	res.Source = source
	if err := RefusePreviewCommand(argv); err != nil {
		return nil, res, err
	}
	if o.HealthURL != "" {
		if err := checkProbeURL(o.HealthURL); err != nil {
			return nil, res, err
		}
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	logDir := o.LogDir
	if logDir == "" {
		d, err := os.MkdirTemp("", "drydock-preview-*")
		if err != nil {
			return nil, res, err
		}
		logDir = d
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, res, err
	}
	logPath := filepath.Join(logDir, fmt.Sprintf("preview-%d.log", time.Now().UnixNano()))
	logf, err := os.Create(logPath)
	if err != nil {
		return nil, res, err
	}
	env := append(os.Environ(), o.Env...)
	env = append(env, "CI=1", "NO_COLOR=1", "FORCE_COLOR=0")
	// The process outlives this call, so it is not tied to ctx; ctx only bounds the wait.
	p, err := pm.Start(context.Background(), proc.Spec{Path: argv[0], Args: argv[1:], Dir: dir, Env: env, Label: "preview"})
	if err != nil {
		logf.Close()
		return nil, res, err
	}
	h := &PreviewHandle{PID: p.PID(), LogRef: logPath, p: p, done: p.Wait(), exited: make(chan struct{})}
	var mu sync.Mutex
	var out strings.Builder
	found := make(chan string, 1)
	var wg sync.WaitGroup
	pump := func(r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			line := sc.Text()
			mu.Lock()
			fmt.Fprintln(logf, line)
			if out.Len() < 64<<10 {
				out.WriteString(line + "\n")
			}
			mu.Unlock()
			if onLine != nil {
				onLine(line)
			}
			if m := urlRE.FindString(line); m != "" {
				select {
				case found <- strings.TrimRight(m, ".,;"):
				default:
				}
			}
		}
	}
	wg.Add(2)
	go pump(p.Stdout())
	go pump(p.Stderr())
	_ = p.Stdin().Close()
	var exit proc.ExitInfo
	exitCh := make(chan struct{})
	go func() {
		exit = <-h.done
		wg.Wait()
		logf.Close()
		close(h.exited)
		close(exitCh)
	}()

	fail := func(why string) (*PreviewHandle, PreviewResult, error) {
		h.Stop()
		select {
		case <-exitCh:
		case <-time.After(5 * time.Second):
		}
		mu.Lock()
		tail := out.String()
		mu.Unlock()
		res.Result.Name = "preview"
		res.ExitCode = 1
		res.LogRef = logPath
		res.Output = Truncate(why+"\n"+tail, 6000, logPath)
		return nil, res, nil
	}

	client := o.HTTP
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	target := o.HealthURL
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	exited := false
	for {
		if target == "" {
			select {
			case u := <-found:
				if err := checkProbeURL(u); err == nil {
					target = u
				}
			default:
			}
		}
		if target != "" {
			if probeOK(ctx, client, target) {
				res.URL, res.Passed, res.LogRef = target, true, logPath
				res.Output = "preview ready at " + target
				h.URL = target
				return h, res, nil
			}
		}
		if exited {
			// a deployment-style command prints its URL and exits 0: that is a preview that is already up
			if target != "" && exit.Code == 0 {
				return fail("the preview command exited before " + target + " answered")
			}
			if exit.Code != 0 {
				return fail(fmt.Sprintf("the preview command exited with code %d before any URL answered", exit.Code))
			}
			return fail("the preview command exited without printing a URL; set preview.url in the project settings")
		}
		select {
		case <-ctx.Done():
			h.Stop()
			<-exitCh
			return nil, res, ctx.Err()
		case <-exitCh:
			exited = true
			// one last chance to read a URL that was printed just before the exit
			if target == "" {
				select {
				case u := <-found:
					if checkProbeURL(u) == nil {
						target = u
					}
				default:
				}
			}
			if target != "" && probeOK(ctx, client, target) {
				// the process is gone but the URL answers (a remote preview): hand back a handle with nothing to stop
				res.URL, res.Passed, res.LogRef = target, true, logPath
				res.Output = "preview ready at " + target + " (the command exited; the URL is served elsewhere)"
				h.URL = target
				return h, res, nil
			}
		case <-deadline.C:
			why := fmt.Sprintf("no answer from %s within %s", firstNonEmptyStr(target, "any printed URL"), timeout)
			return fail(why)
		case <-tick.C:
		}
	}
}

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func checkProbeURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("checks: preview URL %q is not an http(s) URL", raw)
	}
	return nil
}

func probeOK(ctx context.Context, c *http.Client, target string) bool {
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, target, nil)
	if err != nil {
		return false
	}
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

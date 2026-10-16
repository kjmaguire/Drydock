package winverify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kjmaguire/drydock/internal/safe"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The core smoke drives cmd/drydock-e2e (the real core with the development HTTP transport; its agent is the scripted
// fakeagent, a labelled test double) through the same calls the UI makes in ui/e2e/flow.spec.ts.

// coreSession is one running drydock-e2e, shared by the core-smoke, core-terminal and core-shutdown steps.
type coreSession struct {
	dir     string // this session's own folder: the uniquely named copy of the harness, its temp folder
	exe     string
	tmp     string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	base    string // http://127.0.0.1:port
	token   string
	repo    string // the fresh copy of the fixture repository the harness made
	project string // set once the smoke created its project
	exited  chan struct{}
	waitErr error
	out     *switchWriter
	http    *http.Client
}

// switchWriter sends the harness's output to the log of the step that is running (or nowhere between steps).
type switchWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *switchWriter) set(w io.Writer) {
	s.mu.Lock()
	s.w = w
	s.mu.Unlock()
}

func (s *switchWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w != nil {
		_, _ = s.w.Write(p)
	}
	return len(p), nil
}

// lineWriter calls fn with each complete line written to it.
type lineWriter struct {
	mu  sync.Mutex
	buf []byte
	fn  func(string)
}

func (l *lineWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		l.fn(strings.TrimRight(string(l.buf[:i]), "\r"))
		l.buf = l.buf[i+1:]
	}
}

// startCore copies the harness to a uniquely named program (so its processes, including the agents it starts, can be
// told apart from anything else running), starts it on a free loopback port with its own temp folder and a fresh copy of
// the fixture repository, and waits until it listens.
func startCore(ctx context.Context, env *Env, log io.Writer) (*coreSession, error) {
	src := filepath.Join(env.Bundle, env.Manifest.E2E)
	if _, err := os.Stat(src); err != nil {
		return nil, fmt.Errorf("the bundle has no %s", env.Manifest.E2E)
	}
	fixture := filepath.Join(env.Bundle, filepath.FromSlash(env.Manifest.Fixture))
	if _, err := os.Stat(fixture); err != nil {
		return nil, fmt.Errorf("the bundle has no fixture project at %s", env.Manifest.Fixture)
	}
	dir, err := os.MkdirTemp("", "drydock-verify-core-")
	if err != nil {
		return nil, err
	}
	s := &coreSession{dir: dir, tmp: filepath.Join(dir, "tmp"), exited: make(chan struct{}), out: &switchWriter{w: log},
		http: &http.Client{Transport: &http.Transport{Proxy: nil}}}
	s.exe = filepath.Join(dir, "drydock-e2e-verify-"+strconv.Itoa(os.Getpid())+exeSuffix(runtime.GOOS))
	ui := filepath.Join(dir, "ui")
	err = os.MkdirAll(s.tmp, 0o755)
	if err == nil {
		err = os.MkdirAll(ui, 0o755)
	}
	if err == nil {
		if err = copyFile(src, s.exe); err == nil {
			err = os.Chmod(s.exe, 0o755)
		}
	}
	if err != nil {
		s.cleanup(false)
		return nil, fmt.Errorf("prepare a copy of the core: %w", err)
	}
	set := map[string]string{"TMP": s.tmp, "TEMP": s.tmp, "TMPDIR": s.tmp, "DRYDOCK_NO_REAL_AGENTS": "1"}
	if runtime.GOOS != "windows" {
		set["SHELL"] = "/bin/sh" // the terminal check types POSIX shell syntax
	}
	s.cmd = exec.Command(s.exe, "-ui", ui, "-fixture", fixture, "-repos", "1", "-port", "0", "-exit-on-stdin-close")
	s.cmd.Dir = dir
	s.cmd.Env = env.childEnv(set)
	if s.stdin, err = s.cmd.StdinPipe(); err != nil {
		s.cleanup(false)
		return nil, err
	}
	ready := make(chan struct{})
	var once sync.Once
	s.cmd.Stdout = &lineWriter{fn: func(line string) {
		fmt.Fprintln(s.out, line) // the log redacts the token
		switch {
		case strings.HasPrefix(line, "DRYDOCK_E2E_REPO ") && s.repo == "":
			s.repo = strings.TrimPrefix(line, "DRYDOCK_E2E_REPO ")
		case strings.HasPrefix(line, "DRYDOCK_E2E "):
			if u, err := url.Parse(strings.TrimPrefix(line, "DRYDOCK_E2E ")); err == nil {
				s.base, s.token = u.Scheme+"://"+u.Host, u.Query().Get("token")
			}
			once.Do(func() { close(ready) })
		}
	}}
	s.cmd.Stderr = s.out
	s.cmd.WaitDelay = 10 * time.Second
	if err := s.cmd.Start(); err != nil {
		s.cleanup(false)
		return nil, fmt.Errorf("start %s: %w", filepath.Base(s.exe), err)
	}
	safe.Go("winverify.startCore", func() { s.waitErr = s.cmd.Wait(); close(s.exited) })
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	select {
	case <-ready:
		if s.base == "" || s.token == "" || s.repo == "" {
			_, _ = s.stop(ctx, 10*time.Second)
			s.cleanup(false)
			return nil, errors.New("the core started but did not print its address and a fixture repository")
		}
		return s, nil
	case <-s.exited:
		s.cleanup(false)
		return nil, fmt.Errorf("the core exited before it was ready (%v); its output is in the log", s.waitErr)
	case <-timer.C:
		_, _ = s.stop(ctx, 5*time.Second)
		s.cleanup(false)
		return nil, errors.New("the core did not start within 2 minutes")
	case <-ctx.Done():
		_, _ = s.stop(context.Background(), 10*time.Second)
		s.cleanup(false)
		return nil, ctx.Err()
	}
}

// rpcError is a refused call: the HTTP status and the core's own message.
type rpcError struct {
	Method string
	Status int
	Msg    string
}

func (e *rpcError) Error() string { return fmt.Sprintf("%s: HTTP %d: %s", e.Method, e.Status, e.Msg) }

// call makes one RPC exactly as ui/app/api/transport.ts does: POST /rpc/<method> with a JSON body and the token header;
// the reply is {"result": ...} or {"error": "..."}.
func (s *coreSession) call(ctx context.Context, method string, params, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if params == nil {
		params = map[string]any{}
	}
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/rpc/"+url.PathEscape(method), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Drydock-Token", s.token)
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	return decodeEnvelope(method, resp.StatusCode, body, out)
}

func decodeEnvelope(method string, status int, body []byte, out any) error {
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	jerr := json.Unmarshal(body, &env)
	if status != http.StatusOK {
		msg := env.Error
		if jerr != nil || msg == "" {
			msg = strings.TrimSpace(string(body))
		}
		return &rpcError{Method: method, Status: status, Msg: msg}
	}
	if jerr != nil {
		return fmt.Errorf("%s: the reply is not the core's JSON envelope: %v", method, jerr)
	}
	if env.Error != "" {
		return &rpcError{Method: method, Status: status, Msg: env.Error}
	}
	if out != nil && len(env.Result) > 0 && string(env.Result) != "null" {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("%s: unexpected reply: %v", method, err)
		}
	}
	return nil
}

// taskDetail is the part of the core's task.get reply the smoke reads (core.TaskDetail).
type taskDetail struct {
	Task struct {
		ID            string `json:"id"`
		Status        string `json:"status"`
		BlockedReason string `json:"blockedReason"`
		AcceptedBy    string `json:"acceptedBy"`
	} `json:"task"`
	Evidence []struct {
		Kind    string `json:"kind"`
		Passed  *bool  `json:"passed"`
		Summary string `json:"summary"`
	} `json:"evidence"`
	Reviews []struct {
		Verdict  string `json:"verdict"`
		Feedback string `json:"feedback"`
	} `json:"reviews"`
	Run *struct {
		ID           string `json:"id"`
		Status       string `json:"status"`
		FailedReason string `json:"failedReason"`
	} `json:"run"`
	Pending []struct {
		ID   string `json:"id"`
		Spec struct {
			Kind  string `json:"kind"`
			Title string `json:"title"`
		} `json:"spec"`
	} `json:"pending"`
}

func (d taskDetail) describe() string {
	var parts []string
	parts = append(parts, "task status "+d.Task.Status)
	if d.Task.BlockedReason != "" {
		parts = append(parts, "blocked: "+d.Task.BlockedReason)
	}
	if d.Run != nil {
		r := "run " + d.Run.Status
		if d.Run.FailedReason != "" {
			r += " (" + d.Run.FailedReason + ")"
		}
		parts = append(parts, r)
	}
	if len(d.Evidence) > 0 {
		parts = append(parts, "checks: "+d.checks())
	}
	return strings.Join(parts, "; ")
}

func (d taskDetail) checks() string {
	var cs []string
	for _, e := range d.Evidence {
		r := "no verdict"
		if e.Passed != nil {
			r = map[bool]string{true: "pass", false: "fail"}[*e.Passed]
		}
		cs = append(cs, e.Kind+" "+r)
	}
	return strings.Join(cs, ", ")
}

// waitTask polls task.get until done says so, the task ends, or the time is up.
func (s *coreSession) waitTask(ctx context.Context, id string, limit time.Duration, done func(taskDetail) bool) (taskDetail, error) {
	deadline := time.Now().Add(limit)
	var d taskDetail
	for {
		if err := s.call(ctx, "task.get", map[string]string{"id": id}, &d); err != nil {
			return d, err
		}
		if done(d) {
			return d, nil
		}
		switch d.Task.Status {
		case "FAILED", "CANCELLED", "COMPLETED":
			return d, fmt.Errorf("the task ended early: %s", d.describe())
		}
		if time.Now().After(deadline) {
			return d, fmt.Errorf("gave up after %s: %s", limit, d.describe())
		}
		select {
		case <-ctx.Done():
			return d, ctx.Err()
		case <-s.exited:
			return d, fmt.Errorf("the core exited while the task ran (%v)", s.waitErr)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// runTaskLoop is ui/e2e/flow.spec.ts without the browser: project, worker and reviewer, a task to implement Subtract,
// start, the review request, approve, completed, and the change merged into the repository.
func (s *coreSession) runTaskLoop(ctx context.Context, note func(string, ...any)) error {
	var info struct {
		Name string `json:"name"`
	}
	if err := s.call(ctx, "app.info", nil, &info); err != nil {
		return err
	}
	if info.Name != "Drydock" {
		return fmt.Errorf("app.info answered %q, not Drydock", info.Name)
	}
	var p struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		DefaultBranch string `json:"defaultBranch"`
	}
	if err := s.call(ctx, "project.create", map[string]any{"name": "gocalc-verify", "repoPath": s.repo}, &p); err != nil {
		return err
	}
	if p.ID == "" {
		return errors.New("project.create returned no project id")
	}
	s.project = p.ID
	note("project %s created for a fresh copy of the gocalc fixture (default branch %s)", p.Name, p.DefaultBranch)

	type agent struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var worker, reviewer agent
	if err := s.call(ctx, "agent.create", map[string]any{"projectId": p.ID, "name": "Worker One", "adapter": "claude", "role": "backend"}, &worker); err != nil {
		return err
	}
	if err := s.call(ctx, "agent.create", map[string]any{"projectId": p.ID, "name": "Reviewer One", "adapter": "claude", "role": "reviewer"}, &reviewer); err != nil {
		return err
	}
	note("agents %s (backend) and %s (reviewer) created; both run the scripted test-double agent through the claude adapter", worker.Name, reviewer.Name)

	var task struct {
		ID string `json:"id"`
	}
	if err := s.call(ctx, "task.create", map[string]any{"projectId": p.ID, "title": "Implement Subtract",
		"description": "Subtract returns 0; make it return a - b.", "acceptance": []string{"Given 5 and 3 When Subtract is called Then 2"}}, &task); err != nil {
		return err
	}
	if err := s.call(ctx, "task.assign", map[string]any{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, nil); err != nil {
		return err
	}
	var runID string
	if err := s.call(ctx, "task.start", map[string]any{"id": task.ID}, &runID); err != nil {
		return err
	}
	note("task %s assigned and started (run %s)", task.ID, runID)

	started := time.Now()
	d, err := s.waitTask(ctx, task.ID, 5*time.Minute, func(d taskDetail) bool { return len(d.Pending) > 0 })
	if err != nil {
		return fmt.Errorf("waiting for the human review request: %w", err)
	}
	if k := d.Pending[0].Spec.Kind; k != "review" {
		return fmt.Errorf("the run asked the human for %q, not for a review: %s", k, d.Pending[0].Spec.Title)
	}
	note("the review request reached the human after %s (%s)", time.Since(started).Round(100*time.Millisecond), d.describe())
	testPassed := false
	for _, e := range d.Evidence {
		if e.Kind == "test" && e.Passed != nil && *e.Passed {
			testPassed = true
		}
	}
	if !testPassed {
		return fmt.Errorf("no passing test check in the evidence (%s): the fixture's own go test did not run or did not pass", d.checks())
	}
	if len(d.Reviews) == 0 || d.Reviews[len(d.Reviews)-1].Verdict != "SHIP" {
		return fmt.Errorf("the independent reviewer did not answer SHIP: %+v", d.Reviews)
	}
	var diff string
	if err := s.call(ctx, "task.diff", map[string]any{"id": task.ID}, &diff); err != nil {
		return err
	}
	if !strings.Contains(diff, "return a - b") {
		return fmt.Errorf("the task's diff does not hold the worker's change:\n%s", lastLines(diff, 20, 2000))
	}
	note("the reviewer answered SHIP and the diff holds the worker's change (return a - b)")

	if err := s.call(ctx, "run.respond", map[string]any{"runId": runID, "requestId": d.Pending[0].ID, "decision": "approved", "data": map[string]any{}}, nil); err != nil {
		return err
	}
	approved := time.Now()
	d, err = s.waitTask(ctx, task.ID, 3*time.Minute, func(d taskDetail) bool { return d.Task.Status == "COMPLETED" })
	if err != nil {
		return fmt.Errorf("after the approval: %w", err)
	}
	note("approved as the human; the task is COMPLETED after %s (accepted by %s)", time.Since(approved).Round(100*time.Millisecond), d.Task.AcceptedBy)
	b, err := os.ReadFile(filepath.Join(s.repo, "calc.go"))
	if err != nil {
		return fmt.Errorf("read the merged file: %w", err)
	}
	if !strings.Contains(string(b), "return a - b") {
		return fmt.Errorf("the merge did not land: calc.go in the repository is still:\n%s", b)
	}
	note("calc.go in the project's repository now holds the change: the merge landed")
	var needs []json.RawMessage
	if err := s.call(ctx, "needsyou.list", map[string]any{"projectId": p.ID}, &needs); err != nil {
		return err
	}
	if len(needs) != 0 {
		return fmt.Errorf("Needs you still lists %d item(s) after the approval", len(needs))
	}
	note("Needs you is empty again")
	return nil
}

// terminalProbe is the command typed into the shell and what its output must contain. The sum is printed only if the
// shell really ran the command: the typed text itself does not contain it.
func terminalProbe(shell string) (command, want string) {
	const sum = "8534567"
	s := strings.ToLower(shell)
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		s = s[i+1:]
	}
	switch {
	case strings.Contains(s, "pwsh") || strings.Contains(s, "powershell"):
		return "Write-Output (7300000+1234567)", sum
	case strings.HasPrefix(s, "cmd"):
		return "set /a 7300000+1234567", sum
	default:
		return "echo $((7300000+1234567))", sum
	}
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-Za-z]|\x1b[@-Z\\-_]`)

// stripANSI removes terminal escape sequences (colours, cursor moves, titles) so the text can be searched.
func stripANSI(s string) string { return ansiEscape.ReplaceAllString(s, "") }

// terminalCheck opens a terminal in the project, types the probe and reads until its answer appears.
func (s *coreSession) terminalCheck(ctx context.Context, note func(string, ...any)) error {
	var info struct {
		ID    string `json:"id"`
		Shell string `json:"shell"`
		Cwd   string `json:"cwd"`
	}
	if err := s.call(ctx, "terminal.open", map[string]any{"projectId": s.project, "cols": 100, "rows": 30}, &info); err != nil {
		return err
	}
	note("terminal %s opened: %s in %s", info.ID, info.Shell, info.Cwd)
	closed := false
	defer func() {
		if !closed {
			_ = s.call(context.Background(), "terminal.close", map[string]any{"id": info.ID}, nil)
		}
	}()
	var screen strings.Builder
	var from int64
	read := func(wait time.Duration) (bool, int, error) {
		var c struct {
			Data     []byte `json:"data"`
			Next     int64  `json:"next"`
			Exited   bool   `json:"exited"`
			ExitCode int    `json:"exitCode"`
		}
		if err := s.call(ctx, "terminal.read", map[string]any{"id": info.ID, "from": from, "waitMs": wait.Milliseconds()}, &c); err != nil {
			return false, 0, err
		}
		screen.Write(c.Data)
		from = c.Next
		return c.Exited, c.ExitCode, nil
	}
	// let the shell start (its first output is the prompt); type even if it printed nothing within 20 seconds
	deadline := time.Now().Add(20 * time.Second)
	for screen.Len() == 0 && time.Now().Before(deadline) {
		if exited, code, err := read(2 * time.Second); err != nil {
			return err
		} else if exited {
			return fmt.Errorf("the shell exited with code %d before it was used; it printed: %q", code, stripANSI(screen.String()))
		}
	}
	command, want := terminalProbe(info.Shell)
	if err := s.call(ctx, "terminal.write", map[string]any{"id": info.ID, "data": command + "\r"}, nil); err != nil {
		return err
	}
	note("typed: %s", command)
	deadline = time.Now().Add(60 * time.Second)
	for !strings.Contains(stripANSI(screen.String()), want) {
		if time.Now().After(deadline) {
			return fmt.Errorf("the shell did not print %s within 60 seconds; the terminal showed:\n%s", want, lastLines(stripANSI(screen.String()), 15, 2000))
		}
		exited, code, err := read(2 * time.Second)
		if err != nil {
			return err
		}
		if exited && !strings.Contains(stripANSI(screen.String()), want) {
			return fmt.Errorf("the shell exited with code %d without printing %s; it printed:\n%s", code, want, lastLines(stripANSI(screen.String()), 15, 2000))
		}
	}
	note("the shell printed %s, so it ran the command on the core's pseudo-terminal", want)
	closed = true
	if err := s.call(ctx, "terminal.close", map[string]any{"id": info.ID}, nil); err != nil {
		return err
	}
	note("terminal closed")
	return nil
}

// stop closes the harness's input, which makes it shut down cleanly, and kills it if it has not exited in time. It
// reports whether it stopped by itself.
func (s *coreSession) stop(ctx context.Context, wait time.Duration) (bool, error) {
	select {
	case <-s.exited:
		return true, nil
	default:
	}
	_ = s.stdin.Close()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-s.exited:
		return true, nil
	case <-timer.C:
	case <-ctx.Done():
	}
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	select {
	case <-s.exited:
	case <-time.After(30 * time.Second):
		return false, errors.New("the core could not be killed")
	}
	return false, nil
}

// cleanup removes the session's folder (unless kept for debugging).
func (s *coreSession) cleanup(keep bool) {
	if !keep {
		removeAllRetry(s.dir)
	}
}

func coreSmokeStep() Step {
	return Step{
		ID:    "core-smoke",
		Title: "The core runs a whole task",
		Proves: "The real Drydock core runs a whole task on this machine: a project on a fresh Git repository, a worker and a reviewer (the scripted " +
			"test-double agent), the project's own checks, the independent review, the human approval and the merge into the repository.",
		Run: func(ctx context.Context, sc *StepContext) Outcome {
			env := sc.Env
			if _, err := exec.LookPath("git"); err != nil {
				return Outcome{Status: Skip, Reason: "git is not on PATH: the core creates real repositories and worktrees (install Git for Windows)"}
			}
			if !env.HasGo() {
				return Outcome{Status: Skip, Reason: "no Go is available: the fixture project's checks are go build, go test and go vet, which the core runs as real commands"}
			}
			started := time.Now()
			s, err := startCore(ctx, env, sc.Log)
			if err != nil {
				return Outcome{Status: Fail, Reason: "the core did not start: " + err.Error()}
			}
			env.core = s
			defer s.out.set(nil)
			var notes []string
			note := func(format string, a ...any) {
				line := fmt.Sprintf(format, a...)
				notes = append(notes, line)
				sc.Logf("- %s", line)
			}
			note("the scripted core (cmd/drydock-e2e, test-double agent) is listening on loopback after %s", time.Since(started).Round(100*time.Millisecond))
			err = s.runTaskLoop(ctx, note)
			out := Outcome{Status: Pass, Excerpt: strings.Join(notes, "\n")}
			if err != nil {
				out.Status, out.Reason = Fail, err.Error()
			}
			return out
		},
	}
}

func coreTerminalStep() Step {
	return Step{
		ID:    "core-terminal",
		Title: "A terminal through the core",
		Proves: "A terminal opened through the core starts this system's shell on a pseudo-terminal (ConPTY on Windows), runs a typed command " +
			"and returns its output.",
		Run: func(ctx context.Context, sc *StepContext) Outcome {
			s := sc.Env.core
			if s == nil {
				return Outcome{Status: Skip, Reason: "the core is not running: core-smoke did not start it (see that step)"}
			}
			if s.project == "" {
				return Outcome{Status: Skip, Reason: "core-smoke did not get as far as creating a project, which a terminal opens in"}
			}
			s.out.set(sc.Log)
			defer s.out.set(nil)
			var notes []string
			note := func(format string, a ...any) {
				line := fmt.Sprintf(format, a...)
				notes = append(notes, line)
				sc.Logf("- %s", line)
			}
			out := Outcome{Status: Pass}
			if err := s.terminalCheck(ctx, note); err != nil {
				out.Status, out.Reason = Fail, err.Error()
			}
			out.Excerpt = strings.Join(notes, "\n")
			return out
		},
	}
}

func coreShutdownStep() Step {
	return Step{
		ID:    "core-shutdown",
		Title: "Nothing is left running",
		Proves: "When the core stops, nothing it started keeps running: no agent process (the scripted test double) and no other copy of the " +
			"program is left behind.",
		Run: func(ctx context.Context, sc *StepContext) Outcome {
			env := sc.Env
			s := env.core
			if s == nil {
				return Outcome{Status: Skip, Reason: "the core was not started (see core-smoke)"}
			}
			env.core = nil
			s.out.set(sc.Log)
			defer s.cleanup(env.Keep)
			var notes []string
			note := func(format string, a ...any) {
				line := fmt.Sprintf(format, a...)
				notes = append(notes, line)
				sc.Logf("- %s", line)
			}
			start := time.Now()
			clean, err := s.stop(ctx, 60*time.Second)
			s.out.set(nil)
			if err != nil {
				return Outcome{Status: Fail, Reason: err.Error()}
			}
			out := Outcome{Status: Pass}
			if clean {
				note("the core stopped by itself %s after its input closed (exit: %v)", time.Since(start).Round(100*time.Millisecond), exitText(s.waitErr))
			} else {
				out.Status, out.Reason = Fail, "the core did not stop within 60 seconds after its input closed, so it was killed"
				note("%s", out.Reason)
			}
			// processes can take a moment to go once their job or parent is gone
			var left []RunningProcess
			for i := 0; i < 20; i++ {
				left, err = RunningInstances(ctx, s.exe)
				if err != nil || len(left) == 0 {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			if err != nil {
				return Outcome{Status: Fail, Reason: "could not list the running processes: " + err.Error(), Excerpt: strings.Join(notes, "\n")}
			}
			if len(left) > 0 {
				var lines []string
				for _, p := range left {
					lines = append(lines, p.Line)
					if pr, err := os.FindProcess(p.PID); err == nil {
						_ = pr.Kill() // do not leave them on the machine
					}
				}
				out.Status = Fail
				out.Reason = fmt.Sprintf("%d process(es) of the core were still running after it stopped (now killed): %s", len(left), strings.Join(lines, "; "))
				note("%s", out.Reason)
			} else {
				note("no process of %s is running (checked with %s)", filepath.Base(s.exe), map[bool]string{true: "tasklist", false: "ps"}[runtime.GOOS == "windows"])
			}
			// the harness removes its own folder (data, repositories) when it stops cleanly; what tools it ran left behind
			// (Claude Code's probe folders) goes with this session's folder
			if left, _ := filepath.Glob(filepath.Join(s.tmp, "drydock-e2e-*")); len(left) > 0 {
				note("note: the core's own temp folder was not removed when it stopped (a file in it may still have been in use)")
			}
			out.Excerpt = strings.Join(notes, "\n")
			return out
		},
	}
}

func exitText(err error) string {
	if err == nil {
		return "code 0"
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return "code " + strconv.Itoa(ee.ExitCode())
	}
	return err.Error()
}

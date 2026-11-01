package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/proc"
)

// ExecRequest is one command to run in the workspace.
type ExecRequest struct {
	// Command is a shell command line, run with sh -c in the working folder.
	Command string
	// Dir is a folder relative to the working folder ("" = the working folder itself).
	Dir string
	// Timeout ends the command and everything it started (default 10 minutes, at most 60).
	Timeout time.Duration
	// Env adds KEY=VALUE pairs for this command only.
	Env []string
	// Output, if set, also receives the whole output as it comes (an ACP terminal reads it while the command runs, decision
	// P131); the result keeps its head and tail as always.
	Output io.Writer
}

// ExecResult is how a command ended.
type ExecResult struct {
	ExitCode  int           `json:"exitCode"`
	Output    string        `json:"output"` // combined stdout and stderr, head and tail when long
	Bytes     int64         `json:"bytes"`  // the whole output's size
	Truncated bool          `json:"truncated"`
	Duration  time.Duration `json:"duration"`
	TimedOut  bool          `json:"timedOut"`
	LogPath   string        `json:"logPath,omitempty"` // the full output on the human's machine
}

// Output limits: what an agent sees of one command.
const (
	headBytes      = 8 << 10
	tailBytes      = 24 << 10
	DefaultTimeout = 10 * time.Minute
	MaxTimeout     = 60 * time.Minute
)

// Exec runs a command in the box's workspace and waits for it.
func (m *Manager) Exec(ctx context.Context, b *Box, r ExecRequest) (ExecResult, error) {
	return m.exec(ctx, b, r, "cmd")
}

func (m *Manager) exec(ctx context.Context, b *Box, r ExecRequest, kind string) (ExecResult, error) {
	if strings.TrimSpace(r.Command) == "" {
		return ExecResult{}, errors.New("sandbox: empty command")
	}
	if len(r.Command) > 16000 || strings.ContainsRune(r.Command, 0) {
		return ExecResult{}, errors.New("sandbox: the command is longer than 16000 characters or contains a NUL")
	}
	dir, err := containerDir(r.Dir)
	if err != nil {
		return ExecResult{}, err
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if timeout > MaxTimeout {
		timeout = MaxTimeout
	}
	secs := int(timeout.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	script := `exec sh -c "$DRYDOCK_CMD"`
	if b.hasTimeout {
		// timeout puts the command in a process group of its own and ends the whole group, so nothing it started lives on
		script = `exec timeout -k 5 ` + strconv.Itoa(secs) + ` sh -c "$DRYDOCK_CMD"`
	}
	args := []string{"exec", "-w", dir, "-e", "DRYDOCK_CMD=" + r.Command}
	for _, kv := range r.Env {
		if k, _, ok := strings.Cut(kv, "="); !ok || !envKeyRe.MatchString(k) {
			return ExecResult{}, fmt.Errorf("sandbox: bad environment entry %q", kv)
		}
		args = append(args, "-e", kv)
	}
	args = append(args, b.Workspace, "sh", "-c", script)

	capture := &proc.HeadTail{Head: headBytes, Tail: tailBytes}
	var sink io.Writer = capture
	var logPath string
	if m.LogDir != "" {
		m.mu.Lock()
		m.seq++
		n := m.seq
		m.mu.Unlock()
		d := filepath.Join(m.LogDir, BaseName(b.Spec.TaskID))
		if os.MkdirAll(d, 0o700) == nil {
			logPath = filepath.Join(d, fmt.Sprintf("%s-%d-%s.log", kind, n, time.Now().UTC().Format("20060102T150405")))
			if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
				defer f.Close()
				fmt.Fprintf(f, "$ %s\n", r.Command)
				sink = io.MultiWriter(capture, &limitedFile{f: f, max: maxLogBytes})
			} else {
				logPath = ""
			}
		}
	}
	if r.Output != nil {
		sink = io.MultiWriter(sink, r.Output)
	}
	// the client gets a little longer than the command, so the container-side timeout is what normally ends it
	cctx, cancel := context.WithTimeout(ctx, timeout+30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, m.Engine.Bin, args...)
	proc.Harden(cmd)
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = sink, sink
	start := time.Now()
	runErr := cmd.Run()
	res := ExecResult{Duration: time.Since(start), LogPath: logPath}
	res.Output, res.Bytes, res.Truncated = capture.Text()
	var ee *exec.ExitError
	switch {
	case runErr == nil:
	case cctx.Err() != nil:
		// the client was stopped (the turn was interrupted, or the image has no timeout command): end everything the command
		// started by restarting the workspace; the files and the services stay
		_, _ = m.Engine.Run(context.WithoutCancel(ctx), nil, "restart", "-t", "2", b.Workspace)
		res.ExitCode, res.TimedOut = -1, ctx.Err() == nil
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
	case errors.As(runErr, &ee):
		res.ExitCode = ee.ExitCode()
	default:
		return res, runErr
	}
	if b.hasTimeout && (res.ExitCode == 124 || res.ExitCode == 137) && res.Duration >= timeout-time.Second {
		res.TimedOut = true
	}
	return res, nil
}

// Format renders a result for an agent.
func (r ExecResult) Format(command string) string {
	var sb strings.Builder
	switch {
	case r.TimedOut:
		fmt.Fprintf(&sb, "Timed out after %s and was stopped (with everything it started).\n", r.Duration.Round(time.Second))
	default:
		fmt.Fprintf(&sb, "Exit code %d after %s.\n", r.ExitCode, r.Duration.Round(time.Millisecond))
	}
	if r.Bytes == 0 {
		sb.WriteString("(no output)\n")
		return sb.String()
	}
	sb.WriteString(r.Output)
	if !strings.HasSuffix(r.Output, "\n") {
		sb.WriteString("\n")
	}
	if r.Truncated {
		fmt.Fprintf(&sb, "[output shortened: %d bytes in all; the first %d and the last %d are shown]\n", r.Bytes, headBytes, tailBytes)
	}
	return sb.String()
}

// maxLogBytes is the most of one command's output that is written to its log file; the model is told when it is more.
const maxLogBytes = 64 << 20

// limitedFile writes to a log until it holds max bytes, then drops the rest: a command that prints without end must not fill the
// disk (audit 2026-10-16, M6). Its writes always succeed, so the command is not slowed or ended by it.
type limitedFile struct {
	f    *os.File
	max  int64
	n    int64
	full bool
}

func (l *limitedFile) Write(p []byte) (int, error) {
	if l.full {
		return len(p), nil
	}
	room := l.max - l.n
	if int64(len(p)) > room {
		if room > 0 {
			_, _ = l.f.Write(p[:room])
		}
		l.n, l.full = l.max, true
		_, _ = fmt.Fprintf(l.f, "\n[the log stops here: it holds the first %d MiB of the output]\n", l.max>>20)
		return len(p), nil
	}
	n, err := l.f.Write(p)
	l.n += int64(n)
	if err != nil {
		l.full = true // a log that cannot be written is given up; the command carries on
	}
	return len(p), nil
}

// ---- background processes (a dev server, a watcher) ----

var bgNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

const bgDir = "/tmp/.drydock-bg"

// Start runs a command in the background under a name. Its output goes to a log read with Logs; Stop ends it.
func (m *Manager) Start(ctx context.Context, b *Box, name, command, dir string) (string, error) {
	if !bgNameRe.MatchString(name) {
		return "", fmt.Errorf("sandbox: name %q: use lower-case letters, digits and dashes", name)
	}
	if strings.TrimSpace(command) == "" || len(command) > 16000 || strings.ContainsRune(command, 0) {
		return "", errors.New("sandbox: the command is empty, too long or contains a NUL")
	}
	cdir, err := containerDir(dir)
	if err != nil {
		return "", err
	}
	pid, log := bgDir+"/"+name+".pid", bgDir+"/"+name+".log"
	script := `mkdir -p ` + bgDir + ` && if [ -f ` + pid + ` ] && kill -0 "$(cat ` + pid + `)" 2>/dev/null; then echo "already running"; exit 3; fi; ` +
		`if command -v setsid >/dev/null 2>&1; then setsid sh -c "$DRYDOCK_CMD" > ` + log + ` 2>&1 < /dev/null & else sh -c "$DRYDOCK_CMD" > ` + log + ` 2>&1 < /dev/null & fi; ` +
		`echo $! > ` + pid + `; echo "started $(cat ` + pid + `)"`
	out, err := m.Engine.Run(ctx, nil, "exec", "-w", cdir, "-e", "DRYDOCK_CMD="+command, b.Workspace, "sh", "-c", script)
	if err != nil {
		if strings.Contains(out, "already running") || strings.Contains(err.Error(), "already running") {
			return "", fmt.Errorf("%s is already running: stop it first or pick another name", name)
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Logs returns the end of a background process's output (at most max bytes) and whether it is still running.
func (m *Manager) Logs(ctx context.Context, b *Box, name string, max int) (string, bool, error) {
	if !bgNameRe.MatchString(name) {
		return "", false, fmt.Errorf("sandbox: no background process %q", name)
	}
	if max <= 0 || max > 64<<10 {
		max = 8 << 10
	}
	pid, log := bgDir+"/"+name+".pid", bgDir+"/"+name+".log"
	out, err := m.Engine.Run(ctx, nil, "exec", b.Workspace, "sh", "-c",
		`[ -f `+pid+` ] || { echo "no such process"; exit 4; }; if kill -0 "$(cat `+pid+`)" 2>/dev/null; then echo RUNNING; else echo STOPPED; fi; tail -c `+strconv.Itoa(max)+` `+log+` 2>/dev/null`)
	if err != nil {
		return "", false, fmt.Errorf("sandbox: no background process %q", name)
	}
	state, rest, _ := strings.Cut(out, "\n")
	return rest, strings.TrimSpace(state) == "RUNNING", nil
}

// Stop ends a background process and everything it started.
func (m *Manager) Stop(ctx context.Context, b *Box, name string) error {
	if !bgNameRe.MatchString(name) {
		return fmt.Errorf("sandbox: no background process %q", name)
	}
	pid := bgDir + "/" + name + ".pid"
	_, err := m.Engine.Run(ctx, nil, "exec", b.Workspace, "sh", "-c",
		`[ -f `+pid+` ] || exit 4; p="$(cat `+pid+`)"; kill -TERM -"$p" 2>/dev/null || kill -TERM "$p" 2>/dev/null; `+
			`i=0; while kill -0 "$p" 2>/dev/null && [ $i -lt 50 ]; do sleep 0.1; i=$((i+1)); done; `+
			`kill -KILL -"$p" 2>/dev/null || kill -KILL "$p" 2>/dev/null; rm -f `+pid+`; exit 0`)
	if err != nil {
		return fmt.Errorf("sandbox: no background process %q", name)
	}
	return nil
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

// CommandArgs are the engine arguments that run argv (a command and its arguments, no shell) in the box, in hostDir (a folder
// of the task worktree), with env set, stopped after timeout with everything it started. Drydock's checks run this way when
// the project's sandbox is on, so the project's code never runs on the human's machine.
func (m *Manager) CommandArgs(b *Box, argv []string, hostDir string, env []string, timeout time.Duration) ([]string, error) {
	if len(argv) == 0 {
		return nil, errors.New("sandbox: empty command")
	}
	rel := "."
	if hostDir != "" {
		r, err := filepath.Rel(b.Spec.Worktree, hostDir)
		if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("sandbox: %s is not inside the task worktree", hostDir)
		}
		rel = filepath.ToSlash(r)
	}
	dir, err := containerDir(rel)
	if err != nil {
		return nil, err
	}
	quoted := make([]string, len(argv))
	for i, a := range argv {
		if strings.ContainsRune(a, 0) {
			return nil, errors.New("sandbox: an argument contains a NUL")
		}
		quoted[i] = shellQuote(a)
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	script := `exec sh -c "$DRYDOCK_CMD"`
	if b.hasTimeout {
		script = `exec timeout -k 5 ` + strconv.Itoa(int(timeout/time.Second)) + ` sh -c "$DRYDOCK_CMD"`
	}
	args := []string{"exec", "-w", dir, "-e", "DRYDOCK_CMD=" + strings.Join(quoted, " ")}
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); !ok || !envKeyRe.MatchString(k) {
			return nil, fmt.Errorf("sandbox: bad environment entry %q", kv)
		}
		args = append(args, "-e", kv)
	}
	return append(args, b.Workspace, "sh", "-c", script), nil
}

// ShellJoin makes one sh command line of a command and its arguments, each quoted to stay one word (an ACP terminal request
// gives them apart, decision P131).
func ShellJoin(argv []string) (string, error) {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return "", errors.New("sandbox: empty command")
	}
	quoted := make([]string, len(argv))
	for i, a := range argv {
		if strings.ContainsRune(a, 0) {
			return "", errors.New("sandbox: an argument contains a NUL")
		}
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " "), nil
}

// shellQuote makes s one word for sh.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./=:@,+%") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

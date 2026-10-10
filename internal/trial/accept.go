package trial

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// AcceptResult is what the hidden acceptance check said about one result (the run record's eval_accept).
type AcceptResult struct {
	Passed     bool            `json:"passed"`
	Commands   []CommandResult `json:"commands"`
	DurationMs int64           `json:"durationMs"`
	// Error is set when the harness could not run the check at all; Passed is then false.
	Error string `json:"error,omitempty"`
}

// CommandResult is one acceptance command.
type CommandResult struct {
	Command    string `json:"command"`
	ExitCode   int    `json:"exitCode"`
	TimedOut   bool   `json:"timedOut,omitempty"`
	DurationMs int64  `json:"durationMs"`
	// Output is the last part of what the command printed.
	Output string `json:"output"`
}

const outputTail = 6000

// Accept checks tree, the files an arm produced: it copies them (without .git) to a scratch directory, lays the task's hidden
// files over them and runs the acceptance commands there, stopping at the first failure. The arm's own tests run as part of
// `go test ./...`: a result whose own tests fail is not accepted either. The commands run with the Go toolchain on this
// machine, offline (GOTOOLCHAIN=local, GOFLAGS=-mod=mod, GOPROXY=off), the same way for both arms.
func Accept(ctx context.Context, root string, t Task, tree string) AcceptResult {
	start := time.Now()
	res := AcceptResult{}
	finish := func() AcceptResult {
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	dir, err := os.MkdirTemp("", "drydock-accept-*")
	if err != nil {
		res.Error = err.Error()
		return finish()
	}
	defer os.RemoveAll(dir)
	if err := copyTree(tree, dir, nil); err != nil {
		res.Error = "copy the result: " + err.Error()
		return finish()
	}
	if err := copyTree(filepath.Join(root, t.Acceptance.Files), dir, nil); err != nil {
		res.Error = "lay the hidden files over it: " + err.Error()
		return finish()
	}
	env := acceptEnv(dir)
	res.Passed = true
	for _, c := range t.Acceptance.Commands {
		cr := runCommand(ctx, dir, env, c, time.Duration(t.Acceptance.TimeoutSeconds)*time.Second)
		res.Commands = append(res.Commands, cr)
		if cr.ExitCode != 0 {
			res.Passed = false
			break
		}
	}
	return finish()
}

func acceptEnv(dir string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "GOFLAGS", "GOTOOLCHAIN", "GOPROXY", "GOWORK", "GOPATH", "GOCACHE", "CGO_ENABLED":
			continue
		}
		env = append(env, kv)
	}
	cache, _ := os.UserCacheDir()
	return append(env, "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local", "GOPROXY=off", "GOWORK=off", "CGO_ENABLED=0",
		"GOPATH="+filepath.Join(dir, ".gopath"), "GOCACHE="+filepath.Join(cache, "drydock-trial-gocache"))
}

func runCommand(ctx context.Context, dir string, env []string, command string, timeout time.Duration) CommandResult {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir, cmd.Env = dir, env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	cr := CommandResult{Command: command, DurationMs: time.Since(start).Milliseconds(), Output: tail(out.String(), outputTail)}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case ctx.Err() != nil:
		cr.ExitCode, cr.TimedOut = -1, true
	case errors.As(err, &exit):
		cr.ExitCode = exit.ExitCode()
	default:
		cr.ExitCode = -1
		cr.Output = tail(cr.Output+"\n"+err.Error(), outputTail)
	}
	return cr
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

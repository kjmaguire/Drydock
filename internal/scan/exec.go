package scan

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/safe"
)

// Tool is how an external tool is started: its executable (looked up when it is a bare name) and the arguments that always
// come first. The product leaves Args empty; the test harnesses start their labelled stand-ins through it.
type Tool struct {
	Path string
	Args []string
	// Chosen is true when Path is the file a person chose on the Security page.
	Chosen bool
	// Installed is true when Path is the program Drydock installed (decision P66).
	Installed bool
}

// resolve finds the executable and says how: "chosen", "installed", "path" or "folder" (FindTool). A Tool with no Path is
// the named default. look, when set, replaces the lookup (tests).
func (t Tool) resolve(name string, look func(string) (string, error)) (string, string, error) {
	p := t.Path
	if p == "" {
		p = name
	}
	if look != nil {
		path, err := look(p)
		return path, sourceOf(t), err
	}
	if t.Chosen || t.Installed {
		path, err := exec.LookPath(p) // an absolute path: is it there and runnable
		return path, sourceOf(t), err
	}
	return FindTool(p)
}

func sourceOf(t Tool) string {
	switch {
	case t.Chosen:
		return "chosen"
	case t.Installed:
		return "installed"
	}
	return "path"
}

// ExtraToolDirs lists the folders searched after PATH (tooldirs_*.go): on Windows the user's and the machine's PATH as they
// are now in the registry (so a tool installed while Drydock runs is found without a restart) and the folders WinGet, Scoop,
// Chocolatey and `go install` put programs in. A variable so tests can replace it.
var ExtraToolDirs = extraToolDirs

// FindTool finds a program by its bare name: on the PATH Drydock started with ("path"), then in ExtraToolDirs ("folder").
// A name with a folder in it is only checked where it points.
func FindTool(name string) (string, string, error) {
	path, err := exec.LookPath(name)
	if err == nil {
		return path, "path", nil
	}
	if strings.ContainsAny(name, `/\`) {
		return "", "", err
	}
	for _, dir := range ExtraToolDirs() {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		for _, cand := range programNames(name) {
			p := filepath.Join(dir, cand)
			if isProgram(p) {
				return p, "folder", nil
			}
		}
	}
	return "", "", err
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
	safe.Go("scan.runTool", func() { defer wg.Done(); _, _ = io.Copy(&stdout, io.LimitReader(p.Stdout(), 64<<20)) })
	safe.Go("scan.runTool#2", func() { defer wg.Done(); _, _ = io.Copy(&stderr, io.LimitReader(p.Stderr(), 1<<20)) })
	_ = p.Stdin().Close()
	var ei proc.ExitInfo
	select {
	case ei = <-p.Wait():
	case <-cctx.Done():
		_ = p.Kill(true)
		ei = <-p.Wait()
	}
	done := make(chan struct{})
	safe.Go("scan.runTool#3", func() { wg.Wait(); close(done) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	r.Stdout, r.Stderr, r.Code = stdout.Bytes(), stderr.Bytes(), ei.Code
	r.TimedOut = errors.Is(cctx.Err(), context.DeadlineExceeded)
	return r
}

// ansiEscape matches a terminal colour or cursor sequence (ESC [ ... letter); Opengrep colours its errors even into a pipe.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// firstStderrLine is the line of a tool's stderr that says what went wrong, cleaned and capped, for a reason: the first
// non-blank one that is not the usage boilerplate a command-line parser prints around its error ("Usage: opengrep scan
// [--help] [OPTION]… [TARGETS]…" and "Try 'opengrep scan --help'"), else the first non-blank one.
func firstStderrLine(b []byte) string {
	first := ""
	for _, l := range strings.Split(ansiEscape.ReplaceAllString(string(b), ""), "\n") {
		s := strings.TrimSpace(l)
		if s == "" {
			continue
		}
		if first == "" {
			first = s
		}
		if !strings.HasPrefix(s, "Usage:") && !strings.HasPrefix(s, "Try '") {
			return cleanLine(s, 160)
		}
	}
	return cleanLine(first, 160)
}

// programNames are the file names a program called name may have here.
func programNames(name string) []string {
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(name), ".exe") {
		return []string{name + ".exe"}
	}
	return []string{name}
}

// isProgram reports whether p is a regular file that can be run (on Windows any regular file: programNames gave it .exe).
func isProgram(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || fi.Mode().Perm()&0o111 != 0
}

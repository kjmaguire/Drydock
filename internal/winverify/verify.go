package winverify

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Options configure a verification run (the flags of cmd/drydock-verify).
type Options struct {
	Bundle     string   // the bundle folder (default: the folder drydock-verify is in)
	Out        string   // where the results folder and its zip go (default: the bundle folder)
	WithClaude bool     // run the real-Claude tests (spends real usage)
	WithUI     bool     // run the UI suites from a source checkout
	Repo       string   // the source checkout for WithUI (default: found from the working folder or the bundle)
	Steps      []string // run only these step ids; an entry ending in * matches a prefix
	Keep       bool     // keep scratch folders (for debugging)
	List       bool     // print the steps and exit
	Args       []string // the flags as given, for the report
}

// Env is shared by the steps of one run.
type Env struct {
	Bundle   string
	Manifest *Manifest
	Opts     Options
	Keep     bool
	Facts    *Facts

	goDir   string // a folder with the go tool to put first on PATH (the Go inside the bundle), "" otherwise
	goFound bool
	goNote  string
	runTemp string // this run's own temp folder (the Go caches when the bundled Go is used)
	core    *coreSession
}

// HasGo reports whether the tests that run the Go toolchain can find one.
func (e *Env) HasGo() bool { return e.goFound }

func (e *Env) goos() string { return runtime.GOOS }

// setupGo decides which Go the tests that need one will use: the one on PATH, else the copy inside the bundle (put
// first on PATH for test binaries and the core, with its caches in this run's temp folder so nothing is left behind).
func (e *Env) setupGo() {
	if p, err := exec.LookPath("go"); err == nil {
		e.goFound, e.goNote = true, "go on PATH ("+newRedactor().text(p)+")"
		return
	}
	bin := filepath.Join(e.Bundle, "go", "bin")
	if _, err := os.Stat(filepath.Join(bin, "go"+exeSuffix(runtime.GOOS))); err == nil {
		e.goDir, e.goFound = bin, true
		ver := ""
		if b, err := os.ReadFile(filepath.Join(e.Bundle, "go", "VERSION")); err == nil {
			ver = " (" + firstLine(string(b)) + ")"
		}
		e.goNote = "none on PATH, so the tests that need Go use the copy inside the bundle" + ver
		return
	}
	e.goNote = "none on PATH and none inside the bundle: the core smoke and the tests that run go build/test/vet are skipped"
}

// childEnv is the environment for a test binary or the core: this process's environment without the scripted-agent
// switches (a test sets those itself), with set applied and unset removed, and the bundled Go first on PATH when used.
func (e *Env) childEnv(set map[string]string, unset ...string) []string {
	env := os.Environ()
	for _, k := range append([]string{"DRYDOCK_FAKE_AGENT", "DRYDOCK_FAKE_SCRIPT", "DRYDOCK_FAKE_ACP", "DRYDOCK_FAKE_ACP_SCRIPT"}, unset...) {
		env = unsetEnv(env, k)
	}
	if e.goDir != "" {
		env = setEnv(env, "PATH", e.goDir+string(os.PathListSeparator)+getEnv(env, "PATH"))
		env = unsetEnv(env, "GOROOT") // the bundled go finds its own
		env = unsetEnv(env, "GOFLAGS")
		cache := filepath.Join(e.runTemp, "go")
		env = setEnv(env, "GOCACHE", filepath.Join(cache, "cache"))
		env = setEnv(env, "GOPATH", filepath.Join(cache, "path"))
		env = setEnv(env, "GOMODCACHE", filepath.Join(cache, "mod"))
		env = setEnv(env, "GOTOOLCHAIN", "local")
	}
	for _, k := range sortedKeys(set) {
		env = setEnv(env, k, set[k])
	}
	return env
}

func envKeyEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func getEnv(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && envKeyEqual(k, key) {
			return v
		}
	}
	return ""
}

// setEnv replaces key's value, keeping the spelling the environment already uses (Windows has "Path").
func setEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	done := false
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if k != "" && envKeyEqual(k, key) {
			if !done {
				out = append(out, k+"="+value)
				done = true
			}
			continue
		}
		out = append(out, kv)
	}
	if !done {
		out = append(out, key+"="+value)
	}
	return out
}

func unsetEnv(env []string, key string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); k != "" && envKeyEqual(k, key) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// close stops what the run left running (the core, if the shutdown step did not run) and removes the run's temp folder.
func (e *Env) close() {
	if e.core != nil {
		_, _ = e.core.stop(context.Background(), 30*time.Second)
		e.core.cleanup(e.Keep)
		e.core = nil
	}
	if e.runTemp != "" && !e.Keep {
		removeAllRetry(e.runTemp)
	}
}

// Steps are all steps of a run, in order: the machine, the core smoke (the quickest whole-loop proof), each package's
// tests from the basics to the long suites, then the opt-in real-Claude and UI steps.
func Steps(m *Manifest) []Step {
	steps := []Step{factsStep(), coreSmokeStep(), coreTerminalStep(), coreShutdownStep()}
	for _, p := range m.Packages {
		steps = append(steps, unitTestStep(p))
	}
	return append(steps, realClaudeStep(m), uiStep())
}

// FilterSteps keeps the steps named (exact ids, or prefixes for entries that end in *), in run order.
func FilterSteps(steps []Step, names []string) ([]Step, error) {
	if len(names) == 0 {
		return steps, nil
	}
	var out []Step
	used := map[string]bool{}
	for _, s := range steps {
		for _, n := range names {
			if n == s.ID || (strings.HasSuffix(n, "*") && strings.HasPrefix(s.ID, strings.TrimSuffix(n, "*"))) {
				out = append(out, s)
				used[n] = true
				break
			}
		}
	}
	for _, n := range names {
		if !used[n] {
			return nil, fmt.Errorf("no step is called %q (drydock-verify --list shows them)", n)
		}
	}
	return out, nil
}

// Verify runs a whole verification and returns the process exit code: 0 when no step failed, 1 when one did, 2 when the
// run could not start.
func Verify(ctx context.Context, o Options, stdout io.Writer) int {
	if o.Bundle == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintln(stdout, "drydock-verify:", err)
			return 2
		}
		o.Bundle = filepath.Dir(exe)
	}
	m, err := ReadManifest(o.Bundle)
	if err != nil {
		fmt.Fprintln(stdout, "drydock-verify:", err)
		fmt.Fprintln(stdout, "Run it from the folder the bundle was unzipped into (the one that holds bundle.json).")
		return 2
	}
	steps := Steps(m)
	if o.List {
		for _, s := range steps {
			fmt.Fprintf(stdout, "%s\n    %s\n", s.ID, s.Proves)
		}
		return 0
	}
	if steps, err = FilterSteps(steps, o.Steps); err != nil {
		fmt.Fprintln(stdout, "drydock-verify:", err)
		return 2
	}
	out := o.Out
	if out == "" {
		out = o.Bundle
	}
	stamp := time.Now().Format("2006-01-02-150405")
	results := filepath.Join(out, "drydock-verify-"+stamp)
	if err := os.MkdirAll(filepath.Join(results, "logs"), 0o755); err != nil {
		fmt.Fprintln(stdout, "drydock-verify: cannot write the results:", err)
		return 2
	}
	env := &Env{Bundle: o.Bundle, Manifest: m, Opts: o, Keep: o.Keep}
	if env.runTemp, err = os.MkdirTemp("", "drydock-verify-run-"); err != nil {
		fmt.Fprintln(stdout, "drydock-verify:", err)
		return 2
	}
	env.setupGo()
	defer env.close()

	dirty := ""
	if m.Dirty {
		dirty = ", with uncommitted changes"
	}
	fmt.Fprintf(stdout, "Drydock verification\n")
	fmt.Fprintf(stdout, "  bundle:  commit %s%s, built %s for %s/%s with %s\n", m.Commit, dirty, m.Built.Format("2006-01-02 15:04 MST"), m.GOOS, m.GOARCH, m.GoVersion)
	fmt.Fprintf(stdout, "  results: %s\n", results)
	fmt.Fprintf(stdout, "  Go:      %s\n", env.goNote)
	if _, err := exec.LookPath("git"); err != nil {
		fmt.Fprintf(stdout, "  WARNING: git is not on PATH. Most steps need it: install Git for Windows (https://git-scm.com) and run this again.\n")
	}
	fmt.Fprintf(stdout, "  %d steps; the whole run usually takes 10 to 30 minutes.\n\n", len(steps))

	started := time.Now()
	stepResults := RunSteps(ctx, steps, env, filepath.Join(results, "logs"), stdout)
	env.close()
	rep := &Report{Tool: "drydock-verify", Started: started, Finished: time.Now(), Host: runtime.GOOS + "/" + runtime.GOARCH,
		Bundle: m, Options: []string{}, Summary: Summarize(stepResults), Steps: stepResults}
	red := newRedactor()
	for _, a := range o.Args {
		rep.Options = append(rep.Options, red.text(a))
	}
	if err := rep.WriteJSON(filepath.Join(results, ReportJSON)); err != nil {
		fmt.Fprintln(stdout, "drydock-verify: writing the JSON report:", err)
	}
	if err := rep.WriteMarkdown(filepath.Join(results, ReportMD)); err != nil {
		fmt.Fprintln(stdout, "drydock-verify: writing the report:", err)
	}
	zipPath := results + ".zip"
	zipErr := ZipDir(results, zipPath)

	s := rep.Summary
	fmt.Fprintf(stdout, "\nResult: %d passed, %d failed, %d skipped in %s\n", s.Passed, s.Failed, s.Skipped, rep.Finished.Sub(started).Round(time.Second))
	fmt.Fprintf(stdout, "Report: %s\n", filepath.Join(results, ReportMD))
	if zipErr != nil {
		fmt.Fprintf(stdout, "Could not zip the results (%v): send the folder %s instead.\n", zipErr, results)
	} else {
		fmt.Fprintf(stdout, "Send this file back: %s\n", zipPath)
	}
	if s.Failed > 0 {
		return 1
	}
	return 0
}

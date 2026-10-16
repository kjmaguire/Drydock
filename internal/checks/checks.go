// Package checks runs the deterministic verification tools (build, test, lint, typecheck, ...) in a task worktree. They run
// before any reviewer sees the work (token rule 10) and produce workflow.CheckResult evidence: exit code, normalized and
// truncated output, failing test names. The full log always stays on disk.
package checks

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/redact"
	"github.com/kjmaguire/drydock/internal/workflow"
	"gopkg.in/yaml.v3"
)

// PassEnv returns the extra environment variable names the person chose to pass to agents and project commands (settings,
// read at every start). Set by the core; nil passes none.
var PassEnv func() []string

// Redact, if set, removes secrets from every line a check prints before it reaches the log file, the result and the chunk
// stream (the core sets it to the secret scanner; docs/security.md s8, audit 2026-10-16, H7). Nil leaves lines as they are.
var Redact func(string) string

// CommandEnv is the environment every project command starts from (a check, a preview, the dependency scan, the security
// scan's tools): the app's environment filtered to what a toolchain needs (proc.ToolEnv) plus the variables the person
// passes, never its tokens. These commands run code an agent may have just written, so they get no more than the agent
// itself (docs/security.md s8).
func CommandEnv() []string {
	var extra []string
	if PassEnv != nil {
		extra = PassEnv()
	}
	return proc.ToolEnv(os.Environ(), extra)
}

// Spec describes one check.
type Spec struct {
	Name    string        // build | test | lint | typecheck | custom name
	Argv    []string      // command and arguments, no shell
	Dir     string        // working directory (the task worktree)
	Env     []string      // extra environment (KEY=VALUE)
	Timeout time.Duration // default 10 minutes
	Parser  string        // go | js | generic (failing test extraction)
	LogDir  string        // where the full log is written; empty = a temp dir
	// MaxContext bounds the output kept in the result (default 6000 bytes: head 1000 + tail).
	MaxContext int
	// InProcess, when set, runs the check inside Drydock instead of as a command (the secrets check scans the diff with
	// the gitleaks scanner; the deps check drives an external tool and parses its JSON). Argv is then informational.
	InProcess InProcessFunc
}

// InProcessFunc runs an in-process check. It returns the result (Output is what the log file receives) or an error when the
// check could not be run at all. A tool that is missing is a skipped Result, never an error and never a pass.
type InProcessFunc func(ctx context.Context, pm proc.Manager, s Spec) (Result, error)

// Result extends workflow.CheckResult with run metadata.
type Result struct {
	workflow.CheckResult
	Duration time.Duration `json:"duration"`
	LogRef   string        `json:"logRef"`
	Argv     []string      `json:"argv"`
	TimedOut bool          `json:"timedOut,omitempty"`
	// Skipped means the check could not be evaluated honestly (a tool is not installed, nothing to scan): it is neither a
	// pass nor a failure, and the loop does not send it back to an agent. SkipReason says why. Errored is the same for a
	// tool that ran and broke (offline database missing, crash): visible, never a pass, not for an agent to fix.
	Skipped    bool   `json:"skipped,omitempty"`
	SkipReason string `json:"skipReason,omitempty"`
	Errored    bool   `json:"errored,omitempty"`
}

// Inconclusive reports a result that is neither a pass nor a failure of the work.
func (r Result) Inconclusive() bool { return r.Skipped || r.Errored }

// Run executes the check and returns its result. A failing check is a Result with Passed=false, not an error; an error
// means the check could not be run at all (missing executable, cannot create the log).
func Run(ctx context.Context, pm proc.Manager, spec Spec, onLine func(string)) (Result, error) {
	res := Result{Argv: spec.Argv}
	res.Name = spec.Name
	if spec.InProcess != nil {
		return runInProcess(ctx, pm, spec, onLine)
	}
	if len(spec.Argv) == 0 {
		return res, fmt.Errorf("checks: %s has no command", spec.Name)
	}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	logDir := spec.LogDir
	if logDir == "" {
		d, err := os.MkdirTemp("", "drydock-check-*")
		if err != nil {
			return res, err
		}
		logDir = d
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return res, err
	}
	// a random name: on Windows the clock advances in ticks, so two runs of one check could share a time-based name
	logPath := filepath.Join(logDir, ids.New(spec.Name)+".log")
	logf, err := os.Create(logPath)
	if err != nil {
		return res, err
	}
	defer logf.Close()
	res.LogRef = logPath

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	env := append(CommandEnv(), spec.Env...)
	env = append(env, "CI=1", "NO_COLOR=1", "FORCE_COLOR=0")
	start := time.Now()
	p, err := pm.Start(cctx, proc.Spec{Path: spec.Argv[0], Args: spec.Argv[1:], Dir: spec.Dir, Env: env, Label: "check:" + spec.Name})
	if err != nil {
		return res, err
	}
	var buf bytes.Buffer
	var mu sync.Mutex
	var wg sync.WaitGroup
	pump := func(r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			line := sc.Text()
			if red := Redact; red != nil && len(line) >= redact.MinString {
				line = red(line) // a test that prints a token must not leave it in the log, the evidence or the room
			}
			mu.Lock()
			fmt.Fprintln(logf, line)
			buf.WriteString(line)
			buf.WriteByte('\n')
			mu.Unlock()
			if onLine != nil {
				onLine(line)
			}
		}
	}
	wg.Add(2)
	go pump(p.Stdout())
	go pump(p.Stderr())
	_ = p.Stdin().Close()

	var ei proc.ExitInfo
	select {
	case ei = <-p.Wait():
	case <-cctx.Done():
		_ = p.Kill(true)
		ei = <-p.Wait()
		res.TimedOut = cctx.Err() == context.DeadlineExceeded
	}
	// let the pumps drain; descendants holding the pipes are gone once the job closed
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	res.Duration = time.Since(start)
	res.ExitCode = ei.Code
	res.Passed = ei.Code == 0 && !res.TimedOut && ei.Err == nil
	mu.Lock()
	full := buf.String()
	mu.Unlock()
	res.FailingTests = ParseFailingTests(spec.Parser, full)
	maxCtx := spec.MaxContext
	if maxCtx <= 0 {
		maxCtx = 6000
	}
	res.Output = Truncate(full, maxCtx, logPath)
	if res.TimedOut {
		res.Output = fmt.Sprintf("check %s timed out after %s\n", spec.Name, timeout) + res.Output
	}
	return res, nil
}

// Truncate keeps the head and tail of s within max bytes, noting where the full log is (token rule 8).
func Truncate(s string, max int, logPath string) string {
	if len(s) <= max {
		return s
	}
	head := max / 6
	tail := max - head - 120
	if tail < 0 {
		tail = 0
	}
	cut := len(s) - head - tail
	note := fmt.Sprintf("\n... [%d bytes omitted; full log: %s] ...\n", cut, logPath)
	return s[:head] + note + s[len(s)-tail:]
}

var (
	goFail    = regexp.MustCompile(`(?m)^\s*--- FAIL: (\S+)`)
	jsFail    = regexp.MustCompile(`(?m)^\s*(?:FAIL|×|✗|✕)\s+(.+?)\s*$`)
	jsFailAlt = regexp.MustCompile(`(?m)^\s*FAIL\s+(\S+\.(?:test|spec)\.[jt]sx?)(?:\s+>\s+(.+))?`)
)

// ParseFailingTests extracts failing test names from raw output. Unknown parsers return nil.
func ParseFailingTests(parser, out string) []string {
	set := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" {
			set[s] = true
		}
	}
	switch parser {
	case "go":
		for _, m := range goFail.FindAllStringSubmatch(out, -1) {
			add(m[1])
		}
	case "js":
		for _, m := range jsFailAlt.FindAllStringSubmatch(out, -1) {
			if m[2] != "" {
				add(m[1] + " > " + m[2])
			} else {
				add(m[1])
			}
		}
		for _, m := range jsFail.FindAllStringSubmatch(out, -1) {
			add(m[1])
		}
	}
	if len(set) == 0 {
		return nil
	}
	var names []string
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Env supplies what the node handler needs from the app.
type Env struct {
	PM proc.Manager
	// Spec builds the check spec for this execution (worktree path comes from the task).
	Spec func(in workflow.NodeInput) (Spec, error)
	// DiffHash fingerprints the worktree for the watchdog's error-echo signal (git.Repo.DiffHash).
	DiffHash func(ctx context.Context, in workflow.NodeInput) string
	// OnResult is called with every result (record evidence, post the tool-member message). May be nil.
	OnResult func(ctx context.Context, in workflow.NodeInput, r Result)
}

// Node returns a workflow handler for a `check` node. It streams log lines as chunks, then returns the CheckResult as the
// node value with Route "pass" or "fail".
func Node(e Env) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		spec, err := e.Spec(in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		res, err := Run(ctx, e.PM, spec, func(line string) {
			in.Emit(workflow.NewChunk(workflow.ChunkLog, workflow.TextDelta{Text: line}))
		})
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		if e.DiffHash != nil {
			res.DiffHash = e.DiffHash(ctx, in)
		}
		if e.OnResult != nil {
			e.OnResult(ctx, in, res)
		}
		b, _ := json.Marshal(res.CheckResult)
		route := "pass"
		if !res.Passed {
			route = "fail"
		}
		return workflow.NodeOutput{Value: b, Route: route}, nil
	}
}

// Detect proposes check specs for a project directory: a Taskfile with the standard targets (if go-task is installed),
// otherwise Go module commands, otherwise package.json scripts. Targets that do not exist are omitted.
func Detect(dir string) []Spec {
	var specs []Spec
	if tf := readTaskfile(dir); len(tf) > 0 {
		if _, err := exec.LookPath("task"); err == nil {
			for _, name := range []string{"build", "test", "lint", "typecheck"} {
				if tf[name] {
					specs = append(specs, Spec{Name: name, Argv: []string{"task", name}, Dir: dir, Parser: parserFor(dir)})
				}
			}
			if len(specs) > 0 {
				return specs
			}
		}
	}
	if exists(filepath.Join(dir, "go.mod")) {
		return []Spec{
			{Name: "build", Argv: []string{"go", "build", "./..."}, Dir: dir, Parser: "go"},
			{Name: "test", Argv: []string{"go", "test", "./..."}, Dir: dir, Parser: "go"},
			{Name: "lint", Argv: []string{"go", "vet", "./..."}, Dir: dir, Parser: "go"},
		}
	}
	if scripts := packageScripts(dir); len(scripts) > 0 {
		for _, name := range []string{"build", "test", "lint", "typecheck"} {
			if scripts[name] {
				specs = append(specs, Spec{Name: name, Argv: []string{"npm", "run", name, "--silent"}, Dir: dir, Parser: "js"})
			}
		}
	}
	return specs
}

func parserFor(dir string) string {
	if exists(filepath.Join(dir, "go.mod")) {
		return "go"
	}
	return "js"
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func readTaskfile(dir string) map[string]bool {
	for _, n := range []string{"Taskfile.yml", "Taskfile.yaml"} {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		var tf struct {
			Tasks map[string]any `yaml:"tasks"`
		}
		if yaml.Unmarshal(b, &tf) != nil {
			return nil
		}
		out := map[string]bool{}
		for k := range tf.Tasks {
			out[k] = true
		}
		return out
	}
	return nil
}

func packageScripts(dir string) map[string]bool {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil
	}
	var pj struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(b, &pj) != nil {
		return nil
	}
	out := map[string]bool{}
	for k := range pj.Scripts {
		out[k] = true
	}
	return out
}

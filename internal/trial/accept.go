package trial

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/proc"
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
//
// Two things keep the check honest about code an agent wrote. The commands run in a scrubbed environment (acceptEnv): the
// result's tests are the agent's code, and a token of the harness's account is not for it. And once the commands have passed,
// every test the hidden files declare must have been seen to pass (hiddenTestsPassed): `go test` exits 0 for a package whose
// TestMain never calls m.Run, or calls it and exits 0 anyway, and the check would have accepted a result whose hidden tests
// never ran (audit 2026-10-16, L54).
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
	timeout := time.Duration(t.Acceptance.TimeoutSeconds) * time.Second
	res.Passed = true
	for _, c := range t.Acceptance.Commands {
		cr := runCommand(ctx, dir, env, c, timeout)
		res.Commands = append(res.Commands, cr)
		if cr.ExitCode != 0 {
			res.Passed = false
			break
		}
	}
	if res.Passed {
		if cr, ok := hiddenTestsPassed(ctx, dir, filepath.Join(root, t.Acceptance.Files), env, timeout); ok {
			res.Commands = append(res.Commands, cr)
			res.Passed = cr.ExitCode == 0
		}
	}
	return finish()
}

// acceptEnv is the environment the acceptance commands run in: the toolchain's variables and the network settings (proc.ToolEnv,
// the list a project's checks get, with the password taken out of a proxy address), never a token for GitHub, a cloud, npm or the
// agent CLI, which the harness's own environment may hold and an agent's test code could read and send (audit 2026-10-16, M19).
// Home, the temporary folder and the Go settings that decide what is built are the scratch directory's, so the result is judged
// the same way on every machine and an acceptance run leaves nothing behind.
func acceptEnv(dir string) []string {
	var env []string
	for _, kv := range proc.ToolEnv(os.Environ(), nil) {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "GOFLAGS", "GOTOOLCHAIN", "GOPROXY", "GOWORK", "GOPATH", "GOCACHE", "GOMODCACHE", "GOENV", "CGO_ENABLED",
			"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TMPDIR", "TMP", "TEMP",
			"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME":
			continue
		}
		env = append(env, kv)
	}
	home, tmp := filepath.Join(dir, ".home"), filepath.Join(dir, ".tmp")
	_ = os.MkdirAll(home, 0o755)
	_ = os.MkdirAll(tmp, 0o755)
	cache, _ := os.UserCacheDir()
	return append(env, "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local", "GOPROXY=off", "GOWORK=off", "GOENV=off", "CGO_ENABLED=0",
		"GOPATH="+filepath.Join(dir, ".gopath"), "GOCACHE="+filepath.Join(cache, "drydock-trial-gocache"),
		"HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME="+filepath.Join(home, ".local", "state"), "XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp)
}

// hiddenTestsCommand names the check below in the record of an acceptance run.
const hiddenTestsCommand = "go test -json (every test of the hidden files must report a pass)"

// maxTestEvents bounds how much of the JSON stream of go test is kept: the hidden tests print little, and a result whose code
// prints without end cannot make the harness hold it all (the stream is then cut, which is unreadable, which is a failure).
const maxTestEvents = 8 << 20

// hiddenTestsPassed runs the tests the hidden files declare, by name, and checks the JSON stream of go test for each one's own
// "pass" event. ok is false when there is nothing to check: the hidden files declare no Go test (a task whose check is a script).
func hiddenTestsPassed(ctx context.Context, dir, hidden string, env []string, timeout time.Duration) (cr CommandResult, ok bool) {
	tests, err := hiddenTests(hidden)
	if err == nil && len(tests) == 0 {
		return CommandResult{}, false
	}
	start := time.Now()
	cr = CommandResult{Command: hiddenTestsCommand}
	defer func() { cr.DurationMs = time.Since(start).Milliseconds() }()
	fail := func(format string, a ...any) (CommandResult, bool) {
		cr.ExitCode, cr.Output = 1, tail(fmt.Sprintf(format, a...), outputTail)
		return cr, true
	}
	if err != nil {
		return fail("the hidden files could not be read: %v", err)
	}
	goMod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return fail("the result has no go.mod: %v", err)
	}
	module := modulePath(goMod)
	if module == "" {
		return fail("go.mod names no module")
	}
	var names []string
	want := map[testID]bool{}
	for rel, fns := range tests {
		pkg := module
		if rel != "." {
			pkg += "/" + rel
		}
		for _, fn := range fns {
			names = append(names, fn)
			want[testID{pkg, fn}] = true
		}
	}
	sort.Strings(names)
	pattern := "^(?:" + strings.Join(names, "|") + ")$"
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-json", "-run", pattern, "./...")
	cmd.Dir, cmd.Env = dir, env
	var stdout capped
	stdout.max = maxTestEvents
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 5 * time.Second
	runErr := cmd.Run()
	if ctx.Err() != nil {
		cr.TimedOut, cr.ExitCode = true, -1
		cr.Output = "the hidden tests did not finish within the time limit"
		return cr, true
	}
	passed, err := passedTests(stdout.buf.Bytes(), stdout.over)
	if err != nil {
		return fail("%v\n%s", err, stderr.String())
	}
	var missing []string
	for id := range want {
		if !passed[id] {
			missing = append(missing, id.name+" ("+id.pkg+")")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fail("%d of the %d tests of the hidden files did not report a pass (a TestMain that stops them, a skip or a failure): %s",
			len(missing), len(want), strings.Join(missing, ", "))
	}
	if runErr != nil {
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			// every hidden test passed and go test still failed: a package of the result's own failed
			return fail("go test exited %d although every hidden test passed:\n%s", exit.ExitCode(), tail(stderr.String(), outputTail))
		}
		return fail("go test did not run: %v", runErr)
	}
	cr.Output = fmt.Sprintf("%d tests of the hidden files reported a pass", len(want))
	return cr, true
}

type testID struct{ pkg, name string }

// capped keeps the first max bytes written to it and says whether more came.
type capped struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); len(p) <= room {
		c.buf.Write(p)
	} else {
		if room > 0 {
			c.buf.Write(p[:room])
		}
		c.over = true
	}
	return len(p), nil // the program is not stopped by a full buffer; what it printed past it is dropped
}

// passedTests reads the JSON stream of go test and returns the top-level tests that ended with the action "pass". Output that
// is cut or is not the stream is an error: no pass is believed from half a stream.
func passedTests(stream []byte, overflowed bool) (map[testID]bool, error) {
	if overflowed {
		return nil, fmt.Errorf("go test printed more than %d bytes of events", maxTestEvents)
	}
	final := map[testID]string{}
	dec := json.NewDecoder(bytes.NewReader(stream))
	for {
		var ev struct{ Action, Package, Test string }
		if err := dec.Decode(&ev); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("the output of go test -json is unreadable: %v", err)
		}
		if ev.Test == "" || strings.Contains(ev.Test, "/") {
			continue
		}
		switch ev.Action {
		case "pass", "fail", "skip":
			final[testID{ev.Package, ev.Test}] = ev.Action
		}
	}
	passed := map[testID]bool{}
	for id, action := range final {
		passed[id] = action == "pass"
	}
	return passed, nil
}

// hiddenTests lists the test functions the hidden files declare, keyed by the directory of their package relative to the module
// root (slashes; "." for the root). The hidden folder mirrors the fixture's layout, so that is where they land in the copy.
func hiddenTests(hidden string) (map[string][]string, error) {
	out := map[string][]string{}
	err := filepath.WalkDir(hidden, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(hidden, filepath.Dir(p))
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && isTestFunc(fn) {
				out[rel] = append(out[rel], fn.Name.Name)
			}
		}
		return nil
	})
	return out, err
}

// isTestFunc is go test's own rule: TestXxx, where Xxx does not start with a lower-case letter, taking one *testing.T.
func isTestFunc(fn *ast.FuncDecl) bool {
	name := fn.Name.Name
	rest, ok := strings.CutPrefix(name, "Test")
	if !ok {
		return false
	}
	if r, _ := utf8.DecodeRuneInString(rest); rest != "" && unicode.IsLower(r) {
		return false
	}
	params := fn.Type.Params
	if params == nil || len(params.List) != 1 || len(params.List[0].Names) > 1 {
		return false
	}
	star, ok := params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "T"
}

var moduleLineRe = regexp.MustCompile(`(?m)^\s*module\s+("[^"]+"|\S+)`)

// modulePath is the module path go.mod declares ("" when it declares none).
func modulePath(goMod []byte) string {
	m := moduleLineRe.FindSubmatch(goMod)
	if m == nil {
		return ""
	}
	return strings.Trim(string(m[1]), `"`)
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

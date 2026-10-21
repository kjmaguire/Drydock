package winverify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// needsGoReason is the skip reason of a test that runs the Go toolchain itself when no Go is available.
const needsGoReason = "needs the Go toolchain at test time (it runs go build, go test or go vet on a fixture project), and no Go is available here: none on PATH and none inside the bundle"

// testGitConfig gives test binaries the git settings CI gives them (.github/workflows/ci.yml): an identity and
// core.autocrlf false, on top of the user's own global configuration. They are written to a file in this run's temp folder
// and named by GIT_CONFIG_GLOBAL, which Drydock's own git calls also read (GIT_CONFIG_COUNT/KEY/VALUE would reach only the
// tests' git, since Drydock drops them), so no git configuration file on the machine changes. The core smoke does not use
// it: it runs with the machine's own git settings, as the app would.
func (e *Env) testGitConfig(set map[string]string) map[string]string {
	if set == nil {
		set = map[string]string{}
	}
	if p, err := e.writeTestGitConfig(); err == nil {
		set["GIT_CONFIG_GLOBAL"] = p
		return set
	}
	// no temp folder to write to: the variables still reach the tests' own git
	set["GIT_CONFIG_COUNT"] = "3"
	set["GIT_CONFIG_KEY_0"], set["GIT_CONFIG_VALUE_0"] = "user.name", "Drydock Verify"
	set["GIT_CONFIG_KEY_1"], set["GIT_CONFIG_VALUE_1"] = "user.email", "verify@drydock.invalid"
	set["GIT_CONFIG_KEY_2"], set["GIT_CONFIG_VALUE_2"] = "core.autocrlf", "false"
	return set
}

// writeTestGitConfig writes the file testGitConfig names. It includes the global files git would otherwise read (the
// user's GIT_CONFIG_GLOBAL, or ~/.gitconfig and the XDG one), so only the three settings differ from the machine's own.
func (e *Env) writeTestGitConfig() (string, error) {
	if e.runTemp == "" {
		return "", errors.New("no temp folder for this run")
	}
	var b strings.Builder
	var globals []string
	if g := os.Getenv("GIT_CONFIG_GLOBAL"); g != "" {
		globals = []string{g}
	} else {
		if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			globals = append(globals, filepath.Join(x, "git", "config"))
		} else if h, err := os.UserHomeDir(); err == nil {
			globals = append(globals, filepath.Join(h, ".config", "git", "config"))
		}
		if h, err := os.UserHomeDir(); err == nil {
			globals = append(globals, filepath.Join(h, ".gitconfig"))
		}
	}
	for _, g := range globals {
		if _, err := os.Stat(g); err == nil {
			fmt.Fprintf(&b, "[include]\n\tpath = %s\n", gitConfigQuote(filepath.ToSlash(g)))
		}
	}
	b.WriteString("[user]\n\tname = Drydock Verify\n\temail = verify@drydock.invalid\n[core]\n\tautocrlf = false\n")
	p := filepath.Join(e.runTemp, "test-gitconfig")
	return p, os.WriteFile(p, []byte(b.String()), 0o644)
}

// gitConfigQuote writes a value in git's config syntax: double-quoted, with backslash and quote escaped.
func gitConfigQuote(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

// unitTestStep runs one package's test binary in its own folder, without the real-Claude tests.
func unitTestStep(p Package) Step {
	return Step{
		ID:     p.StepID(),
		Title:  "Tests of " + p.Path,
		Proves: p.Proves,
		Run: func(ctx context.Context, sc *StepContext) Outcome {
			env := sc.Env
			skip := append([]string(nil), p.RealClaude...)
			var synthetic []TestResult
			if !env.HasGo() {
				for _, n := range p.NeedsGo {
					if contains(p.RealClaude, n) {
						continue
					}
					skip = append(skip, n)
					synthetic = append(synthetic, TestResult{Package: p.Path, Name: n, Status: Skip, Reason: needsGoReason})
				}
			}
			args := []string{"-test.v=test2json", "-test.timeout=" + p.Timeout}
			if len(skip) > 0 {
				args = append(args, "-test.skip="+namesPattern(skip))
			}
			if len(p.RealClaude) > 0 {
				sc.Logf("left out here (they use the real Claude Code CLI; see the real-claude step): %s", strings.Join(p.RealClaude, ", "))
			}
			run := env.runTestBinary(ctx, sc, p, args, env.childEnv(env.testGitConfig(map[string]string{"DRYDOCK_NO_REAL_AGENTS": "1"}), "DRYDOCK_REAL_CLAUDE"))
			run.Parsed.Tests = append(run.Parsed.Tests, synthetic...)
			return testOutcome([]BinaryRun{run}, env.goos())
		},
	}
}

// realClaudeStep runs the tests that use the real Claude Code CLI, across every package that has some. It only runs with
// --with-claude: it spends real usage on the account claude is signed in to.
func realClaudeStep(m *Manifest) Step {
	return Step{
		ID:    "real-claude",
		Title: "Tests with the real Claude Code CLI",
		Proves: "The Claude Code adapter works with the real claude CLI on this machine (on Windows usually the npm launcher claude.cmd): " +
			"multi-line instructions arrive whole, tools run, usage is recorded, and a full worker-and-reviewer task completes.",
		Run: func(ctx context.Context, sc *StepContext) Outcome {
			env := sc.Env
			if !env.Opts.WithClaude {
				return Outcome{Status: Skip, Reason: "not requested: run drydock-verify --with-claude to include it (it uses the Claude Code sign-in on this machine and spends real usage, roughly one dollar)"}
			}
			if _, err := exec.LookPath("claude"); err != nil {
				return Outcome{Status: Skip, Reason: "claude is not on PATH: install Claude Code and sign in, then run this again"}
			}
			var runs []BinaryRun
			for _, p := range m.Packages {
				if len(p.RealClaude) == 0 {
					continue
				}
				run := p.RealClaude
				var synthetic []TestResult
				if !env.HasGo() {
					run = nil
					for _, n := range p.RealClaude {
						if contains(p.NeedsGo, n) {
							synthetic = append(synthetic, TestResult{Package: p.Path, Name: n, Status: Skip, Reason: needsGoReason})
						} else {
							run = append(run, n)
						}
					}
				}
				var br BinaryRun
				if len(run) > 0 {
					args := []string{"-test.v=test2json", "-test.timeout=30m", "-test.run=" + namesPattern(run)}
					br = env.runTestBinary(ctx, sc, p, args, env.childEnv(env.testGitConfig(map[string]string{"DRYDOCK_REAL_CLAUDE": "1"}), "DRYDOCK_NO_REAL_AGENTS"))
				} else {
					br = BinaryRun{Package: p.Path}
				}
				br.Parsed.Tests = append(br.Parsed.Tests, synthetic...)
				runs = append(runs, br)
			}
			if len(runs) == 0 {
				return Outcome{Status: Skip, Reason: "this bundle has no real-Claude tests"}
			}
			return testOutcome(runs, env.goos())
		},
	}
}

// testOutcome turns test binary runs into a step outcome. With one run the package is already in the step id, so tests
// are listed by name; with several (the real-Claude step) each test carries its package.
func testOutcome(runs []BinaryRun, goos string) Outcome {
	var all []TestResult
	for _, r := range runs {
		for _, t := range r.Parsed.Tests {
			t.Package = ""
			if len(runs) > 1 {
				t.Package = r.Package
			}
			all = append(all, t)
		}
	}
	status, reason := Judge(runs, goos)
	excerpt := testExcerpt(all, 60)
	for _, r := range runs {
		if r.StartErr != "" || r.Parsed.Crash != "" || (r.ExitErr != "" && countFailed(r.Parsed.Tests) == 0) {
			excerpt += "\n\n" + r.Package + ": " + nonEmpty(r.StartErr, r.ExitErr, r.Parsed.Crash) + "; the last lines it printed:\n" + r.Tail
		}
	}
	return Outcome{Status: status, Reason: reason, Tests: all, Excerpt: excerpt}
}

// namesPattern is a -test.run / -test.skip pattern that matches exactly these top-level tests.
func namesPattern(names []string) string {
	return "^(" + strings.Join(names, "|") + ")$"
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// runTestBinary runs one test binary with its package folder as the working folder (tests read testdata/ relative to
// it), writes everything it prints to the step log (framing bytes removed) and parses the result.
func (e *Env) runTestBinary(ctx context.Context, sc *StepContext, p Package, args []string, childEnv []string) BinaryRun {
	run := BinaryRun{Package: p.Path, Args: args}
	bin := filepath.Join(e.Bundle, filepath.FromSlash(p.Binary))
	if _, err := os.Stat(bin); err != nil {
		run.StartErr = "the test binary is missing from the bundle: " + p.Binary
		return run
	}
	timeout, err := time.ParseDuration(p.Timeout)
	if err != nil || timeout <= 0 {
		timeout = defaultTimeout
	}
	// the binary's own -test.timeout stops it first, with a list of the tests still running; this is the backstop
	cctx, cancel := context.WithTimeout(ctx, timeout+2*time.Minute)
	defer cancel()
	raw, err := os.CreateTemp(sc.Work, "test-output-*.txt")
	if err != nil {
		run.StartErr = err.Error()
		return run
	}
	defer raw.Close()
	cmd := exec.CommandContext(cctx, bin, args...)
	cmd.Dir = filepath.Join(e.Bundle, p.Dir())
	cmd.Env = childEnv
	cmd.Stdout, cmd.Stderr = raw, raw
	cmd.WaitDelay = 15 * time.Second
	sc.Logf("$ %s %s\n  (in %s)\n", filepath.Base(bin), strings.Join(args, " "), cmd.Dir)
	start := time.Now()
	err = cmd.Run()
	run.Duration = time.Since(start)
	var ee *exec.ExitError
	switch {
	case err == nil:
	case cctx.Err() == context.DeadlineExceeded:
		run.ExitErr = "was stopped after " + (timeout + 2*time.Minute).String()
	case ctx.Err() != nil:
		run.ExitErr = "was stopped because the run was cancelled"
	case errors.As(err, &ee):
		run.ExitErr = fmt.Sprintf("exited with code %d", ee.ExitCode())
	default:
		run.StartErr = err.Error()
	}
	b, rerr := os.ReadFile(raw.Name())
	if rerr != nil {
		run.StartErr = "cannot read the test output: " + rerr.Error()
		return run
	}
	run.Parsed = ParseTestOutput(bytes.NewReader(b))
	// the log reads like ordinary go test -v output (test2json's bare "=== NAME" lines say "no test is talking now")
	clean := strings.NewReplacer(string(frameMarker)+"=== NAME  \n", "", string(frameMarker), "", string(errBegin), "", string(errEnd), "").Replace(string(b))
	_, _ = io.WriteString(sc.Log, clean)
	run.Tail = lastLines(clean, 25, 4000)
	sc.Logf("\n[%s %s in %s]", p.Path, nonEmpty(run.ExitErr, run.StartErr, "exited with code 0"), run.Duration.Round(time.Millisecond))
	return run
}

func nonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

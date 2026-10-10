package winverify

import (
	"context"
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
)

var (
	nodeMajor     = regexp.MustCompile(`^v?(\d+)\.`)
	playwrightSum = regexp.MustCompile(`(?m)^\s*\d+ (passed|failed|flaky|skipped|did not run)\b.*$`)
)

// uiStep runs the UI's own suites from a source checkout: npm ci, the unit and component tests, the build, and the
// Playwright browser tests (whose global setup builds and starts cmd/drydock-e2e, so Go is needed too).
func uiStep() Step {
	return Step{
		ID:     "ui",
		Title:  "The UI suites",
		Proves: "The UI's unit and component tests, its build and the browser tests against the real core (scripted test-double agent) pass on this machine.",
		Run: func(ctx context.Context, sc *StepContext) Outcome {
			env := sc.Env
			if !env.Opts.WithUI {
				return Outcome{Status: Skip, Reason: "not requested: run drydock-verify --with-ui from a Drydock source checkout with Node 24 (it runs npm ci, npm test, npm run build and npx playwright test in ui/)"}
			}
			repo := env.Opts.Repo
			if repo == "" {
				for _, start := range []string{".", env.Bundle} {
					if r, err := FindRepoRoot(start); err == nil {
						repo = r
						break
					}
				}
			}
			if repo == "" {
				return Outcome{Status: Skip, Reason: "no Drydock source checkout found from the working folder or the bundle folder (give one with --repo)"}
			}
			ui := filepath.Join(repo, "ui")
			if _, err := os.Stat(filepath.Join(ui, "package.json")); err != nil {
				return Outcome{Status: Skip, Reason: "the checkout at " + repo + " has no ui/package.json"}
			}
			node, err := exec.LookPath("node")
			if err != nil {
				return Outcome{Status: Skip, Reason: "node is not on PATH: the UI needs Node 24 or newer"}
			}
			v := toolVersion(ctx, node, []string{"--version"})
			m := nodeMajor.FindStringSubmatch(v)
			if major, _ := strconv.Atoi(firstOr(m, 1)); major < 24 {
				return Outcome{Status: Skip, Reason: "the UI needs Node 24 or newer; this machine has " + v}
			}
			for _, tool := range []string{"npm", "npx"} {
				if _, err := exec.LookPath(tool); err != nil {
					return Outcome{Status: Skip, Reason: tool + " is not on PATH"}
				}
			}
			if !env.HasGo() {
				return Outcome{Status: Skip, Reason: "no Go is available: the browser tests' global setup builds cmd/drydock-e2e with go build"}
			}
			cmds := [][]string{{"npm", "ci"}, {"npm", "test"}, {"npm", "run", "build"}}
			if os.Getenv("PLAYWRIGHT_BROWSERS_PATH") == "" { // a preinstalled Chromium is used when that is set
				cmds = append(cmds, []string{"npx", "playwright", "install", "chromium"})
			}
			cmds = append(cmds, []string{"npx", "playwright", "test"})
			childEnv := env.childEnv(testGitConfig(map[string]string{"DRYDOCK_NO_REAL_AGENTS": "1", "CI": "1"}))
			var last string
			for _, c := range cmds {
				out, err := runLogged(ctx, sc.Log, ui, childEnv, 45*time.Minute, c...)
				last = out
				if err != nil {
					return Outcome{Status: Fail, Reason: strings.Join(c, " ") + " " + err.Error(), Excerpt: lastLines(stripANSI(out), 40, 6000)}
				}
			}
			sum := strings.Join(playwrightSum.FindAllString(stripANSI(last), -1), "\n")
			return Outcome{Status: Pass, Excerpt: "npm ci, npm test, npm run build and npx playwright test passed in " + ui + "\n" + strings.TrimSpace(sum)}
		},
	}
}

func firstOr(m []string, i int) string {
	if len(m) > i {
		return m[i]
	}
	return ""
}

// runLogged runs a command in dir, copies its output to the log and returns the output.
func runLogged(ctx context.Context, log io.Writer, dir string, env []string, limit time.Duration, argv ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = dir, env
	var buf strings.Builder
	w := io.MultiWriter(log, &buf)
	cmd.Stdout, cmd.Stderr = w, w
	cmd.WaitDelay = 15 * time.Second
	fmt.Fprintf(log, "\n$ %s\n", strings.Join(argv, " "))
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case ctx.Err() == context.DeadlineExceeded:
		err = fmt.Errorf("did not finish within %s", limit)
	case errors.As(err, &ee):
		err = fmt.Errorf("exited with code %d", ee.ExitCode())
	}
	return buf.String(), err
}

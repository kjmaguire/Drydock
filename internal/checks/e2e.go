package checks

import (
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
	"time"

	"github.com/kjmaguire/drydock/internal/proc"
	"gopkg.in/yaml.v3"
)

// This file is the `e2e` check kind (brief section 13a): Playwright, run as an external tool in the task worktree, when
// the project defines it. A project that does not define an e2e target gets an honest "skipped", never a pass. Nothing
// here installs anything: a missing dependency is reported, not fetched (Drydock never runs a network install for the
// user, decisions D9 and the Connections rules).

// SkippedExitCode marks a Result that was not run (disabled, or the project defines no such check).
const SkippedExitCode = -2

// SkippedResult builds the Result of a check that did not run. Passed is false on purpose: a skipped check is not a pass,
// and it is not a failure either (Result.Skipped, shared with the secrets and deps checks), so the loop never sends it
// back to an agent.
func SkippedResult(name, reason string) Result {
	r := Result{Skipped: true, SkipReason: reason}
	r.Name = name
	r.ExitCode = SkippedExitCode
	r.Output = "skipped: " + reason
	return r
}

// IsSkipped reports whether r is a check that was not run.
func IsSkipped(r Result) bool { return r.Skipped }

// E2EOptions configures one e2e run.
type E2EOptions struct {
	// Enabled is the per-project setting. False yields a skipped result whatever the project defines.
	Enabled bool
	// Command overrides detection (argv, no shell), from the per-project settings.
	Command []string
	Timeout time.Duration // default 15 minutes
	// LogDir receives the full log and the screenshots copied out of the worktree (the worktree is deleted after a merge).
	LogDir string
	Env    []string
}

// E2EPlan says how e2e would run in a directory, or why it cannot.
type E2EPlan struct {
	Defined bool
	Source  string // taskfile:test:e2e | taskfile:e2e | package.json:test:e2e | package.json:e2e | playwright.config | settings
	Spec    Spec
	Reason  string // when not Defined
}

var playwrightConfigs = []string{"playwright.config.ts", "playwright.config.js", "playwright.config.mjs", "playwright.config.cjs", "playwright.config.mts"}

// DetectE2E looks for a project-defined e2e command, most explicit first: a Taskfile target `test:e2e` then `e2e` (when
// go-task is installed), a package.json script of the same names, then a Playwright config with the test runner installed
// locally (run through `npx --no-install`, so npx can never download it).
func DetectE2E(dir string) E2EPlan {
	var notes []string
	if tf := e2eTaskfileTargets(dir); len(tf) > 0 {
		for _, target := range []string{"test:e2e", "e2e"} {
			if !tf[target] {
				continue
			}
			if _, err := exec.LookPath("task"); err == nil {
				return E2EPlan{Defined: true, Source: "taskfile:" + target, Spec: Spec{Name: "e2e", Argv: []string{"task", target}, Dir: dir, Parser: "js"}}
			}
			notes = append(notes, "the Taskfile defines "+target+" but go-task is not installed")
		}
	}
	if scripts := packageScripts(dir); len(scripts) > 0 {
		for _, s := range []string{"test:e2e", "e2e"} {
			if scripts[s] {
				return E2EPlan{Defined: true, Source: "package.json:" + s, Spec: Spec{Name: "e2e", Argv: []string{"npm", "run", s, "--silent"}, Dir: dir, Parser: "js"}}
			}
		}
	}
	for _, c := range playwrightConfigs {
		if !exists(filepath.Join(dir, c)) {
			continue
		}
		if !playwrightInstalled(dir) {
			notes = append(notes, c+" is present but @playwright/test is not installed in node_modules (install the project's dependencies yourself; Drydock does not)")
			break
		}
		return E2EPlan{Defined: true, Source: "playwright.config", Spec: Spec{Name: "e2e", Argv: []string{"npx", "--no-install", "playwright", "test"}, Dir: dir, Parser: "js"}}
	}
	reason := "the project defines no e2e target (looked for a Taskfile target test:e2e or e2e, a package.json script test:e2e or e2e, and a Playwright config)"
	if len(notes) > 0 {
		reason += "; " + strings.Join(notes, "; ")
	}
	return E2EPlan{Reason: reason}
}

func playwrightInstalled(dir string) bool {
	for _, p := range []string{
		filepath.Join("node_modules", "@playwright", "test", "package.json"),
		filepath.Join("node_modules", ".bin", "playwright"),
		filepath.Join("node_modules", ".bin", "playwright.cmd"),
	} {
		if exists(filepath.Join(dir, p)) {
			return true
		}
	}
	return false
}

func e2eTaskfileTargets(dir string) map[string]bool {
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

// PlaywrightSummary is what Drydock reads from Playwright's JSON report.
type PlaywrightSummary struct {
	Expected   int      `json:"expected"`
	Unexpected int      `json:"unexpected"`
	Flaky      int      `json:"flaky"`
	Skipped    int      `json:"skipped"`
	Failed     []string `json:"failed,omitempty"` // "file > title"
}

// E2EResult is the outcome of RunE2E.
type E2EResult struct {
	Result
	Source      string             `json:"source,omitempty"`
	Summary     *PlaywrightSummary `json:"summary,omitempty"`
	Screenshots []string           `json:"screenshots,omitempty"` // copies under LogDir/e2e-screenshots
}

// reportCandidates are where Playwright JSON reports usually land, relative to the project (the starter writes the first).
var reportCandidates = []string{filepath.Join("test-results", "e2e-report.json"), filepath.Join("test-results", "results.json"), "playwright-report.json", filepath.Join("playwright-report", "report.json")}

// RunE2E runs the project's e2e command. It returns a skipped result (not an error) when e2e is disabled or undefined,
// and an error only when the command could not be started at all.
func RunE2E(ctx context.Context, pm proc.Manager, dir string, o E2EOptions, onLine func(string)) (E2EResult, error) {
	skip := func(src, reason string) E2EResult {
		return E2EResult{Result: SkippedResult("e2e", reason), Source: src}
	}
	if !o.Enabled {
		return skip("", "e2e is switched off in this project's settings"), nil
	}
	var plan E2EPlan
	if len(o.Command) > 0 {
		plan = E2EPlan{Defined: true, Source: "settings", Spec: Spec{Name: "e2e", Argv: o.Command, Dir: dir, Parser: "js"}}
	} else {
		plan = DetectE2E(dir)
	}
	if !plan.Defined {
		return skip("", plan.Reason), nil
	}
	spec := plan.Spec
	spec.Env = append(append([]string(nil), spec.Env...), o.Env...)
	spec.LogDir = o.LogDir
	spec.Timeout = o.Timeout
	if spec.Timeout <= 0 {
		spec.Timeout = 15 * time.Minute
	}
	started := time.Now().Add(-2 * time.Second)
	res, err := Run(ctx, pm, spec, onLine)
	if err != nil {
		return E2EResult{Result: res, Source: plan.Source}, err
	}
	out := E2EResult{Result: res, Source: plan.Source}
	if sum, shots := readPlaywrightReport(dir, started); sum != nil {
		out.Summary = sum
		if len(sum.Failed) > 0 {
			out.FailingTests = sum.Failed
		}
		out.Screenshots = copyScreenshots(shots, o.LogDir)
		// a green exit with unexpected results would be a contradiction: trust the report over the exit code, loudly
		if res.Passed && sum.Unexpected > 0 {
			out.Passed = false
			out.Output = fmt.Sprintf("the e2e command exited 0 but its report lists %d unexpected result(s)\n", sum.Unexpected) + out.Output
		}
	}
	if len(out.FailingTests) == 0 && !res.Passed {
		out.FailingTests = parsePlaywrightList(res.Output)
	}
	return out, nil
}

// Playwright's JSON reporter shape, reduced to what we read.
type pwSpec struct {
	Title string `json:"title"`
	File  string `json:"file"`
	Tests []struct {
		Status  string `json:"status"` // expected | unexpected | flaky | skipped
		Results []struct {
			Attachments []struct {
				Name        string `json:"name"`
				ContentType string `json:"contentType"`
				Path        string `json:"path"`
			} `json:"attachments"`
		} `json:"results"`
	} `json:"tests"`
}

type pwSuite struct {
	Title  string    `json:"title"`
	Specs  []pwSpec  `json:"specs"`
	Suites []pwSuite `json:"suites"`
}

func readPlaywrightReport(dir string, notBefore time.Time) (*PlaywrightSummary, []string) {
	for _, rel := range reportCandidates {
		p := filepath.Join(dir, rel)
		st, err := os.Stat(p)
		if err != nil || st.ModTime().Before(notBefore) {
			continue // a report from an earlier run says nothing about this one
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var rep struct {
			Suites []pwSuite `json:"suites"`
		}
		if json.Unmarshal(b, &rep) != nil || rep.Suites == nil {
			continue
		}
		sum := &PlaywrightSummary{}
		var shots []string
		var walk func(s pwSuite)
		walk = func(s pwSuite) {
			for _, sp := range s.Specs {
				for _, t := range sp.Tests {
					switch t.Status {
					case "expected":
						sum.Expected++
					case "unexpected":
						sum.Unexpected++
						sum.Failed = append(sum.Failed, strings.TrimSpace(sp.File+" > "+sp.Title))
					case "flaky":
						sum.Flaky++
					case "skipped":
						sum.Skipped++
					}
					for _, r := range t.Results {
						for _, a := range r.Attachments {
							if a.Path != "" && strings.HasPrefix(a.ContentType, "image/") {
								shots = append(shots, a.Path)
							}
						}
					}
				}
			}
			for _, c := range s.Suites {
				walk(c)
			}
		}
		for _, s := range rep.Suites {
			walk(s)
		}
		sort.Strings(sum.Failed)
		return sum, shots
	}
	return nil, nil
}

// copyScreenshots keeps up to 20 screenshots (each at most 5 MiB) under logDir so the evidence outlives the worktree.
func copyScreenshots(paths []string, logDir string) []string {
	if len(paths) == 0 || logDir == "" {
		return nil
	}
	dst := filepath.Join(logDir, "e2e-screenshots")
	if os.MkdirAll(dst, 0o755) != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range paths {
		if len(out) >= 20 || seen[p] {
			continue
		}
		seen[p] = true
		st, err := os.Stat(p)
		if err != nil || st.IsDir() || st.Size() > 5<<20 {
			continue
		}
		in, err := os.Open(p)
		if err != nil {
			continue
		}
		name := fmt.Sprintf("%02d-%s", len(out)+1, filepath.Base(p))
		w, err := os.Create(filepath.Join(dst, name))
		if err != nil {
			in.Close()
			continue
		}
		_, cerr := io.Copy(w, in)
		in.Close()
		w.Close()
		if cerr == nil {
			out = append(out, filepath.Join(dst, name))
		}
	}
	return out
}

var pwListFail = regexp.MustCompile(`(?m)^\s*(?:✘|×|✗|x)\s+\d+\s+(.+?)(?:\s+\(\d[\d.]*m?s\))?\s*$`)

// parsePlaywrightList extracts failing test titles from the list reporter's lines (`  ✘  1 [chromium] › file:3:1 › title (1s)`).
func parsePlaywrightList(out string) []string {
	set := map[string]bool{}
	for _, m := range pwListFail.FindAllStringSubmatch(out, -1) {
		set[strings.TrimSpace(m[1])] = true
	}
	var names []string
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

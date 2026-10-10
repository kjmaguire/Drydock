package contract

import (
	"bufio"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Test statuses, as go test reports them, plus one for a test that never reported.
const (
	StatusPass    = "pass"
	StatusFail    = "fail"
	StatusSkip    = "skip"
	StatusMissing = "missing" // the test did not run: not found, or the package did not build
)

// TestResult is the outcome of one test or subtest in a `go test -json` run.
type TestResult struct {
	Package string   `json:"package"`
	Test    string   `json:"test"`
	Status  string   `json:"status"`
	Elapsed float64  `json:"elapsed"` // seconds
	Output  []string `json:"output"`  // the test's own lines (what it logged, why it skipped or failed), without go test's framing
}

// Run is everything one `go test -json` invocation reported.
type Run struct {
	Tests map[string]*TestResult // by test name (Test or Test/Subtest)
	// Package is the package-level outcome ("pass", "fail", "skip") and Output its own lines: build errors, panics outside a
	// test, "no tests to run".
	Package       string
	PackageOutput []string
	BuildFailed   bool
}

type goTestEvent struct {
	Action      string
	Package     string
	ImportPath  string
	Test        string
	Elapsed     float64
	Output      string
	OutputType  string // "frame" marks go test's own framing (Go 1.25 and later), "error" a failure message
	FailedBuild string
}

// ParseGoTestJSON reads the output of `go test -json`. Lines that are not JSON (a toolchain message before the stream) are
// kept as package output.
func ParseGoTestJSON(r io.Reader) (*Run, error) {
	run := &Run{Tests: map[string]*TestResult{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var ev goTestEvent
		if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &ev) != nil {
			if s := strings.TrimRight(string(line), "\r\n"); strings.TrimSpace(s) != "" {
				run.PackageOutput = append(run.PackageOutput, s)
			}
			continue
		}
		switch ev.Action {
		case "build-output":
			run.PackageOutput = append(run.PackageOutput, strings.TrimRight(ev.Output, "\n"))
			continue
		case "build-fail":
			run.BuildFailed = true
			continue
		}
		framing := ev.OutputType == "frame"
		if ev.Test == "" {
			switch ev.Action {
			case "output":
				if s := strings.TrimRight(ev.Output, "\n"); s != "" && !framing && !frameLine(s) {
					run.PackageOutput = append(run.PackageOutput, s)
				}
			case StatusPass, StatusFail, StatusSkip:
				run.Package = ev.Action
				if ev.FailedBuild != "" {
					run.BuildFailed = true
				}
			}
			continue
		}
		tr := run.Tests[ev.Test]
		if tr == nil {
			tr = &TestResult{Package: ev.Package, Test: ev.Test, Status: StatusMissing}
			run.Tests[ev.Test] = tr
		}
		switch ev.Action {
		case "output":
			if s := strings.TrimRight(ev.Output, "\n"); s != "" && !framing && !frameLine(s) {
				tr.Output = append(tr.Output, s)
			}
		case StatusPass, StatusFail, StatusSkip:
			tr.Status, tr.Elapsed = ev.Action, ev.Elapsed
		}
	}
	return run, sc.Err()
}

// frameLine is go test's own framing ("=== RUN", "--- PASS: ...", "PASS", "ok  pkg"), which says nothing a report needs.
func frameLine(s string) bool {
	t := strings.TrimSpace(s)
	for _, p := range []string{"=== RUN", "=== PAUSE", "=== CONT", "=== NAME", "--- PASS", "--- FAIL", "--- SKIP"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return t == "PASS" || t == "FAIL" || strings.HasPrefix(t, "ok  \t") || strings.HasPrefix(t, "FAIL\t") || strings.HasPrefix(t, "?   \t")
}

// Lookup finds a test's result. A subtest that never reported falls back to a parent that skipped or failed, so a parent that
// stopped before starting its subtests (no CLI installed) explains why; a parent that passed says nothing about a subtest
// that did not run (it may have been renamed).
func (r *Run) Lookup(name string) (*TestResult, bool) {
	if tr, ok := r.Tests[name]; ok && tr.Status != StatusMissing {
		return tr, true
	}
	if parent, _, ok := strings.Cut(name, "/"); ok {
		if tr, ok := r.Tests[parent]; ok && (tr.Status == StatusSkip || tr.Status == StatusFail) {
			return tr, true
		}
	}
	return nil, false
}

var logPrefix = regexp.MustCompile(`^\s*[\w.-]+\.go:\d+: ?`)

// Messages strips the file:line prefix go test puts before each logged line.
func Messages(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, strings.TrimRight(logPrefix.ReplaceAllString(l, ""), " "))
	}
	return out
}

// Tagged returns the logged messages that start with prefix (NotePrefix, WarningPrefix), without it.
func Tagged(lines []string, prefix string) []string {
	var out []string
	for _, m := range Messages(lines) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(m), prefix); ok {
			out = append(out, strings.TrimSpace(rest))
		}
	}
	return out
}

// Reason is why a test skipped: its last logged message.
func Reason(lines []string) string {
	ms := Messages(lines)
	for i := len(ms) - 1; i >= 0; i-- {
		if m := strings.TrimSpace(ms[i]); m != "" && !strings.HasPrefix(m, NotePrefix) && !strings.HasPrefix(m, WarningPrefix) && !strings.HasPrefix(m, SpendPrefix) {
			return m
		}
	}
	return ""
}

// Excerpt is the end of a failing test's output: at most maxLines lines and maxBytes bytes, notes and spend lines left out.
func Excerpt(lines []string, maxLines, maxBytes int) string {
	var keep []string
	for _, m := range Messages(lines) {
		t := strings.TrimSpace(m)
		if t == "" || strings.HasPrefix(t, NotePrefix) || strings.HasPrefix(t, SpendPrefix) {
			continue
		}
		keep = append(keep, m)
	}
	if len(keep) > maxLines {
		keep = keep[len(keep)-maxLines:]
	}
	s := strings.Join(keep, "\n")
	if len(s) > maxBytes {
		i := len(s) - maxBytes
		for i < len(s) && !utf8.RuneStart(s[i]) {
			i++
		}
		s = "..." + s[i:]
	}
	return s
}

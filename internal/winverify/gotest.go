package winverify

import (
	"bufio"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// TestResult is one test or subtest of a test binary run.
type TestResult struct {
	Package    string `json:"package,omitempty"`
	Name       string `json:"name"`
	Status     Status `json:"status"`
	DurationMs int64  `json:"durationMs"`
	// Reason is the skip message for a skipped test and the first error line for a failed one.
	Reason string `json:"reason,omitempty"`
	// Output holds the last lines a failed test printed (empty for passed and skipped tests).
	Output []string `json:"output,omitempty"`

	lines  []string // everything the test printed, in order
	errors []string // lines the testing package marked as errors (test2json framing)
	ended  bool
}

// TestRun is what one test binary printed, parsed.
type TestRun struct {
	Tests []TestResult // in the order they started; subtests are named Parent/Sub
	Final string       // "PASS" or "FAIL" as printed last, "" when the binary stopped before printing one
	// NoTests is set when the binary said "testing: warning: no tests to run".
	NoTests bool
	// Crash is the first "panic:" or "fatal error:" line printed outside a test's own output (a timeout is a panic).
	Crash string
}

// Framing bytes of `-test.v=test2json` (testing package): a line that starts with the marker is a framing line; error
// output is wrapped in the begin and end bytes.
const (
	frameMarker = '\x16'
	errBegin    = '\x0f'
	errEnd      = '\x0e'
)

var (
	resultLine = regexp.MustCompile(`^(\s*)--- (PASS|FAIL|SKIP): (\S+) \((\d+(?:\.\d+)?)s\)`)
	headerLine = regexp.MustCompile(`^=== (RUN|PAUSE|CONT|NAME)\s*(\S*)\s*$`)
	logPrefix  = regexp.MustCompile(`^\s+\S+\.go:\d+: `)
)

// ParseTestOutput reads the output of a test binary run with -test.v=test2json (preferred: framing lines are marked, so
// a test that prints "--- FAIL:" in its own log cannot be mistaken for a result) or with plain -test.v.
func ParseTestOutput(r io.Reader) TestRun {
	var run TestRun
	byName := map[string]int{}
	framed := false
	current := ""
	start := func(name string) *TestResult {
		if i, ok := byName[name]; ok {
			return &run.Tests[i]
		}
		run.Tests = append(run.Tests, TestResult{Name: name})
		byName[name] = len(run.Tests) - 1
		return &run.Tests[len(run.Tests)-1]
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		isFrame := false
		if strings.HasPrefix(line, string(frameMarker)) {
			framed, isFrame = true, true
			line = line[1:]
		} else if !framed {
			isFrame = headerLine.MatchString(line) || resultLine.MatchString(line) || line == "PASS" || line == "FAIL"
		}
		isErr := strings.ContainsRune(line, errBegin)
		line = strings.NewReplacer(string(errBegin), "", string(errEnd), "").Replace(line)
		if isFrame {
			if m := headerLine.FindStringSubmatch(line); m != nil {
				switch m[1] {
				case "RUN":
					start(m[2])
					current = m[2]
				case "CONT", "NAME":
					current = m[2]
				}
				continue
			}
			if m := resultLine.FindStringSubmatch(line); m != nil {
				t := start(m[3])
				t.Status = map[string]Status{"PASS": Pass, "FAIL": Fail, "SKIP": Skip}[m[2]]
				if secs, err := strconv.ParseFloat(m[4], 64); err == nil {
					t.DurationMs = int64(secs * 1000)
				}
				t.ended = true
				current = parentOf(m[3])
				continue
			}
			if line == "PASS" || line == "FAIL" {
				run.Final = line
				continue
			}
		}
		switch {
		case strings.HasPrefix(line, "testing: warning: no tests to run"):
			run.NoTests = true
			continue
		case run.Crash == "" && (strings.HasPrefix(line, "panic: ") || strings.HasPrefix(line, "fatal error: ")):
			run.Crash = line
		}
		if current == "" {
			continue
		}
		t := start(current)
		t.lines = append(t.lines, line)
		if isErr {
			t.errors = append(t.errors, strings.TrimSpace(line))
		}
	}
	for i := range run.Tests {
		t := &run.Tests[i]
		switch {
		case !t.ended:
			t.Status = Fail
			t.Reason = "did not finish: the test binary stopped while it ran"
			if run.Crash != "" {
				t.Reason += " (" + run.Crash + ")"
			}
		case t.Status == Skip:
			t.Reason = skipReason(t.lines)
		case t.Status == Fail:
			t.Reason = failReason(t)
		}
		if t.Status == Fail {
			t.Output = tail(t.lines, 30)
		}
	}
	return run
}

func parentOf(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[:i]
	}
	return ""
}

// skipReason is the last message the test logged (t.Skip logs its message last), without the file:line prefix.
func skipReason(lines []string) string {
	start := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if logPrefix.MatchString(lines[i]) {
			start = i
			break
		}
	}
	if start < 0 {
		return "skipped (no reason printed)"
	}
	parts := []string{logPrefix.ReplaceAllString(lines[start], "")}
	for _, l := range lines[start+1:] {
		parts = append(parts, strings.TrimSpace(l))
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func failReason(t *TestResult) string {
	if len(t.errors) > 0 {
		return t.errors[0]
	}
	for _, l := range t.lines {
		if logPrefix.MatchString(l) {
			return strings.TrimSpace(l)
		}
	}
	if len(t.lines) > 0 {
		return strings.TrimSpace(t.lines[len(t.lines)-1])
	}
	return "failed (no message printed)"
}

func tail(lines []string, n int) []string {
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return append([]string(nil), lines...)
}

// TestCounts counts top-level tests (a failing subtest fails its parent, so subtests are not counted twice).
type TestCounts struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

func (c TestCounts) String() string {
	return itoa(c.Passed) + " passed, " + itoa(c.Failed) + " failed, " + itoa(c.Skipped) + " skipped"
}

// Count returns the counts over top-level tests.
func Count(ts []TestResult) TestCounts {
	var c TestCounts
	for _, t := range ts {
		if strings.Contains(t.Name, "/") {
			continue
		}
		switch t.Status {
		case Pass:
			c.Passed++
		case Fail:
			c.Failed++
		case Skip:
			c.Skipped++
		}
	}
	return c
}

// Judge turns one or more test binary runs into a step status and reason. runErr is set when a binary could not be
// started or its exit was abnormal (non-zero exit code, killed after its time limit).
func Judge(runs []BinaryRun, goos string) (Status, string) {
	var all []TestResult
	var problems []string
	for _, r := range runs {
		all = append(all, r.Parsed.Tests...)
		switch {
		case r.StartErr != "":
			problems = append(problems, r.Package+": could not start the test binary: "+r.StartErr)
		case r.Parsed.Crash != "":
			problems = append(problems, r.Package+": the test binary stopped: "+r.Parsed.Crash)
		case r.ExitErr != "" && countFailed(r.Parsed.Tests) == 0:
			problems = append(problems, r.Package+": the test binary "+r.ExitErr+" without a failing test (output tail is in the log)")
		}
	}
	c := Count(all)
	if c.Failed > 0 {
		var names []string
		for _, t := range all {
			if t.Status == Fail && !strings.Contains(t.Name, "/") {
				names = append(names, t.Name)
			}
		}
		reason := itoa(c.Failed) + " test(s) failed: " + joinMax(names, 8)
		if len(problems) > 0 {
			reason += "; " + strings.Join(problems, "; ")
		}
		return Fail, reason
	}
	if len(problems) > 0 {
		return Fail, strings.Join(problems, "; ")
	}
	if c.Passed > 0 {
		return Pass, ""
	}
	if c.Skipped > 0 {
		var reasons []string
		seen := map[string]bool{}
		for _, t := range all {
			if t.Status == Skip && !seen[t.Reason] {
				seen[t.Reason] = true
				reasons = append(reasons, t.Name+": "+t.Reason)
			}
		}
		return Skip, "every test skipped itself here: " + joinMax(reasons, 3)
	}
	return Skip, "no tests in this package run on " + goos + " (its test files are built for other systems only)"
}

func countFailed(ts []TestResult) int { return Count(ts).Failed }

func joinMax(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + " and " + itoa(len(items)-n) + " more"
}

// BinaryRun is one execution of one test binary.
type BinaryRun struct {
	Package  string
	Args     []string
	Duration time.Duration
	ExitErr  string // "exited with code 1", "was stopped after 20m0s" ("" for a zero exit)
	StartErr string
	Parsed   TestRun
	Tail     string // the last lines the binary printed
}

// testExcerpt summarises test results for the report: counts, then each failed test with its last lines, then skipped
// tests with their reasons.
func testExcerpt(ts []TestResult, maxLines int) string {
	var b strings.Builder
	b.WriteString(Count(ts).String() + " (top-level tests)\n")
	lines := 1
	add := func(s string) bool {
		if lines >= maxLines {
			return false
		}
		b.WriteString(s + "\n")
		lines++
		return true
	}
	sorted := append([]TestResult(nil), ts...)
	sort.SliceStable(sorted, func(i, j int) bool { return rank(sorted[i].Status) < rank(sorted[j].Status) })
	for _, t := range sorted {
		name := t.Name
		if t.Package != "" {
			name = t.Package + " " + t.Name
		}
		switch t.Status {
		case Fail:
			if !add("FAIL " + name + ": " + t.Reason) {
				break
			}
			for _, l := range tail(t.Output, 12) {
				if !add("    " + strings.TrimSpace(l)) {
					break
				}
			}
		case Skip:
			add("SKIP " + name + ": " + t.Reason)
		}
	}
	if lines >= maxLines {
		b.WriteString("[more in the log]\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func rank(s Status) int {
	switch s {
	case Fail:
		return 0
	case Skip:
		return 1
	}
	return 2
}

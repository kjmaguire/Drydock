package winverify

import (
	"strings"
	"testing"
)

// framedOutput is what a real go1.27 test binary printed with -test.v=test2json (captured from a scratch package with
// passing, skipped, failing, sub- and parallel tests). \x16 marks framing lines; \x0f and \x0e wrap error output.
// TestPass logs a line that looks like a result ("--- FAIL: Fake") to prove the framing keeps it out of the results.
const framedOutput = "\x16=== RUN   TestPass\n" +
	"    x_test.go:8: hello\n" +
	"        --- FAIL: Fake (0.00s)\n" +
	"        === RUN Fake\n" +
	"\x16--- PASS: TestPass (0.00s)\n" +
	"\x16=== NAME  \n" +
	"\x16=== RUN   TestSkip\n" +
	"    x_test.go:9: needs something\n" +
	"\x16--- SKIP: TestSkip (0.00s)\n" +
	"\x16=== NAME  \n" +
	"\x16=== RUN   TestSkipf\n" +
	"    x_test.go:10: reason 2\n" +
	"\x16--- SKIP: TestSkipf (0.00s)\n" +
	"\x16=== NAME  \n" +
	"\x16=== RUN   TestFail\n" +
	"\x0f    x_test.go:11: boom\x0e\n" +
	"    x_test.go:11: more\n" +
	"\x16--- FAIL: TestFail (0.00s)\n" +
	"\x16=== NAME  \n" +
	"\x16=== RUN   TestSub\n" +
	"\x16=== RUN   TestSub/a\n" +
	"\x16--- PASS: TestSub/a (0.00s)\n" +
	"\x16=== NAME  TestSub\n" +
	"\x16=== RUN   TestSub/b\n" +
	"\x0f    x_test.go:14: bad b\x0e\n" +
	"\x16--- FAIL: TestSub/b (0.00s)\n" +
	"\x16=== NAME  TestSub\n" +
	"\x16=== RUN   TestSub/c\n" +
	"    x_test.go:15: skip c\n" +
	"\x16--- SKIP: TestSub/c (0.00s)\n" +
	"\x16=== NAME  TestSub\n" +
	"\x16--- FAIL: TestSub (0.00s)\n" +
	"\x16=== NAME  \n" +
	"\x16=== RUN   TestPar1\n" +
	"\x16=== PAUSE TestPar1\n" +
	"\x16=== NAME  \n" +
	"\x16=== RUN   TestPar2\n" +
	"\x16=== PAUSE TestPar2\n" +
	"\x16=== NAME  \n" +
	"\x16=== RUN   TestPrint\n" +
	"stderr line\n" +
	"\x16--- PASS: TestPrint (0.00s)\n" +
	"\x16=== NAME  \n" +
	"\x16=== CONT  TestPar1\n" +
	"\x16=== CONT  TestPar2\n" +
	"    x_test.go:18: p2\n" +
	"\x16--- PASS: TestPar2 (0.01s)\n" +
	"\x16=== NAME  TestPar1\n" +
	"    x_test.go:17: p1\n" +
	"\x16--- PASS: TestPar1 (0.02s)\n" +
	"\x16FAIL\n"

// plainOutput is the same binary with plain -test.v (without the logged look-alike line, which plain output cannot tell
// apart from a result: that is why drydock-verify asks for the framing).
const plainOutput = `=== RUN   TestPass
    x_test.go:8: hello
--- PASS: TestPass (0.00s)
=== RUN   TestSkip
    x_test.go:9: needs something
--- SKIP: TestSkip (0.00s)
=== RUN   TestFail
    x_test.go:11: boom
    x_test.go:11: more
--- FAIL: TestFail (0.00s)
=== RUN   TestSub
=== RUN   TestSub/a
=== RUN   TestSub/b
    x_test.go:14: bad b
=== RUN   TestSub/c
    x_test.go:15: skip c
--- FAIL: TestSub (0.00s)
    --- PASS: TestSub/a (0.00s)
    --- FAIL: TestSub/b (0.00s)
    --- SKIP: TestSub/c (0.00s)
=== RUN   TestPar1
=== PAUSE TestPar1
=== CONT  TestPar1
    x_test.go:17: p1
--- PASS: TestPar1 (0.02s)
FAIL
`

func byName(t *testing.T, run TestRun) map[string]TestResult {
	t.Helper()
	m := map[string]TestResult{}
	for _, r := range run.Tests {
		if _, dup := m[r.Name]; dup {
			t.Fatalf("test %s listed twice", r.Name)
		}
		m[r.Name] = r
	}
	return m
}

func TestParseFramedOutputAttributesEveryLineAndResult(t *testing.T) {
	run := ParseTestOutput(strings.NewReader(framedOutput))
	if run.Final != "FAIL" || run.Crash != "" || run.NoTests {
		t.Fatalf("final=%q crash=%q noTests=%v", run.Final, run.Crash, run.NoTests)
	}
	got := byName(t, run)
	want := map[string]Status{"TestPass": Pass, "TestSkip": Skip, "TestSkipf": Skip, "TestFail": Fail, "TestSub": Fail, "TestSub/a": Pass,
		"TestSub/b": Fail, "TestSub/c": Skip, "TestPar1": Pass, "TestPar2": Pass, "TestPrint": Pass}
	if len(got) != len(want) {
		t.Fatalf("got %d tests, want %d: %v", len(got), len(want), run.Tests)
	}
	for name, st := range want {
		if got[name].Status != st {
			t.Errorf("%s: %s, want %s", name, got[name].Status, st)
		}
	}
	if _, ok := got["Fake"]; ok {
		t.Fatal("a logged line that looks like a result became a test")
	}
	if r := got["TestSkip"].Reason; r != "needs something" {
		t.Errorf("skip reason %q", r)
	}
	if r := got["TestSkipf"].Reason; r != "reason 2" {
		t.Errorf("skip reason %q", r)
	}
	if r := got["TestSub/c"].Reason; r != "skip c" {
		t.Errorf("subtest skip reason %q", r)
	}
	if r := got["TestFail"].Reason; r != "x_test.go:11: boom" {
		t.Errorf("fail reason %q (the first marked error line)", r)
	}
	if out := strings.Join(got["TestFail"].Output, "\n"); !strings.Contains(out, "boom") || !strings.Contains(out, "more") {
		t.Errorf("failed test output %q", out)
	}
	if got["TestPass"].Output != nil {
		t.Error("only failed tests keep their output")
	}
	if got["TestPar1"].DurationMs != 20 || got["TestPar2"].DurationMs != 10 {
		t.Errorf("durations %d %d", got["TestPar1"].DurationMs, got["TestPar2"].DurationMs)
	}
	if c := Count(run.Tests); c != (TestCounts{Passed: 4, Failed: 2, Skipped: 2}) {
		t.Errorf("top-level counts %+v", c)
	}
}

func TestParsePlainVerboseOutput(t *testing.T) {
	run := ParseTestOutput(strings.NewReader(plainOutput))
	got := byName(t, run)
	want := map[string]Status{"TestPass": Pass, "TestSkip": Skip, "TestFail": Fail, "TestSub": Fail, "TestSub/a": Pass, "TestSub/b": Fail,
		"TestSub/c": Skip, "TestPar1": Pass}
	if len(got) != len(want) || run.Final != "FAIL" {
		t.Fatalf("got %v final %q", run.Tests, run.Final)
	}
	for name, st := range want {
		if got[name].Status != st {
			t.Errorf("%s: %s, want %s", name, got[name].Status, st)
		}
	}
	if got["TestSkip"].Reason != "needs something" || got["TestSub/c"].Reason != "skip c" {
		t.Errorf("reasons %q %q", got["TestSkip"].Reason, got["TestSub/c"].Reason)
	}
	if got["TestFail"].Reason != "x_test.go:11: boom" {
		t.Errorf("fail reason %q", got["TestFail"].Reason)
	}
}

func TestATimeoutMarksTheRunningTestAndFailsTheStep(t *testing.T) {
	out := "\x16=== RUN   TestQuick\n\x16--- PASS: TestQuick (0.00s)\n\x16=== NAME  \n\x16=== RUN   TestSlow\n" +
		"panic: test timed out after 1s\n\trunning tests:\n\t\tTestSlow (1s)\n\ngoroutine 7 [running]:\n"
	run := ParseTestOutput(strings.NewReader(out))
	got := byName(t, run)
	if run.Crash != "panic: test timed out after 1s" || run.Final != "" {
		t.Fatalf("crash %q final %q", run.Crash, run.Final)
	}
	if got["TestSlow"].Status != Fail || !strings.Contains(got["TestSlow"].Reason, "did not finish") || !strings.Contains(got["TestSlow"].Reason, "timed out") {
		t.Fatalf("TestSlow %+v", got["TestSlow"])
	}
	st, reason := Judge([]BinaryRun{{Package: "internal/x", ExitErr: "exited with code 2", Parsed: run}}, "windows")
	if st != Fail || !strings.Contains(reason, "TestSlow") || !strings.Contains(reason, "stopped") {
		t.Fatalf("%s: %s", st, reason)
	}
}

func TestSkipReasonJoinsContinuationLines(t *testing.T) {
	out := "\x16=== RUN   TestX\n    x_test.go:3: first log\n    x_test.go:4: cannot build the helper: exit status 1\n        go: not found\n" +
		"\x16--- SKIP: TestX (0.01s)\n\x16PASS\n"
	r := ParseTestOutput(strings.NewReader(out)).Tests[0]
	if r.Status != Skip || r.Reason != "cannot build the helper: exit status 1 go: not found" {
		t.Fatalf("%+v", r)
	}
}

func TestJudge(t *testing.T) {
	pass := TestRun{Final: "PASS", Tests: []TestResult{{Name: "TestA", Status: Pass}, {Name: "TestB", Status: Skip, Reason: "windows only"}}}
	if st, reason := Judge([]BinaryRun{{Package: "p", Parsed: pass}}, "linux"); st != Pass || reason != "" {
		t.Errorf("passing run: %s %q", st, reason)
	}
	allSkipped := TestRun{Final: "PASS", Tests: []TestResult{{Name: "TestB", Status: Skip, Reason: "windows only"}}}
	if st, reason := Judge([]BinaryRun{{Package: "p", Parsed: allSkipped}}, "linux"); st != Skip || !strings.Contains(reason, "windows only") {
		t.Errorf("all skipped: %s %q", st, reason)
	}
	none := TestRun{Final: "PASS", NoTests: true}
	if st, reason := Judge([]BinaryRun{{Package: "p", Parsed: none}}, "windows"); st != Skip || !strings.Contains(reason, "no tests in this package run on windows") {
		t.Errorf("no tests: %s %q", st, reason)
	}
	// a binary that exits non-zero without any failing test (TestMain gave up, os.Exit in a test) is a failure
	if st, reason := Judge([]BinaryRun{{Package: "p", ExitErr: "exited with code 3", Parsed: pass}}, "linux"); st != Fail || !strings.Contains(reason, "exited with code 3") {
		t.Errorf("odd exit: %s %q", st, reason)
	}
	if st, reason := Judge([]BinaryRun{{Package: "p", StartErr: "access is denied"}}, "windows"); st != Fail || !strings.Contains(reason, "could not start") {
		t.Errorf("start error: %s %q", st, reason)
	}
	failed := TestRun{Final: "FAIL", Tests: []TestResult{{Name: "TestA", Status: Fail}, {Name: "TestA/sub", Status: Fail}, {Name: "TestB", Status: Pass}}}
	if st, reason := Judge([]BinaryRun{{Package: "p", ExitErr: "exited with code 1", Parsed: failed}}, "linux"); st != Fail || reason != "1 test(s) failed: TestA" {
		t.Errorf("failed: %s %q", st, reason)
	}
}

func TestExcerptListsFailuresFirstWithTheirLastLines(t *testing.T) {
	ts := []TestResult{{Name: "TestOK", Status: Pass}, {Name: "TestSkipped", Status: Skip, Reason: "needs Windows"},
		{Name: "TestBroken", Status: Fail, Reason: "x_test.go:9: boom", Output: []string{"    x_test.go:9: boom", "    x_test.go:10: detail"}}}
	ex := testExcerpt(ts, 60)
	if !strings.HasPrefix(ex, "1 passed, 1 failed, 1 skipped") {
		t.Fatalf("excerpt starts %q", ex)
	}
	if i, j := strings.Index(ex, "FAIL TestBroken"), strings.Index(ex, "SKIP TestSkipped"); i < 0 || j < 0 || i > j || !strings.Contains(ex, "x_test.go:10: detail") {
		t.Fatalf("excerpt:\n%s", ex)
	}
	if strings.Contains(ex, "TestOK") {
		t.Fatal("passing tests are counted, not listed")
	}
	many := make([]TestResult, 200)
	for i := range many {
		many[i] = TestResult{Name: "TestS" + itoa(i), Status: Skip, Reason: "r"}
	}
	if ex := testExcerpt(many, 10); strings.Count(ex, "\n") > 11 || !strings.Contains(ex, "[more in the log]") {
		t.Fatalf("a long excerpt is cut:\n%s", ex)
	}
}

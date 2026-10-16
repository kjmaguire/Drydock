package winverify

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeStep is a TEST DOUBLE for a verification step: it returns a fixed outcome (or does what fn says) so the orchestration
// can be tested without running anything real. Real steps are exercised by the smoke and bundle tests.
func fakeStep(id string, fn func(ctx context.Context, sc *StepContext) Outcome) Step {
	return Step{ID: id, Title: "fake " + id, Proves: "TEST DOUBLE step " + id + " proves nothing.", Run: fn}
}

func runFakes(t *testing.T, env *Env, steps ...Step) ([]StepResult, string, string) {
	t.Helper()
	logs := t.TempDir()
	var progress bytes.Buffer
	return RunSteps(context.Background(), steps, env, logs, &progress), logs, progress.String()
}

func TestRunStepsKeepsOrderStatusesLogsAndTimes(t *testing.T) {
	steps := []Step{
		fakeStep("one", func(ctx context.Context, sc *StepContext) Outcome {
			sc.Logf("hello from one")
			time.Sleep(30 * time.Millisecond)
			return Outcome{Status: Pass, Excerpt: "fine"}
		}),
		fakeStep("tests:internal/two", func(ctx context.Context, sc *StepContext) Outcome {
			return Outcome{Status: Skip, Reason: "not on this system"}
		}),
		fakeStep("three", func(ctx context.Context, sc *StepContext) Outcome {
			return Outcome{Status: Fail, Reason: "it broke", Tests: []TestResult{{Name: "TestA", Status: Fail}, {Name: "TestB", Status: Pass}}}
		}),
	}
	res, logs, progress := runFakes(t, &Env{}, steps...)
	if len(res) != 3 || res[0].ID != "one" || res[1].ID != "tests:internal/two" || res[2].ID != "three" {
		t.Fatalf("%+v", res)
	}
	if res[0].Status != Pass || res[1].Status != Skip || res[1].Reason != "not on this system" || res[2].Status != Fail {
		t.Fatalf("statuses %+v", res)
	}
	if res[0].DurationMs < 30 {
		t.Errorf("duration %dms", res[0].DurationMs)
	}
	if res[2].Counts == nil || *res[2].Counts != (TestCounts{Passed: 1, Failed: 1}) || len(res[2].Tests) != 2 {
		t.Errorf("counts %+v tests %+v", res[2].Counts, res[2].Tests)
	}
	if res[1].Log != "logs/tests_internal_two.log" {
		t.Errorf("log name %q", res[1].Log)
	}
	b, err := os.ReadFile(filepath.Join(logs, "one.log"))
	if err != nil || !strings.Contains(string(b), "hello from one") || !strings.Contains(string(b), "result: pass") {
		t.Fatalf("log: %v\n%s", err, b)
	}
	for _, want := range []string{"[ 1/3] one", "[ 2/3] tests:internal/two", "skip", "not on this system", "fail", "1 passed, 1 failed"} {
		if !strings.Contains(progress, want) {
			t.Errorf("progress lacks %q:\n%s", want, progress)
		}
	}
	if s := Summarize(res); s != (Summary{Passed: 1, Failed: 1, Skipped: 1}) {
		t.Errorf("summary %+v", s)
	}
}

func TestASkipOrFailWithoutAReasonIsAFailureThatSaysSo(t *testing.T) {
	res, _, _ := runFakes(t, &Env{},
		fakeStep("silent-skip", func(context.Context, *StepContext) Outcome { return Outcome{Status: Skip} }),
		fakeStep("silent-fail", func(context.Context, *StepContext) Outcome { return Outcome{Status: Fail, Reason: "  "} }),
		fakeStep("odd", func(context.Context, *StepContext) Outcome { return Outcome{Status: "maybe"} }),
	)
	for _, r := range res {
		if r.Status != Fail || !strings.Contains(r.Reason, "bug in drydock-verify") {
			t.Errorf("%s: %s %q", r.ID, r.Status, r.Reason)
		}
	}
}

func TestAPanickingStepFailsWithThePanicAndTheRunGoesOn(t *testing.T) {
	res, logs, _ := runFakes(t, &Env{},
		fakeStep("boom", func(context.Context, *StepContext) Outcome { panic("index out of range") }),
		fakeStep("after", func(context.Context, *StepContext) Outcome { return Outcome{Status: Pass} }),
	)
	if res[0].Status != Fail || !strings.Contains(res[0].Reason, "crashed") || !strings.Contains(res[0].Reason, "index out of range") {
		t.Fatalf("%+v", res[0])
	}
	if res[1].Status != Pass {
		t.Fatal("the next step must still run")
	}
	b, _ := os.ReadFile(filepath.Join(logs, "boom.log"))
	if !strings.Contains(string(b), "PANIC") || !strings.Contains(string(b), "goroutine") {
		t.Fatalf("the log must hold the stack:\n%s", b)
	}
}

func TestACancelledRunSkipsTheRemainingSteps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ran := false
	res := RunSteps(ctx, []Step{
		fakeStep("first", func(context.Context, *StepContext) Outcome { cancel(); return Outcome{Status: Pass} }),
		fakeStep("second", func(context.Context, *StepContext) Outcome { ran = true; return Outcome{Status: Pass} }),
	}, &Env{}, t.TempDir(), nil)
	if ran || res[1].Status != Skip || !strings.Contains(res[1].Reason, "stopped") {
		t.Fatalf("ran=%v %+v", ran, res[1])
	}
}

func TestSecretsAndTheHomeFolderNeverReachResultsOrLogs(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || len(home) < 4 {
		t.Skip("no usable home folder here")
	}
	const key = "sk-ant-api03-abcdefghijklmnop"
	res, logs, _ := runFakes(t, &Env{}, fakeStep("leaky", func(ctx context.Context, sc *StepContext) Outcome {
		sc.Logf("calling http://127.0.0.1:1234/?token=0123456789abcdef with %s from %s", key, filepath.Join(home, "project"))
		return Outcome{Status: Fail, Reason: "Authorization: Bearer abcdefghijklmnopqrstuvwxyz0123", Excerpt: "key " + key,
			Tests: []TestResult{{Name: "TestX", Status: Fail, Reason: "ghp_abcdefghijklmnopqrstuvwxyz0123456789", Output: []string{"password=hunter2"}}}}
	}))
	r := res[0]
	b, _ := os.ReadFile(filepath.Join(logs, "leaky.log"))
	all := string(b) + r.Reason + r.Excerpt + r.Tests[0].Reason + strings.Join(r.Tests[0].Output, "")
	for _, secret := range []string{key, "0123456789abcdef", "abcdefghijklmnopqrstuvwxyz0123", "ghp_abcdef", "hunter2", home} {
		if strings.Contains(all, secret) {
			t.Errorf("%q leaked:\n%s", secret, all)
		}
	}
	if !strings.Contains(string(b), "token=[REDACTED]") || !strings.Contains(string(b), filepath.Join("~", "project")) {
		t.Errorf("redaction should keep the text readable:\n%s", b)
	}
}

func TestEachStepGetsAScratchFolderThatIsRemoved(t *testing.T) {
	var work string
	runFakes(t, &Env{}, fakeStep("scratch", func(ctx context.Context, sc *StepContext) Outcome {
		work = sc.Work
		return Outcome{Status: Pass}
	}))
	if work == "" {
		t.Fatal("no scratch folder")
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatalf("scratch folder %s is still there: %v", work, err)
	}
	runFakes(t, &Env{Keep: true}, fakeStep("kept", func(ctx context.Context, sc *StepContext) Outcome {
		work = sc.Work
		return Outcome{Status: Pass}
	}))
	if _, err := os.Stat(work); err != nil {
		t.Fatalf("--keep must keep it: %v", err)
	}
	os.RemoveAll(work)
}

func TestFilterSteps(t *testing.T) {
	steps := []Step{fakeStep("facts", nil), fakeStep("core-smoke", nil), fakeStep("tests:internal/proc", nil), fakeStep("tests:internal/git", nil)}
	got, err := FilterSteps(steps, []string{"tests:*", "facts"})
	if err != nil || len(got) != 3 || got[0].ID != "facts" || got[1].ID != "tests:internal/proc" || got[2].ID != "tests:internal/git" {
		t.Fatalf("%v %+v", err, got)
	}
	if _, err := FilterSteps(steps, []string{"nonesuch"}); err == nil || !strings.Contains(err.Error(), "--list") {
		t.Fatalf("an unknown step must be refused: %v", err)
	}
	if all, _ := FilterSteps(steps, nil); len(all) != 4 {
		t.Fatal("no filter keeps every step")
	}
}

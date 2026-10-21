package watchdog

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/workflow"
)

func call(id, name, args string) workflow.Chunk {
	return workflow.NewChunk(workflow.ChunkToolCall, workflow.ToolCall{ID: id, Name: name, Args: json.RawMessage(args)})
}
func result(id, name, args, out string, isErr bool) workflow.Chunk {
	return workflow.NewChunk(workflow.ChunkToolResult, workflow.ToolResult{ID: id, Name: name, Args: json.RawMessage(args), Output: out, IsError: isErr})
}

func TestNormalizeStripsVolatileContent(t *testing.T) {
	a := "\x1b[31mFAIL\x1b[0m TestX (0.23s) at 2026-10-08T12:00:01Z in C:\\Users\\k\\AppData\\Local\\Temp\\go-build123\\x.go ptr=0xc000123456  \n"
	b := "FAIL TestX (0.41s) at 2026-10-09T01:02:03Z in C:\\Users\\k\\AppData\\Local\\Temp\\go-build999\\x.go ptr=0xc0009abcde\n"
	if Normalize(a) != Normalize(b) {
		t.Fatalf("normalize differs:\n%q\n%q", Normalize(a), Normalize(b))
	}
	if n := len(Normalize(strings.Repeat("x", 10000))); n > 4096+32 {
		t.Fatalf("hash input must be capped at about 4 KB, got %d", n)
	}
}

func TestToolSpinTripsOnThirdIdenticalCallAndResult(t *testing.T) {
	tr := NewTurn(Defaults())
	var v Verdict
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("c%d", i)
		tr.Observe(call(id, "read", `{"path":"src/auth.go","b":1}`))
		v = tr.Observe(result(id, "read", `{"b":1,"path":"src/auth.go"}`, "package auth // same content", false)) // arg order differs: canonicalized
		if i < 2 && v.Kind != None {
			t.Fatalf("tripped too early at %d", i)
		}
	}
	if v.Kind != Trip || v.Signal != SignalToolSpin {
		t.Fatalf("want tool_spin trip, got %+v", v)
	}
}

func TestResultIsPartOfTheKey_ReadAfterEditIsProgress(t *testing.T) {
	tr := NewTurn(Defaults())
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("c%d", i)
		tr.Observe(call(id, "read", `{"path":"a.go"}`))
		if v := tr.Observe(result(id, "read", `{"path":"a.go"}`, fmt.Sprintf("content version %d", i), false)); v.Kind != None {
			t.Fatalf("re-reading a changed file must not trip: %+v", v)
		}
	}
}

// False-positive control from the brief: tests are re-run after each of several real edits and must never trip.
func TestIterateUntilVerifiedFalsePositiveControl(t *testing.T) {
	tr := NewTurn(IterateUntilVerified())
	for i := 0; i < 10; i++ {
		e, r := fmt.Sprintf("e%d", i), fmt.Sprintf("r%d", i)
		tr.Observe(call(e, "edit", fmt.Sprintf(`{"path":"a.go","patch":"fix %d"}`, i)))
		if v := tr.Observe(result(e, "edit", fmt.Sprintf(`{"path":"a.go","patch":"fix %d"}`, i), "ok", false)); v.Kind != None {
			t.Fatalf("edit tripped: %+v", v)
		}
		tr.Observe(call(r, "bash", `{"cmd":"go test ./..."}`))
		// each run reports progress: one fewer failing test, with a different duration every time
		out := fmt.Sprintf("--- FAIL: TestN%d (0.%02ds)\nFAIL\n%d tests failed", i, i+10, 10-i)
		if v := tr.Observe(result(r, "bash", `{"cmd":"go test ./..."}`, out, false)); v.Kind != None {
			t.Fatalf("test re-run after an edit tripped at iteration %d: %+v", i, v)
		}
	}
}

func TestSameTestRunWithNoChangeTrips(t *testing.T) {
	tr := NewTurn(IterateUntilVerified())
	var v Verdict
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("r%d", i)
		tr.Observe(call(id, "bash", `{"cmd":"go test ./..."}`))
		v = tr.Observe(result(id, "bash", `{"cmd":"go test ./..."}`, fmt.Sprintf("--- FAIL: TestA (0.%02ds)\nFAIL", i+10), false)) // only the duration differs
	}
	if v.Kind != Trip {
		t.Fatalf("identical test result 3 times must trip even though durations differ: %+v", v)
	}
}

func TestToolCallCap(t *testing.T) {
	c := Defaults()
	c.MaxToolCalls = 60
	tr := NewTurn(c)
	var v Verdict
	for i := 0; i < 61; i++ {
		v = tr.Observe(call(fmt.Sprintf("c%d", i), "grep", fmt.Sprintf(`{"q":"%d"}`, i)))
	}
	if v.Kind != Trip || v.Signal != SignalToolCap {
		t.Fatalf("want tool_call_cap, got %+v", v)
	}
}

func TestConsecutiveToolErrors(t *testing.T) {
	tr := NewTurn(Defaults())
	var v Verdict
	for i := 0; i < 5; i++ {
		v = tr.Observe(result(fmt.Sprintf("c%d", i), "bash", fmt.Sprintf(`{"cmd":"x%d"}`, i), fmt.Sprintf("error %d", i), true))
	}
	if v.Kind != Trip || v.Signal != SignalErrorStreak {
		t.Fatalf("want tool_error_streak, got %+v", v)
	}
	tr = NewTurn(Defaults())
	for i := 0; i < 20; i++ { // a success in between resets the streak
		isErr := i%3 != 2
		if v := tr.Observe(result(fmt.Sprintf("c%d", i), "bash", fmt.Sprintf(`{"cmd":"x%d"}`, i), fmt.Sprintf("out %d", i), isErr)); v.Kind != None {
			t.Fatalf("non-consecutive errors tripped: %+v", v)
		}
	}
}

func TestWallClockCap(t *testing.T) {
	now := time.Unix(1000, 0)
	tr := NewTurn(Defaults())
	tr.SetClock(func() time.Time { return now })
	now = now.Add(21 * time.Minute)
	v := tr.Observe(workflow.NewChunk(workflow.ChunkText, workflow.TextDelta{Text: "still thinking"}))
	if v.Kind != Trip || v.Signal != SignalTimeCap {
		t.Fatalf("want time_cap, got %+v", v)
	}
}

func TestStagnationWarnsAndLowersSpinThreshold(t *testing.T) {
	tr := NewTurn(Defaults())
	base := strings.Repeat("I will now run the test suite to see whether the failure is still present in the module. ", 3)
	var v Verdict
	for i := 0; i < 4; i++ { // 4 messages => 3 consecutive similarity scores
		v = tr.Observe(workflow.NewChunk(workflow.ChunkMessageEnd, workflow.TextDelta{Text: base + "."}))
	}
	if v.Kind != Warn || v.Signal != SignalStagnation {
		t.Fatalf("want stagnation warning, got %+v", v)
	}
	// threshold is now 2: two identical call+result pairs trip
	var tv Verdict
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("c%d", i)
		tr.Observe(call(id, "read", `{"p":1}`))
		tv = tr.Observe(result(id, "read", `{"p":1}`, "same", false))
	}
	if tv.Kind != Trip {
		t.Fatalf("spin threshold should be 2 after a warning: %+v", tv)
	}
}

func TestStagnationNeverTripsOnItsOwn(t *testing.T) {
	tr := NewTurn(Defaults())
	base := strings.Repeat("Progress update: continuing with the same step as before for the record. ", 4)
	for i := 0; i < 30; i++ {
		if v := tr.Observe(workflow.NewChunk(workflow.ChunkMessageEnd, workflow.TextDelta{Text: base})); v.Kind == Trip {
			t.Fatal("stagnation is soft and must never trip")
		}
	}
}

func TestShortMessagesAreIgnoredForStagnation(t *testing.T) {
	tr := NewTurn(Defaults())
	for i := 0; i < 10; i++ {
		if v := tr.Observe(workflow.NewChunk(workflow.ChunkMessageEnd, workflow.TextDelta{Text: "ok"})); v.Kind != None {
			t.Fatal("short messages must not count")
		}
	}
}

func TestErrorEcho(t *testing.T) {
	var d EchoDetector
	fail := workflow.CheckResult{Name: "test", Passed: false, Output: "FAIL TestA (0.1s)\nexit status 1", FailingTests: []string{"TestB", "TestA"}}
	if v := d.OnCheckResult(fail, "diff1"); v.Kind != None {
		t.Fatal("first failure never trips")
	}
	// same failure, agent changed the code: not an echo
	fail2 := fail
	fail2.Output = "FAIL TestA (0.9s)\nexit status 1"
	fail2.FailingTests = []string{"TestA", "TestB"} // order differs, same set
	if v := d.OnCheckResult(fail2, "diff2"); v.Kind != None {
		t.Fatalf("a changed diff means progress: %+v", v)
	}
	// same signature, diff unchanged since that failure: echo
	if v := d.OnCheckResult(fail, "diff2"); v.Kind != Trip || v.Signal != SignalErrorEcho {
		t.Fatalf("want error_echo, got %+v", v)
	}
	// a pass resets
	d.OnCheckResult(workflow.CheckResult{Name: "test", Passed: true}, "diff3")
	if v := d.OnCheckResult(fail, "diff3"); v.Kind != None {
		t.Fatal("after a pass the detector must start over")
	}
}

func TestToolCallVisibilityReporting(t *testing.T) {
	tr := NewTurn(Defaults())
	tr.Observe(workflow.NewChunk(workflow.ChunkText, workflow.TextDelta{Text: "hi"}))
	if tr.ToolCallVisibility() {
		t.Fatal("no tool calls seen yet")
	}
	tr.Observe(call("c", "read", `{}`))
	if !tr.ToolCallVisibility() {
		t.Fatal("tool call seen")
	}
}

// A long test run whose stable "ok" lines fill the first 4 KB and whose failure changes at the end is progress, not a spin;
// the same output three times is still a spin.
func TestALongOutputThatChangesAtTheEndIsNotASpin(t *testing.T) {
	head := strings.Repeat("ok  \tgithub.com/x/pkg\t0.012s\n", 300) // about 9 KB of stable lines
	a := head + "--- FAIL: TestSum (0.00s)\n    sum_test.go:9: got 3, want 4\nFAIL"
	b := head + "--- FAIL: TestSum (0.00s)\n    sum_test.go:9: got 5, want 4\nFAIL"
	args := json.RawMessage(`{"command":"go test ./..."}`)
	if ToolKey("Bash", args, a) == ToolKey("Bash", args, b) {
		t.Fatal("a change at the end of a long output hashes like no change")
	}
	if ToolKey("Bash", args, a) != ToolKey("Bash", args, a+"\n") {
		t.Fatal("the same output must hash the same")
	}
	if FailureSignature(a, nil) == FailureSignature(b, nil) {
		t.Fatal("the failure signature ignored the end of the output")
	}
}

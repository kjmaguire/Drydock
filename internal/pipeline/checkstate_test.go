package pipeline_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/tasks"
)

func evd(id int64, kind string, passed *bool, summary string, data map[string]any) tasks.Evidence {
	b, _ := json.Marshal(data)
	return tasks.Evidence{ID: id, Kind: kind, Passed: passed, Summary: summary, Data: b}
}

var yes, no = func() *bool { b := true; return &b }(), func() *bool { b := false; return &b }()

func statuses(cs []pipeline.CheckState) string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Kind+"="+c.Status)
	}
	return strings.Join(out, " ")
}

func TestLatestChecksReadTheNewestPass(t *testing.T) {
	r1, r2 := map[string]any{"round": "chk_1"}, map[string]any{"round": "chk_2"}
	first := []tasks.Evidence{
		evd(1, "build", yes, "build passed in 1s", r1),
		evd(2, "test", yes, "test passed in 2s", r1),
		evd(3, "lint", yes, "lint passed", r1),
		evd(4, "diff", nil, "1 file(s), +3 -1", map[string]any{"Files": 1}),
		evd(5, "e2e", yes, "e2e passed", nil),
	}
	if got := statuses(pipeline.LatestChecks(first, nil)); got != "build=passed test=passed lint=passed e2e=passed" {
		t.Fatalf("one pass: %s", got)
	}
	// the second pass stopped at a failing test: lint did not run in it, and the end-to-end run is from the pass before
	second := append(append([]tasks.Evidence(nil), first...),
		evd(6, "build", yes, "build passed", r2),
		evd(7, "test", no, "test failed (exit 1) in 2s: TestSubtract, TestAC2_rejects", map[string]any{"round": "chk_2", "failing": []string{"TestSubtract", "TestAC2_rejects"}}))
	got := pipeline.LatestChecks(second, nil)
	if s := statuses(got); s != "build=passed test=failed lint=not_run e2e=not_run" {
		t.Fatalf("a stopped pass: %s", s)
	}
	if !reflect.DeepEqual(got[1].Failing, []string{"TestSubtract", "TestAC2_rejects"}) || !strings.HasPrefix(got[1].Reason, "test failed (exit 1)") {
		t.Fatalf("the failure: %+v", got[1])
	}
	if got[0].Failing != nil || got[0].Reason != "build passed" {
		t.Fatalf("a pass names nothing failing: %+v", got[0])
	}
	// a third pass that has started and is running its first check
	run := &pipeline.RunningCheck{Kind: "build", Round: "chk_3"}
	if s := statuses(pipeline.LatestChecks(second, run)); s != "build=running test=not_run lint=not_run e2e=not_run" {
		t.Fatalf("a new pass: %s", s)
	}
	// the same pass, further on
	third := append(append([]tasks.Evidence(nil), second...), evd(8, "build", yes, "build passed", map[string]any{"round": "chk_3"}))
	if s := statuses(pipeline.LatestChecks(third, &pipeline.RunningCheck{Kind: "test", Round: "chk_3"})); s != "build=passed test=running lint=not_run e2e=not_run" {
		t.Fatalf("further on: %s", s)
	}
	// an inconclusive result, a new kind, a skipped check
	fourth := append(append([]tasks.Evidence(nil), third...),
		evd(9, "test", yes, "test passed", map[string]any{"round": "chk_3"}),
		evd(10, "deps", nil, "deps skipped: osv-scanner is not installed\nsee the guide", map[string]any{"round": "chk_3", "skipped": true}))
	got = pipeline.LatestChecks(fourth, nil)
	if s := statuses(got); s != "build=passed test=passed lint=not_run e2e=not_run deps=inconclusive" {
		t.Fatalf("inconclusive: %s", s)
	}
	if got[4].Reason != "deps skipped: osv-scanner is not installed see the guide" {
		t.Fatalf("the reason is one line: %q", got[4].Reason)
	}
}

func TestLatestChecksOfEvidenceWithoutRoundsAreTheNewestOfEachKind(t *testing.T) {
	ev := []tasks.Evidence{evd(1, "build", yes, "", nil), evd(2, "test", no, "", map[string]any{"failing": []string{"A", "B"}}),
		evd(3, "build", yes, "", nil), evd(4, "test", yes, "", nil), evd(5, "spec", nil, "", nil), evd(6, "commit", nil, "", nil)}
	if s := statuses(pipeline.LatestChecks(ev, nil)); s != "build=passed test=passed" {
		t.Fatalf("%s", s)
	}
	if got := pipeline.LatestChecks(nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("no evidence: %#v", got)
	}
	// only the newest attempt counts
	ev = append(ev, tasks.Evidence{ID: 7, Attempt: 2, Kind: "lint", Passed: no})
	if s := statuses(pipeline.LatestChecks(ev, nil)); s != "lint=failed" {
		t.Fatalf("attempt 2: %s", s)
	}
	many := make([]string, 30)
	for i := range many {
		many[i] = "TestX"
	}
	if got := pipeline.LatestChecks([]tasks.Evidence{evd(1, "test", no, "", map[string]any{"failing": many})}, nil); len(got[0].Failing) != pipeline.MaxFailingNames {
		t.Fatalf("failing names are capped: %d", len(got[0].Failing))
	}
}

func TestRequestPhaseTellsTheSpecGateFromTheMerge(t *testing.T) {
	for _, c := range []struct {
		kind, title, payload, want string
	}{
		{"review", pipeline.SpecRequestTitlePrefix + "Add login", `{"phase":"spec"}`, "spec"},
		{"review", "anything", `{"phase":"spec"}`, "spec"},
		{"review", pipeline.MergeRequestTitlePrefix + "Add login", `{"taskId":"t"}`, "merge"},
		{"review", "Review the plan: x (3 tasks)", `{}`, "review"},
		{"approval", "permission: Bash", `{"gate":"permission"}`, "approval"},
		{"question", "Which one?", `null`, "question"},
	} {
		if got := pipeline.RequestPhase(c.kind, c.title, json.RawMessage(c.payload)); got != c.want {
			t.Errorf("%s %q: %s, want %s", c.kind, c.title, got, c.want)
		}
	}
}

func TestAcceptanceStateFollowsTheNewestTestRun(t *testing.T) {
	acc := []string{"AC1: Given a, when b, then c", "AC2: Given d, when e, then f", "AC12: Given g, when h, then i", "Given no id, when x, then y"}
	states := func(ev []tasks.Evidence) string {
		var out []string
		for i, s := range pipeline.AcceptanceState(acc, ev) {
			if s.Index != i {
				t.Fatalf("index %d at %d", s.Index, i)
			}
			out = append(out, s.ID+"="+s.State)
		}
		return strings.Join(out, " ")
	}
	if got := states(nil); got != "AC1=unknown AC2=unknown AC12=unknown =unknown" {
		t.Fatalf("no tests yet: %s", got)
	}
	// a failing run names AC2's test (and not AC12's: AC1 is not AC12)
	failing := []tasks.Evidence{evd(1, "test", no, "", map[string]any{"failing": []string{"TestLogin/AC2_rejects_empty", "TestOther"}})}
	if got := states(failing); got != "AC1=unknown AC2=failing AC12=unknown =unknown" {
		t.Fatalf("failing: %s", got)
	}
	// it passes now: AC2's test is known, the others are not
	passing := append(failing, evd(2, "test", yes, "", nil))
	if got := states(passing); got != "AC1=unknown AC2=passed AC12=unknown =unknown" {
		t.Fatalf("passing: %s", got)
	}
	// a spec-gated feature went through the test-first step, which made the tests mention every accepted id
	traced := []tasks.Evidence{evd(1, "spec_accept", yes, "", nil), evd(2, "red_test", nil, "", map[string]any{"failing": []string{"TestAC1"}}),
		evd(3, "test", yes, "", nil)}
	if got := states(traced); got != "AC1=passed AC2=passed AC12=passed =unknown" {
		t.Fatalf("traced: %s", got)
	}
	// a newest run with no verdict says nothing
	if got := states(append(traced, evd(4, "test", nil, "test skipped", nil))); got != "AC1=unknown AC2=unknown AC12=unknown =unknown" {
		t.Fatalf("inconclusive: %s", got)
	}
	// a failing run that names none of them leaves them unknown
	if got := states(append(traced, evd(4, "test", no, "", map[string]any{"failing": []string{"TestAC12x"}}))); got != "AC1=unknown AC2=unknown AC12=failing =unknown" {
		t.Fatalf("failing other: %s", got)
	}
	if got := pipeline.AcceptanceState(nil, traced); got == nil || len(got) != 0 {
		t.Fatalf("no criteria: %#v", got)
	}
}

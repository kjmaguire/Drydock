package pipeline_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// Each acceptance criterion's own status (decision P125): a check result confirms met or shows not met, a review finding shows
// not met and never confirms, a person confirms, says not met, waives or reopens, and an agent's claim is shown beside the
// status without setting it.

var acList = []string{"AC1: Given 5 and 3, when Subtract is called, then it returns 2", "AC2: Given 0 and 0, when Subtract is called, then it returns 0"}

func testRun(passed bool, at int64, failing ...string) tasks.Evidence {
	data, _ := json.Marshal(map[string]any{"argv": []string{"go", "test", "./..."}, "failing": failing})
	return tasks.Evidence{Kind: "test", Passed: &passed, Summary: "go test", Data: data, Created: at}
}

func mark(i int, m, actor, note string, at int64) tasks.CriterionMark {
	return tasks.CriterionMark{Criterion: i, SHA: tasks.CriterionSHA(acList[i]), Mark: m, Actor: actor, Note: note, Created: at}
}

func TestCriteriaStatusFromChecksReviewsAndPeople(t *testing.T) {
	// nothing known: both open
	for _, c := range pipeline.CriteriaStatus(acList, nil, nil, nil) {
		if c.Status != pipeline.CriterionOpen || c.Source != "" {
			t.Fatalf("no facts: %+v", c)
		}
	}
	// a failing test that names AC1 shows it not met by the check; AC2 stays open
	ev := []tasks.Evidence{testRun(false, 10, "TestSubtract_AC1")}
	cs := pipeline.CriteriaStatus(acList, ev, nil, nil)
	if c := cs[0]; c.Status != pipeline.CriterionNotMet || c.Source != pipeline.SourceCheck || c.By != "test" || !strings.Contains(c.Evidence, "TestSubtract_AC1") || c.ID != "AC1" {
		t.Fatalf("AC1 %+v", c)
	}
	if cs[1].Status != pipeline.CriterionOpen {
		t.Fatalf("AC2 %+v", cs[1])
	}
	// a passing run whose tests are known to carry both ids confirms both met
	ev = append(ev, testRun(true, 20))
	ev[0].Data, _ = json.Marshal(map[string]any{"failing": []string{"TestSubtract_AC1", "TestZero_AC2"}})
	cs = pipeline.CriteriaStatus(acList, ev, nil, nil)
	if cs[0].Status != pipeline.CriterionMet || cs[1].Status != pipeline.CriterionMet || cs[0].Source != pipeline.SourceCheck {
		t.Fatalf("after the passing run %+v", cs)
	}
	// a newer review that asks for changes and names AC2 shows it not met; a review never confirms
	rv := []tasks.ReviewRecord{{Reviewer: "a2", Verdict: "REVISE", Feedback: "Mostly fine.\nAC2 is not met: Subtract(0, 0) panics.", Created: 30}}
	cs = pipeline.CriteriaStatus(acList, ev, rv, nil)
	if c := cs[1]; c.Status != pipeline.CriterionNotMet || c.Source != pipeline.SourceReview || c.By != "a2" || c.Evidence != "AC2 is not met: Subtract(0, 0) panics." {
		t.Fatalf("AC2 after the review %+v", c)
	}
	if cs[0].Status != pipeline.CriterionMet {
		t.Fatalf("AC1 after the review %+v", cs[0])
	}
	if c := pipeline.CriteriaStatus(acList, nil, []tasks.ReviewRecord{{Reviewer: "a2", Verdict: "SHIP", Created: 30}}, nil); c[0].Status != pipeline.CriterionOpen {
		t.Fatalf("a SHIP confirmed a criterion: %+v", c[0])
	}
	// a person's word, newer, counts; a newer check result counts over it again
	marks := []tasks.CriterionMark{mark(1, tasks.MarkMet, "human:kyle", "checked by hand", 40)}
	cs = pipeline.CriteriaStatus(acList, ev, rv, marks)
	if c := cs[1]; c.Status != pipeline.CriterionMet || c.Source != pipeline.SourcePerson || c.By != "human:kyle" || c.Evidence != "checked by hand" {
		t.Fatalf("AC2 confirmed by the person %+v", c)
	}
	later := append(append([]tasks.Evidence(nil), ev...), testRun(false, 50, "TestZero_AC2"))
	if c := pipeline.CriteriaStatus(acList, later, rv, marks)[1]; c.Status != pipeline.CriterionNotMet || c.Source != pipeline.SourceCheck {
		t.Fatalf("AC2 after a newer failing run %+v", c)
	}
	// a waiver holds over a newer failing run, until the person reopens it
	marks = append(marks, mark(1, tasks.MarkWaived, "human:kyle", "zero is out of scope", 45))
	if c := pipeline.CriteriaStatus(acList, later, rv, marks)[1]; c.Status != pipeline.CriterionWaived || c.Evidence != "zero is out of scope" {
		t.Fatalf("AC2 waived %+v", c)
	}
	marks = append(marks, mark(1, tasks.MarkOpen, "human:kyle", "", 60))
	if c := pipeline.CriteriaStatus(acList, later, rv, marks)[1]; c.Status != pipeline.CriterionNotMet || c.Source != pipeline.SourceCheck {
		t.Fatalf("AC2 reopened %+v", c)
	}
	// a mark on a criterion whose text has changed since does not bind it
	edited := []string{acList[0], "AC2: Given 0 and 0, when Subtract is called, then it returns 0 without panicking"}
	if c := pipeline.CriteriaStatus(edited, nil, nil, []tasks.CriterionMark{mark(1, tasks.MarkWaived, "human:kyle", "x", 45)})[1]; c.Status != pipeline.CriterionOpen {
		t.Fatalf("a waiver of the old text %+v", c)
	}
}

// An agent's claim is shown while nothing newer settled the criterion; it never makes it met.
func TestAnAgentsClaimNeverMakesACriterionMet(t *testing.T) {
	claim := []tasks.CriterionMark{mark(0, tasks.MarkClaimMet, "a1", "TestSubtract_AC1 passes locally", 15)}
	c := pipeline.CriteriaStatus(acList, nil, nil, claim)[0]
	if c.Status != pipeline.CriterionOpen || c.Claim == nil || c.Claim.By != "a1" || c.Claim.Evidence != "TestSubtract_AC1 passes locally" {
		t.Fatalf("claim on an open criterion %+v", c)
	}
	// a failing run newer than the claim settles it: not met, the claim no longer shown
	if c := pipeline.CriteriaStatus(acList, []tasks.Evidence{testRun(false, 20, "TestSubtract_AC1")}, nil, claim)[0]; c.Status != pipeline.CriterionNotMet || c.Claim != nil {
		t.Fatalf("after a newer failing run %+v", c)
	}
	// a claim newer than the failing run is shown beside the not-met status
	if c := pipeline.CriteriaStatus(acList, []tasks.Evidence{testRun(false, 10, "TestSubtract_AC1")}, nil, claim)[0]; c.Status != pipeline.CriterionNotMet || c.Claim == nil {
		t.Fatalf("a claim after the failing run %+v", c)
	}
	if s := pipeline.CriteriaSummary(pipeline.CriteriaStatus(acList, nil, nil, claim)); s != "AC1 open, claimed met by a1; AC2 open" {
		t.Fatalf("summary %q", s)
	}
}

// What a handoff says about the criteria is checked like what it says about the checks.
func TestAHandoffsCriteriaClaimsAreChecked(t *testing.T) {
	cs := pipeline.CriteriaStatus(acList, []tasks.Evidence{testRun(false, 10, "TestSubtract_AC1")}, nil, nil)
	facts := pipeline.HandoffFacts{Exists: func(string) bool { return true }, Acceptance: acList, Criteria: cs}
	for _, tc := range []struct {
		criteria []map[string]any
		want     string
	}{
		{[]map[string]any{{"id": "AC1", "met": true, "evidence": "TestSubtract_AC1 passes"}}, "it says criterion AC1 is met, but the newest test run says it is not (failing: TestSubtract_AC1)"},
		{[]map[string]any{{"id": "AC7", "met": true, "evidence": "x"}}, "criterion AC7, which the task does not have"},
		{[]map[string]any{{"id": "2", "met": true}}, "criterion 2 is met but not what shows it"},
	} {
		raw, _ := json.Marshal(map[string]any{"summary": "s", "next_step": "n", "criteria": tc.criteria})
		if _, why := pipeline.VerifyAgentHandoff(string(raw), facts); !strings.Contains(strings.Join(why, "; "), tc.want) {
			t.Errorf("%v: %v, want %q", tc.criteria, why, tc.want)
		}
	}
	raw, _ := json.Marshal(map[string]any{"summary": "s", "next_step": "n", "criteria": []map[string]any{{"id": "AC2", "met": true, "evidence": "TestZero_AC2 passes"}, {"id": "AC1", "met": false}}})
	if _, why := pipeline.VerifyAgentHandoff(string(raw), facts); len(why) != 0 {
		t.Fatalf("a sound report failed: %v", why)
	}
	for ref, want := range map[string]int{"AC2": 1, "ac1": 0, "#2": 1, "1": 0, "AC3": -1, "3": -1, "": -1} {
		if got := pipeline.CriterionIndex(acList, ref); got != want {
			t.Errorf("CriterionIndex(%q) = %d, want %d", ref, got, want)
		}
	}
	if got := pipeline.CriterionIndex([]string{"Given a, when b, then c", "Given d, when e, then f"}, "AC2"); got != 1 {
		t.Errorf("AC2 of criteria without ids: %d", got)
	}
}

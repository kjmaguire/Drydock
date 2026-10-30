package pipeline

import (
	"fmt"
	"strings"
	"testing"

	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// Review and role turns are cut to the package ceiling (decision P125) in the fixed order of ctxeng.CutOrder. What each turn is
// for is never cut: the role's instructions, the task with its acceptance criteria, and the diff a reviewer reviews.

func itemsBySource(a ctxeng.Assembly) map[string]ctxeng.Item {
	out := map[string]ctxeng.Item{}
	for _, it := range a.Items {
		out[it.Source] = it
	}
	return out
}

func bigText(prefix string, lines int) string {
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&b, "%s line %d %s\n", prefix, i, strings.Repeat("y", 60))
	}
	return b.String()
}

// mustKeep fails unless every piece whose kind may never be cut (or that is marked Keep) is in the package whole.
func mustKeep(t *testing.T, kind string, ps []ctxeng.Piece, a ctxeng.Assembly) {
	t.Helper()
	items := itemsBySource(a)
	for _, p := range ps {
		if ctxeng.Cuttable(p.Kind) && !p.Keep {
			continue
		}
		if it := items[p.Source]; !it.Included || it.Exclusion != "" {
			t.Errorf("%s: %s (%s) was touched: %+v", kind, p.Source, p.Kind, it)
		}
		if !strings.Contains(a.Text, p.Text) {
			t.Errorf("%s: %s is not whole in the prompt", kind, p.Source)
		}
	}
	if strings.Count(a.Text, quoteOpen) != strings.Count(a.Text, quoteClose) {
		t.Errorf("%s: a data block is not closed:\n%s", kind, a.Text)
	}
}

func evidenceList(n int) []tasks.Evidence {
	passed := true
	var ev []tasks.Evidence
	for i := 0; i < n; i++ {
		ev = append(ev, tasks.Evidence{Kind: fmt.Sprintf("scan%d", i), Passed: &passed, Summary: strings.Repeat("finding text ", 30)})
	}
	return ev
}

func bigDiff(kb int) string {
	var b strings.Builder
	b.WriteString("diff --git a/calc.go b/calc.go\n--- a/calc.go\n+++ b/calc.go\n")
	for b.Len() < kb<<10 {
		b.WriteString("+// " + strings.Repeat("z", 70) + "\n")
	}
	return b.String()
}

// The reviewer's and the security reviewer's packages: the evidence list goes, the diff under review stays whole however large,
// and when the diff and the task alone are over the ceiling the package line says so.
func TestReviewPromptsKeepTheDiffAndCriteriaAndCutTheEvidence(t *testing.T) {
	for _, tc := range []struct {
		kind   string
		pieces func(tasks.Task, []tasks.Evidence, string) []ctxeng.Piece
		end    string
	}{
		{"review", ReviewPieces, `{"verdict":"REVISE","feedback":"specific, actionable problems"}   otherwise.`},
		{"security-review", SecurityReviewPieces, `{"verdict":"REVISE","feedback":"specific, actionable findings"}   otherwise.`},
	} {
		ps := tc.pieces(subtract, evidenceList(40), bigDiff(12))
		a := ctxeng.Assemble(ctxeng.MinCeiling, ps)
		mustKeep(t, tc.kind, ps, a)
		items := itemsBySource(a)
		if ev := items["evidence"]; ev.Exclusion != ctxeng.ExclusionOverBudget || !strings.Contains(ev.Reason, "the task's evidence is on the task page") {
			t.Errorf("%s: evidence %+v", tc.kind, ev)
		}
		if d := items["diff"]; !d.Included || d.Kind != ctxeng.KindDiff || !strings.Contains(d.Reason, "never cut") {
			t.Errorf("%s: diff %+v", tc.kind, d)
		}
		if !strings.Contains(a.Text, "AC1: Given 5 and 3") || !strings.HasSuffix(a.Text, tc.end) {
			t.Errorf("%s: criteria or the verdict instructions are missing:\n%s", tc.kind, a.Text)
		}
		if pkg := items["budget:package"]; !strings.Contains(pkg.Reason, "1 piece(s) cut or left out") {
			t.Errorf("%s: package %+v", tc.kind, pkg)
		}
		// a diff larger than the ceiling is still sent whole (up to the 48 KB bound where it is built), and the package says so
		huge := ctxeng.Assemble(ctxeng.MinCeiling, tc.pieces(subtract, evidenceList(3), bigDiff(40)))
		if pkg := itemsBySource(huge)["budget:package"]; !huge.Over || !strings.Contains(pkg.Reason, "OVER THE CEILING") {
			t.Errorf("%s: a diff over the ceiling: over %v, %+v", tc.kind, huge.Over, pkg)
		}
		if !strings.Contains(huge.Text, bigDiff(40)) {
			t.Errorf("%s: the diff under review was cut", tc.kind)
		}
		// nothing is cut under a ceiling it fits in, and the rendered text is the prompt
		small := ctxeng.Assemble(ctxeng.DefaultCeiling, tc.pieces(subtract, evidenceList(2), bigDiff(1)))
		if len(small.LeftOut()) != 0 || small.Text != Render(tc.pieces(subtract, evidenceList(2), bigDiff(1))) {
			t.Errorf("%s: a small package was cut: %v", tc.kind, small.LeftOut())
		}
	}
	// the reviewer is asked to name a criterion it finds unmet by id, when the criteria have ids
	if p := ReviewPrompt(subtract, nil, "d"); !strings.Contains(p, "name its id (for example AC2)") {
		t.Errorf("review prompt: %s", p)
	}
	if p := ReviewPrompt(tasks.Task{Title: "x", Acceptance: []string{"Given a, when b, then c"}}, nil, "d"); strings.Contains(p, "name its id") {
		t.Errorf("criteria without ids: %s", p)
	}
}

// The spec writer's package: the previous spec goes before the feedback (spec before feedback in the cut order), the task and the
// shape of the answer never.
func TestSpecPromptCutsThePreviousSpecBeforeTheFeedback(t *testing.T) {
	ps := SpecPieces(subtract, bigText("previous", 160), bigText("feedback", 140), "Kids use it.")
	a := ctxeng.Assemble(ctxeng.MinCeiling, ps)
	mustKeep(t, "spec", ps, a)
	items := itemsBySource(a)
	prev, fb := items["previous spec"], items["feedback"]
	if prev.Exclusion != ctxeng.ExclusionOverBudget || prev.Included && !strings.Contains(prev.Reason, "cut from") {
		t.Fatalf("previous spec %+v", prev)
	}
	if !fb.Included {
		t.Fatalf("the feedback went before the previous spec: %+v", fb)
	}
	if !strings.Contains(a.Text, "Every acceptance criterion must contain the words Given, when and then.") || !strings.Contains(a.Text, "Kids use it.") {
		t.Fatalf("spec prompt:\n%s", a.Text)
	}
}

// The architect's package: the accepted spec is cut with a pointer to the evidence that keeps it; the accepted criteria stay.
func TestDesignPromptCutsTheSpecAndKeepsTheCriteria(t *testing.T) {
	ps := DesignPieces(subtract, bigText("spec", 300), "")
	a := ctxeng.Assemble(ctxeng.MinCeiling, ps)
	mustKeep(t, "design", ps, a)
	sp := itemsBySource(a)["spec"]
	if sp.Exclusion != ctxeng.ExclusionOverBudget || !strings.Contains(sp.Reason, "spec_accept evidence") {
		t.Fatalf("spec %+v", sp)
	}
	if !strings.Contains(a.Text, "Accepted acceptance criteria:\n- AC1: Given 5 and 3") {
		t.Fatalf("design prompt:\n%s", a.Text)
	}
}

// The test writer's package: the spec first, then the feedback; the criteria with their ids and the rules stay.
func TestTestsPromptCutsTheSpecThenTheFeedback(t *testing.T) {
	ps := TestsPieces(subtract, false, bigText("feedback", 150), bigText("spec", 150), "")
	a := ctxeng.Assemble(ctxeng.MinCeiling, ps)
	mustKeep(t, "write-tests", ps, a)
	items := itemsBySource(a)
	if sp := items["spec"]; sp.Exclusion != ctxeng.ExclusionOverBudget {
		t.Fatalf("the spec should be cut first: %+v", sp)
	}
	if fb := items["feedback"]; !fb.Included || fb.Exclusion != "" {
		t.Fatalf("the feedback was cut although cutting the spec was enough: %+v", fb)
	}
	if !strings.Contains(a.Text, "Put the criterion's id (for example AC1)") || !strings.Contains(a.Text, "- AC1: Given 5 and 3") {
		t.Fatalf("tests prompt:\n%s", a.Text)
	}
	// a bug's reproduction has no spec: its feedback is what may go
	bug := TestsPieces(subtract, true, bigText("feedback", 300), "", "")
	ab := ctxeng.Assemble(ctxeng.MinCeiling, bug)
	mustKeep(t, "reproduce", bug, ab)
	if fb := itemsBySource(ab)["feedback"]; fb.Exclusion != ctxeng.ExclusionOverBudget {
		t.Fatalf("reproduce feedback %+v", fb)
	}
}

// The planner's package: the requirements are cut, the goal, the open tasks, the team and the rules are not.
func TestPlanPromptCutsTheRequirements(t *testing.T) {
	team := []TeamMember{{Name: "Backend #1", Role: "backend", Adapter: "claude"}}
	ps := PlanPieces("Ship auth", bigText("requirement", 400), team, "", PlanContext{Brief: "B2B app"})
	a := ctxeng.Assemble(ctxeng.MinCeiling, ps)
	mustKeep(t, "plan", ps, a)
	if rq := itemsBySource(a)["requirements"]; rq.Exclusion != ctxeng.ExclusionOverBudget || !strings.Contains(rq.Reason, "kept with the plan") {
		t.Fatalf("requirements %+v", rq)
	}
	if !strings.Contains(a.Text, "Goal (from the human):\nShip auth") || !strings.Contains(a.Text, "- Backend #1: backend, claude") || !strings.Contains(a.Text, "Nothing is created until a human reviews your plan.") {
		t.Fatalf("plan prompt:\n%s", a.Text)
	}
}

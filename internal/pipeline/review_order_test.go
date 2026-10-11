package pipeline_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// TestReviewNotStartedBeforeChecks (token rule 10): a worker turn whose checks fail goes back to the worker, never to the
// reviewer; the reviewer is started once, on the turn whose checks passed, and sees them passed.
func TestReviewNotStartedBeforeChecks(t *testing.T) {
	ad, cfg, lg := fakeRoles(t,
		fakeagent.Script{
			Rules: []fakeagent.Rule{{Match: "previous attempt needs changes", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "fixed"}}}},
			Turns: [][]fakeagent.Step{{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: wrongImpl}}, {Say: "implemented"}}},
		},
		fakeagent.Script{Default: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}})
	e := setup(t, cfg, ad)
	id := e.runIUV(t, 4)
	e.await(t, id, workflow.StatusWaitingHuman)
	wp, rp := prompts(lg.workLog), prompts(lg.reviewLog)
	if len(wp) != 2 || !strings.Contains(wp[1], "check failed") {
		t.Fatalf("the failing turn must go back to the worker: %d worker turns %q", len(wp), wp)
	}
	if len(rp) != 1 || !strings.Contains(rp[0], "[passed]") || strings.Contains(rp[0], "[failed]") {
		t.Fatalf("the reviewer must start once, after the checks passed: %d turns %q", len(rp), rp)
	}
}

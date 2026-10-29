package pipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Decision P125, with the scripted Claude stand-in (internal/agents/fakeagent, a labelled test double): review turns cut to the
// package ceiling, the turn that asks the outgoing agent for a handoff, and acceptance criteria with a status each.

// bigComment is a Go file that compiles: a package clause and n comment lines, the last one marked.
func bigComment(n int) string {
	var b strings.Builder
	b.WriteString("package gocalc\n\n")
	for i := 1; i < n; i++ {
		fmt.Fprintf(&b, "// note %d %s\n", i, strings.Repeat("n", 70))
	}
	b.WriteString("// LAST NOTE LINE\n")
	return b.String()
}

// The reviewer's turn under a 4,000-token ceiling: the diff under review is sent whole although it alone is over the ceiling,
// the evidence list is left out with its pointer, and the manifest's package line says the package is over the ceiling.
func TestTheReviewTurnIsCutToTheCeilingButNeverItsDiff(t *testing.T) {
	ad, cfg, lg := fakeRoles(t,
		fakeagent.Script{Turns: [][]fakeagent.Step{{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}},
			{Write: &fakeagent.WriteSpec{Path: "notes.go", Content: bigComment(400)}}, {Say: "implemented"}}}},
		fakeagent.Script{Default: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}})
	e := setup(t, cfg, ad)
	if err := pipeline.SaveDevSettings(context.Background(), e.db, "p1", pipeline.DevSettings{MaxParallel: 2, Respond: true, ContextCeiling: ctxeng.MinCeiling}, "human:me"); err != nil {
		t.Fatal(err)
	}
	id := e.runIUV(t, 3)
	e.await(t, id, workflow.StatusWaitingHuman)
	rp := prompts(lg.reviewLog)
	if len(rp) != 1 {
		t.Fatalf("review turns %d", len(rp))
	}
	for _, want := range []string{"independent reviewer", "Given 5 and 3", "+// LAST NOTE LINE", "+// note 1 n", `{"verdict":"SHIP","feedback":""}`} {
		if !strings.Contains(rp[0], want) {
			t.Errorf("the review prompt lacks %q", want)
		}
	}
	if strings.Contains(rp[0], "Evidence already produced by deterministic tools") {
		t.Error("the evidence list should have been left out first")
	}
	items := manifestItems(t, e, id, "review", 1)
	if ev := items["evidence"]; ev.Included || ev.Exclusion != ctxeng.ExclusionOverBudget || !strings.Contains(ev.Reason, "left out: over the package ceiling of 4000 tokens") {
		t.Fatalf("evidence %+v", ev)
	}
	if d := items["diff"]; !d.Included || d.Exclusion != "" || d.Tokens < ctxeng.MinCeiling {
		t.Fatalf("diff %+v", d)
	}
	if pkg := items["budget:package"]; !strings.Contains(pkg.Reason, "OVER THE CEILING") {
		t.Fatalf("package %+v", pkg)
	}
	if task := items["task"]; !task.Included || task.Exclusion != "" {
		t.Fatalf("task %+v", task)
	}
}

// Which outgoing sessions are asked for a handoff, and why the others are not.
func TestAskForHandoffOnlyWhenTheOutgoingSessionCanAnswer(t *testing.T) {
	prev := prevTurn(150_000, 200_000)
	dec := func(trigger string) pipeline.SessionDecision {
		return pipeline.SessionDecision{Action: pipeline.SessionHandoff, Trigger: trigger}
	}
	for _, trig := range []string{pipeline.HandoffContextPressure, pipeline.HandoffEscalation, pipeline.HandoffModelChange} {
		if ask, _, why := pipeline.AskForHandoff(dec(trig), prev, true, true); !ask {
			t.Errorf("%s: not asked: %s", trig, why)
		}
	}
	for _, tc := range []struct {
		dec       pipeline.SessionDecision
		prev      *pipeline.WorkOutput
		resume    bool
		tool      bool
		outcome   string
		wantWords string
	}{
		{dec(pipeline.HandoffInterrupted), prev, true, true, pipeline.HandoffNotAsked, "after a crash"},
		{dec(pipeline.HandoffWatchdog), prev, true, true, pipeline.HandoffNotAsked, "the watchdog stopped"},
		{dec(pipeline.HandoffResumeFailed), prev, true, true, pipeline.HandoffNotAsked, "could not be resumed"},
		{dec(pipeline.HandoffAgentChange), prev, true, true, pipeline.HandoffNotAsked, "another agent takes the task over"},
		{dec(pipeline.HandoffContextPressure), prev, false, true, pipeline.HandoffNotAsked, "cannot continue a stored session"},
		{dec(pipeline.HandoffContextPressure), prev, true, false, pipeline.HandoffNotAsked, "no handoff tool"},
		{dec(pipeline.HandoffContextPressure), nil, true, true, pipeline.HandoffNotAsked, "no outgoing session"},
		{pipeline.SessionDecision{Action: pipeline.SessionContinue}, prev, true, true, pipeline.HandoffNotAsked, "no session is replaced"},
		{dec(pipeline.HandoffContextPressure), prevTurn(198_500, 200_000), true, true, pipeline.HandoffOverBudget, "1500 tokens of its context window left, less than the 4000"},
	} {
		ask, outcome, why := pipeline.AskForHandoff(tc.dec, tc.prev, tc.resume, tc.tool)
		if ask || outcome != tc.outcome || !strings.Contains(why, tc.wantWords) {
			t.Errorf("%s: ask %v %s %q, want %s %q", tc.dec.Trigger, ask, outcome, why, tc.outcome, tc.wantWords)
		}
	}
}

// handoffTurnEnv is the two-pass run (the first turn ends at 75 % of the window, so the second replaces the session) with the
// gateway's status saying the worker has the handoff tool, and a handoff turn scripted by steps.
func handoffTurnEnv(t *testing.T, handoffSteps []fakeagent.Step) *env {
	t.Helper()
	sc := twoPassScript(150_000)
	sc.Rules = append([]fakeagent.Rule{{Match: "Drydock phase: handoff", Steps: handoffSteps}}, sc.Rules...)
	e := sessionEnv(t, sc)
	e.deps.MCPStatus = func(context.Context, tasks.Task, string, string) pipeline.MCPStatus {
		return pipeline.MCPStatus{Attached: true, Tools: []string{"handoff", "read_range"}}
	}
	return e
}

// requestOf is the handoff request recorded on the run's second work turn, with its manifest line and event.
func requestOf(t *testing.T, e *env, runID workflow.RunID) (pipeline.HandoffRequest, ctxeng.Item, map[string]any) {
	t.Helper()
	outs := workOutputs(t, e, runID)
	if len(outs) != 2 || outs[1].Session == nil || outs[1].Session.Request == nil {
		t.Fatalf("work outputs %+v", outs)
	}
	return *outs[1].Session.Request, manifestItems(t, e, runID, "work", 1)["handoff request"], sessionEvents(t, e)[1]
}

// An outgoing session that does not answer within the time limit is stopped; the fresh session gets Drydock's handoff and the
// run goes on.
func TestAHandoffTurnThatDoesNotAnswerInTimeIsStopped(t *testing.T) {
	e := handoffTurnEnv(t, []fakeagent.Step{{Block: true}})
	e.deps.HandoffTurnTimeout = 2 * time.Second
	start := time.Now()
	runID, _ := e.run(t, 3, workflow.StatusCompleted)
	req, item, ev := requestOf(t, e, runID)
	if !req.Asked || req.Outcome != pipeline.HandoffNoAnswer || !strings.Contains(req.Reason, "did not answer within 2 s") || req.Session != "fake-session-0001" {
		t.Fatalf("request %+v", req)
	}
	if !strings.Contains(item.Reason, "and wrote its own: the outgoing session did not answer") || ev["handoffRequest"] != pipeline.HandoffNoAnswer {
		t.Fatalf("manifest %+v event %v", item, ev)
	}
	p := promptLog()
	if len(p) != 3 || !strings.Contains(p[1], "Drydock phase: handoff") || !strings.Contains(p[2], "What the previous session said it did:\nimplemented Subtract.") {
		t.Fatalf("prompts %d:\n%s", len(p), strings.Join(p, "\n-----\n"))
	}
	// the handoff turn resumed the outgoing session, read-only, on its own model
	args := argsLog(t)
	if len(args) < 3 || flagOf(args[1], "--resume") != "fake-session-0001" || flagOf(args[2], "--resume") != "" {
		t.Fatalf("sessions %v", args)
	}
	var art pipeline.HandoffArtifact
	for _, h := range handoffRows(t, e) {
		if h.reason == pipeline.HandoffContextPressure {
			_ = json.Unmarshal([]byte(h.artifact), &art)
		}
	}
	if art.HandoffRequest == nil || art.HandoffRequest.Outcome != pipeline.HandoffNoAnswer {
		t.Fatalf("artifact %+v", art.HandoffRequest)
	}
	if time.Since(start) > 90*time.Second {
		t.Fatalf("the run took %s: the time limit did not stop the turn", time.Since(start))
	}
}

// A handoff turn that uses more than its budget: what it wrote is not used, and the reason is recorded.
func TestAHandoffTurnOverItsBudgetIsNotUsed(t *testing.T) {
	e := handoffTurnEnv(t, []fakeagent.Step{{Say: "hnd_none"}})
	e.deps.HandoffTurnTokens = 10
	runID, _ := e.run(t, 3, workflow.StatusCompleted)
	req, item, ev := requestOf(t, e, runID)
	if !req.Asked || req.Outcome != pipeline.HandoffOverBudget || req.Tokens <= 10 || !strings.Contains(req.Reason, "more than its budget of 10") {
		t.Fatalf("request %+v", req)
	}
	if !strings.Contains(item.Reason, "a budget of 10 new tokens") || ev["handoffRequest"] != pipeline.HandoffOverBudget {
		t.Fatalf("manifest %+v event %v", item, ev)
	}
	if p := promptLog(); len(p) != 3 || !strings.Contains(p[2], "What the previous session said it did:") {
		t.Fatalf("prompts %d", len(p))
	}
}

// A handoff turn that ends without a handoff: Drydock's is used, with the reason.
func TestAHandoffTurnWithoutAHandoffFallsBackToDrydocks(t *testing.T) {
	e := handoffTurnEnv(t, []fakeagent.Step{{Say: "I would rather keep working."}})
	runID, _ := e.run(t, 3, workflow.StatusCompleted)
	req, _, _ := requestOf(t, e, runID)
	if !req.Asked || req.Outcome != pipeline.HandoffNoAnswer || req.Reason != "the outgoing session ended its turn without writing a handoff" || req.Budget != pipeline.HandoffTurnTokens {
		t.Fatalf("request %+v", req)
	}
}

package pipeline_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Decision P128, with the scripted Claude stand-in (internal/agents/fakeagent, a labelled test double): findings about how the
// criteria are written, criteria a named check confirms, the outgoing agent asked for a handoff when another agent takes over,
// Drydock's own occupancy estimate, and a session restarted after two corrections.

func setAcceptance(t *testing.T, e *env, ac ...string) {
	t.Helper()
	tk, err := e.tasks.Patch(context.Background(), e.task.ID, tasks.Update{Acceptance: &ac})
	if err != nil {
		t.Fatal(err)
	}
	e.task = tk
}

func findings(t *testing.T, e *env) []pipeline.AcceptanceFinding {
	t.Helper()
	ev, err := e.tasks.Evidence(context.Background(), e.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []pipeline.AcceptanceFinding
	for _, x := range ev {
		if x.Kind != pipeline.ReviewFindingKind {
			continue
		}
		if x.Passed != nil {
			t.Errorf("a finding carries a verdict: %+v", x)
		}
		var f pipeline.AcceptanceFinding
		if err := json.Unmarshal(x.Data, &f); err != nil || f.Text != x.Summary {
			t.Fatalf("finding %+v: %v", x, err)
		}
		out = append(out, f)
	}
	return out
}

// The review step finds a criterion written without an id and one that names a check the project does not run: each is a
// finding recorded once, without a verdict, and neither stops the review's SHIP; the criterion with an id that names the lint
// check is confirmed by it, the one naming a check that does not exist stays open (never a silent pass).
func TestTheReviewStepFindsCriteriaWithoutAnIDOrWithAnUnknownCheck(t *testing.T) {
	ad, cfg, lg := fakeRoles(t,
		fakeagent.Script{Turns: [][]fakeagent.Step{{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "implemented"}}}},
		fakeagent.Script{Default: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}})
	e := setup(t, cfg, ad)
	setAcceptance(t, e, "AC1 Given 5 and 3 When Subtract is called Then it returns 2 [check: lint]",
		"Given 0 and 0 When Subtract is called Then it returns 0",
		"AC3 Subtract is fast (check: perf)")
	id := e.runIUV(t, 3)
	e.await(t, id, workflow.StatusWaitingHuman)

	fs := findings(t, e)
	if len(fs) != 2 {
		t.Fatalf("findings %+v", fs)
	}
	if f := fs[0]; f.Kind != pipeline.FindingNoID || f.Severity != "low" || f.Criterion != 1 ||
		!strings.Contains(f.Text, "Acceptance criterion 2 has no id") || !strings.Contains(f.Text, `"AC4 Given 0 and 0`) {
		t.Errorf("no id: %+v", f)
	}
	if f := fs[1]; f.Kind != pipeline.FindingUnknownCheck || f.Severity != "medium" || f.Criterion != 2 || f.Check != "perf" ||
		!strings.Contains(f.Text, `criterion AC3 names the check "perf", which this project does not run (its checks: build, test, lint, secrets)`) {
		t.Errorf("unknown check: %+v", f)
	}
	rv, _ := e.tasks.Reviews(context.Background(), e.task.ID)
	if len(rv) != 1 || rv[0].Verdict != "SHIP" {
		t.Fatalf("the findings must not change the verdict: %+v", rv)
	}
	if p := prompts(lg.reviewLog); len(p) != 1 || !strings.Contains(p[0], "name its id (for example AC2)") {
		t.Fatalf("the reviewer is still asked for the ids of the criteria that have one: %q", p)
	}
	ev, _ := e.tasks.Evidence(context.Background(), e.task.ID)
	tk, _ := e.tasks.Get(context.Background(), e.task.ID)
	cs := pipeline.CriteriaStatus(tk.Acceptance, ev, rv, nil)
	if c := cs[0]; c.Status != pipeline.CriterionMet || c.Source != pipeline.SourceCheck || c.By != "lint" || c.Check != "lint" || c.Evidence != "the newest lint check passed" {
		t.Errorf("AC1 %+v", c)
	}
	if c := cs[2]; c.Status != pipeline.CriterionOpen || c.Check != "perf" {
		t.Errorf("AC3 %+v", c)
	}
	evs, _ := e.bus.Replay(context.Background(), 0, events.Filter{TaskID: e.task.ID, Types: []string{"task.review_finding"}}, 0)
	if len(evs) != 2 {
		t.Errorf("task.review_finding events %d", len(evs))
	}
}

// What a named check says about its criterion, beside the test-name trace: the newest result with a verdict confirms or shows not
// met; a skipped run says nothing; a name that is not a check kind (an approval) never confirms; the findings name what is wrong.
func TestACriterionANamedCheckConfirms(t *testing.T) {
	ac := []string{"AC1: the code is lint-clean [check: lint]", "AC2: the page renders (check: E2E)", "AC3: approved [check: approval]", "AC4: plain"}
	yes, no := true, false
	ev := []tasks.Evidence{{Kind: "lint", Passed: &yes, Summary: "go vet ok", Created: 10}, {Kind: "e2e", Passed: &no, Summary: "2 specs failed", Created: 11},
		{Kind: "approval", Passed: &yes, Summary: "approved by human:kyle", Created: 12}}
	cs := pipeline.CriteriaStatus(ac, ev, nil, nil)
	if c := cs[0]; c.Status != pipeline.CriterionMet || c.By != "lint" || c.At != 10 {
		t.Errorf("AC1 %+v", c)
	}
	if c := cs[1]; c.Status != pipeline.CriterionNotMet || c.Check != "e2e" || c.Evidence != "the newest e2e check failed: 2 specs failed" {
		t.Errorf("AC2 %+v", c)
	}
	if c := cs[2]; c.Status != pipeline.CriterionOpen || c.Check != "approval" {
		t.Errorf("AC3: evidence that is not a check confirmed a criterion: %+v", c)
	}
	if c := cs[3]; c.Status != pipeline.CriterionOpen || c.Check != "" {
		t.Errorf("AC4 %+v", c)
	}
	// a newer failing lint run shows AC1 not met; a skipped one after it says nothing
	ev = append(ev, tasks.Evidence{Kind: "lint", Passed: &no, Summary: "go vet: unreachable code", Created: 20}, tasks.Evidence{Kind: "lint", Summary: "lint skipped", Created: 30})
	if c := pipeline.CriteriaStatus(ac, ev, nil, nil)[0]; c.Status != pipeline.CriterionNotMet || c.At != 20 || !strings.Contains(c.Evidence, "unreachable code") {
		t.Errorf("AC1 after the failing run %+v", c)
	}
	// a person's word newer than the check still counts, as before
	m := tasks.CriterionMark{Criterion: 0, SHA: tasks.CriterionSHA(ac[0]), Mark: tasks.MarkMet, Actor: "human:kyle", Note: "vet's finding is a false positive", Created: 40}
	if c := pipeline.CriteriaStatus(ac, ev, nil, []tasks.CriterionMark{m})[0]; c.Status != pipeline.CriterionMet || c.Source != pipeline.SourcePerson {
		t.Errorf("AC1 after the person %+v", c)
	}
	fs := pipeline.AcceptanceFindings(ac, []string{"build", "test", "lint", "e2e"})
	if len(fs) != 1 || fs[0].Kind != pipeline.FindingUnknownCheck || fs[0].Check != "approval" || fs[0].Criterion != 2 {
		t.Errorf("findings %+v", fs)
	}
	if fs := pipeline.AcceptanceFindings([]string{"Given a, then b [check: lint]"}, nil); len(fs) != 2 || !strings.Contains(fs[0].Text, `"AC1 Given a, then b`) ||
		!strings.Contains(fs[1].Text, "criterion 1 names the check \"lint\", which this project does not run (its checks: none)") {
		t.Errorf("findings without ids or checks %+v", fs)
	}
	if fs := pipeline.AcceptanceFindings(acList, []string{"test"}); len(fs) != 0 {
		t.Errorf("criteria with ids and no checks: %+v", fs)
	}
}

// takeoverEnv runs the two-pass script with a config per assignee: the first work turn is a1's; when it ends the task is
// reassigned to a3, so the second work turn is a3's and replaces a1's session (agent_change). The gateway's status says every
// agent has the handoff tool.
func takeoverEnv(t *testing.T, sc fakeagent.Script, firstTurn func(e *env, tk tasks.Task)) *env {
	t.Helper()
	e := sessionEnv(t, sc)
	if _, err := e.db.Writer().Exec(`INSERT INTO agents(id,project_id,name,adapter,role,created_at) VALUES('a3','p1','Backend #2','claude','backend',1)`); err != nil {
		t.Fatal(err)
	}
	e.deps.AgentConfig = func(tk tasks.Task, role string) rt.Config {
		return rt.Config{AgentID: tk.Assignee, Adapter: "claude", Provider: "fake", Tier: "premium", Spec: agents.RunSpec{AgentID: tk.Assignee, Model: "fake-1"}}
	}
	e.deps.MCPStatus = func(context.Context, tasks.Task, string, string) pipeline.MCPStatus {
		return pipeline.MCPStatus{Attached: true, Tools: []string{"handoff", "read_range"}}
	}
	turns := 0
	e.deps.OnTurn = func(ctx context.Context, tk tasks.Task, agentID, role, text string) {
		if role != "work" {
			return
		}
		if turns++; turns == 1 {
			if firstTurn != nil {
				firstTurn(e, tk)
			}
			a3 := "a3"
			if _, err := e.tasks.Patch(ctx, tk.ID, tasks.Update{Assignee: &a3}); err != nil {
				t.Error(err)
			}
		}
	}
	return e
}

func agentHandoff(t *testing.T, e *env, taskID, agentID, id string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"summary": "Subtract adds instead of subtracting.", "files": []string{"calc.go"},
		"next_step": "change a + b to a - b in Subtract", "checks": []map[string]any{{"name": "test", "passed": false}}})
	if _, err := e.db.Writer().Exec(`INSERT INTO handoffs(id,task_id,run_id,agent_id,artifact,reason,created_at) VALUES(?,?,?,?,?,?,?)`,
		id, taskID, "", agentID, string(raw), "agent", storage.Now()); err != nil {
		t.Error(err)
	}
}

// When another agent takes the task over, the outgoing agent is asked for its handoff in the same bounded read-only turn, with
// its own config, before the new agent's fresh session starts; what it writes passes Drydock's checks and seeds the new session.
func TestTheOutgoingAgentIsAskedForAHandoffWhenAnotherAgentTakesOver(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "handoff-written")
	sc := twoPassScript(0)
	sc.Rules = append([]fakeagent.Rule{{Match: "Drydock phase: handoff", Steps: []fakeagent.Step{{Say: "HANDOFF-NOW"}, {WaitFile: marker}, {Say: "hnd_takeover01"}}}}, sc.Rules...)
	e := takeoverEnv(t, sc, nil)
	// the stand-in "calls" the handoff tool: when the handoff turn says so, the handoff is stored as the tool stores it, under
	// the agent whose session said it
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var writer string
	events.Watch(ctx, e.bus, events.Filter{TaskID: e.task.ID, Types: []string{"agent.output"}}, func(ev events.Event) {
		if writer == "" && strings.Contains(string(ev.Data), "HANDOFF-NOW") {
			writer = ev.AgentID
			agentHandoff(t, e, e.task.ID, ev.AgentID, "hnd_takeover01")
			_ = os.WriteFile(marker, nil, 0o644)
		}
	})
	runID, _ := e.run(t, 3, workflow.StatusCompleted)
	if writer != "a1" {
		t.Fatalf("the handoff turn ran as %q, not the outgoing agent a1", writer)
	}
	outs := workOutputs(t, e, runID)
	if len(outs) != 2 || outs[1].Agent != "a3" || outs[1].Session == nil || outs[1].Session.Trigger != pipeline.HandoffAgentChange {
		t.Fatalf("work outputs %+v", outs)
	}
	req := outs[1].Session.Request
	if req == nil || !req.Asked || req.Outcome != pipeline.HandoffAnswered || req.Agent != "a1" || req.Session != "fake-session-0001" || req.Handoff != "hnd_takeover01" {
		t.Fatalf("request %+v", req)
	}
	args := argsLog(t)
	if len(args) != 3 || flagOf(args[1], "--resume") != "fake-session-0001" || flagOf(args[1], "--permission-mode") != "default" || flagOf(args[2], "--resume") != "" {
		t.Fatalf("sessions %q", args)
	}
	p := promptLog()
	if len(p) != 3 || !strings.Contains(p[1], "Drydock phase: handoff") || !strings.Contains(p[2], "The previous session's own handoff (hnd_takeover01") ||
		!strings.Contains(p[2], "Next step: change a + b to a - b in Subtract") {
		t.Fatalf("prompts %d:\n%s", len(p), strings.Join(p, "\n-----\n"))
	}
	if v := verification(t, e, "hnd_takeover01"); v["status"] != "accepted" {
		t.Fatalf("verification %v", v)
	}
}

// An outgoing agent whose own handoff from its work turn already passes Drydock's checks is not asked again (nothing is missing);
// one that left none is asked, and when it does not answer the new agent gets Drydock's handoff.
func TestAnOutgoingAgentIsAskedOnlyWhenItsHandoffIsMissing(t *testing.T) {
	withHandoffTurn := func(steps []fakeagent.Step) fakeagent.Script {
		sc := twoPassScript(0)
		sc.Rules = append([]fakeagent.Rule{{Match: "Drydock phase: handoff", Steps: steps}}, sc.Rules...)
		return sc
	}
	t.Run("its own handoff passes", func(t *testing.T) {
		e := takeoverEnv(t, withHandoffTurn([]fakeagent.Step{{Say: "should not be asked"}}), func(e *env, tk tasks.Task) { agentHandoff(t, e, tk.ID, "a1", "hnd_ownturn001") })
		runID, _ := e.run(t, 3, workflow.StatusCompleted)
		req := workOutputs(t, e, runID)[1].Session.Request
		if req == nil || req.Asked || req.Outcome != pipeline.HandoffNotAsked || req.Agent != "a1" ||
			req.Reason != "the outgoing session's own handoff hnd_ownturn001 from its turn passed Drydock's checks, so nothing is missing to ask for" {
			t.Fatalf("request %+v", req)
		}
		if p := promptLog(); len(p) != 2 || !strings.Contains(p[1], "The previous session's own handoff (hnd_ownturn001") {
			t.Fatalf("prompts %d:\n%s", len(p), strings.Join(p, "\n-----\n"))
		}
	})
	t.Run("it left none and does not answer", func(t *testing.T) {
		e := takeoverEnv(t, withHandoffTurn([]fakeagent.Step{{Say: "I would rather keep working."}}), nil)
		runID, _ := e.run(t, 3, workflow.StatusCompleted)
		req := workOutputs(t, e, runID)[1].Session.Request
		if req == nil || !req.Asked || req.Outcome != pipeline.HandoffNoAnswer || req.Agent != "a1" || req.Reason != "the outgoing session ended its turn without writing a handoff" {
			t.Fatalf("request %+v", req)
		}
		if p := promptLog(); len(p) != 3 || !strings.Contains(p[2], "What the previous session said it did:\nimplemented Subtract.") {
			t.Fatalf("prompts %d:\n%s", len(p), strings.Join(p, "\n-----\n"))
		}
	})
}

// Drydock's estimate for a CLI that reports no occupancy: what was sent and seen, a 25 % margin and 10,000 tokens for the CLI's
// own prompt, held to 50 % of the window; a session continued on it adds to it; a CLI that reports occupancy gets none.
func TestDrydocksOccupancyEstimate(t *testing.T) {
	res := rt.TurnResult{SeenBytes: 40_000, Usage: []workflow.UsageChunk{{OutputTokens: 2_000}}}
	e := pipeline.EstimateOccupancy(nil, 8_000, res, 0)
	if e == nil || e.Sent != 8_000 || e.Seen != 10_000 || e.Used != 32_500 || e.Size != pipeline.DefaultEstimateWindow || e.Window != pipeline.WindowDefault {
		t.Fatalf("estimate %+v", e)
	}
	// the output the CLI reported counts when it is more than what the runtime saw
	if e2 := pipeline.EstimateOccupancy(e, 1_000, rt.TurnResult{SeenBytes: 400, Usage: []workflow.UsageChunk{{OutputTokens: 3_000}}}, 100_000); e2.Sent != 9_000 || e2.Seen != 13_000 ||
		e2.Used != 37_500 || e2.Size != 100_000 || e2.Window != pipeline.WindowCatalog {
		t.Fatalf("continued estimate %+v", e2)
	}
	if pipeline.EstimateOccupancy(nil, 8_000, rt.TurnResult{Usage: []workflow.UsageChunk{{ContextUsed: 10, ContextSize: 200_000}}}, 0) != nil {
		t.Fatal("a reported occupancy needs no estimate")
	}
	plan := pipeline.SessionPlan{Agent: "a1", Adapter: "claude", Model: "m-1", CanResume: true}
	est := func(used int64) *pipeline.WorkOutput {
		w := prevTurn(0, 0)
		w.Estimate = &pipeline.ContextEstimate{Used: used, Size: 128_000}
		return w
	}
	if d := pipeline.DecideSession(est(63_999), plan); d.Action != pipeline.SessionContinue || !d.Estimated || d.ThresholdPct != 50 ||
		!strings.Contains(d.Reason, "Drydock's estimate: the CLI reports no occupancy, so an estimate is held to 50%, not 70%") {
		t.Errorf("below 50 %%: %+v", d)
	}
	if d := pipeline.DecideSession(est(64_000), plan); d.Action != pipeline.SessionHandoff || d.Trigger != pipeline.HandoffContextPressure || !d.Estimated || d.OccupancyPct != 50 {
		t.Errorf("at 50 %%: %+v", d)
	}
	if d := pipeline.DecideSession(prevTurn(0, 0), plan); d.Action != pipeline.SessionFresh || d.Estimated {
		t.Errorf("no occupancy and no estimate: %+v", d)
	}
	// a reported figure wins over an estimate and keeps the 70 % threshold
	w := prevTurn(100_000, 200_000)
	w.Estimate = &pipeline.ContextEstimate{Used: 127_000, Size: 128_000}
	if d := pipeline.DecideSession(w, plan); d.Action != pipeline.SessionContinue || d.Estimated || d.ThresholdPct != 70 {
		t.Errorf("reported: %+v", d)
	}
}

// End to end with a stand-in that reports no occupancy: the first turn's output carries Drydock's estimate, the second turn
// continues the session on it (recorded as an estimate in the decision, the manifest and the event); with a window so small that
// the estimate is at 50 %, the second turn starts a fresh session with a handoff instead.
func TestASessionTheCLIReportsNoOccupancyForContinuesOnDrydocksEstimate(t *testing.T) {
	sc := twoPassScript(0)
	sc.NoOccupancy = true
	e := sessionEnv(t, sc)
	runID, _ := e.run(t, 3, workflow.StatusCompleted)
	outs := workOutputs(t, e, runID)
	if len(outs) != 2 || outs[0].Estimate == nil || outs[0].Estimate.Window != pipeline.WindowDefault || outs[0].Estimate.Sent <= 0 || outs[0].Estimate.Seen <= 0 {
		t.Fatalf("first turn's estimate %+v", outs[0].Estimate)
	}
	s := outs[1].Session
	if s.Action != pipeline.SessionContinue || !s.Estimated || s.ThresholdPct != pipeline.EstimatedHandoffPct || s.ContextUsed != outs[0].Estimate.Used {
		t.Fatalf("second turn's decision %+v", s)
	}
	if outs[1].Estimate == nil || outs[1].Estimate.Sent <= outs[0].Estimate.Sent || outs[1].Estimate.Seen < outs[0].Estimate.Seen {
		t.Fatalf("the continued session's estimate adds to the first: %+v then %+v", outs[0].Estimate, outs[1].Estimate)
	}
	if args := argsLog(t); len(args) != 2 || flagOf(args[1], "--resume") != "fake-session-0001" {
		t.Fatalf("sessions %q", args)
	}
	if it := manifestItems(t, e, runID, "work", 1)["previous session transcript"]; !it.Included || !strings.Contains(it.Reason, "Drydock's estimate") {
		t.Fatalf("manifest %+v", it)
	}
	if ev := sessionEvents(t, e); len(ev) != 2 || ev[1]["estimated"] != true {
		t.Fatalf("events %v", ev)
	}

	t.Run("a small window", func(t *testing.T) {
		e := sessionEnv(t, sc)
		e.deps.ContextWindow = func(context.Context, rt.Config) int64 { return 20_000 }
		runID, _ := e.run(t, 3, workflow.StatusCompleted)
		outs := workOutputs(t, e, runID)
		if len(outs) != 2 || outs[0].Estimate.Size != 20_000 || outs[0].Estimate.Window != pipeline.WindowCatalog {
			t.Fatalf("estimate %+v", outs[0].Estimate)
		}
		if s := outs[1].Session; s.Action != pipeline.SessionHandoff || s.Trigger != pipeline.HandoffContextPressure || !s.Estimated || s.Handoff == "" {
			t.Fatalf("decision %+v", s)
		}
	})
}

// A session that was corrected twice (each time a failing check sent the work back) is not continued a third time: the next
// work turn is a fresh session with a handoff, reason corrections; the fresh session then counts its own corrections.
func TestASessionCorrectedTwiceRestartsWithAHandoff(t *testing.T) {
	e := sessionEnv(t, fakeagent.Script{
		Rules: []fakeagent.Rule{
			{Match: "This is a fresh session", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "fixed in a fresh session"}}},
			{Match: "previous attempt needs changes", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: wrongImpl + "\n"}}, {Say: "tried again"}}},
		},
		Turns: [][]fakeagent.Step{{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: wrongImpl}}, {Say: "implemented Subtract."}}},
	})
	runID, _ := e.run(t, 5, workflow.StatusCompleted)
	outs := workOutputs(t, e, runID)
	if len(outs) != 4 {
		t.Fatalf("work turns %d", len(outs))
	}
	for i, want := range []struct {
		action      string
		corrections int
	}{{pipeline.SessionFresh, 0}, {pipeline.SessionContinue, 1}, {pipeline.SessionContinue, 2}, {pipeline.SessionHandoff, 1}} {
		if outs[i].Session.Action != want.action || outs[i].Corrections != want.corrections {
			t.Errorf("turn %d: %s with %d corrections, want %s with %d", i+1, outs[i].Session.Action, outs[i].Corrections, want.action, want.corrections)
		}
	}
	s := outs[3].Session
	if s.Trigger != pipeline.HandoffCorrections || s.Corrections != 2 || !strings.Contains(s.Reason, "the session was corrected 2 times") || s.Handoff == "" {
		t.Fatalf("decision %+v", s)
	}
	if hs := handoffRows(t, e); len(hs) != 1 || hs[0].reason != pipeline.HandoffCorrections {
		t.Fatalf("handoffs %+v", hs)
	}
	args := argsLog(t)
	if len(args) != 4 || flagOf(args[3], "--resume") != "" || flagOf(args[2], "--resume") != "fake-session-0001" {
		t.Fatalf("sessions %q", args)
	}
	if ev := sessionEvents(t, e); len(ev) != 4 || ev[3]["trigger"] != pipeline.HandoffCorrections || ev[3]["corrections"] != 2.0 {
		t.Fatalf("events %v", ev)
	}
}

// Guidance and a merge conflict are not corrections; a failing check and a review that sends the work back are.
func TestWhatCountsAsACorrection(t *testing.T) {
	plan := pipeline.SessionPlan{Agent: "a1", Adapter: "claude", Model: "m-1", CanResume: true}
	w := prevTurn(10, 200_000)
	w.Corrections = 1
	if d := pipeline.DecideSession(w, plan); d.Action != pipeline.SessionContinue || d.Corrections != 1 {
		t.Errorf("one correction: %+v", d)
	}
	w.Corrections = 2
	if d := pipeline.DecideSession(w, plan); d.Action != pipeline.SessionHandoff || d.Trigger != pipeline.HandoffCorrections {
		t.Errorf("two corrections: %+v", d)
	}
	// the context threshold is the more pressing reason when both hold
	w = prevTurn(150_000, 200_000)
	w.Corrections = 2
	if d := pipeline.DecideSession(w, plan); d.Trigger != pipeline.HandoffContextPressure {
		t.Errorf("both: %+v", d)
	}
	for _, tc := range []struct {
		inbox string
		want  bool
	}{
		{`{"name":"test","passed":false,"exitCode":1}`, true},
		{`{"name":"test","passed":true}`, false},
		{`{"verdict":"REVISE","feedback":"AC1 is not met"}`, true},
		{`{"feedback":"please rename it","from":"human"}`, true},
		{`{"guidance":"try the table approach","from":"lead"}`, false},
		{`{"conflict":{"files":["calc.go"],"target":"main"}}`, false},
		{`{"watchdog":"you are repeating yourself"}`, false},
	} {
		if got := pipeline.Corrected(workflow.NodeInput{Inbox: []workflow.Message{{Value: json.RawMessage(tc.inbox)}}}); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.inbox, got, tc.want)
		}
	}
}

package pipeline_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The session policy's tests use the scripted Claude stand-in (internal/agents/fakeagent, a labelled test double that speaks
// Claude Code's stream-json): it reports the context occupancy a step sets, keeps a resumed session's running totals, and
// refuses --resume of a session it does not have the way Claude Code does.

// sessionEnv is a pipeline env whose stand-in logs every session's argv and keeps session totals across processes.
func sessionEnv(t *testing.T, s fakeagent.Script) *env {
	t.Helper()
	ad, cfg := fakeEnv(t, s)
	t.Setenv("DRYDOCK_FAKE_ARGS_LOG", filepath.Join(t.TempDir(), "args.log"))
	t.Setenv("DRYDOCK_FAKE_STATE_DIR", t.TempDir())
	return setup(t, cfg, ad)
}

// argsLog is the argv of every session the stand-in started, in order.
func argsLog(t *testing.T) [][]string {
	t.Helper()
	b, _ := os.ReadFile(os.Getenv("DRYDOCK_FAKE_ARGS_LOG"))
	var out [][]string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var a []string
		if json.Unmarshal([]byte(l), &a) == nil {
			out = append(out, a)
		}
	}
	return out
}

func flagOf(args []string, flag string) string {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

// workOutputs are the run's work turns' outputs, in order.
func workOutputs(t *testing.T, e *env, runID workflow.RunID) []pipeline.WorkOutput {
	t.Helper()
	rows, err := e.eng.Executions(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	var out []pipeline.WorkOutput
	for _, r := range rows {
		if r.NodeID != "work" || r.Status != "done" {
			continue
		}
		var no workflow.NodeOutput
		if err := json.Unmarshal(r.Output, &no); err != nil {
			t.Fatalf("node output %s: %v", r.Output, err)
		}
		var w pipeline.WorkOutput
		raw := no.Value
		if v, err := workflow.ReadRef(raw); err == nil {
			raw = v
		}
		if err := json.Unmarshal(raw, &w); err != nil {
			t.Fatalf("work output %s: %v", r.Output, err)
		}
		out = append(out, w)
	}
	return out
}

type handoffRow struct{ id, reason, agent, artifact string }

func handoffRows(t *testing.T, e *env) []handoffRow {
	t.Helper()
	rows, err := e.db.Reader().Query(`SELECT id, reason, agent_id, artifact FROM handoffs WHERE task_id=? ORDER BY created_at`, e.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []handoffRow
	for rows.Next() {
		var h handoffRow
		if err := rows.Scan(&h.id, &h.reason, &h.agent, &h.artifact); err != nil {
			t.Fatal(err)
		}
		out = append(out, h)
	}
	return out
}

func sessionEvents(t *testing.T, e *env) []map[string]any {
	t.Helper()
	evs, err := e.bus.Replay(context.Background(), 0, events.Filter{TaskID: e.task.ID, Types: []string{"agent.session"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, ev := range evs {
		var m map[string]any
		_ = json.Unmarshal(ev.Data, &m)
		out = append(out, m)
	}
	return out
}

// A first pass that writes a wrong Subtract and ends with the given context occupancy; the second pass fixes it. The first pass
// also says something mid-turn that is part of its transcript only.
func twoPassScript(context int64) fakeagent.Script {
	return fakeagent.Script{
		Rules: []fakeagent.Rule{{Match: "previous attempt needs changes", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "fixed"}}}},
		Turns: [][]fakeagent.Step{{
			{Say: "scratch note XYZZY: reading calc.go first", Context: context},
			{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: wrongImpl}},
			{Say: "implemented Subtract.\nShould Subtract saturate on overflow?"},
		}},
	}
}

func prevTurn(used, size int64) *pipeline.WorkOutput {
	return &pipeline.WorkOutput{TurnResult: rt.TurnResult{SessionID: "s-1", Usage: []workflow.UsageChunk{{ContextUsed: used, ContextSize: size, Totals: json.RawMessage(`{"costUsd":1}`)}}},
		Agent: "a1", Adapter: "claude", Model: "m-1"}
}

// The occupancy at which the next turn hands off: at or above 70 % of the model's window, never below; and the other reasons a
// session is not continued. Then end to end: a first turn that ends at 75 % makes the second turn a fresh session seeded with a
// handoff artifact, recorded in the turn's output, its manifest, an agent.session event and the handoffs table.
func TestOccupancyThresholdTriggersHandoff(t *testing.T) {
	plan := pipeline.SessionPlan{Agent: "a1", Adapter: "claude", Model: "m-1", CanResume: true}
	for _, tc := range []struct {
		name    string
		prev    *pipeline.WorkOutput
		plan    func(p *pipeline.SessionPlan)
		action  string
		trigger string
	}{
		{"first turn", nil, nil, pipeline.SessionFresh, ""},
		{"69.9 %", prevTurn(139_999, 200_000), nil, pipeline.SessionContinue, ""},
		{"70 %", prevTurn(140_000, 200_000), nil, pipeline.SessionHandoff, pipeline.HandoffContextPressure},
		{"95 %", prevTurn(190_000, 200_000), nil, pipeline.SessionHandoff, pipeline.HandoffContextPressure},
		{"no occupancy reported", prevTurn(0, 0), nil, pipeline.SessionFresh, ""},
		{"CLI cannot resume", prevTurn(10, 200_000), func(p *pipeline.SessionPlan) { p.CanResume = false }, pipeline.SessionFresh, ""},
		{"escalation", prevTurn(10, 200_000), func(p *pipeline.SessionPlan) { p.Model, p.Escalated = "m-2", true }, pipeline.SessionHandoff, pipeline.HandoffEscalation},
		{"other model", prevTurn(10, 200_000), func(p *pipeline.SessionPlan) { p.Model = "m-2" }, pipeline.SessionHandoff, pipeline.HandoffModelChange},
		{"same model after an escalation", prevTurn(10, 200_000), func(p *pipeline.SessionPlan) { p.Escalated = true }, pipeline.SessionContinue, ""},
		{"another agent", prevTurn(10, 200_000), func(p *pipeline.SessionPlan) { p.Agent = "a9" }, pipeline.SessionHandoff, pipeline.HandoffAgentChange},
		{"interrupted attempt", prevTurn(10, 200_000), func(p *pipeline.SessionPlan) { p.Interrupted = true }, pipeline.SessionHandoff, pipeline.HandoffInterrupted},
		{"after a watchdog pause", prevTurn(10, 200_000), func(p *pipeline.SessionPlan) { p.WatchdogPause = true }, pipeline.SessionHandoff, pipeline.HandoffWatchdog},
	} {
		p := plan
		if tc.plan != nil {
			tc.plan(&p)
		}
		d := pipeline.DecideSession(tc.prev, p)
		if d.Action != tc.action || d.Trigger != tc.trigger || d.ThresholdPct != 70 || d.Reason == "" {
			t.Errorf("%s: %+v, want %s %s", tc.name, d, tc.action, tc.trigger)
		}
	}
	if d := pipeline.DecideSession(prevTurn(150_000, 200_000), plan); d.OccupancyPct != 75 || d.ContextUsed != 150_000 || d.ContextSize != 200_000 || d.PrevSession != "s-1" {
		t.Errorf("the decision records the occupancy: %+v", d)
	}

	e := sessionEnv(t, twoPassScript(150_000))
	runID, _ := e.run(t, 3, workflow.StatusCompleted)
	args := argsLog(t)
	if len(args) != 2 || flagOf(args[1], "--resume") != "" {
		t.Fatalf("the second turn must start a fresh session: %q", args)
	}
	outs := workOutputs(t, e, runID)
	if len(outs) != 2 || outs[0].Session == nil || outs[0].Session.Action != pipeline.SessionFresh || outs[1].Session == nil {
		t.Fatalf("work outputs %+v", outs)
	}
	s := outs[1].Session
	if s.Action != pipeline.SessionHandoff || s.Trigger != pipeline.HandoffContextPressure || s.OccupancyPct != 75 || s.ThresholdPct != 70 ||
		s.PrevSession != outs[0].SessionID || s.Handoff == "" {
		t.Fatalf("second turn's decision %+v", s)
	}
	hs := handoffRows(t, e)
	if len(hs) != 1 || hs[0].id != s.Handoff || hs[0].reason != pipeline.HandoffContextPressure || hs[0].agent != "a1" {
		t.Fatalf("handoffs %+v", hs)
	}
	var art pipeline.HandoffArtifact
	if err := json.Unmarshal([]byte(hs[0].artifact), &art); err != nil {
		t.Fatalf("artifact %s: %v", hs[0].artifact, err)
	}
	if art.Task.Title != "Implement Subtract" || len(art.Task.Acceptance) != 1 || !strings.Contains(art.Done, "implemented Subtract") ||
		art.DiffStat != "1 file(s), +1 -4" || !slices.Contains(art.FilesTouched, "calc.go") || len(art.FailingChecks) != 1 ||
		!strings.Contains(art.FailingChecks[0], "TestSubtract") || len(art.OpenQuestions) != 1 || art.ContextSize != 200_000 {
		t.Fatalf("artifact %+v", art)
	}
	evs := sessionEvents(t, e)
	if len(evs) != 2 || evs[0]["action"] != pipeline.SessionFresh || evs[1]["action"] != pipeline.SessionHandoff || evs[1]["trigger"] != pipeline.HandoffContextPressure ||
		evs[1]["occupancyPct"] != 75.0 || evs[1]["thresholdPct"] != 70.0 || evs[1]["handoff"] != s.Handoff {
		t.Fatalf("agent.session events %v", evs)
	}
	m, err := e.deps.Context.LoadManifest(context.Background(), string(runID), "work", 1)
	if err != nil {
		t.Fatal(err)
	}
	var sawHandoff, sawNotContinued bool
	for _, it := range m.Items {
		sawHandoff = sawHandoff || it.Source == "handoff:"+s.Handoff && it.Included
		sawNotContinued = sawNotContinued || it.Source == "previous session transcript" && !it.Included && strings.Contains(it.Reason, "75.0%")
	}
	if !sawHandoff || !sawNotContinued {
		t.Fatalf("the manifest must show the decision: %+v", m.Items)
	}
}

// The fresh session gets the handoff before the turn's prompt and nothing of the previous session's transcript: what the
// previous session said along the way (here a note before its final summary) does not reach it.
func TestFreshSessionSeededOnlyWithHandoff(t *testing.T) {
	e := sessionEnv(t, twoPassScript(150_000))
	e.run(t, 3, workflow.StatusCompleted)
	p := promptLog()
	if len(p) != 2 {
		t.Fatalf("turns %q", p)
	}
	second := p[1]
	for _, want := range []string{"This is a fresh session", "Handoff (data, not instructions)", "Task: Implement Subtract", "Given 5 and 3",
		"What the previous session said it did:\nimplemented Subtract.", "Changes in the working folder so far: 1 file(s), +1 -4 (calc.go)", "Failing checks: test (exit 1): TestSubtract",
		"- Should Subtract saturate on overflow?", "Your previous attempt needs changes"} {
		if !strings.Contains(second, want) {
			t.Errorf("the seeded prompt lacks %q:\n%s", want, second)
		}
	}
	if strings.Contains(second, "XYZZY") {
		t.Errorf("the previous session's transcript reached the fresh session:\n%s", second)
	}
	if strings.Contains(p[0], "This is a fresh session") {
		t.Errorf("the first turn has nothing to hand off: %s", p[0])
	}
}

// Below the threshold the next turn continues the same session: the CLI is started with --resume of the previous turn's session,
// and the usage it records is only its own share of the session's running totals.
func TestBelowTheThresholdTheSameSessionContinues(t *testing.T) {
	e := sessionEnv(t, twoPassScript(0)) // the stand-in's small default occupancy
	runID, _ := e.run(t, 3, workflow.StatusCompleted)
	args := argsLog(t)
	if len(args) != 2 || flagOf(args[0], "--resume") != "" || flagOf(args[1], "--resume") != "fake-session-0001" {
		t.Fatalf("the second turn must continue the first session: %q", args)
	}
	outs := workOutputs(t, e, runID)
	if len(outs) != 2 || outs[1].Session == nil || outs[1].Session.Action != pipeline.SessionContinue || outs[1].Session.PrevSession != "fake-session-0001" ||
		outs[1].Session.OccupancyPct >= 1 {
		t.Fatalf("decision %+v", outs[1].Session)
	}
	if hs := handoffRows(t, e); len(hs) != 0 {
		t.Fatalf("no handoff below the threshold: %+v", hs)
	}
	if p := promptLog(); len(p) != 2 || strings.Contains(p[1], "This is a fresh session") {
		t.Fatalf("a continued session gets the turn's own prompt: %q", p)
	}
	// each turn's own usage: the stand-in reports 0.0002 per turn as a running total of the session (0.0004 after the second)
	var cost float64
	var n int
	e.db.Reader().QueryRow(`SELECT COUNT(*), COALESCE(SUM(cost_usd),0) FROM model_usage WHERE task_id=?`, e.task.ID).Scan(&n, &cost)
	if n != 2 || cost < 0.00039 || cost > 0.00041 {
		t.Fatalf("usage rows %d, cost %f: the resumed turn must record only its own share", n, cost)
	}
	m, _ := e.deps.Context.LoadManifest(context.Background(), string(runID), "work", 1)
	found := false
	for _, it := range m.Items {
		found = found || it.Source == "previous session transcript" && it.Included && strings.Contains(it.Reason, "continued session fake-session-0001")
	}
	if !found {
		t.Fatalf("the manifest must say the session continued: %+v", m.Items)
	}
}

// A session that cannot be resumed (the CLI no longer has it) does not fail the turn: the work goes to a fresh session seeded
// with a handoff artifact.
func TestAResumeThatFailsStartsAFreshSessionWithAHandoff(t *testing.T) {
	s := twoPassScript(0)
	s.MissingSessions = []string{"fake-session-0001"}
	e := sessionEnv(t, s)
	runID, _ := e.run(t, 3, workflow.StatusCompleted)
	args := argsLog(t)
	if len(args) != 3 || flagOf(args[1], "--resume") != "fake-session-0001" || flagOf(args[2], "--resume") != "" {
		t.Fatalf("starts %q", args)
	}
	outs := workOutputs(t, e, runID)
	if len(outs) != 2 || outs[1].Session.Action != pipeline.SessionHandoff || outs[1].Session.Trigger != pipeline.HandoffResumeFailed {
		t.Fatalf("decision %+v", outs[1].Session)
	}
	if hs := handoffRows(t, e); len(hs) != 1 || hs[0].reason != pipeline.HandoffResumeFailed {
		t.Fatalf("handoffs %+v", hs)
	}
	if p := promptLog(); len(p) != 2 || !strings.Contains(p[1], "could not be resumed") {
		t.Fatalf("prompts %q", p)
	}
}

// When the router escalates a turn to another model it asks for a handoff (RoutedModel.Handoff): the new model's session is a
// fresh one seeded with the handoff artifact, even though the previous session was far below the occupancy threshold.
func TestEscalationSeedsTheNewModelsSessionWithAHandoff(t *testing.T) {
	e := sessionEnv(t, twoPassScript(0))
	turns := 0
	e.eng.Use(workflow.KindAgentTask, func(next workflow.NodeHandler) workflow.NodeHandler {
		return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
			turns++
			r := pipeline.RoutedModel{Model: "cheap-1", Tier: "cheap"}
			if turns > 1 {
				r = pipeline.RoutedModel{Model: "premium-1", Tier: "premium", Handoff: true}
			}
			return next(pipeline.WithRoutedModel(ctx, r), in)
		}
	})
	runID, _ := e.run(t, 3, workflow.StatusCompleted)
	args := argsLog(t)
	if len(args) != 2 || flagOf(args[0], "--model") != "cheap-1" || flagOf(args[1], "--model") != "premium-1" || flagOf(args[1], "--resume") != "" {
		t.Fatalf("starts %q", args)
	}
	outs := workOutputs(t, e, runID)
	s := outs[len(outs)-1].Session
	if s == nil || s.Action != pipeline.SessionHandoff || s.Trigger != pipeline.HandoffEscalation || s.FromModel != "cheap-1" || s.ToModel != "premium-1" {
		t.Fatalf("decision %+v", s)
	}
	hs := handoffRows(t, e)
	if len(hs) != 1 || hs[0].reason != pipeline.HandoffEscalation || hs[0].id != s.Handoff {
		t.Fatalf("handoffs %+v", hs)
	}
	if p := promptLog(); len(p) != 2 || !strings.Contains(p[1], "the router escalated the turn from cheap-1 to premium-1") || !strings.Contains(p[1], "Handoff (data, not instructions)") {
		t.Fatalf("the escalated session must get the handoff: %q", p)
	}
}

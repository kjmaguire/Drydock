package pipeline_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Scripted stand-in agents (labelled test doubles, the fakeagent process) drive these; git, go test, SQLite and the engine
// are real.

func wrongWorker(rules ...fakeagent.Rule) fakeagent.Script {
	return fakeagent.Script{Rules: rules, Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: wrongImpl}}, {Say: "implemented"}}}
}

func limitPayload(t *testing.T, p workflow.PendingRequest) pipeline.LimitPayload {
	t.Helper()
	var lp pipeline.LimitPayload
	if err := json.Unmarshal(p.Spec.Payload, &lp); err != nil {
		t.Fatalf("limit payload: %v (%s)", err, p.Spec.Payload)
	}
	return lp
}

func (e *env) pendingOne(t *testing.T, id workflow.RunID) workflow.PendingRequest {
	t.Helper()
	pend, _ := e.eng.Pending(context.Background(), id)
	if len(pend) != 1 {
		t.Fatalf("pending %+v", pend)
	}
	return pend[0]
}

func (e *env) eventTypes(t *testing.T) map[string]int {
	t.Helper()
	evs, _ := e.bus.Replay(context.Background(), 0, events.Filter{TaskID: e.task.ID}, 0)
	out := map[string]int{}
	for _, x := range evs {
		out[x.Type]++
	}
	return out
}

// Item 3: the default workflow no longer dead-ends at the iteration limit. A human "continue" with guidance resets the loop
// budget (the loop runs its whole budget again) and the guidance reaches the worker as readable text.
func TestIterationLimitHumanContinueResetsTheBudgetAndDeliversGuidance(t *testing.T) {
	ad, cfg, lg := fakeRoles(t,
		wrongWorker(fakeagent.Rule{Match: "GUIDANCE-7", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "fixed with the guidance"}}}),
		fakeagent.Script{Default: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}})
	e := setup(t, cfg, ad)
	id := e.runIUV(t, 1)
	e.await(t, id, workflow.StatusWaitingHuman)
	p := e.pendingOne(t, id)
	if p.NodeID != pipeline.NodeLimit || !strings.Contains(p.Spec.Title, "Iteration limit") {
		t.Fatalf("pending %+v", p)
	}
	if lp := limitPayload(t, p); lp.Trigger != pipeline.TriggerIterationLimit || lp.Origin != "verify" || lp.Lead != nil {
		t.Fatalf("payload %+v", lp)
	}
	if tk, _ := e.tasks.Get(context.Background(), e.task.ID); tk.Status != tasks.WaitingHuman {
		t.Fatalf("task %s", tk.Status)
	}
	if n := len(prompts(lg.workLog)); n != 2 {
		t.Fatalf("work turns before the limit: %d", n)
	}
	if err := e.answer(t, id, "answered", "human:kyle", `{"text":"GUIDANCE-7: Subtract must use the minus operator"}`); err != nil {
		t.Fatal(err)
	}
	e.await(t, id, workflow.StatusWaitingHuman)
	wp := prompts(lg.workLog)
	if len(wp) != 3 || !strings.Contains(wp[2], "Guidance from the human:\nGUIDANCE-7: Subtract must use the minus operator") || strings.Contains(wp[2], `{"text"`) {
		t.Fatalf("the guidance must reach the worker as text: %q", wp)
	}
	p = e.pendingOne(t, id)
	if p.NodeID != "approve" {
		t.Fatalf("after the continue the work went through checks and review to the approval, got %s", p.NodeID)
	}
	if ev := e.eventTypes(t); ev["task.continued"] != 1 {
		t.Fatalf("events %v", ev)
	}
}

// With a fresh budget a still-failing worker gets the loop's whole budget again (no immediate re-exhaustion).
func TestContinueGivesTheWholeLoopBudgetAgain(t *testing.T) {
	ad, cfg, lg := fakeRoles(t, wrongWorker(), fakeagent.Script{Default: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}})
	e := setup(t, cfg, ad)
	id := e.runIUV(t, 2)
	e.await(t, id, workflow.StatusWaitingHuman)
	if n := len(prompts(lg.workLog)); n != 3 {
		t.Fatalf("first round turns %d", n)
	}
	if err := e.answer(t, id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	e.await(t, id, workflow.StatusWaitingHuman)
	wp := prompts(lg.workLog)
	if len(wp) != 6 {
		t.Fatalf("a continue must give 1 + 2 more turns, got %d in total", len(wp))
	}
	if strings.Contains(wp[3], "Guidance") {
		t.Fatalf("a bare continue is not guidance: %q", wp[3])
	}
}

// "stop" (reject, or data.action stop) cancels the task cleanly: a terminal task and a completed run.
func TestIterationLimitStopCancelsTheTaskCleanly(t *testing.T) {
	for name, answer := range map[string][2]string{"reject": {"rejected", `{}`}, "action stop": {"approved", `{"action":"stop"}`}} {
		t.Run(name, func(t *testing.T) {
			ad, cfg, _ := fakeRoles(t, wrongWorker(), fakeagent.Script{Default: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}})
			e := setup(t, cfg, ad)
			id := e.runIUV(t, 1)
			e.await(t, id, workflow.StatusWaitingHuman)
			if err := e.answer(t, id, answer[0], "human:kyle", answer[1]); err != nil {
				t.Fatal(err)
			}
			e.await(t, id, workflow.StatusCompleted)
			if tk, _ := e.tasks.Get(context.Background(), e.task.ID); tk.Status != tasks.Cancelled {
				t.Fatalf("task %s", tk.Status)
			}
		})
	}
}

// Only a human can continue past a limit.
func TestOnlyAHumanContinuesPastALimit(t *testing.T) {
	ad, cfg, _ := fakeRoles(t, wrongWorker(), fakeagent.Script{Default: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}})
	e := setup(t, cfg, ad)
	id := e.runIUV(t, 1)
	e.await(t, id, workflow.StatusWaitingHuman)
	if err := e.answer(t, id, "approved", "a1", `{}`); err != nil {
		t.Fatal(err)
	}
	if info := e.await(t, id, workflow.StatusFailed); !strings.Contains(info.FailedReason, "only a human") {
		t.Fatalf("reason %q", info.FailedReason)
	}
}

// Item 10: the reviewer repeating the same REVISE feedback is detected after two rounds and escalated (no lead: the human
// is asked), long before the loop budget runs out.
func TestRepeatedReviseFeedbackEscalatesToTheHuman(t *testing.T) {
	ad, cfg, lg := fakeRoles(t,
		fakeagent.Script{Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImplHack}}, {Say: "implemented"}}},
		fakeagent.Script{Default: []fakeagent.Step{{Say: `{"verdict":"REVISE","feedback":"Remove the HACK comment."}`}}})
	e := setup(t, cfg, ad)
	id := e.runIUV(t, 5)
	e.await(t, id, workflow.StatusWaitingHuman)
	if n := len(prompts(lg.reviewLog)); n != 2 {
		t.Fatalf("the repeat is caught after 2 review rounds, got %d", n)
	}
	p := e.pendingOne(t, id)
	lp := limitPayload(t, p)
	if p.NodeID != pipeline.NodeLimit || !strings.Contains(p.Spec.Title, "same change") || lp.Trigger != pipeline.TriggerRepeatedRevise || lp.Origin != "review" || !strings.Contains(lp.Evidence, "Remove the HACK comment.") {
		t.Fatalf("pending %+v payload %+v", p, lp)
	}
	if ev := e.eventTypes(t); ev["task.review_repeated"] != 1 {
		t.Fatalf("events %v", ev)
	}
}

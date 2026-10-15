package core_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/router"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The fallback chain of an agent turn (docs/model-routing.md s8 "Fallback chain", decision P53 (b)), end to end through the
// workflow, the real claude adapter and parser, and the labelled fake agent, whose sessions on a model named in ModelErrors
// end every turn the way Claude Code reports a failed API call.

const overloaded = `API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`

type fallbackRig struct {
	a      *app
	capt   *captureAdapter
	p      core.Project
	worker core.Agent
	review core.Agent
	task   tasks.Task
}

// newFallbackRig makes a worker whose router may choose models (cheap ones and one premium), a rule naming the chain for
// cheap models, and a task (not started).
func newFallbackRig(t *testing.T, modelErrors map[string]string, cheap []string, chain []string) *fallbackRig {
	t.Helper()
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	a := openScripted(t, map[string]agents.Adapter{"claude": capt}, fakeagent.Script{
		Rules:       []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
		Default:     wrongStep("a - b"), // the right Subtract: a turn that runs passes the checks
		ModelErrors: modelErrors,
	})
	p, _ := a.setup("gocalc")
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "sonnet"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "sonnet"}, &r)
	var rm []core.RouteModel
	for _, m := range cheap {
		rm = append(rm, core.RouteModel{Model: m, Tier: "cheap"})
	}
	rm = append(rm, core.RouteModel{Model: "opus", Tier: "premium"})
	a.call("agent.update", map[string]any{"id": w.ID, "routeModels": rm}, &w)
	if chain != nil {
		var saved router.Rule
		a.call("route.rules.set", map[string]any{"rule": router.Rule{Scope: "workspace", Priority: 10, When: "candidate.tier == 'cheap'",
			Then: router.Action{Fallback: chain}, Enabled: true}}, &saved)
		if strings.Join(saved.Then.Fallback, ",") != strings.Join(chain, ",") {
			t.Fatalf("stored rule %+v", saved)
		}
	}
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "make Subtract return a - b"}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	return &fallbackRig{a: a, capt: capt, p: p, worker: w, review: r, task: task}
}

func (g *fallbackRig) start() string {
	var runID string
	g.a.call("task.start", map[string]string{"id": g.task.ID}, &runID)
	return runID
}

// workModels are the models of the worker's sessions, in the order they started.
func (g *fallbackRig) workModels() []string {
	var out []string
	for _, s := range g.capt.all() {
		if s.Spec.AgentID == g.worker.ID {
			out = append(out, s.Spec.Model)
		}
	}
	return out
}

// keepGoing answers the watchdog's questions (identical failing turns make it ask) so the run goes on.
func (g *fallbackRig) keepGoing(runID string) {
	var d core.TaskDetail
	g.a.call("task.get", map[string]string{"id": g.task.ID}, &d)
	for _, p := range d.Pending {
		if p.Spec.Kind == "question" {
			_, _ = g.a.c.Call(g.a.t.Context(), "run.respond", mustJSON(map[string]any{"runId": runID, "requestId": string(p.ID), "decision": "answered", "data": map[string]string{"text": "keep going"}}))
		}
	}
}

func (g *fallbackRig) blockedReasons() []string {
	var out []string
	for _, ev := range g.a.eventsOf("routing.blocked") {
		if r, _ := ev["reason"].(string); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// A turn whose model is overloaded before it did any work runs again, in the same node, on the next model of the chain a
// routing rule names; the hop is a recorded decision with a routing.fallback event and a "Why this model?" that says why,
// and the work then goes on to its review.
func TestAnUnavailableModelFallsBackToTheNextModelOfTheChain(t *testing.T) {
	g := newFallbackRig(t, map[string]string{"haiku": overloaded}, []string{"haiku", "sonnet"}, []string{"sonnet"})
	g.start()
	g.a.waitFor("the review of the work", func() bool {
		for _, s := range g.capt.all() {
			if s.Spec.AgentID == g.review.ID {
				return true
			}
		}
		return false
	})
	if got := strings.Join(g.workModels(), ","); got != "haiku,sonnet" {
		t.Fatalf("work sessions %s, want the overloaded haiku turn then its fallback on sonnet", got)
	}
	evs := g.a.eventsOf("routing.fallback")
	if len(evs) != 1 || evs[0]["cause"] != "overloaded" || evs[0]["model"] != "sonnet" || evs[0]["hop"] != float64(1) || evs[0]["_task"] != g.task.ID {
		t.Fatalf("routing.fallback events %v", evs)
	}
	var id, raw string
	if err := g.a.c.DB.Reader().QueryRow(`SELECT id, decision FROM routing_decisions WHERE task_id=? AND model='sonnet' AND run_kind='agent_task'`, g.task.ID).Scan(&id, &raw); err != nil {
		t.Fatal(err)
	}
	var d router.RoutingDecision
	if err := json.Unmarshal([]byte(raw), &d); err != nil || d.FallbackHop != 1 || d.Facts.FallbackFrom == nil || d.Facts.FallbackFrom.Model != "haiku" {
		t.Fatalf("hop decision %v %+v", err, d)
	}
	var why struct {
		Text string `json:"text"`
	}
	g.a.call("route.explain", map[string]string{"decisionId": id}, &why)
	if !strings.Contains(why.Text, "Fallback: agent:claude/haiku was unavailable (overloaded), so the turn moved to this model before any work was done: hop 1 of at most 2") {
		t.Fatalf("Why this model?\n%s", why.Text)
	}
}

// An error that is the work's own (here: a prompt too long for the model) never falls back: the next turn is an ordinary
// new turn on the routed model, and nothing moved to the chain.
func TestAWorkErrorDoesNotFallBack(t *testing.T) {
	g := newFallbackRig(t, map[string]string{"haiku": "Prompt is too long"}, []string{"haiku", "sonnet"}, []string{"sonnet"})
	runID := g.start()
	g.a.waitFor("two work turns", func() bool {
		g.keepGoing(runID)
		return len(g.workModels()) >= 2
	})
	if got := g.workModels()[:2]; got[0] != "haiku" || got[1] != "haiku" {
		t.Fatalf("work sessions %v: a work error moved to the chain", got)
	}
	if evs := g.a.eventsOf("routing.fallback"); len(evs) != 0 {
		t.Fatalf("routing.fallback after a work error: %v", evs)
	}
}

// At most two hops per turn, even with a model left in the chain; the end of the chain is published with its reason, the
// turn's failure goes on as any failed turn does, and the turn counts once toward escalation (the next turn is cheap again,
// not premium as three cheap decisions would have made it).
func TestTheHopLimitHoldsAndATurnCountsOnceTowardEscalation(t *testing.T) {
	down := map[string]string{"haiku": overloaded, "sonnet": overloaded, "tango": overloaded}
	g := newFallbackRig(t, down, []string{"haiku", "sonnet", "tango", "zulu"}, []string{"sonnet", "tango", "zulu"})
	runID := g.start()
	g.a.waitFor("the next work turn", func() bool {
		g.keepGoing(runID)
		return len(g.workModels()) >= 4
	})
	got := g.workModels()
	if strings.Join(got[:4], ",") != "haiku,sonnet,tango,haiku" {
		t.Fatalf("work sessions %v, want haiku, its two hops, then the next turn on haiku again", got)
	}
	for _, m := range got {
		if m == "zulu" {
			t.Fatalf("a third hop ran: %v", got)
		}
	}
	found := false
	for _, r := range g.blockedReasons() {
		found = found || strings.Contains(r, "fallback chain ended: tango was unavailable (overloaded) after 2 fallback hops")
	}
	if !found {
		t.Fatalf("routing.blocked reasons %v", g.blockedReasons())
	}
}

// A budget that is used up stops the chain: the human approved one turn over the project's hard limit, the turn's model was
// overloaded, and no fallback hop spends more.
func TestAUsedUpBudgetStopsTheFallbackChain(t *testing.T) {
	g := newFallbackRig(t, map[string]string{"haiku": overloaded}, []string{"haiku", "sonnet"}, []string{"sonnet"})
	if _, err := g.a.c.DB.Writer().Exec(`INSERT INTO model_usage(ts,task_id,agent_id,provider,model,tier,cost_usd,source,cost_source) VALUES(1, '', ?, 'anthropic','claude-haiku-5-5','premium',0.50,'reported','reported')`, g.worker.ID); err != nil {
		t.Fatal(err)
	}
	g.a.call("budget.set", map[string]any{"scope": "project", "scopeId": g.p.ID, "limitUsd": 0.10}, nil)
	runID := g.start()
	g.a.waitFor("the budget pause", func() bool {
		var d core.TaskDetail
		g.a.call("task.get", map[string]string{"id": g.task.ID}, &d)
		for _, p := range d.Pending {
			if strings.Contains(string(p.Spec.Payload), `"gate":"budget"`) {
				return true
			}
		}
		return false
	})
	g.a.call("budget.resume", map[string]any{"runId": runID}, nil)
	g.a.waitFor("the end of the chain", func() bool {
		for _, r := range g.blockedReasons() {
			if strings.Contains(r, "fallback chain ended: the project budget is used up") {
				return true
			}
		}
		return false
	})
	if got := g.workModels(); len(got) != 1 || got[0] != "haiku" {
		t.Fatalf("work sessions %v, want only the approved turn", got)
	}
	if evs := g.a.eventsOf("routing.fallback"); len(evs) != 0 {
		t.Fatalf("a fallback over a used-up budget: %v", evs)
	}
}

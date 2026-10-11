package core_test

import (
	"os"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

func wrongStep(expr string) []fakeagent.Step {
	impl := "package gocalc\n\nfunc Add(a, b int) int { return a + b }\n\n// Subtract returns a minus b.\nfunc Subtract(a, b int) int { return " + expr + " }\n"
	return []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "tried " + expr}}
}

// An agent whose human listed the models the router may choose runs each turn on the router's choice, among exactly those
// models: cheap first, and premium once three cheap work turns came back with failing checks. Every turn's decision is
// recorded by the rule router with its tier.
func TestTheRouterChoosesTheModelOfAgentTurnsAndEscalates(t *testing.T) {
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	a := openScripted(t, map[string]agents.Adapter{"claude": capt}, fakeagent.Script{
		// each turn writes a different wrong Subtract, chosen from the failure the last one produced, so the diff changes
		// every time (as a real agent's would) and the watchdog's error-echo stop does not end the loop first
		Rules: []fakeagent.Rule{
			{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}},
			{Match: "Subtract(5,3) = 8, want", Steps: wrongStep("a * b")},
			{Match: "Subtract(5,3) = 15, want", Steps: wrongStep("a / b")},
			{Match: "Subtract(5,3) = 1, want", Steps: wrongStep("a % b")},
			{Match: "Subtract(3,5) = 3, want", Steps: wrongStep("b - a")},
		},
		Default: wrongStep("a + b"),
	})
	p, _ := a.setup("gocalc")
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "sonnet"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "sonnet"}, &r)

	// validation: both tiers are needed, tiers are known, names are single words
	for _, bad := range [][]core.RouteModel{
		{{Model: "haiku", Tier: "cheap"}},
		{{Model: "haiku", Tier: "cheap"}, {Model: "opus", Tier: "gold"}},
		{{Model: "haiku", Tier: "cheap"}, {Model: "--dangerous", Tier: "premium"}},
		{{Model: "haiku", Tier: "cheap"}, {Model: "haiku", Tier: "premium"}},
	} {
		if _, err := a.c.Call(t.Context(), "agent.update", mustJSON(map[string]any{"id": w.ID, "routeModels": bad})); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
	a.call("agent.update", map[string]any{"id": w.ID, "routeModels": []core.RouteModel{{Model: "haiku", Tier: "cheap"}, {Model: "opus", Tier: "premium"}}}, &w)
	if len(w.RouteModels) != 2 {
		t.Fatalf("stored %+v", w.RouteModels)
	}
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "make Subtract return a - b"}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	workModels := func() []string {
		var out []string
		for _, s := range capt.all() {
			if s.Spec.AgentID == w.ID {
				out = append(out, s.Spec.Model)
			}
		}
		return out
	}
	defer func() {
		if t.Failed() {
			t.Logf("models so far %v", workModels())
			var d core.TaskDetail
			a.call("task.get", map[string]string{"id": task.ID}, &d)
			t.Logf("task %s run %+v pending %+v", d.Task.Status, d.Run.Status, d.Pending)
		}
	}()
	// identical failures make the watchdog ask the human (stage 2, error echo); the human lets it go on each time
	a.waitFor("five work turns", func() bool {
		if len(workModels()) >= 5 {
			return true
		}
		var d core.TaskDetail
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		for _, p := range d.Pending {
			if p.Spec.Kind == "question" {
				_, rerr := a.c.Call(t.Context(), "run.respond", mustJSON(map[string]any{"runId": runID, "requestId": string(p.ID), "decision": "answered", "data": map[string]string{"text": "keep going"}}))
				if rerr != nil {
					t.Logf("respond: %v", rerr)
				}
			}
		}
		return false
	})
	got := workModels()[:5]
	if strings.Join(got, ",") != "haiku,haiku,haiku,opus,opus" {
		t.Fatalf("work turn models %v, want three cheap turns then premium", got)
	}

	var x core.RunExplanation
	a.call("run.explain", map[string]string{"taskId": task.ID}, &x)
	var decided []string
	for _, e := range x.Executions {
		if e.NodeID == "work" && e.Decision != nil {
			decided = append(decided, e.Decision.DecidedBy+":"+e.Decision.Model)
		}
	}
	if len(decided) < 5 || decided[0] != "rules:haiku" || decided[3] != "rules:opus" {
		t.Fatalf("recorded decisions %v", decided)
	}

	// The escalation hands the work over: the first premium turn is a fresh session (no --resume) seeded with a handoff artifact
	// the router asked for, written to handoffs with the reason escalation; the cheap turns before it continued their session.
	var work []agents.RunSpec
	for _, s := range capt.all() {
		if s.Spec.AgentID == w.ID {
			work = append(work, s.Spec)
		}
	}
	if work[3].Model == work[2].Model || work[3].ResumeID != "" {
		t.Fatalf("the escalated turn must start a fresh session: %+v", work[3])
	}
	continued := 0
	for _, s := range work[1:3] {
		if s.ResumeID != "" {
			continued++
		}
	}
	if continued == 0 {
		t.Errorf("no cheap turn continued its session: %+v", work[:3])
	}
	var hid, artifact string
	if err := a.c.DB.Reader().QueryRow(`SELECT id, artifact FROM handoffs WHERE task_id=? AND reason='escalation'`, task.ID).Scan(&hid, &artifact); err != nil {
		t.Fatalf("no escalation handoff: %v", err)
	}
	if !strings.Contains(artifact, `"fromModel":"`+work[2].Model+`"`) || !strings.Contains(artifact, `"toModel":"`+work[3].Model+`"`) || !strings.Contains(artifact, `"title":"Implement Subtract"`) {
		t.Fatalf("escalation artifact %s", artifact)
	}
	var escalated bool
	for _, ev := range a.eventsOf("agent.session") {
		if ev["trigger"] == "escalation" && ev["handoff"] == hid {
			escalated = true
		}
	}
	if !escalated {
		t.Fatalf("no agent.session event for the escalation: %v", a.eventsOf("agent.session"))
	}
}

// Routing is off by default: an agent keeps its configured model, and the turn's static decision says why.
func TestAgentsWithoutRouteModelsKeepTheirModel(t *testing.T) {
	_, _, a, wid := runOneTask(t, false)
	var list []core.Agent
	a.call("agent.list", map[string]string{"projectId": firstProject(t, a)}, &list)
	for _, ag := range list {
		if len(ag.RouteModels) != 0 {
			t.Fatalf("routing is on by default: %+v", ag)
		}
	}
	var reason string
	if err := a.c.DB.Reader().QueryRow(`SELECT decided_by || ':' || json_extract(decision, '$.Reason') FROM routing_decisions WHERE agent LIKE '%/' || ? ORDER BY ts LIMIT 1`, wid).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(reason, "static:") || !strings.Contains(reason, "routing is off") {
		t.Fatalf("decision %q", reason)
	}
}

func firstProject(t *testing.T, a *app) string {
	var ps []core.Project
	a.call("project.list", nil, &ps)
	if len(ps) == 0 {
		t.Fatal("no project")
	}
	return ps[0].ID
}

// A human who approves one more turn over a budget at its hard limit gets that turn routed as usual (the cheap model), not
// run on the agent's configured, premium model because the router saw the limit and gave up.
func TestTheTurnAHumanApprovesOverABudgetIsStillRouted(t *testing.T) {
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	a := openScripted(t, map[string]agents.Adapter{"claude": capt}, fakeagent.Script{
		Rules:   []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
		Default: wrongStep("a - b"),
	})
	p, _ := a.setup("gocalc")
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "sonnet"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "sonnet"}, &r)
	a.call("agent.update", map[string]any{"id": w.ID, "routeModels": []core.RouteModel{{Model: "haiku", Tier: "cheap"}, {Model: "opus", Tier: "premium"}}}, &w)
	// the project has already spent more than its budget
	if _, err := a.c.DB.Writer().Exec(`INSERT INTO model_usage(ts,task_id,agent_id,provider,model,tier,cost_usd,source,cost_source) VALUES(1, '', ?, 'anthropic','claude-haiku-5-5','premium',0.50,'reported','reported')`, w.ID); err != nil {
		t.Fatal(err)
	}
	a.call("budget.set", map[string]any{"scope": "project", "scopeId": p.ID, "limitUsd": 0.10}, nil)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "make Subtract return a - b"}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	a.waitFor("the budget pause", func() bool {
		var d core.TaskDetail
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		for _, p := range d.Pending {
			if strings.Contains(string(p.Spec.Payload), `"gate":"budget"`) {
				return true
			}
		}
		return false
	})
	for _, s := range capt.all() {
		if s.Spec.AgentID == w.ID {
			t.Fatalf("a work turn started over the budget: %+v", s.Spec)
		}
	}
	// one more turn, without raising the limit
	a.call("budget.resume", map[string]any{"runId": runID}, nil)
	a.waitFor("the approved work turn", func() bool {
		for _, s := range capt.all() {
			if s.Spec.AgentID == w.ID {
				return true
			}
		}
		return false
	})
	for _, s := range capt.all() {
		if s.Spec.AgentID == w.ID {
			if s.Spec.Model != "haiku" {
				t.Fatalf("the approved turn ran on %q, want the routed cheap model", s.Spec.Model)
			}
			break
		}
	}
}

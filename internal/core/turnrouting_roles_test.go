package core_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/agents/responder"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/router"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Every turn of an agent is routed (decision P63): its room replies and the template role turns (the pm's spec, the tests
// written first, the architect's design, the security review) go through the same router, on the agent's own list of
// models, with the same decision records, as its work and review turns. The agent stand-in is the labelled fake agent.

var cheapAndPremium = []core.RouteModel{{Model: "haiku", Tier: "cheap"}, {Model: "opus", Tier: "premium"}}

// decisionRow is one stored routing decision, as the tests read it.
type decisionRow struct {
	ID, RunID, NodeID, Kind, DecidedBy, Model, TaskType string
}

func decisionsWhere(t *testing.T, a *app, where string, args ...any) []decisionRow {
	t.Helper()
	rows, err := a.c.DB.Reader().Query(`SELECT id, run_id, node_id, run_kind, decided_by, model, COALESCE(json_extract(request,'$.TaskType'),'') FROM routing_decisions WHERE `+where+` ORDER BY ts, rowid`, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []decisionRow
	for rows.Next() {
		var d decisionRow
		if err := rows.Scan(&d.ID, &d.RunID, &d.NodeID, &d.Kind, &d.DecidedBy, &d.Model, &d.TaskType); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

func generalRoom(t *testing.T, a *app, projectID string) string {
	t.Helper()
	var rooms []room.Room
	a.call("room.list", map[string]any{"projectId": projectID}, &rooms)
	for _, r := range rooms {
		if r.Name == "general" {
			return r.ID
		}
	}
	t.Fatal("no general channel")
	return ""
}

// replyOf waits for the agent's reply in the room and returns it.
func replyOf(t *testing.T, a *app, roomID, agentID string) room.Message {
	t.Helper()
	var reply room.Message
	a.waitFor("the reply of "+agentID, func() bool {
		var ms []room.Message
		a.call("room.messages", map[string]any{"roomId": roomID, "limit": 200}, &ms)
		for _, m := range ms {
			if m.Author.Key() == agentID && m.Type == room.TypeChat {
				reply = m
				return true
			}
		}
		return false
	})
	return reply
}

func sessionsOf(capt *captureAdapter, agentID string) []string {
	var out []string
	for _, s := range capt.all() {
		if s.Spec.AgentID == agentID {
			out = append(out, s.Spec.Model)
		}
	}
	return out
}

// A room reply of an agent whose human listed its models runs on the router's choice among them (a reply is light: the
// cheap one), with a rule decision of kind room_reply that the reply, the room's turn list and "Why this model?" point to.
// An agent without a list answers on its configured model, recorded as a static decision.
func TestRoomRepliesAreRoutedAmongTheAgentsOwnModels(t *testing.T) {
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	a := openScripted(t, map[string]agents.Adapter{"claude": capt}, fakeagent.Script{Default: []fakeagent.Step{{Say: "here is my answer"}}})
	p, _ := a.setup("gocalc")
	var alice, bob core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Alice", Adapter: "claude", Role: "backend", Model: "sonnet"}, &alice)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Bob", Adapter: "claude", Role: "frontend", Model: "sonnet"}, &bob)
	a.call("agent.update", map[string]any{"id": alice.ID, "routeModels": cheapAndPremium}, &alice)
	general := generalRoom(t, a, p.ID)

	a.call("room.post", map[string]any{"roomId": general, "body": "@" + alice.ID + " what does Subtract do?"}, nil)
	reply := replyOf(t, a, general, alice.ID)
	if got := sessionsOf(capt, alice.ID); len(got) != 1 || got[0] != "haiku" {
		t.Fatalf("Alice's reply sessions %v, want one on the router's cheap choice haiku", got)
	}
	var payload struct {
		Model      string `json:"model"`
		DecisionID string `json:"decision_id"`
		TurnID     string `json:"turn_id"`
	}
	_ = json.Unmarshal(reply.Payload, &payload)
	ds := decisionsWhere(t, a, `agent LIKE '%/' || ?`, alice.ID)
	if len(ds) != 1 || ds[0].Kind != string(router.RunRoomReply) || ds[0].DecidedBy != "rules" || ds[0].Model != "haiku" || ds[0].TaskType != "room_reply" ||
		ds[0].RunID != payload.TurnID || ds[0].NodeID != "reply" {
		t.Fatalf("Alice's decisions %+v (reply payload %s)", ds, reply.Payload)
	}
	if payload.Model != "haiku" || payload.DecisionID != ds[0].ID {
		t.Fatalf("the reply says model %q decision %q, want haiku %s", payload.Model, payload.DecisionID, ds[0].ID)
	}
	var turns []responder.Turn
	a.call("room.turns", map[string]any{"roomId": general}, &turns)
	if len(turns) != 1 || turns[0].Model != "haiku" || turns[0].DecisionID != ds[0].ID {
		t.Fatalf("room turns %+v", turns)
	}
	var why struct {
		Text string `json:"text"`
	}
	a.call("route.explain", map[string]string{"decisionId": ds[0].ID}, &why)
	if !strings.Contains(why.Text, "- Turn: room reply; a reply in a channel or a DM is usually light") || !strings.Contains(why.Text, "haiku") {
		t.Fatalf("Why this model?\n%s", why.Text)
	}

	// Bob has no list: his configured model, and the static decision says routing is off for him
	a.call("room.post", map[string]any{"roomId": general, "body": "@" + bob.ID + " and you?"}, nil)
	replyOf(t, a, general, bob.ID)
	if got := sessionsOf(capt, bob.ID); len(got) != 1 || got[0] != "sonnet" {
		t.Fatalf("Bob's reply sessions %v, want his configured sonnet", got)
	}
	bs := decisionsWhere(t, a, `agent LIKE '%/' || ?`, bob.ID)
	if len(bs) != 1 || bs[0].Kind != string(router.RunRoomReply) || bs[0].DecidedBy != "static" || bs[0].Model != "sonnet" {
		t.Fatalf("Bob's decisions %+v", bs)
	}
	a.call("route.explain", map[string]string{"decisionId": bs[0].ID}, &why)
	if !strings.Contains(why.Text, "routing is off for this agent") {
		t.Fatalf("Bob's Why this model?\n%s", why.Text)
	}
}

// A reply whose model is overloaded before it did any work runs again on the next model of the chain a routing rule names:
// the hop is its own decision with a routing.fallback event, and the reply that is posted is the one from the fallback.
func TestARoomReplyFallsBackToTheNextModelOfTheChain(t *testing.T) {
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	a := openScripted(t, map[string]agents.Adapter{"claude": capt}, fakeagent.Script{Default: []fakeagent.Step{{Say: "answer from the fallback"}},
		ModelErrors: map[string]string{"haiku": overloaded}})
	p, _ := a.setup("gocalc")
	var alice core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Alice", Adapter: "claude", Role: "backend", Model: "opus"}, &alice)
	a.call("agent.update", map[string]any{"id": alice.ID, "routeModels": []core.RouteModel{{Model: "haiku", Tier: "cheap"}, {Model: "sonnet", Tier: "cheap"}, {Model: "opus", Tier: "premium"}}}, &alice)
	a.call("route.rules.set", map[string]any{"rule": router.Rule{Scope: "workspace", Priority: 10, When: "candidate.tier == 'cheap'", Then: router.Action{Fallback: []string{"sonnet"}}, Enabled: true}}, nil)
	general := generalRoom(t, a, p.ID)

	a.call("room.post", map[string]any{"roomId": general, "body": "@" + alice.ID + " are you there?"}, nil)
	reply := replyOf(t, a, general, alice.ID)
	if got := strings.Join(sessionsOf(capt, alice.ID), ","); got != "haiku,sonnet" {
		t.Fatalf("reply sessions %s, want the overloaded haiku then its fallback on sonnet", got)
	}
	if !strings.Contains(reply.Body, "answer from the fallback") || !strings.Contains(string(reply.Payload), `"model":"sonnet"`) {
		t.Fatalf("reply %q %s", reply.Body, reply.Payload)
	}
	ds := decisionsWhere(t, a, `run_kind=?`, string(router.RunRoomReply))
	if len(ds) != 2 || ds[0].Model != "haiku" || ds[1].Model != "sonnet" || ds[0].RunID != ds[1].RunID {
		t.Fatalf("decisions %+v", ds)
	}
	evs := a.eventsOf("routing.fallback")
	if len(evs) != 1 || evs[0]["cause"] != "overloaded" || evs[0]["model"] != "sonnet" || evs[0]["hop"] != float64(1) {
		t.Fatalf("routing.fallback events %v", evs)
	}
	// the turn ends just after its reply is posted (it records the reply's id): wait for it, as the reply was waited for
	var turns []responder.Turn
	a.waitFor("the turn to end", func() bool {
		turns = nil
		a.call("room.turns", map[string]any{"roomId": general}, &turns)
		return len(turns) == 1 && turns[0].Status != responder.StatusRunning
	})
	if len(turns) != 1 || turns[0].Model != "sonnet" || turns[0].DecisionID != ds[1].ID || turns[0].Status != responder.StatusDone {
		t.Fatalf("room turns %+v", turns)
	}
	var why struct {
		Text string `json:"text"`
	}
	a.call("route.explain", map[string]string{"decisionId": ds[1].ID}, &why)
	if !strings.Contains(why.Text, "Fallback: agent:claude/haiku was unavailable (overloaded)") {
		t.Fatalf("Why this model?\n%s", why.Text)
	}
}

// A budget at its hard limit stops a routed agent's reply as it stops its work turns: the agent does not run, nothing is
// routed, and the room says which budget.
func TestABudgetAtItsLimitStopsARoutedReply(t *testing.T) {
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	a := openScripted(t, map[string]agents.Adapter{"claude": capt}, fakeagent.Script{Default: []fakeagent.Step{{Say: "over budget"}}})
	p, _ := a.setup("gocalc")
	var alice core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Alice", Adapter: "claude", Role: "backend", Model: "sonnet"}, &alice)
	a.call("agent.update", map[string]any{"id": alice.ID, "routeModels": cheapAndPremium}, &alice)
	if _, err := a.c.DB.Writer().Exec(`INSERT INTO model_usage(ts,task_id,agent_id,provider,model,tier,cost_usd,source,cost_source) VALUES(?, '', ?, 'anthropic','claude-haiku-5-5','premium',0.50,'reported','reported')`, time.Now().UnixMilli(), alice.ID); err != nil {
		t.Fatal(err)
	}
	a.call("budget.set", map[string]any{"scope": "project", "scopeId": p.ID, "limitUsd": 0.10}, nil)
	general := generalRoom(t, a, p.ID)
	a.call("room.post", map[string]any{"roomId": general, "body": "@" + alice.ID + " one more?"}, nil)
	a.waitFor("the refusal", func() bool {
		var ms []room.Message
		a.call("room.messages", map[string]any{"roomId": general, "limit": 200}, &ms)
		for _, m := range ms {
			if m.Type == room.TypeError && strings.Contains(m.Body, "Alice could not answer: budget:") {
				return true
			}
		}
		return false
	})
	if got := sessionsOf(capt, alice.ID); len(got) != 0 {
		t.Fatalf("Alice ran over the budget: %v", got)
	}
	if ds := decisionsWhere(t, a, `run_kind=?`, string(router.RunRoomReply)); len(ds) != 0 {
		t.Fatalf("a reply over the budget was routed: %+v", ds)
	}
}

// The routing middleware of agent_task nodes routes every template role turn on the role agent's own models (not the
// worker's, as it did before P63), with the kind and class of the turn: the pm's spec and the architect's design are heavy
// (premium), the tests written first start cheap, the security review is premium; a role agent without a list stays static.
// The counters are per kind: three cheap tests turns escalate the next tests turn, and do not count as work iterations.
func TestTemplateRoleTurnsAreRoutedOnTheRoleAgentsModels(t *testing.T) {
	a := openScripted(t, nil, fakeagent.Script{Default: []fakeagent.Step{{Say: "ok"}}})
	p, _ := a.setup("gocalc")
	mk := func(name, role string, routed bool) core.Agent {
		var ag core.Agent
		a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: name, Adapter: "claude", Role: role, Model: "sonnet"}, &ag)
		if routed {
			a.call("agent.update", map[string]any{"id": ag.ID, "routeModels": cheapAndPremium}, &ag)
		}
		return ag
	}
	dev, rev := mk("Dana", "backend", true), mk("Rex", "reviewer", false)
	routed := map[string]core.Agent{pipeline.RolePM: mk("Priya", "pm", true), pipeline.RoleTests: mk("Quinn", "qa", true),
		pipeline.RoleArchitect: mk("Ada", "architect", true), pipeline.RoleSecurity: mk("Sam", "security", true)}
	static := map[string]core.Agent{pipeline.RolePM: mk("Pat", "pm", false), pipeline.RoleTests: mk("Tess", "qa", false),
		pipeline.RoleArchitect: mk("Arno", "architect", false), pipeline.RoleSecurity: mk("Sid", "security", false)}
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Add Multiply", Description: "Add a Multiply function."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": dev.ID, "reviewer": rev.ID}, &task)

	step := 0
	route := func(runID, role string, roles map[string]string) (pipeline.RoutedModel, bool) {
		t.Helper()
		step++
		in, _ := json.Marshal(pipeline.TemplateInput{TaskID: task.ID, Roles: roles})
		got, ok, err := a.c.TestRouteNode(context.Background(), workflow.NodeInput{RunID: workflow.RunID(runID), NodeID: workflow.NodeID(fmt.Sprintf("%s-%d", role, step)), TaskID: task.ID,
			ProjectID: p.ID, Workflow: pipeline.WorkflowSaaS, Attempt: 1, Input: in, Meta: map[string]string{"agent": role}})
		if err != nil {
			t.Fatal(err)
		}
		return got, ok
	}
	want := map[string]struct {
		kind  router.RunKind
		class string
		model string
	}{
		pipeline.RolePM:        {router.RunSpec, "spec", "opus"},
		pipeline.RoleTests:     {router.RunTests, "tests", "haiku"},
		pipeline.RoleArchitect: {router.RunDesign, "architecture", "opus"},
		pipeline.RoleSecurity:  {router.RunSecurityReview, "security_review", "opus"},
	}
	for role, w := range want {
		runID := "run_routed_" + role
		got, ok := route(runID, role, map[string]string{role: routed[role].ID})
		ds := decisionsWhere(t, a, `run_id=?`, runID)
		if !ok || got.Model != w.model || len(ds) != 1 || ds[0].Kind != string(w.kind) || ds[0].DecidedBy != "rules" || ds[0].Model != w.model || ds[0].TaskType != w.class ||
			got.DecisionID != ds[0].ID || !strings.HasSuffix(decisionAgent(t, a, ds[0].ID), "/"+routed[role].ID) {
			t.Errorf("%s: routed %+v (%v), decisions %+v", role, got, ok, ds)
		}
		runID = "run_static_" + role
		if got, ok := route(runID, role, map[string]string{role: static[role].ID}); ok {
			t.Errorf("%s without a list was routed: %+v", role, got)
		}
		if ds := decisionsWhere(t, a, `run_id=?`, runID); len(ds) != 1 || ds[0].Kind != string(w.kind) || ds[0].DecidedBy != "static" || ds[0].Model != "sonnet" {
			t.Errorf("%s without a list: decisions %+v", role, ds)
		}
	}
	// an optional role nobody fills (no architect on the team) is not a turn: nothing is routed or recorded
	if _, ok := route("run_noarch", pipeline.RoleArchitect, map[string]string{}); ok || len(decisionsWhere(t, a, `run_id='run_noarch'`)) != 0 {
		t.Error("a role without an agent was routed")
	}

	// escalation by kind: three cheap tests turns came back, the fourth goes to premium; work in the same run is still cheap
	run := "run_escalate"
	roles := map[string]string{pipeline.RoleTests: routed[pipeline.RoleTests].ID}
	var models []string
	for i := 0; i < 4; i++ {
		got, _ := route(run, pipeline.RoleTests, roles)
		models = append(models, got.Model)
	}
	if strings.Join(models, ",") != "haiku,haiku,haiku,opus" {
		t.Errorf("tests turns %v, want three cheap then premium", models)
	}
	if got, _ := route(run, "work", roles); got.Model != "haiku" {
		t.Errorf("the work turn after three tests turns went to %q: tests turns counted as work iterations", got.Model)
	}
}

func decisionAgent(t *testing.T, a *app, id string) string {
	t.Helper()
	var ag string
	if err := a.c.DB.Reader().QueryRow(`SELECT agent FROM routing_decisions WHERE id=?`, id).Scan(&ag); err != nil {
		t.Fatal(err)
	}
	return ag
}

// The Feature template end to end through the UI methods: the pm and the test writer, whose humans listed their models, run
// on the router's choices (the spec on premium, the tests on cheap), and every turn of the run is in its explanation with its
// kind; the worker and the reviewer without lists stay static.
func TestTheFeatureTemplateRoutesThePMAndTheTestWriter(t *testing.T) {
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	a := openScripted(t, map[string]agents.Adapter{"claude": capt}, devScript())
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newGreenRepo(t)}, &p)
	pm, qa := a.agent(p.ID, "Priya", "pm"), a.agent(p.ID, "Quinn", "qa")
	dev, rev := a.agent(p.ID, "Dana", "backend"), a.agent(p.ID, "Rex", "reviewer")
	a.call("agent.update", map[string]any{"id": pm.ID, "routeModels": cheapAndPremium}, &pm)
	a.call("agent.update", map[string]any{"id": qa.ID, "routeModels": cheapAndPremium}, &qa)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Add Multiply", Description: "Add a Multiply function to the gocalc package.", Template: "feature"}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": dev.ID, "reviewer": rev.ID}, &task)
	var started core.StartTemplateResult
	a.call("task.startTemplate", map[string]any{"taskId": task.ID, "template": "feature"}, &started)
	d := a.waitPending(task.ID, "specreview")
	a.call("run.respond", map[string]any{"runId": d.Run.ID, "requestId": d.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitPending(task.ID, "approve")

	for _, c := range []struct {
		who  core.Agent
		want string
	}{{pm, "opus"}, {qa, "haiku"}, {dev, "fake-1"}, {rev, "fake-1"}} {
		if got := sessionsOf(capt, c.who.ID); len(got) != 1 || got[0] != c.want {
			t.Errorf("%s ran on %v, want %s", c.who.Name, got, c.want)
		}
	}
	byNode := map[string]string{}
	for _, x := range decisionsWhere(t, a, `task_id=?`, task.ID) {
		byNode[x.NodeID] = x.Kind + ":" + x.DecidedBy + ":" + x.Model
	}
	for node, w := range map[string]string{"spec": "spec:rules:opus", "tests": "tests:rules:haiku", "work": "agent_task:static:fake-1", "review": "review:static:fake-1"} {
		if byNode[node] != w {
			t.Errorf("node %s decision %q, want %q (all %v)", node, byNode[node], w, byNode)
		}
	}
	var x core.RunExplanation
	a.call("run.explain", map[string]string{"taskId": task.ID}, &x)
	explained := map[string]string{}
	for _, e := range x.Executions {
		if e.Decision != nil {
			explained[string(e.NodeID)] = e.Decision.DecidedBy + ":" + e.Decision.Model
		}
	}
	if explained["spec"] != "rules:opus" || explained["tests"] != "rules:haiku" {
		t.Fatalf("run explanation %v", explained)
	}
}

// A budget at its hard limit pauses a routed role turn as it pauses a work turn: the template run waits at the pm's spec for
// the human, the pm has not run, and nothing was routed.
func TestABudgetAtItsLimitPausesARoutedRoleTurn(t *testing.T) {
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	a := openScripted(t, map[string]agents.Adapter{"claude": capt}, devScript())
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newGreenRepo(t)}, &p)
	pm := a.agent(p.ID, "Priya", "pm")
	dev, rev := a.agent(p.ID, "Dana", "backend"), a.agent(p.ID, "Rex", "reviewer")
	a.call("agent.update", map[string]any{"id": pm.ID, "routeModels": cheapAndPremium}, &pm)
	if _, err := a.c.DB.Writer().Exec(`INSERT INTO model_usage(ts,task_id,agent_id,provider,model,tier,cost_usd,source,cost_source) VALUES(?, '', ?, 'anthropic','claude-haiku-5-5','premium',0.50,'reported','reported')`, time.Now().UnixMilli(), pm.ID); err != nil {
		t.Fatal(err)
	}
	a.call("budget.set", map[string]any{"scope": "project", "scopeId": p.ID, "limitUsd": 0.10}, nil)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Add Multiply", Description: "Add a Multiply function to the gocalc package.", Template: "feature"}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": dev.ID, "reviewer": rev.ID}, &task)
	a.call("task.startTemplate", map[string]any{"taskId": task.ID, "template": "feature"}, nil)
	a.waitFor("the budget pause at the spec", func() bool {
		d := a.detail(task.ID)
		return len(d.Pending) == 1 && string(d.Pending[0].NodeID) == "spec" && strings.Contains(string(d.Pending[0].Spec.Payload), `"gate":"budget"`)
	})
	if got := sessionsOf(capt, pm.ID); len(got) != 0 {
		t.Fatalf("the pm ran over the budget: %v", got)
	}
	if ds := decisionsWhere(t, a, `task_id=? AND run_kind=?`, task.ID, string(router.RunSpec)); len(ds) != 0 {
		t.Fatalf("the paused spec turn was routed: %+v", ds)
	}
}

package core_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/llm/llmtest"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/router"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// Decision P112, end to end through the workflow, the real claude adapter and parser, and the labelled fake agent (a test
// double of Claude Code): the pre-call reservation, the wall-time ceiling, the data-retention refusal of an agent turn, the
// LLM tiebreak against a protocol fixture of the cheap model, and the routed turns of the team lead and of a recommendation.

// guardRig is a project with a routed worker (the models given, each cheap unless it is opus), a reviewer, and a task (not
// started).
type guardRig struct {
	a      *app
	capt   *captureAdapter
	p      core.Project
	worker core.Agent
	review core.Agent
}

func newGuardRig(t *testing.T, sc fakeagent.Script, models ...string) *guardRig {
	t.Helper()
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	if len(sc.Rules) == 0 {
		sc.Rules = []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}}
	}
	if sc.Default == nil {
		sc.Default = wrongStep("a - b") // the right Subtract
	}
	a := openScripted(t, map[string]agents.Adapter{"claude": capt}, sc)
	return guardRigOn(t, a, capt, models...)
}

func guardRigOn(t *testing.T, a *app, capt *captureAdapter, models ...string) *guardRig {
	t.Helper()
	p, _ := a.setup("gocalc")
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "sonnet"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "sonnet"}, &r)
	var rm []core.RouteModel
	for _, m := range models {
		tier := "cheap"
		if m == "opus" {
			tier = "premium"
		}
		rm = append(rm, core.RouteModel{Model: m, Tier: tier})
	}
	if len(rm) > 0 {
		a.call("agent.update", map[string]any{"id": w.ID, "routeModels": rm}, &w)
	}
	return &guardRig{a: a, capt: capt, p: p, worker: w, review: r}
}

func (g *guardRig) task(title string, worker core.Agent) tasks.Task {
	var task tasks.Task
	g.a.call("task.create", tasks.CreateReq{ProjectID: g.p.ID, Title: title, Description: "make Subtract return a - b"}, &task)
	g.a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": g.review.ID}, &task)
	return task
}

func (g *guardRig) models(agentID string) []string {
	var out []string
	for _, s := range g.capt.all() {
		if s.Spec.AgentID == agentID {
			out = append(out, s.Spec.Model)
		}
	}
	return out
}

func (g *guardRig) pending(taskID, gate string) (core.TaskDetail, bool) {
	var d core.TaskDetail
	g.a.call("task.get", map[string]string{"id": taskID}, &d)
	for _, p := range d.Pending {
		if strings.Contains(string(p.Spec.Payload), `"gate":"`+gate+`"`) {
			return d, true
		}
	}
	return d, false
}

// setting writes a settings row as it is (a test may set what the method refuses, such as a ceiling of seconds).
func (g *guardRig) setting(key string, v any) {
	b, _ := json.Marshal(v)
	if _, err := g.a.c.DB.Writer().Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, string(b)); err != nil {
		g.a.t.Fatal(err)
	}
}

func (g *guardRig) chain(chain ...string) {
	var saved router.Rule
	g.a.call("route.rules.set", map[string]any{"rule": router.Rule{Scope: "workspace", Priority: 10, When: "candidate.tier == 'cheap'", Then: router.Action{Fallback: chain}, Enabled: true}}, &saved)
}

// Two routed turns that start together, with a token budget only one of them fits in: the first holds its estimate, the
// second waits on the budget pause, and the pause says what the first one holds. Without the reservation both started:
// neither had spent anything yet.
func TestTwoRoutedTurnsStartingTogetherCannotBothPassABudgetOnlyOneFitsIn(t *testing.T) {
	slow := append([]fakeagent.Step{{SleepMS: 2500}}, wrongStep("a - b")...)
	g := newGuardRig(t, fakeagent.Script{Default: slow}, "haiku", "opus")
	var w2 core.Agent
	g.a.call("agent.create", core.AgentSpec{ProjectID: g.p.ID, Name: "Backend #2", Adapter: "claude", Role: "backend", Model: "sonnet"}, &w2)
	g.a.call("agent.update", map[string]any{"id": w2.ID, "routeModels": []core.RouteModel{{Model: "haiku", Tier: "cheap"}, {Model: "opus", Tier: "premium"}}}, &w2)
	// a routed agent-native turn is estimated at 2000 output tokens (its price is not known): 3000 fits one of them
	g.a.call("budget.set", map[string]any{"scope": "project", "scopeId": g.p.ID, "limitTokens": 3000}, nil)
	t1, t2 := g.task("Implement Subtract", g.worker), g.task("Implement Subtract too", w2)
	var wg sync.WaitGroup
	for _, id := range []string{t1.ID, t2.ID} {
		wg.Go(func() {
			var runID string
			g.a.call("task.start", map[string]string{"id": id}, &runID)
		})
	}
	wg.Wait()
	var paused core.TaskDetail
	g.a.waitFor("one turn on the budget pause", func() bool {
		for _, id := range []string{t1.ID, t2.ID} {
			if d, ok := g.pending(id, "budget"); ok {
				paused = d
				return true
			}
		}
		return false
	})
	var payload struct {
		Message string `json:"message"`
	}
	for _, p := range paused.Pending {
		json.Unmarshal(p.Spec.Payload, &payload)
	}
	if !strings.Contains(payload.Message, "held by 1 turn(s) in flight") || !strings.Contains(payload.Message, "estimated at 2000") {
		t.Fatalf("the pause must say what the turn in flight holds: %q", payload.Message)
	}
	if n := len(g.models(g.worker.ID)) + len(g.models(w2.ID)); n != 1 {
		t.Fatalf("%d work turns started; one fits", n)
	}
	var held int
	g.a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM budget_reservations`).Scan(&held)
	if held != 1 || len(g.a.eventsOf("budget.reservation_refused")) != 1 {
		t.Fatalf("reservations %d, refusals %d", held, len(g.a.eventsOf("budget.reservation_refused")))
	}
	// the turn that ran ends: its reservation is settled, never left held
	g.a.waitFor("the settled reservation", func() bool {
		var st string
		g.a.c.DB.Reader().QueryRow(`SELECT state FROM budget_reservations LIMIT 1`).Scan(&st)
		return st == "settled"
	})
}

const hang = 30000 // ms: a model that does not answer within the test's ceiling

// A turn that runs past its level's wall-time ceiling is stopped: the run waits for a person, the decision it ran on says why
// ("Why this model?" and the routing outcome), and routing.stopped is published.
func TestATurnPastItsWallTimeCeilingIsStoppedWithAReason(t *testing.T) {
	g := newGuardRig(t, fakeagent.Script{ModelSteps: map[string][]fakeagent.Step{"haiku": {{Say: "looking"}, {Tool: "Read", Input: json.RawMessage(`{"file_path":"calc.go"}`), Result: "x"}, {SleepMS: hang}}}}, "haiku", "sonnet", "opus")
	g.chain("sonnet") // a chain exists, but a turn that started work is never moved to another model
	g.setting("route.ceilings", map[string]any{"levels": map[string]int{"balanced": 2}})
	task := g.task("Implement Subtract", g.worker)
	start := time.Now()
	g.a.call("task.start", map[string]string{"id": task.ID}, new(string))
	g.a.waitFor("the wall-time gate", func() bool { _, ok := g.pending(task.ID, "wall_time"); return ok })
	if took := time.Since(start); took > 20*time.Second {
		t.Fatalf("the turn ran %s; the ceiling is 2s", took)
	}
	if m := g.models(g.worker.ID); strings.Join(m, ",") != "haiku" {
		t.Fatalf("work turns %v: the stopped turn did work, so no fallback", m)
	}
	var id, why string
	if err := g.a.c.DB.Reader().QueryRow(`SELECT id, stop_reason FROM routing_decisions WHERE task_id=? AND run_kind='agent_task'`, task.ID).Scan(&id, &why); err != nil {
		t.Fatal(err)
	}
	if why != "the turn ran past the 2s wall-time ceiling of the balanced level" {
		t.Fatalf("stop reason %q", why)
	}
	var ex struct {
		Text string `json:"text"`
	}
	g.a.call("route.explain", map[string]string{"decisionId": id}, &ex)
	if !strings.Contains(ex.Text, "- Stopped: the turn ran past the 2s wall-time ceiling") {
		t.Fatalf("Why this model? does not say it was stopped:\n%s", ex.Text)
	}
	evs := g.a.eventsOf("routing.stopped")
	if len(evs) != 1 || evs[0]["decisionId"] != id || evs[0]["ceilingSeconds"] != float64(2) {
		t.Fatalf("events %v", evs)
	}
	// the stopped turn's reservation was settled: it reached the model
	var st string
	g.a.c.DB.Reader().QueryRow(`SELECT state FROM budget_reservations WHERE decision_id=?`, id).Scan(&st)
	if st != "settled" {
		t.Fatalf("reservation %q", st)
	}
}

// A model that hangs without doing anything (no tool call) is stopped at the ceiling, and the chain takes the turn.
func TestATurnThatHangsWithoutWorkFallsBackAtTheCeiling(t *testing.T) {
	g := newGuardRig(t, fakeagent.Script{ModelSteps: map[string][]fakeagent.Step{"haiku": {{SleepMS: hang}}}}, "haiku", "sonnet", "opus")
	g.chain("sonnet")
	g.setting("route.ceilings", map[string]any{"levels": map[string]int{"balanced": 2}})
	task := g.task("Implement Subtract", g.worker)
	g.a.call("task.start", map[string]string{"id": task.ID}, new(string))
	g.a.waitFor("the approval", func() bool {
		var d core.TaskDetail
		g.a.call("task.get", map[string]string{"id": task.ID}, &d)
		return len(d.Pending) == 1 && d.Pending[0].NodeID == "approve"
	})
	if m := g.models(g.worker.ID); len(m) < 2 || m[0] != "haiku" || m[1] != "sonnet" {
		t.Fatalf("work turns %v: haiku hung, sonnet took the turn", m)
	}
	var cause string
	g.a.c.DB.Reader().QueryRow(`SELECT json_extract(decision,'$.Facts.FallbackFrom.Rejected') FROM routing_decisions WHERE task_id=? AND model='sonnet'`, task.ID).Scan(&cause)
	if cause != string(router.CauseWallTime) {
		t.Fatalf("the hop's cause %q", cause)
	}
}

const retentionError = `API Error: 400 {"type":"error","error":{"type":"invalid_request_error","message":"In order to access this model, your organization or workspace must have data retention enabled."}}`

// An agent turn whose model the provider refuses for want of data retention moves to the next model of the chain, and the
// model is set aside: the next decisions leave it out with the reason, until a person clears it.
func TestATurnRefusedForDataRetentionMovesOnAndSetsTheModelAside(t *testing.T) {
	g := newGuardRig(t, fakeagent.Script{ModelErrors: map[string]string{"haiku": retentionError}}, "haiku", "sonnet", "opus")
	g.chain("sonnet")
	task := g.task("Implement Subtract", g.worker)
	g.a.call("task.start", map[string]string{"id": task.ID}, new(string))
	g.a.waitFor("the approval", func() bool {
		var d core.TaskDetail
		g.a.call("task.get", map[string]string{"id": task.ID}, &d)
		return len(d.Pending) == 1 && d.Pending[0].NodeID == "approve"
	})
	if m := g.models(g.worker.ID); len(m) < 2 || m[0] != "haiku" || m[1] != "sonnet" {
		t.Fatalf("work turns %v", m)
	}
	var blocks []providers.RetentionBlock
	g.a.call("route.retention.list", nil, &blocks)
	if len(blocks) != 1 || blocks[0].ProviderID != "agent:claude" || blocks[0].Model != "haiku" || blocks[0].Source != "agent" {
		t.Fatalf("set aside %+v", blocks)
	}
	// the next task's first work turn does not try haiku again, and says why
	t2 := g.task("Implement Subtract again", g.worker)
	g.a.call("task.start", map[string]string{"id": t2.ID}, new(string))
	var raw string
	g.a.waitFor("the second task's decision", func() bool {
		return g.a.c.DB.Reader().QueryRow(`SELECT decision FROM routing_decisions WHERE task_id=? AND run_kind='agent_task' ORDER BY ts LIMIT 1`, t2.ID).Scan(&raw) == nil
	})
	var d router.RoutingDecision
	json.Unmarshal([]byte(raw), &d)
	if d.Model == "haiku" {
		t.Fatal("the set-aside model was chosen again")
	}
	found := false
	for _, alt := range d.Alternatives {
		found = found || (alt.Model == "haiku" && alt.Rejected == router.FilterRetention)
	}
	if !found {
		t.Fatalf("alternatives %+v", d.Alternatives)
	}
	g.a.call("route.retention.clear", map[string]string{"providerId": "agent:claude", "model": "haiku"}, nil)
	g.a.call("route.retention.list", nil, &blocks)
	if len(blocks) != 0 {
		t.Fatalf("cleared %+v", blocks)
	}
	if err := g.a.callErr("route.retention.clear", map[string]string{"providerId": "agent:claude", "model": "haiku"}); err == nil {
		t.Fatal("clearing a model that is not set aside must say so")
	}
}

// The routing settings: off and empty by default, checked when set.
func TestRouteSettingsAreCheckedWhenSet(t *testing.T) {
	g := newGuardRig(t, fakeagent.Script{})
	var s core.RouteSettings
	g.a.call("route.settings.get", nil, &s)
	if s.Tiebreak.Enabled || s.Tiebreak.BudgetUSD != core.DefaultTiebreakBudgetUSD || s.Tiebreak.Model != "" || s.Tiebreak.ModelNote == "" || len(s.Ceilings.Levels) != 0 || s.Level != "balanced" {
		t.Fatalf("defaults %+v", s)
	}
	for name, bad := range map[string]any{
		"budget":        map[string]any{"tiebreak": map[string]any{"budgetUsd": 11}},
		"timeout":       map[string]any{"tiebreak": map[string]any{"timeoutMs": 100}},
		"short ceiling": map[string]any{"ceilings": map[string]any{"levels": map[string]int{"balanced": 30}}},
		"level":         map[string]any{"ceilings": map[string]any{"levels": map[string]int{"turbo": 600}}},
		"agent":         map[string]any{"ceilings": map[string]any{"agents": map[string]int{"agt_nobody": 600}}},
	} {
		if err := g.a.callErr("route.settings.set", bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	g.a.call("route.settings.set", map[string]any{"tiebreak": map[string]any{"enabled": true, "budgetUsd": 0.5},
		"ceilings": map[string]any{"levels": map[string]int{"balanced": 1800, "quality": 0}, "agents": map[string]int{g.worker.ID: 600}}}, &s)
	if !s.Tiebreak.Enabled || s.Tiebreak.BudgetUSD != 0.5 || s.Ceilings.Levels["balanced"] != 1800 || len(s.Ceilings.Levels) != 1 || s.Ceilings.Agents[g.worker.ID] != 600 {
		t.Fatalf("set %+v", s)
	}
	// a part left out stays
	g.a.call("route.settings.set", map[string]any{"tiebreak": map[string]any{"enabled": false}}, &s)
	if s.Tiebreak.Enabled || s.Tiebreak.BudgetUSD != 0.5 || s.Ceilings.Levels["balanced"] != 1800 {
		t.Fatalf("partial set %+v", s)
	}
	// the preview of a level with a ceiling says so
	var pv map[string]any
	g.a.call("route.preview", map[string]any{"request": map[string]any{"RunKind": "agent_task", "Level": "balanced"}}, &pv)
	if pv["blocked"] == false && pv["ceilingSeconds"] != float64(1800) {
		t.Fatalf("preview %v", pv)
	}
}

// The LLM tiebreak against a PROTOCOL FIXTURE of the cheap model (llmtest, the OpenAI wire format): the worker's two cheap
// models are tied (the agent CLI's own, no price); the ranking would take haiku by name, the cheap model chooses sonnet, the
// turn runs on sonnet, and the decision says so. The call is told the turn's shape and the candidates, nothing of the task.
// With its own budget used up, the ranking's choice stands and the decision says why.
func TestTheTiebreakChoosesBetweenTiedModelsThroughTheCheapModel(t *testing.T) {
	dir := t.TempDir()
	script, _ := json.Marshal(fakeagent.Script{Rules: []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
		Default: wrongStep("a - b")})
	sp := filepath.Join(dir, "script.json")
	os.WriteFile(sp, script, 0o644)
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	t.Setenv("DRYDOCK_FAKE_SCRIPT", sp)
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		Credentials: providers.NewMemoryCredentialStoreForTests(), Adapters: map[string]agents.Adapter{"claude": capt}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}

	var mu sync.Mutex
	var asked []string
	srv := llmtest.New(t, func(call llmtest.Call, w http.ResponseWriter, r *http.Request) {
		u := &llmtest.Usage{PromptTokens: 300, CompletionTokens: 20}
		if _, probe := call.Body["tools"]; probe { // the cheap model selection's tool-call probe
			llmtest.Respond(w, call, llmtest.Completion{Model: "cheap-good", ToolCalls: []llmtest.ToolCall{{ID: "c1", Name: "add_numbers", Arguments: `{"a":17,"b":25}`}}, Usage: u})
			return
		}
		b, _ := json.Marshal(call.Body)
		mu.Lock()
		asked = append(asked, string(b))
		mu.Unlock()
		llmtest.Respond(w, call, llmtest.Completion{Model: "cheap-good", Content: `{"choice":"agent:claude/sonnet","why":"the larger of two equal options"}`, Usage: u})
	})
	var prov providers.Provider
	a.call("provider.create", map[string]any{"Name": "Fixture", "Kind": "direct", "BaseURL": srv.URL + "/v1", "APIKey": "sk-fixture-key-1234567890", "DataClass": "private", "Tier": "cheap"}, &prov)
	if err := c.Providers.UpsertModels(context.Background(), prov.ID, []providers.Model{
		{ID: "cheap-good", InputPerM: 0.2, OutputPerM: 0.4, ContextLength: 128000, SupportsTools: true, DataClass: providers.DataPrivate, Tier: providers.TierCheap}}); err != nil {
		t.Fatal(err)
	}
	a.call("route.cheap.select", map[string]any{"providerId": prov.ID}, nil)
	var s core.RouteSettings
	a.call("route.settings.set", map[string]any{"tiebreak": map[string]any{"enabled": true}}, &s)
	if s.Tiebreak.Model != prov.ID+"/cheap-good" {
		t.Fatalf("the tiebreak calls the selected cheap model: %+v", s.Tiebreak)
	}
	g := guardRigOn(t, a, capt, "haiku", "sonnet", "opus")
	task := g.task("Implement Subtract SECRETWORD", g.worker)
	g.a.call("task.start", map[string]string{"id": task.ID}, new(string))
	g.a.waitFor("the first work turn", func() bool { return len(g.models(g.worker.ID)) > 0 })
	if m := g.models(g.worker.ID)[0]; m != "sonnet" {
		t.Fatalf("the work turn ran on %s; the tiebreak chose sonnet", m)
	}
	var raw string
	g.a.c.DB.Reader().QueryRow(`SELECT decision FROM routing_decisions WHERE task_id=? AND run_kind='agent_task' ORDER BY ts LIMIT 1`, task.ID).Scan(&raw)
	var d router.RoutingDecision
	json.Unmarshal([]byte(raw), &d)
	if d.DecidedBy != "llm" || d.Facts.Tiebreak == nil || d.Facts.Tiebreak.Ranked != "agent:claude/haiku" || d.Facts.Tiebreak.By != prov.ID+"/cheap-good" ||
		d.Facts.Tiebreak.Why != "the larger of two equal options" || !strings.Contains(d.Reason, "tiebreak by") {
		t.Fatalf("decision %+v / %+v", d, d.Facts.Tiebreak)
	}
	mu.Lock()
	got := strings.Join(asked, "\n")
	mu.Unlock()
	if len(asked) != 1 || !strings.Contains(got, `agent:claude/haiku`) || !strings.Contains(got, `agent:claude/sonnet`) || strings.Contains(got, "opus") {
		t.Fatalf("the tiebreak was asked %d times: %s", len(asked), got)
	}
	for _, never := range []string{"SECRETWORD", "Subtract", task.ID, g.p.ID, g.worker.ID} {
		if strings.Contains(got, never) {
			t.Fatalf("the tiebreak was told %q: %s", never, got)
		}
	}
	var usage int
	g.a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM model_usage WHERE purpose='route_tiebreak' AND agent_id=?`, g.worker.ID).Scan(&usage)
	if usage != 1 {
		t.Fatalf("usage rows %d: the call counts toward the agent's budgets", usage)
	}
	// its own budget used up: the next tie is decided by the ranking, and the decision says why
	g.a.call("route.settings.set", map[string]any{"tiebreak": map[string]any{"budgetUsd": 0}}, &s)
	t2 := g.task("Implement Subtract again", g.worker)
	g.a.call("task.start", map[string]string{"id": t2.ID}, new(string))
	g.a.waitFor("the second task's decision", func() bool {
		return g.a.c.DB.Reader().QueryRow(`SELECT decision FROM routing_decisions WHERE task_id=? AND run_kind='agent_task' ORDER BY ts LIMIT 1`, t2.ID).Scan(&raw) == nil
	})
	d = router.RoutingDecision{}
	json.Unmarshal([]byte(raw), &d)
	if d.Model != "haiku" || d.DecidedBy != "rules" || d.Facts.Tiebreak == nil || !strings.Contains(d.Facts.Tiebreak.Failed, "budget for today is used up") {
		t.Fatalf("decision %s by %s, tiebreak %+v", d.Model, d.DecidedBy, d.Facts.Tiebreak)
	}
}

// The team lead's turns are routed on the lead's own models (run kind lead); an agent with no list keeps its model (static).
func TestTheLeadsTurnsAreRouted(t *testing.T) {
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	a := openScripted(t, map[string]agents.Adapter{"claude": capt}, leadScript())
	p, _ := a.setup("gocalc")
	var worker, lead core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Lead", Adapter: "claude", Role: "lead", Model: "sonnet"}, &lead)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "x"}, &task)
	a.c.Tasks.Transition(context.Background(), task.ID, tasks.Ready, tasks.TransitionOpts{By: "system"})
	var res pipeline.StandupResult
	a.call("lead.standup", map[string]string{"projectId": p.ID}, &res) // no list: static, on the lead's own model
	a.call("agent.update", map[string]any{"id": lead.ID, "routeModels": []core.RouteModel{{Model: "haiku", Tier: "cheap"}, {Model: "opus", Tier: "premium"}}}, &lead)
	a.call("lead.standup", map[string]string{"projectId": p.ID}, &res)
	var models []string
	for _, s := range capt.all() {
		if s.Spec.AgentID == lead.ID {
			models = append(models, s.Spec.Model)
		}
	}
	if strings.Join(models, ",") != "sonnet,haiku" {
		t.Fatalf("the lead's turns ran on %v", models)
	}
	rows, _ := a.c.DB.Reader().Query(`SELECT decided_by, model FROM routing_decisions WHERE run_kind='lead' ORDER BY ts`)
	var got []string
	for rows.Next() {
		var by, m string
		rows.Scan(&by, &m)
		got = append(got, by+":"+m)
	}
	rows.Close()
	if strings.Join(got, ",") != "static:sonnet,rules:haiku" {
		t.Fatalf("decisions %v", got)
	}
}

// A recommendation turn is routed on the advisor's own models (run kind recommend), its decision carrying the
// recommendation's id.
func TestARecommendationTurnIsRouted(t *testing.T) {
	capt := &captureAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	a := openScripted(t, map[string]agents.Adapter{"claude": capt}, fakeagent.Script{Rules: []fakeagent.Rule{{Match: "Drydock phase: recommend",
		Steps: []fakeagent.Step{{Say: `{"summary": "Nothing to change.", "recommendations": []}`}}}}})
	p, _ := a.setup("gocalc")
	var lead core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Lead", Adapter: "claude", Role: "lead", Model: "sonnet"}, &lead)
	a.call("agent.update", map[string]any{"id": lead.ID, "routeModels": []core.RouteModel{{Model: "haiku", Tier: "cheap"}, {Model: "opus", Tier: "premium"}}}, &lead)
	var started core.Recommendation
	a.call("recommend.start", map[string]any{"projectId": p.ID}, &started)
	if r := a.recommendDone(p.ID, started.ID); r.Status != "done" {
		t.Fatalf("recommendation %+v", r)
	}
	var by, model, runID string
	if err := a.c.DB.Reader().QueryRow(`SELECT decided_by, model, run_id FROM routing_decisions WHERE run_kind='recommend'`).Scan(&by, &model, &runID); err != nil {
		t.Fatal(err)
	}
	if by != "rules" || model != "haiku" || runID != started.ID {
		t.Fatalf("decision %s %s %s", by, model, runID)
	}
	for _, s := range capt.all() {
		if s.Spec.AgentID == lead.ID && s.Spec.Model != "haiku" {
			t.Fatalf("the recommendation ran on %s", s.Spec.Model)
		}
	}
}

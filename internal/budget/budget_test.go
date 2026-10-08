package budget_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/budget"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// These tests run the real budget store, the real SQLite database and migrations, the real event bus and the real workflow
// engine. Handlers of the test graphs write real model_usage rows; nothing is mocked.

type env struct {
	t   *testing.T
	ctx context.Context
	db  *storage.DB
	bus *events.SQLBus
	st  *budget.Store
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 1024)
	t.Cleanup(func() { bus.Close(); db.Close() })
	e := &env{t: t, ctx: ctx, db: db, bus: bus, st: &budget.Store{DB: db}}
	e.exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('prj1','p','/x',1),('prj2','q','/y',1)`)
	e.exec(`INSERT INTO agents(id,project_id,name,adapter,role,created_at) VALUES('agtW','prj1','w','claude','Backend',1),('agtR','prj1','r','claude','Reviewer',1),('agtOther','prj2','o','claude','Backend',1)`)
	return e
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if err := e.db.WriteTx(e.ctx, func(tx *sql.Tx) error { _, err := tx.Exec(q, args...); return err }); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) task(id, project, assignee, reviewer string) {
	e.exec(`INSERT INTO tasks(id,project_id,seq,title,status,assignee,reviewer,created_at,updated_at) VALUES(?,?,1,?,?,?,?,1,1)`, id, project, id, "IN_PROGRESS", assignee, reviewer)
}

type row struct {
	task, agent, run string
	in, out          int
	cost             float64
	source           string // reported | estimated (the tokens/cost source column)
	costSource       string // reported | computed_from_table | unknown
	hit              bool
	ts               int64
}

func (e *env) usage(r row) {
	e.t.Helper()
	if r.source == "" {
		r.source = "reported"
	}
	if r.costSource == "" {
		r.costSource = "unknown"
	}
	if r.ts == 0 {
		r.ts = storage.Now()
	}
	hit := 0
	if r.hit {
		hit = 1
	}
	e.exec(`INSERT INTO model_usage(ts,run_id,task_id,agent_id,provider,model,tier,input_tokens,output_tokens,cost_usd,source,cost_source,cache_hit)
		VALUES(?,?,?,?,'claude','m','premium',?,?,?,?,?,?)`, r.ts, r.run, r.task, r.agent, r.in, r.out, r.cost, r.source, r.costSource, hit)
}

func f(v float64) *float64 { return &v }
func i(v int64) *int64     { return &v }

func (e *env) set(b budget.Budget) budget.Budget {
	e.t.Helper()
	out, err := e.st.Set(e.ctx, b, "human:kyle")
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

func TestStoreIsHumanOnlyValidatedAndCompareAndSwap(t *testing.T) {
	e := newEnv(t)
	if _, err := e.st.Set(e.ctx, budget.Budget{Scope: budget.ScopeTask, ScopeID: "t1", LimitUSD: f(1)}, "agent:worker"); !errors.Is(err, budget.ErrNotHuman) {
		t.Fatalf("an agent identity must not be able to set a budget: %v", err)
	}
	if _, err := e.st.Set(e.ctx, budget.Budget{Scope: budget.ScopeTask, ScopeID: "t1", LimitUSD: f(1)}, "human:"); !errors.Is(err, budget.ErrNotHuman) {
		t.Fatalf("an empty human name is not an identity: %v", err)
	}
	for name, b := range map[string]budget.Budget{
		"no limit":        {Scope: budget.ScopeTask, ScopeID: "t1"},
		"zero limit":      {Scope: budget.ScopeTask, ScopeID: "t1", LimitUSD: f(0)},
		"bad scope":       {Scope: "team", ScopeID: "t1", LimitUSD: f(1)},
		"project any":     {Scope: budget.ScopeProject, ScopeID: budget.Any, LimitUSD: f(1)},
		"warn pct of one": {Scope: budget.ScopeTask, ScopeID: "t1", LimitUSD: f(1), WarnPct: 1},
		"no scope id":     {Scope: budget.ScopeTask, LimitUSD: f(1)},
	} {
		if _, err := e.st.Set(e.ctx, b, "human:kyle"); !errors.Is(err, budget.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	b := e.set(budget.Budget{Scope: budget.ScopeTask, ScopeID: "t1", LimitUSD: f(1), Enabled: true})
	if b.Version != 1 || b.WarnPct != 0.8 || b.UpdatedBy != "human:kyle" {
		t.Fatalf("%+v", b)
	}
	if _, err := e.st.Set(e.ctx, budget.Budget{Scope: budget.ScopeTask, ScopeID: "t1", LimitTokens: i(5), Enabled: true}, "human:kyle"); !errors.Is(err, budget.ErrInvalid) {
		t.Fatalf("a second budget for the same target must be refused: %v", err)
	}
	b.LimitUSD = f(2)
	b2, err := e.st.Set(e.ctx, b, "human:kyle")
	if err != nil || b2.Version != 2 || *b2.LimitUSD != 2 {
		t.Fatalf("%+v %v", b2, err)
	}
	if _, err := e.st.Set(e.ctx, b, "human:kyle"); !errors.Is(err, budget.ErrConflict) {
		t.Fatalf("a stale version must conflict: %v", err)
	}
	if err := e.st.Delete(e.ctx, b.ID, "agent:x"); !errors.Is(err, budget.ErrNotHuman) {
		t.Fatal(err)
	}
	if err := e.st.Delete(e.ctx, b.ID, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Get(e.ctx, b.ID); !errors.Is(err, budget.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestCostIsNeverSilentlyZeroAndSourcesStayApart(t *testing.T) {
	e := newEnv(t)
	e.task("t1", "prj1", "agtW", "agtR")
	e.usage(row{task: "t1", agent: "agtW", in: 100, out: 10, cost: 0.30})                                    // CLI reported a cost (runtime row)
	e.usage(row{task: "t1", agent: "agtW", in: 100, out: 10, cost: 0.10, source: "estimated"})               // CLI estimate
	e.usage(row{task: "t1", agent: "agtW", in: 100, out: 10, cost: 0.05, costSource: "computed_from_table"}) // priced from the table
	e.usage(row{task: "t1", agent: "agtW", in: 100, out: 10, cost: 0.20, costSource: "reported"})            // provider returned the cost
	e.usage(row{task: "t1", agent: "agtW", in: 100, out: 10})                                                // nothing reported: unknown
	e.usage(row{task: "t1", agent: "agtW", in: 0, out: 0, hit: true, costSource: "computed_from_table"})     // cache hit: known zero
	sp, err := budget.Usage(e.ctx, e.db, budget.ScopeTask, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if sp.Rows != 6 || sp.UnknownCostRows != 1 {
		t.Fatalf("exactly one row has an unknown cost: %+v", sp)
	}
	if d := sp.ReportedUSD - 0.50; d > 1e-9 || d < -1e-9 {
		t.Fatalf("reported %v", sp.ReportedUSD)
	}
	if d := sp.EstimatedUSD - 0.15; d > 1e-9 || d < -1e-9 {
		t.Fatalf("estimated %v", sp.EstimatedUSD)
	}
	if !sp.Mixed() || sp.Tokens() != 5*110 {
		t.Fatalf("%+v", sp)
	}
	b := e.set(budget.Budget{Scope: budget.ScopeTask, ScopeID: "t1", LimitUSD: f(10), Enabled: true})
	st, err := budget.Evaluate(e.ctx, e.db, b, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if st.CostComplete || st.CostLabel != "reported + estimated" {
		t.Fatalf("a dollar limit with an unknown-cost row must say its figure is incomplete: %+v", st)
	}
}

func TestThresholdBoundaries(t *testing.T) {
	cases := []struct {
		used  int64
		state budget.State
	}{{799, budget.StateOK}, {800, budget.StateWarning}, {999, budget.StateWarning}, {1000, budget.StateExceeded}, {1500, budget.StateExceeded}}
	for _, c := range cases {
		e := newEnv(t)
		e.task("t1", "prj1", "agtW", "agtR")
		e.usage(row{task: "t1", agent: "agtW", in: int(c.used), cost: 0.01})
		b := e.set(budget.Budget{Scope: budget.ScopeTask, ScopeID: "t1", LimitTokens: i(1000), Enabled: true})
		st, err := budget.Evaluate(e.ctx, e.db, b, "t1")
		if err != nil || st.State != c.state {
			t.Errorf("%d of 1000 tokens: state %s, want %s (%v)", c.used, st.State, c.state, err)
		}
	}
	// the same boundaries in dollars (79.9 / 80 / 99.9 / 100 percent)
	for _, c := range []struct {
		cost  float64
		state budget.State
	}{{0.799, budget.StateOK}, {0.8, budget.StateWarning}, {0.999, budget.StateWarning}, {1.0, budget.StateExceeded}} {
		e := newEnv(t)
		e.task("t1", "prj1", "agtW", "agtR")
		e.usage(row{task: "t1", agent: "agtW", cost: c.cost, costSource: "reported"})
		b := e.set(budget.Budget{Scope: budget.ScopeTask, ScopeID: "t1", LimitUSD: f(1), Enabled: true})
		if st, _ := budget.Evaluate(e.ctx, e.db, b, "t1"); st.State != c.state {
			t.Errorf("$%v of $1: %s want %s", c.cost, st.State, c.state)
		}
	}
}

func TestScopesAreAccountedFromTheRowsTheyOwn(t *testing.T) {
	e := newEnv(t)
	e.task("t1", "prj1", "agtW", "agtR")
	e.task2("t2", "prj2", "agtOther", "agtR2")
	e.usage(row{task: "t1", agent: "agtW", in: 10, cost: 1})
	e.usage(row{task: "t1", agent: "agtR", in: 20, cost: 2})
	e.usage(row{agent: "agtW", in: 40, cost: 4}) // a call with no task: still the project's agent
	e.usage(row{task: "t2", agent: "agtOther", in: 80, cost: 8})
	check := func(scope budget.Scope, id string, wantTokens int64, wantUSD float64) {
		t.Helper()
		sp, err := budget.Usage(e.ctx, e.db, scope, id)
		if err != nil || sp.Tokens() != wantTokens || sp.KnownUSD() != wantUSD {
			t.Errorf("%s %s: %+v want %d tokens $%v (%v)", scope, id, sp, wantTokens, wantUSD, err)
		}
	}
	check(budget.ScopeProject, "prj1", 70, 7)
	check(budget.ScopeProject, "prj2", 80, 8)
	check(budget.ScopeTask, "t1", 30, 3)
	check(budget.ScopeAgent, "agtW", 50, 5)
	check(budget.ScopeAgent, "agtR", 20, 2)
}

func (e *env) task2(id, project, assignee, reviewer string) {
	e.exec(`INSERT INTO agents(id,project_id,name,adapter,role,created_at) VALUES('agtR2',?,'r2','claude','Reviewer',1) ON CONFLICT DO NOTHING`, project)
	e.exec(`INSERT INTO tasks(id,project_id,seq,title,status,assignee,reviewer,created_at,updated_at) VALUES(?,?,1,?,?,?,?,1,1)`, id, project, id, "IN_PROGRESS", assignee, reviewer)
}

func TestRunScopeSplitsATasksUsageAtTheNextRun(t *testing.T) {
	e := newEnv(t)
	e.task("t1", "prj1", "agtW", "agtR")
	e.exec(`INSERT INTO workflow_runs(id,workflow,status,task_id,topology_hash,created_at,updated_at) VALUES('runA','w','COMPLETED','t1','h',1000,1000),('runB','w','RUNNING','t1','h',5000,5000)`)
	e.usage(row{task: "t1", agent: "agtW", in: 1, cost: 1, ts: 1500})              // runtime row, window of run A
	e.usage(row{task: "t1", agent: "agtW", in: 2, cost: 2, ts: 5500})              // window of run B
	e.usage(row{task: "t1", agent: "agtW", in: 4, cost: 4, run: "runA", ts: 6000}) // a wrapper row that names its run wins over the window
	a, _ := budget.Usage(e.ctx, e.db, budget.ScopeRun, "runA")
	b, _ := budget.Usage(e.ctx, e.db, budget.ScopeRun, "runB")
	if a.Tokens() != 5 || b.Tokens() != 2 {
		t.Fatalf("run A %+v, run B %+v", a, b)
	}
}

func TestApplicableResolvesTemplatesAndProjectFilters(t *testing.T) {
	e := newEnv(t)
	e.set(budget.Budget{Scope: budget.ScopeProject, ScopeID: "prj1", LimitUSD: f(100), Enabled: true})
	e.set(budget.Budget{Scope: budget.ScopeRun, ScopeID: budget.Any, LimitTokens: i(1000), Enabled: true})                  // every run
	e.set(budget.Budget{Scope: budget.ScopeTask, ScopeID: budget.Any, ProjectID: "prj2", LimitTokens: i(5), Enabled: true}) // tasks of prj2 only
	e.set(budget.Budget{Scope: budget.ScopeAgent, ScopeID: "agtW", LimitTurns: i(3), Enabled: true})
	e.set(budget.Budget{Scope: budget.ScopeAgent, ScopeID: "agtR", LimitTurns: i(3), Enabled: false})
	ts, err := e.st.Applicable(e.ctx, budget.ScopeIDs{Project: "prj1", Task: "t1", Run: "r1", Agent: "agtW"})
	if err != nil || len(ts) != 3 {
		t.Fatalf("project, run template and agent apply; the prj2 task template and the disabled one do not: %d %v", len(ts), err)
	}
	ts, _ = e.st.Applicable(e.ctx, budget.ScopeIDs{Project: "prj2", Task: "t2", Run: "r2", Agent: "agtOther"})
	if len(ts) != 2 { // run template + the prj2 task template
		t.Fatalf("%d", len(ts))
	}
}

// ---- the gate, against the real engine ----

type rig struct {
	*env
	eng   *workflow.Engine
	enf   *budget.Enforcer
	turns atomic.Int32
	notes chan budget.Notice
}

func newRig(t *testing.T) *rig {
	e := newEnv(t)
	r := &rig{env: e, notes: make(chan budget.Notice, 32)}
	r.eng = workflow.New(workflow.Config{DB: e.db, Bus: e.bus, DataDir: t.TempDir(), LeaseTTL: 2 * time.Second})
	t.Cleanup(r.eng.Close)
	r.enf = &budget.Enforcer{Store: e.st, Bus: e.bus, Notify: func(_ context.Context, n budget.Notice) { r.notes <- n }, Consume: r.eng.MarkConsumed}
	r.eng.Use(workflow.KindAgentTask, r.enf.Middleware())
	e.task("t1", "prj1", "agtW", "agtR")
	return r
}

// turn is an agent_task handler that spends tokens like a real turn would, as the worker or the reviewer.
func (r *rig) turn(agent string, tokens int, cost float64) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		r.turns.Add(1)
		r.usage(row{task: in.TaskID, agent: agent, in: tokens, cost: cost})
		r.exec(`INSERT INTO agent_runs(id,agent_id,task_id,status,started_at) VALUES(?,?,?,?,?)`, "ar"+string(rune('a'+r.turns.Load())), agent, in.TaskID, "STOPPED", storage.Now())
		return workflow.NodeOutput{Value: json.RawMessage(`"ok"`)}, nil
	}
}

func (r *rig) graph(name string, tokens int, cost float64) *workflow.Graph {
	g := workflow.NewGraph(name).
		Add(workflow.AgentTask("work", r.turn("agtW", tokens, cost)).With("agent", "work"),
			workflow.AgentTask("review", r.turn("agtR", tokens, cost)).With("agent", "review"),
			workflow.Route("end", func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
				return workflow.NodeOutput{}, nil
			})).
		Edge("work", "review").Edge("review", "end").Start("work")
	if err := r.eng.Register(g); err != nil {
		r.t.Fatal(err)
	}
	return g
}

func (r *rig) start(name string) workflow.RunID {
	id, err := r.eng.Start(r.ctx, name, nil, workflow.StartOptions{ProjectID: "prj1", TaskID: "t1"})
	if err != nil {
		r.t.Fatal(err)
	}
	return id
}

func (r *rig) wait(run workflow.RunID, want workflow.RunStatus) workflow.RunInfo {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(r.ctx, 20*time.Second)
	defer cancel()
	info, err := r.eng.Await(ctx, run, func(i workflow.RunInfo) bool { return i.Status == want || i.Status.Terminal() })
	if err != nil || info.Status != want {
		r.t.Fatalf("run is %s (%s), wanted %s: %v", info.Status, info.FailedReason, want, err)
	}
	return info
}

func (r *rig) eventsOf(typ string) []events.Event {
	evs, err := r.bus.Replay(r.ctx, 0, events.Filter{Types: []string{typ}}, 100)
	if err != nil {
		r.t.Fatal(err)
	}
	return evs
}

func TestWarningAt80PercentFiresOnceAndTheRunContinues(t *testing.T) {
	r := newRig(t)
	r.set(budget.Budget{Scope: budget.ScopeTask, ScopeID: "t1", LimitTokens: i(1000), Enabled: true})
	r.graph("warn", 850, 0.01) // the worker's turn takes the task to 85%; the reviewer's turn is blocked only at 100%
	run := r.start("warn")
	// 850 tokens: warning after the worker. The reviewer turn then takes it to 1700 (exceeded) but is allowed to run: it
	// started below 100%.
	r.wait(run, workflow.StatusCompleted)
	warns := r.eventsOf("budget.warning")
	if len(warns) != 1 {
		t.Fatalf("want exactly one budget.warning, got %d", len(warns))
	}
	var d map[string]any
	json.Unmarshal(warns[0].Data, &d)
	if d["scope"] != "task" || d["instance"] != "t1" || d["pct"].(float64) < 0.8 {
		t.Fatalf("%v", d)
	}
	if len(r.eventsOf("budget.exceeded")) != 1 {
		t.Fatal("the review turn crossed 100%; budget.exceeded must be raised once")
	}
	n1, n2 := <-r.notes, <-r.notes
	if n1.Level != "warning" || n2.Level != "exceeded" || n1.TaskID != "t1" {
		t.Fatalf("notices: %+v / %+v", n1, n2)
	}
}

func TestExceededBudgetPausesBeforeTheNextTurnAndOnlyAHumanResumes(t *testing.T) {
	r := newRig(t)
	r.set(budget.Budget{Scope: budget.ScopeTask, ScopeID: "t1", LimitTokens: i(1000), Enabled: true})
	r.graph("pause", 1200, 0.5) // the worker alone crosses 100%
	run := r.start("pause")
	info := r.wait(run, workflow.StatusWaitingHuman)
	if r.turns.Load() != 1 {
		t.Fatalf("the reviewer turn must not have started: %d turns ran", r.turns.Load())
	}
	pend, _ := r.eng.Pending(r.ctx, info.ID)
	if len(pend) != 1 || pend[0].NodeID != "review" || !pend[0].Spec.Retry || pend[0].Spec.Kind != "approval" {
		t.Fatalf("%+v", pend)
	}
	apps, _ := r.eng.Approvals(r.ctx, "REQUESTED")
	if len(apps) != 1 || apps[0].TaskID != "t1" {
		t.Fatalf("the pause is an open approval in the human's inbox: %+v", apps)
	}
	if len(r.eventsOf("budget.paused")) != 1 {
		t.Fatal("budget.paused missing")
	}
	var last budget.Notice
	for len(r.notes) > 0 {
		last = <-r.notes
	}
	if last.Level != "paused" {
		t.Fatalf("the last notice is the pause: %+v", last)
	}
	// time passes: nothing resumes it by itself
	time.Sleep(300 * time.Millisecond)
	if r.turns.Load() != 1 {
		t.Fatal("a paused run must stay paused")
	}
	// the human approves one more turn without raising the limit: the reviewer turn runs, then everything finishes
	time.Sleep(50 * time.Millisecond)
	if err := r.eng.Resume(r.ctx, run, workflow.Response{RequestID: pend[0].ID, Decision: "approved", By: "human:kyle"}); err != nil {
		t.Fatal(err)
	}
	r.wait(run, workflow.StatusCompleted)
	if r.turns.Load() != 2 {
		t.Fatalf("exactly one more turn after the approval, got %d total", r.turns.Load())
	}
	left, _ := r.eng.Approvals(r.ctx, "CONSUMED")
	if len(left) != 1 {
		t.Fatalf("the approval must be consumed by the turn it allowed: %+v", left)
	}
}

func TestRaisingTheLimitResumesWithoutAnApprovalDecisionMattering(t *testing.T) {
	r := newRig(t)
	b := r.set(budget.Budget{Scope: budget.ScopeProject, ScopeID: "prj1", LimitUSD: f(1), Enabled: true})
	r.graph("raise", 10, 1.5) // $1.50 against a $1 project budget after the first turn
	run := r.start("raise")
	info := r.wait(run, workflow.StatusWaitingHuman)
	pend, _ := r.eng.Pending(r.ctx, info.ID)
	b.LimitUSD = f(5)
	if _, err := r.st.Set(r.ctx, b, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := r.eng.Resume(r.ctx, run, workflow.Response{RequestID: pend[0].ID, Decision: "approved", By: "human:kyle"}); err != nil {
		t.Fatal(err)
	}
	r.wait(run, workflow.StatusCompleted)
	if r.turns.Load() != 2 {
		t.Fatalf("turns %d", r.turns.Load())
	}
	// raising the limit re-armed the thresholds: $3 of $5 is 60%, no new warning; the old alerts were cleared
	var n int
	r.db.Reader().QueryRow(`SELECT COUNT(*) FROM budget_alerts`).Scan(&n)
	if n != 0 {
		t.Fatalf("alerts for a $5 limit at $3 spent: %d", n)
	}
}

func TestRejectingAPauseEndsTheRunAndNoTurnStarts(t *testing.T) {
	r := newRig(t)
	r.set(budget.Budget{Scope: budget.ScopeTask, ScopeID: "t1", LimitTokens: i(1000), Enabled: true})
	r.graph("reject", 1200, 0.01)
	run := r.start("reject")
	info := r.wait(run, workflow.StatusWaitingHuman)
	pend, _ := r.eng.Pending(r.ctx, info.ID)
	time.Sleep(50 * time.Millisecond)
	if err := r.eng.Resume(r.ctx, run, workflow.Response{RequestID: pend[0].ID, Decision: "rejected", By: "human:kyle"}); err != nil {
		t.Fatal(err)
	}
	fin := r.wait(run, workflow.StatusFailed)
	if r.turns.Load() != 1 || !strings.Contains(fin.FailedReason, "rejected by human:kyle") {
		t.Fatalf("turns %d, reason %q", r.turns.Load(), fin.FailedReason)
	}
}

func TestAnAnswerNotFromAHumanIdentityDoesNotLetTheTurnStart(t *testing.T) {
	r := newRig(t)
	r.set(budget.Budget{Scope: budget.ScopeTask, ScopeID: "t1", LimitTokens: i(1000), Enabled: true})
	r.graph("forged", 1200, 0.01)
	run := r.start("forged")
	info := r.wait(run, workflow.StatusWaitingHuman)
	pend, _ := r.eng.Pending(r.ctx, info.ID)
	time.Sleep(50 * time.Millisecond)
	// something other than a human identity answers the request (the engine itself does not police identities)
	if err := r.eng.Resume(r.ctx, run, workflow.Response{RequestID: pend[0].ID, Decision: "approved", By: "agent:worker"}); err != nil {
		t.Fatal(err)
	}
	fin := r.wait(run, workflow.StatusFailed)
	if r.turns.Load() != 1 || !strings.Contains(fin.FailedReason, "not a human identity") {
		t.Fatalf("turns %d, reason %q", r.turns.Load(), fin.FailedReason)
	}
}

func TestAgentTurnLimitGatesTheWorkNodeOfASecondRun(t *testing.T) {
	r := newRig(t)
	r.set(budget.Budget{Scope: budget.ScopeAgent, ScopeID: "agtW", LimitTurns: i(1), Enabled: true})
	r.graph("agent", 1, 0.01)
	run1 := r.start("agent")
	r.wait(run1, workflow.StatusCompleted) // the reviewer is another agent with no budget; the worker used its 1 turn
	run2 := r.start("agent")
	info := r.wait(run2, workflow.StatusWaitingHuman)
	pend, _ := r.eng.Pending(r.ctx, info.ID)
	if len(pend) != 1 || pend[0].NodeID != "work" {
		t.Fatalf("the worker's second turn must be the one that is held: %+v", pend)
	}
	if r.turns.Load() != 2 {
		t.Fatalf("run 1 used two turns, run 2 none: %d", r.turns.Load())
	}
}

func TestPerRunCeilingTemplateAppliesToEachRunSeparately(t *testing.T) {
	r := newRig(t)
	r.set(budget.Budget{Scope: budget.ScopeRun, ScopeID: budget.Any, ProjectID: "prj1", LimitTokens: i(1000), Enabled: true})
	r.graph("ceiling", 600, 0.01)
	run1 := r.start("ceiling")
	r.wait(run1, workflow.StatusCompleted) // 600 before the review turn: allowed; the review turn ends at 1200
	run2 := r.start("ceiling")
	// run 2 starts a new window: its own usage is 0 at the worker turn even though the task already spent 1200
	r.wait(run2, workflow.StatusCompleted)
}

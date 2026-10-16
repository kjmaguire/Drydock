package evals_test

import (
	"context"
	"database/sql"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/evals"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// A real SQLite database seeded with rows shaped exactly like the ones the app writes. Every expected number below was
// worked out by hand from the seed (see the comments), then compared with what the metrics code computes. The seed is
// labeled data, not a product run: it proves the arithmetic, the source split and the n/a rules, not any saving.

type world struct {
	t   *testing.T
	ctx context.Context
	db  *storage.DB
	seq int
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &world{t: t, ctx: ctx, db: db}
}

func (w *world) exec(q string, args ...any) {
	w.t.Helper()
	if err := w.db.WriteTx(w.ctx, func(tx *sql.Tx) error { _, err := tx.Exec(q, args...); return err }); err != nil {
		w.t.Fatal(err)
	}
}

type use struct {
	task, agent, run, tier, provider, purpose, source, costSource string
	cost                                                          float64
	in, out, cached                                               int
	hit                                                           bool
}

func (w *world) usage(u use) {
	w.t.Helper()
	if u.tier == "" {
		u.tier = "premium"
	}
	if u.source == "" {
		u.source = "reported"
	}
	if u.costSource == "" {
		u.costSource = "unknown"
	}
	if u.provider == "" {
		u.provider = "claude"
	}
	hit := 0
	if u.hit {
		hit = 1
	}
	w.exec(`INSERT INTO model_usage(ts,agent_run_id,task_id,agent_id,provider,model,tier,input_tokens,output_tokens,cached_tokens,cost_usd,source,cost_source,cache_hit,purpose)
		VALUES(?,?,?,?,?,'m',?,?,?,?,?,?,?,?,?)`, storage.Now(), u.run, u.task, u.agent, u.provider, u.tier, u.in, u.out, u.cached, u.cost, u.source, u.costSource, hit, u.purpose)
}

func (w *world) task(id, status, assignee, reviewer, acceptedBy string, started, completed int64) {
	w.t.Helper()
	w.seq++
	w.exec(`INSERT INTO tasks(id,project_id,seq,title,status,assignee,reviewer,accepted_by,created_at,updated_at,started_at,completed_at,iteration) VALUES(?, 'p1', ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, 1)`,
		id, w.seq, id, status, assignee, reviewer, acceptedBy, storage.Now(), started, completed)
}

func (w *world) review(task, reviewer, verdict string) {
	w.exec(`INSERT INTO task_reviews(task_id,attempt,reviewer,verdict,created_at) VALUES(?,0,?,?,?)`, task, reviewer, verdict, storage.Now())
}

func (w *world) evidence(task, kind string, passed bool) {
	p := 0
	if passed {
		p = 1
	}
	w.exec(`INSERT INTO task_evidence(task_id,attempt,kind,passed,created_at) VALUES(?,0,?,?,?)`, task, kind, p, storage.Now())
}

func (w *world) seed() {
	w.exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p1','proj','/x',1)`)
	w.exec(`INSERT INTO providers(id,name,kind,base_url,data_class,tier,created_at) VALUES('prov_cheap','cheapco','aggregator','https://x','private','cheap',1)`)
	// agtCW and agtCR run on the registered cheap provider; the runtime stamped their rows tier=premium anyway (core passes
	// every agent as premium), which is exactly why evals resolve the tier through the agent's provider.
	w.exec(`INSERT INTO agents(id,project_id,name,adapter,role,provider_id,created_at) VALUES
		('agtCW','p1','cw','opencode','Backend','prov_cheap',1),('agtCR','p1','cr','opencode','Reviewer','prov_cheap',1),
		('agtPW','p1','pw','claude','Backend','',1),('agtPR','p1','pr','claude','Reviewer','',1)`)
	now := storage.Now()
	w.task("T1", "COMPLETED", "agtCW", "agtPR", "human:kyle", now-3000, now-1000)
	w.task("T2", "COMPLETED", "agtPW", "agtCR", "human:kyle", now-3000, now-1000)
	w.task("T3", "COMPLETED", "agtCW", "agtCR", "human:kyle", now-3000, now-1000)
	w.task("T4", "FAILED", "agtCW", "agtPR", "", now-3000, now-1000)
	w.task("T5", "IN_PROGRESS", "agtCW", "agtPR", "", 0, 0)

	// T1: cheap worker (2 calls reported $0.10), premium reviewer (reported $0.30)
	w.usage(use{task: "T1", agent: "agtCW", run: "ar1", cost: 0.10, in: 100, out: 10})
	w.usage(use{task: "T1", agent: "agtCW", run: "ar2", cost: 0.10, in: 100, out: 10})
	w.usage(use{task: "T1", agent: "agtPR", run: "ar3", cost: 0.30, in: 500, out: 50, cached: 400})
	w.review("T1", "agtPR", "REVISE")
	w.review("T1", "agtPR", "SHIP")
	w.evidence("T1", "test", false)
	w.evidence("T1", "test", true)
	// T2: premium worker $0.50, cheap reviewer with NO cost reported (unknown, not zero)
	w.usage(use{task: "T2", agent: "agtPW", run: "ar4", cost: 0.50, in: 800, out: 80})
	w.usage(use{task: "T2", agent: "agtCR", run: "ar5", in: 300, out: 30})
	w.review("T2", "agtCR", "SHIP")
	w.evidence("T2", "test", true)
	// T3: all cheap; the cost was computed from the price table (estimated)
	w.usage(use{task: "T3", agent: "agtCW", run: "ar6", cost: 0.02, costSource: "computed_from_table", source: "estimated", in: 200, out: 20})
	w.usage(use{task: "T3", agent: "agtCR", run: "ar7", cost: 0, costSource: "computed_from_table", hit: true})
	w.review("T3", "agtCR", "SHIP")
	w.evidence("T3", "test", true)
	// T4: failed after a stage-2 watchdog trip
	w.usage(use{task: "T4", agent: "agtCW", run: "ar8", cost: 0.04, in: 50, out: 5})
	// outside any task: an app-owned call; plus overhead that must be excluded
	w.usage(use{agent: "", cost: 0.05, in: 10, out: 1, purpose: "classify"})
	w.usage(use{cost: 0.01, purpose: "tool_call_probe", tier: "cheap"})

	// watchdog: T1 stage 1 followed by a human reset in the same run (false); T4 stage 2 nobody acted on; T2 stage 1
	// dismissed from Needs-you (false); one soft warning (not a trip); the reset row itself is not a trip
	w.exec(`INSERT INTO workflow_runs(id,workflow,status,task_id,topology_hash,created_at,updated_at) VALUES
		('r1','iterate-until-verified','COMPLETED','T1','h',?,?),('r2','iterate-until-verified','FAILED','T4','h',?,?),
		('r3','iterate-until-verified','WAITING_HUMAN','T3','h',?,?),('r4','iterate-until-verified','RUNNING','T5','h',?,?),
		('r5','iterate-until-verified','CANCELLED','T2','h',?,?),('r6','iterate-until-verified','COMPLETED','T2','h',?,?)`,
		now-3500, now-1000, now-3500, now-1000, now-3500, now-1000, now-3500, now-1000, now-3500, now-1000, now-500, now-400)
	w.exec(`UPDATE tasks SET run_id='r1' WHERE id='T1'`)
	w.exec(`UPDATE workflow_runs SET failed_reason='recover: topology mismatch or missing checkpoint' WHERE id='r2'`)
	w.exec(`INSERT INTO watchdog_trips(id,task_id,run_id,node_id,signal,stage,created_at) VALUES
		(1,'T1','r1','work','error_echo',1,?),(2,'T1','','','reset',0,?),(3,'T4','r2','work','tool_spin',2,?),(4,'T2','r5','work','tool_spin',1,?),(5,'T3','r3','work','stagnation',1,?)`,
		now-2500, now-2400, now-2000, now-2000, now-2000)
	w.exec(`INSERT INTO room_inbox_acks(item_key,acked_by,acked_at) VALUES('watchdog:4','human:kyle',?)`, now)
	// resume: r1, r3, r4, r5 were recovered after a restart; r2 failed its recovery
	for _, run := range []string{"r1", "r3", "r4", "r5"} {
		w.exec(`INSERT INTO events(ts,type,run_id,data) VALUES(?,?,?,x'7b7d')`, now-2000, "workflow.recovering", run)
	}
	// routing decisions: one per task (static, tier resolved cheap or premium)
	for id, spec := range map[string][3]string{"d1": {"T1", "opencode", "cheap"}, "d2": {"T2", "claude", "premium"}, "d3": {"T3", "opencode", "cheap"}, "d4": {"T4", "opencode", "cheap"}, "d5": {"T5", "opencode", "cheap"}} {
		w.exec(`INSERT INTO routing_decisions(id,ts,task_id,decided_by,provider_id,model,request,decision) VALUES(?,?,?,'static',?,'m',?,?)`,
			id, now-3000, spec[0], spec[1], `{"TaskType":"implement","Risk":false}`, `{"Facts":{"Tier":"`+spec[2]+`"}}`)
	}
	for _, id := range []string{"T1", "T2", "T3", "T4"} {
		if err := evals.Record(w.ctx, w.db, id); err != nil {
			w.t.Fatal(err)
		}
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestMetricsAreComputedFromTheRowsAndMatchTheHandWorkedNumbers(t *testing.T) {
	w := newWorld(t)
	w.seed()
	rep, err := evals.Build(w.ctx, w.db, evals.Options{MinN: 3})
	if err != nil {
		t.Fatal(err)
	}

	// tasks
	q := rep.Quality
	if q.Started != 4 || q.Accepted != 3 || q.Failed != 1 || q.Active != 1 || q.Cancelled != 0 {
		t.Fatalf("tasks: %+v", q)
	}
	if q.Completion.Num != 3 || q.Completion.Den != 4 {
		t.Fatalf("completion 3/4: %+v", q.Completion)
	}

	// cost: all non-overhead rows in the window. reported = T1 0.50 + T2 0.50 + T4 0.04 + unattributed 0.05 = 1.09;
	// estimated = T3 0.02; unknown = T2's cheap review call (1 row). The probe row ($0.01) is overhead, excluded.
	c := rep.Cost
	if c.Accepted != 3 || !near(c.AllWindow.ReportedUSD, 1.09) || !near(c.AllWindow.EstimatedUSD, 0.02) || c.AllWindow.UnknownRows != 1 {
		t.Fatalf("all-window cost: %+v", c.AllWindow)
	}
	if !near(c.Unattributed.ReportedUSD, 0.05) {
		t.Fatalf("unattributed: %+v", c.Unattributed)
	}
	if !near(c.AcceptedOnly.ReportedUSD, 1.00) || !near(c.AcceptedOnly.EstimatedUSD, 0.02) || c.AcceptedOnly.UnknownRows != 1 {
		t.Fatalf("accepted-only cost: %+v", c.AcceptedOnly)
	}
	if c.PerAcceptedOnly == nil || !near(c.PerAcceptedOnly.ReportedUSD, 1.00/3) || !near(c.PerAcceptedOnly.EstimatedUSD, 0.02/3) {
		t.Fatalf("per accepted: %+v", c.PerAcceptedOnly)
	}
	if o := c.Overhead["tool_call_probe"]; !near(o.ReportedUSD, 0.01) {
		t.Fatalf("overhead must be listed apart: %+v", c.Overhead)
	}
	// by tier (accepted tasks): the cheap worker's rows are cheap although the runtime stamped them premium
	tier := map[string]evals.Group{}
	for _, g := range c.ByTier {
		tier[g.Name] = g
	}
	if g := tier["premium"]; !near(g.Money.ReportedUSD, 0.80) || g.Money.UnknownRows != 0 { // T1 reviewer 0.30 + T2 worker 0.50
		t.Fatalf("premium: %+v", g)
	}
	if g := tier["cheap"]; !near(g.Money.ReportedUSD, 0.20) || !near(g.Money.EstimatedUSD, 0.02) || g.Money.UnknownRows != 1 {
		t.Fatalf("cheap: %+v", g)
	}
	role := map[string]evals.Group{}
	for _, g := range c.ByRole {
		role[g.Name] = g
	}
	if !near(role["Backend"].Money.ReportedUSD, 0.70) || !near(role["Reviewer"].Money.ReportedUSD, 0.30) || role["Reviewer"].Money.UnknownRows != 1 {
		t.Fatalf("by role: %+v", role)
	}

	// premium usage: T1's reviewer run ar3 and T2's worker run ar4 are the premium turns of accepted tasks
	if rep.Premium.Turns != 2 || rep.Premium.TurnsPerAccepted == nil || !near(*rep.Premium.TurnsPerAccepted, 2.0/3) {
		t.Fatalf("premium: %+v", rep.Premium)
	}
	if len(rep.Premium.ByProvider) != 1 || rep.Premium.ByProvider[0].Tokens.Input != 1300 || rep.Premium.ByProvider[0].Tokens.Cached != 400 {
		t.Fatalf("premium tokens by provider: %+v", rep.Premium.ByProvider)
	}
	if !strings.Contains(rep.Premium.QuotaDelta, "not available") {
		t.Fatal("quota delta must say it is not available")
	}

	// cheap-only: T1 yes, T2 no, T3 yes (cheap worker 2/3); strict: only T3 (1/3)
	if rep.Cheap.CheapWorker.Num != 2 || rep.Cheap.CheapWorker.Den != 3 || rep.Cheap.StrictCheapOnly.Num != 1 || rep.Cheap.StrictCheapOnly.Den != 3 {
		t.Fatalf("cheap: %+v", rep.Cheap)
	}

	// premium REVISE on cheap-worker tasks (T1, T3, and T4 which escalated): premium verdicts are T1's REVISE and SHIP
	rv := rep.Revise
	if rv.CheapWorkerTasks != 3 || rv.PremiumVerdicts.Num != 1 || rv.PremiumVerdicts.Den != 2 || rv.TasksWithRevise.Num != 1 || rv.TasksWithRevise.Den != 1 {
		t.Fatalf("revise: %+v", rv)
	}
	if rv.Escalated.Num != 1 || rv.Escalated.Den != 3 {
		t.Fatalf("escalated: %+v", rv.Escalated)
	}

	// tests and iterations: first test passed for T2, T3 but not T1; T1 needed 2 reviews to SHIP
	if q.FirstCheckTestPass.Num != 2 || q.FirstCheckTestPass.Den != 3 || q.FinalTestPass.Num != 3 || q.FinalTestPass.Den != 3 {
		t.Fatalf("tests: %+v / %+v", q.FirstCheckTestPass, q.FinalTestPass)
	}
	if d := q.IterationsToShip; d.N != 3 || *d.Median != 1 || *d.Min != 1 || *d.Max != 2 {
		t.Fatalf("iterations: %+v", d)
	}

	// resume: recovered r1, r3 (completed / waiting); failed r2 (failed recovery); pending r4; cancelled r5
	rs := rep.Resume
	if rs.Attempts != 5 || rs.Recovered != 2 || rs.Failed != 1 || rs.Pending != 1 || rs.Cancelled != 1 || rs.Reliability.Num != 2 || rs.Reliability.Den != 3 {
		t.Fatalf("resume: %+v", rs)
	}

	// watchdog: 3 trips (error_echo, tool_spin x2); 1 warning; false: T1 reset in the same run, T2 dismissed
	wd := rep.Watchdog
	if wd.Trips != 3 || wd.Stage1 != 2 || wd.Stage2 != 1 || wd.Warnings != 1 || wd.FalseTrips != 2 || wd.FalseRate.Num != 2 || wd.FalseRate.Den != 3 {
		t.Fatalf("watchdog: %+v", wd)
	}
	if wd.PerAccepted == nil || !near(*wd.PerAccepted, 1.0) {
		t.Fatalf("trips per accepted: %v", wd.PerAccepted)
	}

	// routing: 5 decisions, 4 with outcomes; good = accepted and not escalated = d1, d2, d3 -> 3/4; d5's task is open
	rt := rep.Routing
	if rt.Decisions != 5 || rt.WithOutcome != 4 || rt.Pending != 1 || rt.Good.Num != 3 || rt.Good.Den != 4 {
		t.Fatalf("routing: %+v", rt)
	}

	// the same facts, as routing_outcomes rows
	var accepted, escalated, premUnits int
	var costSource string
	w.db.Reader().QueryRow(`SELECT accepted, escalated, premium_subscription_units, cost_source FROM routing_outcomes WHERE task_id='T1' AND decision_id='d1'`).Scan(&accepted, &escalated, &premUnits, &costSource)
	if accepted != 1 || escalated != 0 || premUnits != 550 || costSource != "reported" {
		t.Fatalf("T1 outcome: %d %d %d %s", accepted, escalated, premUnits, costSource)
	}
	w.db.Reader().QueryRow(`SELECT accepted, escalated, cost_source FROM routing_outcomes WHERE task_id='T4'`).Scan(&accepted, &escalated, &costSource)
	if accepted != 0 || escalated != 1 {
		t.Fatalf("T4 outcome: %d %d", accepted, escalated)
	}
	w.db.Reader().QueryRow(`SELECT cost_source FROM routing_outcomes WHERE task_id='T2'`).Scan(&costSource)
	if costSource != "mixed" {
		t.Fatalf("T2 has a call with no reported cost: its figure is a lower bound, not 'reported' (%s)", costSource)
	}

	// the markdown is rendered from the same data
	md := rep.Markdown()
	for _, want := range []string{"Accepted tasks: **3**", "reported $0.3333", "unknown 1 call(s)", "66.7% (2/3)", "33.3% (1/3)", "50.0% (1/2)", "75.0% (3/4)", "not available"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}
}

func TestNothingIsInventedWhenThereIsNoData(t *testing.T) {
	w := newWorld(t)
	rep, err := evals.Build(w.ctx, w.db, evals.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Cost.Accepted != 0 || rep.Cost.PerAcceptedAll != nil || rep.Cost.PerAcceptedOnly != nil || rep.Premium.TurnsPerAccepted != nil || rep.Watchdog.PerAccepted != nil {
		t.Fatalf("no accepted task means no per-task figure: %+v", rep.Cost)
	}
	for name, r := range map[string]evals.Ratio{"completion": rep.Quality.Completion, "cheap": rep.Cheap.CheapWorker, "strict": rep.Cheap.StrictCheapOnly,
		"revise": rep.Revise.PremiumVerdicts, "resume": rep.Resume.Reliability, "false": rep.Watchdog.FalseRate, "routing": rep.Routing.Good} {
		if r.Rate != nil {
			t.Errorf("%s: a rate with a zero denominator must be absent, got %v", name, *r.Rate)
		}
	}
	md := rep.Markdown()
	if !strings.Contains(md, "n/a (n=0)") || !strings.Contains(md, "No tasks in the window.") || strings.Contains(md, "NaN") {
		t.Fatalf("markdown for an empty database:\n%s", md)
	}
}

func TestSmallSamplesGetTotalsAndRawRowsNotAverages(t *testing.T) {
	w := newWorld(t)
	w.seed()
	rep, _ := evals.Build(w.ctx, w.db, evals.Options{ProjectID: "p1"}) // MinN defaults to 5; there are 3 accepted tasks
	// with a project filter, a call that names neither a task nor an agent belongs to no project and is not counted for it
	if !near(rep.Cost.AllWindow.ReportedUSD, 1.04) || rep.Cost.Unattributed.Rows != 0 {
		t.Fatalf("project-filtered cost: %+v", rep.Cost.AllWindow)
	}
	for _, g := range rep.Cost.ByTier {
		if g.PerAccepted != nil {
			t.Fatalf("a breakdown with n < 5 must not print per-task averages: %+v", g)
		}
	}
	if len(rep.Rows) != 5 {
		t.Fatalf("raw per-task rows: %d", len(rep.Rows))
	}
	if !strings.Contains(rep.Markdown(), "n < 5: see the per-task rows") {
		t.Fatal("the markdown must say why there is no average")
	}
}

func TestWindowAndProjectFiltersApply(t *testing.T) {
	w := newWorld(t)
	w.seed()
	w.exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p2','other','/y',1)`)
	rep, _ := evals.Build(w.ctx, w.db, evals.Options{ProjectID: "p2"})
	if rep.Quality.Accepted != 0 || len(rep.Rows) != 0 || rep.Cost.AllWindow.Rows != 0 {
		t.Fatalf("project p2 has nothing: %+v", rep.Cost.AllWindow)
	}
	future := time.Now().Add(time.Hour).UnixMilli()
	rep, _ = evals.Build(w.ctx, w.db, evals.Options{SinceMS: future})
	if rep.Quality.Accepted != 0 || rep.Cost.AllWindow.Rows != 0 {
		t.Fatalf("a window in the future is empty: %+v", rep.Cost.AllWindow)
	}
}

// ---- the recorder: real task service, real event bus ----

func TestOutcomesAreRecordedFromTaskEventsExactlyOnceAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 256)
	t.Cleanup(func() { bus.Close(); db.Close() })
	db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p1','proj','/x',1)`)
	svc := &tasks.Service{DB: db, Bus: bus}
	out := &evals.Outcomes{DB: db, Bus: bus}
	if err := out.Start(ctx); err != nil {
		t.Fatal(err)
	}

	finish := func(title string, accept bool) tasks.Task {
		task, err := svc.Create(ctx, tasks.CreateReq{ProjectID: "p1", Title: title})
		if err != nil {
			t.Fatal(err)
		}
		a, r := "agent-1", "agent-2"
		svc.Patch(ctx, task.ID, tasks.Update{Assignee: &a, Reviewer: &r})
		for _, to := range []tasks.Status{tasks.Ready, tasks.Claimed, tasks.InProgress} {
			if _, err := svc.Transition(ctx, task.ID, to, tasks.TransitionOpts{By: "system"}); err != nil {
				t.Fatal(err)
			}
		}
		if !accept {
			nt, err := svc.Transition(ctx, task.ID, tasks.Failed, tasks.TransitionOpts{By: "system", Reason: "gave up"})
			if err != nil {
				t.Fatal(err)
			}
			return nt
		}
		tr := true
		for _, e := range []tasks.Evidence{{Kind: "test", Passed: &tr}, {Kind: "diff"}, {Kind: "commit", Ref: "abc"}} {
			svc.AddEvidence(ctx, task.ID, e)
		}
		if _, err := svc.Transition(ctx, task.ID, tasks.Review, tasks.TransitionOpts{By: "system"}); err != nil {
			t.Fatal(err)
		}
		svc.AddReview(ctx, task.ID, tasks.ReviewRecord{Reviewer: "agent-2", Verdict: "SHIP"})
		svc.Transition(ctx, task.ID, tasks.Verification, tasks.TransitionOpts{By: "system"})
		nt, err := svc.Transition(ctx, task.ID, tasks.Completed, tasks.TransitionOpts{By: "human:kyle"})
		if err != nil {
			t.Fatal(err)
		}
		return nt
	}
	wait := func(q string, want int, args ...any) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			var n int
			db.Reader().QueryRow(q, args...).Scan(&n)
			if n == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %q == %d", q, want)
	}
	done := finish("accepted one", true)
	wait(`SELECT COUNT(*) FROM routing_outcomes WHERE task_id=? AND accepted=1 AND outcome='accepted' AND accepted_by='human:kyle'`, 1, done.ID)
	failed := finish("failed one", false)
	wait(`SELECT COUNT(*) FROM routing_outcomes WHERE task_id=? AND accepted=0 AND outcome='failed' AND failure_class='failed'`, 1, failed.ID)

	// a task finished while the recorder is down is picked up from the saved cursor on the next start, and nothing is doubled
	out.Stop()
	late := finish("finished while down", true)
	var n int
	db.Reader().QueryRow(`SELECT COUNT(*) FROM routing_outcomes WHERE task_id=?`, late.ID).Scan(&n)
	if n != 0 {
		t.Fatal("the stopped recorder must not have recorded it")
	}
	out2 := &evals.Outcomes{DB: db, Bus: bus}
	if err := out2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer out2.Stop()
	wait(`SELECT COUNT(*) FROM routing_outcomes WHERE task_id=?`, 1, late.ID)
	time.Sleep(200 * time.Millisecond)
	wait(`SELECT COUNT(*) FROM routing_outcomes`, 3)
	// recording the same task again is a no-op
	if err := evals.Record(ctx, db, done.ID); err != nil {
		t.Fatal(err)
	}
	wait(`SELECT COUNT(*) FROM routing_outcomes`, 3)
}

// A task's stratum is the class of its own work and review turns. The other routed turns of the task (decision P63: the
// template roles' spec, tests, design and security review) carry their own turn's class, so an architect's design (class
// architecture) or a security review does not make a general task premium-only; their decisions still get the task's
// outcome rows. A room reply in the task's channel gets none: the task's outcome does not judge a chat answer.
func TestTheStratumComesFromTheTasksWorkTurnsOnly(t *testing.T) {
	w := newWorld(t)
	w.exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p1','proj','/x',1)`)
	w.exec(`INSERT INTO tasks(id,project_id,seq,title,status,created_at,updated_at) VALUES('T9','p1',9,'t','IN_PROGRESS',1,1)`)
	for i, row := range [][2]string{{"design", "architecture"}, {"security_review", "security_review"}, {"room_reply", "room_reply"}, {"agent_task", "implement"}, {"review", ""}} {
		w.exec(`INSERT INTO routing_decisions(id,ts,task_id,run_kind,decided_by,provider_id,model,request,decision) VALUES(?,?,'T9',?,'rules','p','m',?,'{}')`,
			"dx"+row[0], 10+i, row[0], `{"TaskType":"`+row[1]+`","Risk":false}`)
	}
	f, err := evals.Facts(w.ctx, w.db, "T9")
	if err != nil {
		t.Fatal(err)
	}
	if f.Stratum != "general" || len(f.Decisions) != 4 || slices.Contains(f.Decisions, "dxroom_reply") {
		t.Fatalf("stratum %q decisions %v", f.Stratum, f.Decisions)
	}
}

// Room replies are routed (P63) but are not task work: the routing report neither counts them nor leaves them pending
// forever (a reply in a project channel has no task, so it would never get an outcome).
func TestRoomRepliesStayOutOfTheRoutingReport(t *testing.T) {
	w := newWorld(t)
	w.seed()
	now := time.Now().UnixMilli()
	w.exec(`INSERT INTO routing_decisions(id,ts,task_id,run_kind,decided_by,provider_id,model,request,decision) VALUES
		('reply-in-task',?,'T1','room_reply','rules','p','m','{"TaskType":"room_reply"}','{}'),
		('reply-in-channel',?,'','room_reply','rules','p','m','{"TaskType":"room_reply"}','{}')`, now-2500, now-2500)
	if err := evals.Record(w.ctx, w.db, "T1"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := w.db.Reader().QueryRow(`SELECT COUNT(*) FROM routing_outcomes WHERE decision_id LIKE 'reply-%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a reply got the task's outcome: %d %v", n, err)
	}
	rep, err := evals.Build(w.ctx, w.db, evals.Options{MinN: 3})
	if err != nil {
		t.Fatal(err)
	}
	if rt := rep.Routing; rt.Decisions != 5 || rt.WithOutcome != 4 || rt.Pending != 1 {
		t.Fatalf("routing with replies: %+v", rt)
	}
}

// A report reads a bounded number of tasks, the newest, and says what it left out (audit 2026-10-16, L18): a task's facts take
// about ten reads of the database, and a report of every task there ever was held a connection for minutes.
func TestAReportReadsOnlyTheNewestTasksAndSaysWhatItLeftOut(t *testing.T) {
	w := newWorld(t)
	w.seed() // five tasks
	rep, err := evals.Build(w.ctx, w.db, evals.Options{MaxCohort: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 3 || rep.Rows[0].ID != "T3" || rep.Rows[1].ID != "T4" || rep.Rows[2].ID != "T5" {
		t.Fatalf("%d rows %+v, want the 3 newest (T3 T4 T5)", len(rep.Rows), rep.Rows)
	}
	said := false
	for _, n := range rep.NotMeasured {
		said = said || (strings.Contains(n, "2 oldest of the 5 tasks") && strings.Contains(n, "newest 3"))
	}
	if !said {
		t.Fatalf("the report does not say it left tasks out: %v", rep.NotMeasured)
	}
	all, err := evals.Build(w.ctx, w.db, evals.Options{})
	if err != nil || len(all.Rows) != 5 {
		t.Fatalf("the default reads all five: %d rows, %v", len(all.Rows), err)
	}
	for _, n := range all.NotMeasured {
		if strings.Contains(n, "oldest of the") {
			t.Fatalf("a complete report says it left tasks out: %q", n)
		}
	}
}

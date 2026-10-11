package evals

import (
	"context"
	"database/sql"
	"sort"

	"github.com/kjmaguire/drydock/internal/budget"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Options select the cohort. A task is in the cohort when it was created before UntilMS and last changed (completed, or
// updated while open) at or after SinceMS; usage is windowed by its own timestamp.
type Options struct {
	ProjectID string `json:"projectId,omitempty"`
	SinceMS   int64  `json:"sinceMs,omitempty"`
	UntilMS   int64  `json:"untilMs,omitempty"`
	// MinN is the smallest number of accepted tasks for which per-task averages are printed for a breakdown (default 5,
	// docs/evals.md s3.8). Below it the totals and the raw per-task rows are all that is shown.
	MinN int `json:"minN,omitempty"`
}

// Ratio is a count over a denominator. Rate is nil when the denominator is zero: a missing measurement is never printed as 0.
type Ratio struct {
	Num  int      `json:"num"`
	Den  int      `json:"den"`
	Rate *float64 `json:"rate,omitempty"`
}

func ratio(num, den int) Ratio {
	r := Ratio{Num: num, Den: den}
	if den > 0 {
		v := float64(num) / float64(den)
		r.Rate = &v
	}
	return r
}

// Dist is the distribution of a small integer measure (medians and ranges, never significance claims, docs/evals.md s1).
type Dist struct {
	N      int      `json:"n"`
	Median *float64 `json:"median,omitempty"`
	Min    *int     `json:"min,omitempty"`
	Max    *int     `json:"max,omitempty"`
}

func dist(v []int) Dist {
	d := Dist{N: len(v)}
	if len(v) == 0 {
		return d
	}
	s := append([]int(nil), v...)
	sort.Ints(s)
	mn, mx := s[0], s[len(s)-1]
	var med float64
	if len(s)%2 == 1 {
		med = float64(s[len(s)/2])
	} else {
		med = float64(s[len(s)/2-1]+s[len(s)/2]) / 2
	}
	d.Median, d.Min, d.Max = &med, &mn, &mx
	return d
}

// Group is the cost of one slice of the accepted work.
type Group struct {
	Name     string `json:"name"`
	Money    Money  `json:"money"`
	Tokens   Tokens `json:"tokens"`
	Accepted int    `json:"accepted"` // the denominator: accepted tasks (all of them for role/tier, the workflow's for workflow)
	// PerAccepted is set only when Accepted >= MinN.
	PerAccepted *Money `json:"perAccepted,omitempty"`
}

// CostSection is cost per accepted task (docs/evals.md s3.1).
type CostSection struct {
	Accepted        int              `json:"accepted"`
	AllWindow       Money            `json:"allWindow"`    // every non-overhead usage row in the window, attributed or not
	AcceptedOnly    Money            `json:"acceptedOnly"` // the rows of accepted tasks
	Unattributed    Money            `json:"unattributed"` // rows with no task (app-owned LLM calls)
	Overhead        map[string]Money `json:"overhead"`     // connection tests, probes, ...: excluded from the figures above
	PerAcceptedAll  *Money           `json:"perAcceptedAll,omitempty"`
	PerAcceptedOnly *Money           `json:"perAcceptedOnly,omitempty"`
	ByTier          []Group          `json:"byTier"`
	ByRole          []Group          `json:"byRole"`
	ByWorkflow      []Group          `json:"byWorkflow"`
	Note            string           `json:"note"`
}

// ProviderTokens is premium token usage of one provider, kept apart because vendors count tokens differently (D12).
type ProviderTokens struct {
	Provider string `json:"provider"`
	Tokens   Tokens `json:"tokens"`
	Money    Money  `json:"money"`
}

// PremiumSection is premium usage per accepted task (docs/evals.md s3.2).
type PremiumSection struct {
	Accepted         int              `json:"accepted"`
	Turns            int64            `json:"turns"` // distinct agent runs with premium usage, over accepted tasks
	Calls            int64            `json:"calls"` // premium usage rows with no agent run
	ByProvider       []ProviderTokens `json:"byProvider"`
	TurnsPerAccepted *float64         `json:"turnsPerAccepted,omitempty"`
	QuotaDelta       string           `json:"quotaDelta"`
}

// CheapSection is the cheap-only acceptance share (docs/evals.md s3.3).
type CheapSection struct {
	CheapWorker          Ratio            `json:"cheapWorker"`     // accepted tasks whose worker ran only on cheap tier, no escalation / classified accepted tasks
	StrictCheapOnly      Ratio            `json:"strictCheapOnly"` // accepted tasks with zero premium rows / classified
	Unclassified         int              `json:"unclassified"`    // accepted tasks with no usage rows to classify
	Strata               map[string]int   `json:"strata"`          // accepted tasks by routing stratum
	CheapWorkerByStratum map[string]Ratio `json:"cheapWorkerByStratum"`
}

// ReviseSection is the premium REVISE rate on cheap-worker tasks (docs/evals.md s3.4).
type ReviseSection struct {
	CheapWorkerTasks   int    `json:"cheapWorkerTasks"`
	PremiumVerdicts    Ratio  `json:"premiumReviseVerdicts"`  // REVISE verdicts / premium verdicts
	TasksWithRevise    Ratio  `json:"tasksWithPremiumRevise"` // cheap-worker tasks with >= 1 premium REVISE / cheap-worker tasks with a premium verdict
	Escalated          Ratio  `json:"escalated"`              // cheap-worker tasks that escalated / cheap-worker tasks
	UnknownTierReviews int    `json:"unknownTierReviews"`     // agent verdicts whose reviewer has no usage rows: excluded
	HumanVerdicts      int    `json:"humanVerdicts"`          // human verdicts: excluded (they are not model reviews)
	Note               string `json:"note"`
}

// QualitySection: completion, test success, iterations to SHIP (docs/evals.md s3.7).
type QualitySection struct {
	Started            int   `json:"started"`
	Accepted           int   `json:"accepted"`
	Failed             int   `json:"failed"`
	Cancelled          int   `json:"cancelled"`
	Active             int   `json:"active"`
	Completion         Ratio `json:"completion"`         // accepted / started
	FirstCheckTestPass Ratio `json:"firstCheckTestPass"` // tasks whose first test evidence passed / tasks with test evidence
	FinalTestPass      Ratio `json:"finalTestPass"`      // accepted tasks whose last test evidence passed / accepted with test evidence
	IterationsToShip   Dist  `json:"iterationsToShip"`   // accepted tasks that an agent reviewer shipped
}

// ResumeSection: runs recovered after a restart versus failed (docs/evals.md s3.6).
type ResumeSection struct {
	Attempts    int    `json:"attempts"`  // runs that were recovered (workflow.recovering) or failed their recovery
	Recovered   int    `json:"recovered"` // later completed, or waiting for a human
	Failed      int    `json:"failed"`
	Pending     int    `json:"pending"`     // recovered and still running
	Cancelled   int    `json:"cancelled"`   // a human cancelled after recovery: not counted either way
	Reliability Ratio  `json:"reliability"` // recovered / (recovered + failed)
	Note        string `json:"note"`
}

// WatchdogSection: trips per accepted task and the false-trip rate (docs/evals.md s3.5).
type WatchdogSection struct {
	Trips       int            `json:"trips"`
	Stage1      int            `json:"stage1"`
	Stage2      int            `json:"stage2"`
	BySignal    map[string]int `json:"bySignal"`
	Warnings    int            `json:"warnings"` // soft stagnation warnings: not trips
	PerAccepted *float64       `json:"perAccepted,omitempty"`
	FalseTrips  int            `json:"falseTrips"` // a human reset or dismissed the trip within the same run (overridden, not judged false: see Note)
	FalseRate   Ratio          `json:"falseRate"`
	Note        string         `json:"note"`
}

// RoutingGroup is one (decided_by, tier, model) slice of decisions with an outcome.
type RoutingGroup struct {
	DecidedBy      string   `json:"decidedBy"`
	Tier           string   `json:"tier"`
	Provider       string   `json:"provider"`
	Model          string   `json:"model"`
	Decisions      int      `json:"decisions"`
	Accepted       int      `json:"accepted"`
	Escalated      int      `json:"escalated"`
	Failed         int      `json:"failed"`
	MeanIterations *float64 `json:"meanIterations,omitempty"`
}

// RoutingSection: routing quality from routing_decisions against routing_outcomes (docs/evals.md s3.7).
type RoutingSection struct {
	Decisions   int            `json:"decisions"`
	WithOutcome int            `json:"withOutcome"`
	Pending     int            `json:"pending"` // decisions whose task has not ended
	Good        Ratio          `json:"good"`    // decisions whose task was accepted without escalation / decisions with an outcome
	Fallbacks   int            `json:"fallbacks"`
	Escalations int            `json:"escalations"`
	Groups      []RoutingGroup `json:"groups"`
	Note        string         `json:"note"`
}

// TaskRow is one raw per-task row, so no aggregate stands without its data (docs/evals.md s9).
type TaskRow struct {
	ID            string `json:"id"`
	ProjectID     string `json:"projectId"`
	Seq           int    `json:"seq"`
	Workflow      string `json:"workflow"`
	Status        string `json:"status"`
	Accepted      bool   `json:"accepted"`
	Money         Money  `json:"money"`
	Tokens        Tokens `json:"tokens"`
	PremiumTokens Tokens `json:"premiumTokens"`
	PremiumTurns  int64  `json:"premiumTurns"`
	CheapWorker   string `json:"cheapWorker"`     // yes | no | unclassified
	StrictCheap   string `json:"strictCheapOnly"` // yes | no | unclassified
	Stratum       string `json:"stratum"`
	ItersToShip   int    `json:"iterationsToShip"`
	FirstTest     string `json:"firstTest"` // pass | fail | none | inconclusive
	Trips         int    `json:"watchdogTrips"`
	Escalated     bool   `json:"escalated"`
}

// Report is the full evaluation of a cohort.
type Report struct {
	GeneratedAt int64           `json:"generatedAt"`
	Options     Options         `json:"options"`
	Cost        CostSection     `json:"cost"`
	Premium     PremiumSection  `json:"premium"`
	Cheap       CheapSection    `json:"cheap"`
	Revise      ReviseSection   `json:"revise"`
	Quality     QualitySection  `json:"quality"`
	Resume      ResumeSection   `json:"resume"`
	Watchdog    WatchdogSection `json:"watchdog"`
	Routing     RoutingSection  `json:"routing"`
	Rows        []TaskRow       `json:"rows"`
	NotMeasured []string        `json:"notMeasured"`
}

// Build computes the report from the database.
func Build(ctx context.Context, db *storage.DB, o Options) (Report, error) {
	if o.MinN <= 0 {
		o.MinN = 5
	}
	until := o.UntilMS
	if until <= 0 {
		until = storage.Now() + 1
	}
	rep := Report{GeneratedAt: storage.Now(), Options: o}
	rep.Options.UntilMS = until

	// cohort
	rows, err := db.Reader().QueryContext(ctx, `SELECT id FROM tasks WHERE (?='' OR project_id=?) AND created_at<=? AND (CASE WHEN completed_at>0 THEN completed_at ELSE updated_at END)>=? ORDER BY created_at, id`,
		o.ProjectID, o.ProjectID, until, o.SinceMS)
	if err != nil {
		return rep, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return rep, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var facts []TaskFacts
	for _, id := range ids {
		f, err := Facts(ctx, db, id)
		if err != nil {
			return rep, err
		}
		facts = append(facts, f)
	}

	rep.computeQuality(facts)
	if err := rep.computeCost(ctx, db, facts, o, until); err != nil {
		return rep, err
	}
	rep.computePremium(facts)
	rep.computeCheap(facts)
	rep.computeRevise(facts)
	if err := rep.computeResume(ctx, db, o, until); err != nil {
		return rep, err
	}
	if err := rep.computeWatchdog(ctx, db, o, until); err != nil {
		return rep, err
	}
	if err := rep.computeRouting(ctx, db, o, until); err != nil {
		return rep, err
	}
	for _, f := range facts {
		rep.Rows = append(rep.Rows, row(f))
	}
	rep.NotMeasured = []string{
		"Premium quota consumed per accepted task (docs/evals.md s3.2c): no CLI rate-limit sample is stored, so there is no quota delta to report.",
		"Cost per accepted task of the naive broadcast baseline arms B0 and B1 (docs/evals.md s5): the baseline harness is not built, so no comparison or saving is stated.",
		"Duplicate effects, orphan processes and UI catch-up misses (docs/evals.md s3.6): measured by the engine acceptance tests and Windows checks, not recoverable from the database.",
		"Review verdicts that failed to parse: the pipeline stores an unparseable answer as REVISE, so 'invalid' cannot be told apart in task_reviews.",
	}
	return rep, nil
}

func row(f TaskFacts) TaskRow {
	yn := func(v, classified bool) string {
		switch {
		case !classified:
			return "unclassified"
		case v:
			return "yes"
		}
		return "no"
	}
	cw, cwOK := f.CheapWorker()
	st, stOK := f.StrictCheapOnly()
	ft := "none"
	if f.FirstTestPassed != nil {
		ft = map[bool]string{true: "pass", false: "fail"}[*f.FirstTestPassed]
	}
	return TaskRow{ID: f.ID, ProjectID: f.ProjectID, Seq: f.Seq, Workflow: f.Workflow, Status: f.Status, Accepted: f.Accepted, Money: f.Total, Tokens: f.Tokens,
		PremiumTokens: f.PremiumTokens, PremiumTurns: f.PremiumTurns, CheapWorker: yn(cw, cwOK), StrictCheap: yn(st, stOK), Stratum: f.Stratum,
		ItersToShip: f.ItersToShip, FirstTest: ft, Trips: f.Trips(), Escalated: f.Escalated}
}

func (r *Report) computeQuality(facts []TaskFacts) {
	q := &r.Quality
	var firstPass, firstN, finalPass, finalN int
	var iters []int
	for _, f := range facts {
		if f.Started > 0 {
			q.Started++
		}
		switch f.Status {
		case "COMPLETED":
			if f.Accepted {
				q.Accepted++
			}
		case "FAILED":
			q.Failed++
		case "CANCELLED":
			q.Cancelled++
		default:
			q.Active++
		}
		if f.FirstTestPassed != nil {
			firstN++
			if *f.FirstTestPassed {
				firstPass++
			}
		}
		if f.Accepted {
			if f.LastTestPassed != nil {
				finalN++
				if *f.LastTestPassed {
					finalPass++
				}
			}
			if f.ItersToShip > 0 {
				iters = append(iters, f.ItersToShip)
			}
		}
	}
	q.Completion = ratio(q.Accepted, q.Started)
	q.FirstCheckTestPass = ratio(firstPass, firstN)
	q.FinalTestPass = ratio(finalPass, finalN)
	q.IterationsToShip = dist(iters)
}

func (r *Report) computeCost(ctx context.Context, db *storage.DB, facts []TaskFacts, o Options, until int64) error {
	c := &r.Cost
	c.Overhead = map[string]Money{}
	tiers, roles, wfs := map[string]*Group{}, map[string]*Group{}, map[string]*Group{}
	wfAccepted := map[string]int{}
	for _, f := range facts {
		if f.Accepted {
			c.Accepted++
			wfAccepted[wfName(f.Workflow)]++
		}
	}
	grp := func(m map[string]*Group, name string, den int) *Group {
		g := m[name]
		if g == nil {
			g = &Group{Name: name, Accepted: den}
			m[name] = g
		}
		return g
	}
	for _, f := range facts {
		if !f.Accepted {
			continue
		}
		c.AcceptedOnly.add(f.Total)
		for _, s := range f.Slices {
			role := s.Role
			if role == "" {
				role = "(no agent)"
			}
			for _, x := range []*Group{grp(tiers, s.Tier, c.Accepted), grp(roles, role, c.Accepted), grp(wfs, wfName(f.Workflow), wfAccepted[wfName(f.Workflow)])} {
				x.Money.add(s.Money)
				x.Tokens.add(s.Tokens)
			}
		}
	}
	// every non-overhead usage row in the window, attributed or not
	q := `SELECT ` + budget.CostKindExpr("mu") + ` AS kind, (mu.task_id='') AS unattributed, COUNT(*), COALESCE(SUM(mu.cost_usd),0)
		FROM model_usage mu WHERE mu.ts>=? AND mu.ts<=? AND mu.purpose NOT IN (` + overheadList + `)
		AND (?='' OR mu.task_id IN (SELECT id FROM tasks WHERE project_id=?) OR (mu.task_id='' AND mu.agent_id IN (SELECT id FROM agents WHERE project_id=?)))
		GROUP BY kind, unattributed`
	rows, err := db.Reader().QueryContext(ctx, q, o.SinceMS, until, o.ProjectID, o.ProjectID, o.ProjectID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var kind string
		var un int
		var n int64
		var cost float64
		if err := rows.Scan(&kind, &un, &n, &cost); err != nil {
			rows.Close()
			return err
		}
		m := Money{Rows: n}
		switch kind {
		case budget.CostReported:
			m.ReportedUSD = cost
		case budget.CostEstimated:
			m.EstimatedUSD = cost
		default:
			m.UnknownRows = n
		}
		c.AllWindow.add(m)
		if un == 1 {
			c.Unattributed.add(m)
		}
	}
	rows.Close()
	orows, err := db.Reader().QueryContext(ctx, `SELECT mu.purpose, `+budget.CostKindExpr("mu")+` AS kind, COUNT(*), COALESCE(SUM(mu.cost_usd),0) FROM model_usage mu
		WHERE mu.ts>=? AND mu.ts<=? AND mu.purpose IN (`+overheadList+`) GROUP BY mu.purpose, kind`, o.SinceMS, until)
	if err != nil {
		return err
	}
	for orows.Next() {
		var purpose, kind string
		var n int64
		var cost float64
		if err := orows.Scan(&purpose, &kind, &n, &cost); err != nil {
			orows.Close()
			return err
		}
		m := c.Overhead[purpose]
		m.Rows += n
		switch kind {
		case budget.CostReported:
			m.ReportedUSD += cost
		case budget.CostEstimated:
			m.EstimatedUSD += cost
		default:
			m.UnknownRows += n
		}
		c.Overhead[purpose] = m
	}
	orows.Close()

	per := func(m Money, den int) *Money {
		if den == 0 {
			return nil
		}
		d := float64(den)
		return &Money{ReportedUSD: m.ReportedUSD / d, EstimatedUSD: m.EstimatedUSD / d, UnknownRows: m.UnknownRows, Rows: m.Rows}
	}
	if c.Accepted >= 1 {
		c.PerAcceptedAll, c.PerAcceptedOnly = per(c.AllWindow, c.Accepted), per(c.AcceptedOnly, c.Accepted)
	}
	finish := func(m map[string]*Group) []Group {
		var out []Group
		for _, g := range m {
			if g.Accepted >= r.Options.MinN {
				g.PerAccepted = per(g.Money, g.Accepted)
			}
			out = append(out, *g)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	}
	c.ByTier, c.ByRole, c.ByWorkflow = finish(tiers), finish(roles), finish(wfs)
	c.Note = "Unknown rows are calls whose cost no CLI or provider reported and no price table covers; they are counted, never priced at zero, so every dollar figure is a lower bound when unknown rows exist. Reported and estimated dollars are shown apart and never summed into one figure."
	return nil
}

func wfName(w string) string {
	if w == "" {
		return "(no workflow run)"
	}
	return w
}

func (r *Report) computePremium(facts []TaskFacts) {
	p := &r.Premium
	by := map[string]*ProviderTokens{}
	for _, f := range facts {
		if !f.Accepted {
			continue
		}
		p.Accepted++
		p.Turns += f.PremiumTurns
		p.Calls += f.PremiumCalls
		for _, s := range f.Slices {
			if s.Tier != "premium" {
				continue
			}
			x := by[s.Provider]
			if x == nil {
				x = &ProviderTokens{Provider: s.Provider}
				by[s.Provider] = x
			}
			x.Tokens.add(s.Tokens)
			x.Money.add(s.Money)
		}
	}
	for _, x := range by {
		p.ByProvider = append(p.ByProvider, *x)
	}
	sort.Slice(p.ByProvider, func(i, j int) bool { return p.ByProvider[i].Provider < p.ByProvider[j].Provider })
	if p.Accepted > 0 {
		v := float64(p.Turns) / float64(p.Accepted)
		p.TurnsPerAccepted = &v
	}
	p.QuotaDelta = "not available: no CLI rate-limit sample is stored (docs/evals.md s3.2c)"
}

func (r *Report) computeCheap(facts []TaskFacts) {
	c := &r.Cheap
	c.Strata = map[string]int{}
	c.CheapWorkerByStratum = map[string]Ratio{}
	var cw, cwDen, st, stDen int
	byStratum := map[string][2]int{}
	for _, f := range facts {
		if !f.Accepted {
			continue
		}
		c.Strata[f.Stratum]++
		cheap, ok := f.CheapWorker()
		strict, sok := f.StrictCheapOnly()
		if !ok {
			c.Unclassified++
		} else {
			cwDen++
			x := byStratum[f.Stratum]
			x[1]++
			if cheap {
				cw++
				x[0]++
			}
			byStratum[f.Stratum] = x
		}
		if sok {
			stDen++
			if strict {
				st++
			}
		}
	}
	c.CheapWorker, c.StrictCheapOnly = ratio(cw, cwDen), ratio(st, stDen)
	for k, v := range byStratum {
		c.CheapWorkerByStratum[k] = ratio(v[0], v[1])
	}
}

func (r *Report) computeRevise(facts []TaskFacts) {
	v := &r.Revise
	var prem, premRevise, withVerdict, withRevise, esc int
	for _, f := range facts {
		cheap, ok := f.CheapWorker()
		// a task that escalated is no longer cheap-worker by definition, but its premium verdicts are exactly what this
		// metric is about: classify by the worker's tier alone
		workerCheap := ok && (cheap || (f.AssigneeCheap && f.Escalated))
		if !workerCheap {
			continue
		}
		v.CheapWorkerTasks++
		if f.Escalated {
			esc++
		}
		had, hadRevise := false, false
		for _, rv := range f.Reviews {
			switch {
			case rv.Human:
				v.HumanVerdicts++
			case rv.Premium == nil:
				v.UnknownTierReviews++
			case *rv.Premium:
				prem++
				had = true
				if rv.Verdict == "REVISE" {
					premRevise++
					hadRevise = true
				}
			}
		}
		if had {
			withVerdict++
		}
		if hadRevise {
			withRevise++
		}
	}
	v.PremiumVerdicts = ratio(premRevise, prem)
	v.TasksWithRevise = ratio(withRevise, withVerdict)
	v.Escalated = ratio(esc, v.CheapWorkerTasks)
	v.Note = "A premium verdict is one by a non-human reviewer whose usage rows on the task include premium-tier rows. Verdicts are SHIP or REVISE as stored in task_reviews (an unparseable answer is stored as REVISE)."
}

func (r *Report) computeResume(ctx context.Context, db *storage.DB, o Options, until int64) error {
	s := &r.Resume
	rows, err := db.Reader().QueryContext(ctx, `SELECT w.id, w.status, w.failed_reason FROM workflow_runs w WHERE (?='' OR w.project_id=? OR w.task_id IN (SELECT id FROM tasks WHERE project_id=?)) AND
		(w.id IN (SELECT run_id FROM events WHERE type='workflow.recovering' AND ts>=? AND ts<=? AND run_id<>'') OR (w.status='FAILED' AND w.failed_reason LIKE 'recover:%' AND w.updated_at>=? AND w.updated_at<=?))`,
		o.ProjectID, o.ProjectID, o.ProjectID, o.SinceMS, until, o.SinceMS, until)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, status, reason string
		if err := rows.Scan(&id, &status, &reason); err != nil {
			return err
		}
		s.Attempts++
		switch status {
		case "COMPLETED", "WAITING_HUMAN":
			s.Recovered++
		case "FAILED":
			s.Failed++
		case "CANCELLED":
			s.Cancelled++
		default:
			s.Pending++
		}
	}
	s.Reliability = ratio(s.Recovered, s.Recovered+s.Failed)
	s.Note = "A run counts as recovered when the engine resumed it after a restart (workflow.recovering) and it later completed or is waiting for a human; failed when it ended FAILED (including a failed recovery). Runs a human cancelled and runs still running are not counted either way."
	return rows.Err()
}

func (r *Report) computeWatchdog(ctx context.Context, db *storage.DB, o Options, until int64) error {
	w := &r.Watchdog
	w.BySignal = map[string]int{}
	rows, err := db.Reader().QueryContext(ctx, `SELECT t.id, t.signal, t.stage, t.task_id, t.run_id, t.created_at,
		EXISTS(SELECT 1 FROM room_inbox_acks k WHERE k.item_key='watchdog:'||t.id AND k.acked_by LIKE 'human:%') AS dismissed,
		EXISTS(SELECT 1 FROM watchdog_trips r WHERE r.task_id=t.task_id AND r.signal='reset' AND r.id>t.id
		       AND r.created_at < COALESCE((SELECT MIN(w2.created_at) FROM workflow_runs w2 WHERE w2.task_id=t.task_id AND t.run_id<>''
		                                    AND w2.created_at > COALESCE((SELECT w1.created_at FROM workflow_runs w1 WHERE w1.id=t.run_id),0)), `+maxInt+`)) AS reset
		FROM watchdog_trips t WHERE t.signal NOT IN ('reset') AND t.created_at>=? AND t.created_at<=?
		AND (?='' OR t.task_id IN (SELECT id FROM tasks WHERE project_id=?))`, o.SinceMS, until, o.ProjectID, o.ProjectID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var sig, task, run string
		var stage int
		var at int64
		var dismissed, reset int
		if err := rows.Scan(&id, &sig, &stage, &task, &run, &at, &dismissed, &reset); err != nil {
			rows.Close()
			return err
		}
		if sig == "stagnation" {
			w.Warnings++
			continue
		}
		w.Trips++
		w.BySignal[sig]++
		if stage >= 2 {
			w.Stage2++
		} else {
			w.Stage1++
		}
		if dismissed == 1 || reset == 1 {
			w.FalseTrips++
		}
	}
	rows.Close()
	acc := r.Quality.Accepted
	if acc > 0 {
		v := float64(w.Trips) / float64(acc)
		w.PerAccepted = &v
	}
	w.FalseRate = ratio(w.FalseTrips, w.Trips)
	w.Note = "falseTrips counts trips a person let go: a watchdog 'reset' row after the trip, before the task's next workflow run began, or a dismissed Needs-you item. That is not a judgement that the trip was wrong: answering a pause to let a genuinely stuck agent try again resets it too, so it can count real trips, and a trip nobody acted on counts in neither direction. No person is asked whether a trip was real, so no false-trip rate is measured; read this as the share of trips people overrode. Stage-1 trips on adapters without tool-call visibility run reduced detection and are not comparable."
	return rows.Err()
}

const maxInt = "9223372036854775807"

func (r *Report) computeRouting(ctx context.Context, db *storage.DB, o Options, until int64) error {
	g := &r.Routing
	if err := db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM routing_decisions d WHERE d.ts>=? AND d.ts<=? AND (?='' OR d.task_id IN (SELECT id FROM tasks WHERE project_id=?))`,
		o.SinceMS, until, o.ProjectID, o.ProjectID).Scan(&g.Decisions); err != nil {
		return err
	}
	rows, err := db.Reader().QueryContext(ctx, `SELECT d.decided_by, COALESCE(json_extract(d.decision,'$.Facts.Tier'),''), d.provider_id, d.model, COUNT(*),
		COALESCE(SUM(oc.accepted),0), COALESCE(SUM(oc.escalated),0), COALESCE(SUM(oc.outcome='failed'),0), AVG(oc.iterations)
		FROM routing_decisions d JOIN routing_outcomes oc ON oc.decision_id=d.id
		WHERE d.ts>=? AND d.ts<=? AND (?='' OR d.task_id IN (SELECT id FROM tasks WHERE project_id=?))
		GROUP BY d.decided_by, 2, d.provider_id, d.model ORDER BY d.decided_by, 2, d.provider_id, d.model`, o.SinceMS, until, o.ProjectID, o.ProjectID)
	if err != nil {
		return err
	}
	var good int
	for rows.Next() {
		var x RoutingGroup
		var mean sql.NullFloat64
		if err := rows.Scan(&x.DecidedBy, &x.Tier, &x.Provider, &x.Model, &x.Decisions, &x.Accepted, &x.Escalated, &x.Failed, &mean); err != nil {
			rows.Close()
			return err
		}
		if mean.Valid {
			v := mean.Float64
			x.MeanIterations = &v
		}
		g.WithOutcome += x.Decisions
		g.Groups = append(g.Groups, x)
	}
	rows.Close()
	if err := db.Reader().QueryRowContext(ctx, `SELECT COALESCE(SUM(oc.accepted=1 AND oc.escalated=0),0) FROM routing_decisions d JOIN routing_outcomes oc ON oc.decision_id=d.id
		WHERE d.ts>=? AND d.ts<=? AND (?='' OR d.task_id IN (SELECT id FROM tasks WHERE project_id=?))`, o.SinceMS, until, o.ProjectID, o.ProjectID).Scan(&good); err != nil {
		return err
	}
	g.Pending = g.Decisions - g.WithOutcome
	g.Good = ratio(good, g.WithOutcome)
	_ = db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE type='routing.fallback' AND ts>=? AND ts<=?`, o.SinceMS, until).Scan(&g.Fallbacks)
	_ = db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE type='routing.escalated' AND ts>=? AND ts<=?`, o.SinceMS, until).Scan(&g.Escalations)
	g.Note = "A decision is good when its task was accepted by a human and never escalated (no routing.escalated event, no second watchdog trip); it is bad when the task failed, was cancelled or escalated. Decisions of tasks that have not ended are pending. Turns of an agent with no list of models the router may choose between are 'static' decisions (the agent's own configured assignment): for those, this section measures the configured assignments, not router choices."
	return nil
}

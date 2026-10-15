// Package evals computes the evaluation metrics of brief s23 and docs/evals.md from the real database. Nothing here is
// estimated or invented: every number is a count or sum over rows that exist, a metric with no data says so (n/a, n=0)
// instead of printing zero, and cost is always split by source (reported, estimated, unknown) so the sources are never
// mixed silently (brief s7).
//
// docs/evals.md section 11 defines each metric exactly as computed here.
package evals

import (
	"context"
	"database/sql"
	"strings"

	"github.com/kjmaguire/drydock/internal/budget"
	"github.com/kjmaguire/drydock/internal/storage"
)

// OverheadPurposes are model_usage purposes that are product-adjacent measurement or setup traffic, excluded from cost per
// accepted task and listed apart (docs/evals.md s3.9).
var OverheadPurposes = []string{"connection_test", "conformance", "tool_call_probe", "eval_baseline_setup", "pricing_probe"}

const overheadList = `'connection_test','conformance','tool_call_probe','eval_baseline_setup','pricing_probe'`

// tierExpr resolves the tier of a usage row. The agent runtime stamps every CLI row with the tier its caller passed (today
// every agent is passed as premium); an agent that runs on a registered provider is the provider's tier. So: the tier of
// the agent's registered provider when it has one, else the row's own tier.
const tierExpr = `COALESCE((SELECT p.tier FROM agents ag JOIN providers p ON p.id=ag.provider_id WHERE ag.id=mu.agent_id AND ag.provider_id<>''), NULLIF(mu.tier,''), 'unknown')`

// Money is cost split by source. Unknown rows are counted, never priced at zero.
type Money struct {
	ReportedUSD  float64 `json:"reportedUsd"`
	EstimatedUSD float64 `json:"estimatedUsd"`
	UnknownRows  int64   `json:"unknownRows"`
	Rows         int64   `json:"rows"`
}

// Known is reported plus estimated; a lower bound when UnknownRows > 0.
func (m Money) Known() float64 { return m.ReportedUSD + m.EstimatedUSD }

// Mixed reports that both reported and estimated cost are present (the report prints the parts, never one merged figure).
func (m Money) Mixed() bool { return m.ReportedUSD > 0 && m.EstimatedUSD > 0 }

func (m *Money) add(o Money) {
	m.ReportedUSD += o.ReportedUSD
	m.EstimatedUSD += o.EstimatedUSD
	m.UnknownRows += o.UnknownRows
	m.Rows += o.Rows
}

// Tokens as recorded in model_usage.
type Tokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Cached     int64 `json:"cached"`
	CacheWrite int64 `json:"cacheWrite"`
}

func (t *Tokens) add(o Tokens) {
	t.Input += o.Input
	t.Output += o.Output
	t.Cached += o.Cached
	t.CacheWrite += o.CacheWrite
}

// Total is input plus output.
func (t Tokens) Total() int64 { return t.Input + t.Output }

// Slice is the usage of one (tier, role, provider) combination of a task.
type Slice struct {
	Tier     string `json:"tier"`
	Role     string `json:"role"`
	Provider string `json:"provider"`
	AgentID  string `json:"agentId,omitempty"`
	Money    Money  `json:"money"`
	Tokens   Tokens `json:"tokens"`
}

// Review is a verdict on a task.
type Review struct {
	Attempt  int
	Reviewer string
	Verdict  string
	Premium  *bool // nil: the reviewer's tier is unknown (no usage rows)
	Human    bool
}

// TaskFacts is everything the metrics need about one task, read from the database.
type TaskFacts struct {
	ID, ProjectID string
	Seq           int
	Status        string
	AcceptedBy    string
	Accepted      bool // COMPLETED through a human: identity
	Started       int64
	Completed     int64
	Updated       int64
	Workflow      string
	Assignee      string
	Reviewer      string
	Iteration     int

	Slices []Slice
	Total  Money
	Tokens Tokens

	PremiumTokens Tokens
	PremiumMoney  Money
	PremiumTurns  int64 // distinct agent runs with premium usage
	PremiumCalls  int64 // premium usage rows with no agent run (app-owned LLM calls)

	AssigneeRows   int64
	AssigneeCheap  bool // every assignee row is cheap tier
	AnyPremiumRows bool
	Escalated      bool

	Reviews          []Review
	ItersToShip      int // reviews up to and including the first SHIP by a non-human reviewer; 0 = never shipped by an agent reviewer
	FirstTestPassed  *bool
	LastTestPassed   *bool
	ChecksAllPassing *bool // latest evidence of each kind passed; nil = no check evidence

	TripsStage1, TripsStage2, Warnings int
	Decisions                          []string
	Stratum                            string // premium_only | general | unclassified
}

// Trips is all hard watchdog trips on the task.
func (t TaskFacts) Trips() int { return t.TripsStage1 + t.TripsStage2 }

// CheapWorker: the task was worked only on cheap-tier models and never escalated. Unclassified (false, false) when the
// assignee has no usage rows.
func (t TaskFacts) CheapWorker() (cheap, classified bool) {
	if t.AssigneeRows == 0 {
		return false, false
	}
	return t.AssigneeCheap && !t.Escalated, true
}

// StrictCheapOnly: not one premium-tier row on the task, review included.
func (t TaskFacts) StrictCheapOnly() (strict, classified bool) {
	if t.Total.Rows == 0 {
		return false, false
	}
	return !t.AnyPremiumRows, true
}

// Facts reads one task's facts.
func Facts(ctx context.Context, db *storage.DB, taskID string) (TaskFacts, error) {
	f := TaskFacts{ID: taskID}
	r := db.Reader()
	err := r.QueryRowContext(ctx, `SELECT t.project_id,t.seq,t.status,t.accepted_by,t.started_at,t.completed_at,t.updated_at,t.assignee,t.reviewer,t.iteration,
		COALESCE((SELECT w.workflow FROM workflow_runs w WHERE w.id=t.run_id AND t.run_id<>''),(SELECT w.workflow FROM workflow_runs w WHERE w.task_id=t.id ORDER BY w.created_at DESC LIMIT 1),'')
		FROM tasks t WHERE t.id=?`, taskID).Scan(&f.ProjectID, &f.Seq, &f.Status, &f.AcceptedBy, &f.Started, &f.Completed, &f.Updated, &f.Assignee, &f.Reviewer, &f.Iteration, &f.Workflow)
	if err != nil {
		return f, err
	}
	f.Accepted = f.Status == "COMPLETED" && strings.HasPrefix(f.AcceptedBy, "human:")

	// usage, split by tier / role / provider / agent / cost source
	rows, err := r.QueryContext(ctx, `SELECT `+tierExpr+` AS tier, COALESCE(ag.role,'') AS role, mu.provider, mu.agent_id, `+budget.CostKindExpr("mu")+` AS kind,
		COUNT(*), COALESCE(SUM(mu.cost_usd),0), COALESCE(SUM(mu.input_tokens),0), COALESCE(SUM(mu.output_tokens),0), COALESCE(SUM(mu.cached_tokens),0), COALESCE(SUM(mu.cache_write_tokens),0)
		FROM model_usage mu LEFT JOIN agents ag ON ag.id=mu.agent_id WHERE mu.task_id=? AND mu.purpose NOT IN (`+overheadList+`)
		GROUP BY tier, role, mu.provider, mu.agent_id, kind`, taskID)
	if err != nil {
		return f, err
	}
	idx := map[[4]string]int{}
	assigneeTiers := map[string]bool{}
	for rows.Next() {
		var tier, role, prov, agent, kind string
		var n int64
		var cost float64
		var tk Tokens
		if err := rows.Scan(&tier, &role, &prov, &agent, &kind, &n, &cost, &tk.Input, &tk.Output, &tk.Cached, &tk.CacheWrite); err != nil {
			rows.Close()
			return f, err
		}
		key := [4]string{tier, role, prov, agent}
		i, ok := idx[key]
		if !ok {
			i = len(f.Slices)
			idx[key] = i
			f.Slices = append(f.Slices, Slice{Tier: tier, Role: role, Provider: prov, AgentID: agent})
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
		f.Slices[i].Money.add(m)
		f.Slices[i].Tokens.add(tk)
		f.Total.add(m)
		f.Tokens.add(tk)
		if tier == "premium" {
			f.AnyPremiumRows = true
			f.PremiumMoney.add(m)
			f.PremiumTokens.add(tk)
		}
		if agent != "" && agent == f.Assignee {
			f.AssigneeRows += n
			assigneeTiers[tier] = true
		}
	}
	rows.Close()
	f.AssigneeCheap = f.AssigneeRows > 0 && len(assigneeTiers) == 1 && assigneeTiers["cheap"]
	if err := r.QueryRowContext(ctx, `SELECT COUNT(DISTINCT mu.agent_run_id) FROM model_usage mu WHERE mu.task_id=? AND mu.agent_run_id<>'' AND mu.purpose NOT IN (`+overheadList+`) AND `+tierExpr+`='premium'`, taskID).Scan(&f.PremiumTurns); err != nil {
		return f, err
	}
	if err := r.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_usage mu WHERE mu.task_id=? AND mu.agent_run_id='' AND mu.purpose NOT IN (`+overheadList+`) AND `+tierExpr+`='premium'`, taskID).Scan(&f.PremiumCalls); err != nil {
		return f, err
	}

	// reviews: a reviewer is premium when any usage row of that agent on this task is premium tier
	rr, err := r.QueryContext(ctx, `SELECT tr.attempt, tr.reviewer, tr.verdict FROM task_reviews tr WHERE tr.task_id=? ORDER BY tr.id`, taskID)
	if err != nil {
		return f, err
	}
	for rr.Next() {
		var v Review
		if err := rr.Scan(&v.Attempt, &v.Reviewer, &v.Verdict); err != nil {
			rr.Close()
			return f, err
		}
		v.Human = strings.HasPrefix(v.Reviewer, "human:")
		f.Reviews = append(f.Reviews, v)
	}
	rr.Close()
	for i := range f.Reviews {
		v := &f.Reviews[i]
		if v.Human {
			continue
		}
		var n, prem int
		if err := r.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(`+tierExpr+`='premium'),0) FROM model_usage mu WHERE mu.task_id=? AND mu.agent_id=? AND mu.purpose NOT IN (`+overheadList+`)`, taskID, v.Reviewer).Scan(&n, &prem); err != nil {
			return f, err
		}
		if n > 0 {
			p := prem > 0
			v.Premium = &p
		}
	}
	count := 0
	for _, v := range f.Reviews {
		if v.Human {
			continue
		}
		count++
		if v.Verdict == "SHIP" {
			f.ItersToShip = count
			break
		}
	}

	// evidence: the first and last test result, and whether the latest result of every kind passed
	ev, err := r.QueryContext(ctx, `SELECT kind, passed FROM task_evidence WHERE task_id=? ORDER BY id`, taskID)
	if err != nil {
		return f, err
	}
	latest := map[string]*bool{}
	for ev.Next() {
		var kind string
		var p sql.NullInt64
		if err := ev.Scan(&kind, &p); err != nil {
			ev.Close()
			return f, err
		}
		var b *bool
		if p.Valid {
			v := p.Int64 != 0
			b = &v
		}
		if kind == "test" {
			if f.FirstTestPassed == nil {
				f.FirstTestPassed = b
			}
			f.LastTestPassed = b
		}
		if kind == "build" || kind == "test" || kind == "lint" || kind == "typecheck" || kind == "secrets" || kind == "deps" {
			latest[kind] = b
		}
	}
	ev.Close()
	if len(latest) > 0 {
		all, any := true, false
		for _, b := range latest {
			if b == nil {
				continue // an inconclusive check is neither a pass nor a fail
			}
			any = true
			all = all && *b
		}
		if any {
			f.ChecksAllPassing = &all
		}
	}

	// watchdog: hard trips by stage; stagnation is a warning, reset is the human resuming
	wr, err := r.QueryContext(ctx, `SELECT signal, stage, COUNT(*) FROM watchdog_trips WHERE task_id=? GROUP BY signal, stage`, taskID)
	if err != nil {
		return f, err
	}
	for wr.Next() {
		var sig string
		var stage, n int
		if err := wr.Scan(&sig, &stage, &n); err != nil {
			wr.Close()
			return f, err
		}
		switch {
		case sig == "reset":
		case sig == "stagnation":
			f.Warnings += n
		case stage >= 2:
			f.TripsStage2 += n
		default:
			f.TripsStage1 += n
		}
	}
	wr.Close()
	var esc int
	if err := r.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE task_id=? AND type='routing.escalated'`, taskID).Scan(&esc); err != nil {
		return f, err
	}
	f.Escalated = esc > 0 || f.TripsStage2 > 0

	// routing decisions and the task's stratum (docs/model-routing.md s7 premium-only classes). The stratum is the class of
	// the task's own work and review turns: the other turns of the task (the template roles' spec, tests, design and
	// security review, P63) carry the class of that turn, not of the task, and do not classify it. They share the task's
	// outcome, as turns of its work. A room reply is not: what the task came to says nothing about a chat answer, so its
	// decision gets no outcome (and the routing report leaves replies out, report.go).
	dr, err := r.QueryContext(ctx, `SELECT id, COALESCE(json_extract(request,'$.TaskType'),''), COALESCE(json_extract(request,'$.Risk'),0), run_kind IN ('agent_task','review','')
		FROM routing_decisions WHERE task_id=? AND run_kind<>'room_reply' ORDER BY ts, id`, taskID)
	if err != nil {
		return f, err
	}
	f.Stratum = "unclassified"
	for dr.Next() {
		var id, tt string
		var risk int
		var taskTurn bool
		if err := dr.Scan(&id, &tt, &risk, &taskTurn); err != nil {
			dr.Close()
			return f, err
		}
		f.Decisions = append(f.Decisions, id)
		if !taskTurn {
			continue
		}
		if tt == "architecture" || tt == "security_review" || risk != 0 {
			f.Stratum = "premium_only"
		} else if tt != "" && f.Stratum != "premium_only" {
			f.Stratum = "general"
		}
	}
	dr.Close()
	return f, nil
}

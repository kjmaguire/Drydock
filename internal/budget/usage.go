package budget

import (
	"context"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/storage"
)

// CostKinds are the three honest answers to "what did this row cost".
const (
	CostReported  = "reported"
	CostEstimated = "estimated"
	CostUnknown   = "unknown"
)

// costKind is the one classification of a model_usage row's cost, shared by budgets and evals so they can never disagree.
//
//   - cost_source 'reported' (the provider returned a cost) and 'computed_from_table' (we priced the tokens) are explicit.
//   - Rows written by the agent runtime leave cost_source at its default 'unknown' and carry the CLI's own figure in
//     cost_usd with source reported|estimated. A positive figure is therefore the CLI's number; a zero there means the CLI
//     did not report a cost, which is unknown, not free.
//   - A cache hit costs nothing by construction (estimated, known zero).
const costKind = `CASE
  WHEN {a}cost_source='reported' THEN 'reported'
  WHEN {a}cost_source='computed_from_table' THEN 'estimated'
  WHEN {a}cost_usd>0 AND {a}source='estimated' THEN 'estimated'
  WHEN {a}cost_usd>0 THEN 'reported'
  WHEN {a}cache_hit=1 THEN 'estimated'
  ELSE 'unknown' END`

// CostKindExpr returns the SQL expression that classifies a model_usage row; alias is the table alias ("mu") or "".
func CostKindExpr(alias string) string {
	if alias != "" && !strings.HasSuffix(alias, ".") {
		alias += "."
	}
	return strings.ReplaceAll(costKind, "{a}", alias)
}

// Spend is what a scope has used, by source.
type Spend struct {
	Rows            int64   `json:"rows"`
	ReportedUSD     float64 `json:"reportedUsd"`
	EstimatedUSD    float64 `json:"estimatedUsd"`
	UnknownCostRows int64   `json:"unknownCostRows"` // rows with no reported cost and no price: NOT counted as zero
	InputTokens     int64   `json:"inputTokens"`
	OutputTokens    int64   `json:"outputTokens"`
	CachedTokens    int64   `json:"cachedTokens"`
	Turns           int64   `json:"turns"`
}

// KnownUSD is reported plus estimated cost. It is a lower bound when UnknownCostRows > 0.
func (s Spend) KnownUSD() float64 { return s.ReportedUSD + s.EstimatedUSD }

// Tokens is input plus output tokens as recorded.
func (s Spend) Tokens() int64 { return s.InputTokens + s.OutputTokens }

// Mixed reports that the dollar figure combines reported and estimated parts (the UI badges it).
func (s Spend) Mixed() bool { return s.ReportedUSD > 0 && s.EstimatedUSD > 0 }

const maxInt64 = "9223372036854775807"

// usageWhere is the model_usage predicate (alias mu) for a scope instance. Runs: the agent runtime writes no run id, so
// its rows belong to a run through the task and the time window between the run's start and the next run of the task.
func usageWhere(scope Scope, id string) (string, []any, error) {
	switch scope {
	case ScopeProject:
		return `(mu.task_id IN (SELECT id FROM tasks WHERE project_id=?) OR (mu.task_id='' AND mu.agent_id IN (SELECT id FROM agents WHERE project_id=?)))`, []any{id, id}, nil
	case ScopeTask:
		return `mu.task_id=?`, []any{id}, nil
	case ScopeAgent:
		return `mu.agent_id=?`, []any{id}, nil
	case ScopeRun:
		return `(mu.run_id=? OR (mu.run_id='' AND mu.task_id<>'' AND mu.task_id=(SELECT r.task_id FROM workflow_runs r WHERE r.id=?)
			AND mu.ts>=(SELECT r.created_at FROM workflow_runs r WHERE r.id=?)
			AND mu.ts<COALESCE((SELECT MIN(r2.created_at) FROM workflow_runs r2 WHERE r2.task_id=(SELECT r.task_id FROM workflow_runs r WHERE r.id=?)
				AND r2.created_at>(SELECT r.created_at FROM workflow_runs r WHERE r.id=?)), ` + maxInt64 + `)))`, []any{id, id, id, id, id}, nil
	case ScopeWorkflow:
		// every run of the workflow (in the budget's project), each counted as the run scope counts it
		p, wf := workflowInstance(id)
		return `EXISTS (SELECT 1 FROM workflow_runs r WHERE r.workflow=? AND (?='' OR r.project_id=?) AND (mu.run_id=r.id
			OR (mu.run_id='' AND mu.task_id<>'' AND mu.task_id=r.task_id AND mu.ts>=r.created_at
				AND mu.ts<COALESCE((SELECT MIN(r2.created_at) FROM workflow_runs r2 WHERE r2.task_id=r.task_id AND r2.created_at>r.created_at), ` + maxInt64 + `))))`,
			[]any{wf, p, p}, nil
	}
	return "", nil, fmt.Errorf("budget: unknown scope %q", scope)
}

// turnsWhere is the agent_runs predicate (alias ar) for a scope instance.
func turnsWhere(scope Scope, id string) (string, []any, error) {
	switch scope {
	case ScopeProject:
		return `(ar.task_id IN (SELECT id FROM tasks WHERE project_id=?) OR (ar.task_id='' AND ar.agent_id IN (SELECT id FROM agents WHERE project_id=?)))`, []any{id, id}, nil
	case ScopeTask:
		return `ar.task_id=?`, []any{id}, nil
	case ScopeAgent:
		return `ar.agent_id=?`, []any{id}, nil
	case ScopeRun:
		return `(ar.task_id<>'' AND ar.task_id=(SELECT r.task_id FROM workflow_runs r WHERE r.id=?)
			AND ar.started_at>=(SELECT r.created_at FROM workflow_runs r WHERE r.id=?)
			AND ar.started_at<COALESCE((SELECT MIN(r2.created_at) FROM workflow_runs r2 WHERE r2.task_id=(SELECT r.task_id FROM workflow_runs r WHERE r.id=?)
				AND r2.created_at>(SELECT r.created_at FROM workflow_runs r WHERE r.id=?)), ` + maxInt64 + `))`, []any{id, id, id, id}, nil
	case ScopeWorkflow:
		p, wf := workflowInstance(id)
		return `EXISTS (SELECT 1 FROM workflow_runs r WHERE r.workflow=? AND (?='' OR r.project_id=?) AND ar.task_id<>'' AND ar.task_id=r.task_id
			AND ar.started_at>=r.created_at
			AND ar.started_at<COALESCE((SELECT MIN(r2.created_at) FROM workflow_runs r2 WHERE r2.task_id=r.task_id AND r2.created_at>r.created_at), ` + maxInt64 + `))`,
			[]any{wf, p, p}, nil
	}
	return "", nil, fmt.Errorf("budget: unknown scope %q", scope)
}

// Usage sums what a scope instance has used. Everything comes from model_usage and agent_runs; nothing is estimated here.
func Usage(ctx context.Context, db *storage.DB, scope Scope, id string) (Spend, error) {
	var s Spend
	w, args, err := usageWhere(scope, id)
	if err != nil {
		return s, err
	}
	q := `SELECT COUNT(*),
		COALESCE(SUM(CASE k WHEN 'reported' THEN cost END),0), COALESCE(SUM(CASE k WHEN 'estimated' THEN cost END),0),
		COALESCE(SUM(CASE k WHEN 'unknown' THEN 1 END),0),
		COALESCE(SUM(i),0), COALESCE(SUM(o),0), COALESCE(SUM(c),0)
		FROM (SELECT mu.cost_usd AS cost, mu.input_tokens AS i, mu.output_tokens AS o, mu.cached_tokens AS c, ` + CostKindExpr("mu") + ` AS k
		      FROM model_usage mu WHERE ` + w + `)`
	if err := db.Reader().QueryRowContext(ctx, q, args...).Scan(&s.Rows, &s.ReportedUSD, &s.EstimatedUSD, &s.UnknownCostRows, &s.InputTokens, &s.OutputTokens, &s.CachedTokens); err != nil {
		return s, err
	}
	tw, targs, err := turnsWhere(scope, id)
	if err != nil {
		return s, err
	}
	if err := db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_runs ar WHERE `+tw, targs...).Scan(&s.Turns); err != nil {
		return s, err
	}
	return s, nil
}

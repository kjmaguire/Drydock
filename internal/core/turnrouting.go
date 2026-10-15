package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/budget"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/router"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The model router in the path of agent turns (docs/model-routing.md). An agent whose human listed the models the router may
// choose between (Agent.RouteModels, each cheap or premium) is routed on every work and review turn: the rule router chooses
// among exactly those models, never another adapter or provider, so a turn always runs on the agent's own CLI. The level
// ladder and the stored rules decide, with the failure counters of the run (cheap work turns that came back, premium work
// that the reviewer sent back) driving escalation to premium. The chosen model, its tier and the decision reach the node
// through the context (pipeline.RoutedModel). An agent with no list keeps its configured model, recorded as a static
// decision; if the router cannot choose, the configured model runs and the decision says why.

// routeTurn replaces the static decision recorder in the agent_task middleware chain. A routed turn whose model or provider
// turns out to be unavailable before the turn did any work moves along the fallback chain a routing rule names (fallback.go).
func (e *economics) routeTurn(next workflow.NodeHandler) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		if in.TaskID == "" {
			return next(ctx, in)
		}
		rctx, rt := e.decideTurn(ctx, in)
		if rt == nil {
			return next(rctx, in)
		}
		return e.runWithFallback(ctx, rctx, in, next, rt)
	}
}

// routedTurn is what a fallback hop needs of the turn's routing: the request, the decision the turn runs on, the agent.
type routedTurn struct {
	req     router.RouteRequest
	d       router.RoutingDecision
	agent   Agent
	tierOf  map[string]string
	approve bool // the human approved this turn over a budget's hard limit
}

// runWithFallback runs the turn, and while it ends unavailable (a rate limit, an overloaded or failing provider, a model
// not found) before doing any work, re-runs it on the next model of its fallback chain. Fallback happens only at a turn
// boundary: a turn that made a tool call has started the work (files may have changed, the session holds context), so it
// is never re-run on another model; its result goes on as it is, as any failed turn does. A hop is a new turn on another
// model, so the session policy starts it as a fresh session with a handoff artifact when a work turn came before (P39).
func (e *economics) runWithFallback(ctx, rctx context.Context, in workflow.NodeInput, next workflow.NodeHandler, rt *routedTurn) (workflow.NodeOutput, error) {
	tried := []string{rt.d.Model}
	for {
		w := &turnWatch{}
		hin := in
		hin.Emit = w.wrap(in.Emit)
		out, err := next(rctx, hin)
		if ctx.Err() != nil {
			return out, err
		}
		cause := w.unavailable(err)
		if cause == "" {
			return out, err
		}
		if w.limit != nil {
			e.c.limits.observe(rt.agent.Adapter, w.limit) // the next decision must see the rejected limit now, not when the bus delivers it
		}
		if w.toolCalls > 0 {
			e.c.logf("route turn: %s was unavailable (%s) after the turn started work; no fallback mid-turn", rt.d.Model, cause)
			return out, err
		}
		req := rt.req
		req.Budget = router.BudgetState{} // read again: the failed turn may have spent
		if berr := e.fillBudget(ctx, &req); berr != nil {
			e.c.logf("route turn: fallback: budget: %v", berr)
			return out, err // budgets cannot be read: no hop spends more (fail closed, as the budget middleware does)
		}
		if rt.approve {
			req.Budget.HardLimit = false
		}
		d, ferr := e.agentRouter.FallbackTurn(ctx, req, rt.d, cause, tried)
		if ferr != nil {
			return out, err // the chain ended; routing.blocked says why
		}
		tried = append(tried, d.Model)
		rt.d = d
		rctx = pipeline.WithRoutedModel(ctx, pipeline.RoutedModel{Model: d.Model, Tier: rt.tierOf[d.Model], DecisionID: d.ID, Handoff: d.Handoff})
	}
}

// turnWatch sees a turn's chunks on their way to the run's stream: tool calls (the turn started work), the last error the
// CLI reported, and a rejected rate limit.
type turnWatch struct {
	mu        sync.Mutex
	toolCalls int
	lastErr   string
	limit     json.RawMessage
}

func (w *turnWatch) wrap(emit func(workflow.Chunk)) func(workflow.Chunk) {
	return func(c workflow.Chunk) {
		w.mu.Lock()
		switch c.Kind {
		case workflow.ChunkToolCall:
			w.toolCalls++
		case workflow.ChunkError:
			var e workflow.ErrorChunk
			if c.Decode(&e) == nil && e.Code == "" { // a coded chunk is a warning the turn goes on after (adapter drift)
				w.lastErr = e.Message
			}
		case workflow.ChunkRateLimit:
			var ev struct {
				Info struct {
					Status string `json:"status"`
				} `json:"rate_limit_info"`
			}
			if json.Unmarshal(c.Data, &ev) == nil && ev.Info.Status == "rejected" {
				w.limit = append(json.RawMessage(nil), c.Data...)
			}
		}
		w.mu.Unlock()
		if emit != nil {
			emit(c)
		}
	}
}

// unavailable is the fallback cause of the turn's end, or "" when the turn ended for any other reason (its own work).
func (w *turnWatch) unavailable(err error) router.Cause {
	w.mu.Lock()
	defer w.mu.Unlock()
	msg := w.lastErr
	if err != nil {
		msg = err.Error()
	}
	if c := router.ClassifyUnavailable(msg); c != "" {
		return c
	}
	if w.limit != nil && (err != nil || w.lastErr != "") {
		return router.CauseRateLimited // the CLI said its limit was rejected, and the turn ended in an error
	}
	return ""
}

func (e *economics) decideTurn(ctx context.Context, in workflow.NodeInput) (context.Context, *routedTurn) {
	t, err := e.c.Tasks.Get(ctx, in.TaskID)
	if err != nil {
		return ctx, nil
	}
	id, kind := t.Assignee, router.RunAgentTask
	if in.Meta["agent"] == "review" {
		id, kind = t.Reviewer, router.RunReview
	}
	a, err := e.c.agent(ctx, id)
	if err != nil {
		return ctx, nil
	}
	if len(a.RouteModels) == 0 || e.agentRouter == nil {
		e.saveStatic(ctx, in, "the agent's configured model: routing is off for this agent (Team, Edit: models the router may choose)")
		return ctx, nil
	}
	req := router.RouteRequest{RunKind: kind, TaskID: t.ID, RunID: string(in.RunID), NodeID: string(in.NodeID), Attempt: in.Attempt, Role: router.Role(a.Role),
		Level: router.LevelBalanced, ProjectID: t.ProjectID, AgentID: a.ID, Workflow: in.Workflow, ContextCeiling: router.ContextPrivate,
		Failures: e.turnFailures(ctx, t.ID, string(in.RunID))}
	if kind == router.RunReview {
		if w := e.lastWorkModel(ctx, string(in.RunID)); w != "" {
			req.Worker = &router.WorkerRef{Model: w}
		}
	}
	if err := e.fillBudget(ctx, &req); err != nil {
		e.c.logf("route turn: budget: %v", err)
	}
	approve := budget.ApprovedOverLimit(ctx)
	if approve {
		req.Budget.HardLimit = false // the human approved this one turn over the limit: route it, do not block it
	}
	d, err := e.agentRouter.Decide(ctx, req)
	if err != nil {
		reason := err.Error()
		var be *router.BlockedError
		if errors.As(err, &be) {
			reason = be.Reason
		}
		e.saveStatic(ctx, in, "the router could not choose ("+reason+"), so the agent's configured model runs")
		return ctx, nil
	}
	tierOf := map[string]string{}
	for _, r := range a.RouteModels {
		tierOf[r.Model] = r.Tier
	}
	req.Budget = router.BudgetState{} // the stored request keeps it; a hop reads the budget again
	return pipeline.WithRoutedModel(ctx, pipeline.RoutedModel{Model: d.Model, Tier: tierOf[d.Model], DecisionID: d.ID, Handoff: d.Handoff}),
		&routedTurn{req: req, d: d, agent: a, tierOf: tierOf, approve: approve}
}

// turnFailures are the escalation counters for a run: work turns routed to a cheap model before this one (each came back:
// a check failed or the reviewer revised), and REVISE verdicts given since the run first routed work to premium.
func (e *economics) turnFailures(ctx context.Context, taskID, runID string) router.FailureCounts {
	var f router.FailureCounts
	rows, err := e.c.DB.Reader().QueryContext(ctx, `SELECT ts, decision FROM routing_decisions WHERE run_id=? AND run_kind=? AND decided_by IN ('rules','explicit') ORDER BY ts`,
		runID, string(router.RunAgentTask))
	if err != nil {
		return f
	}
	type turn struct {
		ts   int64
		tier providers.Tier
	}
	var turns []turn
	for rows.Next() {
		var ts int64
		var raw []byte
		if rows.Scan(&ts, &raw) != nil {
			continue
		}
		var d struct {
			FallbackHop int
			Facts       struct{ Tier string }
		}
		if json.Unmarshal(raw, &d) != nil {
			continue
		}
		if d.FallbackHop > 0 && len(turns) > 0 {
			// a fallback hop re-ran the same turn on another model: one turn, counted once, on the model that ran it
			turns[len(turns)-1].tier = providers.Tier(d.Facts.Tier)
			continue
		}
		turns = append(turns, turn{ts, providers.Tier(d.Facts.Tier)})
	}
	rows.Close()
	var firstPremium int64
	for _, x := range turns {
		switch x.tier {
		case providers.TierCheap:
			f.CheapIterations++
		case providers.TierPremium:
			if firstPremium == 0 {
				firstPremium = x.ts
			}
		}
	}
	if firstPremium > 0 {
		if revs, err := e.c.Tasks.Reviews(ctx, taskID); err == nil {
			for _, r := range revs {
				if strings.EqualFold(r.Verdict, "REVISE") && r.Created >= firstPremium {
					f.PremiumRevise++
				}
			}
		}
	}
	return f
}

// lastWorkModel is the model of the run's latest work turn, so a routed reviewer can prefer a different one.
func (e *economics) lastWorkModel(ctx context.Context, runID string) string {
	var m string
	_ = e.c.DB.Reader().QueryRowContext(ctx, `SELECT model FROM routing_decisions WHERE run_id=? AND run_kind=? ORDER BY ts DESC LIMIT 1`, runID, string(router.RunAgentTask)).Scan(&m)
	return m
}

// agentSource gives the router exactly the models an agent's human listed: on the agent's registered provider when it has one
// (with that provider's health, quota and prices), else as agent-native routes of its CLI (a subscription: no price, no
// quota the router can see).
type agentSource struct{ e *economics }

func (s agentSource) Candidates(ctx context.Context, req router.RouteRequest) ([]router.Candidate, error) {
	a, err := s.e.c.agent(ctx, req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("route turn: agent %s: %w", req.AgentID, err)
	}
	var out []router.Candidate
	for _, r := range a.RouteModels {
		tier := providers.Tier(r.Tier)
		if a.ProviderID != "" && s.e.src != nil {
			c, err := s.e.src.CandidateFor(ctx, req, a.ProviderID, r.Model)
			if err != nil {
				return nil, err
			}
			c.Tier = tier // the human's tier for this agent wins over the catalog's
			out = append(out, c)
			continue
		}
		// the CLI's own report of a rejected rate limit makes the model unavailable until the limit resets (agentlimits.go)
		limited, _ := s.e.c.limits.blocked(a.Adapter, r.Model, time.Now())
		out = append(out, router.Candidate{Provider: router.ProviderID("agent:" + a.Adapter), Model: r.Model, Tier: tier, DataClass: providers.DataPrivate,
			Enabled: true, PrivateEligible: true, SupportsTools: true, ResponsesValidated: true, Healthy: true, QuotaOK: !limited, QuotaRemainingPct: -1,
			AgentNative: true}) // no tool-call probe ran: the agent CLI brings its own tools (the explanation says so)
	}
	return out, nil
}

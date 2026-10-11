package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// routeTurn replaces the static decision recorder in the agent_task middleware chain.
func (e *economics) routeTurn(next workflow.NodeHandler) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		if in.TaskID != "" {
			ctx = e.decideTurn(ctx, in)
		}
		return next(ctx, in)
	}
}

func (e *economics) decideTurn(ctx context.Context, in workflow.NodeInput) context.Context {
	t, err := e.c.Tasks.Get(ctx, in.TaskID)
	if err != nil {
		return ctx
	}
	id, kind := t.Assignee, router.RunAgentTask
	if in.Meta["agent"] == "review" {
		id, kind = t.Reviewer, router.RunReview
	}
	a, err := e.c.agent(ctx, id)
	if err != nil {
		return ctx
	}
	if len(a.RouteModels) == 0 || e.agentRouter == nil {
		e.saveStatic(ctx, in, "the agent's configured model: routing is off for this agent (Team, Edit: models the router may choose)")
		return ctx
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
	if budget.ApprovedOverLimit(ctx) {
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
		return ctx
	}
	tier := ""
	for _, r := range a.RouteModels {
		if r.Model == d.Model {
			tier = r.Tier
		}
	}
	return pipeline.WithRoutedModel(ctx, pipeline.RoutedModel{Model: d.Model, Tier: tier, DecisionID: d.ID})
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
	var firstPremium int64
	for rows.Next() {
		var ts int64
		var raw []byte
		if rows.Scan(&ts, &raw) != nil {
			continue
		}
		var d struct{ Facts struct{ Tier string } }
		if json.Unmarshal(raw, &d) != nil {
			continue
		}
		switch providers.Tier(d.Facts.Tier) {
		case providers.TierCheap:
			f.CheapIterations++
		case providers.TierPremium:
			if firstPremium == 0 {
				firstPremium = ts
			}
		}
	}
	rows.Close()
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

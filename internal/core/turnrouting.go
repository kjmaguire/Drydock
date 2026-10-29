package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/responder"
	"github.com/kjmaguire/drydock/internal/budget"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/router"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The model router in the path of agent turns (docs/model-routing.md). An agent whose human listed the models the router may
// choose between (Agent.RouteModels, each cheap or premium) is routed on every turn it takes (decision P63): task work and
// review, the template role turns (the pm's spec, the tests written first, the architect's design, the security review) and
// its replies in the room. The rule router chooses among exactly those models, never another adapter or provider, so a turn
// always runs on the agent's own CLI. Each kind of turn is a run kind of its own (router.TurnSignals gives the task class
// and weight the rules see); the level ladder and the stored rules decide, with the failure counters of the run (cheap
// turns of the same kind that came back, premium work that the reviewer sent back) driving escalation to premium. The
// chosen model, its tier and the decision reach the turn through the context (pipeline.RoutedModel) or, for a room reply,
// through the responder's Route hook. An agent with no list keeps its configured model, recorded as a static decision; if
// the router cannot choose, the configured model runs and the decision says why.

// routeTurn replaces the static decision recorder in the agent_task middleware chain. A routed turn whose model or provider
// turns out to be unavailable before the turn did any work moves along the fallback chain a routing rule names (fallback.go).
func (e *economics) routeTurn(next workflow.NodeHandler) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		if in.TaskID == "" {
			return next(ctx, in)
		}
		s, ok := e.subjectOf(ctx, in)
		if !ok {
			return next(ctx, in)
		}
		rctx, rt, staticID, halt := e.decide(ctx, s)
		if halt != nil {
			// the router refused the turn because a budget is at its hard limit: the turn waits for a human, it is not run on the
			// agent's configured model instead (audit 2026-10-16, M4)
			return e.enforcer.PauseBlocked(ctx, in, halt.Reason)
		}
		out, err := e.runTurn(ctx, rctx, s, in, next, rt, staticID)
		var re *budget.RefusedError
		if errors.As(err, &re) {
			// the turn's estimate does not fit what the budgets have left beside the turns in flight: it waits for a human, as a
			// turn over a budget does (P112)
			return e.enforcer.PauseRefused(ctx, in, re), nil
		}
		return out, err
	}
}

// runTurn runs one agent turn with what decision P112 puts around it: its estimate reserved against the budgets (and held
// on its provider's quota windows) before it starts, its wall-time ceiling, and, for a routed turn, the fallback chain. A
// turn the agent runs on its configured model (staticID) gets the reservation and the ceiling, without a chain.
func (e *economics) runTurn(ctx, rctx context.Context, s turnSubject, in workflow.NodeInput, next workflow.NodeHandler, rt *routedTurn, staticID string) (workflow.NodeOutput, error) {
	if rt != nil {
		return e.runWithFallback(ctx, rctx, s, in, next, rt)
	}
	at := e.attempt(ctx, rctx, s, in, next, turnOn{decisionID: staticID, model: s.agent.Model, force: budget.ApprovedOverLimit(ctx)})
	switch {
	case at.refused != nil:
		return workflow.NodeOutput{}, at.refused
	case at.stop != nil:
		return at.out, at.stop
	}
	return at.out, at.err
}

// routeReply routes one room reply turn (the responder's Route hook): the same decision, records and fallback chain as a
// workflow turn, with the reply's attempt in place of the node.
func (e *economics) routeReply(ctx context.Context, t responder.ReplyTurn, attempt responder.Attempt) error {
	a, err := e.c.agent(ctx, t.Agent.ID)
	if err != nil {
		return attempt(ctx, responder.Routed{}, nil)
	}
	s := turnSubject{kind: router.RunRoomReply, agent: a, projectID: t.ProjectID, taskID: t.TaskID, runID: t.TurnID, nodeID: "reply", attempt: 1,
		contextTokens: t.ContextTokens}
	rctx, rt, staticID, halt := e.decide(ctx, s)
	if halt != nil {
		return fmt.Errorf("%s, so the reply was not made; raise the limit on the Budgets page", halt.Reason)
	}
	next := func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		r, ok := pipeline.RoutedFrom(ctx)
		if !ok {
			return workflow.NodeOutput{}, attempt(ctx, responder.Routed{DecisionID: staticID}, in.Emit)
		}
		return workflow.NodeOutput{}, attempt(ctx, responder.Routed{Model: r.Model, Tier: r.Tier, DecisionID: r.DecisionID}, in.Emit)
	}
	_, err = e.runTurn(ctx, rctx, s, workflow.NodeInput{}, next, rt, staticID)
	var re *budget.RefusedError
	if errors.As(err, &re) {
		return fmt.Errorf("%s, so the reply was not made; raise the limit on the Budgets page", re.Message)
	}
	return err
}

// DirectTurn is an agent turn that is not a workflow node or a room reply: the team lead's (pipeline.Lead) and a
// recommendation (P52). It is routed like every other turn (P112).
type DirectTurn struct {
	Kind      router.RunKind // router.RunLead or router.RunRecommend
	AgentID   string
	ProjectID string
	TaskID    string
	RunID     string // the run the turn belongs to, or the recommendation's id; "" for a project-wide lead turn
	NodeID    string
}

// EconomicsRouteTurn routes a direct turn: run is the turn, which applies the routed model it finds in its context
// (pipeline.RoutedFrom). Without the economics extension, or for an agent that cannot be read, the turn runs once as it is.
func EconomicsRouteTurn(ctx context.Context, c *Core, t DirectTurn, in workflow.NodeInput, run workflow.NodeHandler) (workflow.NodeOutput, error) {
	econMu.Lock()
	e := econByCore[c]
	econMu.Unlock()
	if e == nil {
		return run(ctx, in)
	}
	a, err := c.agent(ctx, t.AgentID)
	if err != nil {
		return run(ctx, in)
	}
	s := turnSubject{kind: t.Kind, agent: a, projectID: t.ProjectID, taskID: t.TaskID, runID: t.RunID, nodeID: t.NodeID, attempt: 1}
	rctx, rt, staticID, halt := e.decide(ctx, s)
	if halt != nil {
		return workflow.NodeOutput{}, fmt.Errorf("%s, so the turn did not start; raise the limit on the Budgets page", halt.Reason)
	}
	out, err := e.runTurn(ctx, rctx, s, in, run, rt, staticID)
	var re *budget.RefusedError
	if errors.As(err, &re) {
		return out, fmt.Errorf("budget: %s", re.Message)
	}
	return out, err
}

// EconomicsRouteReply routes a room reply turn through the model router (decision P63). Without the economics extension
// the turn runs once on the agent's configured model.
func EconomicsRouteReply(ctx context.Context, c *Core, t responder.ReplyTurn, attempt responder.Attempt) error {
	econMu.Lock()
	e := econByCore[c]
	econMu.Unlock()
	if e == nil {
		return attempt(ctx, responder.Routed{}, nil)
	}
	return e.routeReply(ctx, t, attempt)
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
func (e *economics) runWithFallback(ctx, rctx context.Context, s turnSubject, in workflow.NodeInput, next workflow.NodeHandler, rt *routedTurn) (workflow.NodeOutput, error) {
	tried := []string{rt.d.Model}
	for {
		at := e.attempt(ctx, rctx, s, in, next, turnOn{decisionID: rt.d.ID, model: rt.d.Model, estUSD: rt.d.EstCostUSD,
			estTokens: rt.d.EstTokens.Input + rt.d.EstTokens.Output, force: rt.approve})
		if at.refused != nil {
			return workflow.NodeOutput{}, at.refused
		}
		out, err, w := at.out, at.err, at.w
		if ctx.Err() != nil {
			return out, err
		}
		var cause router.Cause
		switch {
		case at.quota:
			cause = router.CauseQuotaExhausted // the provider's windows could not take the turn: it never started
		case at.stop != nil:
			if w.toolCalls > 0 {
				return out, at.stop // stopped after it started work: no other model takes it over mid-turn
			}
			cause = router.CauseWallTime // it hung without doing anything: the chain may take it
			err = at.stop
		default:
			cause = w.unavailable(err)
		}
		if cause == "" {
			return out, err
		}
		if cause == router.CauseRetentionRequired {
			e.markRetention(ctx, s, rt.d)
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
	// what the turn reported spending (its usage chunks), for the reservation's settlement
	usage    providers.Usage
	costUSD  float64
	answered bool // the agent said something or reported usage: the turn reached the model
	// progress, when set, hears that the turn started (its first chunk) and what it has reported spending so far, so its
	// reservation holds only the rest (P112)
	progress func(started bool, usd float64, tokens int64)
	started  bool
}

// ran reports whether the turn reached the model (it spent something), so its reservation is settled rather than released.
func (w *turnWatch) ran() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.answered || w.toolCalls > 0
}

func (w *turnWatch) wrap(emit func(workflow.Chunk)) func(workflow.Chunk) {
	return func(c workflow.Chunk) {
		w.mu.Lock()
		first := !w.started
		w.started = true
		spent := false
		switch c.Kind {
		case workflow.ChunkToolCall:
			w.toolCalls++
		case workflow.ChunkText, workflow.ChunkMessageEnd:
			w.answered = true
		case workflow.ChunkUsage:
			var u workflow.UsageChunk
			if c.Decode(&u) == nil {
				w.answered = true
				w.usage.Input += int(u.InputTokens)
				w.usage.Output += int(u.OutputTokens)
				w.usage.Cached += int(u.CachedTokens)
				w.costUSD += u.CostUSD
				spent = true
			}
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
		progress, usd, tokens := w.progress, w.costUSD, int64(w.usage.Total())
		w.mu.Unlock()
		if progress != nil && (first || spent) {
			progress(true, usd, tokens)
		}
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

// turnSubject is one agent turn as the router is asked about it: which agent, which kind of turn, where it belongs.
type turnSubject struct {
	kind                    router.RunKind
	agent                   Agent
	projectID, taskID       string
	runID, nodeID, workflow string
	attempt, contextTokens  int
}

// subjectOf names the agent and the kind of a workflow agent turn from its node: the task's worker or reviewer, or the agent
// the run pinned for a template role. false: the node is not an agent's task turn the router knows (the planner).
func (e *economics) subjectOf(ctx context.Context, in workflow.NodeInput) (turnSubject, bool) {
	t, err := e.c.Tasks.Get(ctx, in.TaskID)
	if err != nil {
		return turnSubject{}, false
	}
	var id string
	var kind router.RunKind
	switch role := in.Meta["agent"]; role {
	case "review":
		id, kind = t.Reviewer, router.RunReview
	case pipeline.RolePM, pipeline.RoleTests, pipeline.RoleArchitect, pipeline.RoleSecurity:
		var ti pipeline.TemplateInput
		if json.Unmarshal(in.Input, &ti) != nil || ti.Roles[role] == "" {
			return turnSubject{}, false // no agent for the role: the node passes through without a turn (the optional architect)
		}
		id = ti.Roles[role]
		kind = map[string]router.RunKind{pipeline.RolePM: router.RunSpec, pipeline.RoleTests: router.RunTests,
			pipeline.RoleArchitect: router.RunDesign, pipeline.RoleSecurity: router.RunSecurityReview}[role]
	case "work", "":
		id, kind = t.Assignee, router.RunAgentTask
	default:
		return turnSubject{}, false
	}
	a, err := e.c.agent(ctx, id)
	if err != nil {
		return turnSubject{}, false
	}
	return turnSubject{kind: kind, agent: a, projectID: t.ProjectID, taskID: t.ID, runID: string(in.RunID), nodeID: string(in.NodeID), workflow: in.Workflow,
		attempt: in.Attempt}, true
}

// decide asks the router for the turn's model among the agent's own models. Without a list (or when the router cannot
// choose) the configured model runs, recorded as a static decision whose id is returned. The one exception is a budget at its
// hard limit that no human has allowed this turn to exceed: then the turn must not run at all, and the fourth result says so.
func (e *economics) decide(ctx context.Context, s turnSubject) (context.Context, *routedTurn, string, *router.BlockedError) {
	a := s.agent
	if len(a.RouteModels) == 0 || e.agentRouter == nil {
		return ctx, nil, e.saveStatic(ctx, s, "the agent's configured model: routing is off for this agent (Team, Edit: models the router may choose)"), nil
	}
	level, from := e.levelFor(ctx, s.projectID) // the project's own level, else the workspace's (P118)
	req := router.RouteRequest{RunKind: s.kind, TaskID: s.taskID, RunID: s.runID, NodeID: s.nodeID, Attempt: s.attempt, Role: router.Role(a.Role),
		Level: level, LevelSource: from, ProjectID: s.projectID, AgentID: a.ID, Workflow: s.workflow, ContextCeiling: router.ContextPrivate,
		ContextTokens: s.contextTokens, Failures: e.turnFailures(ctx, s)}
	if sig, ok := router.TurnSignals(s.kind); ok {
		req.TaskType, req.Complexity, req.Ambiguity = sig.TaskType, sig.Complexity, sig.Ambiguity
	}
	if s.kind == router.RunReview || s.kind == router.RunSecurityReview {
		if w := e.lastWorkModel(ctx, s.runID); w != "" {
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
			if be.Budget && !approve {
				return ctx, nil, "", be
			}
		}
		return ctx, nil, e.saveStatic(ctx, s, "the router could not choose ("+reason+"), so the agent's configured model runs"), nil
	}
	tierOf := map[string]string{}
	for _, r := range a.RouteModels {
		tierOf[r.Model] = r.Tier
	}
	req.Budget = router.BudgetState{} // the stored request keeps it; a hop reads the budget again
	return pipeline.WithRoutedModel(ctx, pipeline.RoutedModel{Model: d.Model, Tier: tierOf[d.Model], DecisionID: d.ID, Handoff: d.Handoff}),
		&routedTurn{req: req, d: d, agent: a, tierOf: tierOf, approve: approve}, d.ID, nil
}

// turnFailures are the escalation counters of a turn. Work, review and security review turns see the run's work: work turns
// routed to a cheap model before this one (each came back: a check failed or the reviewer revised), and REVISE verdicts
// given since the run first routed work to premium. A tests, spec or design turn counts the earlier cheap turns of its own
// kind in the run (each came back: tests that did not fail first, a spec that could not be used or was sent back). A room
// reply, a team lead's decision and a recommendation stand alone: no counters.
func (e *economics) turnFailures(ctx context.Context, s turnSubject) router.FailureCounts {
	var f router.FailureCounts
	kind := s.kind
	switch kind {
	case router.RunRoomReply, router.RunLead, router.RunRecommend:
		return f
	case router.RunReview, router.RunSecurityReview:
		kind = router.RunAgentTask
	}
	rows, err := e.c.DB.Reader().QueryContext(ctx, `SELECT ts, decision FROM routing_decisions WHERE run_id=? AND run_kind=? AND decided_by IN ('rules','explicit','llm') ORDER BY ts`,
		s.runID, string(kind))
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
	if firstPremium > 0 && kind == router.RunAgentTask {
		if revs, err := e.c.Tasks.Reviews(ctx, s.taskID); err == nil {
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
			AgentNative: true, RetentionRequired: s.e.c.Providers.RetentionBlocked(ctx, "agent:"+a.Adapter, r.Model)}) // no tool-call probe ran: the agent CLI brings its own tools (the explanation says so)
	}
	return out, nil
}

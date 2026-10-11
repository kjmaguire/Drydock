package router

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kjmaguire/drydock/internal/ids"
)

// Assignment is a configured agent, provider, model and effort.
type Assignment struct {
	Agent    AgentRef
	Provider ProviderID
	Model    string
	Effort   Effort
	TeamSize int
}

// StaticConfig is the Slice 1 routing configuration: the model an agent is assigned, no rules.
type StaticConfig struct {
	Default   Assignment
	ByRole    map[Role]Assignment
	ByAgent   map[string]Assignment // keyed by RouteRequest.AgentID
	Fallbacks []Assignment          // tried in order when the assignment is unhealthy or out of quota
	// Escalation is the assignment Next uses for an escalation cause; without one the static router cannot escalate.
	Escalation *Assignment
}

// StaticRouter returns the configured assignment (brief section 6, Slice 1). It still enforces the invariants: a
// private context never gets a trainable assignment, the free tier is for CONSULT only, disabled providers are not
// used. A violating assignment is BLOCKED, never silently replaced; a merely unavailable one (unhealthy, quota,
// concurrency) falls to the configured fallbacks.
type StaticRouter struct {
	cfg StaticConfig
	src *RegistrySource
	rec *Recorder
	now func() time.Time
}

// StaticOptions wires the static router.
type StaticOptions struct {
	Source   *RegistrySource // required: the invariants need the catalog
	Recorder *Recorder       // optional: persistence and events
	Now      func() time.Time
}

// NewStaticRouter builds the router.
func NewStaticRouter(cfg StaticConfig, o StaticOptions) (*StaticRouter, error) {
	if o.Source == nil || o.Source.Catalog == nil {
		return nil, errors.New("router: StaticRouter needs a RegistrySource with a Catalog")
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	return &StaticRouter{cfg: cfg, src: o.Source, rec: o.Recorder, now: o.Now}, nil
}

func (r *StaticRouter) assignmentFor(req RouteRequest) (a Assignment, by string) {
	if p := req.Explicit; p != nil && p.Provider != "" && p.Model != "" {
		return Assignment{Provider: p.Provider, Model: p.Model, Effort: p.Effort, Agent: AgentRef{Instance: p.Agent}}, "explicit"
	}
	if a, ok := r.cfg.ByAgent[req.AgentID]; ok && req.AgentID != "" {
		return a, "static"
	}
	if a, ok := r.cfg.ByRole[req.Role]; ok {
		return a, "static"
	}
	return r.cfg.Default, "static"
}

// Decide returns the configured assignment for the request.
func (r *StaticRouter) Decide(ctx context.Context, req RouteRequest) (RoutingDecision, error) {
	a, by := r.assignmentFor(req)
	return r.decide(ctx, req, a, by, nil, nil)
}

func (r *StaticRouter) decide(ctx context.Context, req RouteRequest, a Assignment, by string, from *Alternative, extra func(*RoutingDecision)) (RoutingDecision, error) {
	if a.Provider == "" || a.Model == "" {
		return RoutingDecision{}, r.blocked(ctx, req, "no provider and model assigned for this agent or role", nil)
	}
	chain := []Assignment{a}
	if by != "explicit" { // an explicit pin is never replaced by a fallback
		chain = append(chain, r.cfg.Fallbacks...)
	}
	var rejected []Alternative
	for i, cand := range chain {
		if from != nil && cand.Provider == from.Provider && cand.Model == from.Model {
			rejected = append(rejected, Alternative{Provider: cand.Provider, Model: cand.Model, Tier: from.Tier, Rejected: from.Rejected, Detail: "the model that just failed"})
			continue
		}
		c, err := r.src.CandidateFor(ctx, req, string(cand.Provider), cand.Model)
		if err != nil {
			rejected = append(rejected, Alternative{Provider: cand.Provider, Model: cand.Model, Rejected: "unknown_provider", Detail: err.Error()})
			if i == 0 {
				return RoutingDecision{}, r.blocked(ctx, req, fmt.Sprintf("assigned provider %s is not registered", cand.Provider), rejected)
			}
			continue
		}
		kept, rej := HardFilter(req, []Candidate{c})
		if len(kept) == 1 {
			d := r.build(req, cand, c, by, rejected, from)
			if i > 0 && len(rejected) > 0 {
				d.Reason = fmt.Sprintf("assigned %s/%s unavailable (%s); using configured fallback", a.Provider, a.Model, rejected[0].Rejected)
			}
			if extra != nil {
				extra(&d)
			}
			return r.finish(ctx, req, d)
		}
		rejected = append(rejected, rej...)
		if isInvariant(rej[0].Rejected) {
			return RoutingDecision{}, r.blocked(ctx, req, fmt.Sprintf("%s/%s violates %s: %s", cand.Provider, cand.Model, rej[0].Rejected, rej[0].Detail), rejected)
		}
	}
	return RoutingDecision{}, r.blocked(ctx, req, "the assigned model and every configured fallback are unavailable", rejected)
}

// isInvariant reports whether the filter is a policy invariant (never worked around) rather than availability.
func isInvariant(filter string) bool {
	return filter == FilterDataClass || filter == FilterFreeAgentWork || filter == FilterDisabled
}

func (r *StaticRouter) build(req RouteRequest, a Assignment, c Candidate, by string, rejected []Alternative, from *Alternative) RoutingDecision {
	level := req.Level
	if level == "" {
		level = LevelBalanced
	}
	effort := a.Effort
	if req.Explicit != nil && req.Explicit.Effort != "" {
		effort = req.Explicit.Effort
	}
	if effort == "" {
		effort = DefaultEffort(level)
	}
	team := a.TeamSize
	if team <= 0 {
		team = 1
	}
	est := estimate(req, c)
	d := RoutingDecision{ID: ids.New("dec"), Agent: a.Agent, Provider: c.Provider, Model: c.Model, Effort: effort, EffortApplied: !c.AgentNative, TeamSize: team,
		Reason: fmt.Sprintf("configured assignment for %s", roleOrAgent(req)), Confidence: 1, EstTokens: est, EstCostUSD: c.EstCostUSD,
		Alternatives: rejected, DecidedBy: by, Created: r.now(), SwitchCostTokens: switchCost(req, c)}
	d.Facts = factsFor(req, c, 0)
	d.Facts.Level = level
	d.Facts.FallbackFrom = from
	if by == "explicit" {
		d.Reason = "explicit pin"
		d.PoliciesApplied = []string{"explicit_pin"}
	}
	return d
}

func roleOrAgent(req RouteRequest) string {
	switch {
	case req.AgentID != "":
		return "agent " + req.AgentID
	case req.Role != "":
		return "role " + string(req.Role)
	}
	return "the default"
}

func (r *StaticRouter) finish(ctx context.Context, req RouteRequest, d RoutingDecision) (RoutingDecision, error) {
	if err := r.rec.Save(ctx, req, d); err != nil {
		return RoutingDecision{}, err
	}
	r.rec.emit(ctx, "routing.decided", req, d, nil)
	return d, nil
}

func (r *StaticRouter) blocked(ctx context.Context, req RouteRequest, reason string, rej []Alternative) error {
	e := &BlockedError{Reason: reason, Alternatives: rej}
	r.rec.emitBlocked(ctx, req, e)
	return e
}

// Next handles a fallback by moving to the next configured assignment, an escalation by switching to the configured
// escalation assignment, and a de-escalation by deciding again from the original request.
func (r *StaticRouter) Next(ctx context.Context, prev RoutingDecision, cause Cause) (RoutingDecision, error) {
	req, _, err := r.rec.Load(ctx, prev.ID)
	if err != nil {
		return RoutingDecision{}, err
	}
	pr := &PriorRef{DecisionID: prev.ID, Provider: prev.Provider, Model: prev.Model, Tier: prev.Facts.Tier}
	switch causeKind(cause) {
	case "fallback":
		a, by := r.assignmentFor(req)
		from := &Alternative{Provider: prev.Provider, Model: prev.Model, Tier: prev.Facts.Tier, Rejected: string(cause)}
		d, err := r.decide(ctx, req, a, by, from, nil)
		if err != nil {
			return RoutingDecision{}, err
		}
		if d.Provider == prev.Provider && d.Model == prev.Model {
			return RoutingDecision{}, r.blocked(ctx, req, "no configured fallback differs from the failed model", nil)
		}
		r.rec.emit(ctx, "routing.fallback", req, d, map[string]any{"from": map[string]any{"provider": prev.Provider, "model": prev.Model}, "cause": cause})
		return d, nil
	case "escalate":
		if r.cfg.Escalation == nil {
			return RoutingDecision{}, r.blocked(ctx, req, "the static router has no escalation assignment configured", nil)
		}
		d, err := r.decide(ctx, req, *r.cfg.Escalation, "static", nil, func(d *RoutingDecision) { d.EscalatedFrom, d.Handoff = pr, true })
		if err != nil {
			return RoutingDecision{}, err
		}
		r.rec.emit(ctx, "routing.escalated", req, d, map[string]any{"cause": cause})
		return d, nil
	case "deescalate":
		req.Failures = FailureCounts{}
		req.Session = nil
		a, by := r.assignmentFor(req)
		d, err := r.decide(ctx, req, a, by, nil, func(d *RoutingDecision) { d.DeescalatedFrom = pr })
		if err != nil {
			return RoutingDecision{}, err
		}
		r.rec.emit(ctx, "routing.deescalated", req, d, map[string]any{"cause": cause})
		return d, nil
	}
	return RoutingDecision{}, fmt.Errorf("router: unknown cause %q", cause)
}

// Explain renders the decision.
func (r *StaticRouter) Explain(d RoutingDecision) string { return explain(d) }

// estimate fills the token estimate: the context to send, the expected output, and the part of the context a live
// session on the same model already has cached.
func estimate(req RouteRequest, c Candidate) TokenEstimate {
	in := req.ContextTokens
	if in == 0 && req.Session != nil {
		in = req.Session.ContextTokens
	}
	est := TokenEstimate{Input: in, Output: outputBudget(req)}
	if s := req.Session; s != nil && s.Provider == c.Provider && s.Model == c.Model && s.CacheAge < 5*time.Minute {
		est.Cached = min(s.ContextTokens, in)
	}
	return est
}

// switchCost is the uncached re-read cost in tokens if the choice differs from the live session's model.
func switchCost(req RouteRequest, c Candidate) int {
	if s := req.Session; s != nil && (s.Provider != c.Provider || s.Model != c.Model) {
		return s.ContextTokens
	}
	return 0
}

func factsFor(req RouteRequest, c Candidate, excludedTrainable int) DecisionFacts {
	ceiling := req.ContextCeiling
	if ceiling != ContextPublicDocs {
		ceiling = ContextPrivate
	}
	return DecisionFacts{TaskID: req.TaskID, Level: req.Level, RunKind: req.RunKind, TaskType: req.TaskType, DiffLines: req.DiffLines, Risk: req.Risk, Attempt: req.Attempt,
		ContextCeiling: ceiling, Tier: c.Tier, DataClass: c.DataClass, ExcludedTrainable: excludedTrainable, QuotaRemainingPct: c.QuotaRemainingPct,
		BudgetScope: req.Budget.Scope, BudgetPctUsed: req.Budget.PctUsed, ProbePassed: c.ProbePassed, PriceUpdatedAt: c.PriceUpdatedAt}
}

var _ ModelRouter = (*StaticRouter)(nil)

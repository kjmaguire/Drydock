package router

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/providers"
)

// RuleRouter is the deterministic rule-based router of Slice 2: hard filters, explicit pin, stored CEL policies in
// priority order, the budget limit, then the level ladder (docs/model-routing.md s2). It is pure apart from reading
// candidates, writing the decision record and, when a Tiebreaker is set and the ranking leaves a tie, the one tiebreak call
// (tiebreak.go).
type RuleRouter struct {
	eng   *Engine
	store *RuleStore
	extra []Rule
	noDef bool
	src   CandidateSource
	rec   *Recorder
	tie   Tiebreaker
	now   func() time.Time

	mu    sync.Mutex
	cache map[string]*Compiled
}

// RuleOptions wires the RuleRouter.
type RuleOptions struct {
	Engine *Engine         // required
	Source CandidateSource // required
	// Store supplies stored rules. Without one (or with an empty one) the shipped DefaultRules apply unless NoDefaults.
	Store      *RuleStore
	Rules      []Rule // additional fixed rules, applied after the stored ones
	NoDefaults bool
	Recorder   *Recorder
	// Tiebreaker, when set, chooses between candidates the ranking leaves tied (tiebreak.go); nil: the ranking decides.
	Tiebreaker Tiebreaker
	Now        func() time.Time
}

// NewRuleRouter builds the router.
func NewRuleRouter(o RuleOptions) (*RuleRouter, error) {
	if o.Engine == nil || o.Source == nil {
		return nil, errors.New("router: RuleRouter needs an Engine and a CandidateSource")
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	return &RuleRouter{eng: o.Engine, store: o.Store, extra: o.Rules, noDef: o.NoDefaults, src: o.Source, rec: o.Recorder, tie: o.Tiebreaker, now: o.Now, cache: map[string]*Compiled{}}, nil
}

type mode struct {
	order           []providers.Tier // overrides the level's tier order
	exclude         *PriorRef        // the failed candidate of a fallback
	requireTier     providers.Tier
	escalatedFrom   *PriorRef
	deescalatedFrom *PriorRef
	fallbackFrom    *Alternative
	handoff         bool
	prefer          providers.Tier // a budget downshift prefers the cheap tier
	// only and onlyProvider limit the candidates to one model (and, when set, one provider): a fallback hop (fallback.go).
	only         string
	onlyProvider ProviderID
	quiet        bool // no routing.blocked event: the caller tries the next model and reports the outcome itself
}

// Decide chooses a model for the request.
func (r *RuleRouter) Decide(ctx context.Context, req RouteRequest) (RoutingDecision, error) {
	d, err := r.decide(ctx, req, mode{})
	if err != nil {
		return RoutingDecision{}, err
	}
	return d, r.persist(ctx, req, d, "routing.decided", nil)
}

func (r *RuleRouter) persist(ctx context.Context, req RouteRequest, d RoutingDecision, typ string, extra map[string]any) error {
	if err := r.rec.Save(ctx, req, d); err != nil {
		return err
	}
	r.rec.emit(ctx, "routing.decided", req, d, nil)
	if typ != "routing.decided" {
		r.rec.emit(ctx, typ, req, d, extra)
	}
	return nil
}

func (r *RuleRouter) blocked(ctx context.Context, req RouteRequest, reason string, rej []Alternative) error {
	e := &BlockedError{Reason: reason, Alternatives: rej}
	r.rec.emitBlocked(ctx, req, e)
	return e
}

// rules returns the compiled rules in force for the request.
func (r *RuleRouter) rules(ctx context.Context, req RouteRequest) ([]*Compiled, error) {
	var all []Rule
	if r.store != nil {
		stored, err := r.store.List(ctx)
		if err != nil {
			return nil, err
		}
		all = append(all, stored...)
	}
	if len(all) == 0 && !r.noDef {
		all = append(all, DefaultRules()...)
	}
	all = append(all, r.extra...)
	all = append(all, req.Policies...)
	byID := map[string]int{}
	var dedup []Rule
	for _, x := range all { // a later rule with the same id replaces the earlier one (request over project over workspace)
		if i, ok := byID[x.ID]; ok {
			dedup[i] = x
			continue
		}
		byID[x.ID] = len(dedup)
		dedup = append(dedup, x)
	}
	var out []*Compiled
	for _, x := range dedup {
		if !x.Enabled {
			continue
		}
		key := fmt.Sprintf("%s|%d|%d|%s", x.ID, x.Version, x.Priority, x.When)
		r.mu.Lock()
		c := r.cache[key]
		r.mu.Unlock()
		if c == nil {
			var err error
			if c, err = r.eng.Compile(x); err != nil {
				return nil, err
			}
			r.mu.Lock()
			r.cache[key] = c
			r.mu.Unlock()
		} else {
			cp := *c
			cp.Rule = x
			c = &cp
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func varsFor(req RouteRequest, level Level, c *Candidate, now time.Time) Vars {
	v := Vars{
		Task: TaskVars{Type: req.TaskType, Role: string(req.Role), Complexity: req.Complexity, Ambiguity: req.Ambiguity, Risk: req.Risk, DiffLines: req.DiffLines,
			FilesTouched: req.FilesTouched, Attempt: req.Attempt, CheapIterations: req.Failures.CheapIterations, PremiumReviseCount: req.Failures.PremiumRevise,
			WatchdogStage1: req.Failures.WatchdogStage1, WatchdogStage2: req.Failures.WatchdogStage2, SameSignatureRepeats: req.Failures.SameSignatureRepeats,
			ChecksPassing: req.ChecksPassing},
		Run:    RunVars{Kind: string(req.RunKind), Workflow: req.Workflow, Template: req.Template, Iteration: req.Iteration, MaxIterations: req.MaxIterations},
		Ctx:    CtxVars{Ceiling: string(req.ContextCeiling), Tokens: req.ContextTokens, HasUntrusted: req.HasUntrusted},
		Budget: BudgetVars{Scope: req.Budget.Scope, PctUsed: req.Budget.PctUsed, RemainingUSD: req.Budget.RemainingUSD, HardLimit: req.Budget.HardLimit},
		Level:  string(level), Now: now,
	}
	if v.Ctx.Ceiling == "" {
		v.Ctx.Ceiling = string(ContextPrivate)
	}
	if req.Explicit != nil {
		v.Task.PinnedTier = string(req.Explicit.Tier)
	}
	if req.Worker != nil {
		v.Task.WorkerModel = req.Worker.Model
	}
	v.Candidate = CandidateVars{EstCostUSD: -1, QuotaRemainingPct: -1}
	if c != nil {
		est := -1.0
		if c.EstCostUSD != nil {
			est = *c.EstCostUSD
		}
		v.Candidate = CandidateVars{Provider: string(c.Provider), Model: c.Model, Tier: string(c.Tier), DataClass: string(c.DataClass), Kind: string(c.Kind),
			SupportsTools: c.SupportsTools, ResponsesVerified: c.ResponsesValidated, EstCostUSD: est, QuotaRemainingPct: c.QuotaRemainingPct, Healthy: c.Healthy,
			Concurrent: c.Concurrent, MaxConcurrent: c.MaxConcurrent, ProbePassed: c.ProbePassed, Context: c.Context, AgentNative: c.AgentNative}
	}
	return v
}

func pinMatches(p *Pin, c Candidate) bool {
	return (p.Provider == "" || p.Provider == c.Provider) && (p.Model == "" || p.Model == c.Model) && (p.Tier == "" || p.Tier == c.Tier)
}

func (r *RuleRouter) decide(ctx context.Context, req RouteRequest, m mode) (RoutingDecision, error) {
	level := req.Level
	if level == "" {
		level = LevelBalanced
		req.Level = level
	}
	now := r.now()
	block := func(reason string, rej []Alternative) error {
		if m.quiet {
			return &BlockedError{Reason: reason, Alternatives: rej}
		}
		return r.blocked(ctx, req, reason, rej)
	}
	all, err := r.src.Candidates(ctx, req)
	if err != nil {
		return RoutingDecision{}, err
	}
	if m.only != "" {
		keep := all[:0:0]
		for _, c := range all {
			if c.Model == m.only && (m.onlyProvider == "" || c.Provider == m.onlyProvider) {
				keep = append(keep, c)
			}
		}
		if len(keep) == 0 {
			return RoutingDecision{}, block("not among the models the router may choose for this turn", nil)
		}
		all = keep
	}
	if m.exclude != nil {
		keep := all[:0:0]
		for _, c := range all {
			if !(c.Provider == m.exclude.Provider && c.Model == m.exclude.Model) {
				keep = append(keep, c)
			}
		}
		all = keep
	}
	if len(all) == 0 {
		return RoutingDecision{}, block("no candidate providers or models are registered", nil)
	}
	kept, rejected := HardFilter(req, all)
	excludedTrainable := 0
	for _, a := range rejected {
		if a.Rejected == FilterDataClass {
			excludedTrainable++
		}
	}
	if len(kept) == 0 {
		return RoutingDecision{}, block("no candidate passes the invariants (data class, health, quota, capability)", rejected)
	}

	// Explicit pin: narrows the set; it never overrides an invariant.
	by := "rules"
	pin := req.Explicit
	if pin != nil && (pin.Provider != "" || pin.Model != "" || pin.Tier != "") {
		var narrowed []Candidate
		for _, c := range kept {
			if pinMatches(pin, c) {
				narrowed = append(narrowed, c)
			}
		}
		if len(narrowed) == 0 {
			reason := "the pinned model is not among the known candidates"
			for _, a := range rejected {
				if (pin.Provider == "" || pin.Provider == a.Provider) && (pin.Model == "" || pin.Model == a.Model) && (pin.Tier == "" || pin.Tier == a.Tier) {
					reason = fmt.Sprintf("the pinned %s/%s is refused by %s: %s", a.Provider, a.Model, a.Rejected, a.Detail)
					break
				}
			}
			return RoutingDecision{}, block(reason, rejected)
		}
		kept, by = narrowed, "explicit"
	} else {
		pin = nil
	}

	if req.Budget.HardLimit && req.Budget.PctUsed >= 1 {
		err := block(fmt.Sprintf("the %s budget is at its hard limit; only a human response resumes work", req.Budget.Scope), nil)
		var be *BlockedError
		if errors.As(err, &be) {
			be.Budget = true
		}
		return RoutingDecision{}, err
	}

	// Stored CEL policies, in priority order.
	compiled, err := r.rules(ctx, req)
	if err != nil {
		return RoutingDecision{}, err
	}
	var global Effects
	per := map[string]Effects{}
	var evalErrs []string
	reqVars := varsFor(req, level, nil, now)
	for _, c := range compiled {
		if c.UsesCandidate {
			continue
		}
		ok, err := c.Eval(reqVars)
		if err != nil {
			evalErrs = append(evalErrs, err.Error())
			continue
		}
		if ok {
			global.Merge(c.ID, c.Then)
		}
	}
	for i := range kept {
		cv := varsFor(req, level, &kept[i], now)
		var e Effects
		for _, c := range compiled {
			if !c.UsesCandidate {
				continue
			}
			ok, err := c.Eval(cv)
			if err != nil {
				evalErrs = append(evalErrs, err.Error())
				continue
			}
			if ok {
				e.Merge(c.ID, c.Then)
			}
		}
		per[kept[i].Key()] = e
	}
	// Level escalation thresholds (the shipped rules use the Balanced numbers; levels differ).
	if m.prefer != "" && global.PreferTier == "" {
		global.PreferTier = m.prefer
		global.Applied = append(global.Applied, "budget-downshift")
	}
	if esc := ShouldEscalate(level, req.Failures, req.ChecksPassing, ""); esc.Escalate && global.EscalateTo == "" {
		global.EscalateTo, global.Handoff = providers.TierPremium, esc.Handoff
		global.Applied = append(global.Applied, "level:"+esc.Trigger)
	}
	if m.requireTier != "" && tierRank(m.requireTier) > tierRank(global.RequireTier) {
		global.RequireTier = m.requireTier
	}
	if global.EscalateTo != "" && tierRank(global.EscalateTo) > tierRank(global.RequireTier) {
		global.RequireTier = global.EscalateTo
	}
	handoff := global.Handoff || m.handoff

	order := m.order
	if order == nil {
		order = TierOrder(level, req.RunKind, global.PreferTier)
	}
	var ranked []Scored
	var ruleRejected []Alternative
	if pin != nil {
		// An explicit instruction outranks the stored policies: only ordering and effects apply.
		order = []providers.Tier{providers.TierFree, providers.TierCheap, providers.TierPremium}
		free := Effects{Applied: global.Applied, SetEffort: global.SetEffort, SetTeamSize: global.SetTeamSize}
		ranked, ruleRejected = Rank(order, free, nil, kept)
	} else {
		ranked, ruleRejected = Rank(order, global, per, kept)
	}
	if len(ranked) == 0 {
		return RoutingDecision{}, block("the policies leave no candidate", append(append([]Alternative(nil), rejected...), ruleRejected...))
	}
	var tb *TiebreakRecord
	if pin == nil && m.only == "" && !m.quiet {
		tb = r.tiebreak(ctx, req, level, ranked)
	}
	top := ranked[0]
	c := top.Candidate

	// Fallback may not violate a tier the policy pins.
	if m.fallbackFrom != nil && req.Explicit != nil && req.Explicit.Provider != "" && req.Explicit.Model != "" {
		return RoutingDecision{}, block("the model is pinned by an explicit instruction; no fallback is substituted", nil)
	}

	eff := top.Effects
	d := RoutingDecision{
		ID: ids.New("dec"), Agent: AgentRef{Adapter: "", Instance: req.AgentID}, Provider: c.Provider, Model: c.Model,
		Effort: ResolveEffort(level, pin, eff.SetEffort), EffortApplied: !c.AgentNative, TeamSize: ResolveTeamSize(level, req.Risk, eff.SetTeamSize),
		EstTokens: estimate(req, c), EstCostUSD: c.EstCostUSD, DecidedBy: by, Created: now, SwitchCostTokens: switchCost(req, c),
		EscalatedFrom: m.escalatedFrom, DeescalatedFrom: m.deescalatedFrom,
	}
	if req.Explicit != nil && req.Explicit.Agent != "" {
		d.Agent.Instance = req.Explicit.Agent
	}
	d.Facts = factsFor(req, c, excludedTrainable)
	d.Facts.Level = level
	d.Facts.FallbackFrom = m.fallbackFrom
	d.Facts.BudgetDownshift = contains(eff.Applied, "budget-downshift") || contains(global.Applied, "budget-downshift")
	d.Facts.Tiebreak = tb
	if tb != nil && tb.Failed == "" {
		d.DecidedBy = "llm"
	}

	// Policies applied: invariants first, then the pin, then rules in application order.
	if req.ContextCeiling != ContextPublicDocs {
		d.PoliciesApplied = append(d.PoliciesApplied, "invariant:data_class")
	}
	if req.RunKind != RunConsult {
		d.PoliciesApplied = append(d.PoliciesApplied, "invariant:free_tier_consult_only")
	}
	if by == "explicit" {
		d.PoliciesApplied = append(d.PoliciesApplied, "explicit_pin")
	}
	seen := map[string]bool{}
	for _, id := range append(append([]string(nil), global.Applied...), eff.Applied...) {
		if !seen[id] {
			seen[id] = true
			d.PoliciesApplied = append(d.PoliciesApplied, id)
		}
	}

	if tb != nil && tb.Failed == "" {
		d.PoliciesApplied = append(d.PoliciesApplied, "llm_tiebreak")
	}

	// Confidence is rule agreement on the tier, not a quality prediction.
	var tierOpinions, agree int
	for _, id := range global.Applied {
		for _, cr := range compiled {
			if cr.ID == id && (cr.Then.PreferTier != "" || cr.Then.RequireTier != "") {
				tierOpinions++
				if cr.Then.PreferTier == c.Tier || cr.Then.RequireTier == c.Tier {
					agree++
				}
			}
		}
	}
	d.Confidence = 1
	if tierOpinions > 0 {
		d.Confidence = float64(agree) / float64(tierOpinions)
	}

	// Alternatives: rule rejections, availability rejections (data-class exclusions are only counted), then the next
	// two ranked candidates.
	alts := append([]Alternative(nil), ruleRejected...)
	for _, a := range rejected {
		if a.Rejected != FilterDataClass {
			alts = append(alts, a)
		}
	}
	for i := 1; i < len(ranked); i++ {
		s := ranked[i]
		switch {
		case s.Penalty > top.Penalty:
			// demoted by a penalty rule: name it
		case i > 2:
			continue
		}
		why, detail := "ranked_lower", "valid but ranked below the choice"
		if own := per[s.Key()]; own.Penalty > 0 && len(own.Applied) > 0 {
			why, detail = own.Applied[0], "demoted by a penalty"
		}
		alts = append(alts, Alternative{Provider: s.Provider, Model: s.Model, Tier: s.Tier, Rejected: why, Detail: detail})
	}
	sort.SliceStable(alts, func(i, j int) bool {
		if alts[i].Rejected != alts[j].Rejected {
			return alts[i].Rejected < alts[j].Rejected
		}
		return string(alts[i].Provider)+alts[i].Model < string(alts[j].Provider)+alts[j].Model
	})
	if len(alts) > 8 {
		alts = alts[:8]
	}
	d.Alternatives = alts

	why := "level ladder"
	if len(eff.Applied) > 0 || len(global.Applied) > 0 {
		why = "rules " + joinMax(d.PoliciesApplied, 3)
	}
	if by == "explicit" {
		why = "explicit pin"
	}
	d.Reason = fmt.Sprintf("%s/%s (%s) for %s: %s", c.Provider, c.Model, c.Tier, orStr(req.TaskType, string(req.RunKind)), why)
	if len(eff.Fallback) > 0 {
		d.FallbackChain, d.FallbackRule = eff.Fallback, eff.FallbackRule
	}
	d.Reason += tiebreakReason(tb)
	if handoff {
		d.Handoff = true
		d.Reason += "; handoff artifact required"
	}
	if len(evalErrs) > 0 {
		d.Reason += fmt.Sprintf("; %d rule(s) failed to evaluate and were skipped", len(evalErrs))
	}
	if c.Tier != providers.TierPremium && by != "explicit" {
		if n := IterationsUntilEscalation(level, req.Failures); n > 0 && !req.ChecksPassing {
			d.Facts.NextEscalation = fmt.Sprintf("escalates to premium after %d more failed %s (%d cheap iterations) with a handoff artifact", n, plural(n, "iteration", "iterations"), LevelThresholds(level).CheapIterations)
		}
	}
	return d, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func joinMax(s []string, n int) string {
	var out []string
	for _, x := range s {
		if len(x) > 10 && x[:10] == "invariant:" {
			continue
		}
		out = append(out, x)
		if len(out) == n {
			break
		}
	}
	res := ""
	for i, x := range out {
		if i > 0 {
			res += ", "
		}
		res += x
	}
	return res
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Next produces the follow-up decision after a failure or a resolution. Fallback excludes the failed model and tries
// the same tier then the next tier up; escalation requires the premium tier with a handoff artifact (a fresh session,
// never the transcript); de-escalation starts again from the cheap ladder and never keeps the premium model sticky.
func (r *RuleRouter) Next(ctx context.Context, prev RoutingDecision, cause Cause) (RoutingDecision, error) {
	req, stored, err := r.rec.Load(ctx, prev.ID)
	if err != nil {
		return RoutingDecision{}, err
	}
	if prev.Facts.Tier == "" {
		prev.Facts = stored.Facts
	}
	pr := &PriorRef{DecisionID: prev.ID, Provider: prev.Provider, Model: prev.Model, Tier: prev.Facts.Tier}
	level := req.Level
	if level == "" {
		level = LevelBalanced
	}
	switch causeKind(cause) {
	case "fallback":
		order := FallbackTierOrder(prev.Facts.Tier)
		if req.Explicit != nil && req.Explicit.Tier != "" {
			order = []providers.Tier{req.Explicit.Tier}
		}
		from := &Alternative{Provider: prev.Provider, Model: prev.Model, Tier: prev.Facts.Tier, Rejected: string(cause)}
		d, err := r.decide(ctx, req, mode{order: order, exclude: pr, fallbackFrom: from})
		if err != nil {
			return RoutingDecision{}, err
		}
		return d, r.persist(ctx, req, d, "routing.fallback", map[string]any{"from": map[string]any{"provider": prev.Provider, "model": prev.Model}, "cause": cause})
	case "escalate":
		th := LevelThresholds(level)
		switch cause {
		case CauseCheapStuck:
			req.Failures.CheapIterations = max(req.Failures.CheapIterations, th.CheapIterations)
		case CausePremiumRevise:
			req.Failures.PremiumRevise = max(req.Failures.PremiumRevise, th.PremiumRevise)
		case CauseWatchdogStage2:
			req.Failures.WatchdogStage2 = max(req.Failures.WatchdogStage2, th.WatchdogStage2)
		}
		req.Session = nil // a fresh session seeded with the handoff artifact: no cache to lose
		d, err := r.decide(ctx, req, mode{requireTier: providers.TierPremium, escalatedFrom: pr, handoff: true})
		if err != nil {
			return RoutingDecision{}, err
		}
		return d, r.persist(ctx, req, d, "routing.escalated", map[string]any{"cause": cause, "handoff": true})
	case "deescalate":
		live := req.Session
		req.Failures, req.Attempt, req.Session = FailureCounts{}, 0, nil
		md := mode{deescalatedFrom: pr}
		if cause == CauseBudgetDownshift {
			md.prefer = providers.TierCheap
		}
		d, err := r.decide(ctx, req, md)
		if err != nil {
			return RoutingDecision{}, err
		}
		if cause == CauseBudgetDownshift && live != nil {
			// Inside a live session a downgrade must pay for the cache it loses (docs/model-routing.md s8).
			var saving float64
			if prev.EstCostUSD != nil && d.EstCostUSD != nil {
				saving = *prev.EstCostUSD - *d.EstCostUSD
			}
			var switchUSD float64
			if d.EstCostUSD != nil && d.EstTokens.Input > 0 {
				switchUSD = *d.EstCostUSD / float64(d.EstTokens.Input+d.EstTokens.Output) * float64(live.ContextTokens)
			}
			if !AllowSwitch(true, saving, switchUSD) {
				return RoutingDecision{}, r.blocked(ctx, req, fmt.Sprintf("downshift refused inside a live session: saving $%.4f does not exceed the switch cost $%.4f", saving, switchUSD), nil)
			}
		}
		return d, r.persist(ctx, req, d, "routing.deescalated", map[string]any{"cause": cause})
	}
	return RoutingDecision{}, fmt.Errorf("router: unknown cause %q", cause)
}

// Explain renders the decision.
func (r *RuleRouter) Explain(d RoutingDecision) string { return explain(d) }

var _ ModelRouter = (*RuleRouter)(nil)

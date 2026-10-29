package router

import (
	"sort"
	"time"

	"github.com/kjmaguire/drydock/internal/providers"
)

// This file holds the cheap-first ladder, escalation and fallback decisions as pure functions of their inputs: no
// clock, no database, no network. Every number marked "proposed" comes from docs/model-routing.md s3, is unmeasured, and
// is tuned through the eval harness (brief section 23).

// Candidate is a provider and model the router may choose, with the live facts the rules and filters need.
type Candidate struct {
	Provider           ProviderID
	Model              string
	Tier               providers.Tier
	DataClass          DataClass
	Kind               providers.Kind
	Enabled            bool
	PrivateEligible    bool // false for a plain-http LAN host without the user's attestation
	SupportsTools      bool
	ResponsesValidated bool
	Healthy            bool
	QuotaOK            bool    // QuotaLedger.Check passed
	QuotaRemainingPct  float64 // -1 unknown
	Concurrent         int
	MaxConcurrent      int // 0 = unlimited
	ProbePassed        bool
	// ProbeFailure is how the model failed the tool-call probe ("no_tool_call", "bad_arguments", ...); "" when it passed,
	// was never probed, or the probe could not run (an error says nothing about the model).
	ProbeFailure   string
	Context        int
	AgentNative    bool
	PriceKnown     bool
	BlendedPerM    float64 // USD per million tokens at the 3:1 input:output weight; meaningful when PriceKnown
	PriceUpdatedAt time.Time
	EstCostUSD     *float64 // for this request; nil when the price is unknown
	// RetentionRequired is true when the provider refused the model because the organization has no data retention enabled
	// (providers.Registry.MarkRetentionRequired): no turn can run on it until a person clears the mark.
	RetentionRequired bool
}

// Key identifies the candidate.
func (c Candidate) Key() string { return string(c.Provider) + "\x00" + c.Model }

// Rejected names why a candidate was removed.
func (c Candidate) rejected(filter, detail string) Alternative {
	return Alternative{Provider: c.Provider, Model: c.Model, Tier: c.Tier, Rejected: filter, Detail: detail}
}

// Filter names of the hard filters (docs/model-routing.md s2 step 1). They are code, not user rules.
const (
	FilterDisabled      = "disabled"
	FilterDataClass     = "data_class"
	FilterFreeAgentWork = "free_tier_agent_work"
	FilterUnhealthy     = "unhealthy"
	FilterQuota         = "quota"
	FilterConcurrency   = "host_concurrency"
	FilterContext       = "context_window"
	FilterTools         = "capability_tools"
	FilterRetention     = "retention_required"
)

// HardFilter applies the invariants. An empty or unknown context ceiling counts as private (fail closed).
func HardFilter(req RouteRequest, cands []Candidate) (kept []Candidate, rejected []Alternative) {
	private := req.ContextCeiling != ContextPublicDocs
	need := req.ContextTokens + outputBudget(req)
	for _, c := range cands {
		switch {
		case !c.Enabled:
			rejected = append(rejected, c.rejected(FilterDisabled, "provider disabled"))
		case private && c.DataClass == providers.DataTrainable:
			rejected = append(rejected, c.rejected(FilterDataClass, "context is private; the candidate is trainable"))
		case private && !c.PrivateEligible:
			rejected = append(rejected, c.rejected(FilterDataClass, "plain http on a private network address without the LAN attestation"))
		case c.Tier == providers.TierFree && req.RunKind != RunConsult:
			rejected = append(rejected, c.rejected(FilterFreeAgentWork, "free tier is for CONSULT only"))
		case c.RetentionRequired:
			rejected = append(rejected, c.rejected(FilterRetention, providers.RetentionAdvice))
		case !c.Healthy:
			rejected = append(rejected, c.rejected(FilterUnhealthy, "provider down or quota blocked"))
		case !c.QuotaOK:
			rejected = append(rejected, c.rejected(FilterQuota, "quota window exhausted"))
		case c.MaxConcurrent > 0 && c.Concurrent >= c.MaxConcurrent:
			rejected = append(rejected, c.rejected(FilterConcurrency, "max_concurrent reached"))
		case req.RunKind.WritesFiles() && !c.SupportsTools:
			rejected = append(rejected, c.rejected(FilterTools, "agent work needs tool calling"))
		case req.RunKind.WritesFiles() && c.ProbeFailure != "":
			rejected = append(rejected, c.rejected(FilterTools, "failed the tool-call probe ("+c.ProbeFailure+"): it does not call tools reliably"))
		case c.Context > 0 && req.ContextTokens > 0 && need > c.Context:
			rejected = append(rejected, c.rejected(FilterContext, "context does not fit the model window"))
		default:
			kept = append(kept, c)
		}
	}
	return kept, rejected
}

func outputBudget(req RouteRequest) int {
	if req.EstOutputTokens > 0 {
		return req.EstOutputTokens
	}
	return 2000
}

// Effects are what the matching rules asked for. A request-level Effects holds the rules that do not read candidate.*;
// each candidate also has its own.
type Effects struct {
	PreferTier   providers.Tier
	RequireTier  providers.Tier
	RequireTools bool
	Deny         bool
	DeniedBy     string
	EscalateTo   providers.Tier
	Handoff      bool
	SetEffort    Effort
	SetTeamSize  int
	Penalty      float64
	Applied      []string // rule ids, in application order
	// Fallback is the chain the first matching rule with a fallback set (FallbackRule); see fallback.go.
	Fallback     []string
	FallbackRule string
}

func tierRank(t providers.Tier) int {
	switch t {
	case providers.TierFree:
		return 0
	case providers.TierCheap:
		return 1
	case providers.TierPremium:
		return 2
	}
	return -1
}

// Merge folds a rule's action into the effects. Conflicts resolve toward safety: the higher required tier wins, any
// deny denies, penalties add, an effort or team size set by an earlier (higher priority) rule is kept.
func (e *Effects) Merge(ruleID string, a Action) {
	e.Applied = append(e.Applied, ruleID)
	if a.PreferTier != "" && e.PreferTier == "" {
		e.PreferTier = a.PreferTier
	}
	if a.RequireTier != "" && tierRank(a.RequireTier) > tierRank(e.RequireTier) {
		e.RequireTier = a.RequireTier
	}
	if len(a.RequireCapability) > 0 {
		e.RequireTools = true
	}
	if a.Deny && !e.Deny {
		e.Deny, e.DeniedBy = true, ruleID
	}
	if a.EscalateTo != "" && tierRank(a.EscalateTo) > tierRank(e.EscalateTo) {
		e.EscalateTo, e.Handoff = a.EscalateTo, e.Handoff || a.Handoff
	} else if a.Handoff {
		e.Handoff = true
	}
	if a.SetEffort != "" && e.SetEffort == "" {
		e.SetEffort = a.SetEffort
	}
	if a.SetTeamSize > 0 && e.SetTeamSize == 0 {
		e.SetTeamSize = a.SetTeamSize
	}
	if len(a.Fallback) > 0 && len(e.Fallback) == 0 {
		e.Fallback, e.FallbackRule = append([]string(nil), a.Fallback...), ruleID
	}
	e.Penalty += a.Penalty
}

// Scored is a ranked candidate.
type Scored struct {
	Candidate
	TierIdx int
	Penalty float64
	Effects Effects
}

// TierOrder is the order of tiers to try for a level (docs/model-routing.md s3): cheap first, then premium, with
// free only for CONSULT; Quality and above start at premium. prefer, set by a prefer_tier rule, moves to the front;
// a RequireTier is applied as a filter by Rank.
func TierOrder(level Level, kind RunKind, prefer providers.Tier) []providers.Tier {
	var base []providers.Tier
	switch level {
	case LevelQuality, LevelMaximumQuality:
		base = []providers.Tier{providers.TierPremium, providers.TierCheap, providers.TierFree}
	default: // cheapest, fast, balanced, custom
		base = []providers.Tier{providers.TierCheap, providers.TierFree, providers.TierPremium}
	}
	var order []providers.Tier
	if prefer != "" {
		order = append(order, prefer)
	}
	for _, t := range base {
		if t != prefer {
			order = append(order, t)
		}
	}
	if kind == RunConsult { // free is for CONSULT only (also enforced by HardFilter)
		return order
	}
	out := order[:0]
	for _, t := range order {
		if t != providers.TierFree {
			out = append(out, t)
		}
	}
	return out
}

// Rank filters the candidates by the effects and orders the survivors: tier order first, then the rule penalties,
// then probe-passed models, then the blended price (unknown prices last), then remaining quota, then name, so the same
// inputs always give the same order. perCandidate holds the candidate-level effects by Candidate.Key().
func Rank(order []providers.Tier, global Effects, perCandidate map[string]Effects, cands []Candidate) (ranked []Scored, rejected []Alternative) {
	idx := map[providers.Tier]int{}
	for i, t := range order {
		idx[t] = i
	}
	for _, c := range cands {
		eff := global
		if pc, ok := perCandidate[c.Key()]; ok {
			eff = mergeEffects(global, pc)
		}
		ti, inOrder := idx[c.Tier]
		switch {
		case eff.Deny:
			rejected = append(rejected, c.rejected(eff.DeniedBy, "denied by rule"))
		case eff.RequireTier != "" && c.Tier != eff.RequireTier:
			rejected = append(rejected, c.rejected("require_tier", "a rule requires tier "+string(eff.RequireTier)))
		case eff.RequireTools && !c.SupportsTools:
			rejected = append(rejected, c.rejected("require_capability", "tool calling required"))
		case !inOrder:
			rejected = append(rejected, c.rejected("tier_order", "tier "+string(c.Tier)+" is not in this level's order"))
		default:
			ranked = append(ranked, Scored{Candidate: c, TierIdx: ti, Penalty: eff.Penalty, Effects: eff})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.TierIdx != b.TierIdx {
			return a.TierIdx < b.TierIdx
		}
		if a.Penalty != b.Penalty {
			return a.Penalty < b.Penalty
		}
		if a.ProbePassed != b.ProbePassed {
			return a.ProbePassed
		}
		if a.PriceKnown != b.PriceKnown {
			return a.PriceKnown
		}
		if a.PriceKnown && a.BlendedPerM != b.BlendedPerM {
			return a.BlendedPerM < b.BlendedPerM
		}
		if qa, qb := effQuota(a.QuotaRemainingPct), effQuota(b.QuotaRemainingPct); qa != qb {
			return qa > qb
		}
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		return a.Model < b.Model
	})
	return ranked, rejected
}

// effQuota treats an unknown remaining quota (-1: no limit is known) like a full one, so a known-low window ranks below it.
func effQuota(p float64) float64 {
	if p < 0 {
		return 1
	}
	return p
}

func mergeEffects(g, c Effects) Effects {
	out := g
	out.Applied = append(append([]string(nil), g.Applied...), c.Applied...)
	if c.Deny && !out.Deny {
		out.Deny, out.DeniedBy = true, c.DeniedBy
	}
	if c.RequireTools {
		out.RequireTools = true
	}
	if tierRank(c.RequireTier) > tierRank(out.RequireTier) {
		out.RequireTier = c.RequireTier
	}
	if out.SetEffort == "" {
		out.SetEffort = c.SetEffort
	}
	if out.SetTeamSize == 0 {
		out.SetTeamSize = c.SetTeamSize
	}
	if len(c.Fallback) > 0 { // a chain set for this candidate (per tier or per model) is more specific than a request-wide one
		out.Fallback, out.FallbackRule = c.Fallback, c.FallbackRule
	}
	out.Penalty += c.Penalty
	return out
}

// DefaultEffort is the level's effort (docs/model-routing.md s3; the table gives "low-medium" for Fast, mapped to low).
func DefaultEffort(level Level) Effort {
	switch level {
	case LevelCheapest, LevelFast:
		return EffortLow
	case LevelQuality:
		return EffortHigh
	case LevelMaximumQuality:
		return EffortMax
	}
	return EffortMedium
}

// ResolveEffort picks the effort: an explicit pin wins, then a rule's set_effort, then the level default.
func ResolveEffort(level Level, pin *Pin, ruleEffort Effort) Effort {
	switch {
	case pin != nil && pin.Effort != "":
		return pin.Effort
	case ruleEffort != "":
		return ruleEffort
	}
	return DefaultEffort(level)
}

// ResolveTeamSize is 1 unless a rule sets it, or the level is Maximum Quality and the task is risky (brief section 6,
// docs/model-routing.md s3).
func ResolveTeamSize(level Level, risk bool, ruleSize int) int {
	switch {
	case ruleSize > 0:
		return ruleSize
	case level == LevelMaximumQuality && risk:
		return 2
	}
	return 1
}

// Thresholds are the per-level escalation triggers (docs/model-routing.md s3 "Escalate after"), proposed values.
type Thresholds struct {
	CheapIterations int // cheap iterations without passing checks
	PremiumRevise   int // premium REVISE results
	WatchdogStage2  int
}

// LevelThresholds returns the thresholds for a level.
func LevelThresholds(level Level) Thresholds {
	t := Thresholds{CheapIterations: 3, PremiumRevise: 2, WatchdogStage2: 1} // balanced and custom
	switch level {
	case LevelCheapest, LevelQuality:
		t.CheapIterations = 2
	case LevelFast:
		t.CheapIterations = 3
	case LevelMaximumQuality:
		t.CheapIterations = 1
	}
	return t
}

// Escalation is the verdict of ShouldEscalate.
type Escalation struct {
	Escalate bool
	Trigger  string // watchdog_stage2 | premium_revise | cheap_stuck
	Handoff  bool   // write a handoff artifact and start a fresh session (never seed with the transcript)
}

// ShouldEscalate evaluates the triggers in order of severity: a second watchdog trip, repeated premium REVISE, cheap
// iterations without passing checks (only meaningful while on a non-premium tier).
func ShouldEscalate(level Level, f FailureCounts, checksPassing bool, current providers.Tier) Escalation {
	th := LevelThresholds(level)
	switch {
	case f.WatchdogStage2 >= th.WatchdogStage2:
		return Escalation{true, "watchdog_stage2", true}
	case f.PremiumRevise >= th.PremiumRevise:
		return Escalation{true, "premium_revise", true}
	case current != providers.TierPremium && f.CheapIterations >= th.CheapIterations && !checksPassing:
		return Escalation{true, "cheap_stuck", true}
	}
	return Escalation{}
}

// IterationsUntilEscalation is how many more cheap iterations remain before escalation (0 when due or not applicable).
func IterationsUntilEscalation(level Level, f FailureCounts) int {
	if n := LevelThresholds(level).CheapIterations - f.CheapIterations; n > 0 {
		return n
	}
	return 0
}

// FallbackTierOrder is the tier order after a failure: the same tier, then the next tier up (a fallback never moves
// down: a premium route that failed is not replaced by a weaker model).
func FallbackTierOrder(prev providers.Tier) []providers.Tier {
	switch prev {
	case providers.TierFree:
		return []providers.Tier{providers.TierFree, providers.TierCheap, providers.TierPremium}
	case providers.TierCheap:
		return []providers.Tier{providers.TierCheap, providers.TierPremium}
	}
	return []providers.Tier{providers.TierPremium}
}

// FallbackFor returns the ordered fallback choices after prev failed: the same tier first (other providers and
// models, cheapest first), then the next tier up. pinnedTier, when set, limits the result to that tier: with nothing
// left the caller reports BLOCKED. cands must already have passed HardFilter and exclude nothing about prev.
func FallbackFor(prev Candidate, cands []Candidate, pinnedTier providers.Tier) []Candidate {
	order := FallbackTierOrder(prev.Tier)
	if pinnedTier != "" {
		order = []providers.Tier{pinnedTier}
	}
	var pool []Candidate
	for _, c := range cands {
		if c.Provider == prev.Provider && c.Model == prev.Model {
			continue
		}
		pool = append(pool, c)
	}
	ranked, _ := Rank(order, Effects{}, nil, pool)
	out := make([]Candidate, len(ranked))
	for i, r := range ranked {
		out[i] = r.Candidate
	}
	return out
}

// FallbackCauses are the causes that ask for a fallback; EscalationCauses for an escalation; DeescalationCauses for
// a de-escalation.
func causeKind(c Cause) string {
	switch c {
	case CauseQuotaExhausted, CauseRateLimited, CauseProviderDown, CauseOverloaded, CauseModelUnavailable, CauseRetentionRequired, CauseWallTime:
		return "fallback"
	case CauseCheapStuck, CausePremiumRevise, CauseWatchdogStage2:
		return "escalate"
	case CausePremiumResolved, CauseBudgetDownshift:
		return "deescalate"
	}
	return ""
}

// AllowSwitch reports whether a model change inside a live session is worth its cache loss (docs/model-routing.md
// s8): refused unless the estimated saving exceeds the cost of re-reading the context uncached. A change at a natural
// break (no live session) is always allowed.
func AllowSwitch(sessionLive bool, savingUSD, switchCostUSD float64) bool {
	return !sessionLive || savingUSD > switchCostUSD
}

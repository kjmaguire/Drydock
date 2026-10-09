package router

import (
	"errors"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/providers"
)

func key(d RoutingDecision) string { return string(d.Provider) + "/" + d.Model }

func (w *world) id(p providers.Provider) ProviderID { return ProviderID(p.ID) }

func mustDecide(t *testing.T, r ModelRouter, rq RouteRequest) RoutingDecision {
	t.Helper()
	d, err := r.Decide(t.Context(), rq)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return d
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCheapFirstPicksTheProbedPrivateToolModel(t *testing.T) {
	w := newWorld(t)
	r := w.router()
	d := mustDecide(t, r, req())
	if d.Provider != w.id(w.or) || d.Model != "qwen-cheap" {
		t.Fatalf("chose %s (the probed model must beat the cheaper unprobed one)", key(d))
	}
	if d.Facts.Tier != providers.TierCheap || d.DecidedBy != "rules" || d.Effort != EffortMedium || d.TeamSize != 1 || d.Reservation != "" {
		t.Fatalf("decision %+v", d)
	}
	if len(d.PoliciesApplied) < 3 || d.PoliciesApplied[0] != "invariant:data_class" || d.PoliciesApplied[1] != "invariant:free_tier_consult_only" || !contains(d.PoliciesApplied, "cheap-first-classes") {
		t.Fatalf("policies %v (invariants must come first)", d.PoliciesApplied)
	}
	// 14000 input + 2000 output at 0.12/0.8 per Mtok.
	if d.EstCostUSD == nil || !near(*d.EstCostUSD, (14000*0.12+2000*0.8)/1e6) {
		t.Fatalf("estimated cost %v", d.EstCostUSD)
	}
	if d.EstTokens.Input != 14000 || d.EstTokens.Output != 2000 {
		t.Fatalf("estimate %+v", d.EstTokens)
	}
	if d.Facts.ExcludedTrainable != 2 { // north-mini:free and gem-free
		t.Fatalf("excluded trainable = %d", d.Facts.ExcludedTrainable)
	}
	if !d.Facts.ProbePassed || d.Confidence != 1 {
		t.Fatalf("facts %+v confidence %v", d.Facts, d.Confidence)
	}
	got := map[string]string{}
	for _, a := range d.Alternatives {
		got[a.Model] = a.Rejected
	}
	if got["no-tools"] != FilterTools || got["glm-cheaper"] != "ranked_lower" {
		t.Fatalf("alternatives %v", got)
	}
	for _, a := range d.Alternatives {
		if a.Rejected == FilterDataClass {
			t.Fatal("data-class exclusions are counted, not listed")
		}
	}
	// Persisted and announced.
	var n int
	w.db.Reader().QueryRow(`SELECT count(*) FROM routing_decisions WHERE id=? AND decided_by='rules' AND task_id='task_42' AND model='qwen-cheap'`, d.ID).Scan(&n)
	if n != 1 || len(w.events("routing.decided")) != 1 {
		t.Fatalf("rows=%d events=%d", n, len(w.events("routing.decided")))
	}
	if _, stored, err := w.rec.Load(w.ctx, d.ID); err != nil || stored.Model != d.Model || stored.Created.IsZero() {
		t.Fatalf("load: %v", err)
	}
}

func TestPremiumOnlyClasses(t *testing.T) {
	w := newWorld(t)
	r := w.router()
	for name, mod := range map[string]func(*RouteRequest){
		"architecture":    func(q *RouteRequest) { q.TaskType = "architecture" },
		"security review": func(q *RouteRequest) { q.TaskType = "security_review" },
		"risk":            func(q *RouteRequest) { q.Risk = true },
		"ambiguity":       func(q *RouteRequest) { q.Ambiguity = 0.8 },
		"big final":       func(q *RouteRequest) { q.TaskType, q.DiffLines = "final_review", 400 },
	} {
		q := req()
		mod(&q)
		d := mustDecide(t, r, q)
		if d.Facts.Tier != providers.TierPremium || d.Provider != w.id(w.ant) || d.Model != "prem-1" {
			t.Errorf("%s: chose %s (%s)", name, key(d), d.Facts.Tier)
		}
		if !contains(d.PoliciesApplied, "premium-only") {
			t.Errorf("%s: policies %v", name, d.PoliciesApplied)
		}
	}
	q := req()
	q.TaskType, q.DiffLines = "final_review", 399
	if d := mustDecide(t, r, q); d.Facts.Tier == providers.TierPremium && contains(d.PoliciesApplied, "premium-only") {
		t.Errorf("a 399-line final review is below the premium-only threshold: %v", d.PoliciesApplied)
	}
	// Security role on a premium model carries the rule's high effort.
	q = req()
	q.TaskType, q.Role = "security_review", "Security"
	if d := mustDecide(t, r, q); d.Effort != EffortHigh || !contains(d.PoliciesApplied, "security-high-effort") {
		t.Errorf("security effort %s %v", d.Effort, d.PoliciesApplied)
	}
}

func TestLevelsChangeTheLadder(t *testing.T) {
	w := newWorld(t)
	r := w.router()
	cases := []struct {
		level  Level
		tier   providers.Tier
		effort Effort
	}{
		{LevelCheapest, providers.TierCheap, EffortLow},
		{LevelFast, providers.TierCheap, EffortLow},
		{LevelBalanced, providers.TierCheap, EffortMedium},
		{LevelQuality, providers.TierPremium, EffortHigh},
		{LevelMaximumQuality, providers.TierPremium, EffortMax},
		{LevelCustom, providers.TierCheap, EffortMedium},
	}
	for _, tc := range cases {
		q := req()
		q.TaskType = "implement" // not a cheap-first class: the level alone decides
		q.Level = tc.level
		d := mustDecide(t, r, q)
		if d.Facts.Tier != tc.tier || d.Effort != tc.effort {
			t.Errorf("%s: %s/%s effort %s, want %s/%s", tc.level, key(d), d.Facts.Tier, d.Effort, tc.tier, tc.effort)
		}
	}
	// Maximum Quality never takes the cheap-first shortcut, even for a cheap-first class.
	q := req()
	q.Level = LevelMaximumQuality
	if d := mustDecide(t, r, q); d.Facts.Tier != providers.TierPremium {
		t.Errorf("max quality used %s for test_fix", d.Facts.Tier)
	}
	// And a risky task at maximum quality gets a team of two.
	q.Risk = true
	if d := mustDecide(t, r, q); d.TeamSize != 2 {
		t.Errorf("team size %d", d.TeamSize)
	}
}

func TestPrivateContextNeverReachesTrainableEvenWhenItIsTheOnlyOption(t *testing.T) {
	w := newWorld(t)
	r := w.router()
	// Disable every private provider: only the trainable Gemini key (and free OpenRouter models) remain.
	w.reg.SetEnabled(w.ctx, w.or.ID, false)
	w.reg.SetEnabled(w.ctx, w.ant.ID, false)
	q := req()
	_, err := r.Decide(w.ctx, q)
	var be *BlockedError
	if !errors.Is(err, ErrBlocked) || !errors.As(err, &be) {
		t.Fatalf("err = %v, want BLOCKED", err)
	}
	if len(w.events("routing.blocked")) != 1 {
		t.Fatal("no routing.blocked event")
	}
	// The same state serves public documentation CONSULT from the free trainable tier.
	q.RunKind, q.ContextCeiling, q.TaskType = RunConsult, ContextPublicDocs, "docs"
	d := mustDecide(t, r, q)
	if d.Facts.Tier != providers.TierFree || d.DataClass() != providers.DataTrainable {
		t.Fatalf("consult chose %s (%s)", key(d), d.Facts.Tier)
	}
	// An unspecified ceiling is private.
	q.ContextCeiling = ""
	if _, err := r.Decide(w.ctx, q); !errors.Is(err, ErrBlocked) {
		t.Fatalf("empty ceiling must behave as private: %v", err)
	}
}

func (d RoutingDecision) DataClass() providers.DataClass { return d.Facts.DataClass }

func TestFreeTierIsConsultOnly(t *testing.T) {
	w := newWorld(t)
	// A user policy that would love the free tier cannot get it for agent work.
	r := w.router(func(o *RuleOptions) {
		o.Rules = []Rule{{ID: "love-free", Priority: 1, Enabled: true, When: `true`, Then: Action{PreferTier: providers.TierFree}}}
	})
	q := req()
	q.ContextCeiling = ContextPublicDocs
	d := mustDecide(t, r, q)
	if d.Facts.Tier == providers.TierFree {
		t.Fatalf("free tier used for agent work: %s", key(d))
	}
	// For a CONSULT on public docs it is used.
	q.RunKind = RunConsult
	d = mustDecide(t, r, q)
	if d.Facts.Tier != providers.TierFree {
		t.Fatalf("consult did not use the preferred free tier: %s", key(d))
	}
}

func TestExplicitPinOutranksPoliciesButNeverInvariants(t *testing.T) {
	w := newWorld(t)
	r := w.router()
	// The pinned premium model is used although the cheap-first rules would pick a cheap one, with the pinned effort.
	q := req()
	q.Explicit = &Pin{Provider: w.id(w.ant), Model: "prem-2", Effort: EffortLow, Agent: "claude-1"}
	d := mustDecide(t, r, q)
	if key(d) != w.ant.ID+"/prem-2" || d.DecidedBy != "explicit" || d.Effort != EffortLow || d.Agent.Instance != "claude-1" || !contains(d.PoliciesApplied, "explicit_pin") {
		t.Fatalf("pin decision %+v", d)
	}
	// Pinning only a tier narrows the set.
	q.Explicit = &Pin{Tier: providers.TierPremium}
	if d := mustDecide(t, r, q); d.Facts.Tier != providers.TierPremium || d.DecidedBy != "explicit" {
		t.Fatalf("tier pin: %+v", d)
	}
	// A pin that violates an invariant is BLOCKED with the reason, never silently substituted.
	for name, pin := range map[string]*Pin{
		"trainable provider": {Provider: w.id(w.gem), Model: "gem-free"},
		"free openrouter":    {Provider: w.id(w.or), Model: "north-mini:free"},
		"no tool support":    {Provider: w.id(w.or), Model: "no-tools"},
	} {
		q := req()
		q.Explicit = pin
		_, err := r.Decide(w.ctx, q)
		var be *BlockedError
		if !errors.As(err, &be) || !strings.Contains(be.Reason, "pinned") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// A pin naming an unknown model is blocked too.
	q.Explicit = &Pin{Provider: w.id(w.ant), Model: "does-not-exist"}
	if _, err := r.Decide(w.ctx, q); !errors.Is(err, ErrBlocked) {
		t.Fatalf("unknown pin: %v", err)
	}
	// The pin overrides a deny policy (priority 2 above stored policies in docs/model-routing.md s10).
	deny := w.router(func(o *RuleOptions) {
		o.Rules = []Rule{{ID: "no-anthropic", Priority: 1, Enabled: true, When: `candidate.provider == '` + w.ant.ID + `'`, Then: Action{Deny: true}}}
	})
	q = req()
	q.TaskType = "architecture"
	if _, err := deny.Decide(w.ctx, q); !errors.Is(err, ErrBlocked) {
		t.Fatalf("the deny rule should leave no premium candidate: %v", err)
	}
	q.Explicit = &Pin{Provider: w.id(w.ant), Model: "prem-1"}
	if d, err := deny.Decide(w.ctx, q); err != nil || d.Model != "prem-1" {
		t.Fatalf("an explicit pin should override the deny rule: %v %+v", err, d)
	}
}

func TestBudgetDownshiftAndHardLimit(t *testing.T) {
	w := newWorld(t)
	r := w.router()
	q := req()
	q.Level, q.TaskType = LevelQuality, "implement"
	if d := mustDecide(t, r, q); d.Facts.Tier != providers.TierPremium {
		t.Fatalf("quality level without budget pressure: %s", d.Facts.Tier)
	}
	q.Budget.PctUsed = 0.85
	d := mustDecide(t, r, q)
	if d.Facts.Tier != providers.TierCheap || !d.Facts.BudgetDownshift || !contains(d.PoliciesApplied, "budget-downshift") {
		t.Fatalf("85%% budget: %+v", d)
	}
	q.Risk = true // risky work is never downshifted
	if d := mustDecide(t, r, q); d.Facts.Tier != providers.TierPremium {
		t.Fatalf("risky task downshifted: %s", d.Facts.Tier)
	}
	q.Risk = false
	q.Level = LevelMaximumQuality
	if d := mustDecide(t, r, q); d.Facts.Tier != providers.TierPremium {
		t.Fatalf("maximum quality downshifted: %s", d.Facts.Tier)
	}
	q = req()
	q.Budget = BudgetState{Scope: "task", PctUsed: 1, HardLimit: true}
	_, err := r.Decide(w.ctx, q)
	var be *BlockedError
	if !errors.As(err, &be) || !strings.Contains(be.Reason, "hard limit") {
		t.Fatalf("100%% budget: %v", err)
	}
	// An explicit pin cannot bypass the 100% pause either.
	q.Explicit = &Pin{Provider: w.id(w.ant), Model: "prem-1"}
	if _, err := r.Decide(w.ctx, q); !errors.Is(err, ErrBlocked) {
		t.Fatalf("pin bypassed the budget pause: %v", err)
	}
}

func TestReviewerPrefersADifferentModelAndLowQuotaIsPenalized(t *testing.T) {
	w := newWorld(t)
	r := w.router()
	q := req()
	q.RunKind, q.TaskType = RunReview, "first_pass_review"
	q.Worker = &WorkerRef{Provider: w.id(w.or), Model: "qwen-cheap"}
	d := mustDecide(t, r, q)
	if d.Model != "glm-cheaper" || !hasAlt(d, "qwen-cheap", "reviewer-differs") {
		t.Fatalf("reviewer chose %s, alternatives %+v (the demoted worker model should name the rule)", key(d), d.Alternatives)
	}
	// Penalty, not deny (brief section 12 "when possible"): with only one candidate it is still used.
	// Leave a single usable model in the catalog (real rows, removed with SQL).
	if _, err := w.db.Writer().Exec(`DELETE FROM model_catalog WHERE model <> 'qwen-cheap'`); err != nil {
		t.Fatal(err)
	}
	only := r
	if d := mustDecide(t, only, q); d.Model != "qwen-cheap" {
		t.Fatalf("the penalized worker model was refused outright: %s", key(d))
	}

	// Low remaining quota demotes a candidate (quota-avoid-low).
	w2 := newWorld(t)
	r2 := w2.router()
	w2.ledger.Configure(w2.ctx, providers.Window{ProviderID: w2.or.ID, Model: "qwen-cheap", Kind: providers.WindowRPD, Limit: 100, Used: 95})
	// Configure resets usage, so consume 95 of 100 through Reserve instead.
	w2.ledger.Configure(w2.ctx, providers.Window{ProviderID: w2.or.ID, Model: "qwen-cheap", Kind: providers.WindowRPD, Limit: 20})
	for i := 0; i < 19; i++ {
		w2.ledger.Reserve(w2.ctx, w2.or.ID, "qwen-cheap")
	}
	d = mustDecide(t, r2, req())
	if d.Model != "glm-cheaper" {
		t.Fatalf("a model at 5%% quota should be demoted: chose %s", key(d))
	}
	// Exhausted quota removes the candidate entirely.
	w2.ledger.Reserve(w2.ctx, w2.or.ID, "qwen-cheap")
	d = mustDecide(t, r2, req())
	found := false
	for _, a := range d.Alternatives {
		found = found || (a.Model == "qwen-cheap" && a.Rejected == FilterQuota)
	}
	if d.Model != "glm-cheaper" || !found {
		t.Fatalf("exhausted model: chose %s, alternatives %+v", key(d), d.Alternatives)
	}
	if d.Facts.QuotaRemainingPct != -1 {
		t.Fatalf("quota pct %v (no window configured for glm-cheaper)", d.Facts.QuotaRemainingPct)
	}
}

func hasAlt(d RoutingDecision, model, why string) bool {
	for _, a := range d.Alternatives {
		if a.Model == model && a.Rejected == why {
			return true
		}
	}
	return false
}

func TestEscalationByLevelThresholdsAndRules(t *testing.T) {
	w := newWorld(t)
	r := w.router()
	cases := []struct {
		level Level
		iters int
		tier  providers.Tier
	}{
		{LevelBalanced, 2, providers.TierCheap},
		{LevelBalanced, 3, providers.TierPremium},
		{LevelFast, 2, providers.TierCheap},
		{LevelCheapest, 2, providers.TierPremium},
		{LevelMaximumQuality, 1, providers.TierPremium},
	}
	for _, tc := range cases {
		q := req()
		q.TaskType, q.Level = "implement", tc.level
		if tc.level == LevelMaximumQuality {
			q.TaskType = "docs"
		}
		q.Failures.CheapIterations = tc.iters
		d := mustDecide(t, r, q)
		if d.Facts.Tier != tc.tier {
			t.Errorf("%s with %d failed cheap iterations: %s, want %s (%v)", tc.level, tc.iters, d.Facts.Tier, tc.tier, d.PoliciesApplied)
		}
		if tc.tier == providers.TierPremium && !strings.Contains(d.Reason, "handoff") {
			t.Errorf("%s: an escalation must require a handoff artifact: %q", tc.level, d.Reason)
		}
	}
	// Checks passing cancels the cheap-stuck escalation.
	q := req()
	q.Failures.CheapIterations, q.ChecksPassing = 5, true
	if d := mustDecide(t, r, q); d.Facts.Tier != providers.TierCheap {
		t.Errorf("escalated although checks pass: %s", d.Facts.Tier)
	}
	// Watchdog stage 2 and repeated premium REVISE escalate at any level.
	q = req()
	q.Failures.WatchdogStage2 = 1
	if d := mustDecide(t, r, q); d.Facts.Tier != providers.TierPremium || !contains(d.PoliciesApplied, "escalate-watchdog") {
		t.Errorf("watchdog escalation: %+v", d.PoliciesApplied)
	}
	q = req()
	q.Failures.PremiumRevise = 2
	if d := mustDecide(t, r, q); d.Facts.Tier != providers.TierPremium {
		t.Errorf("premium REVISE escalation: %s", d.Facts.Tier)
	}
	// The explanation tells how close a cheap decision is to escalating.
	q = req()
	q.Failures.CheapIterations = 2
	d := mustDecide(t, r, q)
	if !strings.Contains(d.Facts.NextEscalation, "after 1 more failed iteration (3 cheap iterations)") {
		t.Errorf("next escalation: %q", d.Facts.NextEscalation)
	}
}

func TestNextFallbackChain(t *testing.T) {
	w := newWorld(t)
	r := w.router()
	d1 := mustDecide(t, r, req())
	if d1.Model != "qwen-cheap" {
		t.Fatal(d1.Model)
	}
	// Quota runs out on the chosen model: the next decision is the same tier, another model.
	w.ledger.Penalize(w.ctx, w.or.ID, "qwen-cheap", w.clock.Add(time.Hour), "retry-after")
	d2, err := r.Next(w.ctx, d1, CauseQuotaExhausted)
	if err != nil {
		t.Fatal(err)
	}
	if d2.Model != "glm-cheaper" || d2.Facts.Tier != providers.TierCheap || d2.Facts.FallbackFrom == nil || d2.Facts.FallbackFrom.Model != "qwen-cheap" {
		t.Fatalf("fallback 1: %+v", d2)
	}
	// Then the whole provider goes down: the next tier up.
	w.ledger.Penalize(w.ctx, w.or.ID, "", w.clock.Add(time.Hour), "overloaded")
	d3, err := r.Next(w.ctx, d2, CauseProviderDown)
	if err != nil {
		t.Fatal(err)
	}
	if d3.Facts.Tier != providers.TierPremium || d3.Provider != w.id(w.ant) {
		t.Fatalf("fallback 2: %s %s", key(d3), d3.Facts.Tier)
	}
	// Premium falls back to the other premium model, never down to cheap.
	w.ledger.Penalize(w.ctx, w.ant.ID, "prem-1", w.clock.Add(time.Hour), "overloaded") // the failure is what marks it unavailable
	d4, err := r.Next(w.ctx, d3, CauseOverloaded)
	if err != nil || d4.Model != "prem-2" || d4.Facts.Tier != providers.TierPremium {
		t.Fatalf("fallback 3: %v %+v", err, d4)
	}
	// Nothing left in the tier: BLOCKED, not a weaker model.
	w.ledger.Penalize(w.ctx, w.ant.ID, "prem-2", w.clock.Add(time.Hour), "overloaded")
	if _, err := r.Next(w.ctx, d4, CauseModelUnavailable); !errors.Is(err, ErrBlocked) {
		t.Fatalf("exhausted tier: %v", err)
	}
	if n := len(w.events("routing.fallback")); n != 3 {
		t.Fatalf("%d routing.fallback events", n)
	}
	// The fallback decisions are persisted as decisions of their own.
	var n int
	w.db.Reader().QueryRow(`SELECT count(*) FROM routing_decisions`).Scan(&n)
	if n != 4 {
		t.Fatalf("%d stored decisions", n)
	}
	if _, err := r.Next(w.ctx, RoutingDecision{ID: "dec_missing"}, CauseOverloaded); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown decision: %v", err)
	}
}

func TestNextFallbackRespectsPinnedTier(t *testing.T) {
	w := newWorld(t)
	r := w.router()
	q := req()
	q.Explicit = &Pin{Tier: providers.TierCheap}
	d1 := mustDecide(t, r, q)
	w.ledger.Penalize(w.ctx, w.or.ID, "", w.clock.Add(time.Hour), "down")
	// The only cheap provider is out; the policy pins the cheap tier, so there is no step up: BLOCKED.
	if _, err := r.Next(w.ctx, d1, CauseProviderDown); !errors.Is(err, ErrBlocked) {
		t.Fatalf("pinned tier fell back anyway: %v", err)
	}
	// A pinned model is never replaced.
	q.Explicit = &Pin{Provider: w.id(w.ant), Model: "prem-1"}
	d2 := mustDecide(t, r, q)
	if _, err := r.Next(w.ctx, d2, CauseOverloaded); !errors.Is(err, ErrBlocked) {
		t.Fatalf("pinned model was substituted: %v", err)
	}
}

func TestNextEscalateAndDeescalate(t *testing.T) {
	w := newWorld(t)
	r := w.router()
	q := req()
	q.Session = &SessionState{Provider: w.id(w.or), Model: "qwen-cheap", ContextTokens: 30000, CacheAge: time.Minute}
	cheap := mustDecide(t, r, q)
	if cheap.Model != "qwen-cheap" || cheap.EstTokens.Cached != 14000 || cheap.SwitchCostTokens != 0 {
		t.Fatalf("cheap decision in a live session: %+v", cheap)
	}
	prem, err := r.Next(w.ctx, cheap, CauseCheapStuck)
	if err != nil {
		t.Fatal(err)
	}
	if prem.Facts.Tier != providers.TierPremium || prem.EscalatedFrom == nil || prem.EscalatedFrom.DecisionID != cheap.ID || prem.EscalatedFrom.Model != "qwen-cheap" ||
		!strings.Contains(prem.Reason, "handoff") || prem.SwitchCostTokens != 0 {
		t.Fatalf("escalation: %+v", prem)
	}
	if len(w.events("routing.escalated")) != 1 {
		t.Fatal("no routing.escalated event")
	}
	var from string
	w.db.Reader().QueryRow(`SELECT escalated_from FROM routing_decisions WHERE id=?`, prem.ID).Scan(&from)
	if from != cheap.ID {
		t.Fatalf("escalated_from column %q", from)
	}
	// The premium resolution is done: the next unit starts from the cheap ladder again, not sticky.
	back, err := r.Next(w.ctx, prem, CausePremiumResolved)
	if err != nil {
		t.Fatal(err)
	}
	if back.Facts.Tier != providers.TierCheap || back.DeescalatedFrom == nil || back.DeescalatedFrom.DecisionID != prem.ID {
		t.Fatalf("de-escalation: %+v", back)
	}
	if len(w.events("routing.deescalated")) != 1 {
		t.Fatal("no routing.deescalated event")
	}
	// Premium REVISE and watchdog stage 2 also escalate.
	for _, c := range []Cause{CausePremiumRevise, CauseWatchdogStage2} {
		if d, err := r.Next(w.ctx, cheap, c); err != nil || d.Facts.Tier != providers.TierPremium {
			t.Errorf("%s: %v %+v", c, err, d)
		}
	}
	if _, err := r.Next(w.ctx, cheap, "no_such_cause"); err == nil {
		t.Fatal("unknown cause accepted")
	}
}

func TestBudgetDownshiftInsideALiveSessionMustPayForTheSwitch(t *testing.T) {
	w := newWorld(t)
	r := w.router()
	q := req()
	q.Level, q.TaskType = LevelQuality, "implement"
	q.Session = &SessionState{Provider: w.id(w.ant), Model: "prem-1", ContextTokens: 600000, CacheAge: time.Minute}
	q.ContextTokens = 10000
	prem := mustDecide(t, r, q)
	if prem.Facts.Tier != providers.TierPremium {
		t.Fatal(prem.Facts.Tier)
	}
	// Re-reading 600k tokens uncached costs more than the per-call saving: refused.
	if _, err := r.Next(w.ctx, prem, CauseBudgetDownshift); !errors.Is(err, ErrBlocked) {
		t.Fatalf("expensive switch allowed: %v", err)
	}
	// A small session makes the switch pay.
	q.Session.ContextTokens = 10000
	prem2 := mustDecide(t, r, q)
	d, err := r.Next(w.ctx, prem2, CauseBudgetDownshift)
	if err != nil || d.Facts.Tier != providers.TierCheap {
		t.Fatalf("cheap switch refused: %v %+v", err, d)
	}
}

func TestDecisionsAreDeterministic(t *testing.T) {
	w := newWorld(t)
	r := w.router()
	norm := func(d RoutingDecision) RoutingDecision { d.ID, d.Created = "", time.Time{}; return d }
	a := norm(mustDecide(t, r, req()))
	for i := 0; i < 5; i++ {
		b := norm(mustDecide(t, r, req()))
		if key(a) != key(b) || a.Reason != b.Reason || a.Effort != b.Effort || len(a.Alternatives) != len(b.Alternatives) || strings.Join(a.PoliciesApplied, ",") != strings.Join(b.PoliciesApplied, ",") {
			t.Fatalf("run %d differs:\n%+v\n%+v", i, a, b)
		}
		for j := range a.Alternatives {
			if a.Alternatives[j] != b.Alternatives[j] {
				t.Fatalf("alternative %d differs", j)
			}
		}
	}
	d1, d2 := mustDecide(t, r, req()), mustDecide(t, r, req())
	if r.Explain(d1) != strings.ReplaceAll(r.Explain(d2), d2.ID, d1.ID) && r.Explain(d1) != r.Explain(d2) {
		t.Fatalf("Explain differs:\n%s\n%s", r.Explain(d1), r.Explain(d2))
	}
}

func TestStoredRulesReplaceTheDefaultsAndBadRulesAreSkipped(t *testing.T) {
	w := newWorld(t)
	eng, _ := NewEngine()
	store := NewRuleStore(w.db, eng)
	// Only one stored rule: Database work requires premium. No defaults are active, so cheap-first does not apply.
	if _, err := store.Put(w.ctx, Rule{ID: "db-premium", Priority: 10, Enabled: true, When: `task.role == 'Database'`, Then: Action{RequireTier: providers.TierPremium}}); err != nil {
		t.Fatal(err)
	}
	r := w.router(func(o *RuleOptions) { o.Store = store })
	q := req()
	q.Role = "Database"
	if d := mustDecide(t, r, q); d.Facts.Tier != providers.TierPremium || !contains(d.PoliciesApplied, "db-premium") {
		t.Fatalf("db rule: %+v", d)
	}
	q.Role = "Backend"
	if d := mustDecide(t, r, q); contains(d.PoliciesApplied, "cheap-first-classes") {
		t.Fatalf("defaults applied although rules are stored: %v", d.PoliciesApplied)
	}
	// Editing the stored rule takes effect on the next decision.
	rs, _ := store.List(w.ctx)
	rs[0].When = `task.role == 'Backend'`
	if _, err := store.Put(w.ctx, rs[0]); err != nil {
		t.Fatal(err)
	}
	if d := mustDecide(t, r, q); d.Facts.Tier != providers.TierPremium {
		t.Fatalf("edited rule not applied: %+v", d)
	}
	// A rule that fails at runtime is skipped and reported, not fatal.
	w2 := newWorld(t)
	bad := w2.router(func(o *RuleOptions) {
		o.Rules = []Rule{{ID: "div-zero", Priority: 1, Enabled: true, When: `task.diff_lines / (task.attempt - 2) > 1`, Then: Action{Deny: true}}}
	})
	d := mustDecide(t, bad, req()) // attempt is 2: division by zero
	if !strings.Contains(d.Reason, "failed to evaluate") || d.Model != "qwen-cheap" {
		t.Fatalf("runtime rule error: %+v", d)
	}
	// Request-scoped policies are added; a disabled one is ignored.
	q = req()
	q.Policies = []Rule{{ID: "off", Priority: 1, Enabled: false, When: `true`, Then: Action{Deny: true}}}
	if _, err := w2.router().Decide(w2.ctx, q); err != nil {
		t.Fatalf("disabled policy applied: %v", err)
	}
	q.Policies = []Rule{{ID: "syntax", Priority: 1, Enabled: true, When: `task.type ==`}}
	if _, err := w2.router().Decide(w2.ctx, q); err == nil {
		t.Fatal("a policy that does not compile must fail loudly")
	}
}

func TestExplainRendersTheRecordDeterministically(t *testing.T) {
	w := newWorld(t)
	r := w.router()
	q := req()
	q.Failures.CheapIterations = 2
	d := mustDecide(t, r, q)
	text := r.Explain(d)
	for _, want := range []string{
		"Why this model? qwen-cheap via " + w.or.ID + " (cheap, private)",
		"attempt 2",
		"- Class: test_fix, not risky, diff 38 lines -> ",
		"cheap-first-classes",
		"- Data: context is private; only private providers considered (invariant). 2 trainable candidates excluded.",
		"- Capability: tool calling probe passed.",
		"- Budget: scope at 34%, no downshift.",
		"- Alternatives rejected: ",
		"no-tools (capability_tools)",
		"- Estimated: 14k input / 2k output tokens, about $0.0033 (estimated from catalog price updated " + time.Now().UTC().Format("2006-01-02") + "; not reported)", // the catalog stamps the day it was written
		"- Effort: medium (applied); team size 1.",
		"- Next: escalates to premium after 1 more failed iteration (3 cheap iterations) with a handoff artifact",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("explanation lacks %q:\n%s", want, text)
		}
	}
	// An unknown price is stated, never rendered as zero.
	d.EstCostUSD = nil
	if txt := r.Explain(d); !strings.Contains(txt, "price unknown") || strings.Contains(txt, "$0.0000") {
		t.Errorf("unknown price:\n%s", txt)
	}
	// Public-docs context is described as such.
	pd := d
	pd.Facts.ContextCeiling = ContextPublicDocs
	if txt := r.Explain(pd); !strings.Contains(txt, "public documentation only") {
		t.Errorf("public docs:\n%s", txt)
	}
}

// TestRuleRouterNeverReturnsATrainableTargetForPrivateContext drives Decide with random requests and random provider
// states and asserts the invariant on every answer.
func TestRuleRouterNeverReturnsATrainableTargetForPrivateContext(t *testing.T) {
	w := newWorld(t)
	rng := rand.New(rand.NewSource(11))
	types := []string{"test_fix", "docs", "architecture", "implement", "security_review", "exploration", "first_pass_review"}
	levels := []Level{LevelCheapest, LevelFast, LevelBalanced, LevelQuality, LevelMaximumQuality, LevelCustom, ""}
	kinds := []RunKind{RunAgentTask, RunReview, RunConsult, RunSummarize, RunClassify, RunProbe}
	ceilings := []ContextClass{ContextPrivate, "", ContextPublicDocs}
	pins := []*Pin{nil, nil, nil, {Provider: w.id(w.gem), Model: "gem-free"}, {Provider: w.id(w.or), Model: "north-mini:free"}, {Tier: providers.TierFree}, {Tier: providers.TierPremium}}
	r := w.router()
	decided := 0
	for i := 0; i < 80; i++ {
		w.reg.SetEnabled(w.ctx, w.or.ID, rng.Intn(4) != 0)
		w.reg.SetEnabled(w.ctx, w.ant.ID, rng.Intn(4) != 0)
		q := req()
		q.TaskType, q.Level, q.RunKind, q.ContextCeiling = types[rng.Intn(len(types))], levels[rng.Intn(len(levels))], kinds[rng.Intn(len(kinds))], ceilings[rng.Intn(len(ceilings))]
		q.Risk, q.Explicit = rng.Intn(4) == 0, pins[rng.Intn(len(pins))]
		q.Failures.CheapIterations = rng.Intn(5)
		d, err := r.Decide(w.ctx, q)
		if err != nil {
			if !errors.Is(err, ErrBlocked) {
				t.Fatalf("unexpected error %v", err)
			}
			continue
		}
		decided++
		if q.ContextCeiling != ContextPublicDocs && d.Facts.DataClass == providers.DataTrainable {
			t.Fatalf("INVARIANT VIOLATED: private context routed to %s (%+v)", key(d), q)
		}
		if q.RunKind != RunConsult && d.Facts.Tier == providers.TierFree {
			t.Fatalf("free tier chosen for %s", q.RunKind)
		}
	}
	if decided < 20 {
		t.Fatalf("only %d of 80 requests produced a decision; the property test is not exercising the router", decided)
	}
}

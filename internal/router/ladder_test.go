package router

import (
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/providers"
)

func cand(provider, model string, tier providers.Tier, class DataClass, price float64) Candidate {
	return Candidate{Provider: ProviderID(provider), Model: model, Tier: tier, DataClass: class, Kind: providers.KindDirect, Enabled: true, PrivateEligible: true,
		SupportsTools: true, Healthy: true, QuotaOK: true, QuotaRemainingPct: -1, PriceKnown: true, BlendedPerM: price}
}

func keys(cs []Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, string(c.Provider)+"/"+c.Model)
	}
	return out
}

func scoredKeys(cs []Scored) []string {
	var out []string
	for _, c := range cs {
		out = append(out, string(c.Provider)+"/"+c.Model)
	}
	return out
}

func TestHardFilterTable(t *testing.T) {
	base := cand("p", "m", providers.TierCheap, providers.DataPrivate, 1)
	mod := func(f func(*Candidate)) Candidate { c := base; f(&c); return c }
	cases := []struct {
		name   string
		req    RouteRequest
		c      Candidate
		filter string // "" = kept
	}{
		{"kept", RouteRequest{RunKind: RunAgentTask, ContextCeiling: ContextPrivate}, base, ""},
		{"disabled", RouteRequest{}, mod(func(c *Candidate) { c.Enabled = false }), FilterDisabled},
		{"private ceiling refuses trainable", RouteRequest{ContextCeiling: ContextPrivate}, mod(func(c *Candidate) { c.DataClass = providers.DataTrainable }), FilterDataClass},
		{"empty ceiling is private", RouteRequest{}, mod(func(c *Candidate) { c.DataClass = providers.DataTrainable }), FilterDataClass},
		{"unknown ceiling is private", RouteRequest{ContextCeiling: "internal"}, mod(func(c *Candidate) { c.DataClass = providers.DataTrainable }), FilterDataClass},
		{"public docs allow trainable", RouteRequest{ContextCeiling: ContextPublicDocs, RunKind: RunConsult}, mod(func(c *Candidate) { c.DataClass = providers.DataTrainable }), ""},
		{"LAN without attestation is not private-eligible", RouteRequest{ContextCeiling: ContextPrivate}, mod(func(c *Candidate) { c.PrivateEligible = false }), FilterDataClass},
		{"LAN without attestation is fine for public docs", RouteRequest{ContextCeiling: ContextPublicDocs}, mod(func(c *Candidate) { c.PrivateEligible = false }), ""},
		{"free tier refused for agent work", RouteRequest{ContextCeiling: ContextPublicDocs, RunKind: RunAgentTask}, mod(func(c *Candidate) { c.Tier = providers.TierFree }), FilterFreeAgentWork},
		{"free tier allowed for consult", RouteRequest{ContextCeiling: ContextPublicDocs, RunKind: RunConsult}, mod(func(c *Candidate) { c.Tier = providers.TierFree }), ""},
		{"unhealthy", RouteRequest{}, mod(func(c *Candidate) { c.Healthy = false }), FilterUnhealthy},
		{"quota", RouteRequest{}, mod(func(c *Candidate) { c.QuotaOK = false }), FilterQuota},
		{"host concurrency reached", RouteRequest{}, mod(func(c *Candidate) { c.MaxConcurrent, c.Concurrent = 2, 2 }), FilterConcurrency},
		{"host concurrency available", RouteRequest{}, mod(func(c *Candidate) { c.MaxConcurrent, c.Concurrent = 2, 1 }), ""},
		{"unlimited concurrency", RouteRequest{}, mod(func(c *Candidate) { c.MaxConcurrent, c.Concurrent = 0, 99 }), ""},
		{"agent work needs tools", RouteRequest{RunKind: RunAgentTask}, mod(func(c *Candidate) { c.SupportsTools = false }), FilterTools},
		{"classify does not need tools", RouteRequest{RunKind: RunClassify}, mod(func(c *Candidate) { c.SupportsTools = false }), ""},
		{"context does not fit", RouteRequest{ContextTokens: 100000, EstOutputTokens: 5000}, mod(func(c *Candidate) { c.Context = 100000 }), FilterContext},
		{"context fits", RouteRequest{ContextTokens: 90000, EstOutputTokens: 5000}, mod(func(c *Candidate) { c.Context = 100000 }), ""},
		{"unknown context window is not a filter", RouteRequest{ContextTokens: 900000}, mod(func(c *Candidate) { c.Context = 0 }), ""},
	}
	for _, tc := range cases {
		kept, rej := HardFilter(tc.req, []Candidate{tc.c})
		switch {
		case tc.filter == "" && (len(kept) != 1 || len(rej) != 0):
			t.Errorf("%s: want kept, got rejected %v", tc.name, rej)
		case tc.filter != "" && (len(kept) != 0 || len(rej) != 1 || rej[0].Rejected != tc.filter):
			t.Errorf("%s: want rejected by %s, got kept=%d rej=%v", tc.name, tc.filter, len(kept), rej)
		}
	}
}

// TestDataClassInvariantProperty: for any mix of candidates, a private (or unspecified) ceiling never keeps a
// trainable candidate, and Rank never reintroduces one.
func TestDataClassInvariantProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	tiers := []providers.Tier{providers.TierFree, providers.TierCheap, providers.TierPremium}
	classes := []DataClass{providers.DataPrivate, providers.DataTrainable}
	ceilings := []ContextClass{ContextPrivate, "", "weird", ContextPublicDocs}
	kinds := []RunKind{RunAgentTask, RunConsult, RunReview, RunProbe}
	for i := 0; i < 3000; i++ {
		var cs []Candidate
		for j := 0; j < 1+rng.Intn(8); j++ {
			c := cand("p"+string(rune('a'+rng.Intn(4))), "m"+string(rune('a'+j)), tiers[rng.Intn(3)], classes[rng.Intn(2)], rng.Float64()*10)
			c.PrivateEligible = rng.Intn(5) != 0
			c.Healthy, c.QuotaOK = rng.Intn(6) != 0, rng.Intn(6) != 0
			cs = append(cs, c)
		}
		req := RouteRequest{ContextCeiling: ceilings[rng.Intn(len(ceilings))], RunKind: kinds[rng.Intn(len(kinds))]}
		kept, _ := HardFilter(req, cs)
		eff := Effects{}
		if rng.Intn(2) == 0 {
			eff.PreferTier = tiers[rng.Intn(3)]
		}
		order := TierOrder(Level([]string{"cheapest", "fast", "balanced", "quality", "maximum_quality", "custom", ""}[rng.Intn(7)]), req.RunKind, eff.PreferTier)
		ranked, _ := Rank(order, eff, nil, kept)
		for _, s := range ranked {
			if req.ContextCeiling != ContextPublicDocs && (s.DataClass == providers.DataTrainable || !s.PrivateEligible) {
				t.Fatalf("INVARIANT VIOLATED: ceiling %q kept %+v", req.ContextCeiling, s.Candidate)
			}
			if req.RunKind != RunConsult && s.Tier == providers.TierFree {
				t.Fatalf("free tier kept for %s", req.RunKind)
			}
		}
		for _, s := range FallbackFor(cand("x", "y", tiers[rng.Intn(3)], providers.DataPrivate, 1), kept, "") {
			if req.ContextCeiling != ContextPublicDocs && s.DataClass == providers.DataTrainable {
				t.Fatalf("fallback reintroduced a trainable candidate: %+v", s)
			}
		}
	}
}

func TestTierOrderTable(t *testing.T) {
	C, P, F := providers.TierCheap, providers.TierPremium, providers.TierFree
	cases := []struct {
		level  Level
		kind   RunKind
		prefer providers.Tier
		want   []providers.Tier
	}{
		{LevelBalanced, RunAgentTask, "", []providers.Tier{C, P}},
		{LevelCheapest, RunAgentTask, "", []providers.Tier{C, P}},
		{LevelCheapest, RunConsult, "", []providers.Tier{C, F, P}},
		{LevelFast, RunAgentTask, "", []providers.Tier{C, P}},
		{LevelQuality, RunAgentTask, "", []providers.Tier{P, C}},
		{LevelMaximumQuality, RunReview, "", []providers.Tier{P, C}},
		{LevelQuality, RunConsult, "", []providers.Tier{P, C, F}},
		{LevelCustom, RunAgentTask, "", []providers.Tier{C, P}},
		{"", RunAgentTask, "", []providers.Tier{C, P}},
		{LevelBalanced, RunAgentTask, P, []providers.Tier{P, C}},
		{LevelQuality, RunAgentTask, C, []providers.Tier{C, P}},
		{LevelBalanced, RunAgentTask, F, []providers.Tier{F, C, P}}, // a prefer_tier rule naming free: HardFilter, not TierOrder, enforces consult-only
	}
	for _, tc := range cases {
		got := TierOrder(tc.level, tc.kind, tc.prefer)
		if tc.prefer == F && tc.kind != RunConsult {
			// free is dropped again for non-consult work
			tc.want = []providers.Tier{C, P}
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s/%s/prefer=%q: got %v, want %v", tc.level, tc.kind, tc.prefer, got, tc.want)
		}
	}
}

func TestRankOrdersByTierPenaltyProbePriceQuotaThenName(t *testing.T) {
	order := []providers.Tier{providers.TierCheap, providers.TierPremium}
	probe := cand("b", "probed", providers.TierCheap, providers.DataPrivate, 5)
	probe.ProbePassed = true
	cheapest := cand("b", "cheapest", providers.TierCheap, providers.DataPrivate, 1)
	unknownPrice := cand("b", "unknown", providers.TierCheap, providers.DataPrivate, 0)
	unknownPrice.PriceKnown = false
	sameA := cand("a", "same", providers.TierCheap, providers.DataPrivate, 3)
	sameB := cand("a", "same2", providers.TierCheap, providers.DataPrivate, 3)
	lowQuota := cand("c", "lowq", providers.TierCheap, providers.DataPrivate, 3)
	lowQuota.QuotaRemainingPct = 0.05
	highQuota := cand("c", "highq", providers.TierCheap, providers.DataPrivate, 3)
	highQuota.QuotaRemainingPct = 0.9
	prem := cand("d", "prem", providers.TierPremium, providers.DataPrivate, 0.01)
	in := []Candidate{prem, unknownPrice, sameB, lowQuota, cheapest, probe, highQuota, sameA}
	ranked, rej := Rank(order, Effects{}, nil, in)
	if len(rej) != 0 {
		t.Fatalf("rejected %v", rej)
	}
	// Probe-passed first even though pricier, then by price, ties by quota remaining (higher first), unknown price last in tier, premium last.
	want := []string{"b/probed", "b/cheapest", "a/same", "a/same2", "c/highq", "c/lowq", "b/unknown", "d/prem"}
	if got := scoredKeys(ranked); !reflect.DeepEqual(got, want) {
		t.Fatalf("order\n got %v\nwant %v", got, want)
	}
	// Deterministic: any input order gives the same ranking.
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 50; i++ {
		shuffled := append([]Candidate(nil), in...)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		r2, _ := Rank(order, Effects{}, nil, shuffled)
		if !reflect.DeepEqual(scoredKeys(r2), want) {
			t.Fatalf("ranking depends on input order: %v", scoredKeys(r2))
		}
	}
	// A rule penalty outranks price within the tier.
	pen := map[string]Effects{cheapest.Key(): {Penalty: 1}, probe.Key(): {Penalty: 0.5}}
	r3, _ := Rank(order, Effects{}, pen, in)
	if r3[0].Model != "same" {
		t.Fatalf("penalties ignored: %v", scoredKeys(r3))
	}
}

func TestRankAppliesRequireDenyAndCapabilityEffects(t *testing.T) {
	order := []providers.Tier{providers.TierCheap, providers.TierPremium}
	cheap := cand("a", "cheap", providers.TierCheap, providers.DataPrivate, 1)
	notools := cand("a", "notools", providers.TierCheap, providers.DataPrivate, 0.5)
	notools.SupportsTools = false
	prem := cand("b", "prem", providers.TierPremium, providers.DataPrivate, 9)
	free := cand("c", "free", providers.TierFree, providers.DataPrivate, 0)
	in := []Candidate{cheap, notools, prem, free}

	r, rej := Rank(order, Effects{RequireTier: providers.TierPremium}, nil, in)
	if got := scoredKeys(r); !reflect.DeepEqual(got, []string{"b/prem"}) || len(rej) != 3 {
		t.Fatalf("require_tier: %v rej=%v", got, rej)
	}
	r, _ = Rank(order, Effects{RequireTools: true}, nil, in)
	for _, s := range r {
		if !s.SupportsTools {
			t.Fatalf("require_capability kept %v", s.Model)
		}
	}
	r, rej = Rank(order, Effects{}, map[string]Effects{cheap.Key(): {Deny: true, DeniedBy: "no-cheap"}}, in)
	for _, s := range r {
		if s.Model == "cheap" {
			t.Fatal("denied candidate ranked")
		}
	}
	found := false
	for _, a := range rej {
		found = found || (a.Model == "cheap" && a.Rejected == "no-cheap")
	}
	if !found {
		t.Fatalf("rejection should name the denying rule: %v", rej)
	}
	// The free tier is not in the order for this level: rejected with the tier_order reason, not silently dropped.
	if !hasRejection(rej, "free", "tier_order") {
		t.Fatalf("free not reported: %v", rej)
	}
}

func hasRejection(rej []Alternative, model, filter string) bool {
	for _, a := range rej {
		if a.Model == model && a.Rejected == filter {
			return true
		}
	}
	return false
}

func TestEffectsMergeResolvesConflictsTowardSafety(t *testing.T) {
	var e Effects
	e.Merge("r1", Action{PreferTier: providers.TierCheap, SetEffort: EffortLow, SetTeamSize: 1})
	e.Merge("r2", Action{PreferTier: providers.TierPremium, SetEffort: EffortHigh, SetTeamSize: 3, RequireTier: providers.TierCheap})
	e.Merge("r3", Action{RequireTier: providers.TierPremium, Deny: true, Penalty: 0.5, EscalateTo: providers.TierPremium, Handoff: true})
	e.Merge("r4", Action{Deny: true, Penalty: 0.25, RequireCapability: []string{"tool_calls"}})
	if e.PreferTier != providers.TierCheap || e.SetEffort != EffortLow || e.SetTeamSize != 1 {
		t.Errorf("first rule (higher priority) should win preferences: %+v", e)
	}
	if e.RequireTier != providers.TierPremium {
		t.Errorf("the higher required tier wins: %s", e.RequireTier)
	}
	if !e.Deny || e.DeniedBy != "r3" || e.Penalty != 0.75 || !e.RequireTools || e.EscalateTo != providers.TierPremium || !e.Handoff {
		t.Errorf("effects %+v", e)
	}
	if !reflect.DeepEqual(e.Applied, []string{"r1", "r2", "r3", "r4"}) {
		t.Errorf("applied %v", e.Applied)
	}
}

func TestShouldEscalateTable(t *testing.T) {
	C, P := providers.TierCheap, providers.TierPremium
	cases := []struct {
		name   string
		level  Level
		f      FailureCounts
		passes bool
		tier   providers.Tier
		want   Escalation
	}{
		{"nothing", LevelBalanced, FailureCounts{}, false, C, Escalation{}},
		{"balanced: 2 cheap iterations is not enough", LevelBalanced, FailureCounts{CheapIterations: 2}, false, C, Escalation{}},
		{"balanced: 3 cheap iterations", LevelBalanced, FailureCounts{CheapIterations: 3}, false, C, Escalation{true, "cheap_stuck", true}},
		{"balanced: checks pass, no escalation", LevelBalanced, FailureCounts{CheapIterations: 5}, true, C, Escalation{}},
		{"cheap iterations do not escalate a premium run", LevelBalanced, FailureCounts{CheapIterations: 9}, false, P, Escalation{}},
		{"cheapest: 2", LevelCheapest, FailureCounts{CheapIterations: 2}, false, C, Escalation{true, "cheap_stuck", true}},
		{"fast: 3", LevelFast, FailureCounts{CheapIterations: 2}, false, C, Escalation{}},
		{"quality: 2", LevelQuality, FailureCounts{CheapIterations: 2}, false, C, Escalation{true, "cheap_stuck", true}},
		{"maximum quality: 1", LevelMaximumQuality, FailureCounts{CheapIterations: 1}, false, C, Escalation{true, "cheap_stuck", true}},
		{"custom behaves as balanced", LevelCustom, FailureCounts{CheapIterations: 2}, false, C, Escalation{}},
		{"two premium REVISE", LevelBalanced, FailureCounts{PremiumRevise: 2}, true, P, Escalation{true, "premium_revise", true}},
		{"one premium REVISE", LevelBalanced, FailureCounts{PremiumRevise: 1}, true, P, Escalation{}},
		{"watchdog stage 1 only", LevelBalanced, FailureCounts{WatchdogStage1: 4}, false, C, Escalation{}},
		{"watchdog stage 2 wins", LevelBalanced, FailureCounts{WatchdogStage2: 1, PremiumRevise: 2, CheapIterations: 3}, false, C, Escalation{true, "watchdog_stage2", true}},
	}
	for _, tc := range cases {
		if got := ShouldEscalate(tc.level, tc.f, tc.passes, tc.tier); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
	if n := IterationsUntilEscalation(LevelBalanced, FailureCounts{CheapIterations: 2}); n != 1 {
		t.Errorf("until escalation %d", n)
	}
	if n := IterationsUntilEscalation(LevelBalanced, FailureCounts{CheapIterations: 7}); n != 0 {
		t.Errorf("until escalation %d", n)
	}
}

func TestFallbackForTable(t *testing.T) {
	C, P, F := providers.TierCheap, providers.TierPremium, providers.TierFree
	cheapA := cand("a", "c1", C, providers.DataPrivate, 1)
	cheapB := cand("b", "c2", C, providers.DataPrivate, 2)
	cheapC := cand("c", "c3", C, providers.DataPrivate, 0.5)
	premA := cand("a", "p1", P, providers.DataPrivate, 9)
	premB := cand("b", "p2", P, providers.DataPrivate, 7)
	free := cand("d", "f1", F, providers.DataPrivate, 0)
	pool := []Candidate{cheapA, cheapB, cheapC, premA, premB, free}
	cases := []struct {
		name   string
		prev   Candidate
		pinned providers.Tier
		want   []string
	}{
		{"cheap fails: other cheap by price, then premium by price", cheapA, "", []string{"c/c3", "b/c2", "b/p2", "a/p1"}},
		{"premium fails: only other premium, never down", premA, "", []string{"b/p2"}},
		{"free fails: free, then cheap, then premium", free, "", []string{"c/c3", "a/c1", "b/c2", "b/p2", "a/p1"}},
		{"pinned tier limits the choice", cheapA, C, []string{"c/c3", "b/c2"}},
		{"pinned premium", cheapA, P, []string{"b/p2", "a/p1"}},
	}
	for _, tc := range cases {
		got := keys(FallbackFor(tc.prev, pool, tc.pinned))
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %v\nwant %v", tc.name, got, tc.want)
		}
	}
	// Pinning a tier with nothing left in it yields nothing: the caller reports BLOCKED.
	if got := FallbackFor(premB, []Candidate{premB, cheapA}, P); len(got) != 0 {
		t.Errorf("pinned premium with no other premium: %v", keys(got))
	}
	// The failed candidate is never offered again.
	for _, c := range FallbackFor(cheapA, pool, "") {
		if c.Provider == cheapA.Provider && c.Model == cheapA.Model {
			t.Error("fallback offered the failed candidate")
		}
	}
}

func TestEffortAndTeamSize(t *testing.T) {
	cases := []struct {
		level Level
		want  Effort
	}{{LevelCheapest, EffortLow}, {LevelFast, EffortLow}, {LevelBalanced, EffortMedium}, {LevelQuality, EffortHigh}, {LevelMaximumQuality, EffortMax}, {LevelCustom, EffortMedium}, {"", EffortMedium}}
	for _, tc := range cases {
		if got := ResolveEffort(tc.level, nil, ""); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.level, got, tc.want)
		}
	}
	if got := ResolveEffort(LevelBalanced, nil, EffortHigh); got != EffortHigh {
		t.Errorf("rule effort ignored: %s", got)
	}
	if got := ResolveEffort(LevelBalanced, &Pin{Effort: EffortLow}, EffortHigh); got != EffortLow {
		t.Errorf("an explicit pin outranks a rule: %s", got)
	}
	if got := ResolveEffort(LevelBalanced, &Pin{}, ""); got != EffortMedium {
		t.Errorf("a pin without effort changed the default: %s", got)
	}
	for _, tc := range []struct {
		level Level
		risk  bool
		rule  int
		want  int
	}{{LevelBalanced, true, 0, 1}, {LevelMaximumQuality, false, 0, 1}, {LevelMaximumQuality, true, 0, 2}, {LevelQuality, true, 0, 1}, {LevelBalanced, false, 3, 3}} {
		if got := ResolveTeamSize(tc.level, tc.risk, tc.rule); got != tc.want {
			t.Errorf("%+v: team size %d", tc, got)
		}
	}
}

func TestAllowSwitch(t *testing.T) {
	if !AllowSwitch(false, 0, 100) {
		t.Error("a natural break must always allow the switch")
	}
	if AllowSwitch(true, 1, 2) || AllowSwitch(true, 2, 2) {
		t.Error("inside a live session the saving must exceed the switch cost")
	}
	if !AllowSwitch(true, 3, 2) {
		t.Error("saving above the switch cost should allow it")
	}
}

func TestCauseKinds(t *testing.T) {
	want := map[Cause]string{CauseQuotaExhausted: "fallback", CauseProviderDown: "fallback", CauseOverloaded: "fallback", CauseModelUnavailable: "fallback",
		CauseCheapStuck: "escalate", CausePremiumRevise: "escalate", CauseWatchdogStage2: "escalate", CausePremiumResolved: "deescalate", CauseBudgetDownshift: "deescalate", "bogus": ""}
	for c, k := range want {
		if causeKind(c) != k {
			t.Errorf("%s -> %q", c, causeKind(c))
		}
	}
}

// TestRouterDoesNotImportLLM: the router is deterministic and must stay free of LLM code (docs/token-control.md rule 15).
func TestRouterDoesNotImportLLM(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Skipf("go list unavailable: %v: %s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "drydock/internal/llm") {
			t.Fatalf("internal/router depends on %s", line)
		}
	}
}

// TestDeterministicPackagesDoNotImportLLM is the same boundary over every package rule 15 names (docs/token-control.md, decision
// P119): none of them may depend on internal/llm, directly or through another package. A package added here is one whose job
// rules do; the sanctioned LLM uses (token-control section 9) live elsewhere. The check is proven able to see a dependency: the
// Anthropic client, which is LLM code, must show one.
func TestDeterministicPackagesDoNotImportLLM(t *testing.T) {
	const mod = "github.com/kjmaguire/drydock/internal/"
	llmDeps := func(pkg string) []string {
		t.Helper()
		out, err := exec.Command("go", "list", "-deps", mod+pkg).CombinedOutput()
		if err != nil {
			t.Skipf("go list unavailable: %v: %s", err, out)
		}
		var hits []string
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == mod+"llm" || strings.HasPrefix(line, mod+"llm/") {
				hits = append(hits, line)
			}
		}
		return hits
	}
	for pkg, why := range map[string]string{
		"router":   "routing by rule, the ladder, fallbacks and the explanation",
		"watchdog": "tool spin, error echo, caps and stagnation",
		"room":     "the marker parser, minutes and ADR export, inbox derivation and turn scheduling",
		"checks":   "the check runners and the check summarizer",
		"context":  "the instruction files and the package ceiling",
		"repomap":  "the repo map",
		"budget":   "budgets, reservations and ceilings",
	} {
		if hits := llmDeps(pkg); len(hits) > 0 {
			t.Errorf("internal/%s (%s) depends on %v", pkg, why, hits)
		}
	}
	if hits := llmDeps("llm/anthropic"); len(hits) == 0 {
		t.Fatal("the check sees no dependency on internal/llm even in the Anthropic client: it would pass anything")
	}
}

// A model that answered the tool-call probe wrongly never gets agent work (before it was only ranked lower and could still
// be chosen when the better model was denied); a probe that could not run (an error) says nothing and excludes nothing.
func TestAModelThatFailedTheToolProbeGetsNoAgentWork(t *testing.T) {
	failed := cand("p", "chatty-model", providers.TierCheap, providers.DataPrivate, 0.1)
	failed.ProbeFailure = probeFailure("fail:no_tool_call")
	errored := cand("p", "busy-model", providers.TierCheap, providers.DataPrivate, 0.1)
	errored.ProbeFailure = probeFailure("fail:error:rate_limit")
	kept, rejected := HardFilter(RouteRequest{RunKind: RunAgentTask, ContextCeiling: ContextPrivate}, []Candidate{failed, errored})
	if len(kept) != 1 || kept[0].Model != "busy-model" || len(rejected) != 1 || rejected[0].Rejected != FilterTools || !strings.Contains(rejected[0].Detail, "no_tool_call") {
		t.Fatalf("kept %+v rejected %+v", kept, rejected)
	}
	// a consult does not call tools: the model may still answer it
	if kept, _ := HardFilter(RouteRequest{RunKind: RunConsult, ContextCeiling: ContextPrivate}, []Candidate{failed}); len(kept) != 1 {
		t.Fatalf("consult: %+v", kept)
	}
}

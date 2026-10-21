package router

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/providers"
)

func TestClassifyUnavailableKeepsTheWorksOwnErrorsOut(t *testing.T) {
	for msg, want := range map[string]Cause{
		// what agent CLIs and provider APIs print when the provider or model is unavailable
		`API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`:     CauseOverloaded,
		`API Error: 500 {"type":"error","error":{"type":"api_error","message":"Internal server error"}}`: CauseProviderDown,
		"503 Service Unavailable":                  CauseProviderDown,
		"request failed: connect ECONNREFUSED":     CauseProviderDown,
		"Claude AI usage limit reached|1760000000": CauseRateLimited,
		"429 Too Many Requests":                    CauseRateLimited,
		"[429] RESOURCE_EXHAUSTED: quota exceeded": CauseRateLimited,
		`API Error: 404 {"type":"error","error":{"type":"not_found_error","message":"model: claude-x"}}`:        CauseModelUnavailable,
		"There's an issue with the selected model (opus-9). It may not exist or you may not have access to it.": CauseModelUnavailable,
		"The model `gpt-x` does not exist":       CauseModelUnavailable,
		"ProviderModelNotFoundError: qwen-cheap": CauseModelUnavailable,
		// the work's or the account's own errors: never a fallback
		"":                   "",
		"Prompt is too long": "",
		`API Error: 400 {"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 210000 tokens"}}`: "",
		"stop_reason: refusal":                                        "",
		"Invalid API key · Please run /login":                         "",
		"API Error: 401 authentication_error":                         "",
		"Your credit balance is too low":                              "",
		"3 tests failed: TestSubtract":                                "",
		"agent output ended before the turn finished: panic: nil map": "",
		"the agent session did not start: exec: claude: not found":    "",
	} {
		if got := ClassifyUnavailable(msg); got != want {
			t.Errorf("%q: %q, want %q", msg, got, want)
		}
	}
}

func TestAFallbackChainIsShortAndNamesEachModelOnce(t *testing.T) {
	for _, bad := range [][]string{
		{"a", "b", "c", "d", "e"},
		{"a", "a"},
		{"--dangerous"},
		{"a b"},
		{""},
	} {
		if err := (Action{Fallback: bad}).Validate(); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if err := (Action{Fallback: []string{"h2", "h3"}}).Validate(); err != nil {
		t.Fatal(err)
	}
}

// fixedSource is a labelled test double for the candidate source: the models of one agent's own CLI (agent-native, as
// core's agentSource builds them) plus, to prove a fallback never leaves that CLI, one model of a registered provider.
type fixedSource struct{ cands []Candidate }

func (f *fixedSource) Candidates(context.Context, RouteRequest) ([]Candidate, error) {
	return append([]Candidate(nil), f.cands...), nil
}

func (f *fixedSource) set(model string, fn func(*Candidate)) {
	for i := range f.cands {
		if f.cands[i].Model == model {
			fn(&f.cands[i])
		}
	}
}

func agentCands() *fixedSource {
	c := func(prov, model string, tier providers.Tier) Candidate {
		return Candidate{Provider: ProviderID(prov), Model: model, Tier: tier, DataClass: providers.DataPrivate, Enabled: true, PrivateEligible: true,
			SupportsTools: true, Healthy: true, QuotaOK: true, QuotaRemainingPct: -1, AgentNative: strings.HasPrefix(prov, "agent:")}
	}
	return &fixedSource{cands: []Candidate{
		c("agent:claude", "h1", providers.TierCheap), c("agent:claude", "h2", providers.TierCheap), c("agent:claude", "h3", providers.TierCheap),
		c("agent:claude", "h4", providers.TierCheap), c("agent:claude", "opus", providers.TierPremium), c("openrouter", "elsewhere", providers.TierCheap),
	}}
}

func chainRouter(w *world, src CandidateSource, when string, chain ...string) *RuleRouter {
	return w.router(func(o *RuleOptions) {
		o.Source = src
		o.Rules = []Rule{{ID: "chain-1", Scope: "workspace", Priority: 90, When: when, Then: Action{Fallback: chain}, Enabled: true}}
	})
}

func blockedReason(t *testing.T, err error) string {
	t.Helper()
	var be *BlockedError
	if !errors.As(err, &be) {
		t.Fatalf("want a BlockedError, got %v", err)
	}
	return be.Reason
}

// A turn moves along the chain a rule names: the decision carries the chain, each hop is a recorded decision with a
// routing.fallback event and an explanation, a model that cannot take the turn is skipped (not a hop), and after
// MaxFallbackHops the chain ends even with models left.
func TestTheFallbackChainHopsAlongTheRulesChainAndStopsAtTheHopLimit(t *testing.T) {
	w := newWorld(t)
	src := agentCands()
	r := chainRouter(w, src, "candidate.tier == 'cheap'", "h2", "h3", "h4")
	d1 := mustDecide(t, r, req())
	if d1.Model != "h1" || strings.Join(d1.FallbackChain, ",") != "h2,h3,h4" || d1.FallbackRule != "chain-1" {
		t.Fatalf("first decision %s chain %v rule %q", d1.Model, d1.FallbackChain, d1.FallbackRule)
	}
	if x := r.Explain(d1); !strings.Contains(x, "Fallback chain (rule chain-1): if this model is unavailable before the turn does any work, the turn moves to h2, then h3, then h4; at most 2 hops") {
		t.Fatalf("explanation:\n%s", x)
	}

	src.set("h2", func(c *Candidate) { c.QuotaOK = false }) // h2's window is exhausted too: skipped, not a hop
	d2, err := r.FallbackTurn(w.ctx, req(), d1, CauseOverloaded, []string{"h1"})
	if err != nil {
		t.Fatal(err)
	}
	if d2.Model != "h3" || d2.FallbackHop != 1 || d2.Facts.FallbackFrom == nil || d2.Facts.FallbackFrom.Model != "h1" || d2.Facts.FallbackFrom.Rejected != string(CauseOverloaded) {
		t.Fatalf("hop 1: %s hop %d from %+v", d2.Model, d2.FallbackHop, d2.Facts.FallbackFrom)
	}
	x := r.Explain(d2)
	for _, want := range []string{"Fallback: agent:claude/h1 was unavailable (overloaded), so the turn moved to this model before any work was done: hop 1 of at most 2 in the chain h2, then h3, then h4 (rule chain-1)",
		"Fallback skipped h2: quota: quota window exhausted."} {
		if !strings.Contains(x, want) {
			t.Fatalf("explanation lacks %q:\n%s", want, x)
		}
	}
	evs := w.events("routing.fallback")
	if len(evs) != 1 || !strings.Contains(string(evs[0].Data), `"hop":1`) || !strings.Contains(string(evs[0].Data), `"rule":"chain-1"`) || !strings.Contains(string(evs[0].Data), `"cause":"overloaded"`) {
		t.Fatalf("routing.fallback events %v", evs)
	}

	d3, err := r.FallbackTurn(w.ctx, req(), d2, CauseRateLimited, []string{"h1", "h3"})
	if err != nil || d3.Model != "h4" || d3.FallbackHop != 2 {
		t.Fatalf("hop 2: %v %s %d", err, d3.Model, d3.FallbackHop)
	}
	src.set("h2", func(c *Candidate) { c.QuotaOK = true }) // a model is left, but the turn has taken its hops
	_, err = r.FallbackTurn(w.ctx, req(), d3, CauseOverloaded, []string{"h1", "h3", "h4"})
	if reason := blockedReason(t, err); !strings.Contains(reason, "after 2 fallback hops") {
		t.Fatalf("hop limit: %q", reason)
	}
	if n := len(w.events("routing.fallback")); n != 2 {
		t.Fatalf("%d routing.fallback events", n)
	}
	if n := len(w.events("routing.blocked")); n != 1 {
		t.Fatalf("%d routing.blocked events: the end of the chain is published once, a skipped entry is not", n)
	}
}

// Every model at most once: a chain that names the failed model, or one tried earlier in the turn, never goes back to it.
func TestTheFallbackChainNeverLoops(t *testing.T) {
	w := newWorld(t)
	r := chainRouter(w, agentCands(), "true", "h2", "h1")
	d1 := mustDecide(t, r, req())
	d2, err := r.FallbackTurn(w.ctx, req(), d1, CauseOverloaded, []string{"h1"})
	if err != nil || d2.Model != "h2" {
		t.Fatalf("hop 1: %v %s", err, d2.Model)
	}
	_, err = r.FallbackTurn(w.ctx, req(), d2, CauseOverloaded, []string{"h1", "h2"})
	if reason := blockedReason(t, err); !strings.Contains(reason, "no model left in the chain h2, h1") {
		t.Fatalf("loop: %q", reason)
	}
}

// Without a rule that names a chain there is no fallback: the chain is explicit.
func TestWithoutAChainThereIsNoFallback(t *testing.T) {
	w := newWorld(t)
	r := w.router(func(o *RuleOptions) { o.Source = agentCands() })
	d1 := mustDecide(t, r, req())
	if len(d1.FallbackChain) != 0 {
		t.Fatalf("chain %v without a rule", d1.FallbackChain)
	}
	_, err := r.FallbackTurn(w.ctx, req(), d1, CauseOverloaded, []string{d1.Model})
	if reason := blockedReason(t, err); !strings.Contains(reason, "no routing rule names a fallback chain") {
		t.Fatalf("%q", reason)
	}
	if _, err := r.FallbackTurn(w.ctx, req(), d1, CausePremiumRevise, []string{d1.Model}); err == nil || errors.Is(err, ErrBlocked) {
		t.Fatalf("an escalation cause is not a fallback: %v", err)
	}
}

// A used-up budget stops the chain before any model is tried, even one the human approved over a hard limit.
func TestAUsedUpBudgetStopsTheChain(t *testing.T) {
	w := newWorld(t)
	r := chainRouter(w, agentCands(), "true", "h2")
	d1 := mustDecide(t, r, req())
	q := req()
	q.Budget = BudgetState{Scope: "project", PctUsed: 1.02, HardLimit: false}
	_, err := r.FallbackTurn(w.ctx, q, d1, CauseOverloaded, []string{"h1"})
	if reason := blockedReason(t, err); !strings.Contains(reason, "the project budget is used up (102%), so no fallback hop spends more") {
		t.Fatalf("%q", reason)
	}
	if n := len(w.events("routing.fallback")); n != 0 {
		t.Fatalf("%d fallbacks over a used-up budget", n)
	}
}

// A fallback stays in the same tier or a cheaper one: a premium model in a cheap model's chain is skipped (moving up is
// escalation, with its own thresholds and handoff); a premium turn may fall back to cheap, unless a rule requires premium
// (an escalation is never undone by a fallback).
func TestAFallbackNeverMovesUpATierOrUndoesAnEscalation(t *testing.T) {
	w := newWorld(t)
	r := chainRouter(w, agentCands(), "true", "opus", "h2")
	d1 := mustDecide(t, r, req())
	d2, err := r.FallbackTurn(w.ctx, req(), d1, CauseOverloaded, []string{"h1"})
	if err != nil || d2.Model != "h2" || !hasAlt(d2, "opus", "fallback_skipped") {
		t.Fatalf("cheap turn: %v %s %+v", err, d2.Model, d2.Alternatives)
	}
	if x := r.Explain(d2); !strings.Contains(x, "Fallback skipped opus: tier premium is above cheap: a fallback never moves up a tier (that is escalation).") {
		t.Fatalf("explanation:\n%s", x)
	}

	r = chainRouter(w, agentCands(), "true", "h2")
	q := req()
	q.Level, q.TaskType = LevelQuality, "implement" // premium first
	p1 := mustDecide(t, r, q)
	if p1.Model != "opus" {
		t.Fatalf("quality chose %s", p1.Model)
	}
	p2, err := r.FallbackTurn(w.ctx, q, p1, CauseModelUnavailable, []string{"opus"})
	if err != nil || p2.Model != "h2" || p2.Facts.Tier != providers.TierCheap {
		t.Fatalf("premium to cheap: %v %s", err, p2.Model)
	}

	q = req()
	q.Failures.CheapIterations = 3 // escalated: the level requires premium
	e1 := mustDecide(t, r, q)
	if e1.Model != "opus" || !e1.Handoff {
		t.Fatalf("escalation chose %s handoff %v", e1.Model, e1.Handoff)
	}
	_, err = r.FallbackTurn(w.ctx, q, e1, CauseOverloaded, []string{"opus"})
	if reason := blockedReason(t, err); !strings.Contains(reason, "no model left in the chain h2") {
		t.Fatalf("escalated turn fell back: %q", reason)
	}
}

// A turn on an agent CLI's own login never leaves that CLI's provider, even when the chain names a model another
// provider has (decision D1: a subscription is never moved to another provider behind the person's back).
func TestAFallbackNeverLeavesTheAgentsOwnCLI(t *testing.T) {
	w := newWorld(t)
	r := chainRouter(w, agentCands(), "true", "elsewhere", "h3")
	d1 := mustDecide(t, r, req())
	d2, err := r.FallbackTurn(w.ctx, req(), d1, CauseRateLimited, []string{"h1"})
	if err != nil || d2.Provider != "agent:claude" || d2.Model != "h3" {
		t.Fatalf("%v %s", err, key(d2))
	}
	if !hasAlt(d2, "elsewhere", "fallback_skipped") {
		t.Fatalf("alternatives %+v", d2.Alternatives)
	}
}

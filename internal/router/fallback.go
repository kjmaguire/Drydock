package router

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The fallback chain of a turn (docs/model-routing.md s8 "Fallback chain", decision P53 (b)). A routing rule may name, in
// its fallback action, the models a turn moves to when the chosen model or its provider is unavailable: rate-limited,
// overloaded, a 5xx, the provider down, or the model not found. The chain is explicit (no rule, no fallback), bounded
// (MaxFallbackChain entries, MaxFallbackHops re-run turns, every model at most once per turn, so it cannot loop),
// never moves up a tier (that is escalation, which has its own thresholds and handoff), never leaves the provider of an
// agent's own CLI (a subscription is never moved to another provider, D1), and stops when a budget in scope is used up.
// Errors that are the work's fault (a refusal, a prompt that is too long, a failing test) never fall back.

const (
	// MaxFallbackHops is how many times one turn may be re-run on a model of its chain.
	MaxFallbackHops = 2
	// MaxFallbackChain is the longest chain a rule may name; entries that cannot be used are skipped without a hop.
	MaxFallbackChain = 4
)

func validateChain(chain []string) error {
	if len(chain) > MaxFallbackChain {
		return fmt.Errorf("a fallback chain names at most %d models", MaxFallbackChain)
	}
	seen := map[string]bool{}
	for _, m := range chain {
		switch {
		case strings.TrimSpace(m) != m || m == "":
			return fmt.Errorf("fallback model %q: a model name without spaces", m)
		case strings.HasPrefix(m, "-") || strings.ContainsAny(m, " \t\r\n"):
			return fmt.Errorf("fallback model %q is not a model name", m)
		case seen[m]:
			return fmt.Errorf("fallback model %q is named twice", m)
		}
		seen[m] = true
	}
	return nil
}

// Patterns of a provider or model that is unavailable, as agent CLIs and provider APIs word it. They are matched on the
// error a turn ended with (an API error the CLI printed as its result, or the error the turn failed with), never on the
// agent's own output. notOurs is checked first: those errors are the work's or the account's, not the provider's.
var (
	notOurs = regexp.MustCompile(`(?i)refusal|prompt is too long|context (length|window)|maximum context|too many tokens|invalid_request_error|` +
		`invalid api key|authenticat|unauthori[sz]ed|permission|forbidden|\b40[13]\b|credit balance|billing|insufficient_quota|test(s)? failed|failing test`)
	unavailable = []struct {
		cause Cause
		re    *regexp.Regexp
	}{
		{CauseModelUnavailable, regexp.MustCompile(`(?i)model[_ ]not[_ ]found|not_found_error|no such model|unknown model|model .{0,80}(does not exist|not found|is not available|unavailable)|` +
			`issue with the selected model|no endpoints found|ProviderModelNotFound`)},
		{CauseOverloaded, regexp.MustCompile(`(?i)overloaded|\b529\b|over capacity|at capacity`)},
		{CauseRateLimited, regexp.MustCompile(`(?i)rate[_ -]?limit|\b429\b|too many requests|resource_exhausted|usage limit|limit reached`)},
		{CauseProviderDown, regexp.MustCompile(`(?i)\b50[0234]\b|internal server error|bad gateway|service unavailable|gateway time-?out|api_error|` +
			`ECONNREFUSED|ECONNRESET|ENOTFOUND|connection (refused|reset)|fetch failed|network error|unavailable`)},
	}
)

// ClassifyUnavailable names why a provider or model was unavailable from the error a turn ended with, or returns "" when
// the error is not about availability (the turn's own failure, which never falls back).
func ClassifyUnavailable(msg string) Cause {
	msg = strings.TrimSpace(msg)
	if msg == "" || notOurs.MatchString(msg) {
		return ""
	}
	for _, u := range unavailable {
		if u.re.MatchString(msg) {
			return u.cause
		}
	}
	return ""
}

// FallbackTurn chooses the model a turn moves to after prev, the decision it ran on, failed with an unavailability cause.
// req is the turn's request with the budget state read again just now. tried holds the models this turn already ran on
// (prev's included). It returns a recorded decision (routing.decided and routing.fallback), or a BlockedError that says
// why the chain ends (also published as routing.blocked).
func (r *RuleRouter) FallbackTurn(ctx context.Context, req RouteRequest, prev RoutingDecision, cause Cause, tried []string) (RoutingDecision, error) {
	if causeKind(cause) != "fallback" {
		return RoutingDecision{}, fmt.Errorf("router: %q is not a fallback cause", cause)
	}
	stop := func(reason string, rej []Alternative) (RoutingDecision, error) {
		return RoutingDecision{}, r.blocked(ctx, req, "fallback chain ended: "+reason, rej)
	}
	hop := len(tried)
	switch {
	case len(prev.FallbackChain) == 0:
		return stop(fmt.Sprintf("%s was unavailable (%s) and no routing rule names a fallback chain for it", prev.Model, cause), nil)
	case hop > MaxFallbackHops:
		return stop(fmt.Sprintf("%s was unavailable (%s) after %d fallback hops, the most one turn may take", prev.Model, cause, MaxFallbackHops), nil)
	}
	if reason := BudgetStopsFallback(req.Budget); reason != "" {
		return stop(reason, nil)
	}
	from := &Alternative{Provider: prev.Provider, Model: prev.Model, Tier: prev.Facts.Tier, Rejected: string(cause)}
	m := mode{exclude: &PriorRef{DecisionID: prev.ID, Provider: prev.Provider, Model: prev.Model, Tier: prev.Facts.Tier}, fallbackFrom: from, quiet: true}
	if strings.HasPrefix(string(prev.Provider), "agent:") {
		m.onlyProvider = prev.Provider // an agent CLI's own login: the turn stays with that CLI and its account
	}
	var skipped []Alternative
	done := map[string]bool{}
	for _, t := range tried {
		done[t] = true
	}
	for _, next := range prev.FallbackChain {
		if done[next] {
			continue // every model at most once per turn: the chain cannot loop
		}
		m.only = next
		d, err := r.decide(ctx, req, m)
		if err != nil {
			skipped = append(skipped, Alternative{Provider: m.onlyProvider, Model: next, Rejected: "fallback_skipped", Detail: blockedDetail(err)})
			continue
		}
		if tierRank(d.Facts.Tier) > tierRank(prev.Facts.Tier) {
			skipped = append(skipped, Alternative{Provider: d.Provider, Model: next, Tier: d.Facts.Tier, Rejected: "fallback_skipped",
				Detail: fmt.Sprintf("tier %s is above %s: a fallback never moves up a tier (that is escalation)", d.Facts.Tier, prev.Facts.Tier)})
			continue
		}
		if est := d.EstCostUSD; est != nil && req.Budget.Scope != "" && req.Budget.RemainingUSD > 0 && *est > req.Budget.RemainingUSD {
			skipped = append(skipped, Alternative{Provider: d.Provider, Model: next, Tier: d.Facts.Tier, Rejected: "fallback_skipped",
				Detail: fmt.Sprintf("estimated $%.4f is more than the $%.4f left in the %s budget", *est, req.Budget.RemainingUSD, req.Budget.Scope)})
			continue
		}
		d.FallbackChain, d.FallbackRule, d.FallbackHop = prev.FallbackChain, prev.FallbackRule, hop
		d.Alternatives = append(skipped, d.Alternatives...)
		d.Reason = fmt.Sprintf("%s/%s (%s): fallback hop %d of at most %d, %s/%s was unavailable (%s); chain %s from rule %s",
			d.Provider, d.Model, d.Facts.Tier, hop, MaxFallbackHops, prev.Provider, prev.Model, cause, strings.Join(prev.FallbackChain, ", "), prev.FallbackRule)
		if d.Handoff {
			d.Reason += "; handoff artifact required"
		}
		return d, r.persist(ctx, req, d, "routing.fallback", map[string]any{"from": map[string]any{"provider": prev.Provider, "model": prev.Model, "decisionId": prev.ID},
			"cause": cause, "hop": hop, "maxHops": MaxFallbackHops, "rule": prev.FallbackRule, "skipped": len(skipped)})
	}
	return stop(fmt.Sprintf("%s was unavailable (%s) and no model left in the chain %s can take the turn", prev.Model, cause, strings.Join(prev.FallbackChain, ", ")), skipped)
}

// BudgetStopsFallback says why a budget in scope rules out a fallback hop ("" when none does): a hop re-runs a turn, which
// spends again, so it is never taken once a budget is used up, even when a human approved this turn over a hard limit.
func BudgetStopsFallback(b BudgetState) string {
	if b.Scope != "" && b.PctUsed >= 1 {
		return fmt.Sprintf("the %s budget is used up (%.0f%%), so no fallback hop spends more", b.Scope, b.PctUsed*100)
	}
	return ""
}

func blockedDetail(err error) string {
	var be *BlockedError
	if !errors.As(err, &be) {
		return err.Error()
	}
	for _, a := range be.Alternatives {
		if a.Detail != "" {
			return a.Rejected + ": " + a.Detail
		}
	}
	return be.Reason
}

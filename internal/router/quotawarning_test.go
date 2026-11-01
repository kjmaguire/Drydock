package router

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/providers"
)

// Decision P128: a subscription's allowed_warning (Candidate.QuotaWarning) moves the turns the ladder's rules allow to the cheap
// tier first, and "Why this model?" says so; the turns the rules keep premium stay premium, with the warning shown.
func TestASubscriptionWarningDownshiftsPremiumUse(t *testing.T) {
	const warn = "Claude Code reported its seven day limit near (allowed_warning, resets 2026-11-02 18:00 UTC)"
	w := newWorld(t)
	src := &fixedSource{cands: []Candidate{
		{Provider: "agent:claude", Model: "opus", Tier: providers.TierPremium, DataClass: providers.DataPrivate, Enabled: true, PrivateEligible: true,
			SupportsTools: true, Healthy: true, QuotaOK: true, QuotaRemainingPct: -1, AgentNative: true},
		{Provider: "agent:claude", Model: "sonnet", Tier: providers.TierCheap, DataClass: providers.DataPrivate, Enabled: true, PrivateEligible: true,
			SupportsTools: true, Healthy: true, QuotaOK: true, QuotaRemainingPct: -1, AgentNative: true},
	}}
	r := w.router(func(o *RuleOptions) { o.Source = src })
	work := func(level Level) RouteRequest {
		rq := req()
		rq.TaskType, rq.Level = "", level // a work turn carries no class; Quality starts at the premium tier
		return rq
	}

	if d := mustDecide(t, r, work(LevelQuality)); d.Model != "opus" || d.Facts.QuotaWarning != "" || d.Facts.QuotaDownshift {
		t.Fatalf("without a warning: %s %+v", d.Model, d.Facts)
	}
	src.set("opus", func(c *Candidate) { c.QuotaWarning = warn })
	src.set("sonnet", func(c *Candidate) { c.QuotaWarning = warn }) // an account-wide limit covers both
	d := mustDecide(t, r, work(LevelQuality))
	if d.Model != "sonnet" || !d.Facts.QuotaDownshift || d.Facts.QuotaWarning != warn || !contains(d.PoliciesApplied, QuotaWarningRule) {
		t.Fatalf("warned: %s %+v %v", d.Model, d.Facts, d.PoliciesApplied)
	}
	if x := r.Explain(d); !strings.Contains(x, "- Subscription limit: "+warn+", so premium use is downshifted: the cheap tier is tried first until the limit resets or is reported allowed again.") {
		t.Fatalf("explanation:\n%s", x)
	}
	// the explanation is rendered from the stored record alone
	if _, stored, err := w.rec.Load(w.ctx, d.ID); err != nil || !stored.Facts.QuotaDownshift || stored.Facts.QuotaWarning != warn {
		t.Fatalf("stored %+v: %v", stored.Facts, err)
	}

	// the turns the rules keep premium: an architecture class (premium-only), a risky task, an escalation, Maximum Quality, a pin
	keep := map[string]RouteRequest{}
	arch := work(LevelQuality)
	arch.TaskType = "architecture"
	keep["premium-only class"] = arch
	risky := work(LevelQuality)
	risky.Risk = true
	keep["risky task"] = risky
	stuck := work(LevelBalanced)
	stuck.Failures.CheapIterations = 3
	keep["escalation"] = stuck
	keep["maximum quality"] = work(LevelMaximumQuality)
	pinned := work(LevelQuality)
	pinned.Explicit = &Pin{Model: "opus"}
	keep["pin"] = pinned
	for name, rq := range keep {
		d := mustDecide(t, r, rq)
		if d.Model != "opus" || d.Facts.QuotaDownshift || d.Facts.QuotaWarning != warn {
			t.Errorf("%s: %s %+v", name, d.Model, d.Facts)
		}
		if x := r.Explain(d); !strings.Contains(x, "- Subscription limit: "+warn+"; no downshift for this turn") {
			t.Errorf("%s: explanation:\n%s", name, x)
		}
	}

	// a warning on the cheap model alone is no reason to change anything; with no cheap model left the premium one runs
	src.set("opus", func(c *Candidate) { c.QuotaWarning = "" })
	if d := mustDecide(t, r, work(LevelQuality)); d.Model != "opus" || d.Facts.QuotaWarning != "" {
		t.Errorf("cheap warned only: %s %+v", d.Model, d.Facts)
	}
	src.set("opus", func(c *Candidate) { c.QuotaWarning = warn })
	src.set("sonnet", func(c *Candidate) { c.QuotaOK = false })
	if d := mustDecide(t, r, work(LevelQuality)); d.Model != "opus" || d.Facts.QuotaDownshift {
		t.Errorf("no cheap model: %s %+v", d.Model, d.Facts)
	}
}

func TestQuotaDownshiftDecides(t *testing.T) {
	prem := Candidate{Tier: providers.TierPremium, QuotaWarning: "near"}
	cheap := Candidate{Tier: providers.TierCheap}
	for _, tc := range []struct {
		name   string
		level  Level
		risk   bool
		pinned bool
		prefer providers.Tier
		kept   []Candidate
		want   bool
	}{
		{"warned premium", LevelBalanced, false, false, "", []Candidate{prem, cheap}, true},
		{"maximum quality", LevelMaximumQuality, false, false, "", []Candidate{prem, cheap}, false},
		{"risky", LevelQuality, true, false, "", []Candidate{prem, cheap}, false},
		{"pinned", LevelQuality, false, true, "", []Candidate{prem, cheap}, false},
		{"a rule prefers a tier", LevelQuality, false, false, providers.TierPremium, []Candidate{prem, cheap}, false},
		{"no warning", LevelQuality, false, false, "", []Candidate{{Tier: providers.TierPremium}, cheap}, false},
	} {
		got, warning := QuotaDownshift(tc.level, RouteRequest{Risk: tc.risk}, tc.pinned, tc.prefer, tc.kept)
		if got != tc.want || (tc.name != "no warning") != (warning == "near") {
			t.Errorf("%s: %v %q", tc.name, got, warning)
		}
	}
}

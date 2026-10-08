package router

import (
	"fmt"
	"sort"
	"strings"
)

// explain renders "Why this model?" (docs/model-routing.md s11) from the decision record alone: deterministic, no LLM,
// estimated and reported figures labeled apart.
func explain(d RoutingDecision) string {
	f := d.Facts
	var b strings.Builder
	fmt.Fprintf(&b, "Why this model? %s via %s (%s, %s)", d.Model, d.Provider, f.Tier, f.DataClass)
	switch {
	case f.TaskID != "" && f.RunKind != "":
		fmt.Fprintf(&b, " for task %s (%s run)", f.TaskID, f.RunKind)
	case f.RunKind != "":
		fmt.Fprintf(&b, " for a run of kind %s", f.RunKind)
	}
	if f.Attempt > 0 {
		fmt.Fprintf(&b, ", attempt %d", f.Attempt)
	}
	b.WriteString(".\n")
	if f.TaskType != "" {
		risk := "not risky"
		if f.Risk {
			risk = "risky"
		}
		pol := "no rule matched; the level ladder decided"
		if len(d.PoliciesApplied) > 0 {
			pol = strings.Join(d.PoliciesApplied, ", ")
		}
		fmt.Fprintf(&b, "- Class: %s, %s, diff %d lines -> %s (decided by %s)\n", f.TaskType, risk, f.DiffLines, pol, d.DecidedBy)
	}
	switch f.ContextCeiling {
	case ContextPublicDocs:
		b.WriteString("- Data: context is public documentation only; trainable providers are allowed for it.\n")
	default:
		fmt.Fprintf(&b, "- Data: context is private; only private providers considered (invariant). %d trainable candidates excluded.\n", f.ExcludedTrainable)
	}
	if f.ProbePassed {
		b.WriteString("- Capability: tool calling probe passed.\n")
	}
	switch {
	case f.QuotaRemainingPct >= 0:
		fmt.Fprintf(&b, "- Quota: %.0f%% of the tightest window remaining.\n", f.QuotaRemainingPct*100)
	default:
		b.WriteString("- Quota: remaining quota unknown.\n")
	}
	if f.BudgetDownshift {
		fmt.Fprintf(&b, "- Budget: scope at %.0f%%, downshifted to a cheaper tier.\n", f.BudgetPctUsed*100)
	} else {
		fmt.Fprintf(&b, "- Budget: scope at %.0f%%, no downshift.\n", f.BudgetPctUsed*100)
	}
	if f.FallbackFrom != nil {
		fmt.Fprintf(&b, "- Fallback: %s/%s was unavailable (%s).\n", f.FallbackFrom.Provider, f.FallbackFrom.Model, f.FallbackFrom.Rejected)
	}
	if d.EscalatedFrom != nil {
		fmt.Fprintf(&b, "- Escalated from %s/%s (%s) with a handoff artifact.\n", d.EscalatedFrom.Provider, d.EscalatedFrom.Model, d.EscalatedFrom.Tier)
	}
	if d.DeescalatedFrom != nil {
		fmt.Fprintf(&b, "- De-escalated from %s/%s (%s); the premium model is not kept sticky.\n", d.DeescalatedFrom.Provider, d.DeescalatedFrom.Model, d.DeescalatedFrom.Tier)
	}
	if len(d.Alternatives) > 0 {
		alts := append([]Alternative(nil), d.Alternatives...)
		sort.SliceStable(alts, func(i, j int) bool {
			return string(alts[i].Provider)+alts[i].Model < string(alts[j].Provider)+alts[j].Model
		})
		var parts []string
		for _, a := range alts {
			parts = append(parts, fmt.Sprintf("%s/%s (%s)", a.Provider, a.Model, a.Rejected))
		}
		fmt.Fprintf(&b, "- Alternatives rejected: %s\n", strings.Join(parts, ", "))
	}
	e := d.EstTokens
	if d.EstCostUSD != nil {
		date := "unknown date"
		if !f.PriceUpdatedAt.IsZero() {
			date = f.PriceUpdatedAt.UTC().Format("2006-01-02")
		}
		fmt.Fprintf(&b, "- Estimated: %s input / %s output tokens, about $%.4f (estimated from catalog price updated %s; not reported)\n", kfmt(e.Input), kfmt(e.Output), *d.EstCostUSD, date)
	} else {
		fmt.Fprintf(&b, "- Estimated: %s input / %s output tokens; price unknown, so no cost estimate.\n", kfmt(e.Input), kfmt(e.Output))
	}
	if d.Effort != "" {
		applied := "applied"
		if !d.EffortApplied {
			applied = "recorded; the adapter has no effort control"
		}
		fmt.Fprintf(&b, "- Effort: %s (%s); team size %d.\n", d.Effort, applied, d.TeamSize)
	}
	if f.NextEscalation != "" {
		fmt.Fprintf(&b, "- Next: %s\n", f.NextEscalation)
	}
	return b.String()
}

func kfmt(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

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
	if sig, ok := TurnSignals(f.RunKind); ok && sig.Why != "" {
		fmt.Fprintf(&b, "- Turn: %s; %s.\n", sig.Label, sig.Why)
	}
	switch f.LevelSource {
	case LevelFromProject:
		fmt.Fprintf(&b, "- Level: %s, the project's own level (its settings).\n", LevelWords(f.Level))
	case LevelFromWorkspace:
		fmt.Fprintf(&b, "- Level: %s, the workspace's level (the project sets none of its own).\n", LevelWords(f.Level))
	}
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
	switch {
	case strings.HasPrefix(string(d.Provider), "agent:"):
		b.WriteString("- Capability: runs in the agent's own CLI, which brings its own tools (the tool-call probe does not apply).\n")
	case f.ProbePassed:
		b.WriteString("- Capability: tool calling probe passed.\n")
	}
	switch {
	case f.QuotaRemainingPct >= 0:
		fmt.Fprintf(&b, "- Quota: %.0f%% of the tightest window remaining.\n", f.QuotaRemainingPct*100)
	default:
		b.WriteString("- Quota: remaining quota unknown.\n")
	}
	scope := "scope"
	if f.BudgetScope != "" {
		scope = f.BudgetScope + " budget"
	}
	switch {
	case f.BudgetDownshift:
		fmt.Fprintf(&b, "- Budget: %s at %.0f%%, downshifted to a cheaper tier.\n", scope, f.BudgetPctUsed*100)
	case f.BudgetScope == "" && f.BudgetPctUsed == 0:
		b.WriteString("- Budget: no budget applies.\n")
	default:
		fmt.Fprintf(&b, "- Budget: %s at %.0f%%, no downshift.\n", scope, f.BudgetPctUsed*100)
	}
	chain := strings.Join(d.FallbackChain, ", then ")
	switch {
	case f.FallbackFrom != nil && d.FallbackHop > 0:
		fmt.Fprintf(&b, "- Fallback: %s/%s was unavailable (%s), so the turn moved to this model before any work was done: hop %d of at most %d in the chain %s (rule %s); a fallback stays in the same tier or a cheaper one.\n",
			f.FallbackFrom.Provider, f.FallbackFrom.Model, f.FallbackFrom.Rejected, d.FallbackHop, MaxFallbackHops, chain, d.FallbackRule)
	case f.FallbackFrom != nil:
		fmt.Fprintf(&b, "- Fallback: %s/%s was unavailable (%s).\n", f.FallbackFrom.Provider, f.FallbackFrom.Model, f.FallbackFrom.Rejected)
	case len(d.FallbackChain) > 0:
		fmt.Fprintf(&b, "- Fallback chain (rule %s): if this model is unavailable before the turn does any work, the turn moves to %s; at most %d hops, never to a higher tier.\n",
			d.FallbackRule, chain, MaxFallbackHops)
	}
	for _, a := range d.Alternatives {
		if a.Rejected == "fallback_skipped" {
			fmt.Fprintf(&b, "- Fallback skipped %s: %s.\n", a.Model, a.Detail)
		}
	}
	if t := f.Tiebreak; t != nil {
		switch {
		case t.DryRun:
			fmt.Fprintf(&b, "- Tiebreak: the ranking leaves %s within %.0f%% of each other; a real decision asks %s to choose between them (a dry run does not call it).\n",
				strings.Join(t.Between, ", "), TiebreakMargin*100, orStr(t.By, "the cheap model"))
		case t.Failed != "":
			fmt.Fprintf(&b, "- Tiebreak: the ranking left %s within %.0f%% of each other; the tiebreak call failed (%s), so the ranking's choice stands.\n",
				strings.Join(t.Between, ", "), TiebreakMargin*100, t.Failed)
		default:
			why := ""
			if t.Why != "" {
				why = ": \"" + t.Why + "\""
			}
			fmt.Fprintf(&b, "- Tiebreak: the ranking left %s within %.0f%% of each other; %s chose %s from a description of the turn and the candidates (no content of the work)%s.\n",
				strings.Join(t.Between, ", "), TiebreakMargin*100, t.By, t.Chose, why)
		}
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
			applied = "recorded only: the agent runs with the effort set on its Team page"
		}
		fmt.Fprintf(&b, "- Effort: %s (%s); team size %d.\n", d.Effort, applied, d.TeamSize)
	}
	if f.NextEscalation != "" {
		fmt.Fprintf(&b, "- Next: %s\n", f.NextEscalation)
	}
	if f.Stopped != "" {
		fmt.Fprintf(&b, "- Stopped: %s\n", f.Stopped)
	}
	return b.String()
}

func kfmt(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

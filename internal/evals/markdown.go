package evals

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func usd(v float64) string { return fmt.Sprintf("$%.4f", v) }

// moneyCell prints cost with its sources apart: "reported $a / estimated $b / unknown n call(s)".
func moneyCell(m Money) string {
	var parts []string
	parts = append(parts, "reported "+usd(m.ReportedUSD), "estimated "+usd(m.EstimatedUSD))
	if m.UnknownRows > 0 {
		parts = append(parts, fmt.Sprintf("unknown %d call(s)", m.UnknownRows))
	}
	return strings.Join(parts, " / ")
}

func perCell(m *Money) string {
	if m == nil {
		return "n/a"
	}
	return moneyCell(*m)
}

func ratioCell(r Ratio) string {
	if r.Rate == nil {
		return "n/a (n=0)"
	}
	return fmt.Sprintf("%.1f%% (%d/%d)", *r.Rate*100, r.Num, r.Den)
}

func distCell(d Dist) string {
	if d.N == 0 || d.Median == nil {
		return "n/a (n=0)"
	}
	return fmt.Sprintf("median %.1f, min %d, max %d (n=%d)", *d.Median, *d.Min, *d.Max, d.N)
}

func tok(t Tokens) string {
	return fmt.Sprintf("in %d / out %d / cached %d / cache-write %d", t.Input, t.Output, t.Cached, t.CacheWrite)
}

func fptr(v *float64, format string) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf(format, *v)
}

// Markdown renders the report in the style of docs/evals.md: tables with n beside every figure, reported and estimated
// cost apart, and "n/a" wherever the denominator is zero. The numbers come from the database at GeneratedAt; this text is
// regenerated, never edited by hand.
func (r Report) Markdown() string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w("# Drydock evaluation report")
	w("")
	scope := "all projects"
	if r.Options.ProjectID != "" {
		scope = "project " + r.Options.ProjectID
	}
	w("Generated %s UTC from the local database (%s; window %s to %s). Every figure is a count or sum over rows that exist; a figure with no data says n/a. Cost is shown as reported, estimated and unknown and never merged. This is a measurement of this installation's data, not a benchmark: no comparison, saving or learning is claimed.",
		time.UnixMilli(r.GeneratedAt).UTC().Format("2006-01-02 15:04:05"), scope, ts(r.Options.SinceMS), ts(r.Options.UntilMS))
	w("")

	q := r.Quality
	w("## Tasks")
	w("")
	w("| Started | Accepted (human) | Failed | Cancelled | Still open |")
	w("|---|---|---|---|---|")
	w("| %d | %d | %d | %d | %d |", q.Started, q.Accepted, q.Failed, q.Cancelled, q.Active)
	w("")

	c := r.Cost
	w("## Cost per accepted task")
	w("")
	w("Accepted tasks: **%d**. Figures divide by accepted tasks only when at least one exists.", c.Accepted)
	w("")
	w("| Measure | Total | Per accepted task |")
	w("|---|---|---|")
	w("| All usage in the window (numerator of CPAT, failed and unattributed work included) | %s | %s |", moneyCell(c.AllWindow), perCell(c.PerAcceptedAll))
	w("| Usage of accepted tasks only (CPAT accepted-only) | %s | %s |", moneyCell(c.AcceptedOnly), perCell(c.PerAcceptedOnly))
	w("| Unattributed (no task: app-owned LLM calls) | %s | |", moneyCell(c.Unattributed))
	w("")
	if len(c.Overhead) > 0 {
		w("Overhead excluded from the figures above:")
		w("")
		w("| Purpose | Cost |")
		w("|---|---|")
		for _, k := range sortedKeys(c.Overhead) {
			w("| %s | %s |", k, moneyCell(c.Overhead[k]))
		}
		w("")
	}
	groupTable := func(title string, gs []Group) {
		w("### By %s", title)
		w("")
		if len(gs) == 0 {
			w("No usage rows on accepted tasks.")
			w("")
			return
		}
		w("| %s | Accepted tasks (n) | Total (accepted tasks) | Per accepted task |", strings.Title(title))
		w("|---|---|---|---|")
		for _, g := range gs {
			per := fmt.Sprintf("n < %d: see the per-task rows", r.Options.MinN)
			if g.PerAccepted != nil {
				per = moneyCell(*g.PerAccepted)
			}
			w("| %s | %d | %s | %s |", g.Name, g.Accepted, moneyCell(g.Money), per)
		}
		w("")
	}
	groupTable("role", c.ByRole)
	groupTable("workflow", c.ByWorkflow)
	groupTable("tier", c.ByTier)
	w("%s", c.Note)
	w("")

	p := r.Premium
	w("## Premium usage per accepted task")
	w("")
	w("| Accepted tasks (n) | Premium turns (agent runs) | Per accepted task | Premium LLM calls without an agent run | Quota consumed |")
	w("|---|---|---|---|---|")
	w("| %d | %d | %s | %d | %s |", p.Accepted, p.Turns, fptr(p.TurnsPerAccepted, "%.2f"), p.Calls, p.QuotaDelta)
	w("")
	if len(p.ByProvider) > 0 {
		w("| Premium provider | Tokens as recorded (accepted tasks) | Cost |")
		w("|---|---|---|")
		for _, x := range p.ByProvider {
			w("| %s | %s | %s |", x.Provider, tok(x.Tokens), moneyCell(x.Money))
		}
		w("")
		w("Token counts are kept per provider and not summed: vendors count cached input differently (decision D12). Subscription CLIs have no per-token price; their dollar figure, when present, is the CLI's own estimate.")
		w("")
	}

	ch := r.Cheap
	w("## Cheap-only acceptance")
	w("")
	w("| Variant | Share of accepted tasks |")
	w("|---|---|")
	w("| Cheap-worker (worker ran only on cheap-tier models, no escalation; a premium review does not disqualify) | %s |", ratioCell(ch.CheapWorker))
	w("| Strict cheap-only (no premium-tier usage at all, review included) | %s |", ratioCell(ch.StrictCheapOnly))
	w("")
	w("Accepted tasks that cannot be classified (the worker has no usage rows): %d. By routing stratum:", ch.Unclassified)
	w("")
	w("| Stratum | Accepted tasks | Cheap-worker share |")
	w("|---|---|---|")
	for _, k := range sortedKeys(ch.Strata) {
		w("| %s | %d | %s |", k, ch.Strata[k], ratioCell(ch.CheapWorkerByStratum[k]))
	}
	w("")

	rv := r.Revise
	w("## Premium REVISE rate on cheap-worker tasks")
	w("")
	w("| Cheap-worker tasks | Premium REVISE / premium verdicts | Tasks with a premium REVISE | Escalated |")
	w("|---|---|---|---|")
	w("| %d | %s | %s | %s |", rv.CheapWorkerTasks, ratioCell(rv.PremiumVerdicts), ratioCell(rv.TasksWithRevise), ratioCell(rv.Escalated))
	w("")
	w("Excluded: %d verdict(s) by reviewers with no usage rows (tier unknown), %d human verdict(s). %s", rv.UnknownTierReviews, rv.HumanVerdicts, rv.Note)
	w("")

	w("## Completion, tests and iterations")
	w("")
	w("| Completion (accepted / started) | First test run passed | Last test run passed (accepted tasks) | Iterations to SHIP (accepted, agent reviewer) |")
	w("|---|---|---|---|")
	w("| %s | %s | %s | %s |", ratioCell(q.Completion), ratioCell(q.FirstCheckTestPass), ratioCell(q.FinalTestPass), distCell(q.IterationsToShip))
	w("")

	rs := r.Resume
	w("## Resume reliability")
	w("")
	w("| Recovered runs | Failed | Still running | Cancelled by a human | Reliability (recovered / (recovered + failed)) |")
	w("|---|---|---|---|---|")
	w("| %d | %d | %d | %d | %s |", rs.Recovered, rs.Failed, rs.Pending, rs.Cancelled, ratioCell(rs.Reliability))
	w("")
	w("%s", rs.Note)
	w("")

	wd := r.Watchdog
	w("## Watchdog")
	w("")
	w("| Trips | Stage 1 | Stage 2 | Per accepted task | Soft warnings | False trips | False-trip rate |")
	w("|---|---|---|---|---|---|---|")
	w("| %d | %d | %d | %s | %d | %d | %s |", wd.Trips, wd.Stage1, wd.Stage2, fptr(wd.PerAccepted, "%.2f"), wd.Warnings, wd.FalseTrips, ratioCell(wd.FalseRate))
	w("")
	if len(wd.BySignal) > 0 {
		w("| Signal | Trips |")
		w("|---|---|")
		for _, k := range sortedKeys(wd.BySignal) {
			w("| %s | %d |", k, wd.BySignal[k])
		}
		w("")
	}
	w("%s", wd.Note)
	w("")

	rt := r.Routing
	w("## Routing quality")
	w("")
	w("| Decisions | With an outcome | Pending | Good (accepted, not escalated) | Fallbacks | Escalations |")
	w("|---|---|---|---|---|---|")
	w("| %d | %d | %d | %s | %d | %d |", rt.Decisions, rt.WithOutcome, rt.Pending, ratioCell(rt.Good), rt.Fallbacks, rt.Escalations)
	w("")
	if len(rt.Groups) > 0 {
		w("| Decided by | Tier | Provider | Model | Decisions | Accepted | Escalated | Failed | Mean iterations |")
		w("|---|---|---|---|---|---|---|---|---|")
		for _, g := range rt.Groups {
			w("| %s | %s | %s | %s | %d | %d | %d | %d | %s |", g.DecidedBy, g.Tier, g.Provider, g.Model, g.Decisions, g.Accepted, g.Escalated, g.Failed, fptr(g.MeanIterations, "%.2f"))
		}
		w("")
	}
	w("%s", rt.Note)
	w("")

	w("## Per-task rows")
	w("")
	if len(r.Rows) == 0 {
		w("No tasks in the window.")
	} else {
		w("| Task | Workflow | Status | Cost | Tokens in/out | Premium turns | Cheap worker | Strict cheap | Stratum | Iterations to SHIP | First test | Trips | Escalated |")
		w("|---|---|---|---|---|---|---|---|---|---|---|---|---|")
		for _, t := range r.Rows {
			its := "-"
			if t.ItersToShip > 0 {
				its = fmt.Sprint(t.ItersToShip)
			}
			w("| %s | %s | %s | %s | %d / %d | %d | %s | %s | %s | %s | %s | %d | %v |", t.ID, wfName(t.Workflow), t.Status, moneyCell(t.Money), t.Tokens.Input, t.Tokens.Output,
				t.PremiumTurns, t.CheapWorker, t.StrictCheap, t.Stratum, its, t.FirstTest, t.Trips, t.Escalated)
		}
	}
	w("")
	w("## Not measured here")
	w("")
	for _, n := range r.NotMeasured {
		w("- %s", n)
	}
	return b.String()
}

func ts(ms int64) string {
	if ms <= 0 {
		return "(open)"
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04")
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

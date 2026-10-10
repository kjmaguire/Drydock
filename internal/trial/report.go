package trial

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Report renders run records as Markdown for docs/evals.md: every run as a raw row, then per arm and per task the counts,
// medians and ranges. It never prints a mean or a significance claim (docs/evals.md 1.6), and a figure with no runs behind it
// reads "n/a (n=0)".
func Report(recs []Record) string {
	var b strings.Builder
	if len(recs) == 0 {
		return "No runs.\n"
	}
	trials, commits, clis, models, used, hosts := set(), set(), set(), set(), set(), set()
	for _, r := range recs {
		trials[r.Trial], commits[r.Commit], clis[r.CLI], models[r.Model], hosts[r.Host] = true, true, true, true, true
		for _, m := range r.ModelsUsed {
			used[m] = true
		}
	}
	fmt.Fprintf(&b, "Trial %s; Drydock %s; %s; model asked %s, reported %s; host %s.\n\n", join(trials), join(commits), join(clis), join(models), join(used), join(hosts))

	b.WriteString("| Arm | Runs | Accepted | Cost, all runs | Cost per accepted task | Cost per run, median (range) | Wall time per run, median (range) |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, arm := range arms(recs) {
		rs := filter(recs, func(r Record) bool { return r.Arm == arm && r.Counted() })
		acc := count(rs, func(r Record) bool { return r.Outcome == Accepted })
		total, lower := sumCost(rs)
		per := "n/a (n=0)"
		if acc > 0 {
			per = money(total/float64(acc)) + lower
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %s%s | %s | %s | %s |\n", arm, len(rs), acc, money(total), lower, per,
			spread(rs, func(r Record) float64 { return r.CostUSD }, money), spread(rs, func(r Record) float64 { return float64(r.WallMs) }, dur))
	}
	b.WriteString("\n| Task | Arm | Runs | Accepted | Cost per run, median (range) | Wall time per run, median (range) |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, task := range taskIDs(recs) {
		for _, arm := range arms(recs) {
			rs := filter(recs, func(r Record) bool { return r.TaskID == task && r.Arm == arm && r.Counted() })
			if len(rs) == 0 {
				continue
			}
			acc := count(rs, func(r Record) bool { return r.Outcome == Accepted })
			fmt.Fprintf(&b, "| %s | %s | %d | %d | %s | %s |\n", task, arm, len(rs), acc,
				spread(rs, func(r Record) float64 { return r.CostUSD }, money), spread(rs, func(r Record) float64 { return float64(r.WallMs) }, dur))
		}
	}
	b.WriteString("\nEvery run:\n\n| Task | Arm | # | Outcome | Cost | Wall | Sessions | Tokens in / out / cache read / cache write | Reviews | Why |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range recs {
		out := r.Outcome
		if r.SawHiddenFiles {
			out += " (not counted: read the hidden checks)"
		}
		var verdicts []string
		for _, v := range r.Reviews {
			verdicts = append(verdicts, v.Verdict)
		}
		cost := money(r.CostUSD)
		if r.CallsWithoutCost > 0 {
			cost += fmt.Sprintf(" + %d unknown", r.CallsWithoutCost)
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %s | %s | %s | %d | %d / %d / %d / %d | %s | %s |\n", r.TaskID, r.Arm, r.Repeat, out, cost,
			dur(float64(r.WallMs)), r.Sessions, r.Tokens.Input, r.Tokens.Output, r.Tokens.CacheRead, r.Tokens.CacheWrite,
			strings.Join(verdicts, ", "), cell(why(r)))
	}
	b.WriteString("\nCost is Claude Code's own `total_cost_usd` (its estimate at API prices, not what a subscription costs); \"unknown\" " +
		"counts sessions that reported none, which makes a figure a lower bound. Accepted: the hidden acceptance check passed. " +
		"The numbers are a small sample: medians and ranges, no averages, no claim of a difference beyond these runs.\n")
	return b.String()
}

func why(r Record) string {
	if r.Reason != "" {
		return r.Reason
	}
	if r.EvalAccept != nil && !r.EvalAccept.Passed && len(r.EvalAccept.Commands) > 0 {
		last := r.EvalAccept.Commands[len(r.EvalAccept.Commands)-1]
		return "hidden check failed: " + last.Command
	}
	return ""
}

func cell(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "|", "\\|")
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}

func set() map[string]bool { return map[string]bool{} }

func join(m map[string]bool) string {
	var out []string
	for k := range m {
		if k != "" {
			out = append(out, k)
		}
	}
	if len(out) == 0 {
		return "unknown"
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func arms(recs []Record) []string {
	m := set()
	for _, r := range recs {
		m[r.Arm] = true
	}
	out := sortedKeys(m)
	sort.Slice(out, func(i, j int) bool { return out[i] == ArmDrydock || out[j] != ArmDrydock && out[i] < out[j] }) // D first
	return out
}

func taskIDs(recs []Record) []string {
	m := set()
	for _, r := range recs {
		m[r.TaskID] = true
	}
	return sortedKeys(m)
}

func filter(recs []Record, keep func(Record) bool) []Record {
	var out []Record
	for _, r := range recs {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

func count(recs []Record, f func(Record) bool) int { return len(filter(recs, f)) }

// sumCost adds the runs' cost and says when it is a lower bound.
func sumCost(recs []Record) (float64, string) {
	var total float64
	unknown := 0
	for _, r := range recs {
		total += r.CostUSD
		unknown += r.CallsWithoutCost
	}
	if unknown > 0 {
		return total, fmt.Sprintf(" (at least; %d session(s) reported no cost)", unknown)
	}
	return total, ""
}

// spread is "median (min to max)" of f over recs.
func spread(recs []Record, f func(Record) float64, format func(float64) string) string {
	if len(recs) == 0 {
		return "n/a (n=0)"
	}
	vs := make([]float64, 0, len(recs))
	for _, r := range recs {
		vs = append(vs, f(r))
	}
	sort.Float64s(vs)
	var med float64
	if n := len(vs); n%2 == 1 {
		med = vs[n/2]
	} else {
		med = (vs[n/2-1] + vs[n/2]) / 2
	}
	if len(vs) == 1 {
		return format(med) + " (n=1)"
	}
	return fmt.Sprintf("%s (%s to %s)", format(med), format(vs[0]), format(vs[len(vs)-1]))
}

func money(v float64) string { return fmt.Sprintf("$%.4f", v) }

func dur(ms float64) string {
	return (time.Duration(ms) * time.Millisecond).Round(time.Second).String()
}

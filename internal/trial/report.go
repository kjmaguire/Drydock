package trial

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Report renders run records as Markdown for docs/evals.md: per arm and per task the counts, medians and ranges, the arms
// compared task by task, then every run as a raw row. It never prints a mean (docs/evals.md 1.6), and a figure with no runs
// behind it reads "n/a (n=0)". A statistical test is printed only for a trial of the size decision P34 asks for before any
// claim (ClaimTasks tasks with ClaimRepeats counted runs of each arm); a smaller trial is labelled a pilot.
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
	writePaired(&b, recs)
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
	writeB0(&b, recs)
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

// The size of trial decision P34 asks for before a claim that one arm beats or matches the other.
const (
	ClaimTasks   = 20
	ClaimRepeats = 3
)

// Paired is the arms compared task by task, over the tasks both D and B1 ran (counted runs only).
type Paired struct {
	Rows []PairRow
	// DMore, B1More and Same count the tasks where D's acceptance rate was higher, lower or equal.
	DMore, B1More, Same int
	// MinRepeats is the fewest counted runs either arm had on one task.
	MinRepeats int
	// Claim is set when the trial has the size P34 asks for; only then is P computed.
	Claim bool
	// P is the exact two-sided sign test over the tasks whose acceptance rates differ (-1 when not computed).
	P float64
}

// PairRow is one task of a Paired comparison.
type PairRow struct {
	Task, Class           string
	DRuns, DAccepted      int
	B1Runs, B1Accepted    int
	DMedianCost, B1Cost   float64
	DMedianMs, B1MedianMs float64
}

// Pair compares D with B1 task by task.
func Pair(recs []Record) Paired {
	p := Paired{P: -1}
	for _, task := range taskIDs(recs) {
		d := filter(recs, func(r Record) bool { return r.TaskID == task && r.Arm == ArmDrydock && r.Counted() })
		b := filter(recs, func(r Record) bool { return r.TaskID == task && r.Arm == ArmB1 && r.Counted() })
		if len(d) == 0 || len(b) == 0 {
			continue
		}
		row := PairRow{Task: task, DRuns: len(d), B1Runs: len(b),
			DAccepted: count(d, func(r Record) bool { return r.Outcome == Accepted }), B1Accepted: count(b, func(r Record) bool { return r.Outcome == Accepted }),
			DMedianCost: median(d, func(r Record) float64 { return r.CostUSD }), B1Cost: median(b, func(r Record) float64 { return r.CostUSD }),
			DMedianMs: median(d, func(r Record) float64 { return float64(r.WallMs) }), B1MedianMs: median(b, func(r Record) float64 { return float64(r.WallMs) })}
		for _, r := range append(d, b...) {
			if r.TaskClass != "" {
				row.Class = r.TaskClass
			}
		}
		// compare rates without division: DAccepted/DRuns against B1Accepted/B1Runs
		switch l, r := row.DAccepted*row.B1Runs, row.B1Accepted*row.DRuns; {
		case l > r:
			p.DMore++
		case l < r:
			p.B1More++
		default:
			p.Same++
		}
		for _, n := range []int{row.DRuns, row.B1Runs} {
			if p.MinRepeats == 0 || n < p.MinRepeats {
				p.MinRepeats = n
			}
		}
		p.Rows = append(p.Rows, row)
	}
	p.Claim = len(p.Rows) >= ClaimTasks && p.MinRepeats >= ClaimRepeats
	if p.Claim {
		p.P = signTest(p.DMore, p.B1More)
	}
	return p
}

// signTest is the exact two-sided sign test: the chance, if neither arm were better, of a split at least as uneven as a
// against b.
func signTest(a, b int) float64 {
	n := a + b
	if n == 0 {
		return 1
	}
	k := min(a, b)
	tail := 0.0
	c := 1.0 // C(n, 0)
	for i := 0; i <= k; i++ {
		if i > 0 {
			c = c * float64(n-i+1) / float64(i)
		}
		tail += c
	}
	return min(1, 2*tail/math.Pow(2, float64(n)))
}

func median(recs []Record, f func(Record) float64) float64 {
	vs := make([]float64, 0, len(recs))
	for _, r := range recs {
		vs = append(vs, f(r))
	}
	sort.Float64s(vs)
	if len(vs) == 0 {
		return 0
	}
	if n := len(vs); n%2 == 1 {
		return vs[n/2]
	}
	return (vs[len(vs)/2-1] + vs[len(vs)/2]) / 2
}

func writePaired(b *strings.Builder, recs []Record) {
	p := Pair(recs)
	if len(p.Rows) == 0 {
		return
	}
	b.WriteString("\nD against B1, task by task (counted runs):\n\n| Task | Class | D accepted | B1 accepted | D cost per run, median | B1 cost per run, median | D wall, median | B1 wall, median |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, r := range p.Rows {
		class := r.Class
		if class == "" {
			class = "unknown"
		}
		fmt.Fprintf(b, "| %s | %s | %d of %d | %d of %d | %s | %s | %s | %s |\n", r.Task, class, r.DAccepted, r.DRuns, r.B1Accepted, r.B1Runs,
			money(r.DMedianCost), money(r.B1Cost), dur(r.DMedianMs), dur(r.B1MedianMs))
	}
	classes := map[string][4]int{}
	for _, r := range p.Rows {
		c := r.Class
		if c == "" {
			c = "unknown"
		}
		v := classes[c]
		v[0], v[1], v[2], v[3] = v[0]+r.DAccepted, v[1]+r.DRuns, v[2]+r.B1Accepted, v[3]+r.B1Runs
		classes[c] = v
	}
	if len(classes) > 1 {
		b.WriteString("\n| Class | D accepted | B1 accepted |\n|---|---|---|\n")
		names := make([]string, 0, len(classes))
		for c := range classes {
			names = append(names, c)
		}
		sort.Strings(names)
		for _, c := range names {
			v := classes[c]
			fmt.Fprintf(b, "| %s | %d of %d | %d of %d |\n", c, v[0], v[1], v[2], v[3])
		}
	}
	fmt.Fprintf(b, "\nD was accepted more often than B1 on %d task(s), less often on %d and as often on %d (%d tasks run by both, at least %d counted run(s) per arm and task).\n",
		p.DMore, p.B1More, p.Same, len(p.Rows), p.MinRepeats)
	if p.Claim {
		fmt.Fprintf(b, "Exact two-sided sign test over the %d tasks where the arms differ: p = %.3f (the chance of a split at least this uneven if neither arm were better; it says nothing about how large a difference is).\n",
			p.DMore+p.B1More, p.P)
	} else {
		fmt.Fprintf(b, "This is a pilot: decision P34 asks for at least %d tasks with %d counted runs of each arm before any claim, so no test is computed.\n", ClaimTasks, ClaimRepeats)
	}
}

// writeB0 adds what only the broadcast team has: how long it talked, why it stopped, and how much it broadcast.
func writeB0(b *strings.Builder, recs []Record) {
	rs := filter(recs, func(r Record) bool { return r.Arm == ArmB0 })
	if len(rs) == 0 {
		return
	}
	b.WriteString("\nThe broadcast team (B0), every run:\n\n| Task | # | Agents | Rounds | Stopped by | Sessions | Broadcast, all turns | Largest prompt | CLI, provider |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range rs {
		fmt.Fprintf(b, "| %s | %d | %d | %d | %s | %d | %s | %s | %s |\n", r.TaskID, r.Repeat, r.Agents, r.Rounds, r.Stop, r.Sessions,
			kib(r.BroadcastBytes), kib(r.MaxPromptBytes), b0Where(r))
	}
}

// b0Where is the CLI a B0 run's team ran and the provider it ran on (decision P121); a record from before names neither.
func b0Where(r Record) string {
	cli := r.AgentCLI
	if cli == "" {
		cli = CLIClaude
	}
	if r.Provider == "" {
		return cli + ", its own sign-in"
	}
	if r.CostSource == "estimated" {
		return cli + ", " + r.Provider + " (cost estimated at its prices)"
	}
	return cli + ", " + r.Provider
}

func kib(n int64) string { return fmt.Sprintf("%.1f KiB", float64(n)/1024) }

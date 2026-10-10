package trial

import (
	"fmt"
	"sort"
	"strings"
)

// Group is one variant of a comparison: the counted D runs of one trial (for example the same suite with skills delivered
// in different ways).
type Group struct {
	Name string
	Recs []Record
}

// Compare reports variants of arm D side by side: each group's acceptance, cost, time and tokens, then task by task, and an
// exact sign test of each group against the first one over the tasks where their acceptance rates differ.
func Compare(groups []Group) string {
	var b strings.Builder
	for i := range groups {
		groups[i].Recs = filter(groups[i].Recs, func(r Record) bool { return r.Arm == ArmDrydock && r.Counted() })
	}
	b.WriteString("| Variant | Runs | Accepted | Cost, all runs | Cost per accepted task | Cost per run, median (range) | Prompt tokens per run, median (range) | Wall time per run, median (range) | Skill files read per run, median (range) |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	for _, g := range groups {
		acc := count(g.Recs, func(r Record) bool { return r.Outcome == Accepted })
		total, note := sumCost(g.Recs)
		per := "n/a"
		if acc > 0 {
			per = money(total / float64(acc))
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %s%s | %s | %s | %s | %s | %s |\n", g.Name, len(g.Recs), acc, money(total), note, per,
			spread(g.Recs, func(r Record) float64 { return r.CostUSD }, money),
			spread(g.Recs, promptTokens, func(v float64) string { return fmt.Sprintf("%.0f", v) }),
			spread(g.Recs, func(r Record) float64 { return float64(r.WallMs) }, dur),
			spread(g.Recs, func(r Record) float64 { return float64(r.SkillReads) }, func(v float64) string { return fmt.Sprintf("%.1f", v) }))
	}
	byTask := map[string]map[string][2]int{} // task -> group -> {accepted, runs}
	for _, g := range groups {
		for _, r := range g.Recs {
			if byTask[r.TaskID] == nil {
				byTask[r.TaskID] = map[string][2]int{}
			}
			v := byTask[r.TaskID][g.Name]
			v[1]++
			if r.Outcome == Accepted {
				v[0]++
			}
			byTask[r.TaskID][g.Name] = v
		}
	}
	tasks := make([]string, 0, len(byTask))
	for t := range byTask {
		tasks = append(tasks, t)
	}
	sort.Strings(tasks)
	b.WriteString("\nTask by task (accepted of counted runs):\n\n| Task |")
	for _, g := range groups {
		b.WriteString(" " + g.Name + " |")
	}
	b.WriteString("\n|---|" + strings.Repeat("---|", len(groups)) + "\n")
	for _, t := range tasks {
		b.WriteString("| " + t + " |")
		for _, g := range groups {
			v, ok := byTask[t][g.Name]
			if !ok {
				b.WriteString(" - |")
				continue
			}
			fmt.Fprintf(&b, " %d of %d |", v[0], v[1])
		}
		b.WriteString("\n")
	}
	if len(groups) > 1 {
		b.WriteString("\n")
		base := groups[0].Name
		for _, g := range groups[1:] {
			more, less, same := 0, 0, 0
			for _, t := range tasks {
				a, okA := byTask[t][base]
				o, okO := byTask[t][g.Name]
				if !okA || !okO || a[1] == 0 || o[1] == 0 {
					continue
				}
				ra, ro := float64(a[0])/float64(a[1]), float64(o[0])/float64(o[1])
				switch {
				case ro > ra:
					more++
				case ro < ra:
					less++
				default:
					same++
				}
			}
			fmt.Fprintf(&b, "%s against %s: accepted more often on %d task(s), less often on %d, as often on %d; exact two-sided sign test p = %.3f.\n",
				g.Name, base, more, less, same, signTest(more, less))
		}
	}
	return b.String()
}

// promptTokens is what the model read over the run: fresh input plus the cache reads and writes.
func promptTokens(r Record) float64 {
	return float64(r.Tokens.Input + r.Tokens.CacheRead + r.Tokens.CacheWrite)
}

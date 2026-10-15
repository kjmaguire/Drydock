package repomap

import (
	"sort"
	"strings"
)

// The rendering is Aider's (to_tree and get_ranked_tags_map_uncached): files with the lines of their most important
// definitions, each with the lines of the definitions that enclose it (a method's class), "│" before a line and "⋮" where
// lines are left out; then the files with nothing ranked as bare paths. A binary search finds the most definitions (taken
// in the order of their rank) whose rendering fits the budget. Unlike Aider, the files are printed in the order of their
// PageRank (Aider sorts them by name), so the first file is the most important one, and the budget is a hard limit (Aider
// accepts 15% over).

type entry struct {
	file string
	tag  *Tag // nil: the file as a bare path
}

func entries(g *graph) []entry {
	var out []entry
	seen := map[string]bool{}
	for _, d := range g.defs {
		tags := g.defTags[d.defKey]
		for i := range tags {
			out = append(out, entry{d.file, &tags[i]})
		}
		seen[d.file] = true
	}
	for _, f := range g.order {
		if !seen[f] {
			out = append(out, entry{f, nil})
		}
	}
	return out
}

type renderer struct {
	g      *graph
	es     []entry
	pos    map[string]int // a file's place in the ranking
	scopes map[*Tag][]Tag // enclosing definitions of a definition tag
}

func (r *renderer) enclosing(f string, t *Tag) []Tag {
	if s, ok := r.scopes[t]; ok {
		return s
	}
	var s []Tag
	for _, d := range r.g.fileDefs[f] {
		if d.Line < t.Line && d.End >= t.Line {
			s = append(s, d)
		}
	}
	r.scopes[t] = s
	return s
}

// text renders the first n entries.
func (r *renderer) text(n int) (string, map[string]bool) {
	var order []string
	lines := map[string]map[int]string{}
	for _, e := range r.es[:n] {
		if _, ok := lines[e.file]; !ok {
			order = append(order, e.file)
			lines[e.file] = map[int]string{}
		}
		if e.tag == nil {
			continue
		}
		lines[e.file][e.tag.Line] = e.tag.Text
		for _, s := range r.enclosing(e.file, e.tag) {
			lines[e.file][s.Line] = s.Text
		}
	}
	sort.Slice(order, func(i, j int) bool { return r.pos[order[i]] < r.pos[order[j]] })
	var b strings.Builder
	shown := map[string]bool{}
	for _, f := range order {
		shown[f] = true
		ls := lines[f]
		if len(ls) == 0 {
			b.WriteString(f + "\n")
			continue
		}
		b.WriteString(f + ":\n")
		nums := make([]int, 0, len(ls))
		for l := range ls {
			nums = append(nums, l)
		}
		sort.Ints(nums)
		prev := -1
		for _, l := range nums {
			if l != prev+1 {
				b.WriteString("⋮\n")
			}
			b.WriteString("│" + cut(ls[l]) + "\n")
			prev = l
		}
		b.WriteString("⋮\n")
	}
	return b.String(), shown
}

func cut(s string) string {
	s = strings.TrimRight(s, " \t\r")
	if r := []rune(s); len(r) > maxLineChars {
		return string(r[:maxLineChars])
	}
	return s
}

// render returns the largest rendering within budget tokens and the files it shows.
func render(g *graph, budget int) (string, map[string]bool) {
	r := &renderer{g: g, es: entries(g), pos: map[string]int{}, scopes: map[*Tag][]Tag{}}
	for i, f := range g.order {
		r.pos[f] = i
	}
	best, bestShown := "", map[string]bool{}
	lo, hi := 1, len(r.es)
	for lo <= hi {
		mid := (lo + hi) / 2
		t, shown := r.text(mid)
		if EstimateTokens(t) <= budget {
			best, bestShown = t, shown
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return best, bestShown
}

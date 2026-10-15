package repomap

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// The ranking follows Aider's repomap.py (get_ranked_tags; docs/phase0/go-platform.md section 13): a graph of files where an
// edge goes from a file that references an identifier to each file that defines it, weighted by how distinctive the
// identifier is and how often it is referenced; personalized PageRank over it; then each file's rank is shared out over
// its outgoing edges to the (file, identifier) definitions they point at.

type fileTags struct {
	Path string
	Lang string
	Tags []Tag
}

type defKey struct{ file, ident string }

type rankedDef struct {
	defKey
	rank float64
}

// graph is the outcome of a ranking.
type graph struct {
	files    map[string]fileTags
	defs     []rankedDef        // most important first
	order    []string           // files, most important first
	fileRank map[string]float64 // PageRank of each file
	defCount map[string]int     // definitions per file
	refCount map[string]int     // references per file
	defTags  map[defKey][]Tag   // the definition tags of an identifier in a file
	fileDefs map[string][]Tag   // every definition of a file (for the enclosing lines)
	focus    map[string]bool    // files the map is personalised toward
}

type edge struct {
	src, dst int
	ident    string
	w        float64
}

// identWeight is Aider's multiplier: long snake_case, kebab-case or camelCase names are distinctive (x10), private names
// (leading underscore) and names defined in many files are not (x0.1 each).
func identWeight(ident string, definers int) float64 {
	mul := 1.0
	hasUpper, hasLower, hasLetter := false, false, false
	for _, r := range ident {
		hasUpper = hasUpper || unicode.IsUpper(r)
		hasLower = hasLower || unicode.IsLower(r)
		hasLetter = hasLetter || unicode.IsLetter(r)
	}
	// as Aider: snake and kebab need a letter of either case, so MAX_RETRY_ATTEMPTS counts as snake_case
	snake := strings.Contains(ident, "_") && hasLetter
	kebab := strings.Contains(ident, "-") && hasLetter
	camel := hasUpper && hasLower
	if (snake || kebab || camel) && len(ident) >= 8 {
		mul *= 10
	}
	if strings.HasPrefix(ident, "_") {
		mul *= 0.1
	}
	if definers > 5 {
		mul *= 0.1
	}
	return mul
}

// rank builds the graph of files and ranks files and definitions. focus holds the files to personalise toward.
func rank(files []fileTags, focus map[string]bool) *graph {
	g := &graph{files: map[string]fileTags{}, fileRank: map[string]float64{}, defCount: map[string]int{}, refCount: map[string]int{},
		defTags: map[defKey][]Tag{}, fileDefs: map[string][]Tag{}, focus: focus}
	idx := map[string]int{}
	names := make([]string, 0, len(files))
	defines := map[string]map[string]bool{}   // ident -> files defining it
	references := map[string]map[string]int{} // ident -> file -> count
	for _, f := range files {
		g.files[f.Path] = f
		idx[f.Path] = len(names)
		names = append(names, f.Path)
		for _, t := range f.Tags {
			if t.Def {
				if defines[t.Name] == nil {
					defines[t.Name] = map[string]bool{}
				}
				defines[t.Name][f.Path] = true
				k := defKey{f.Path, t.Name}
				g.defTags[k] = append(g.defTags[k], t)
				g.fileDefs[f.Path] = append(g.fileDefs[f.Path], t)
				g.defCount[f.Path]++
			} else {
				if references[t.Name] == nil {
					references[t.Name] = map[string]int{}
				}
				references[t.Name][f.Path]++
				g.refCount[f.Path]++
			}
		}
	}
	if len(references) == 0 { // no reference query matched anywhere: count each definition as referenced once
		for ident, fs := range defines {
			references[ident] = map[string]int{}
			for f := range fs {
				references[ident][f] = 1
			}
		}
	}
	idents := make([]string, 0, len(defines))
	for ident := range defines {
		idents = append(idents, ident)
	}
	sort.Strings(idents)
	var edges []edge
	for _, ident := range idents {
		definers := sortedKeys(defines[ident])
		refs, ok := references[ident]
		if !ok {
			for _, d := range definers { // defined, never referenced: a weak self-edge keeps the definition rankable
				edges = append(edges, edge{idx[d], idx[d], ident, 0.1})
			}
			continue
		}
		mul := identWeight(ident, len(definers))
		for _, referencer := range sortedKeys(refs) {
			w := mul * math.Sqrt(float64(refs[referencer]))
			if focus[referencer] {
				w *= 50
			}
			for _, d := range definers {
				edges = append(edges, edge{idx[referencer], idx[d], ident, w})
			}
		}
	}

	n := len(names)
	pers := make([]float64, n)
	if len(focus) > 0 {
		for i, f := range names {
			if focus[f] {
				pers[i] = 1
			}
		}
	}
	pr := pagerank(n, edges, pers)
	for i, f := range names {
		g.fileRank[f] = pr[i]
	}

	// share each file's rank over its out-edges, by weight, onto the definitions they point at
	out := make([]float64, n)
	for _, e := range edges {
		out[e.src] += e.w
	}
	acc := map[defKey]float64{}
	for _, e := range edges {
		if out[e.src] > 0 {
			acc[defKey{names[e.dst], e.ident}] += pr[e.src] * e.w / out[e.src]
		}
	}
	for k, r := range acc {
		g.defs = append(g.defs, rankedDef{k, r})
	}
	sort.Slice(g.defs, func(i, j int) bool {
		a, b := g.defs[i], g.defs[j]
		if a.rank != b.rank {
			return a.rank > b.rank
		}
		if a.file != b.file {
			return a.file < b.file
		}
		return a.ident < b.ident
	})
	g.order = append([]string(nil), names...)
	sort.SliceStable(g.order, func(i, j int) bool {
		a, b := g.order[i], g.order[j]
		if g.fileRank[a] != g.fileRank[b] {
			return g.fileRank[a] > g.fileRank[b]
		}
		return a < b
	})
	return g
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// pagerank is networkx's pagerank (alpha 0.85, power iteration, at most 100 iterations, tolerance n*1e-6) over a weighted
// multigraph: parallel edges add up. pers is the personalization vector (all zero: uniform); it is also where the rank of a
// file without out-edges goes, as networkx does when dangling is not given.
func pagerank(n int, edges []edge, pers []float64) []float64 {
	if n == 0 {
		return nil
	}
	const alpha, maxIter = 0.85, 100
	tol := 1e-6
	p := make([]float64, n)
	sum := 0.0
	for _, v := range pers {
		sum += v
	}
	for i := range p {
		if sum > 0 {
			p[i] = pers[i] / sum
		} else {
			p[i] = 1 / float64(n)
		}
	}
	type out struct {
		dst int
		w   float64
	}
	adj := make([]map[int]float64, n)
	wsum := make([]float64, n)
	for _, e := range edges {
		if e.w <= 0 {
			continue
		}
		if adj[e.src] == nil {
			adj[e.src] = map[int]float64{}
		}
		adj[e.src][e.dst] += e.w
		wsum[e.src] += e.w
	}
	lists := make([][]out, n)
	for i, m := range adj {
		for d, w := range m {
			lists[i] = append(lists[i], out{d, w / wsum[i]})
		}
		sort.Slice(lists[i], func(a, b int) bool { return lists[i][a].dst < lists[i][b].dst })
	}
	x := make([]float64, n)
	for i := range x {
		x[i] = 1 / float64(n)
	}
	next := make([]float64, n)
	for iter := 0; iter < maxIter; iter++ {
		dangle := 0.0
		for i := range next {
			next[i] = 0
			if wsum[i] == 0 {
				dangle += x[i]
			}
		}
		for i, l := range lists {
			for _, o := range l {
				next[o.dst] += alpha * x[i] * o.w
			}
		}
		diff := 0.0
		for i := range next {
			next[i] += alpha*dangle*p[i] + (1-alpha)*p[i]
			diff += math.Abs(next[i] - x[i])
		}
		x, next = next, x
		if diff < float64(n)*tol {
			break
		}
	}
	return x
}

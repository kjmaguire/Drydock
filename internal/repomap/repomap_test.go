package repomap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

// These tests cover the pure-Go half (listing filters, cache, ranking, rendering) in every build, with or without CGO. The
// tags come from tagsFromComments, a TEST DOUBLE for the tree-sitter parser: it reads "//def Name" and "//ref Name" lines,
// so a test states exactly which definitions and references a file has. parser_cgo_test.go runs the real parser.

// tagsFromComments is a TEST DOUBLE of extractTags: "//def Name" or "//def Name end=7" defines Name on that line (its
// definition ending at line 7), "//ref Name" references it.
func tagsFromComments(_ context.Context, _ string, src []byte) ([]Tag, error) {
	var out []Tag
	for i, line := range strings.Split(string(src), "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "//def":
			t := Tag{Name: f[1], Def: true, Line: i, End: i, Text: line}
			if len(f) > 2 {
				_, _ = fmt.Sscanf(f[2], "end=%d", &t.End)
			}
			out = append(out, t)
		case "//ref":
			out = append(out, Tag{Name: f[1], Line: i})
		}
	}
	return out, nil
}

func defs(names ...string) []Tag {
	var out []Tag
	for i, n := range names {
		out = append(out, Tag{Name: n, Def: true, Line: i, End: i, Text: "func " + n + "()"})
	}
	return out
}

func refs(names ...string) []Tag {
	var out []Tag
	for i, n := range names {
		out = append(out, Tag{Name: n, Line: 100 + i})
	}
	return out
}

func TestTheHeavilyReferencedFileRanksFirst(t *testing.T) {
	files := []fileTags{
		{Path: "a.go", Tags: append(defs("HandleRequest"), refs("LookupValue", "SaveValue", "RenderLeaf")...)},
		{Path: "b.go", Tags: refs("LookupValue", "SaveValue")},
		{Path: "c.go", Tags: refs("LookupValue")},
		{Path: "hub.go", Tags: defs("LookupValue", "SaveValue")},
		{Path: "leaf.go", Tags: defs("RenderLeaf")},
	}
	g := rank(files, nil)
	if g.order[0] != "hub.go" {
		t.Fatalf("hub.go is referenced by three files and must rank first: %v %v", g.order, g.fileRank)
	}
	if g.fileRank["hub.go"] <= g.fileRank["leaf.go"] {
		t.Fatalf("hub %.4f must outrank leaf %.4f", g.fileRank["hub.go"], g.fileRank["leaf.go"])
	}
	if d := g.defs[0]; d.file != "hub.go" || d.ident != "LookupValue" {
		t.Fatalf("the most referenced definition comes first: %+v", g.defs[:3])
	}
	sum := 0.0
	for _, r := range g.fileRank {
		sum += r
	}
	if sum < 0.999 || sum > 1.001 {
		t.Fatalf("PageRank sums to 1, got %f", sum)
	}
}

func TestFocusPersonalisesTheRanking(t *testing.T) {
	files := []fileTags{
		{Path: "web/page.go", Tags: refs("RenderAlpha")},
		{Path: "jobs/run.go", Tags: refs("RenderBeta")},
		{Path: "alpha.go", Tags: defs("RenderAlpha")},
		{Path: "beta.go", Tags: defs("RenderBeta")},
	}
	plain := rank(files, nil)
	if plain.fileRank["alpha.go"] != plain.fileRank["beta.go"] {
		t.Fatalf("symmetric files rank the same without focus: %v", plain.fileRank)
	}
	focused := rank(files, map[string]bool{"jobs/run.go": true})
	if focused.fileRank["beta.go"] <= focused.fileRank["alpha.go"] {
		t.Fatalf("what the focus file uses must rank higher: %v", focused.fileRank)
	}
	if focused.order[0] != "beta.go" && focused.order[0] != "jobs/run.go" {
		t.Fatalf("the focus and what it uses lead: %v", focused.order)
	}
	if !isFocused("jobs/run.go", []string{"jobs"}) || isFocused("jobsx/run.go", []string{"jobs"}) || !isFocused("a.go", []string{"a.go"}) {
		t.Fatal("a focus folder covers its files and nothing else")
	}
}

func TestIdentWeightFollowsAider(t *testing.T) {
	cases := []struct {
		ident    string
		definers int
		want     float64
	}{
		{"LookupValue", 1, 10}, {"open_account", 1, 10}, {"kebab-case-name", 1, 10}, {"run", 1, 1}, {"Short", 1, 1},
		{"_private_helper", 1, 1}, {"_x", 1, 0.1}, {"LookupValue", 6, 1},
	}
	for _, c := range cases {
		if got := identWeight(c.ident, c.definers); got < c.want*0.999 || got > c.want*1.001 {
			t.Errorf("identWeight(%q, %d) = %v, want %v", c.ident, c.definers, got, c.want)
		}
	}
}

func TestRenderRespectsTheBudgetAndShowsEnclosingDefinitions(t *testing.T) {
	var files []fileTags
	for i := 0; i < 60; i++ {
		var tags []Tag
		tags = append(tags, Tag{Name: fmt.Sprintf("Service%02d", i), Def: true, Line: 2, End: 40, Text: fmt.Sprintf("type Service%02d struct {", i)})
		for j := 0; j < 6; j++ {
			tags = append(tags, Tag{Name: fmt.Sprintf("Method%02d_%d", i, j), Def: true, Line: 10 + j*4, End: 12 + j*4, Text: fmt.Sprintf("	func (s *Service%02d) Method%02d_%d(ctx context.Context) error {", i, i, j)})
		}
		for j := 0; j < 6; j++ {
			tags = append(tags, Tag{Name: fmt.Sprintf("Method%02d_%d", (i+1)%60, j), Line: 50 + j})
		}
		files = append(files, fileTags{Path: fmt.Sprintf("svc/service_%02d.go", i), Tags: tags})
	}
	g := rank(files, nil)
	for _, budget := range []int{MinBudget, 512, 1024, DefaultBudget} {
		text, shown := render(g, budget)
		if n := EstimateTokens(text); n > budget || n < budget*7/10 {
			t.Errorf("budget %d: the map has %d tokens: it must fit and use most of it", budget, n)
		}
		if len(shown) == 0 {
			t.Errorf("budget %d: nothing shown", budget)
		}
	}
	text, _ := render(g, MaxBudget)
	// a method line comes with the line of the type that encloses it, and lines are cut to 100 characters
	block := text[strings.Index(text, "svc/service_"):]
	block = block[:strings.Index(block[1:], "svc/service_")+1]
	if !strings.Contains(block, ":\n⋮\n│type Service") || !strings.Contains(block, "│\tfunc (s *Service") {
		t.Fatalf("a file block shows the enclosing type before its methods:\n%s", block)
	}
	for _, l := range strings.Split(text, "\n") {
		if len([]rune(l)) > maxLineChars+1 {
			t.Fatalf("line longer than %d characters: %q", maxLineChars, l)
		}
	}
}

// writeFiles makes files under root and returns their relative paths.
func writeFiles(t *testing.T, root string, files map[string]string) []string {
	t.Helper()
	var rels []string
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	return rels
}

// countingMapper is a Mapper whose parser is the TEST DOUBLE tagsFromComments, counting the files it parses.
func countingMapper(list Lister, cacheDir string, calls *atomic.Int64) *Mapper {
	m := New(Options{List: list, CacheDir: cacheDir, MaxFileBytes: 2048})
	m.extract = func(ctx context.Context, lang string, src []byte) ([]Tag, error) {
		calls.Add(1)
		return tagsFromComments(ctx, lang, src)
	}
	m.version = "test-double"
	return m
}

func TestMapSkipsVendoredGeneratedBinaryHugeAndUnsupportedFiles(t *testing.T) {
	root := t.TempDir()
	rels := writeFiles(t, root, map[string]string{
		"core/store.go":              "package core\n//def LookupValue\n",
		"app/main.go":                "package main\n//ref LookupValue\n",
		"vendor/lib/lib.go":          "//def LookupValue\n",
		"node_modules/x/index.js":    "//def LookupValue\n",
		"web/app.min.js":             "//def LookupValue\n",
		"api/api.pb.go":              "//def LookupValue\n",
		"gen/models.go":              "// Code generated by sqlc. DO NOT EDIT.\n//def LookupValue\n",
		"blob.go":                    "package blob\x00\x01//def LookupValue\n",
		"big.go":                     strings.Repeat("// padding\n", 400) + "//def LookupValue\n",
		"README.md":                  "# readme\n",
		"assets/logo.png":            "png",
		"web/src/component.test.tsx": "//ref LookupValue\n",
	})
	var calls atomic.Int64
	m := countingMapper(func(context.Context, string) ([]string, error) { return append(rels, "deleted/gone.go"), nil }, "", &calls)
	res, err := m.Map(context.Background(), Request{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	want := Skipped{Unsupported: 2, Vendored: 2, Generated: 3, TooLarge: 1, Binary: 1, Unreadable: 1}
	if res.Skipped != want {
		t.Fatalf("skipped %+v, want %+v", res.Skipped, want)
	}
	if res.Files != 3 || calls.Load() != 3 || res.Listed != 13 {
		t.Fatalf("parsed %d files with %d parser calls of %d listed, want 3, 3, 13", res.Files, calls.Load(), res.Listed)
	}
	if !strings.HasPrefix(res.Map, "core/store.go:\n") || strings.Contains(res.Map, "vendor/") || strings.Contains(res.Map, "gen/") {
		t.Fatalf("map:\n%s", res.Map)
	}
	if res.Ranked[0].Path != "core/store.go" || !res.Ranked[0].Shown || res.Ranked[0].Defs != 1 {
		t.Fatalf("ranked: %+v", res.Ranked)
	}
	if res.Languages["go"] != 2 || res.Languages["tsx"] != 1 {
		t.Fatalf("languages: %v", res.Languages)
	}

	// a bound on the number of files
	m2 := countingMapper(func(context.Context, string) ([]string, error) { return rels, nil }, "", &calls)
	m2.opts.MaxFiles = 1
	res, err = m2.Map(context.Background(), Request{Root: root})
	if err != nil || res.Files != 1 || res.Skipped.OverLimit != 5 { // 6 source files pass the name filters; 1 is parsed
		t.Fatalf("over the file limit: %+v %v", res.Skipped, err)
	}
}

func TestTagsAreCachedByContentInMemoryAndOnDisk(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	rels := writeFiles(t, root, map[string]string{
		"a.go": "//def LookupValue\n",
		"b.go": "//ref LookupValue\n",
		"c.go": "package c\n//ref LookupValue\n",
	})
	list := func(context.Context, string) ([]string, error) { return rels, nil }
	var calls atomic.Int64
	m := countingMapper(list, cache, &calls)
	ctx := context.Background()
	first, err := m.Map(ctx, Request{Root: root})
	if err != nil || calls.Load() != 3 || first.Cached != 0 {
		t.Fatalf("first map: %d calls, %d cached, %v", calls.Load(), first.Cached, err)
	}
	second, _ := m.Map(ctx, Request{Root: root, TokenBudget: 4000})
	if calls.Load() != 3 || second.Cached != 3 || second.Map != first.Map {
		t.Fatalf("an unchanged repository is not parsed again: %d calls, %d cached", calls.Load(), second.Cached)
	}
	// a changed file is parsed again; the others are not
	if err := os.WriteFile(filepath.Join(root, "c.go"), []byte("package c\n//ref LookupValue\n//def Other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Map(ctx, Request{Root: root}); err != nil || calls.Load() != 4 {
		t.Fatalf("one changed file, one parse: %d calls", calls.Load())
	}
	// a new Mapper (the app restarted) reads the disk cache
	var calls2 atomic.Int64
	m2 := countingMapper(list, cache, &calls2)
	res, err := m2.Map(ctx, Request{Root: root})
	if err != nil || calls2.Load() != 0 || res.Cached != 3 {
		t.Fatalf("after a restart the disk cache answers: %d calls, %d cached, %v", calls2.Load(), res.Cached, err)
	}
	// a cache of another parser version is ignored
	var calls3 atomic.Int64
	m3 := countingMapper(list, cache, &calls3)
	m3.version = "another"
	if _, err := m3.Map(ctx, Request{Root: root}); err != nil || calls3.Load() != 3 {
		t.Fatalf("another version parses again: %d calls", calls3.Load())
	}
	entries, _ := os.ReadDir(cache)
	if len(entries) != 1 {
		t.Fatalf("one cache file per mapped folder: %v", entries)
	}
}

func TestRequestsAreCheckedAndBudgetsClamped(t *testing.T) {
	for in, want := range map[int]int{0: DefaultBudget, 10: MinBudget, 100000: MaxBudget, 1000: 1000} {
		if got := ClampBudget(in); got != want {
			t.Errorf("ClampBudget(%d) = %d, want %d", in, got, want)
		}
	}
	got, err := CleanFocus([]string{" internal\\core ", "./ui/app/", "", "."})
	if err != nil || strings.Join(got, ",") != "internal/core,ui/app" {
		t.Fatalf("CleanFocus = %v, %v", got, err)
	}
	for _, bad := range []string{"../etc", "/etc/passwd", "C:\\Windows", "a/../../b"} {
		if _, err := CleanFocus([]string{bad}); err == nil {
			t.Errorf("CleanFocus(%q) must refuse", bad)
		}
	}
	var calls atomic.Int64
	m := countingMapper(func(context.Context, string) ([]string, error) { return nil, errors.New("not a git repository") }, "", &calls)
	if _, err := m.Map(context.Background(), Request{Root: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("a listing error is reported: %v", err)
	}
	if _, err := m.Map(context.Background(), Request{Root: t.TempDir(), Focus: []string{"../x"}}); err == nil {
		t.Fatal("a focus outside the repository is refused")
	}
	// a cancelled map stops and says so (the AGENTS.md section bounds mapping with a deadline)
	root := t.TempDir()
	rels := writeFiles(t, root, map[string]string{"a.go": "//def LookupValue\n"})
	m = countingMapper(func(context.Context, string) ([]string, error) { return rels, nil }, "", &calls)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Map(ctx, Request{Root: root}); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled map: %v", err)
	}
}

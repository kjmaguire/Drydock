package context

import (
	"context"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/memory"
)

func itemOf(t *testing.T, items []Item, source string) Item {
	t.Helper()
	for _, it := range items {
		if it.Source == source {
			return it
		}
	}
	t.Fatalf("no manifest item %q in %+v", source, items)
	return Item{}
}

func bulk(word string, n int) string { return strings.Repeat(word+" line of text\n", n) }

// Under its ceiling a package is every piece, rendered in order, with one line saying how big it is.
func TestAssembleUnderTheCeilingKeepsEverything(t *testing.T) {
	ps := []Piece{
		{Kind: KindProjectInstructions, Source: "file:AGENTS.md", Counted: true, Bytes: 400},
		{Kind: KindTask, Source: "task", Text: "Task: add a flag\n"},
		{Kind: KindFileRange, Source: "file:x.go [1-10]", Text: "package x\n"},
	}
	a := Assemble(10000, ps)
	if a.Text != "Task: add a flag\npackage x\n" || len(a.LeftOut()) != 0 || a.Over {
		t.Fatalf("%+v", a)
	}
	if a.Tokens != 100+EstimateTokens("Task: add a flag\n")+EstimateTokens("package x\n") {
		t.Fatalf("tokens %d", a.Tokens)
	}
	if it := itemOf(t, a.Items, "budget:package"); !strings.Contains(it.Reason, "nothing cut") {
		t.Fatalf("%+v", it)
	}
	if a := Assemble(0, ps); !strings.Contains(a.Items[len(a.Items)-1].Reason, "no package ceiling") || len(a.Pieces) != 3 {
		t.Fatalf("no ceiling: %+v", a)
	}
}

// Over the ceiling, pieces go in the fixed order (files, then the repo map, memory, design, spec, diff, handoff, check output)
// whatever their position in the package, each cut or left out piece is in the manifest with its reason and pointer, and the
// task and instructions are untouched.
func TestAssembleCutsInTheFixedOrderAndListsWhatWasLeftOut(t *testing.T) {
	task := "Task: fix the parser\n\nAcceptance criteria:\n- parses empty input\n"
	ps := []Piece{
		{Kind: KindHandoff, Source: "handoff:hnd_1", Text: bulk("handoff", 40), Pointer: "the handoff tool reads it whole"},
		{Kind: KindProjectInstructions, Source: "file:AGENTS.md", Counted: true, Bytes: 8000},
		{Kind: KindTask, Source: "task", Text: task},
		{Kind: KindSpec, Source: "spec", Text: bulk("spec", 60)},
		{Kind: KindFileRange, Source: "file:a.go [1-40]", Text: bulk("a.go", 50), Pointer: "read_range on a.go"},
		{Kind: KindFileWhole, Source: "file:b.go", Text: bulk("b.go", 50), Pointer: "read b.go"},
		{Kind: KindFeedback, Source: "feedback:1", Text: bulk("failing test", 30)},
		{Kind: KindRoleInstructions, Source: "turn", Text: "Work in the current directory.\n"},
	}
	full := Assemble(0, ps).Tokens
	// room for everything but the two files and about half the spec
	ceiling := full - EstimateTokens(ps[4].Text) - EstimateTokens(ps[5].Text) - EstimateTokens(ps[3].Text)/2
	a := Assemble(ceiling, ps)
	if a.Tokens > ceiling || a.Over {
		t.Fatalf("tokens %d over ceiling %d", a.Tokens, ceiling)
	}
	for _, src := range []string{"file:a.go [1-40]", "file:b.go"} {
		if it := itemOf(t, a.Items, src); it.Included || it.Exclusion != ExclusionOverBudget || !strings.Contains(it.Reason, "left out: over the package ceiling") || !strings.Contains(it.Reason, "read") {
			t.Fatalf("%s: %+v", src, it)
		}
	}
	spec := itemOf(t, a.Items, "spec")
	if !spec.Included || spec.Exclusion != ExclusionOverBudget || !strings.Contains(spec.Reason, "cut from") {
		t.Fatalf("spec: %+v", spec)
	}
	for _, src := range []string{"handoff:hnd_1", "feedback:1", "task", "turn", "file:AGENTS.md"} {
		if it := itemOf(t, a.Items, src); !it.Included || it.Exclusion != "" {
			t.Fatalf("%s should be whole: %+v", src, it)
		}
	}
	if !strings.Contains(a.Text, task) || !strings.Contains(a.Text, "[cut to fit the context ceiling") || strings.Contains(a.Text, "a.go line") {
		t.Fatalf("text:\n%s", a.Text)
	}
	if !strings.HasSuffix(a.Text, "Work in the current directory.\n") {
		t.Fatal("pieces must keep their order")
	}
	if len(a.LeftOut()) != 3 {
		t.Fatalf("left out %+v", a.LeftOut())
	}
	// the same pieces in another order are cut the same way: the order is by kind, not by position
	rev := append([]Piece(nil), ps...)
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	b := Assemble(ceiling, rev)
	for _, it := range a.LeftOut() {
		if got := itemOf(t, b.Items, it.Source); got.Included != it.Included || got.Exclusion != it.Exclusion {
			t.Fatalf("%s: %+v against %+v", it.Source, got, it)
		}
	}
}

// When the task and instructions alone are over the ceiling, everything that may go goes, nothing else is touched, and the
// manifest says so.
func TestTheTaskAndInstructionsAreNeverCut(t *testing.T) {
	task := bulk("task", 200)
	ps := []Piece{
		{Kind: KindProjectInstructions, Source: "file:AGENTS.md", Counted: true, Bytes: 4000},
		{Kind: KindTask, Source: "task", Text: task},
		{Kind: KindPackInstructions, Source: "pack:x", Text: bulk("pack", 20)},
		{Kind: KindDesign, Source: "design", Text: bulk("design", 20)},
		{Kind: KindFeedback, Source: "feedback:1", Text: bulk("check", 20)},
	}
	a := Assemble(500, ps)
	if !a.Over || !strings.Contains(a.Text, task) || !strings.Contains(a.Text, "pack line") {
		t.Fatalf("%+v", a.Items)
	}
	for _, src := range []string{"design", "feedback:1"} {
		if it := itemOf(t, a.Items, src); it.Included {
			t.Fatalf("%s kept: %+v", src, it)
		}
	}
	if it := itemOf(t, a.Items, "budget:package"); !strings.HasPrefix(it.Reason, "OVER THE CEILING") {
		t.Fatalf("%+v", it)
	}
	for _, k := range []string{KindTask, KindRoleInstructions, KindProjectInstructions, KindPackInstructions, KindWorkflowState} {
		if Cuttable(k) {
			t.Fatalf("%s must never be cut", k)
		}
	}
}

// A piece with its own cutter is cut by it (a diff keeps its file list), and a cut it cannot fit leaves the piece out.
func TestAPieceCutsItselfWhenItKnowsHow(t *testing.T) {
	calls := 0
	ps := []Piece{
		{Kind: KindTask, Source: "task", Text: "Task: review\n"},
		{Kind: KindDiff, Source: "diff", Text: bulk("+ added", 400), Cut: func(max int) string {
			calls++
			return "Diff TRUNCATED; files: a.go\n"[:min(max, 28)]
		}},
	}
	a := Assemble(200, ps)
	if calls != 1 || !strings.Contains(a.Text, "Diff TRUNCATED") || itemOf(t, a.Items, "diff").Exclusion != ExclusionOverBudget {
		t.Fatalf("%d %+v", calls, a)
	}
	ps[1].Cut = func(max int) string { return strings.Repeat("x", max+1) }
	if a := Assemble(200, ps); itemOf(t, a.Items, "diff").Included {
		t.Fatal("a cutter that cannot fit leaves the piece out")
	}
}

// The generated AGENTS.md keeps within its share of the project's ceiling: the repo map goes first, then memory is cut; pack
// instructions and the standing instructions stay; the file says what was left out and the manifest lists it.
func TestAGENTSMDKeepsWithinItsShareOfTheCeiling(t *testing.T) {
	e, ms := newEngine(t)
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		ms.Add(ctx, memory.Entry{ProjectID: "p1", Kind: memory.Convention, Title: "Convention " + string(rune('A'+i)), Body: strings.Repeat("Prefer explicit names. ", 25)}, true)
	}
	repoMap := bulk("internal/x/file.go: func Something()", 120)
	e.Sections = func(context.Context, string, string) ([]Section, error) {
		return []Section{{Title: RepoMapSectionTitle, Body: repoMap, Source: "repo_map", Priority: 40},
			{Title: "Expert packs", Body: "Use the cloudflare pack.", Source: "pack:cloudflare", Priority: 30}}, nil
	}
	files, m, err := e.Generate(ctx, "p1", nil)
	if err != nil {
		t.Fatal(err)
	}
	whole := files["AGENTS.md"]
	if !strings.Contains(whole, "internal/x/file.go") || strings.Contains(whole, LeftOutPrefix) {
		t.Fatal("without a ceiling nothing is cut")
	}
	ceiling := 2 * (EstimateTokens(whole) - EstimateTokens(repoMap) - 300)
	e.Ceiling = func(context.Context, string) int { return ceiling }
	files, m, err = e.Generate(ctx, "p1", nil)
	if err != nil {
		t.Fatal(err)
	}
	agents := files["AGENTS.md"]
	if EstimateTokens(agents) > ceiling/InstructionShare+60 { // the header and the note are outside the assembly
		t.Fatalf("AGENTS.md is %d tokens, share %d", EstimateTokens(agents), ceiling/InstructionShare)
	}
	if strings.Contains(agents, "internal/x/file.go") || !strings.Contains(agents, "Use the cloudflare pack.") || !strings.Contains(agents, "Prefer small PRs.") {
		t.Fatalf("AGENTS.md:\n%s", agents)
	}
	if !strings.Contains(agents, LeftOutPrefix+"part of project memory (search_memory); the repository map (the repo_map tool makes one).") {
		t.Fatalf("note missing:\n%s", agents)
	}
	if it := itemOf(t, m.Items, "repo_map"); it.Included || it.Exclusion != ExclusionOverBudget {
		t.Fatalf("%+v", it)
	}
	if it := itemOf(t, m.Items, "pack:cloudflare"); !it.Included {
		t.Fatalf("%+v", it)
	}
	if it := itemOf(t, m.Items, "budget:instruction-files"); !strings.Contains(it.Reason, "AGENTS.md (at most 1/2") {
		t.Fatalf("%+v", it)
	}
	cutMemory := 0
	for _, it := range m.Items {
		if strings.HasPrefix(it.Source, "memory:") && strings.Contains(it.Reason, "cut to fit the package ceiling") {
			cutMemory++
		}
	}
	if cutMemory == 0 {
		t.Fatalf("memory items do not say the section was cut: %+v", m.Items)
	}
}

package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

func msg(v any) workflow.Message {
	b, _ := json.Marshal(v)
	return workflow.Message{Value: b}
}

var subtract = tasks.Task{Title: "Implement Subtract", Description: "calc.go has a stub Subtract that returns 0.", Acceptance: []string{"AC1: Given 5 and 3, when Subtract is called, then it returns 2"}}

// Item 1: every pass carries the task; human guidance is readable text, never raw JSON.
func TestWorkPromptKeepsTheTaskOnEveryPassAndRendersGuidanceAsText(t *testing.T) {
	first := WorkPrompt(subtract, workflow.NodeInput{}, PromptContext{})
	if !strings.Contains(first, "Task: Implement Subtract") || !strings.Contains(first, "stub Subtract") || !strings.Contains(first, "AC1: Given 5") || !strings.Contains(first, "Work in the current directory") {
		t.Fatalf("first pass: %q", first)
	}
	in := workflow.NodeInput{Inbox: []workflow.Message{
		msg(map[string]any{"decision": "answered", "by": "human:k", "data": map[string]any{"text": "use the minus operator"}}),
	}}
	p := WorkPrompt(subtract, in, PromptContext{})
	for _, want := range []string{"Task: Implement Subtract", "stub Subtract", "AC1: Given 5", "previous attempt needs changes", "Guidance from the human:\nuse the minus operator"} {
		if !strings.Contains(p, want) {
			t.Fatalf("rework pass lacks %q: %q", want, p)
		}
	}
	if strings.Contains(p, `{"text"`) || strings.Contains(p, `"decision"`) {
		t.Fatalf("guidance must not be raw JSON: %q", p)
	}
	// an object without a text field becomes key: value lines; a bare continue is not guidance
	p = WorkPrompt(subtract, workflow.NodeInput{Inbox: []workflow.Message{msg(map[string]any{"decision": "approved", "data": map[string]any{"focus": "negative numbers", "action": "continue"}})}}, PromptContext{})
	if !strings.Contains(p, "focus: negative numbers") || strings.Contains(p, "action") {
		t.Fatalf("object guidance: %q", p)
	}
	if p := WorkPrompt(subtract, workflow.NodeInput{Inbox: []workflow.Message{msg(map[string]any{"decision": "approved", "data": map[string]any{}})}}, PromptContext{}); strings.Contains(p, "Guidance") {
		t.Fatalf("an empty answer is not guidance: %q", p)
	}
	// guidance from the team lead (resume's output) is labelled as the lead's
	p = WorkPrompt(subtract, workflow.NodeInput{Inbox: []workflow.Message{msg(map[string]any{"guidance": "stop re-reading calc.go; edit it", "from": "lead"})}}, PromptContext{})
	if !strings.Contains(p, "Guidance from the team lead:\nstop re-reading calc.go") {
		t.Fatalf("lead guidance: %q", p)
	}
	// a long description is compact on a rework pass
	long := subtract
	long.Description = strings.Repeat("x", 5000)
	if p := WorkPrompt(long, in, PromptContext{}); strings.Count(p, "x") > maxDescRework+50 {
		t.Fatalf("rework description not compact: %d", strings.Count(p, "x"))
	}
}

// Items 2 and 9: the brief, the accepted spec and the design notes go into the builder's prompt.
func TestWorkPromptCarriesBriefSpecAndDesign(t *testing.T) {
	p := WorkPrompt(subtract, workflow.NodeInput{}, PromptContext{Brief: "A calculator library for kids.", Spec: "## Problem\nNo subtraction.", Design: "Touch calc.go only."})
	for _, want := range []string{"Project brief, written by the human", "A calculator library for kids.", "The spec a human accepted", "No subtraction.", "Design notes from the architect", "Touch calc.go only."} {
		if !strings.Contains(p, want) {
			t.Fatalf("missing %q: %q", want, p)
		}
	}
	if strings.Index(p, "Project brief") > strings.Index(p, "Task:") {
		t.Fatal("the brief comes first (a stable prefix)")
	}
}

// Item 8: a task without criteria asks for a definition of done once, then reminds the worker of the one it stated.
func TestVagueTaskAsksForADefinitionOfDone(t *testing.T) {
	vague := tasks.Task{Title: "Make it faster", Description: "The app is slow."}
	p := WorkPrompt(vague, workflow.NodeInput{}, PromptContext{})
	if !strings.Contains(p, `write "Definition of done:"`) || !strings.Contains(p, "1 to 3 short bullet points") {
		t.Fatalf("no definition-of-done ask: %q", p)
	}
	if p := WorkPrompt(subtract, workflow.NodeInput{}, PromptContext{}); strings.Contains(p, "Definition of done") {
		t.Fatal("a task with criteria needs no definition of done")
	}
	p = WorkPrompt(vague, workflow.NodeInput{}, PromptContext{DoD: "- startup under 1s"})
	if !strings.Contains(p, "The definition of done you stated earlier") || !strings.Contains(p, "- startup under 1s") || strings.Contains(p, "1 to 3 short bullet points") {
		t.Fatalf("reminder: %q", p)
	}
	text := "Plan first.\n\nDefinition of done:\n- cold start under 1 second\n* no new dependencies\n1. benchmark added\n- a fourth one\n\nThen I changed main.go."
	if got := ExtractDoD(text); got != "- cold start under 1 second\n- no new dependencies\n- benchmark added" {
		t.Fatalf("ExtractDoD = %q", got)
	}
	if ExtractDoD("I did the work.") != "" {
		t.Fatal("no definition of done means empty")
	}
	// the reviewer checks against the task text and the stated definition
	ev := []tasks.Evidence{{Kind: "test", Summary: "test passed"}, {Kind: dodKind, Summary: "- cold start under 1 second"}}
	rp := ReviewPrompt(vague, ev, "diff --git a/x b/x\n")
	if !strings.Contains(rp, "definition of done the worker stated") || !strings.Contains(rp, "cold start under 1 second") || strings.Contains(rp, "- definition_of_done") {
		t.Fatalf("review prompt: %q", rp)
	}
	if rp := ReviewPrompt(vague, nil, ""); !strings.Contains(rp, "stated no definition of done: check the work against the task text") {
		t.Fatalf("review prompt without a stated definition: %q", rp)
	}
}

func TestHumanText(t *testing.T) {
	for in, want := range map[string]string{
		`"just a string"`:                    "just a string",
		`{"feedback":"fix nil"}`:             "fix nil",
		`{"text":"","guidance":"use a map"}`: "use a map",
		`{"b":"two","a":1}`:                  "a: 1\nb: two",
		`null`:                               "",
		`{}`:                                 "",
		`{"action":"continue"}`:              "",
	} {
		if got := HumanText(json.RawMessage(in)); got != want {
			t.Errorf("%s -> %q want %q", in, got, want)
		}
	}
}

func TestSpecContextDropsTheCriteriaSection(t *testing.T) {
	body := "# Multiply\n## Problem\nNo multiply.\n## Scope and non-goals\nIntegers only.\n## Acceptance criteria\n- Given 3 and 4, when Multiply is called, then it returns 12\n## Open questions\nnone"
	got := SpecContext(body)
	if !strings.Contains(got, "No multiply.") || !strings.Contains(got, "Integers only.") || !strings.Contains(got, "## Open questions") || strings.Contains(got, "returns 12") {
		t.Fatalf("SpecContext = %q", got)
	}
}

// Item 7: a big diff is said to be truncated and comes with a per-file stat of the whole change.
func TestReviewPromptSaysWhenTheDiffIsTruncated(t *testing.T) {
	small := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@\n+x\n-y\n"
	if p := ReviewPrompt(subtract, nil, small); strings.Contains(p, "TRUNCATED") || !strings.Contains(p, "+x") {
		t.Fatalf("small diff: %q", p)
	}
	var b strings.Builder
	b.WriteString("diff --git a/big.go b/big.go\n--- a/big.go\n+++ b/big.go\n@@\n")
	for b.Len() < 60<<10 {
		b.WriteString("+" + strings.Repeat("z", 80) + "\n")
	}
	b.WriteString("diff --git a/tail.go b/tail.go\n--- a/tail.go\n+++ b/tail.go\n@@\n+late\n-early\n-gone\n")
	p := ReviewPrompt(subtract, nil, b.String())
	if !strings.Contains(p, "TRUNCATED") || !strings.Contains(p, "- tail.go +1 -2") || !strings.Contains(p, "- big.go +") || strings.Contains(p, "+late") {
		t.Fatalf("truncated diff prompt: %s", p[:600])
	}
	if strings.Count(p, "z") < 40<<10 {
		t.Fatalf("the reviewer sees up to 48 KB of the diff, saw %d bytes", strings.Count(p, "z"))
	}
	if sp := SecurityReviewPrompt(subtract, nil, b.String()); !strings.Contains(sp, "TRUNCATED") {
		t.Fatal("the security reviewer gets the same treatment")
	}
	stats := DiffFileStats(b.String())
	if len(stats) != 2 || stats[1].Path != "tail.go" || stats[1].Insertions != 1 || stats[1].Deletions != 2 {
		t.Fatalf("stats %+v", stats)
	}
}

// Items 2 and 9 for the template turns: design and tests see the description, the accepted spec and the brief.
func TestTemplatePromptsCarryDescriptionSpecAndBrief(t *testing.T) {
	d := DesignPrompt(subtract, "## Problem\nNo subtraction.", "Kids use it.")
	for _, want := range []string{"stub Subtract", "No subtraction.", "Kids use it.", "AC1"} {
		if !strings.Contains(d, want) {
			t.Fatalf("design prompt lacks %q: %q", want, d)
		}
	}
	tp := TestsPrompt(subtract, false, "", "## Problem\nNo subtraction.", "Kids use it.")
	for _, want := range []string{"No subtraction.", "Kids use it.", "AC1"} {
		if !strings.Contains(tp, want) {
			t.Fatalf("tests prompt lacks %q: %q", want, tp)
		}
	}
	if sp := SpecPrompt(subtract, "", "", "Kids use it."); !strings.Contains(sp, "Kids use it.") {
		t.Fatal("spec prompt lacks the brief")
	}
	if s := BriefForInstructions("  "); s != "" {
		t.Fatalf("no brief, no section: %q", s)
	}
	if s := BriefForInstructions(strings.Repeat("b", 9000)); !strings.HasPrefix(s, "Project brief, written by the human") || len(s) > maxBriefFile+200 {
		t.Fatalf("instructions brief not bounded: %d", len(s))
	}
}

// Item 9: the planner sees the open tasks and the team with adapters; the lead is shown but never assignable.
func TestPlanPromptShowsOpenTasksAndTheTeam(t *testing.T) {
	team := []TeamMember{{ID: "a1", Name: "Backend #1", Role: "backend", Adapter: "claude"}, {ID: "l1", Name: "Lead", Role: "team lead", Adapter: "claude"}}
	open := []tasks.Task{{Seq: 3, Title: "Add login", Status: tasks.InProgress}}
	p := PlanPrompt("Ship auth", "", team, "", PlanContext{Brief: "B2B app", Open: open})
	for _, want := range []string{"#3 Add login [IN_PROGRESS]", "Backend #1: backend, claude", "Lead: team lead (supervises the team; never assign it a task), claude", "B2B app"} {
		if !strings.Contains(p, want) {
			t.Fatalf("plan prompt lacks %q: %q", want, p)
		}
	}
	reply := `{"tasks":[{"key":"a","title":"A","description":"d","role":"manager","dependsOn":[],"acceptance":["Given x, when y, then z"]}]}`
	if _, probs := ParseProposal(reply); len(probs) == 0 || !strings.Contains(probs[0], "team lead supervises") {
		t.Fatalf("a task for the lead must be refused: %v", probs)
	}
	if NormalizePlanRole("Supervisor") != RoleLead || !IsLeadRole("Team Lead") || IsLeadRole("backend") {
		t.Fatal("lead synonyms")
	}
}

// Item 6: the spec gate's second lock covers the pull request variants.
func TestSpecGateSecondLockCoversPRVariants(t *testing.T) {
	for w, want := range map[string]bool{"feature": true, "feature-pr": true, "full-stack-saas": true, "full-stack-saas-pr": true, "bug-pr": false, "security": false, "iterate-until-verified-pr": false} {
		if IsGated(w) != want {
			t.Errorf("IsGated(%s) = %v", w, !want)
		}
	}
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bus := events.NewSQLBus(db, 64)
	defer bus.Close()
	db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p1','p','/nowhere',1)`)
	ts := &tasks.Service{DB: db, Bus: bus}
	tk, err := ts.Create(ctx, tasks.CreateReq{ProjectID: "p1", Title: "Gated"})
	if err != nil {
		t.Fatal(err)
	}
	td := TemplateDeps{Deps: Deps{Tasks: ts}}
	input, _ := json.Marshal(TemplateInput{TaskID: tk.ID})
	for _, w := range []string{"feature-pr", "full-stack-saas-pr"} {
		_, err := td.implement()(ctx, workflow.NodeInput{NodeInfo: workflow.NodeInfo{Workflow: w}, Input: input})
		if err == nil || !strings.Contains(err.Error(), "until a human accepts the spec") {
			t.Fatalf("%s: implementation must refuse without an accepted spec, got %v", w, err)
		}
	}
}

func TestRepeatedReviseDetection(t *testing.T) {
	r := func(v, f string) tasks.ReviewRecord { return tasks.ReviewRecord{Verdict: v, Feedback: f} }
	if !RepeatedRevise([]tasks.ReviewRecord{r("REVISE", "Remove the HACK comment."), r("REVISE", "remove the hack comment")}) {
		t.Fatal("same feedback twice (case and punctuation aside) is a repeat")
	}
	if RepeatedRevise([]tasks.ReviewRecord{r("REVISE", "Remove the HACK comment."), r("REVISE", "Handle negative numbers in Subtract and add a test for it.")}) {
		t.Fatal("different feedback is progress")
	}
	if RepeatedRevise([]tasks.ReviewRecord{r("SHIP", ""), r("REVISE", "x y z")}) || RepeatedRevise([]tasks.ReviewRecord{r("REVISE", "a")}) {
		t.Fatal("needs two REVISE rounds")
	}
	if !FeedbackSimilar("The error message on line 12 is wrong; it must name the file.", "The error message on line 12 is wrong, it must name the file!") {
		t.Fatal("near-identical feedback is similar")
	}
}

// Item 2: the accepted spec is read from the spec_accept evidence, without its criteria section.
func TestAcceptedSpecComesFromHumanAcceptance(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.OpenTemp(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bus := events.NewSQLBus(db, 64)
	defer bus.Close()
	db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p1','p','/nowhere',1)`)
	ts := &tasks.Service{DB: db, Bus: bus}
	tk, err := ts.Create(ctx, tasks.CreateReq{ProjectID: "p1", Title: "Spec'd"})
	if err != nil {
		t.Fatal(err)
	}
	d := Deps{Tasks: ts}
	if d.acceptedSpec(ctx, tk.ID) != "" {
		t.Fatal("no acceptance, no spec")
	}
	path := filepath.Join(dir, "spec.md")
	os.WriteFile(path, []byte("# T\n## Problem\nSlow.\n## Acceptance criteria\n- Given a, when b, then c\n"), 0o644)
	ok := true
	ts.AddEvidence(ctx, tk.ID, tasks.Evidence{Kind: "spec_accept", Passed: &ok, Ref: path, Summary: "accepted"})
	if got := d.acceptedSpec(ctx, tk.ID); !strings.Contains(got, "Slow.") || strings.Contains(got, "then c") {
		t.Fatalf("accepted spec %q", got)
	}
	if b := (Deps{ProjectBrief: func(context.Context, string) string { return strings.Repeat("q", 5000) }}).brief(ctx, "p1"); len(b) > maxBriefPrompt+20 {
		t.Fatalf("the brief in a prompt is bounded: %d", len(b))
	}
}

// The brief reaches a task turn once: from the worktree's AGENTS.md when that file carries it, otherwise in the prompt.
func TestTheBriefIsSentOncePerTurn(t *testing.T) {
	ctx := context.Background()
	brief := "A calculator library for primary-school pupils. Keep every message short and friendly."
	d := Deps{ProjectBrief: func(context.Context, string) string { return brief }}
	wt := t.TempDir()
	tk := tasks.Task{ProjectID: "p1", Worktree: wt}
	if got := d.taskBrief(ctx, tk); got != brief {
		t.Fatalf("no instruction file: the prompt carries the brief, got %q", got)
	}
	os.WriteFile(filepath.Join(wt, "AGENTS.md"), []byte("# Project instructions\n\n## Standing instructions\n\n"+BriefForInstructions(brief)+"\n"), 0o644)
	if got := d.taskBrief(ctx, tk); got != "" {
		t.Fatalf("AGENTS.md carries the brief: the prompt must not repeat it, got %q", got)
	}
	if p := WorkPrompt(tasks.Task{Title: "Add Subtract"}, workflow.NodeInput{}, PromptContext{Brief: d.taskBrief(ctx, tk)}); strings.Contains(p, "primary-school") {
		t.Fatalf("work prompt repeats the brief:\n%s", p)
	}
	// a person edited AGENTS.md and the brief is gone from it: the prompt carries it again
	os.WriteFile(filepath.Join(wt, "AGENTS.md"), []byte("# My own notes\n"), 0o644)
	if got := d.taskBrief(ctx, tk); got != brief {
		t.Fatalf("an AGENTS.md without the brief: got %q", got)
	}
}

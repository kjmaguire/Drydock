package trial

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/sandbox/sandboxtest"
	"github.com/kjmaguire/drydock/internal/skills"
)

// The arms' agent in these tests is the repository's scripted stand-in for Claude Code (internal/agents/fakeagent, a TEST
// DOUBLE): this test binary re-executes itself as it. The real CLI runs in the trial itself (docs/evals.md section 13).
func TestMain(m *testing.M) {
	if code, ok := mcp.MaybeRunShim(os.Args); ok {
		os.Exit(code)
	}
	if os.Getenv("DRYDOCK_FAKE_AGENT") == "1" {
		os.Exit(fakeagent.Run(os.Args[1:], os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func suite(t *testing.T) []Task {
	t.Helper()
	root := repoRoot(t)
	// The Windows verification bundle runs this compiled test from a folder of its own, which holds no repository: no go.mod
	// here means no source checkout (a checkout that lost evals/tasks still fails). CI's go and trial-suite jobs run these tests.
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skip("not run from the source checkout: the repository's evals/ folder is not here")
	}
	ts, err := LoadSuite(root, filepath.Join(root, "evals", "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func task(t *testing.T, id string) Task {
	t.Helper()
	ts, err := FindTasks(suite(t), []string{id})
	if err != nil {
		t.Fatal(err)
	}
	return ts[0]
}

func TestTheSuiteLoadsEveryTaskWithItsHiddenCheck(t *testing.T) {
	ts := suite(t)
	keep := regexp.MustCompile(`Keep (the )?existing exported`)
	for _, x := range ts {
		if x.Acceptance.TimeoutSeconds <= 0 || len(x.Acceptance.Commands) == 0 || !keep.MatchString(x.Prompt) || x.Suite == "" {
			t.Errorf("%s: suite %q, %+v", x.ID, x.Suite, x.Acceptance)
		}
	}
	pilot, err := InSuite(ts, "pilot")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, x := range pilot {
		ids = append(ids, x.ID)
	}
	if strings.Join(ids, ",") != "saasapi-csv-export,saasapi-invoice-pages,saasapi-invoice-rules,saasapi-rate-limit" {
		t.Fatalf("pilot tasks %v", ids)
	}
	tally, err := InSuite(ts, "tally")
	if err != nil || len(tally) < ClaimTasks {
		t.Fatalf("the tally suite has %d tasks (%v); decision P34 needs %d", len(tally), err, ClaimTasks)
	}
	classes := map[string]bool{}
	for _, x := range tally {
		classes[x.Class] = true
		if x.Fixture != "testdata/fixtures/tally" || !strings.HasPrefix(x.ID, "tally-") {
			t.Errorf("%s: fixture %s", x.ID, x.Fixture)
		}
	}
	if len(classes) < 4 {
		t.Errorf("the tally suite covers only the classes %v", classes)
	}
	if _, err := InSuite(ts, "nope"); err == nil {
		t.Fatal("an unknown suite is not an error")
	}
	if _, err := FindTasks(ts, []string{"saasapi-rate-limit", "nope"}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("an unknown task id: %v", err)
	}
}

// A reference solution can be a patch; one that does not apply, or that the hidden check rejects, fails the self-check.
func TestSelfCheckAppliesAPatchSolution(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("fx/go.mod", "module example.com/fx\n\ngo 1.24\n")
	write("fx/add.go", "package fx\n\nfunc Add(a, b int) int { return a - b }\n")
	write("hidden/p/add_hidden_test.go", "package fx\n\nimport \"testing\"\n\nfunc TestHiddenAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(Add(2, 3))\n\t}\n}\n")
	// the hidden folder mirrors the fixture's layout: its root is the module root
	if err := os.Rename(filepath.Join(root, "hidden", "p", "add_hidden_test.go"), filepath.Join(root, "hidden", "add_hidden_test.go")); err != nil {
		t.Fatal(err)
	}
	x := Task{ID: "fx-add", Fixture: "fx", Acceptance: Acceptance{Files: "hidden", Commands: []string{"go test -count=1 ./..."}, TimeoutSeconds: 120}}
	write(SolutionsDir+"/fx-add.patch", "--- a/add.go\n+++ b/add.go\n@@ -1,3 +1,3 @@\n package fx\n \n-func Add(a, b int) int { return a - b }\n+func Add(a, b int) int { return a + b }\n")
	if err := SelfCheck(context.Background(), root, x); err != nil {
		t.Fatalf("a good patch: %v", err)
	}
	write(SolutionsDir+"/fx-add.patch", "--- a/add.go\n+++ b/add.go\n@@ -1,3 +1,3 @@\n package fx\n \n-func Add(a, b int) int { return a - b }\n+func Add(a, b int) int { return a * b }\n")
	if err := SelfCheck(context.Background(), root, x); err == nil || !strings.Contains(err.Error(), "fails on the reference solution") {
		t.Fatalf("a wrong patch: %v", err)
	}
	write(SolutionsDir+"/fx-add.patch", "--- a/nothere.go\n+++ b/nothere.go\n@@ -1 +1 @@\n-x\n+y\n")
	if err := SelfCheck(context.Background(), root, x); err == nil || !strings.Contains(err.Error(), "does not apply") {
		t.Fatalf("a patch that does not apply: %v", err)
	}
}

func TestATaskFileWithAMisspelledFieldIsRefused(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	body := "id: typo\ntitle: T\nfixture: testdata/fixtures/saasapi\nprompt: p\nacceptence:\n  commands: [\"true\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "typo.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSuite(root, dir); err == nil || !strings.Contains(err.Error(), "acceptence") {
		t.Fatalf("a misspelled field must be an error, not a task without a hidden check: %v", err)
	}
}

// The hidden checks prove something only if they fail on the untouched fixture and pass on a correct solution.
func TestEveryHiddenCheckFailsOnTheFixtureAndPassesOnTheReferenceSolution(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and tests every task's fixture twice")
	}
	root := repoRoot(t)
	for _, x := range suite(t) {
		t.Run(x.ID, func(t *testing.T) {
			t.Parallel()
			if err := SelfCheck(context.Background(), root, x); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPrepareStartsBothArmsFromOneCommitOfTheFixture(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	start, err := Prepare(ctx, repoRoot(t), task(t, "saasapi-invoice-rules"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := git(ctx, dir, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("%s commits", n)
	}
	if b, _ := git(ctx, dir, "branch", "--show-current"); b != "main" {
		t.Fatalf("branch %q", b)
	}
	if head, _ := git(ctx, dir, "rev-parse", "HEAD"); head != start || len(start) != 40 {
		t.Fatalf("start %q head %q", start, head)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "billing", "invoice.go")); err != nil {
		t.Fatal(err)
	}
	if out, _ := git(ctx, dir, "ls-files"); strings.Contains(out, "zz_hidden") {
		t.Fatalf("a hidden check is in the arm's repository:\n%s", out)
	}
}

func TestResultDiffShowsWhatAnArmChangedAndNothingOfDrydocks(t *testing.T) {
	ctx := context.Background()
	root := repoRoot(t)
	x := task(t, "saasapi-invoice-rules")
	dir := t.TempDir()
	if _, err := Prepare(ctx, root, x, dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "NOTES.md"), []byte("new file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".drydock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".drydock", "sandbox.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := ResultDiff(ctx, root, x, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "result/NOTES.md") || !strings.Contains(d, "+# changed") || strings.Contains(d, "sandbox.json") {
		t.Fatalf("diff:\n%s", d)
	}
}

func TestAcceptStopsAtTheFirstFailingCommandAndKeepsItsOutput(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"fixture/a.txt", "hidden/h.txt"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	x := Task{ID: "t", Fixture: "fixture", Acceptance: Acceptance{Files: "hidden", TimeoutSeconds: 30,
		Commands: []string{"test -f h.txt && test -f a.txt && echo both-here", "echo failing; exit 3", "echo never"}}}
	res := Accept(context.Background(), root, x, filepath.Join(root, "fixture"))
	if res.Passed || res.Error != "" || len(res.Commands) != 2 {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(res.Commands[0].Output, "both-here") || res.Commands[1].ExitCode != 3 || !strings.Contains(res.Commands[1].Output, "failing") {
		t.Fatalf("%+v", res.Commands)
	}
	if _, err := os.Stat(filepath.Join(root, "fixture", "h.txt")); err == nil {
		t.Fatal("the hidden files were laid over the arm's own tree instead of a copy")
	}
	x.Acceptance.Commands = []string{"sleep 5"}
	x.Acceptance.TimeoutSeconds = 1
	if res := Accept(context.Background(), root, x, filepath.Join(root, "fixture")); res.Passed || !res.Commands[0].TimedOut {
		t.Fatalf("time limit: %+v", res)
	}
}

// fakeScript points the stand-in at a script for this test.
func fakeScript(t *testing.T, s fakeagent.Script) []string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return []string{"DRYDOCK_FAKE_AGENT=1", "DRYDOCK_FAKE_SCRIPT=" + p}
}

func solution(t *testing.T, id, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), SolutionsDir, id, path))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestB1RunsOnePlainSessionAndReadsWhatTheCLIReports(t *testing.T) {
	ctx := context.Background()
	root := repoRoot(t)
	x := task(t, "saasapi-invoice-rules")
	repo := t.TempDir()
	if _, err := Prepare(ctx, root, x, repo); err != nil {
		t.Fatal(err)
	}
	argsLog := filepath.Join(t.TempDir(), "args.log")
	env := fakeScript(t, fakeagent.Script{Default: []fakeagent.Step{
		{Tool: "Bash", Input: json.RawMessage(`{"command":"go test ./..."}`), Result: "ok"},
		{Write: &fakeagent.WriteSpec{Path: "internal/billing/invoice.go", Content: solution(t, x.ID, "internal/billing/invoice.go")}},
		{Say: "Fixed the rules."},
	}})
	b := B1{Bin: os.Args[0], Model: "haiku", CeilingUSD: 2, Wall: time.Minute, Root: root, Hidden: []string{"evals/testdata", "zz_hidden_"},
		ExtraEnv: append(env, "DRYDOCK_FAKE_ARGS_LOG="+argsLog)}
	var transcript bytes.Buffer
	rec := b.Run(ctx, x, repo, &transcript)
	if rec.Outcome != "" || rec.Reason != "" {
		t.Fatalf("the session did not finish: %s %s", rec.Outcome, rec.Reason)
	}
	if rec.Sessions != 1 || rec.CostUSD != 0.0002 || rec.ModelTurns != 0 || rec.ToolUses["Bash"] != 1 || rec.ToolUses["Write"] != 1 ||
		strings.Join(rec.ModelsUsed, ",") != "fake-1" || rec.Tokens.Input != 10 || rec.SawHiddenFiles {
		t.Fatalf("record %+v", rec)
	}
	if !strings.Contains(transcript.String(), `"type":"result"`) {
		t.Fatalf("transcript %s", transcript.String())
	}
	args, _ := os.ReadFile(argsLog)
	for _, want := range []string{`"-p","Fix invoice totals`, `"--permission-mode","acceptEdits"`, `"--allowedTools","Bash"`, `"--max-budget-usd","2.00"`, `"--model","haiku"`, `"--no-session-persistence"`} {
		if !strings.Contains(string(args), want) {
			t.Errorf("argv lacks %s: %s", want, args)
		}
	}
	if res := Accept(ctx, root, x, repo); !res.Passed {
		t.Fatalf("the stand-in wrote the reference solution, the hidden check must pass: %+v", res)
	}
	for _, kv := range b.Env([]string{"PATH=/bin", "GITHUB_TOKEN=x", "CLAUDE_CODE_REMOTE_SESSION_ID=outer", "CLAUDE_CODE_OAUTH_TOKEN=y"}) {
		if strings.HasPrefix(kv, "GITHUB_TOKEN") || strings.HasPrefix(kv, "CLAUDE_CODE_REMOTE_SESSION_ID") {
			t.Errorf("B1's environment keeps %s", kv)
		}
	}
}

func TestB1NoticesASessionThatReadTheHiddenChecks(t *testing.T) {
	ctx := context.Background()
	root := repoRoot(t)
	x := task(t, "saasapi-invoice-rules")
	repo := t.TempDir()
	if _, err := Prepare(ctx, root, x, repo); err != nil {
		t.Fatal(err)
	}
	peek, _ := json.Marshal(map[string]string{"command": "cat " + filepath.Join(root, "evals", "testdata", "hidden", x.ID, "internal", "billing", "zz_hidden_rules_test.go")})
	env := fakeScript(t, fakeagent.Script{Default: []fakeagent.Step{{Tool: "Bash", Input: peek, Result: "..."}, {Say: "done"}}})
	rec := B1{Bin: os.Args[0], Model: "haiku", CeilingUSD: 2, Wall: time.Minute, Root: root, Hidden: []string{"evals/testdata", "zz_hidden_"}, ExtraEnv: env}.Run(ctx, x, repo, &bytes.Buffer{})
	if !rec.SawHiddenFiles || rec.Counted() {
		t.Fatalf("a session that read the hidden check must be marked and not counted: %+v", rec)
	}
}

// The Drydock arm with the stand-in as both worker and reviewer, running the project's checks on this machine (P24): the
// real core, workflow, worktree, checks, review and merge, with the hidden check standing in for the human.
func TestTheDrydockArmAcceptsOrRejectsOnTheHiddenCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the whole product loop twice")
	}
	root := repoRoot(t)
	x := task(t, "saasapi-invoice-rules")
	for _, c := range []struct {
		name, impl, want string
	}{
		{"correct", solution(t, x.ID, "internal/billing/invoice.go"), Accepted},
		{"plausible but wrong", strings.Replace(solution(t, x.ID, "internal/billing/invoice.go"), "return (n + d/2) / d", "return n / d", 1), Rejected},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			for _, kv := range fakeScript(t, fakeagent.Script{
				Rules:   []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
				Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "internal/billing/invoice.go", Content: c.impl}}, {Say: "implemented the rules"}},
			}) {
				k, v, _ := strings.Cut(kv, "=")
				t.Setenv(k, v)
			}
			dir := t.TempDir()
			repo := filepath.Join(dir, "repo")
			if _, err := Prepare(ctx, root, x, mustMkdir(t, repo)); err != nil {
				t.Fatal(err)
			}
			defer func(n int) { eventPage = n }(eventPage)
			eventPage = 7
			d := Drydock{Root: root, DataDir: filepath.Join(dir, "data"), Model: "haiku", CeilingUSD: 2, Wall: 5 * time.Minute, Mode: ModeChecks,
				Poll: 200 * time.Millisecond, Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}}}
			var diff string
			rec, art := d.Run(ctx, x, repo, func(tree string) AcceptResult {
				diff, _ = ResultDiff(ctx, root, x, tree)
				return Accept(ctx, root, x, tree)
			})
			if rec.Outcome != c.want || rec.EvalAccept == nil || rec.EvalAccept.Passed != (c.want == Accepted) {
				t.Fatalf("outcome %s (%s), accept %+v", rec.Outcome, rec.Reason, rec.EvalAccept)
			}
			if len(rec.Reviews) == 0 || rec.Reviews[0].Verdict != "SHIP" || rec.Sessions < 2 || rec.CostUSD <= 0 || !strings.Contains(diff, "roundDiv") {
				t.Fatalf("record %+v\ndiff %s", rec, diff)
			}
			if len(art.Events) == 0 || len(art.Usage) == 0 {
				t.Fatal("no events or usage exported")
			}
			// the export holds the whole task, up to its review and the merge approval, read in pages (it once stopped at the
			// core's 500): the page size is small here, so this run needed many pages
			var all []json.RawMessage
			if err := json.Unmarshal(art.Events, &all); err != nil || len(all) <= 3*eventPage || !strings.Contains(string(art.Events), `"task.review"`) {
				t.Fatalf("the exported events stop early: %d events with pages of %d (%v)", len(all), eventPage, err)
			}
			if c.want == Accepted {
				if b, _ := os.ReadFile(filepath.Join(repo, "internal", "billing", "invoice.go")); !strings.Contains(string(b), "roundDiv") {
					t.Fatal("the accepted change was not merged into main")
				}
			}
		})
	}
}

// With -skills-dir and -prebuilt-skills together, the repository's own skills are rules and the prebuilt agents' skills are
// files to read on demand; a prebuilt skill with a repository skill's name is not imported (the repository's wins, with a
// note). SkillReads counts the Reads of files that were delivered on demand: a Read of a rule, or of an on-demand path
// nothing was written to, does not count.
func TestTheDrydockArmGivesTheBundlesSkillsAndCountsTheirReads(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the whole product loop")
	}
	root := repoRoot(t)
	x := task(t, "saasapi-invoice-rules")
	ctx := context.Background()
	refs, err := PrebuiltSkillRefs()
	if err != nil || len(refs) < 2 {
		t.Fatalf("%v %v", refs, err)
	}
	listed, taken := refs[0].Name, refs[1].Name
	for _, kv := range fakeScript(t, fakeagent.Script{
		Rules: []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
		Default: []fakeagent.Step{
			{Tool: "Read", Input: json.RawMessage(`{"file_path":"/w/.claude/drydock-skills/` + listed + `.md"}`), Result: "the skill"},
			{Tool: "Read", Input: json.RawMessage(`{"file_path":"/w/.claude/drydock-skills/house-style.md"}`), Result: "no such file"},
			{Tool: "Read", Input: json.RawMessage(`{"file_path":"/w/.claude/rules/house-style.md"}`), Result: "the rule"},
			{Tool: "Read", Input: json.RawMessage(`{"file_path":"/w/internal/billing/invoice.go"}`), Result: "code"},
			{Write: &fakeagent.WriteSpec{Path: "internal/billing/invoice.go", Content: solution(t, x.ID, "internal/billing/invoice.go")}}, {Say: "done"}},
	}) {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	dir := t.TempDir()
	bundle := filepath.Join(dir, "bundle")
	// the declared name differs from the directory, as in some registries: the directory's name wins, as on import
	mustWrite(t, filepath.Join(bundle, "house-style", "SKILL.md"), "---\nname: vendor-house-style\ndescription: How code is written in this house\n---\nKeep it plain.\nname: not front matter\n")
	mustWrite(t, filepath.Join(bundle, taken, "SKILL.md"), "---\nname: "+taken+"\ndescription: This house's own "+taken+"\n---\nOur own text.\n")
	repo := filepath.Join(dir, "repo")
	if _, err := Prepare(ctx, root, x, mustMkdir(t, repo)); err != nil {
		t.Fatal(err)
	}
	var worktree string
	d := Drydock{Root: root, DataDir: filepath.Join(dir, "data"), Model: "haiku", CeilingUSD: 2, Wall: 5 * time.Minute, Mode: ModeChecks,
		Poll: 200 * time.Millisecond, Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}},
		SkillsDir: bundle, Prebuilt: true, SkillsFetcher: &CachingFetcher{Inner: &stubRegistry{}}, Skills: "relevant"}
	rec, _ := d.Run(ctx, x, repo, func(tree string) AcceptResult {
		worktree = tree
		if b, err := os.ReadFile(filepath.Join(tree, ".claude", "rules", "house-style.md")); err != nil || !strings.Contains(string(b), "Keep it plain.\nname: not front matter") {
			t.Errorf("the repository's skill as a rule: %v %s", err, b)
		}
		if b, err := os.ReadFile(filepath.Join(tree, ".claude", "rules", taken+".md")); err != nil || !strings.Contains(string(b), "Our own text.") || strings.Contains(string(b), "Vendor text") {
			t.Errorf("the repository's %s wins over the prebuilt one: %v %s", taken, err, b)
		}
		if b, err := os.ReadFile(filepath.Join(tree, ".claude", "drydock-skills", listed+".md")); err != nil || !strings.Contains(string(b), "Vendor text of "+listed) {
			t.Errorf("the prebuilt %s is a file to read on demand: %v %s", listed, err, b)
		}
		if _, err := os.Stat(filepath.Join(tree, ".claude", "drydock-skills", "house-style.md")); err == nil {
			t.Error("a rule has no on-demand file")
		}
		return Accept(ctx, root, x, tree)
	})
	var want []string
	for _, r := range refs {
		want = append(want, r.Name)
	}
	want = append(want, "house-style")
	sort.Strings(want)
	if rec.Outcome != Accepted || rec.Skills != "relevant" || strings.Join(rec.SkillNames, ",") != strings.Join(want, ",") || rec.SkillReads != 1 {
		t.Fatalf("%s (%s) skills %q %v reads %d", rec.Outcome, rec.Reason, rec.Skills, rec.SkillNames, rec.SkillReads)
	}
	if worktree == "" || !strings.Contains(rec.ArmDetail, "the repository's skills and the prebuilt agents' skills delivered relevant") {
		t.Fatal(rec.ArmDetail)
	}
	if !strings.Contains(strings.Join(rec.Notes, ";"), "the prebuilt skill "+taken+" was not imported: the repository has a skill of that name") {
		t.Fatalf("%v", rec.Notes)
	}
}

// stubRegistry is a labelled test double for the skill registries: every prebuilt agent's skill, at any commit.
type stubRegistry struct{ fetched int }

func (f *stubRegistry) Resolve(_ context.Context, _, ref string) (string, error) { return ref, nil }

func (f *stubRegistry) Fetch(_ context.Context, repo, _, root string) ([]skills.RemoteSkill, error) {
	f.fetched++
	regs, _ := skills.CuratedRegistries()
	refs, _ := PrebuiltSkillRefs()
	var out []skills.RemoteSkill
	for _, r := range refs {
		for _, g := range regs {
			if g.ID == r.Registry && g.Repo == repo {
				out = append(out, skills.RemoteSkill{Name: r.Name, Dir: root + r.Name, LicenseTxt: "MIT",
					SkillMD: []byte("---\nname: " + r.Name + "\ndescription: Stand-in for " + r.Name + "\nlicense: MIT\n---\nVendor text of " + r.Name + ".\n")})
			}
		}
	}
	return out, nil
}

// With Prebuilt, D imports the prebuilt agents' skills from their registries, as installing those agents does: they are
// registry skills, so with the default delivery Claude gets them listed, not loaded. A caching fetcher fetches each
// registry once for many runs.
func TestTheDrydockArmImportsThePrebuiltSkillsFromTheirRegistries(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the whole product loop")
	}
	root := repoRoot(t)
	x := task(t, "saasapi-invoice-rules")
	ctx := context.Background()
	for _, kv := range fakeScript(t, fakeagent.Script{
		Rules:   []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
		Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "internal/billing/invoice.go", Content: solution(t, x.ID, "internal/billing/invoice.go")}}, {Say: "done"}},
	}) {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	refs, err := PrebuiltSkillRefs()
	if err != nil || len(refs) < 10 {
		t.Fatalf("%v %v", refs, err)
	}
	stub := &stubRegistry{}
	cache := &CachingFetcher{Inner: stub}
	for i := 0; i < 2; i++ {
		dir := t.TempDir()
		repo := filepath.Join(dir, "repo")
		if _, err := Prepare(ctx, root, x, mustMkdir(t, repo)); err != nil {
			t.Fatal(err)
		}
		d := Drydock{Root: root, DataDir: filepath.Join(dir, "data"), Model: "haiku", CeilingUSD: 2, Wall: 5 * time.Minute, Mode: ModeChecks,
			Poll: 200 * time.Millisecond, Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}},
			Prebuilt: true, SkillsFetcher: cache}
		rec, _ := d.Run(ctx, x, repo, func(tree string) AcceptResult {
			if rules, _ := filepath.Glob(filepath.Join(tree, ".claude", "rules", "*.md")); len(rules) != 0 {
				t.Errorf("registry skills are listed, not loaded: %v", rules)
			}
			if b, err := os.ReadFile(filepath.Join(tree, ".claude", "drydock-skills", refs[0].Name+".md")); err != nil || !strings.Contains(string(b), "Vendor text of "+refs[0].Name) {
				t.Errorf("%v %s", err, b)
			}
			return Accept(ctx, root, x, tree)
		})
		if rec.Outcome != Accepted || rec.Skills != "relevant" || len(rec.SkillNames) != len(refs) || !strings.Contains(rec.ArmDetail, "the prebuilt agents' skills delivered relevant") {
			t.Fatalf("%s (%s) %q %v %s", rec.Outcome, rec.Reason, rec.Skills, rec.SkillNames, rec.ArmDetail)
		}
	}
	regs := map[string]bool{}
	for _, r := range refs {
		regs[r.Registry+"@"+r.Ref] = true
	}
	if stub.fetched != len(regs) {
		t.Fatalf("each registry is fetched once for both runs: %d fetches for %d registries", stub.fetched, len(regs))
	}
}

// Compare puts D's variants side by side and tests each against the first.
func TestCompareShowsVariantsTaskByTask(t *testing.T) {
	r := func(trial, task, outcome string, cost float64, reads int) Record {
		return Record{Trial: trial, Arm: ArmDrydock, TaskID: task, Outcome: outcome, CostUSD: cost, SkillReads: reads, WallMs: 60000,
			Tokens: Tokens{Input: 10, CacheRead: 1000, CacheWrite: 90}}
	}
	md := Compare([]Group{
		{Name: "none", Recs: []Record{r("none", "a", Accepted, 0.1, 0), r("none", "b", Accepted, 0.1, 0), {Trial: "none", Arm: ArmB1, TaskID: "a", Outcome: Rejected}}},
		{Name: "all", Recs: []Record{r("all", "a", Rejected, 0.2, 0), r("all", "b", Accepted, 0.2, 0)}},
		{Name: "relevant", Recs: []Record{r("relevant", "a", Accepted, 0.1, 2), r("relevant", "b", Accepted, 0.1, 1)}},
	})
	for _, want := range []string{
		"| none | 2 | 2 | $0.2000 | $0.1000 |", "| all | 2 | 1 | $0.4000 | $0.4000 |", "| 1100 (1100 to 1100) |", "| 1.5 (1.0 to 2.0) |",
		"| a | 1 of 1 | 0 of 1 | 1 of 1 |", "all against none: accepted more often on 0 task(s), less often on 1, as often on 1. No test is computed: decision P34 asks for at least 20 paired tasks with 3 counted runs of each arm (here 2 tasks, at least 1 runs each).",
		"relevant against none: accepted more often on 0 task(s), less often on 0, as often on 2",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
	if strings.Contains(md, "sign test p") {
		t.Errorf("a pilot-sized comparison printed a significance test:\n%s", md)
	}
	// at the size P34 asks for, the test is printed
	var none, all []Record
	for i := 0; i < ClaimTasks; i++ {
		for k := 0; k < ClaimRepeats; k++ {
			id := fmt.Sprintf("t%02d", i)
			none = append(none, r("none", id, Accepted, 0.1, 0))
			all = append(all, r("all", id, Accepted, 0.1, 0))
		}
	}
	if md := Compare([]Group{{Name: "none", Recs: none}, {Name: "all", Recs: all}}); !strings.Contains(md, "exact two-sided sign test p = 1.000") {
		t.Errorf("a claim-sized comparison lacks its test:\n%s", md)
	}
}

// The same with the sandbox (P30), against a real container engine; skipped without one.
func TestTheDrydockArmRunsTheChecksInTheSandbox(t *testing.T) {
	if testing.Short() {
		t.Skip("starts containers")
	}
	bin := sandboxtest.Engine(t)
	img := sandboxtest.Go(t, bin)
	ctx := context.Background()
	root := repoRoot(t)
	x := task(t, "saasapi-invoice-rules")
	for _, kv := range fakeScript(t, fakeagent.Script{
		Rules:   []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
		Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "internal/billing/invoice.go", Content: solution(t, x.ID, "internal/billing/invoice.go")}}, {Say: "done"}},
	}) {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	dir := t.TempDir()
	repo := mustMkdir(t, filepath.Join(dir, "repo"))
	if _, err := Prepare(ctx, root, x, repo); err != nil {
		t.Fatal(err)
	}
	d := Drydock{Root: root, DataDir: filepath.Join(dir, "data"), Model: "haiku", CeilingUSD: 2, Wall: 10 * time.Minute, Mode: ModeSandbox, SandboxImage: img,
		Poll: 500 * time.Millisecond, Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}}}
	rec, art := d.Run(ctx, x, repo, func(tree string) AcceptResult { return Accept(ctx, root, x, tree) })
	if rec.Outcome != Accepted || rec.CoreTaskID == "" {
		t.Fatalf("outcome %s: %s", rec.Outcome, rec.Reason)
	}
	// the project's checks ran in the task's container: their evidence names the engine command
	if !strings.Contains(string(art.Evidence), `exec`) || !strings.Contains(string(art.Evidence), "go test") {
		t.Fatalf("the checks' evidence does not show them running in the container:\n%s", art.Evidence)
	}
	if n := sandboxtest.Containers(t, bin, "drydock.task="+rec.CoreTaskID); n != 0 {
		t.Fatalf("%d of the task's containers are left", n)
	}
}

func mustMkdir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRecordsAreWrittenAndReadBack(t *testing.T) {
	out := t.TempDir()
	r := Record{Schema: 1, Trial: "unit", Arm: ArmB1, TaskID: "t1", Repeat: 2, StartedAt: time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC), Outcome: Accepted, CostUSD: 0.5}
	dir, err := r.Write(out, map[string][]byte{"diff.patch": []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "2026-10-10_unit_B1_t1_2" {
		t.Fatalf("dir %s", dir)
	}
	recs, err := LoadRecords(out)
	if err != nil || len(recs) != 1 || recs[0].CostUSD != 0.5 || recs[0].Repeat != 2 {
		t.Fatalf("%+v %v", recs, err)
	}
}

func TestTheReportShowsEveryRunWithMediansAndRangesOnly(t *testing.T) {
	recs := []Record{
		{Trial: "p", Commit: "abc", CLI: "2.1.296 (Claude Code)", Model: "haiku", ModelsUsed: []string{"claude-haiku-5-5"}, Host: "linux/amd64", Arm: ArmDrydock, TaskID: "a", Repeat: 1, Outcome: Accepted, CostUSD: 0.30, WallMs: 300000, Sessions: 2, Reviews: []Review{{Verdict: "SHIP"}}},
		{Trial: "p", Arm: ArmDrydock, TaskID: "a", Repeat: 2, Outcome: Rejected, CostUSD: 0.10, WallMs: 100000, Sessions: 2, EvalAccept: &AcceptResult{Commands: []CommandResult{{Command: "go test -count=1 ./...", ExitCode: 1}}}},
		{Trial: "p", Arm: ArmB1, TaskID: "a", Repeat: 1, Outcome: Accepted, CostUSD: 0.20, WallMs: 60000, Sessions: 1, CallsWithoutCost: 1},
		{Trial: "p", Arm: ArmB1, TaskID: "a", Repeat: 2, Outcome: Accepted, CostUSD: 0.05, WallMs: 50000, Sessions: 1, SawHiddenFiles: true},
	}
	md := Report(recs)
	for _, want := range []string{
		"| D | 2 | 1 | $0.4000 | $0.4000 | $0.2000 ($0.1000 to $0.3000) | 3m20s (1m40s to 5m0s) |",
		"| B1 | 1 | 1 | $0.2000 (at least; 1 session(s) reported no cost) |",
		"$0.2000 (n=1)",
		"hidden check failed: go test -count=1 ./...",
		"accepted (not counted: read the hidden checks)",
		"$0.2000 + 1 unknown",
		"no averages",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("the report lacks %q:\n%s", want, md)
		}
	}
	if regexp.MustCompile(`(?i)\b(mean|average|significant)\b`).MatchString(md) {
		t.Errorf("the report must not print a mean or a significance claim:\n%s", md)
	}
	if !strings.Contains(md, "This is a pilot") || !strings.Contains(md, "| a | unknown | 1 of 2 | 1 of 1 |") {
		t.Errorf("the task-by-task comparison is missing or wrong:\n%s", md)
	}
	none := Report([]Record{{Trial: "p", Arm: ArmB1, TaskID: "a", Outcome: Failed, Reason: "the wall-clock cap of 30m0s ended it"}})
	if !strings.Contains(none, "n/a (n=0)") {
		t.Errorf("no accepted task must read n/a (n=0):\n%s", none)
	}
}

func TestPairComparesTheArmsTaskByTask(t *testing.T) {
	var recs []Record
	add := func(task, class, arm string, accepted ...bool) {
		for i, ok := range accepted {
			out := Rejected
			if ok {
				out = Accepted
			}
			recs = append(recs, Record{Trial: "t", Arm: arm, TaskID: task, TaskClass: class, Repeat: i + 1, Outcome: out, CostUSD: float64(i + 1), WallMs: 1000})
		}
	}
	// 20 tasks, 3 runs each: D better on 8, B1 better on 1, the same on 11
	for i := 0; i < 20; i++ {
		task := fmt.Sprintf("t%02d", i)
		switch {
		case i < 8:
			add(task, "security review", ArmDrydock, true, true, true)
			add(task, "security review", ArmB1, true, false, false)
		case i == 8:
			add(task, "iteration", ArmDrydock, false, false, false)
			add(task, "iteration", ArmB1, true, false, false)
		default:
			add(task, "iteration", ArmDrydock, true, true, false)
			add(task, "iteration", ArmB1, false, true, true)
		}
	}
	p := Pair(recs)
	if len(p.Rows) != 20 || p.DMore != 8 || p.B1More != 1 || p.Same != 11 || p.MinRepeats != 3 || !p.Claim {
		t.Fatalf("pair = %+v", p)
	}
	// exact sign test, 8 against 1: 2 * (C(9,0) + C(9,1)) / 2^9 = 20/512
	if math.Abs(p.P-20.0/512) > 1e-12 {
		t.Fatalf("p = %v", p.P)
	}
	if p.Rows[0].DMedianCost != 2 || p.Rows[0].Class != "security review" {
		t.Fatalf("row = %+v", p.Rows[0])
	}
	md := Report(recs)
	for _, want := range []string{"p = 0.039", "| security review | 24 of 24 | 8 of 24 |", "on 8 task(s), less often on 1 and as often on 11"} {
		if !strings.Contains(md, want) {
			t.Errorf("the report lacks %q", want)
		}
	}
	// a run that read the hidden checks does not count, which leaves a task with 2 runs of B1: no longer a claim
	recs[3].SawHiddenFiles = true
	if p := Pair(recs); p.Claim || p.MinRepeats != 2 || p.P != -1 {
		t.Fatalf("with one run not counted: %+v", p)
	}
	for _, c := range []struct {
		a, b int
		p    float64
	}{{0, 0, 1}, {1, 1, 1}, {5, 0, 2.0 / 32}, {0, 10, 2.0 / 1024}, {3, 2, 1}} {
		if got := signTest(c.a, c.b); math.Abs(got-c.p) > 1e-12 {
			t.Errorf("signTest(%d, %d) = %v, want %v", c.a, c.b, got, c.p)
		}
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

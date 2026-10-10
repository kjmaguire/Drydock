package contract_test

import (
	"os"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/contract"
)

// The samples in testdata are real `go test -json` output (Go 1.27) of a scratch package with a passing, a failing and a
// skipping test, notes, a warning, subtests and a parent that skips before its subtests; a package that does not build; and a
// -run pattern that matches nothing. The first sample starts with a toolchain message that is not JSON.
func parse(t *testing.T, name string) *contract.Run {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	run, err := contract.ParseGoTestJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestParseGoTestJSONReadsEveryOutcome(t *testing.T) {
	run := parse(t, "sample.jsonl")
	if run.Package != contract.StatusFail || run.BuildFailed {
		t.Fatalf("package %q build failed %v", run.Package, run.BuildFailed)
	}
	if len(run.PackageOutput) != 1 || !strings.Contains(run.PackageOutput[0], "go: downloading") {
		t.Fatalf("the non-JSON line is kept as package output, framing is not: %q", run.PackageOutput)
	}
	want := map[string]string{
		"TestPasses": "pass", "TestFails": "fail", "TestSkips": "skip", "TestWarns": "pass", "TestSubtests": "fail",
		"TestSubtests/goose": "pass", "TestSubtests/cline": "fail", "TestSubtests/codex-acp": "skip", "TestParentSkips": "skip",
	}
	for name, status := range want {
		tr, ok := run.Lookup(name)
		if !ok || tr.Status != status {
			t.Errorf("%s: %+v, want %s", name, tr, status)
		}
	}
	fails, _ := run.Lookup("TestFails")
	if got := contract.Excerpt(fails.Output, 20, 2000); got != "first step ok\nthe read-only session created out.txt" {
		t.Errorf("excerpt %q", got)
	}
	for _, l := range fails.Output {
		if strings.Contains(l, "=== RUN") || strings.Contains(l, "--- FAIL") {
			t.Errorf("framing kept: %q", l)
		}
	}
	skips, _ := run.Lookup("TestSkips")
	if r := contract.Reason(skips.Output); r != "real claude not usable here: status=not_installed" {
		t.Errorf("reason %q", r)
	}
	passes, _ := run.Lookup("TestPasses")
	if n := contract.Tagged(passes.Output, contract.NotePrefix); len(n) != 1 || n[0] != "answered by claude-haiku-5-5" {
		t.Errorf("notes %q", n)
	}
	warns, _ := run.Lookup("TestWarns")
	if w := contract.Tagged(warns.Output, contract.WarningPrefix); len(w) != 1 || w[0] != "CLAUDE.md is now in context natively" {
		t.Errorf("warnings %q", w)
	}
}

func TestLookupFallsBackOnlyToAParentThatStopped(t *testing.T) {
	run := parse(t, "sample.jsonl")
	// the parent skipped before running any subtest: its reason explains the subtest
	tr, ok := run.Lookup("TestParentSkips/stdio-shim")
	if !ok || tr.Status != contract.StatusSkip || contract.Reason(tr.Output) != "claude is not installed" {
		t.Fatalf("%+v %v", tr, ok)
	}
	// TestWarns passed: a subtest it never ran (renamed, say) is missing, not passed
	if tr, ok := run.Lookup("TestWarns/renamed"); ok {
		t.Fatalf("a passing parent must not stand in for a subtest that did not run: %+v", tr)
	}
}

func TestOutcomesOfABuildFailureAndOfATestThatDoesNotExist(t *testing.T) {
	broken := parse(t, "broken.jsonl")
	if !broken.BuildFailed || broken.Package != contract.StatusFail {
		t.Fatalf("%+v", broken)
	}
	o := contract.OutcomeOf(broken, contract.TestRef{Package: "./broken", Test: "TestNeverBuilds"}, nil)
	if o.Status != contract.StatusMissing || !strings.Contains(o.Failure, "cannot use") {
		t.Fatalf("a build failure carries the compiler's message: %+v", o)
	}
	none := parse(t, "none.jsonl")
	o = contract.OutcomeOf(none, contract.TestRef{Package: "./sample", Test: "TestDoesNotExist"}, nil)
	if o.Status != contract.StatusMissing || !strings.Contains(o.Reason, "no test by that name") {
		t.Fatalf("%+v", o)
	}
}

func TestOutcomeCarriesSpendNotesAndSkipReasons(t *testing.T) {
	run := parse(t, "sample.jsonl")
	spend := contract.SpendByTest([]contract.Spend{{Test: "TestPasses", USD: 0.0063}, {Test: "TestSubtests/goose", USD: 0.01}})
	o := contract.OutcomeOf(run, contract.TestRef{Package: "./sample", Test: "TestPasses", EstUSD: 0.02}, spend)
	if o.Status != contract.StatusPass || o.SpendUSD == nil || *o.SpendUSD != 0.0063 || len(o.Notes) != 1 || !o.Ran {
		t.Fatalf("%+v", o)
	}
	o = contract.OutcomeOf(run, contract.TestRef{Package: "./sample", Test: "TestSubtests", Subtest: "codex-acp"}, spend)
	if o.Status != contract.StatusSkip || o.Reason != "codex-acp is not installed here" || o.Ran || o.SpendUSD != nil {
		t.Fatalf("%+v", o)
	}
	if spend["TestSubtests"] != 0.01 {
		t.Fatalf("a subtest's spend counts for its parent: %v", spend)
	}
}

// A contract fails when any of its tests failed or did not run, passes when one passed (saying which others skipped), and is
// skipped only when all were.
func TestBuildRowCombinesTheOutcomes(t *testing.T) {
	c := contract.Contract{ID: "CLAUDE-X-01", CLI: "claude", Assumption: "a", Tests: []contract.TestRef{
		{Package: "./a", Test: "TestA", EstUSD: 0.02}, {Package: "./b", Test: "TestB", EstUSD: 0.03}}}
	spent := 0.01
	pass := contract.Outcome{Status: contract.StatusPass, Elapsed: 2, Ran: true, SpendUSD: &spent, Notes: []string{"n1"}}
	unreported := contract.Outcome{Status: contract.StatusPass, Elapsed: 3, Ran: true}
	skip := contract.NotRun("budget: $1.50 spent")
	fail := contract.Outcome{Status: contract.StatusFail, Ran: true, Failure: "boom", Warnings: []string{"w"}}

	row := contract.BuildRow(c, "2.1.296", []contract.Outcome{pass, skip})
	if row.Status != contract.StatusPass || !strings.Contains(row.Reason, "TestB: budget") || row.SpendUSD == nil || *row.SpendUSD != 0.01 || row.EstimatedUSD != 0 {
		t.Fatalf("%+v", row)
	}
	row = contract.BuildRow(c, "2.1.296", []contract.Outcome{pass, unreported})
	if row.Status != contract.StatusPass || row.EstimatedUSD != 0.03 || row.Duration != 5 {
		t.Fatalf("a paid test that ran without reporting is counted at its estimate: %+v", row)
	}
	row = contract.BuildRow(c, "2.1.296", []contract.Outcome{pass, fail})
	if row.Status != contract.StatusFail || !strings.Contains(row.Failure, "TestB:\nboom") || len(row.Warnings) != 1 || len(row.Notes) != 1 {
		t.Fatalf("%+v", row)
	}
	row = contract.BuildRow(c, "2.1.296", []contract.Outcome{skip, skip})
	if row.Status != contract.StatusSkip || !strings.Contains(row.Reason, "TestA: budget") {
		t.Fatalf("%+v", row)
	}
	missing := contract.Outcome{Status: contract.StatusMissing, Ran: true, Reason: "TestA did not run: no test by that name in ./a"}
	if row = contract.BuildRow(c, "", []contract.Outcome{missing, pass}); row.Status != contract.StatusFail {
		t.Fatalf("a test that did not run fails the contract: %+v", row)
	}
}

// The plan runs each test once even when several contracts refer to it, free checks first, and narrows a test to the
// subtests the contracts name.
func TestPlanGroupsTheTests(t *testing.T) {
	cs := []contract.Contract{
		{ID: "A-01", Tests: []contract.TestRef{{Package: "./p", Test: "TestPaid", EstUSD: 0.05, Env: []string{"X=1"}}}},
		{ID: "B-01", Tests: []contract.TestRef{{Package: "./p", Test: "TestPaid", EstUSD: 0.02}, {Package: "./p", Test: "TestFree"}}},
		{ID: "C-01", Tests: []contract.TestRef{{Package: "./q", Test: "TestACP", Subtest: "goose"}}},
		{ID: "C-01", Tests: []contract.TestRef{{Package: "./q", Test: "TestACP", Subtest: "codex-acp"}}},
		{ID: "D-01", Tests: []contract.TestRef{{Package: "./q", Test: "TestPrompt", Subtest: "goose", EstUSD: 0.05}, {Package: "./q", Test: "TestPrompt", Subtest: "cline", EstUSD: 0.05}}},
	}
	gs := contract.Plan(cs)
	var got []string
	for _, g := range gs {
		got = append(got, g.Package+" "+g.Pattern())
	}
	want := []string{"./p ^TestFree$", "./q ^TestACP$/^(codex-acp|goose)$", "./p ^TestPaid$", "./q ^TestPrompt$/^(cline|goose)$"}
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Fatalf("plan\n got %q\nwant %q", got, want)
	}
	if gs[2].EstUSD != 0.05 || strings.Join(gs[2].Env, ",") != "X=1" || gs[3].EstUSD != 0.10 {
		t.Fatalf("estimates and env: %+v %+v", gs[2], gs[3])
	}
}

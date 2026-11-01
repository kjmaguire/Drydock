package pipeline_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The `scan` check leaves out the vulnerable packages the task's base already had (decision P133): a real git repository, the
// base read from it at the task's merge base, and the LABELLED stand-in osv-scanner following the go.mod of the folder it scans.

const (
	modWithoutText = "module gocalc\n\ngo 1.21\n"
	modWithText    = "module gocalc\n\ngo 1.21\n\nrequire golang.org/x/text v0.3.7\n"
)

func scanEnv(t *testing.T, s fakeagent.Script, baseMod string) *env {
	t.Helper()
	ad, cfg := fakeEnv(t, s)
	e := setup(t, cfg, ad)
	if baseMod != modWithoutText { // the fixture's go.mod already is that
		commitFile(t, e, "go.mod", baseMod)
	}
	if err := checks.SaveSettings(context.Background(), e.db, "p1", checks.Settings{Scan: true}, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	rules := filepath.Join(t.TempDir(), "rules")
	if err := os.MkdirAll(rules, 0o755); err != nil {
		t.Fatal(err)
	}
	e.deps.ScanTools = func(context.Context, string) scan.CheckOptions {
		return scan.CheckOptions{Paths: scan.Paths{Opengrep: standinTool("opengrep", "--empty"), OSV: standinTool("osv-scanner", "--follow-tree")}, BuiltinRules: rules}
	}
	return e
}

// The base's go.mod names the vulnerable package; the worker changes another line of it. The package was there before the task,
// so the check passes the first time and says what it left out.
func TestAVulnerablePackageTheBaseHadIsNotTheWorkersToFix(t *testing.T) {
	e := scanEnv(t, fakeagent.Script{Default: []fakeagent.Step{
		{Write: &fakeagent.WriteSpec{Path: "go.mod", Content: modWithText + "\nrequire other v1.0.0\n"}},
		{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "done"}}}, modWithText)
	e.run(t, 3, workflow.StatusCompleted)
	if got := evidenceOf(t, e, "scan"); len(got) != 1 || got[0] == nil || !*got[0] {
		t.Fatalf("the scan must pass the first time: %v", got)
	}
	if p := promptLog(); len(p) != 1 {
		t.Fatalf("the worker was sent back: %d prompts", len(p))
	}
}

// The base's go.mod does not name it and the worker adds it: that one is the worker's, goes back with the package named, and the
// next pass, whose go.mod no longer has it, passes.
func TestAVulnerablePackageTheWorkerBroughtGoesBackToTheWorker(t *testing.T) {
	e := scanEnv(t, fakeagent.Script{
		Rules: []fakeagent.Rule{{Match: "previous attempt needs changes", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "go.mod", Content: modWithoutText + "\nrequire other v1.0.0\n"}}, {Say: "dropped it"}}}},
		Turns: [][]fakeagent.Step{{{Write: &fakeagent.WriteSpec{Path: "go.mod", Content: modWithText}}, {Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "implemented"}}},
	}, modWithoutText)
	e.run(t, 3, workflow.StatusCompleted)
	p := promptLog()
	if len(p) != 2 || !strings.Contains(p[1], `"scan" check failed`) || !strings.Contains(p[1], "golang.org/x/text") {
		t.Fatalf("the second prompt must carry the package the worker brought: %d prompts, %q", len(p), p[len(p)-1])
	}
	got := evidenceOf(t, e, "scan")
	if len(got) != 2 || got[0] == nil || *got[0] || got[1] == nil || !*got[1] {
		t.Fatalf("scan evidence must be fail then pass: %v", got)
	}
}

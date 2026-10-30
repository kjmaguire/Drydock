package pipeline_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/scan/scantest"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The `scan` check inside the real Verify node (decision P131): a real git worktree, the real scan engines driving the
// LABELLED stand-in Opengrep and osv-scanner (scantest), the scripted real-process agent (labelled stand-in) as the worker.

func standinTool(name string, opts ...string) scan.Tool {
	return scan.Tool{Path: os.Args[0], Args: scantest.Args(name, opts...)}
}

// A code finding on lines the worker added fails the check and goes back to the worker with its file, line and rule; the next
// pass, whose scan finds nothing, passes and the task moves on.
func TestVerifySendsAScanFindingOnTheTasksLinesBackToTheWorker(t *testing.T) {
	long := rightImpl + "\n// one\n// two\n// three\n"
	ad, cfg := fakeEnv(t, fakeagent.Script{
		Rules: []fakeagent.Rule{{Match: "previous attempt needs changes", Steps: []fakeagent.Step{{Say: "used crypto/rand"}}}},
		Turns: [][]fakeagent.Step{{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: long}}, {Say: "implemented"}}},
	})
	e := setup(t, cfg, ad)
	if err := checks.SaveSettings(context.Background(), e.db, "p1", checks.Settings{Scan: true}, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	rules := filepath.Join(t.TempDir(), "rules")
	if err := os.MkdirAll(rules, 0o755); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	e.deps.ScanTools = func(context.Context, string) scan.CheckOptions {
		og := standinTool("opengrep")
		if calls.Add(1) > 1 {
			og = standinTool("opengrep", "--empty") // the worker's second pass removed what the rule matched
		}
		return scan.CheckOptions{Paths: scan.Paths{Opengrep: og, OSV: scan.Tool{Path: "osv-scanner-that-is-not-installed"}}, BuiltinRules: rules}
	}
	e.run(t, 3, workflow.StatusCompleted)

	p := promptLog()
	if len(p) != 2 || !strings.Contains(p[1], `"scan" check failed`) || !strings.Contains(p[1], "calc.go:5") {
		t.Fatalf("the second prompt must carry the finding in the worker's lines: %d prompts, %q", len(p), p[len(p)-1])
	}
	got := evidenceOf(t, e, "scan")
	if len(got) != 2 || got[0] == nil || *got[0] || got[1] == nil || !*got[1] {
		t.Fatalf("scan evidence must be fail then pass: %v", got)
	}
}

// With the check on and neither tool installed it is evidence without a verdict and an inconclusive event naming why; the task
// is not failed by it and it never counts as a pass.
func TestVerifyReportsTheScanAsSkippedWithoutItsTools(t *testing.T) {
	ad, cfg := fakeEnv(t, fakeagent.Script{Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "done"}}})
	e := setup(t, cfg, ad)
	if err := checks.SaveSettings(context.Background(), e.db, "p1", checks.Settings{Scan: true}, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	e.deps.ScanTools = func(context.Context, string) scan.CheckOptions {
		return scan.CheckOptions{Paths: scan.Paths{Opengrep: scan.Tool{Path: "opengrep-not-here"}, OSV: scan.Tool{Path: "osv-not-here"}}}
	}
	e.run(t, 3, workflow.StatusCompleted)
	if got := evidenceOf(t, e, "scan"); len(got) != 1 || got[0] != nil {
		t.Fatalf("a skipped scan is evidence with no verdict: %v", got)
	}
	evs, err := e.bus.Replay(context.Background(), 0, events.Filter{Types: []string{"task.check_inconclusive"}, TaskID: e.task.ID}, 10)
	if err != nil || len(evs) != 1 || !strings.Contains(string(evs[0].Data), "Opengrep is not installed") {
		t.Fatalf("events %v %v", evs, err)
	}
}

// A build without the scan (no ScanTools) skips the check a project turned on and says so.
func TestVerifySkipsTheScanInABuildWithoutIt(t *testing.T) {
	ad, cfg := fakeEnv(t, fakeagent.Script{Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "done"}}})
	e := setup(t, cfg, ad)
	if err := checks.SaveSettings(context.Background(), e.db, "p1", checks.Settings{Scan: true}, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	e.run(t, 3, workflow.StatusCompleted)
	ev, _ := e.tasks.Evidence(context.Background(), e.task.ID)
	found := false
	for _, x := range ev {
		if x.Kind == "scan" {
			found = x.Passed == nil && strings.Contains(x.Summary, "no security scan")
		}
	}
	if !found {
		t.Fatalf("evidence %+v", ev)
	}
}

// In the Security template the scan judges the whole tree: its task is the findings already there. The first scan gives the
// worker the findings by file, line and rule; the gate's scan after the fix passes.
func TestTheSecurityTemplateRunsTheSecurityScanOverTheWholeTree(t *testing.T) {
	sc := map[string]fakeagent.Script{aWork: {Default: []fakeagent.Step{say("replaced math/rand")}}, aSec: shipReviewerStep(), aReview: shipReviewer()}
	e := newTpl(t, sc, tplOpts{title: "Audit", desc: "Audit the code."})
	rules := t.TempDir()
	var calls atomic.Int32
	e.td.ScanTools = func(context.Context, string) scan.CheckOptions {
		og := standinTool("opengrep")
		if calls.Add(1) > 1 {
			og = standinTool("opengrep", "--empty")
		}
		return scan.CheckOptions{Paths: scan.Paths{Opengrep: og, OSV: scan.Tool{Path: "osv-scanner-that-is-not-installed"}}, BuiltinRules: rules}
	}
	if err := pipeline.RegisterTemplates(e.eng, e.td); err != nil {
		t.Fatal(err)
	}
	id := e.start(pipeline.WorkflowSecurity, map[string]string{pipeline.RoleSecurity: aSec}, "scan")
	e.await(id, workflow.StatusWaitingHuman)
	wp := e.prompts(aWork)
	if len(wp) != 1 || !strings.Contains(wp[0], "scan: FAILED") || !strings.Contains(wp[0], "calc.go:3") {
		t.Fatalf("the worker is told the findings of the tree: %q", wp)
	}
	ev := e.evidence()
	if len(ev["scan"]) != 2 || ev["scan"][0].Passed == nil || *ev["scan"][0].Passed || ev["scan"][1].Passed == nil || !*ev["scan"][1].Passed {
		t.Fatalf("the scan fails at the first scan and passes at the gate: %+v", ev["scan"])
	}
}

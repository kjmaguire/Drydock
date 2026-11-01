package scan_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/scan"
)

// checkTree is a task worktree in the shape the stand-ins report on: Opengrep's finds calc.go:3 and calc.go:5-7, osv-scanner's
// a vulnerable golang.org/x/text in go.mod.
func checkTree(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"calc.go": "package calc\n\nimport \"math/rand\"\n\nfunc Run(s string) {\n\texec.Command(s).Run()\n}\n\nfunc Other() {}\n\n",
		"go.mod":  "module x\n\nrequire golang.org/x/text v0.3.7\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// addedDiff is a git diff that adds the given lines (1-based) of one file and nothing else.
func addedDiff(file string, from, to int) string {
	var b strings.Builder
	b.WriteString("diff --git a/" + file + " b/" + file + "\n--- a/" + file + "\n+++ b/" + file + "\n")
	b.WriteString("@@ -0,0 +" + strconv.Itoa(from) + "," + strconv.Itoa(to-from+1) + " @@\n")
	for i := from; i <= to; i++ {
		b.WriteString("+line\n")
	}
	return b.String()
}

func checkOpts(t *testing.T, diff string) scan.CheckOptions {
	return scan.CheckOptions{Paths: scan.Paths{OSV: tool("osv-scanner"), Opengrep: tool("opengrep")}, BuiltinRules: rules(t),
		Added: func(context.Context) (string, error) { return diff, nil }}
}

func runScanCheck(t *testing.T, dir string, o scan.CheckOptions) checks.Result {
	t.Helper()
	res, err := checks.Run(context.Background(), proc.New(), scan.CheckSpec(dir, o), nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// A finding on a line the task added fails the check and is named; findings elsewhere in the tree are counted but do not.
func TestTheScanCheckFailsOnAFindingTheTaskAdded(t *testing.T) {
	dir := checkTree(t)
	res := runScanCheck(t, dir, checkOpts(t, addedDiff("calc.go", 1, 7)))
	if res.Passed || res.Inconclusive() || res.ExitCode != 1 || res.Name != "scan" {
		t.Fatalf("%+v", res)
	}
	if len(res.FailingTests) != 2 || !strings.HasPrefix(res.FailingTests[0], "calc.go:3 ") || !strings.HasPrefix(res.FailingTests[1], "calc.go:5 ") {
		t.Fatalf("failing: %v", res.FailingTests)
	}
	if !strings.Contains(res.Output, "2 security finding(s) in this change") || !strings.Contains(res.Output, "1 finding(s) elsewhere") {
		t.Fatalf("output: %s", res.Output)
	}
}

// A change that adds no line with a finding passes, though the tree has findings from before the task.
func TestTheScanCheckPassesWhenTheChangeAddsNoFinding(t *testing.T) {
	res := runScanCheck(t, checkTree(t), checkOpts(t, addedDiff("calc.go", 9, 10)))
	if !res.Passed || res.Inconclusive() {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(res.Output, "found nothing in this change") || !strings.Contains(res.Output, "3 finding(s) elsewhere") {
		t.Fatalf("output: %s", res.Output)
	}
}

// A task that changed the manifest owns its vulnerable packages; without a change (the Security template) every finding counts.
func TestTheScanCheckJudgesAChangedManifestAndTheWholeTree(t *testing.T) {
	dir := checkTree(t)
	res := runScanCheck(t, dir, checkOpts(t, addedDiff("go.mod", 3, 3)))
	if res.Passed || len(res.FailingTests) != 1 || res.FailingTests[0] != "golang.org/x/text@0.3.7" || !strings.Contains(res.Output, "upgrade to 0.3.8") {
		t.Fatalf("%+v", res)
	}
	o := checkOpts(t, "")
	o.Added = nil
	res = runScanCheck(t, dir, o)
	if res.Passed || len(res.FailingTests) != 3 || strings.Contains(res.Output, "elsewhere") {
		t.Fatalf("whole tree: %+v", res)
	}
}

// A finding a person dismissed on the Security page does not count in a task either.
func TestTheScanCheckLeavesOutDismissedFindings(t *testing.T) {
	dir := checkTree(t)
	o := checkOpts(t, addedDiff("calc.go", 1, 7))
	o.Added = nil
	first := runScanCheck(t, dir, o)
	if first.Passed {
		t.Fatal("expected findings")
	}
	// the fingerprints the check saw, from a scan of the same tree with the same engines
	rep := scan.Start(context.Background(), scan.Options{Dir: dir, Paths: o.Paths, BuiltinRules: o.BuiltinRules, NoGit: true, Engines: []scan.EngineID{scan.SAST, scan.Deps}}).Wait()
	d := map[string]scan.Dismissal{}
	for _, f := range rep.Findings {
		d[f.Fingerprint] = scan.Dismissal{Fingerprint: f.Fingerprint, Reason: scan.ReasonFalsePositive, By: "human:kyle"}
	}
	o.Dismissed = func(context.Context) map[string]scan.Dismissal { return d }
	res := runScanCheck(t, dir, o)
	if !res.Passed || !strings.Contains(res.Output, "3 finding(s) a person dismissed") {
		t.Fatalf("%+v", res)
	}
}

// Without the tools the check is skipped and says why; a tool that breaks makes it errored. Neither is a pass.
func TestTheScanCheckIsSkippedWithoutItsToolsAndErroredWhenOneBreaks(t *testing.T) {
	dir := checkTree(t)
	o := checkOpts(t, addedDiff("calc.go", 1, 7))
	o.Paths = scan.Paths{LookPath: notFound}
	res := runScanCheck(t, dir, o)
	if !res.Skipped || res.Passed || !strings.Contains(res.SkipReason, "Opengrep is not installed") || !strings.Contains(res.SkipReason, "osv-scanner is not installed") {
		t.Fatalf("%+v", res)
	}
	o.Paths = scan.Paths{Opengrep: tool("opengrep", "--fail"), OSV: scan.Tool{Path: "no-such-osv-scanner"}}
	res = runScanCheck(t, dir, o)
	if !res.Errored || res.Passed || !strings.Contains(res.Output, "Code failed") {
		t.Fatalf("%+v", res)
	}
	// one engine that ran gives the verdict; the other is named as not checked
	o.Paths = scan.Paths{Opengrep: tool("opengrep", "--empty"), OSV: scan.Tool{Path: "no-such-osv-scanner"}}
	res = runScanCheck(t, dir, o)
	if !res.Passed || !strings.Contains(res.Output, "Not checked: Dependencies skipped") {
		t.Fatalf("%+v", res)
	}
}

// A diff that cannot be read is not judged as the whole tree: the check is errored.
func TestTheScanCheckWithoutTheTasksChangeIsErrored(t *testing.T) {
	o := checkOpts(t, "")
	o.Added = func(context.Context) (string, error) { return "", errors.New("git broke") }
	res := runScanCheck(t, checkTree(t), o)
	if !res.Errored || res.Passed {
		t.Fatalf("%+v", res)
	}
}

// A scan limited to some engines runs and reports only those.
func TestAScanCanRunSomeOfItsEngines(t *testing.T) {
	rep := scan.Start(context.Background(), scan.Options{Dir: checkTree(t), Paths: scan.Paths{LookPath: notFound}, Engines: []scan.EngineID{scan.Deps}}).Wait()
	if len(rep.Engines) != 1 || rep.Engines[0].ID != scan.Deps || rep.Engines[0].Status != scan.Skipped || rep.Status != scan.StatusDone {
		t.Fatalf("%+v", rep.Engines)
	}
}

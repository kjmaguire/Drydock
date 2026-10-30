package scan_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/scan"
)

// baseTree is a worktree whose go.mod names the vulnerable golang.org/x/text at head, with the diff of a task that changed that
// file; the stand-in osv-scanner follows the go.mod of the folder it is asked to scan (--follow-tree).
func baseTree(t *testing.T, headMod string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(headMod), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func followOpts(t *testing.T, base func(context.Context, string) (string, bool, error)) scan.CheckOptions {
	o := scan.CheckOptions{Paths: scan.Paths{OSV: tool("osv-scanner", "--follow-tree"), Opengrep: tool("opengrep", "--empty")}, BuiltinRules: rules(t),
		Added: func(context.Context) (string, error) { return addedDiff("go.mod", 3, 3), nil }}
	o.BaseFile = base
	return o
}

const headMod = "module x\n\nrequire other v1.0.0\nrequire golang.org/x/text v0.3.7\n"

// A task that changes a lockfile does not own the vulnerable packages the base had in it, but does own the ones it brought.
func TestADependencyTheBaseAlreadyHadIsNotTheTasks(t *testing.T) {
	// the base had the same package at the same version: the task only added the "other" line
	had := func(context.Context, string) (string, bool, error) {
		return "module x\n\nrequire golang.org/x/text v0.3.7\n", true, nil
	}
	res := runScanCheck(t, baseTree(t, headMod), followOpts(t, had))
	if !res.Passed || res.Inconclusive() || len(res.FailingTests) != 0 {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(res.Output, "1 vulnerable package(s) in a manifest or lockfile this change touched were already vulnerable before it") {
		t.Fatalf("output: %s", res.Output)
	}

	// the base did not name it: the task brought it
	brought := func(context.Context, string) (string, bool, error) {
		return "module x\n\nrequire other v1.0.0\n", true, nil
	}
	res = runScanCheck(t, baseTree(t, headMod), followOpts(t, brought))
	if res.Passed || len(res.FailingTests) != 1 || res.FailingTests[0] != "golang.org/x/text@0.3.7" {
		t.Fatalf("%+v", res)
	}

	// the base named another version of it: a new package version is the task's, and is named with the fix
	older := func(context.Context, string) (string, bool, error) {
		return "module x\n\nrequire golang.org/x/text v0.3.6\n", true, nil
	}
	res = runScanCheck(t, baseTree(t, headMod), followOpts(t, older))
	if res.Passed || len(res.FailingTests) != 1 {
		t.Fatalf("%+v", res)
	}

	// the file did not exist at the base (the task added it): the base is not scanned and the finding is the task's
	added := func(context.Context, string) (string, bool, error) { return "", false, nil }
	res = runScanCheck(t, baseTree(t, headMod), followOpts(t, added))
	if res.Passed || len(res.FailingTests) != 1 {
		t.Fatalf("%+v", res)
	}
}

// When the base cannot be read or scanned the comparison is not made: every finding still counts, and the check says so.
func TestTheBaseNotBeingReadableLeavesEveryFindingCounted(t *testing.T) {
	broken := func(context.Context, string) (string, bool, error) { return "", false, errors.New("bad object") }
	res := runScanCheck(t, baseTree(t, headMod), followOpts(t, broken))
	if res.Passed || len(res.FailingTests) != 1 || !strings.Contains(res.Output, "Not checked: not compared with the base: the base's go.mod could not be read: bad object") {
		t.Fatalf("%+v", res)
	}
	// no base function at all (the tree-wide scan, or a build without it): the finding counts, with nothing said
	res = runScanCheck(t, baseTree(t, headMod), followOpts(t, nil))
	if res.Passed || len(res.FailingTests) != 1 || strings.Contains(res.Output, "base") {
		t.Fatalf("%+v", res)
	}
}

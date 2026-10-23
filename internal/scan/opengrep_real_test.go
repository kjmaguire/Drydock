package scan_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/scan"
)

// TestAnOptionOpengrepDoesNotKnowIsAnErrorThatNamesTheOption is the failure the owner saw on 2026-10-22 ("opengrep exited 2:
// Usage: opengrep scan [--help] [OPTION]… [TARGETS]…"): the stand-in refuses an unknown option the way Opengrep 1.31.0 does, and
// the reason is the error line, not the usage line.
func TestAnOptionOpengrepDoesNotKnowIsAnErrorThatNamesTheOption(t *testing.T) {
	rep := scan.Start(context.Background(), scan.Options{Dir: tree(t), RulesDir: rules(t),
		Paths: scan.Paths{OSV: tool("osv-scanner", "--empty"), Opengrep: tool("opengrep", "--no-progress-bar")}}).Wait()
	e := engine(rep, scan.SAST)
	if e.Status != scan.Errored || e.Reason != "opengrep exited 2: opengrep scan: unknown option --no-progress-bar" {
		t.Fatalf("%+v", e)
	}
}

// TestTheRealOpengrepRunsTheBuiltInRules runs the Opengrep Drydock ships (DRYDOCK_TEST_OPENGREP names the program; CI's app job
// sets it to the one in the Windows download) with exactly the arguments a scan gives it, over a tree whose Go file turns off
// TLS certificate checks, and expects the built-in rule to find it. It is the only test that runs the real engine.
func TestTheRealOpengrepRunsTheBuiltInRules(t *testing.T) {
	prog := os.Getenv("DRYDOCK_TEST_OPENGREP")
	if prog == "" {
		t.Skip("set DRYDOCK_TEST_OPENGREP to an Opengrep program (CI's app job sets it to the bundled one)")
	}
	builtin, err := scan.WriteBuiltinRules(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, noGit := range []bool{false, true} {
		dir := filepath.Join(t.TempDir(), "code")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "client.go"), []byte("package main\n\nimport \"crypto/tls\"\n\nvar cfg = &tls.Config{InsecureSkipVerify: true}\n"), 0o644)
		rep := scan.Start(context.Background(), scan.Options{Dir: dir, BuiltinRules: builtin, NoGit: noGit, Paths: scan.Paths{
			OSV: tool("osv-scanner", "--empty"), Opengrep: scan.Tool{Path: prog, Installed: true}}}).Wait()
		e := engine(rep, scan.SAST)
		if e.Status != scan.Done {
			t.Fatalf("noGit=%v: %+v", noGit, e)
		}
		found := false
		for _, f := range rep.Findings {
			if f.Engine == scan.SAST && strings.HasSuffix(f.Rule, "drydock.go.tls-certificate-check-off") && f.File == "client.go" && f.Line == 5 {
				found = true
			}
		}
		if !found {
			t.Fatalf("noGit=%v: the built-in rule found nothing: %+v", noGit, rep.Findings)
		}
	}
}

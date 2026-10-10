package winverify

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBundleEndToEnd builds a bundle for this system with the builder (drydock-winbundle's code), unzips it into a fresh
// folder and runs the bundled drydock-verify there, as a person would, then reads its report: no step may fail, the core
// steps must pass and every package's tests must pass (or skip with a reason). It builds every test binary and runs every
// suite (about 10 minutes, 1 GB of temporary disk), so it is opt-in:
//
//	DRYDOCK_BUNDLE_E2E=1 go test -run TestBundleEndToEnd -timeout 60m ./internal/winverify
//
// With DRYDOCK_BUNDLE_E2E_NO_GO=1 the verifier runs with no go on PATH, as on a computer without Go: the bundle's own
// copy of Go is then what the core smoke and the tests that run go use.
func TestBundleEndToEnd(t *testing.T) {
	if testing.Short() || os.Getenv("DRYDOCK_BUNDLE_E2E") != "1" {
		t.Skip("set DRYDOCK_BUNDLE_E2E=1 to build a whole bundle and run it (about 10 minutes)")
	}
	root := repoRoot(t)
	dir := t.TempDir()
	built, zipPath := filepath.Join(dir, "built"), filepath.Join(dir, "bundle.zip")
	m, err := Build(context.Background(), BuildOptions{Root: root, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Out: built, Zip: zipPath, WithGo: true, Log: testLog{t}})
	if err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(built) // the zip is what a person gets
	bundle := filepath.Join(dir, "unzipped")
	if err := unzip(zipPath, bundle); err != nil {
		t.Fatal(err)
	}
	os.Remove(zipPath)
	results := filepath.Join(dir, "results")
	os.MkdirAll(results, 0o755)
	cmd := exec.Command(filepath.Join(bundle, m.Verify), "--out", results)
	cmd.Dir = bundle
	cmd.Env = os.Environ()
	if os.Getenv("DRYDOCK_BUNDLE_E2E_NO_GO") == "1" {
		cmd.Env = setEnv(cmd.Env, "PATH", pathWithout(getEnv(cmd.Env, "PATH"), "go"+exeSuffix(runtime.GOOS)))
		cmd.Env = unsetEnv(cmd.Env, "GOROOT")
	}
	out, err := cmd.CombinedOutput()
	t.Logf("drydock-verify printed:\n%s", out)
	reports, _ := filepath.Glob(filepath.Join(results, "drydock-verify-*", ReportJSON))
	if len(reports) != 1 {
		t.Fatalf("one report expected, found %v (exit: %v)", reports, err)
	}
	b, _ := os.ReadFile(reports[0])
	var rep Report
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	if err != nil || rep.Summary.Failed != 0 {
		t.Errorf("exit %v, %d step(s) failed", err, rep.Summary.Failed)
	}
	for _, s := range rep.Steps {
		switch {
		case s.Status == Fail:
			t.Errorf("%s failed: %s\n%s", s.ID, s.Reason, s.Excerpt)
		case strings.HasPrefix(s.ID, "core-") || s.ID == "facts":
			if s.Status != Pass {
				t.Errorf("%s must pass here: %s %s", s.ID, s.Status, s.Reason)
			}
		case s.ID == "real-claude" || s.ID == "ui":
			if s.Status != Skip {
				t.Errorf("%s runs only when asked: %s", s.ID, s.Status)
			}
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(results, "drydock-verify-*.zip")); len(matches) != 1 {
		t.Errorf("the results zip: %v", matches)
	}
}

// testLog sends the builder's progress to the test log.
type testLog struct{ t *testing.T }

func (l testLog) Write(p []byte) (int, error) {
	l.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// pathWithout drops every PATH entry that holds the named program.
func pathWithout(path, program string) string {
	var keep []string
	for _, d := range filepath.SplitList(path) {
		if _, err := os.Stat(filepath.Join(d, program)); err == nil {
			continue
		}
		keep = append(keep, d)
	}
	return strings.Join(keep, string(os.PathListSeparator))
}

func unzip(zipPath, dst string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		target := filepath.Join(dst, filepath.FromSlash(f.Name))
		if !strings.HasPrefix(target, filepath.Clean(dst)+string(os.PathSeparator)) {
			continue // no entry may leave the folder
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode().Perm()|0o200)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(w, rc)
		rc.Close()
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

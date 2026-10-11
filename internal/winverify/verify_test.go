package winverify

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func minimalBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	m := &Manifest{Commit: "abc123", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(), Built: time.Now().UTC(),
		Verify: "drydock-verify" + exeSuffix(runtime.GOOS), E2E: "drydock-e2e" + exeSuffix(runtime.GOOS), Fixture: "tests/testdata/fixtures/gocalc"}
	if err := WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A whole run with only the facts step: the report files, the zip next to the results, the exit code and what is printed.
func TestVerifyWritesTheReportsAndTheZip(t *testing.T) {
	bundle, out := minimalBundle(t), t.TempDir()
	var stdout bytes.Buffer
	code := Verify(context.Background(), Options{Bundle: bundle, Out: out, Steps: []string{"facts", "real-claude", "ui"}, Args: []string{"--steps", "facts,real-claude,ui"}}, &stdout)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stdout.String())
	}
	zips, _ := filepath.Glob(filepath.Join(out, "drydock-verify-*.zip"))
	if len(zips) != 1 {
		t.Fatalf("zips %v\n%s", zips, stdout.String())
	}
	results := strings.TrimSuffix(zips[0], ".zip")
	var rep Report
	b, err := os.ReadFile(filepath.Join(results, ReportJSON))
	if err != nil || json.Unmarshal(b, &rep) != nil {
		t.Fatalf("JSON report: %v", err)
	}
	if len(rep.Steps) != 3 || rep.Steps[0].Status != Pass || rep.Steps[0].Facts == nil || rep.Bundle.Commit != "abc123" || rep.Summary.Skipped != 2 {
		t.Fatalf("%+v", rep)
	}
	if rep.Steps[1].Status != Skip || !strings.Contains(rep.Steps[1].Reason, "--with-claude") || !strings.Contains(rep.Steps[2].Reason, "--with-ui") {
		t.Fatalf("opt-in steps skip with how to turn them on: %+v %+v", rep.Steps[1], rep.Steps[2])
	}
	zr, err := zip.OpenReader(zips[0])
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{ReportMD, ReportJSON, "logs/facts.log", "logs/real-claude.log", "logs/ui.log"} {
		if !names[want] {
			t.Errorf("zip lacks %s: %v", want, names)
		}
	}
	for _, want := range []string{"commit abc123", "[ 1/3] facts", "Result: 1 passed, 0 failed, 2 skipped", "Send this file back: " + zips[0]} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, stdout.String())
		}
	}
}

func TestVerifyListsStepsAndRefusesWhatItCannotRun(t *testing.T) {
	bundle := minimalBundle(t)
	var out bytes.Buffer
	if code := Verify(context.Background(), Options{Bundle: bundle, List: true}, &out); code != 0 {
		t.Fatalf("list exit %d", code)
	}
	for _, id := range []string{"facts", "core-smoke", "core-terminal", "core-shutdown", "real-claude", "ui"} {
		if !strings.Contains(out.String(), id+"\n    ") {
			t.Errorf("list lacks %s:\n%s", id, out.String())
		}
	}
	out.Reset()
	if code := Verify(context.Background(), Options{Bundle: t.TempDir()}, &out); code != 2 || !strings.Contains(out.String(), "bundle.json") {
		t.Fatalf("a folder without a bundle: %d %s", code, out.String())
	}
	out.Reset()
	if code := Verify(context.Background(), Options{Bundle: bundle, Steps: []string{"nonesuch"}}, &out); code != 2 || !strings.Contains(out.String(), "nonesuch") {
		t.Fatalf("an unknown step: %d %s", code, out.String())
	}
}

func TestChildEnvironment(t *testing.T) {
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	t.Setenv("DRYDOCK_REAL_CLAUDE", "1")
	t.Setenv("GOROOT", "/stale")
	e := &Env{}
	env := e.childEnv(map[string]string{"DRYDOCK_NO_REAL_AGENTS": "1"}, "DRYDOCK_REAL_CLAUDE")
	if getEnv(env, "DRYDOCK_FAKE_AGENT") != "" || getEnv(env, "DRYDOCK_REAL_CLAUDE") != "" || getEnv(env, "DRYDOCK_NO_REAL_AGENTS") != "1" {
		t.Fatal("the stand-in switch and unset names are removed; set names are added")
	}
	if getEnv(env, "GOROOT") != "/stale" {
		t.Fatal("without a bundled Go the environment's Go settings are left alone")
	}
	e = &Env{goDir: filepath.Join("bundle", "go", "bin"), runTemp: t.TempDir()}
	env = e.childEnv(nil)
	if !strings.HasPrefix(getEnv(env, "PATH"), e.goDir+string(os.PathListSeparator)) || getEnv(env, "GOROOT") != "" ||
		!strings.HasPrefix(getEnv(env, "GOCACHE"), e.runTemp) || getEnv(env, "GOTOOLCHAIN") != "local" {
		t.Fatalf("the bundled Go goes first on PATH with its caches in the run's folder: PATH=%s GOCACHE=%s", getEnv(env, "PATH"), getEnv(env, "GOCACHE"))
	}
	paths := 0
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); envKeyEqual(k, "PATH") {
			paths++
		}
	}
	if paths != 1 {
		t.Fatalf("PATH is replaced, not added again: %d entries", paths)
	}
	// Windows spells it Path; the value is replaced under the spelling it already has
	got := setEnv([]string{"Path=a", "X=1"}, "Path", "b")
	if len(got) != 2 || got[0] != "Path=b" {
		t.Fatalf("%v", got)
	}
}

func TestSetupGoPrefersPATHThenTheBundle(t *testing.T) {
	bundle := t.TempDir()
	t.Setenv("PATH", t.TempDir()) // no go here
	e := &Env{Bundle: bundle}
	e.setupGo()
	if e.HasGo() || !strings.Contains(e.goNote, "skipped") {
		t.Fatalf("no Go anywhere: %+v", e)
	}
	bin := filepath.Join(bundle, "go", "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "go"+exeSuffix(runtime.GOOS)), []byte("x"), 0o755)
	os.WriteFile(filepath.Join(bundle, "go", "VERSION"), []byte("go1.27.2\ntime 2026-10-01\n"), 0o644)
	e = &Env{Bundle: bundle}
	e.setupGo()
	if !e.HasGo() || e.goDir != bin || !strings.Contains(e.goNote, "go1.27.2") {
		t.Fatalf("the bundled Go: %+v", e)
	}
}

// The package tests' git settings reach every git call, Drydock's own included, through a config file that still includes
// the user's own global file; the machine's files are not changed.
func TestTestGitConfigIsAFileThatKeepsTheUsersOwnSettings(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	own := "[alias]\n\tdrydockmark = yes\n[core]\n\tautocrlf = true\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &Env{runTemp: t.TempDir()}
	set := e.testGitConfig(map[string]string{"X": "1"})
	if set["X"] != "1" || set["GIT_CONFIG_GLOBAL"] == "" || set["GIT_CONFIG_COUNT"] != "" {
		t.Fatalf("set %v", set)
	}
	get := func(key string) string {
		cmd := exec.Command("git", "config", "--get", key)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+set["GIT_CONFIG_GLOBAL"], "GIT_CONFIG_NOSYSTEM=1")
		out, _ := cmd.Output()
		return strings.TrimSpace(string(out))
	}
	if get("core.autocrlf") != "false" || get("user.name") != "Drydock Verify" || get("alias.drydockmark") != "yes" {
		t.Fatalf("autocrlf %q name %q own %q", get("core.autocrlf"), get("user.name"), get("alias.drydockmark"))
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".gitconfig")); string(b) != own {
		t.Fatal("the user's own file changed")
	}
}

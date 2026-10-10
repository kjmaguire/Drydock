package winverify

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func sampleReport() *Report {
	yes := true
	start := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	return &Report{
		Tool: "drydock-verify", Started: start, Finished: start.Add(15 * time.Minute), Host: "windows/amd64",
		Bundle:  &Manifest{Commit: "abc123def456", Built: start.Add(-time.Hour), GOOS: "windows", GOARCH: "amd64", GoVersion: "go1.27.2"},
		Options: []string{"--with-claude"},
		Summary: Summary{Passed: 1, Failed: 1, Skipped: 1},
		Steps: []StepResult{
			{ID: "facts", Proves: "Records the machine.", Status: Pass, DurationMs: 800, Log: "logs/facts.log",
				Facts: &Facts{OS: "Windows 11 Pro", OSVersion: "24H2", Build: "26100.2033", Arch: "amd64", CPU: "AMD Ryzen 7", Cores: 16, RAMBytes: 32 << 30,
					Elevated: &yes, Tools: []Tool{{Name: "git", Found: true, Path: `C:\Program Files\Git\cmd\git.exe`, Version: "git version 2.51.0.windows.1"},
						{Name: "claude", Found: true, Path: `~\AppData\Roaming\npm\claude.cmd`, Version: "2.1.296 (Claude Code)", Note: "a batch-file launcher"},
						{Name: "docker"}}}},
			{ID: "tests:internal/proc", Proves: "Process trees | Job Objects.", Status: Fail, Reason: "1 test(s) failed: TestKillTreeReapsGrandchildren",
				DurationMs: 3200, Log: "logs/tests_internal_proc.log", Excerpt: "FAIL TestKillTreeReapsGrandchildren\n```nested fence```",
				Counts: &TestCounts{Passed: 5, Failed: 1}, Tests: []TestResult{{Name: "TestKillTreeReapsGrandchildren", Status: Fail, Reason: "ORPHAN"}}},
			{ID: "ui", Proves: "The UI suites.", Status: Skip, Reason: "not requested", Log: "logs/ui.log"},
		},
	}
}

func TestMarkdownReportPutsTheProblemsFirst(t *testing.T) {
	md := sampleReport().Markdown()
	for _, want := range []string{
		"# Drydock verification report",
		"**Machine:** Windows 11 Pro 24H2 (build 26100.2033), amd64",
		"AMD Ryzen 7, 16 logical CPUs, 32.0 GB RAM",
		"commit `abc123def456`",
		"**Options:** `--with-claude`",
		"**Result:** 1 passed, 1 failed, 1 skipped",
		"| tests:internal/proc | FAIL | 3.2s | 5 / 1 / 0 | Process trees \\| Job Objects. |",
		"| ui | SKIP | 0s |  | The UI suites. |",
		"- Running as administrator: yes",
		"- claude: 2.1.296 (Claude Code) (~\\AppData\\Roaming\\npm\\claude.cmd), a batch-file launcher",
		"- docker: not on PATH",
		"**Why it failed:** 1 test(s) failed: TestKillTreeReapsGrandchildren",
		"**Reason:** not requested",
		"Log: `logs/tests_internal_proc.log`",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if strings.Index(md, "## Steps that did not pass") > strings.Index(md, "## Steps that passed") {
		t.Error("problems must come before the passed steps")
	}
	if strings.Count(md, "```") != 2 { // one excerpt in one fence; the excerpt's own fence was neutralised
		t.Errorf("fences: %d\n%s", strings.Count(md, "```"), md)
	}
}

func TestJSONReportRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), ReportJSON)
	if err := sampleReport().WriteJSON(path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var back Report
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Steps) != 3 || back.Steps[1].Tests[0].Name != "TestKillTreeReapsGrandchildren" || back.Steps[0].Facts.CPU != "AMD Ryzen 7" ||
		back.Summary.Failed != 1 || back.Bundle.Commit != "abc123def456" {
		t.Fatalf("%+v", back)
	}
	for _, key := range []string{`"proves"`, `"status": "fail"`, `"durationMs": 3200`, `"counts"`, `"logicalCpus": 16`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("JSON lacks %s", key)
		}
	}
}

func TestZipDirKeepsPathsContentAndExecutableBits(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "logs", "deep"), 0o755)
	os.WriteFile(filepath.Join(dir, "report.md"), []byte("# hi"), 0o644)
	os.WriteFile(filepath.Join(dir, "logs", "deep", "a.log"), []byte("line"), 0o644)
	os.WriteFile(filepath.Join(dir, "tool"), []byte("#!/bin/sh\n"), 0o755)
	inside := filepath.Join(dir, "self.zip") // a zip written inside the folder it packs must not contain itself
	if err := ZipDir(dir, inside); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(inside)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	got := map[string]*zip.File{}
	for _, f := range zr.File {
		got[f.Name] = f
	}
	if len(got) != 3 || got["logs/deep/a.log"] == nil || got["report.md"] == nil || got["self.zip"] != nil {
		t.Fatalf("entries %v", got)
	}
	rc, _ := got["logs/deep/a.log"].Open()
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "line" {
		t.Fatalf("content %q", b)
	}
	if runtime.GOOS != "windows" && got["tool"].Mode()&0o111 == 0 {
		t.Fatalf("the executable bit was lost: %v", got["tool"].Mode())
	}
}

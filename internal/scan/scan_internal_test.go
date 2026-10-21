package scan

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/scan/scantest"
)

func TestMain(m *testing.M) {
	if scantest.IsStandIn(os.Args) { // the LABELLED stand-in osv-scanner and opengrep (scantest)
		os.Exit(scantest.Main(os.Args))
	}
	os.Exit(m.Run())
}

// The scan only reads files (decision P61): osv-scanner's call analysis, the part that runs the go toolchain (or cargo) over
// the repository, is off for every language, and the tool's leading arguments (the stand-ins') still come first.
func TestOSVScannerRunsWithoutCallAnalysis(t *testing.T) {
	got := strings.Join(osvArgs(Tool{Path: "osv-scanner", Args: []string{"lead"}}), " ")
	if got != "lead scan source --format json --no-call-analysis=all -r ." {
		t.Fatal(got)
	}
}

// A folder scan's Opengrep walks the folder itself (--no-git-ignore), so git never runs in a folder that came from elsewhere;
// a project's scan keeps git's view of its files.
func TestOpengrepRunsNoGitInAFolderScan(t *testing.T) {
	lead := Tool{Path: "opengrep", Args: []string{"lead"}}
	if got := strings.Join(opengrepArgs(lead, []string{"/rules"}, "/tmp/o.sarif", true), " "); got != "lead scan -f /rules --sarif-output=/tmp/o.sarif --no-progress-bar -q --no-git-ignore ." {
		t.Fatal(got)
	}
	if got := strings.Join(opengrepArgs(lead, []string{"/rules"}, "/tmp/o.sarif", false), " "); got != "lead scan -f /rules --sarif-output=/tmp/o.sarif --no-progress-bar -q ." {
		t.Fatal(got)
	}
}

func TestRelativeURIHandlesFileURIsAndPlainPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo")
	cases := map[string]string{
		"calc.go":                           "calc.go",
		"./pkg/a.go":                        "pkg/a.go",
		"pkg%20x/a.go":                      "pkg x/a.go",
		filepath.ToSlash(dir) + "/sub/b.go": "sub/b.go",
	}
	if runtime.GOOS == "windows" {
		cases["file:///"+filepath.ToSlash(dir)+"/c.go"] = "c.go"
	} else {
		cases["file://"+dir+"/c.go"] = "c.go"
	}
	for in, want := range cases {
		if got := relativeURI(in, dir); got != want {
			t.Errorf("relativeURI(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSARIFSeverityPrefersTheScoreThenTheLevel(t *testing.T) {
	for _, c := range []struct {
		level, rule, score string
		want               Severity
	}{
		{"error", "", "", High}, {"warning", "", "", Medium}, {"note", "", "", Low}, {"", "", "", Info}, {"", "error", "", High},
		{"note", "", "9.5", Critical}, {"note", "", "7.0", High}, {"error", "", "4.2", Medium}, {"error", "", "1", Low}, {"error", "", "nonsense", High},
	} {
		if got := sarifSeverity(c.level, c.rule, c.score); got != c.want {
			t.Errorf("sarifSeverity(%q,%q,%q) = %s, want %s", c.level, c.rule, c.score, got, c.want)
		}
	}
}

func TestCleanLineDropsControlCharactersAndCapsOnARuneBoundary(t *testing.T) {
	if got := cleanLine("a\x00b\tc\n", 100); got != "ab c" {
		t.Fatalf("%q", got)
	}
	long := "ééééééééééé" // 11 runes, 22 bytes
	got := cleanLine(long, 10)
	if len(got) > 10+3 || got[len(got)-3:] != "…" {
		t.Fatalf("%q (%d bytes)", got, len(got))
	}
}

func TestSummarizeNamesWhatWasFoundAndWhatDidNotRun(t *testing.T) {
	rep := Report{Status: StatusDone, StartedAt: 1000, FinishedAt: 13_400, Engines: []EngineResult{
		{ID: Secrets, Name: "Secrets", Status: Done, Findings: 1},
		{ID: Deps, Name: "Dependencies", Status: Done, Findings: 0},
		{ID: SAST, Name: "Code", Status: Skipped, Reason: "Opengrep is not installed, so no code was checked. Install Opengrep from somewhere."},
	}}
	got := summarize(rep)
	want := "Scanned in 12.4 s: 1 secret, no vulnerable packages; the Code engine was skipped (Opengrep is not installed, so no code was checked)."
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	rep.Status = StatusCancelled
	if got := summarize(rep); got != "Cancelled after 12.4 s." {
		t.Fatalf("%q", got)
	}
	if durText(250*time.Millisecond) != "250 ms" || durText(90*time.Second) != "1 min 30 s" {
		t.Fatal(durText(250*time.Millisecond), durText(90*time.Second))
	}
}

func TestCheckRulesWantsAFolderWithRuleFiles(t *testing.T) {
	if ok, note := checkRules(""); ok || note == "" {
		t.Fatal(ok, note)
	}
	if ok, _ := checkRules(filepath.Join(t.TempDir(), "missing")); ok {
		t.Fatal("a missing folder is not usable")
	}
	if ok, note := checkRules("rules"); ok || !strings.Contains(note, "full path") {
		t.Fatal("a relative folder would mean two different folders:", ok, note)
	}
	d := t.TempDir()
	if ok, note := checkRules(d); ok || note != "The rules folder has no .yaml or .yml rule files." {
		t.Fatal(ok, note)
	}
	os.MkdirAll(filepath.Join(d, "go"), 0o755)
	os.WriteFile(filepath.Join(d, "go", "rule.yml"), []byte("rules: []\n"), 0o644)
	if ok, note := checkRules(d); !ok || note != "" {
		t.Fatal(ok, note)
	}
}

func TestEveryToolRunsWithGitHardenedAgainstTheProjectsConfiguration(t *testing.T) {
	o := &Options{Env: func() []string { return []string{"PATH=/bin", "HOME=/h"} }}
	env := o.env()
	has := func(kv string) bool {
		for _, e := range env {
			if e == kv {
				return true
			}
		}
		return false
	}
	if !has("PATH=/bin") || !has("GIT_CONFIG_COUNT=3") || !has("GIT_CONFIG_KEY_1=core.fsmonitor") || !has("GIT_CONFIG_VALUE_1=false") {
		t.Fatalf("tool environment: %v", env)
	}
	// the caller's slice is not written to
	base := make([]string, 2, 10)
	base[0], base[1] = "PATH=/bin", "HOME=/h"
	o = &Options{Env: func() []string { return base }}
	_ = o.env()
	if len(base) != 2 || cap(base) != 10 || base[:3][2] != "" {
		t.Fatalf("the caller's environment was extended in place: %v", base[:3])
	}
}

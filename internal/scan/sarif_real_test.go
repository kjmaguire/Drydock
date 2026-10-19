package scan_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/scan"
)

// TestSARIFFromARealSemgrepRunIsReadWithoutTheMatchedCode reads the SARIF that Semgrep OSS 1.181.0 wrote for the rule the Security
// page offers to try (ui/app/screens/security/rules-sources.ts, docs/security-scan.md section 5). Semgrep is the engine Opengrep was
// forked from and the two write the same SARIF; this is the only output of a real engine of that family in the repository (not
// Opengrep's own: the development host cannot fetch it). It was captured in a git repository holding main.go (a fmt.Println and a
// fmt.Printf), sub/other.go (a fmt.Println and a fmt.Sprint) and notes.py, with
//
//	semgrep scan -f <folder>/rules --sarif-output=out.sarif -q --metrics=off --disable-version-check .
//
// run in it (Drydock also passes --no-progress-bar, which Opengrep documents and Semgrep does not know), the rule folder's long
// path in the rule id shortened to home.dev.rules, and the JSON indented. Two things in it
// are not what the stand-in's log looks like and are the reason to keep it: a result carries no `level` (the rule's
// defaultConfiguration has it), and every result carries the matched line of code (region.snippet), which no finding may keep.
func TestSARIFFromARealSemgrepRunIsReadWithoutTheMatchedCode(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "semgrep-oss-1.181.0.sarif"))
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{`fmt.Println(\"hello\")`, `fmt.Println(\"x is\", x)`} {
		if !bytes.Contains(data, []byte(code)) {
			t.Fatalf("the capture should hold the matched code %q in region.snippet: the test is about leaving it out", code)
		}
	}
	got, version, err := scan.ParseSARIF(data, scan.SAST, "/home/dev/code", nil)
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.181.0" {
		t.Errorf("version %q", version)
	}
	want := []scan.Finding{
		{Engine: scan.SAST, Severity: scan.Low, Rule: "home.dev.rules.demo-fmt-println", Title: "Debug print left in the code", File: "main.go", Line: 6},
		{Engine: scan.SAST, Severity: scan.Low, Rule: "home.dev.rules.demo-fmt-println", Title: "Debug print left in the code", File: "sub/other.go", Line: 6},
	}
	if len(got) != len(want) {
		t.Fatalf("%d findings, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Engine != want[i].Engine || got[i].Severity != want[i].Severity || got[i].Rule != want[i].Rule || got[i].Title != want[i].Title || got[i].File != want[i].File || got[i].Line != want[i].Line || got[i].EndLine != 0 {
			t.Errorf("finding %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
	// what the engine matched never reaches a finding (decision P44: a finding names the rule, the file and the line)
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"hello", "x is", "Println(", "\\t"} {
		if strings.Contains(string(b), code) {
			t.Errorf("a finding carries the matched code (%q): %s", code, b)
		}
	}
}

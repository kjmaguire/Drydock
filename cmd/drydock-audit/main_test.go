package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyMeasuresTheGap(t *testing.T) {
	cases := []struct{ used, latest, want string }{
		{"1.2.3", "1.2.3", "current"},
		{"1.2.3", "1.2.4", "patch"},
		{"1.2.3", "1.3.0", "minor"},
		{"1.2.3", "2.0.0", "major"},
		{"v0.9.0", "v0.10.0", "minor"},
		{"22.22.0", "22.23.3", "minor"},
		{"1.26.0", "1.27.2", "minor"},
		{"3.0.0-beta.28", "3.0.0-beta.28", "current"},
		{"3.0.0-beta.27", "3.0.0-beta.28", "prerelease"},
		{"1.0.0", "", "unknown"},
		{"not pinned", "1.0.0", "unknown"},
		{"2.0.0", "1.9.0", "current"}, // newer than the registry's latest (a pre-release channel): not behind
	}
	for _, c := range cases {
		if got := classify(c.used, c.latest); got != c.want {
			t.Errorf("classify(%q, %q) = %s, want %s", c.used, c.latest, got, c.want)
		}
	}
}

func TestParseSemverReadsGoAndNpmVersions(t *testing.T) {
	for _, c := range []struct {
		in         string
		maj, min   int
		pat        int
		pre, valid bool
	}{
		{"1.26.0", 1, 26, 0, false, true},
		{"v1.60.1", 1, 60, 1, false, true},
		{"go1.27rc2", 1, 27, 0, true, true},
		{"go1.27.2", 1, 27, 2, false, true},
		{"3.0.0-beta.28", 3, 0, 0, true, true},
		{"v4", 0, 0, 0, false, false},
	} {
		maj, min, pat, pre, ok := parseSemver(c.in)
		if ok != c.valid || (ok && (maj != c.maj || min != c.min || pat != c.pat || pre != c.pre)) {
			t.Errorf("parseSemver(%q) = %d.%d.%d pre=%v ok=%v", c.in, maj, min, pat, pre, ok)
		}
	}
	if !less("1.27.1", "1.27.2") || less("1.27.2", "1.27.2") || !less("1.9.0", "1.10.0") {
		t.Error("less orders versions numerically")
	}
}

// A sentence in docs/decisions.md that names a different version than the code pins is reported, and a library whose name only
// ends the same (acp-go-sdk and go-sdk) is not mistaken for the other.
func TestDocDriftFindsDisagreementsAndIgnoresLookalikes(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs", "decisions.md"), []byte("| ACP | `github.com/coder/acp-go-sdk` v0.13.5 |\n| MCP | `github.com/modelcontextprotocol/go-sdk` v1.8.0 |\n| Tests | Playwright 1.64.0 |\n"), 0o644)
	old := root
	root = dir
	defer func() { root = old }()
	items := []Item{
		{ID: "go:github.com/coder/acp-go-sdk", Name: "github.com/coder/acp-go-sdk", Kind: "direct", Used: "0.13.5"},
		{ID: "go:github.com/modelcontextprotocol/go-sdk", Name: "github.com/modelcontextprotocol/go-sdk", Kind: "direct", Used: "1.8.0"},
		{ID: "npm:@playwright/test", Name: "@playwright/test", Kind: "dev", Used: "1.56.1"},
	}
	got := docDrift(items)
	if len(got) != 1 || got[0].Name != "@playwright/test" || got[0].Docs != "1.64.0" || got[0].Code != "1.56.1" {
		t.Fatalf("drift = %+v", got)
	}
}

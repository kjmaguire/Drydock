package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Antigravity CLI is not on npm, so the npm pins leave it out; it is listed anyway, at its manifest's version.
func TestAnAgentCLIOffNPMIsListed(t *testing.T) {
	items := otherAgentCLIs()
	var agy *Item
	for i := range items {
		if items[i].ID == "cli:antigravity" {
			agy = &items[i]
		}
		if items[i].ID == "cli:claude" {
			t.Error("claude is on npm and is listed by the npm pins already")
		}
	}
	if agy == nil || agy.Used == "" || agy.Latest != "" || !strings.Contains(agy.Note, "Not distributed on npm") {
		t.Fatalf("%+v", agy)
	}
}

const debianLib = `Maintainers: x
Tags: bookworm, bookworm-20261005, 12.15, 12
Architectures: amd64

Tags: bookworm-slim, 12-slim

Tags: trixie, trixie-20261005, 13.7, 13, latest

Tags: forky, forky-20261005
`

const ubuntuLib = `Tags: 24.04, noble-20260930.1, noble

Tags: 26.04, resolute-20261006, resolute, latest

Tags: 26.10, stonking, rolling
`

const nodeLib = `Tags: 24.11.0-bookworm, 24-bookworm, lts-bookworm
SharedTags: 24, lts

Tags: 24.11.0-trixie, 24-trixie, lts-trixie
`

// Images are measured against Docker's official-images library: the newest Debian or Ubuntu release, and whether the tag is
// still published.
func TestImagesAreJudgedAgainstTheOfficialLibrary(t *testing.T) {
	l := parseLibrary(map[string]string{"debian": debianLib, "ubuntu": ubuntuLib, "node": nodeLib, "python": "Tags: 3-trixie\n"})
	if l.debianLatest != "trixie" || l.ubuntuLatest != "26.04" || !l.codenames["bookworm"] || !l.tags["node"]["24"] {
		t.Fatalf("%+v", l)
	}
	for _, c := range []struct{ ref, latest, status string }{
		{"debian:bookworm", "debian:trixie", "major"},
		{"debian:trixie", "debian:trixie", "current"},
		{"node:24-bookworm", "node:24-trixie", "major"},
		{"ubuntu:24.04", "ubuntu:26.04", "major"},
		{"ubuntu:26.04", "ubuntu:26.04", "current"},
		{"python:3-bookworm", "", "major"}, // no longer published
	} {
		latest, status, _ := l.judge(c.ref)
		if latest != c.latest || status != c.status {
			t.Errorf("%s: %s %s, want %s %s", c.ref, latest, status, c.latest, c.status)
		}
	}
	if m := imageR.FindAllStringSubmatch("c := Config{Image: \"debian:bookworm\"}\nconst Dockerfile = `FROM ubuntu:24.04\nRUN x`\nx := \"nginx:1\"", -1); len(m) != 2 || m[0][1] != "debian:bookworm" || m[1][1] != "ubuntu:24.04" {
		t.Fatalf("%v", m)
	}
}

// CI's versioned installs are read from the workflow text: npm globals, npx and go install or go run, each once.
func TestCIInstallsAreReadFromTheWorkflows(t *testing.T) {
	wf := `      - run: |
          npm install -g @anthropic-ai/claude-code@2.1.293 @openai/codex@0.161.0 opencode-ai@1.18.35
          npm i -g typescript
      - run: ./bin/acpspawn.exe 180 npx -y @agentclientprotocol/codex-acp@2.1.1
      - run: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.30
      - run: go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
      - run: npx -y @agentclientprotocol/codex-acp@2.1.1
      - run: npm install -g ${{ inputs.pkg }}@1.0.0
`
	got := map[string]string{}
	for _, s := range workflowInstalls(wf) {
		got[s.pkg] = s.ver
		if s.goPkg != strings.Contains(s.pkg, ".") {
			t.Errorf("%s: go %v", s.pkg, s.goPkg)
		}
	}
	want := map[string]string{"@anthropic-ai/claude-code": "2.1.293", "@openai/codex": "0.161.0", "opencode-ai": "1.18.35",
		"@agentclientprotocol/codex-acp": "2.1.1", "github.com/wailsapp/wails/v3/cmd/wails3": "v3.0.0-beta.30", "golang.org/x/vuln/cmd/govulncheck": "v1.9.0"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for pkg, mod := range map[string]string{"github.com/wailsapp/wails/v3/cmd/wails3": "github.com/wailsapp/wails/v3", "golang.org/x/vuln/cmd/govulncheck": "golang.org/x/vuln", "github.com/a/b/cmd/c": "github.com/a/b"} {
		if got := goModule(pkg); got != mod {
			t.Errorf("goModule(%s) = %s", pkg, got)
		}
	}
}

// The MCP specification tags its revisions by date; release candidates do not count.
func TestTheNewestMCPRevisionIsItsLatestDatedTag(t *testing.T) {
	tags := []string{"2025-06-18", "2025-11-25", "2025-11-25-RC", "2026-07-28-RC", "draft"}
	if got := newestDated(tags); got != "2025-11-25" {
		t.Fatalf("%s", got)
	}
	if datedStatus("2025-11-25", "2025-11-25") != "current" || datedStatus("2025-06-18", "2025-11-25") != "minor" || datedStatus("2026-07-28", "") != "unknown" {
		t.Fatal("status")
	}
}

// A value the code no longer carries is a failed lookup, not a silent blank.
func TestAPinThatMovedIsAFailedLookup(t *testing.T) {
	old := root
	root = sourceRoot(t)
	t.Cleanup(func() { root = old })
	if v := pinnedIn("internal/mcp/protocol.go", `protocolNew = "([0-9-]+)"`); v == "" {
		t.Fatal("the MCP revision was not read")
	}
	before := len(failed)
	if v := pinnedIn("internal/mcp/protocol.go", `noSuchConstant = "(x)"`); v != "" || len(failed) != before+1 {
		t.Fatalf("%q, %d failures", v, len(failed)-before)
	}
	failed = failed[:before]
}

// sourceRoot is Drydock's source tree above the test's folder. The tests that use it read the sources; the Windows
// verification bundle carries only test binaries (its folder has a go.mod of its own), so there they skip.
func sourceRoot(t *testing.T) string {
	t.Helper()
	d, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if b, err := os.ReadFile(filepath.Join(d, "go.mod")); err == nil {
			if first, _, _ := strings.Cut(string(b), "\n"); strings.TrimSpace(first) != "module github.com/kjmaguire/drydock" {
				t.Skipf("%s is not Drydock's source tree (a verification bundle carries no sources), and this test reads them", d)
			}
			return d
		}
		p := filepath.Dir(d)
		if p == d {
			t.Skip("no source tree above the test's folder (a verification bundle carries no sources), and this test reads them")
		}
		d = p
	}
}

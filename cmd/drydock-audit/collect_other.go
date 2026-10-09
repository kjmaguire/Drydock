package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var usesR = regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*([\w.-]+/[\w./-]+)@(\S+)`)

// actions: the GitHub Actions the workflows use, against the newest release tag of each (git ls-remote; the GitHub API is not
// needed).
func actions(ctx context.Context) []Item {
	files, _ := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	seen := map[string]string{}
	where := map[string][]string{}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		for _, m := range usesR.FindAllStringSubmatch(string(b), -1) {
			seen[m[1]] = m[2]
			where[m[1]] = append(where[m[1]], filepath.Base(f))
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	var items []Item
	items = append(items, ciGoPins(files)...)
	for _, n := range names {
		it := Item{ID: "action:" + n, Group: "GitHub Actions (CI)", Name: n, Kind: "action", Used: seen[n], Where: ".github/workflows/" + strings.Join(dedupe(where[n]), ", ")}
		out, err := run(ctx, root, nil, "git", "ls-remote", "--tags", "--refs", "https://github.com/"+n)
		if err != nil {
			fail("tags of " + n + ": " + err.Error())
		} else {
			best := ""
			for _, line := range strings.Split(out, "\n") {
				i := strings.Index(line, "refs/tags/")
				if i < 0 {
					continue
				}
				t := line[i+len("refs/tags/"):]
				if _, _, _, pre, ok := parseSemver(t); ok && !pre && strings.Count(t, ".") == 2 && (best == "" || less(best, t)) {
					best = t
				}
			}
			it.Latest = best
			// workflows pin a major tag (v4): only a new major is a gap worth showing
			um, _, _, _, ok1 := parseSemver(seen[n] + ".0.0")
			lm, _, _, _, ok2 := parseSemver(best)
			switch {
			case !ok1 || !ok2:
				it.Status = "unknown"
			case lm > um:
				it.Status = "major"
			default:
				it.Status = "current"
			}
		}
		items = append(items, it)
	}
	return items
}

var goVersionR = regexp.MustCompile(`(?m)^\s*go-version:\s*"?([0-9][0-9.]*)"?`)

// ciGoPins: workflows that pin an exact Go version (instead of reading go.mod) are measured against the newest Go release.
func ciGoPins(files []string) []Item {
	pins := map[string][]string{}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		for _, m := range goVersionR.FindAllStringSubmatch(string(b), -1) {
			pins[m[1]] = append(pins[m[1]], filepath.Base(f))
		}
	}
	var out []Item
	for v, where := range pins {
		out = append(out, Item{ID: "ci-go:" + v, Group: "GitHub Actions (CI)", Name: "Go version pinned in a workflow", Kind: "action", Used: v, Latest: latestGo,
			Where: ".github/workflows/" + strings.Join(dedupe(where), ", ")})
	}
	return out
}

func dedupe(s []string) []string {
	m := map[string]bool{}
	var out []string
	for _, x := range s {
		if !m[x] {
			m[x] = true
			out = append(out, x)
		}
	}
	return out
}

func npmLatest(ctx context.Context, pkg string) string {
	out, err := run(ctx, root, nil, "npm", "view", pkg, "version")
	if err != nil {
		fail("npm view " + pkg + ": " + err.Error())
		return ""
	}
	return strings.TrimSpace(out)
}

func goLatest(ctx context.Context, mod string) (version, date string) {
	out, err := run(ctx, root, nil, "go", "list", "-m", "-json", mod+"@latest")
	if err != nil {
		fail("latest of " + mod + ": " + err.Error())
		return "", ""
	}
	var m struct{ Version, Time string }
	_ = json.Unmarshal([]byte(out), &m)
	return strings.TrimPrefix(m.Version, "v"), day(m.Time)
}

var (
	bridgeR  = regexp.MustCompile(`"(@[\w.-]+/[\w.-]+)@(\d+\.\d+\.\d+)"`)
	recommR  = regexp.MustCompile(`versions:\s*\{min:\s*"([^"]+)",\s*recommended:\s*"([^"]+)"\}`)
	cliNames = map[string]string{
		"claude": "@anthropic-ai/claude-code", "codex": "@openai/codex", "gemini": "@google/gemini-cli",
		"goose": "@aaif/goose-acp", "opencode": "opencode-ai",
	}
)

// agentSoftware: the agent CLIs Drydock drives (the version its manifest recommends, against the newest release) and the ACP
// bridges it starts with npx (the pinned version, against the newest).
func agentSoftware(ctx context.Context) []Item {
	dir := filepath.Join(root, "internal", "agents", "manifests", "cli")
	files, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	sort.Strings(files)
	var items []Item
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".yaml")
		b, _ := os.ReadFile(f)
		text := string(b)
		if pkg, ok := cliNames[id]; ok {
			if m := recommR.FindStringSubmatch(text); m != nil {
				it := Item{ID: "cli:" + id, Group: "Agent CLIs Drydock drives", Name: id + " (" + pkg + ")", Kind: "cli", Used: m[2], Where: "internal/agents/manifests/cli/" + id + ".yaml (recommended; minimum " + m[1] + ")"}
				it.Latest = npmLatest(ctx, pkg)
				if id == "claude" {
					if v, err := run(ctx, root, nil, "claude", "--version"); err == nil {
						f := strings.Fields(v)
						if len(f) > 0 {
							it.Note = "Installed in this sandbox: " + f[0] + "."
						}
					}
				}
				items = append(items, it)
			}
		}
		for _, m := range bridgeR.FindAllStringSubmatch(text, -1) {
			it := Item{ID: "bridge:" + m[1], Group: "Agent CLIs Drydock drives", Name: m[1] + " (ACP bridge)", Kind: "bridge", Used: m[2], Where: "internal/agents/manifests/cli/" + id + ".yaml"}
			it.Latest = npmLatest(ctx, m[1])
			items = append(items, it)
		}
	}
	return items
}

// tools: programs Drydock runs or tells you to install, with the newest release. The version "used" is what the documentation
// says the project is built and tested with (docs/software-notes.json "used"), because none of these is a dependency in a lock file.
func tools(ctx context.Context) []Item {
	type tool struct{ id, name, kind, goMod, npmPkg string }
	list := []tool{
		{"tool:gh", "GitHub CLI (gh)", "tool", "github.com/cli/cli/v2", ""},
		{"tool:task", "go-task", "tool", "github.com/go-task/task/v3", ""},
		{"tool:osv-scanner", "osv-scanner", "tool", "github.com/google/osv-scanner/v2", ""},
		{"tool:go-licenses", "go-licenses", "tool", "github.com/google/go-licenses/v2", ""},
		{"tool:govulncheck", "govulncheck", "tool", "golang.org/x/vuln", ""},
		{"tool:wrangler", "Wrangler", "tool", "", "wrangler"},
	}
	used := toolUsed()
	var items []Item
	for _, t := range list {
		it := Item{ID: t.id, Group: "Tools Drydock runs or you install", Name: t.name, Kind: t.kind, Used: used[t.id], Where: "docs/decisions.md section 2"}
		if t.goMod != "" {
			it.Latest, it.LatestDate = goLatest(ctx, t.goMod)
		} else {
			it.Latest = npmLatest(ctx, t.npmPkg)
		}
		if it.Used == "" {
			it.Used, it.Status = "not pinned", "unknown"
		}
		items = append(items, it)
	}
	return items
}

// toolUsed reads the "used" versions of the tools from docs/software-notes.json.
func toolUsed() map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(filepath.Join(root, "docs", "software-notes.json"))
	if err != nil {
		return out
	}
	var n struct {
		Used map[string]string `json:"used"`
	}
	_ = json.Unmarshal(b, &n)
	return n.Used
}

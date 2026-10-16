package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kjmaguire/drydock/internal/agents/pins"
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
		best, err := gitLatestTag(ctx, actionRepo(n))
		if err != nil {
			fail("tags of " + n + ": " + err.Error())
		} else {
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

// actionRepo is the repository an action lives in: a workflow names an action inside a repository by its path
// (github/codeql-action/init), and the tags are the repository's.
func actionRepo(action string) string {
	parts := strings.SplitN(action, "/", 3)
	if len(parts) < 2 {
		return action
	}
	return parts[0] + "/" + parts[1]
}

// gitLatestTag is the newest stable release tag (vX.Y.Z or X.Y.Z) of a GitHub repository, read with git ls-remote: no API
// token is needed, and it works for projects that publish only GitHub releases (Opengrep).
func gitLatestTag(ctx context.Context, repo string) (string, error) {
	out, err := run(ctx, root, nil, "git", "ls-remote", "--tags", "--refs", "https://github.com/"+repo)
	if err != nil {
		return "", err
	}
	return newestStableTag(out), nil
}

// newestStableTag picks the highest vX.Y.Z (or X.Y.Z) tag of git ls-remote output; pre-releases and alpha tags are skipped.
func newestStableTag(lsRemote string) string {
	best := ""
	for _, line := range strings.Split(lsRemote, "\n") {
		i := strings.Index(line, "refs/tags/")
		if i < 0 {
			continue
		}
		t := line[i+len("refs/tags/"):]
		if _, _, _, pre, ok := parseSemver(t); ok && !pre && strings.Count(t, ".") == 2 && (best == "" || less(best, t)) {
			best = t
		}
	}
	return best
}

// gitLatestBuildTag is the newest build tag (bNNNN, llama.cpp's weekly builds) of a GitHub repository.
func gitLatestBuildTag(ctx context.Context, repo string) (string, error) {
	out, err := run(ctx, root, nil, "git", "ls-remote", "--tags", "--refs", "https://github.com/"+repo)
	if err != nil {
		return "", err
	}
	return newestBuildTag(out), nil
}

var buildTagR = regexp.MustCompile(`refs/tags/(b(\d+))$`)

// newestBuildTag picks the highest bNNNN tag of git ls-remote output.
func newestBuildTag(lsRemote string) string {
	best, bestN := "", -1
	for _, line := range strings.Split(lsRemote, "\n") {
		m := buildTagR.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		if n > bestN {
			best, bestN = m[1], n
		}
	}
	return best
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
	v, err := pins.Latest(ctx, pkg)
	if err != nil {
		fail("npm view " + pkg + ": " + err.Error())
		return ""
	}
	return v
}

func goLatest(ctx context.Context, mod string) (version, date string) {
	out, err := run(ctx, root, nil, "go", "list", "-m", "-json", mod+"@latest")
	if err != nil {
		fail("latest of " + mod + ": " + err.Error())
		return "", ""
	}
	var m struct{ Version, Time string }
	_ = json.Unmarshal([]byte(out), &m)
	return releaseOf(m.Version), day(m.Time)
}

// releaseOf is a module proxy version as the project's releases name it: no "v", and no "+incompatible", which the proxy adds
// to projects that tag major versions above 1 without a /vN module path (github.com/docker/cli is v29.9.0+incompatible).
func releaseOf(v string) string {
	return strings.TrimSuffix(strings.TrimPrefix(v, "v"), "+incompatible")
}

// agentSoftware: the agent CLIs Drydock drives (the version its manifest recommends, against the newest release) and the ACP
// bridges it starts with npx (the pinned version, against the newest). The pins are read by internal/agents/pins, which the
// contract suite's version watch (cmd/drydock-contract) uses too.
func agentSoftware(ctx context.Context) []Item {
	ps, err := pins.Embedded()
	if err != nil {
		fail("agent manifests: " + err.Error())
		return nil
	}
	var items []Item
	for _, p := range ps {
		switch p.Kind {
		case pins.KindCLI:
			it := Item{ID: p.ID(), Group: "Agent CLIs Drydock drives", Name: p.Adapter + " (" + p.Package + ")", Kind: "cli", Used: p.Version, Where: p.File + " (recommended; minimum " + p.Min + ")"}
			it.Latest = npmLatest(ctx, p.Package)
			if p.Adapter == "claude" {
				if v, err := run(ctx, root, nil, "claude", "--version"); err == nil {
					f := strings.Fields(v)
					if len(f) > 0 {
						it.Note = "Installed in this sandbox: " + f[0] + "."
					}
				}
			}
			items = append(items, it)
		case pins.KindBridge:
			it := Item{ID: p.ID(), Group: "Agent CLIs Drydock drives", Name: p.Package + " (ACP bridge)", Kind: "bridge", Used: p.Version, Where: p.File}
			it.Latest = npmLatest(ctx, p.Package)
			items = append(items, it)
		}
	}
	return items
}

// tools: programs Drydock runs or tells you to install, with the newest release. The version "used" is what the documentation
// says the project is built and tested with (docs/software-notes.json "used"), because none of these is a dependency in a lock file.
func tools(ctx context.Context) []Item {
	// a tool is measured against the Go module proxy, npm, or (a project that publishes only GitHub releases) its git tags
	type tool struct {
		id, name, kind, goMod, npmPkg, ghRepo string
		buildTags                             bool // the repository tags builds bNNNN instead of versions (llama.cpp)
	}
	list := []tool{
		{"tool:gh", "GitHub CLI (gh)", "tool", "github.com/cli/cli/v2", "", "", false},
		{"tool:task", "go-task", "tool", "github.com/go-task/task/v3", "", "", false},
		{"tool:osv-scanner", "osv-scanner", "tool", "github.com/google/osv-scanner/v2", "", "", false},
		{"tool:opengrep", "Opengrep (the security scan's Code engine)", "tool", "", "", "opengrep/opengrep", false},
		{"tool:go-licenses", "go-licenses", "tool", "github.com/google/go-licenses/v2", "", "", false},
		{"tool:govulncheck", "govulncheck", "tool", "golang.org/x/vuln", "", "", false},
		{"tool:golangci-lint", "golangci-lint (CI lint job)", "tool", "github.com/golangci/golangci-lint/v2", "", "", false},
		{"tool:wrangler", "Wrangler", "tool", "", "wrangler", "", false},
		{"tool:docker", "Docker-compatible container engine (docker)", "tool", "github.com/docker/cli", "", "", false},
		{"tool:actions-runner", "GitHub Actions runner (the CI runners download it)", "tool", "", "", "actions/runner", false},
		{"tool:llama.cpp", "llama.cpp (the local models' engine, decision P65)", "tool", "", "", "ggml-org/llama.cpp", true},
	}
	used := toolUsed()
	var items []Item
	for _, t := range list {
		it := Item{ID: t.id, Group: "Tools Drydock runs or you install", Name: t.name, Kind: t.kind, Used: used[t.id], Where: "docs/decisions.md section 2"}
		switch {
		case t.goMod != "":
			it.Latest, it.LatestDate = goLatest(ctx, t.goMod)
		case t.ghRepo != "" && t.buildTags:
			tag, err := gitLatestBuildTag(ctx, t.ghRepo)
			if err != nil {
				fail("tags of " + t.ghRepo + ": " + err.Error())
			}
			it.Latest = tag
		case t.ghRepo != "":
			tag, err := gitLatestTag(ctx, t.ghRepo)
			if err != nil {
				fail("tags of " + t.ghRepo + ": " + err.Error())
			}
			it.Latest = releaseOf(tag)
		default:
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

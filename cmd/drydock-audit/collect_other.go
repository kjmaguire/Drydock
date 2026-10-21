package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kjmaguire/drydock/internal/agents/pins"
	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/localllm"
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

// lsRemoteTags lists the tag names of a GitHub repository with git ls-remote: no API token is needed, and it works for
// projects that publish only GitHub releases (Opengrep).
func lsRemoteTags(ctx context.Context, repo string) ([]string, error) {
	out, err := run(ctx, root, nil, "git", "ls-remote", "--tags", "--refs", "https://github.com/"+repo)
	if err != nil {
		return nil, err
	}
	return tagNames(out), nil
}

// tagNames reads the tag names of git ls-remote --tags --refs output.
func tagNames(lsRemote string) []string {
	var out []string
	for _, line := range strings.Split(lsRemote, "\n") {
		if i := strings.Index(line, "refs/tags/"); i >= 0 {
			if t := strings.TrimSpace(line[i+len("refs/tags/"):]); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

// gitLatestTag is the newest stable release tag (vX.Y.Z or X.Y.Z) of a GitHub repository.
func gitLatestTag(ctx context.Context, repo string) (string, error) {
	tags, err := lsRemoteTags(ctx, repo)
	if err != nil {
		return "", err
	}
	return newestStableTag(tags), nil
}

// newestStableTag picks the highest vX.Y.Z (or X.Y.Z) tag; pre-releases and alpha tags are skipped.
func newestStableTag(tags []string) string {
	best := ""
	for _, t := range tags {
		if _, _, _, pre, ok := parseSemver(t); ok && !pre && strings.Count(t, ".") == 2 && (best == "" || less(best, t)) {
			best = t
		}
	}
	return best
}

var buildTagR = regexp.MustCompile(`^b(\d+)$`)

// newestBuildTag picks the highest bNNNN tag (llama.cpp's weekly builds).
func newestBuildTag(tags []string) string {
	best, bestN := "", -1
	for _, t := range tags {
		m := buildTagR.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		if n, _ := strconv.Atoi(m[1]); n > bestN {
			best, bestN = t, n
		}
	}
	return best
}

// forkReleaseR is a release tag of the TurboQuant fork: the llama.cpp build it is based on, then its own version (b10298-2.0.0).
var forkReleaseR = regexp.MustCompile(`^b(\d+)-(\d+\.\d+\.\d+)$`)

// A latestFunc finds the newest release of a tool's GitHub repository, with a note when how it was found matters.
type latestFunc func(ctx context.Context, repo string) (latest, note string, err error)

// stableRelease is the newest vX.Y.Z tag, as a version (the default for a repository).
func stableRelease(ctx context.Context, repo string) (string, string, error) {
	tag, err := gitLatestTag(ctx, repo)
	return releaseOf(tag), "", err
}

// buildRelease is the newest bNNNN build tag (llama.cpp's weekly builds; its vX.Y.Z tags carry no binaries).
func buildRelease(ctx context.Context, repo string) (string, string, error) {
	tags, err := lsRemoteTags(ctx, repo)
	return newestBuildTag(tags), "", err
}

// forkRelease is the newest release of the TurboQuant fork Drydock would install: read from GitHub's release listing with the
// app's own picker (localllm.LatestRelease: no drafts, no pre-releases, the Windows build of the bundled backend with its
// digest, ordered by the fork's version), so the audit and the app agree. Where the listing cannot be read (no API access),
// the newest release tag stands in, and the note says what was not checked.
func forkRelease(ctx context.Context, repo string) (string, string, error) {
	src, _ := localllm.SourceByID(localllm.SourceTurboQuant)
	if src.Repo != repo {
		return "", "", fmt.Errorf("%s is not the TurboQuant fork's repository", repo)
	}
	b := localllm.Vulkan
	if p, err := readBundlePins(); err == nil && p.Llama != nil && p.Llama.Backend != "" {
		b = localllm.Backend(p.Llama.Backend)
	}
	api := host.HTTPAPI{BaseURL: "https://api.github.com", Token: os.Getenv("GITHUB_TOKEN")}
	rel, apiErr := localllm.LatestRelease(ctx, api, src, b)
	if apiErr == nil {
		return rel.Tag, "Newest release with the Windows " + localllm.Describe(b).Label + " build and a SHA-256 digest, from GitHub's release listing (as the app's update check picks it).", nil
	}
	tags, err := lsRemoteTags(ctx, repo)
	if err != nil {
		return "", "", err
	}
	best := newestForkTag(src, tags)
	if best == "" {
		return "", "", fmt.Errorf("no release tag (%s) among the tags, and GitHub's release listing could not be read: %s", src.TagForm(), firstLineOf(apiErr.Error()))
	}
	// the listing's error stays on the console: an answer body (a proxy's, a rate limit's) is not for the committed report
	fmt.Fprintln(os.Stderr, "TurboQuant fork: GitHub's release listing could not be read; using its release tags:", firstLineOf(apiErr.Error()))
	return best, "Newest release tag, read from the git tags: GitHub's release listing could not be read here, so whether it is a pre-release or carries the Windows build was not checked.", nil
}

// firstLineOf is the first line of an error, at most 160 characters.
func firstLineOf(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if r := []rune(s); len(r) > 160 {
		s = string(r[:160]) + "..."
	}
	return strings.TrimSpace(s)
}

// newestForkTag picks the source's newest release tag by its own numbering; its dev builds are not release tags.
func newestForkTag(src localllm.Source, tags []string) string {
	best := ""
	for _, t := range tags {
		if !src.ReleaseTag(t) {
			continue
		}
		if newer, _ := src.Newer(t, best); best == "" || newer {
			best = t
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

// followsNewest are the tools Drydock does not pin: it installs the newest release when a person presses Install or Update
// (llama.cpp, decision P65, whose builds come out several times a day), so the version it uses is the newest one and
// docs/software-notes.json carries none that would be a build or two behind by the next audit.
var followsNewest = map[string]bool{"tool:llama.cpp": true}

// usedVersion is the version a tool is shown as using: the newest for one Drydock does not pin, else the recorded one.
func usedVersion(id, recorded, latest string) string {
	if followsNewest[id] {
		return latest
	}
	return recorded
}

// tools: programs Drydock runs or tells you to install, with the newest release. The version "used" is what the documentation
// says the project is built and tested with (docs/software-notes.json "used"), because none of these is a dependency in a lock file.
func tools(ctx context.Context) []Item {
	// a tool is measured against the Go module proxy, npm, or (a project that publishes only GitHub releases) its git tags
	type tool struct {
		id, name, kind, goMod, npmPkg, ghRepo string
		latest                                latestFunc // for a GitHub repository: how its newest release is found (nil: the newest vX.Y.Z tag)
	}
	list := []tool{
		{"tool:gh", "GitHub CLI (gh)", "tool", "github.com/cli/cli/v2", "", "", nil},
		{"tool:task", "go-task", "tool", "github.com/go-task/task/v3", "", "", nil},
		{"tool:osv-scanner", "osv-scanner", "tool", "github.com/google/osv-scanner/v2", "", "", nil},
		{"tool:opengrep", "Opengrep (the security scan's Code engine)", "tool", "", "", "opengrep/opengrep", nil},
		{"tool:go-licenses", "go-licenses", "tool", "github.com/google/go-licenses/v2", "", "", nil},
		{"tool:govulncheck", "govulncheck", "tool", "golang.org/x/vuln", "", "", nil},
		{"tool:golangci-lint", "golangci-lint (CI lint job)", "tool", "github.com/golangci/golangci-lint/v2", "", "", nil},
		{"tool:wrangler", "Wrangler", "tool", "", "wrangler", "", nil},
		{"tool:docker", "Docker-compatible container engine (docker)", "tool", "github.com/docker/cli", "", "", nil},
		{"tool:actions-runner", "GitHub Actions runner (the CI runners download it)", "tool", "", "", "actions/runner", nil},
		{"tool:git", "Git (Drydock runs it for every repository; Git for Windows on a PC)", "tool", "", "", "git/git", nil},
		{"tool:semgrep", "Semgrep OSS (checks the built-in rules against their fixtures; not shipped)", "tool", "", "", "semgrep/semgrep", nil},
		{"tool:llama.cpp", "llama.cpp (the local models' engine, decision P65)", "tool", "", "", "ggml-org/llama.cpp", buildRelease},
		{"tool:llama.cpp-turboquant", "llama.cpp TurboQuant fork (comes with the download, Vulkan build, decision P77)", "tool", "", "", "AtomicBot-ai/atomic-llama-cpp-turboquant", forkRelease},
	}
	used := toolUsed()
	bundled := map[string]string{}
	if p, err := readBundlePins(); err != nil {
		fail("packaging/bundle.json: " + err.Error())
	} else {
		bundled = p.used()
	}
	var items []Item
	for _, t := range list {
		it := Item{ID: t.id, Group: "Tools Drydock runs or you install", Name: t.name, Kind: t.kind, Used: used[t.id], Where: "docs/decisions.md section 2"}
		if v, ok := bundled[t.id]; ok {
			it.Used, it.Where = v, "packaging/bundle.json"
		}
		switch {
		case t.goMod != "":
			it.Latest, it.LatestDate = goLatest(ctx, t.goMod)
		case t.ghRepo != "":
			find := t.latest
			if find == nil {
				find = stableRelease
			}
			latest, note, err := find(ctx, t.ghRepo)
			switch {
			case err != nil:
				fail("tags of " + t.ghRepo + ": " + err.Error())
			case latest == "":
				// a lookup that answered but found no release is a broken lookup (the project changed how it tags), not news
				fail("tags of " + t.ghRepo + ": no release tag found")
			}
			it.Latest, it.Note = latest, note
		default:
			it.Latest = npmLatest(ctx, t.npmPkg)
		}
		it.Used = usedVersion(t.id, it.Used, it.Latest)
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

// bundlePins is packaging/bundle.json: the tools the Windows download carries (decision P77). The versions it pins are what
// Drydock ships, so the audit reads them from there, not from a copy in docs/software-notes.json.
type bundlePins struct {
	Scan []struct {
		Tool string `json:"tool"`
		Tag  string `json:"tag"`
	} `json:"scan"`
	Llama *struct {
		Source  string `json:"source"`
		Backend string `json:"backend"`
		Tag     string `json:"tag"`
	} `json:"llama"`
}

func readBundlePins() (bundlePins, error) {
	var p bundlePins
	b, err := os.ReadFile(filepath.Join(root, "packaging", "bundle.json"))
	if err != nil {
		return p, err
	}
	return p, json.Unmarshal(b, &p)
}

// used maps each pinned tool's item id to the version the download carries.
func (p bundlePins) used() map[string]string {
	out := map[string]string{}
	for _, s := range p.Scan {
		out["tool:"+s.Tool] = strings.TrimPrefix(s.Tag, "v")
	}
	if p.Llama != nil {
		out["tool:llama.cpp-"+p.Llama.Source] = p.Llama.Tag
	}
	return out
}

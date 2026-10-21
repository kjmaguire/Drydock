package main

// The pieces of software Drydock uses that are not in a lock file or a manifest of packages (the sweep of 2026-10-20): what is
// copied into it, the skills it names at a commit, the container images it pulls or builds on, the vendor API versions it
// sends, what CI installs by version, and the agent CLIs not distributed on npm. Each value is read from the code that uses it,
// never from a copy, so the page shows what Drydock really uses.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/agents/pins"
	"github.com/kjmaguire/drydock/internal/scan"
)

// extras collects every piece below.
func extras(ctx context.Context) []Item {
	var items []Item
	items = append(items, otherAgentCLIs()...)
	items = append(items, copiedContent(ctx)...)
	items = append(items, pinnedSkills(ctx)...)
	items = append(items, containerImages(ctx)...)
	items = append(items, vendorAPIs(ctx)...)
	items = append(items, ciInstalls(ctx)...)
	return items
}

// readRepo reads a file of the repository.
func readRepo(rel string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	return string(b), err
}

// pinnedIn reads one value from a file of the repository with a regular expression (its first group); a value that is not
// there any more is a failed lookup, never a silent gap.
func pinnedIn(rel, expr string) string {
	s, err := readRepo(rel)
	if err != nil {
		fail(rel + ": " + err.Error())
		return ""
	}
	m := regexp.MustCompile(expr).FindStringSubmatch(s)
	if m == nil {
		fail(rel + ": no match for " + expr)
		return ""
	}
	return m[1]
}

// headCommit is the newest commit of a GitHub repository's default branch.
func headCommit(ctx context.Context, repo string) string {
	out, err := run(ctx, root, nil, "git", "ls-remote", "https://github.com/"+repo, "HEAD")
	if err != nil {
		fail("HEAD of " + repo + ": " + err.Error())
		return ""
	}
	f := strings.Fields(out)
	if len(f) == 0 {
		fail("HEAD of " + repo + ": no answer")
		return ""
	}
	return f[0]
}

// commitItem is a copy pinned at a commit, against the repository's newest commit: the same commit is current; a newer one
// means upstream moved on, which may or may not touch what was copied, so it reads "patch" with a note saying to look.
func commitItem(ctx context.Context, it Item, repo, commit string) Item {
	head := headCommit(ctx, repo)
	it.Used = short(commit)
	if head == "" {
		return it
	}
	it.Latest = short(head)
	if head == commit {
		it.Status = "current"
	} else {
		it.Status = "patch"
		it.Note = withNote(it.Note, "The repository has newer commits than the copy; look at what changed before copying again.")
	}
	return it
}

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// otherAgentCLIs are the agent CLIs whose manifests recommend a version but which are not installed from npm (the Antigravity
// CLI comes from Google's own installer), so internal/agents/pins does not list them. There is no feed to ask for the newest
// release, so they read "unknown" with the reason, instead of being left off the page.
func otherAgentCLIs() []Item {
	set, err := manifests.Embedded()
	if err != nil {
		fail("agent manifests: " + err.Error())
		return nil
	}
	onNPM := map[string]bool{}
	for _, p := range pins.FromSet(set) {
		if p.Kind == pins.KindCLI {
			onNPM[p.Adapter] = true
		}
	}
	var items []Item
	for _, m := range set.All() {
		if m.Versions.Recommended == "" || onNPM[m.Meta.ID] {
			continue
		}
		items = append(items, Item{ID: "cli:" + m.Meta.ID, Group: "Agent CLIs Drydock drives", Name: m.Meta.DisplayName + " (" + m.Meta.ID + ")", Kind: "cli",
			Used: m.Versions.Recommended, Where: "internal/agents/manifests/cli/" + m.Meta.ID + ".yaml (recommended; minimum " + m.Versions.Min + ")",
			Note: "Not distributed on npm (its vendor's own installer), so there is no feed to read its newest release from: check it by hand."})
	}
	return items
}

// iconSetR finds an icon set and its version in the brand logo file's header ("Simple Icons 16.35.0").
var iconSets = []struct{ id, name, npm, expr string }{
	{"copied:simple-icons", "Simple Icons (brand logos, CC0-1.0)", "simple-icons", `Simple Icons (\d+\.\d+\.\d+)`},
	{"copied:lobehub-icons", "LobeHub icons (four brand logos, MIT)", "@lobehub/icons-static-svg", `@lobehub/icons-static-svg (\d+\.\d+\.\d+)`},
}

// copiedContent is what is copied into Drydock rather than installed: the built-in Opengrep rule sets (at a commit) and the
// brand logos (paths from two icon sets, at a release).
func copiedContent(ctx context.Context) []Item {
	var items []Item
	sets, err := scan.BuiltinRuleSets()
	if err != nil {
		fail("built-in rules: " + err.Error())
	}
	for _, s := range sets {
		if s.Commit == "" {
			continue
		}
		repo := strings.TrimPrefix(s.Source, "https://github.com/")
		it := Item{ID: "copied:" + strings.TrimPrefix(s.ID, "third_party/"), Group: "Copied into Drydock", Name: s.Name + " (Opengrep rules, " + s.Licence + ")", Kind: "copied",
			Where: "internal/scan/rules/" + s.ID + " (commit in internal/scan/builtin_rules.go)",
			Role:  fmt.Sprintf("Built-in Opengrep rules: %d rules run on every scan (decision P77).", s.Rules)}
		items = append(items, commitItem(ctx, it, repo, s.Commit))
	}
	for _, ic := range iconSets {
		it := Item{ID: ic.id, Group: "Copied into Drydock", Name: ic.name, Kind: "copied", Used: pinnedIn("ui/app/ui/brand-logo.tsx", ic.expr),
			Where: "ui/app/ui/brand-logo.tsx", Role: "SVG paths of brand logos, copied so only the few the app shows ship."}
		it.Latest = npmLatest(ctx, ic.npm)
		items = append(items, it)
	}
	return items
}

// pinnedSkills are the skills Drydock's prebuilt agents name, each at a commit of its registry (internal/skills/registry.yaml):
// one row per registry and commit.
func pinnedSkills(ctx context.Context) []Item {
	s, err := readRepo("internal/skills/registry.yaml")
	if err != nil {
		fail("internal/skills/registry.yaml: " + err.Error())
		return nil
	}
	var doc struct {
		Registries []struct{ ID, Repo string }            `yaml:"registries"`
		Skills     []struct{ Registry, Name, Ref string } `yaml:"skills"`
	}
	if err := yaml.Unmarshal([]byte(s), &doc); err != nil {
		fail("internal/skills/registry.yaml: " + err.Error())
		return nil
	}
	repoOf := map[string]string{}
	for _, r := range doc.Registries {
		repoOf[r.ID] = r.Repo
	}
	type pin struct{ registry, ref string }
	names := map[pin][]string{}
	for _, k := range doc.Skills {
		p := pin{k.Registry, k.Ref}
		names[p] = append(names[p], k.Name)
	}
	keys := make([]pin, 0, len(names))
	for p := range names {
		keys = append(keys, p)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].registry+keys[i].ref < keys[j].registry+keys[j].ref })
	var items []Item
	for _, p := range keys {
		repo := repoOf[p.registry]
		if repo == "" {
			fail("internal/skills/registry.yaml: skill registry " + p.registry + " is not listed")
			continue
		}
		sort.Strings(names[p])
		it := Item{ID: "skills:" + p.registry + "@" + short(p.ref), Group: "Skills pinned to a commit", Name: repo + " (" + strings.Join(names[p], ", ") + ")", Kind: "skill",
			Where: "internal/skills/registry.yaml", Role: "Skills the prebuilt agents name, with their descriptions quoted at this commit."}
		items = append(items, commitItem(ctx, it, repo, p.ref))
	}
	return items
}

// imageR finds an official Docker image Drydock names in its code: a quoted "name:tag" or a Dockerfile FROM.
var imageR = regexp.MustCompile(`(?:"|FROM )((?:debian|ubuntu|node|golang|python):[A-Za-z0-9._-]+)`)

// containerImages are the official images Drydock pulls or builds on (the CI runner image, the sandbox's suggested images),
// read from the Go code that names them and measured against Docker's official-images library: whether the tag is still
// published, and whether its Debian or Ubuntu release is the newest.
func containerImages(ctx context.Context) []Item {
	where := map[string][]string{}
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || strings.Contains(p, string(filepath.Separator)+"testdata"+string(filepath.Separator)) {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		for _, m := range imageR.FindAllStringSubmatch(string(b), -1) {
			where[m[1]] = appendOnce(where[m[1]], filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		fail("images in the code: " + err.Error())
		return nil
	}
	if len(where) == 0 {
		return nil
	}
	lib, err := officialImages(ctx, imageNames(where))
	if err != nil {
		fail("Docker official images: " + err.Error())
	}
	names := make([]string, 0, len(where))
	for n := range where {
		names = append(names, n)
	}
	sort.Strings(names)
	var items []Item
	for _, ref := range names {
		it := Item{ID: "image:" + ref, Group: "Container images", Name: ref, Kind: "image", Used: ref, Where: strings.Join(where[ref], ", "),
			Role: "A Docker official image Drydock names (the CI runner container, or a sandbox image it suggests)."}
		if lib != nil {
			it.Latest, it.Status, it.Note = lib.judge(ref)
		}
		items = append(items, it)
	}
	return items
}

func appendOnce(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

func imageNames(where map[string][]string) []string {
	set := map[string]bool{"debian": true, "ubuntu": true}
	for ref := range where {
		set[strings.SplitN(ref, ":", 2)[0]] = true
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// imageLibrary is the tags each official image publishes, read from docker-library/official-images.
type imageLibrary struct {
	tags         map[string]map[string]bool // image -> tag -> published
	debianLatest string                     // the codename Debian's "latest" is (trixie)
	ubuntuLatest string                     // the version Ubuntu's "latest" is (26.04)
	codenames    map[string]bool            // Debian codenames (bookworm, trixie, forky, ...)
}

// officialImages reads the official-images library files of the images named: a shallow, blob-less, sparse clone, so only those
// files are fetched.
func officialImages(ctx context.Context, names []string) (*imageLibrary, error) {
	dir, err := os.MkdirTemp("", "drydock-audit-images-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if _, err := run(ctx, root, nil, "git", "clone", "-q", "--depth", "1", "--filter=blob:none", "--sparse", "https://github.com/docker-library/official-images", dir); err != nil {
		return nil, err
	}
	args := []string{"sparse-checkout", "set", "--no-cone"}
	for _, n := range names {
		args = append(args, "/library/"+n)
	}
	if _, err := run(ctx, dir, nil, "git", args...); err != nil {
		return nil, err
	}
	files := map[string]string{}
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, "library", n))
		if err != nil {
			return nil, err
		}
		files[n] = string(b)
	}
	return parseLibrary(files), nil
}

// parseLibrary reads the Tags and SharedTags lines of official-images library files.
func parseLibrary(files map[string]string) *imageLibrary {
	l := &imageLibrary{tags: map[string]map[string]bool{}, codenames: map[string]bool{}}
	for name, text := range files {
		l.tags[name] = map[string]bool{}
		for _, line := range strings.Split(text, "\n") {
			k, v, ok := strings.Cut(line, ":")
			if !ok || (k != "Tags" && k != "SharedTags") {
				continue
			}
			var tags []string
			for _, t := range strings.Split(v, ",") {
				if t = strings.TrimSpace(t); t != "" {
					tags = append(tags, t)
					l.tags[name][t] = true
				}
			}
			if k != "Tags" || len(tags) == 0 {
				continue
			}
			latest := false
			for _, t := range tags {
				latest = latest || t == "latest"
			}
			switch {
			case name == "debian" && regexp.MustCompile(`^[a-z]+$`).MatchString(tags[0]):
				l.codenames[tags[0]] = true
				if latest {
					l.debianLatest = tags[0]
				}
			case name == "ubuntu" && latest:
				l.ubuntuLatest = tags[0]
			}
		}
	}
	return l
}

// judge measures one image reference: the newest equivalent tag, a status and a note.
func (l *imageLibrary) judge(ref string) (latest, status, note string) {
	name, tag, _ := strings.Cut(ref, ":")
	published := l.tags[name][tag]
	if name == "ubuntu" {
		if l.ubuntuLatest == "" {
			return "", "unknown", "Ubuntu's latest tag was not found in the official-images library."
		}
		latest = "ubuntu:" + l.ubuntuLatest
		switch {
		case !published:
			return latest, "major", "This tag is no longer published."
		case tag == l.ubuntuLatest:
			return latest, "current", ""
		}
		return latest, "major", "A newer Ubuntu LTS is Docker's latest."
	}
	suite := ""
	for _, part := range strings.Split(tag, "-") {
		if l.codenames[part] {
			suite = part
		}
	}
	if !published {
		return "", "major", "This tag is no longer published."
	}
	if suite == "" || l.debianLatest == "" || suite == l.debianLatest {
		return ref, "current", ""
	}
	newer := strings.Replace(tag, suite, l.debianLatest, 1)
	if !l.tags[name][newer] {
		return ref, "current", "Built on Debian " + suite + "; no " + l.debianLatest + " variant of this tag is published yet."
	}
	return name + ":" + newer, "major", "Built on Debian " + suite + "; Debian's newest release is " + l.debianLatest + "."
}

// vendorAPIs are the versions of the vendor APIs and protocols Drydock speaks, read from the code that sends them. Where the
// vendor publishes its versions somewhere a program can read (the MCP specification's dated tags, the Linear SDK on npm), the
// newest is measured; otherwise the row says where to look.
func vendorAPIs(ctx context.Context) []Item {
	group := "Vendor APIs and protocols"
	var items []Item
	mcp := Item{ID: "api:mcp", Group: group, Name: "Model Context Protocol (revision Drydock speaks)", Kind: "api",
		Used: pinnedIn("internal/mcp/protocol.go", `protocolNew = "([0-9-]+)"`), Where: "internal/mcp/protocol.go",
		Role: "The MCP gateway and the hosted MCP servers Drydock connects to."}
	if tags, err := lsRemoteTags(ctx, "modelcontextprotocol/modelcontextprotocol"); err != nil {
		fail("tags of modelcontextprotocol/modelcontextprotocol: " + err.Error())
	} else {
		mcp.Latest = newestDated(tags)
		mcp.Status = datedStatus(mcp.Used, mcp.Latest)
	}
	items = append(items, mcp)

	linear := Item{ID: "api:linear", Group: group, Name: "Linear GraphQL API (schema of @linear/sdk)", Kind: "api",
		Used: pinnedIn("internal/integrations/catalog.go", `@linear/sdk (\d+\.\d+\.\d+)`), Where: "internal/integrations/catalog.go",
		Role: "Reading and writing Linear issues; the API is unversioned, so Drydock follows the schema of Linear's own SDK."}
	linear.Latest = npmLatest(ctx, "@linear/sdk")
	items = append(items, linear)

	for _, a := range []struct{ id, name, file, expr, where string }{
		{"api:notion", "Notion API (Notion-Version header)", "internal/integrations/notion.go", `NotionVersion = "([0-9-]+)"`, "https://developers.notion.com/page/changelog"},
		{"api:github", "GitHub REST API (X-GitHub-Api-Version header)", "internal/git/host/host.go", `X-GitHub-Api-Version: ([0-9-]+)`, "https://docs.github.com/rest/about-the-rest-api/api-versions"},
		{"api:anthropic", "Anthropic API (anthropic-version header of the provider probe)", "internal/providers/probe.go", `"anthropic-version", "([0-9-]+)"`, "https://docs.anthropic.com/en/api/versioning"},
	} {
		items = append(items, Item{ID: a.id, Group: group, Name: a.name, Kind: "api", Used: pinnedIn(a.file, a.expr), Where: a.file,
			Note: "The vendor lists its versions only on a web page (" + a.where + "): check it by hand."})
	}
	return items
}

var datedR = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// newestDated picks the newest YYYY-MM-DD tag (release candidates, "-RC", are left out).
func newestDated(tags []string) string {
	best := ""
	for _, t := range tags {
		if datedR.MatchString(t) && t > best {
			best = t
		}
	}
	return best
}

func datedStatus(used, latest string) string {
	switch {
	case latest == "":
		return "unknown"
	case used >= latest:
		return "current"
	}
	return "minor"
}

// ciInstall finds what a workflow installs by version: npm install -g a@1 b@2, npx -y pkg@1, go install or go run pkg@v1.
var (
	npmGlobalR = regexp.MustCompile(`npm (?:install|i) -g ([^\n]+)`)
	npxR       = regexp.MustCompile(`npx -y ((?:@[\w.-]+/)?[\w.-]+@[\w.-]+)`)
	goToolR    = regexp.MustCompile(`go (?:install|run) ([\w.-]+(?:/[\w.-]+)+@v[\w.-]+)`)
)

// ciInstalls are the programs CI workflows install at a version, against their newest release; one that differs from the
// version Drydock's manifests pin says so. The Phase 0 probes follow the newest releases too (2026-10-20, the owner's
// instruction): a push that changes their workflow runs them again on Windows.
func ciInstalls(ctx context.Context) []Item {
	ps, _ := pins.Embedded()
	pinned := map[string]string{}
	for _, p := range ps {
		pinned[p.Package] = p.Version
	}
	files, _ := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	sort.Strings(files)
	var items []Item
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			fail(f + ": " + err.Error())
			continue
		}
		wf := filepath.Base(f)
		for _, spec := range workflowInstalls(string(b)) {
			pkg, ver, goPkg := spec.pkg, spec.ver, spec.goPkg
			it := Item{ID: "ci:" + wf + ":" + pkg, Group: "Installed by CI workflows", Name: pkg + " (" + wf + ")", Kind: "ci", Used: strings.TrimPrefix(ver, "v"),
				Where: ".github/workflows/" + wf}
			if goPkg {
				it.Latest, it.LatestDate = goLatest(ctx, goModule(pkg))
			} else {
				it.Latest = npmLatest(ctx, pkg)
				if want, ok := pinned[pkg]; ok && want != it.Used {
					it.Note = "Drydock's manifests pin " + want + "."
				}
			}
			items = append(items, it)
		}
	}
	return items
}

type installSpec struct {
	pkg, ver string
	goPkg    bool
}

// workflowInstalls lists the versioned installs of a workflow file, each once.
func workflowInstalls(text string) []installSpec {
	var out []installSpec
	seen := map[string]bool{}
	add := func(spec string, goPkg bool) {
		i := strings.LastIndex(spec, "@")
		if i <= 0 || strings.Contains(spec, "${{") {
			return
		}
		s := installSpec{pkg: spec[:i], ver: spec[i+1:], goPkg: goPkg}
		if s.ver == "" || s.ver == "latest" || seen[s.pkg+"@"+s.ver] {
			return
		}
		seen[s.pkg+"@"+s.ver] = true
		out = append(out, s)
	}
	for _, m := range npmGlobalR.FindAllStringSubmatch(text, -1) {
		if strings.Contains(m[1], "${{") {
			continue // a line built from workflow expressions names no fixed version
		}
		for _, tok := range strings.Fields(m[1]) {
			if !strings.HasPrefix(tok, "-") && strings.Contains(strings.TrimPrefix(tok, "@"), "@") {
				add(tok, false)
			}
		}
	}
	for _, m := range npxR.FindAllStringSubmatch(text, -1) {
		add(m[1], false)
	}
	for _, m := range goToolR.FindAllStringSubmatch(text, -1) {
		add(m[1], true)
	}
	return out
}

// goModule is the module a package path belongs to, by the hosts' layout: github.com/owner/repo (plus a /vN), or
// golang.org/x/name.
func goModule(pkg string) string {
	parts := strings.Split(pkg, "/")
	if len(parts) < 3 {
		return pkg
	}
	mod := parts[:3]
	if len(parts) > 3 && regexp.MustCompile(`^v\d+$`).MatchString(parts[3]) {
		mod = parts[:4]
	}
	return strings.Join(mod, "/")
}

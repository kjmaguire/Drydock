// Package libver reads which versions of well-known SDKs and frameworks a repository uses, from its manifests and
// lockfiles, so the agents write code for those versions and ask documentation (Context7, Microsoft Learn, the vendor's
// site) for them instead of the latest (decision P54). It reads files only: it runs no package manager, follows no
// symbolic link and contacts nothing.
//
// Only libraries on a curated list (curated.go) are reported: vendor SDKs and frameworks whose API changes between major
// versions, where writing for the wrong version is a real mistake. Every other library's version stays in the files the
// report names. A name or version that does not look like one is dropped, so a manifest cannot put text of its own into
// the agents' instructions.
package libver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kjmaguire/drydock/internal/safefs"
)

// Ecosystems.
const (
	NPM     = "npm"
	Go      = "go"
	NuGet   = "nuget"
	PyPI    = "pypi"
	maxRead = 32 << 20 // a lockfile larger than this is not read (its manifest's ranges are reported instead)
)

// Lib is one library a manifest depends on directly.
type Lib struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Exact is true when Version is the resolved version (a lockfile, a pinned requirement, go.mod); false when it is only
	// the range the manifest asks for.
	Exact bool `json:"exact"`
}

// Manifest is what one manifest file contributes.
type Manifest struct {
	Path      string `json:"path"`           // repository-relative, forward slashes
	Ecosystem string `json:"ecosystem"`      // npm, go, nuget, pypi
	Lock      string `json:"lock,omitempty"` // repository-relative lockfile the exact versions came from
	Libs      []Lib  `json:"libs"`
}

// Report is the result of a scan.
type Report struct {
	Manifests []Manifest `json:"manifests"`
	// Omitted counts curated libraries left out because the report reached Options.MaxLibs.
	Omitted int `json:"omitted,omitempty"`
}

// Libs is the number of libraries in the report.
func (r Report) Libs() int {
	n := 0
	for _, m := range r.Manifests {
		n += len(m.Libs)
	}
	return n
}

// Options bound a scan. Zero values take the defaults.
type Options struct {
	MaxDepth     int // directory levels below the root searched for manifests (default 3)
	MaxManifests int // manifests read (default 24)
	MaxLibs      int // libraries reported in all (default 40)
	MaxEntries   int // directory entries visited (default 20000)
}

func (o Options) withDefaults() Options {
	if o.MaxDepth <= 0 {
		o.MaxDepth = 3
	}
	if o.MaxManifests <= 0 {
		o.MaxManifests = 24
	}
	if o.MaxLibs <= 0 {
		o.MaxLibs = 40
	}
	if o.MaxEntries <= 0 {
		o.MaxEntries = 20000
	}
	return o
}

// skipDirs are never searched: installed packages, build output, tool state and test fixtures.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true, "out": true, "target": true, "bin": true,
	"obj": true, ".venv": true, "venv": true, "env": true, "__pycache__": true, ".next": true, ".nuxt": true, ".svelte-kit": true,
	".turbo": true, ".cache": true, "coverage": true, ".claude": true, ".aet": true, ".agents": true, "testdata": true,
	"fixtures": true, "__fixtures__": true, "examples": true, "packages-cache": true, ".yarn": true, ".pnpm-store": true,
}

var errStop = errors.New("stop")

// Scan reads the manifests under root. A manifest that cannot be read or parsed is skipped; Scan fails only when root
// itself cannot be read.
func Scan(root string, o Options) (Report, error) {
	o = o.withDefaults()
	if _, err := os.Stat(root); err != nil {
		return Report{}, err
	}
	var found []string
	visited := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil // an unreadable folder is skipped
		}
		visited++
		if visited > o.MaxEntries {
			return errStop
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if p == root {
				return nil
			}
			if skipDirs[d.Name()] || strings.Count(rel, "/")+1 > o.MaxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() { // a symbolic link is never followed
			return nil
		}
		if manifestKind(d.Name()) != "" {
			found = append(found, rel)
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		return Report{}, err
	}
	sort.Slice(found, func(i, j int) bool { // the root's manifests first, then by path
		di, dj := strings.Count(found[i], "/"), strings.Count(found[j], "/")
		if di != dj {
			return di < dj
		}
		return found[i] < found[j]
	})
	var r Report
	left := o.MaxLibs
	for i, rel := range found {
		if i == o.MaxManifests {
			break
		}
		m, ok := readManifest(root, rel)
		if !ok || len(m.Libs) == 0 {
			continue
		}
		if len(m.Libs) > left {
			r.Omitted += len(m.Libs) - left
			m.Libs = m.Libs[:left]
		}
		left -= len(m.Libs)
		if len(m.Libs) > 0 {
			r.Manifests = append(r.Manifests, m)
		}
	}
	return r, nil
}

func manifestKind(name string) string {
	switch {
	case name == "package.json":
		return NPM
	case name == "go.mod":
		return Go
	case strings.HasSuffix(name, ".csproj"), strings.HasSuffix(name, ".fsproj"), strings.HasSuffix(name, ".vbproj"):
		return NuGet
	case name == "requirements.txt", name == "pyproject.toml":
		return PyPI
	}
	return ""
}

func readManifest(root, rel string) (Manifest, bool) {
	data, ok := readFile(root, rel)
	if !ok {
		return Manifest{}, false
	}
	m := Manifest{Path: rel, Ecosystem: manifestKind(path.Base(rel))}
	switch m.Ecosystem {
	case NPM:
		m.Lock, m.Libs = readNPM(root, rel, data)
	case Go:
		m.Libs = readGoMod(data)
	case NuGet:
		m.Libs = readMSBuild(root, rel, data)
	case PyPI:
		m.Lock, m.Libs = readPython(root, rel, data)
	}
	var keep []Lib
	seen := map[string]bool{}
	for _, l := range m.Libs {
		if !validName(m.Ecosystem, l.Name) || !validVersion.MatchString(l.Version) || !Curated(m.Ecosystem, l.Name) || seen[l.Name] {
			continue
		}
		seen[l.Name] = true
		keep = append(keep, l)
	}
	sort.SliceStable(keep, func(i, j int) bool { return strings.ToLower(keep[i].Name) < strings.ToLower(keep[j].Name) })
	m.Libs = keep
	if !anyExact(keep) {
		m.Lock = ""
	}
	return m, true
}

func anyExact(ls []Lib) bool {
	for _, l := range ls {
		if l.Exact {
			return true
		}
	}
	return false
}

// readFile reads a repository-relative regular file up to maxRead bytes. A path that leaves the repository through a symbolic
// link or junction (the worktree is the agent's to write) is not read.
func readFile(root, rel string) ([]byte, bool) {
	b, err := safefs.ReadFile(root, rel, maxRead+1)
	if err != nil || len(b) > maxRead {
		return nil, false
	}
	return b, true
}

// upward lists rel's directory and its parents up to the root ("" is the root), nearest first.
func upward(rel string) []string {
	dir := path.Dir(rel)
	var out []string
	for {
		if dir == "." {
			out = append(out, "")
			return out
		}
		out = append(out, dir)
		dir = path.Dir(dir)
	}
}

func join(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// ---- npm ----

func readNPM(root, rel string, data []byte) (string, []Lib) {
	var pj struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
	}
	if json.Unmarshal(data, &pj) != nil {
		return "", nil
	}
	want := map[string]string{}
	for _, deps := range []map[string]string{pj.PeerDependencies, pj.OptionalDependencies, pj.DevDependencies, pj.Dependencies} {
		for k, v := range deps {
			want[k] = v // dependencies win over the others
		}
	}
	if len(want) == 0 {
		return "", nil
	}
	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	lock, exact := "", map[string]string{}
	for _, d := range upward(rel) {
		if b, ok := readFile(root, join(d, "package-lock.json")); ok {
			lock, exact = join(d, "package-lock.json"), npmLock(b, strings.TrimPrefix(strings.TrimPrefix(dir, d), "/"))
			break
		}
		if b, ok := readFile(root, join(d, "pnpm-lock.yaml")); ok {
			lock, exact = join(d, "pnpm-lock.yaml"), pnpmLock(b, strings.TrimPrefix(strings.TrimPrefix(dir, d), "/"))
			break
		}
		if b, ok := readFile(root, join(d, "yarn.lock")); ok {
			lock, exact = join(d, "yarn.lock"), yarnLock(b, want)
			break
		}
	}
	var out []Lib
	for name, spec := range want {
		if v, ok := exact[name]; ok && v != "" {
			out = append(out, Lib{Name: name, Version: v, Exact: true})
			continue
		}
		spec = strings.TrimSpace(spec)
		if strings.HasPrefix(spec, "workspace:") || strings.Contains(spec, ":") || strings.Contains(spec, "/") {
			continue // a workspace link, git URL, file path or alias says nothing about the published version
		}
		out = append(out, Lib{Name: name, Version: strings.ReplaceAll(spec, " ", ""), Exact: isExactSemver(spec)})
	}
	return lock, out
}

var semverRe = regexp.MustCompile(`^v?\d+\.\d+\.\d+([-+][0-9A-Za-z.+-]+)?$`)

func isExactSemver(s string) bool { return semverRe.MatchString(strings.TrimSpace(s)) }

// npmLock reads package-lock.json (lockfileVersion 2 and 3): the installed version of each package for the workspace
// at sub ("" for the lockfile's own folder), falling back to the hoisted one.
func npmLock(b []byte, sub string) map[string]string {
	var lf struct {
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"` // lockfileVersion 1
	}
	if json.Unmarshal(b, &lf) != nil {
		return nil
	}
	out := map[string]string{}
	for key, p := range lf.Packages {
		name, ok := strings.CutPrefix(key, "node_modules/")
		if !ok || strings.Contains(strings.TrimPrefix(name, "@"), "/node_modules/") {
			continue
		}
		out[name] = p.Version
	}
	if sub != "" {
		for key, p := range lf.Packages {
			if name, ok := strings.CutPrefix(key, sub+"/node_modules/"); ok && !strings.Contains(name, "/node_modules/") {
				out[name] = p.Version // the workspace's own copy wins over the hoisted one
			}
		}
	}
	for name, d := range lf.Dependencies {
		if _, ok := out[name]; !ok {
			out[name] = d.Version
		}
	}
	return out
}

// pnpmLock reads pnpm-lock.yaml (lockfileVersion 6 and 9): importers.<sub or ".">.{dependencies,devDependencies}.<name>.
func pnpmLock(b []byte, sub string) map[string]string {
	var lf struct {
		Importers map[string]map[string]map[string]yaml.Node `yaml:"importers"`
	}
	if yaml.Unmarshal(b, &lf) != nil {
		return nil
	}
	key := sub
	if key == "" {
		key = "."
	}
	imp, ok := lf.Importers[key]
	if !ok {
		return nil
	}
	out := map[string]string{}
	for _, group := range []string{"optionalDependencies", "devDependencies", "dependencies"} {
		for name, n := range imp[group] {
			v := ""
			switch n.Kind {
			case yaml.ScalarNode: // lockfileVersion 5: the version itself
				v = n.Value
			case yaml.MappingNode:
				var e struct {
					Version string `yaml:"version"`
				}
				if n.Decode(&e) == nil {
					v = e.Version
				}
			}
			if i := strings.IndexByte(v, '('); i > 0 { // 15.4.0(react@19.0.0): peer suffix
				v = v[:i]
			}
			if isExactSemver(v) {
				out[name] = v
			}
		}
	}
	return out
}

// yarnLock reads yarn.lock (classic and Berry): the entry whose key lists name@spec for the manifest's own spec.
func yarnLock(b []byte, want map[string]string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var keys []string
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && strings.HasSuffix(line, ":") {
			keys = nil
			for _, k := range strings.Split(strings.TrimSuffix(line, ":"), ",") {
				keys = append(keys, strings.Trim(strings.TrimSpace(k), `"`))
			}
			continue
		}
		t := strings.TrimSpace(line)
		var v string
		if s, ok := strings.CutPrefix(t, "version: "); ok {
			v = s
		} else if s, ok := strings.CutPrefix(t, "version "); ok {
			v = s
		} else {
			continue
		}
		v = strings.Trim(v, `"`)
		for _, k := range keys {
			at := strings.LastIndex(k, "@")
			if at <= 0 {
				continue
			}
			name, spec := k[:at], strings.TrimPrefix(k[at+1:], "npm:")
			if w, ok := want[name]; ok && w == spec {
				out[name] = v
			}
		}
		keys = nil
	}
	return out
}

// ---- Go ----

// readGoMod reads the require directives of go.mod; requirements marked // indirect are not the module's own choice.
func readGoMod(b []byte) []Lib {
	var out []Lib
	in := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		indirect := strings.Contains(line, "// indirect")
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		switch {
		case line == "require (":
			in = true
			continue
		case in && line == ")":
			in = false
			continue
		case strings.HasPrefix(line, "require "):
			line = strings.TrimSpace(strings.TrimPrefix(line, "require "))
		case !in:
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 || indirect {
			continue
		}
		out = append(out, Lib{Name: f[0], Version: f[1], Exact: true})
	}
	return out
}

// ---- .NET ----

type msbuildItem struct {
	XMLName xml.Name
	Include string `xml:"Include,attr"`
	Update  string `xml:"Update,attr"`
	Version string `xml:"Version,attr"`
	VerElem string `xml:"Version"`
}

func msbuildItems(b []byte, element string) []msbuildItem {
	var out []msbuildItem
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	for {
		tok, err := d.Token()
		if err != nil {
			return out
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != element {
			continue
		}
		var it msbuildItem
		if d.DecodeElement(&it, &se) == nil {
			out = append(out, it)
		}
	}
}

// readMSBuild reads PackageReference items; a reference without a version takes it from the nearest
// Directory.Packages.props (central package management).
func readMSBuild(root, rel string, b []byte) []Lib {
	refs := msbuildItems(b, "PackageReference")
	if len(refs) == 0 {
		return nil
	}
	central := map[string]string{}
	for _, d := range upward(rel) {
		if pb, ok := readFile(root, join(d, "Directory.Packages.props")); ok {
			for _, it := range msbuildItems(pb, "PackageVersion") {
				name := firstNonEmpty(it.Include, it.Update)
				central[strings.ToLower(name)] = strings.TrimSpace(firstNonEmpty(it.Version, it.VerElem))
			}
			break
		}
	}
	var out []Lib
	for _, it := range refs {
		name := firstNonEmpty(it.Include, it.Update)
		v := strings.TrimSpace(firstNonEmpty(it.Version, it.VerElem))
		if v == "" {
			v = central[strings.ToLower(name)]
		}
		if name == "" || v == "" || strings.Contains(v, "$(") {
			continue
		}
		out = append(out, Lib{Name: name, Version: v, Exact: !strings.ContainsAny(v, "[]()*,")})
	}
	return out
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// ---- Python ----

var pyReq = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)(\[[^\]]*\])?\s*((?:==|~=|>=|<=|!=|>|<)\s*[^;#\s]+(?:\s*,\s*(?:==|~=|>=|<=|!=|>|<)\s*[^;#\s,]+)*)?`)

func parsePyReq(s string) (Lib, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "-") {
		return Lib{}, false
	}
	m := pyReq.FindStringSubmatch(s)
	if m == nil || m[3] == "" {
		return Lib{}, false // a requirement without a version says nothing
	}
	v := strings.ReplaceAll(m[3], " ", "")
	if exact, ok := strings.CutPrefix(v, "=="); ok && !strings.ContainsAny(exact, ",*") {
		return Lib{Name: m[1], Version: exact, Exact: true}, true
	}
	return Lib{Name: m[1], Version: v}, true
}

var pySep = regexp.MustCompile(`[-_.]+`)

func pyNorm(name string) string { return strings.ToLower(pySep.ReplaceAllString(name, "-")) }

// readPython reads requirements.txt or pyproject.toml ([project] dependencies, [tool.poetry.dependencies]); exact versions
// come from an uv.lock or poetry.lock next to it or above it.
func readPython(root, rel string, b []byte) (string, []Lib) {
	var out []Lib
	if path.Base(rel) == "requirements.txt" {
		for _, line := range strings.Split(string(b), "\n") {
			if l, ok := parsePyReq(line); ok {
				out = append(out, l)
			}
		}
	} else {
		out = pyproject(string(b))
	}
	if len(out) == 0 {
		return "", nil
	}
	for _, d := range upward(rel) {
		for _, name := range []string{"uv.lock", "poetry.lock"} {
			lb, ok := readFile(root, join(d, name))
			if !ok {
				continue
			}
			locked := tomlPackages(string(lb))
			for i, l := range out {
				if v, ok := locked[pyNorm(l.Name)]; ok && !l.Exact {
					out[i].Version, out[i].Exact = v, true
				}
			}
			return join(d, name), out
		}
	}
	return "", out
}

// pyproject reads the dependency strings of [project] dependencies = [...] and the name = "version" lines of
// [tool.poetry.dependencies], without a TOML library: both are plain lists of strings in practice.
func pyproject(s string) []Lib {
	var out []Lib
	section := ""
	inDeps := false
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") && !inDeps {
			section = strings.Trim(line, "[] ")
			continue
		}
		switch {
		case section == "project" && strings.HasPrefix(line, "dependencies") && strings.Contains(line, "["):
			inDeps = true
			line = line[strings.Index(line, "[")+1:]
			fallthrough
		case inDeps:
			for _, q := range quoted.FindAllStringSubmatch(line, -1) {
				if l, ok := parsePyReq(q[1]); ok {
					out = append(out, l)
				}
			}
			if strings.Contains(line, "]") && !strings.Contains(line, "[") {
				inDeps = false
			}
		case section == "tool.poetry.dependencies":
			name, val, ok := strings.Cut(line, "=")
			name = strings.TrimSpace(name)
			if !ok || strings.EqualFold(name, "python") {
				continue
			}
			if q := quoted.FindStringSubmatch(val); q != nil && strings.HasPrefix(strings.TrimSpace(val), `"`) {
				v := strings.ReplaceAll(q[1], " ", "")
				out = append(out, Lib{Name: name, Version: v, Exact: semverRe.MatchString(v)})
			}
		}
	}
	return out
}

var quoted = regexp.MustCompile(`"([^"]*)"`)

// tomlPackages reads the name and version of each [[package]] of uv.lock or poetry.lock.
func tomlPackages(s string) map[string]string {
	out := map[string]string{}
	name := ""
	in := false
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") {
			in, name = line == "[[package]]", ""
			continue
		}
		if !in {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"`)
		switch k {
		case "name":
			name = pyNorm(v)
		case "version":
			if name != "" {
				out[name] = v
			}
		}
	}
	return out
}

// ---- validation ----

var (
	validVersion = regexp.MustCompile(`^[0-9A-Za-z.+_^~<>=*!,|\[\]()-]{1,64}$`)
	npmName      = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._~-]*/)?[a-z0-9][a-z0-9._~-]*$`)
	goPath       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~/-]*$`)
	dottedName   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

func validName(eco, name string) bool {
	if len(name) == 0 || len(name) > 200 {
		return false
	}
	switch eco {
	case NPM:
		return npmName.MatchString(name)
	case Go:
		return goPath.MatchString(name) && !strings.Contains(name, "..")
	default:
		return dottedName.MatchString(name)
	}
}

// ---- rendering ----

// Section is the AGENTS.md text of a report, or "" when it found nothing.
func (r Report) Section() string {
	if len(r.Manifests) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("This repository uses these versions of the SDKs and frameworks below, read from its manifests and lockfiles. ")
	b.WriteString("Write code for these versions, and when you look up a library's documentation (Context7, Microsoft Learn or the vendor's site) ")
	b.WriteString("ask for this version, not the latest. Any other library's version is in the same files.\n\n")
	for _, m := range r.Manifests {
		var libs []string
		for _, l := range m.Libs {
			s := l.Name + " " + l.Version
			if !l.Exact {
				s += " (range)"
			}
			libs = append(libs, s)
		}
		from := ""
		if m.Lock != "" {
			from = fmt.Sprintf(" (exact versions from `%s`)", m.Lock)
		}
		fmt.Fprintf(&b, "- `%s`%s: %s\n", m.Path, from, strings.Join(libs, ", "))
	}
	if r.Omitted > 0 {
		fmt.Fprintf(&b, "- and %d more in these files\n", r.Omitted)
	}
	return b.String()
}

// Line is the report as one line per manifest without the instructions, for prompts that only need the facts.
func (r Report) Line() string {
	var parts []string
	for _, m := range r.Manifests {
		var libs []string
		for _, l := range m.Libs {
			libs = append(libs, l.Name+" "+l.Version)
		}
		parts = append(parts, m.Path+": "+strings.Join(libs, ", "))
	}
	return strings.Join(parts, "; ")
}

// Sources lists the manifest and lockfile paths the report read, for a context manifest entry.
func (r Report) Sources() []string {
	var out []string
	for _, m := range r.Manifests {
		out = append(out, m.Path)
		if m.Lock != "" {
			out = append(out, m.Lock)
		}
	}
	return out
}

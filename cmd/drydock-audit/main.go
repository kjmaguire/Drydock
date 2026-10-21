// Command drydock-audit measures every piece of third-party software Drydock uses against the newest release and writes
// docs/software-versions.json. It reads what the repository really pins (go.mod, ui/package-lock.json, the GitHub workflows,
// the agent manifests) and asks the registries what is newest, so the result is a measurement, not a list somebody keeps by hand.
// The hand-written part is docs/software-notes.json: what each piece is for and why it is held back, if it is.
//
//	go run ./cmd/drydock-audit            # network lookups, writes docs/software-versions.json
//	go run ./cmd/drydock-audit -vuln=false # skip the vulnerability scans (faster)
//	go run ./cmd/drydock-audit -vulndb https://storage.googleapis.com/go-vulndb   # where vuln.go.dev is not reachable
//
// The report is written even when a lookup failed (it lists them under lookupsFailed and a package with no answer is "unknown",
// never "current"), and the exit status is 2 then, so an incomplete measurement is not mistaken for a complete one.
//
// The page published as the "Software versions" Artifact is generated from that JSON file (docs/software-versions.md).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Item is one piece of software.
type Item struct {
	ID     string `json:"id"`
	Group  string `json:"group"`
	Name   string `json:"name"`
	Kind   string `json:"kind"` // direct | indirect | dev | runtime | tool | action | cli | bridge
	Used   string `json:"used"`
	Latest string `json:"latest,omitempty"`
	// Status: current | patch | minor | major | prerelease | unknown
	Status     string `json:"status"`
	UsedDate   string `json:"usedDate,omitempty"`
	LatestDate string `json:"latestDate,omitempty"`
	Where      string `json:"where,omitempty"`
	Role       string `json:"role,omitempty"`
	Hold       string `json:"hold,omitempty"` // why it is deliberately not on the newest version
	Deprecated string `json:"deprecated,omitempty"`
	Note       string `json:"note,omitempty"`
	// UsedBy is how many product packages import a Go library (Linux and Windows builds together); -1 when not measured.
	UsedBy *int `json:"usedBy,omitempty"`
	// InBuild says whether any package of a Go library is compiled into Drydock or its tests (Linux and Windows builds together);
	// a library that is only in the module graph does not run in the app.
	InBuild *bool `json:"inBuild,omitempty"`
}

// Vuln is one known vulnerability that affects what is installed.
type Vuln struct {
	Source   string `json:"source"` // govulncheck | npm audit
	ID       string `json:"id"`
	Package  string `json:"package"`
	Severity string `json:"severity,omitempty"`
	Fixed    string `json:"fixed,omitempty"`
	Called   bool   `json:"called,omitempty"` // govulncheck: the code actually reaches the vulnerable function
	Summary  string `json:"summary,omitempty"`
}

// Report is docs/software-versions.json.
type Report struct {
	Generated string            `json:"generated"`
	Commit    string            `json:"commit"`
	Offline   []string          `json:"lookupsFailed,omitempty"`
	Summary   map[string]int    `json:"summary"`
	Items     []Item            `json:"items"`
	Vulns     []Vuln            `json:"vulnerabilities"`
	VulnScans map[string]string `json:"vulnerabilityScans"`
	Counts    map[string]int    `json:"counts"`
	Docs      []DocDrift        `json:"docDrift,omitempty"`
}

// DocDrift is a place where the documentation names a different version than the code pins.
type DocDrift struct {
	Name string `json:"name"`
	Docs string `json:"docs"`
	Code string `json:"code"`
	Ref  string `json:"ref"`
}

// Notes is docs/software-notes.json (hand-written).
type Notes struct {
	Notes map[string]struct {
		Role string `json:"role"`
		Hold string `json:"hold,omitempty"`
		Note string `json:"note,omitempty"`
	} `json:"notes"`
}

var (
	root   string
	failMu sync.Mutex
	failed []string
)

func fail(what string) {
	failMu.Lock()
	failed = append(failed, what)
	failMu.Unlock()
}

// run returns what the program wrote to its standard output. A program that cannot be started, or that ends with a failure code,
// is an error even when it wrote something first: half an answer is not an answer, and it used to be taken for one, so a
// scanner that died part-way reported what it had found so far as everything (audit 2026-10-16, L51).
func run(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	return runAllowing(ctx, dir, env, nil, name, args...)
}

// runAllowing is run for a program that reports what it found with its exit code: the codes in ok are not errors (npm outdated
// and npm audit exit 1 when there is something to report, and still print their answer).
func runAllowing(ctx context.Context, dir string, env []string, ok []int, name string, args ...string) (string, error) {
	out, code, err := runCode(ctx, dir, env, name, args...)
	if err != nil && slices.Contains(ok, code) {
		return out, nil
	}
	return out, err
}

// runCode runs a program and returns what it wrote to its standard output and its exit code, which is -1 when it did not get as
// far as exiting (it could not be started, or was killed). The error is non-nil for every failure, a failure code included; its
// text carries what the program wrote to its standard error.
func runCode(ctx context.Context, dir string, env []string, name string, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err == nil {
		return out.String(), 0, nil
	}
	code := -1
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	}
	return out.String(), code, fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
}

var versionR = regexp.MustCompile(`^(?:go)?v?(\d+)\.(\d+)(?:\.(\d+))?(.*)$`)

// parseSemver reads 1.2.3, v1.2.3, go1.26 and go1.27rc2. pre is true for release candidates and pre-release suffixes.
func parseSemver(v string) (maj, min, pat int, pre bool, ok bool) {
	m := versionR.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return 0, 0, 0, false, false
	}
	maj, _ = strconv.Atoi(m[1])
	min, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		pat, _ = strconv.Atoi(m[3])
	}
	rest := m[4]
	pre = strings.HasPrefix(rest, "-") || strings.HasPrefix(rest, "rc") || strings.HasPrefix(rest, "beta") || strings.HasPrefix(rest, "alpha")
	return maj, min, pat, pre, true
}

// classify says how far used is behind latest.
func classify(used, latest string) string {
	if latest == "" {
		return "unknown"
	}
	if ub, lb, ok := buildNumbers(used, latest); ok { // bNNNN builds (llama.cpp): behind by builds counts as a patch
		switch {
		case lb > ub:
			return "patch"
		case lb == ub:
			return "current"
		}
		return "unknown"
	}
	if fu, fl := forkReleaseR.FindStringSubmatch(used), forkReleaseR.FindStringSubmatch(latest); fu != nil && fl != nil {
		// the TurboQuant fork's bNNNN-X.Y.Z: its own version says how far behind; a newer build of the same version is a patch
		if st := classify(fu[2], fl[2]); st != "current" {
			return st
		}
		ub, _ := strconv.Atoi(fu[1])
		lb, _ := strconv.Atoi(fl[1])
		if lb > ub {
			return "patch"
		}
		return "current"
	}
	um, un, up, upre, ok1 := parseSemver(used)
	lm, ln, lp, _, ok2 := parseSemver(latest)
	if !ok1 || !ok2 {
		if used == latest {
			return "current"
		}
		return "unknown"
	}
	switch {
	case lm > um:
		return "major"
	case lm == um && ln > un:
		return "minor"
	case lm == um && ln == un && lp > up:
		return "patch"
	case upre && used != latest:
		return "prerelease"
	}
	return "current"
}

// buildNumbers reads two bNNNN build tags.
func buildNumbers(a, b string) (int, int, bool) {
	m1, m2 := buildTagR.FindStringSubmatch(a), buildTagR.FindStringSubmatch(b)
	if m1 == nil || m2 == nil {
		return 0, 0, false
	}
	n1, _ := strconv.Atoi(m1[1])
	n2, _ := strconv.Atoi(m2[1])
	return n1, n2, true
}

func day(t string) string {
	if len(t) >= 10 {
		return t[:10]
	}
	return t
}

func main() {
	vuln := flag.Bool("vuln", true, "run govulncheck and npm audit")
	flag.StringVar(&vulnDB, "vulndb", os.Getenv("DRYDOCK_AUDIT_VULNDB"), "the vulnerability database govulncheck reads (default: its own, https://vuln.go.dev; the same data is at https://storage.googleapis.com/go-vulndb)")
	out := flag.String("out", "docs/software-versions.json", "output file, relative to the repository root")
	artifactDir := flag.String("artifact-dir", "", "also write the dataset files for the Software Versions Artifact (items, vulns, drift, meta) into this directory")
	flag.Parse()
	var err error
	if root, err = repoRoot(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	var notes Notes
	if b, err := os.ReadFile(filepath.Join(root, "docs", "software-notes.json")); err == nil {
		if err := json.Unmarshal(b, &notes); err != nil {
			fmt.Fprintln(os.Stderr, "docs/software-notes.json:", err)
			os.Exit(1)
		}
	}

	var items []Item
	items = append(items, goToolchain(ctx)...)
	items = append(items, goModules(ctx)...)
	markGoUsage(ctx, items)
	items = append(items, npmPackages(ctx)...)
	items = append(items, actions(ctx)...)
	items = append(items, agentSoftware(ctx)...)
	items = append(items, tools(ctx)...)
	items = append(items, extras(ctx)...)

	missing := []string{}
	for i := range items {
		it := &items[i]
		if n, ok := notes.Notes[it.ID]; ok {
			it.Role, it.Hold = n.Role, n.Hold
			it.Note = withNote(n.Note, it.Note)
		} else if it.Kind == "direct" || it.Kind == "dev" || it.Kind == "runtime" || it.Kind == "tool" || it.Kind == "action" || it.Kind == "cli" || it.Kind == "bridge" {
			missing = append(missing, it.ID)
		}
		if it.Status == "" {
			it.Status = classify(it.Used, it.Latest)
		}
	}
	markGraphOnly(items)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Group < items[j].Group })

	rep := Report{Generated: time.Now().UTC().Format(time.RFC3339), Items: items, VulnScans: map[string]string{}, Vulns: []Vuln{}}
	if c, err := run(ctx, root, nil, "git", "rev-parse", "--short", "HEAD"); err == nil {
		st, _ := run(ctx, root, nil, "git", "status", "--porcelain", "--", ".", ":(exclude)"+*out)
		rep.Commit = commitLabel(c, st)
		if strings.HasSuffix(rep.Commit, "-dirty") {
			fmt.Fprintf(os.Stderr, "measured with uncommitted changes: the report says %s; commit the dependency change first, then measure\n", rep.Commit)
		}
	}
	if *vuln {
		v1, s1 := govulncheck(ctx)
		v2, s2 := npmAudit(ctx)
		rep.Vulns = append(rep.Vulns, v1...)
		rep.Vulns = append(rep.Vulns, v2...)
		rep.VulnScans["govulncheck"], rep.VulnScans["npm audit"] = s1, s2
	} else {
		rep.VulnScans["govulncheck"], rep.VulnScans["npm audit"] = "not run", "not run"
	}
	rep.Docs = docDrift(items)
	rep.Summary, rep.Counts = summarize(items)
	if len(missing) > 0 {
		sort.Strings(missing)
		rep.Counts["withoutNote"] = len(missing)
		fmt.Fprintf(os.Stderr, "no entry in docs/software-notes.json for: %s\n", strings.Join(missing, ", "))
	}
	sort.Strings(failed)
	rep.Offline = failed
	b, _ := json.MarshalIndent(rep, "", "  ")
	if err := os.WriteFile(filepath.Join(root, *out), append(b, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *artifactDir != "" {
		if err := writeArtifactData(*artifactDir, rep); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("%d items (%d only in the module graph, not counted): %v; %d vulnerabilities; %d lookups failed\n", len(items), rep.Counts["graphOnly"], rep.Summary, len(rep.Vulns), len(failed))
	if len(failed) > 0 {
		// the report is written, and says which lookups failed; the exit status says the measurement is incomplete, so a script or
		// a person who only looks at "did it run" is not told all is well (audit 2026-10-16, L51)
		for _, f := range failed {
			fmt.Fprintln(os.Stderr, "lookup failed:", f)
		}
		os.Exit(2)
	}
}

func repoRoot() (string, error) {
	d, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(d, "cmd", "drydock-audit")); err == nil {
				return d, nil
			}
		}
		p := filepath.Dir(d)
		if p == d {
			return "", fmt.Errorf("run drydock-audit inside the Drydock repository")
		}
		d = p
	}
}

// graphOnlyNote is the remark every Go library gets that is only in the module graph (decision P23).
// withNote is an item's note: the hand-written one, then what the lookup itself found (how it read the version, what it could
// not check), which a hand-written note never replaces.
func withNote(hand, measured string) string {
	return strings.TrimSpace(strings.TrimSpace(hand) + " " + strings.TrimSpace(measured))
}

const graphOnlyNote = "Only in the module graph: no package of it is compiled into Drydock or its tests, so its version is the one the libraries that list it ask for, and it is not chased (decision P23)."

// graphOnly reports whether a Go library is in the module graph without any package of it compiled into Drydock or its tests.
func graphOnly(it Item) bool {
	return it.InBuild != nil && !*it.InBuild
}

// markGraphOnly gives every graph-only library the P23 remark, so none needs a hand-written hold.
func markGraphOnly(items []Item) {
	for i := range items {
		if graphOnly(items[i]) && !strings.Contains(items[i].Note, graphOnlyNote) {
			items[i].Note = strings.TrimSpace(items[i].Note + " " + graphOnlyNote)
		}
	}
}

// summarize counts the items by status and by kind. A graph-only library is not part of what Drydock runs, so it is left out
// of the status summary (how far behind Drydock is) and counted on its own as counts.graphOnly.
func summarize(items []Item) (summary, counts map[string]int) {
	summary, counts = map[string]int{}, map[string]int{}
	for _, it := range items {
		counts[it.Kind]++
		if graphOnly(it) {
			counts["graphOnly"]++
			continue
		}
		summary[it.Status]++
	}
	return summary, counts
}

// commitLabel is the commit a measurement describes: HEAD, marked -dirty when the working tree (other than the report itself)
// has uncommitted changes, because then the measured versions are not the ones that commit pins.
func commitLabel(head, porcelain string) string {
	c := strings.TrimSpace(head)
	if strings.TrimSpace(porcelain) != "" {
		c += "-dirty"
	}
	return c
}

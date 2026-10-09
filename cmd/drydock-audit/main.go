// Command drydock-audit measures every piece of third-party software Drydock uses against the newest release and writes
// docs/software-versions.json. It reads what the repository really pins (go.mod, ui/package-lock.json, the GitHub workflows,
// the agent manifests) and asks the registries what is newest, so the result is a measurement, not a list somebody keeps by hand.
// The hand-written part is docs/software-notes.json: what each piece is for and why it is held back, if it is.
//
//	go run ./cmd/drydock-audit            # network lookups, writes docs/software-versions.json
//	go run ./cmd/drydock-audit -vuln=false # skip the vulnerability scans (faster)
//
// The page published as the "Software versions" Artifact is generated from that JSON file (docs/software-versions.md).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

func run(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err != nil && out.Len() == 0 {
		return "", fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil // npm outdated exits 1 when something is outdated; the output is what we want
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

func day(t string) string {
	if len(t) >= 10 {
		return t[:10]
	}
	return t
}

func main() {
	vuln := flag.Bool("vuln", true, "run govulncheck and npm audit")
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

	missing := []string{}
	for i := range items {
		it := &items[i]
		if n, ok := notes.Notes[it.ID]; ok {
			it.Role, it.Hold = n.Role, n.Hold
			if n.Note != "" {
				it.Note = n.Note
			}
		} else if it.Kind == "direct" || it.Kind == "dev" || it.Kind == "runtime" || it.Kind == "tool" || it.Kind == "action" || it.Kind == "cli" || it.Kind == "bridge" {
			missing = append(missing, it.ID)
		}
		if it.Status == "" {
			it.Status = classify(it.Used, it.Latest)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Group < items[j].Group })

	rep := Report{Generated: time.Now().UTC().Format(time.RFC3339), Items: items, VulnScans: map[string]string{}, Vulns: []Vuln{}}
	if c, err := run(ctx, root, nil, "git", "rev-parse", "--short", "HEAD"); err == nil {
		rep.Commit = strings.TrimSpace(c)
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
	rep.Summary = map[string]int{}
	rep.Counts = map[string]int{}
	for _, it := range items {
		rep.Summary[it.Status]++
		rep.Counts[it.Kind]++
	}
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
	fmt.Printf("%d items: %v; %d vulnerabilities; %d lookups failed\n", len(items), rep.Summary, len(rep.Vulns), len(failed))
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

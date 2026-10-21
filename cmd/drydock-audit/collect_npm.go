package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// npmPackages reads ui/package.json for what is declared and ui/package-lock.json for what is installed, and asks the registry
// (npm outdated) for the newest version of each.
func npmPackages(ctx context.Context) []Item {
	ui := filepath.Join(root, "ui")
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		Engines         map[string]string `json:"engines"`
	}
	b, err := os.ReadFile(filepath.Join(ui, "package.json"))
	if err != nil {
		fail("ui/package.json: " + err.Error())
		return nil
	}
	_ = json.Unmarshal(b, &pkg)
	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	if b, err := os.ReadFile(filepath.Join(ui, "package-lock.json")); err == nil {
		_ = json.Unmarshal(b, &lock)
	}
	// outdatedKnown says npm answered: only then is a package it does not list "already the newest". A lookup that failed (no
	// network, a registry error) said nothing, and every package used to be reported current (audit 2026-10-16, L51).
	latest := map[string]string{}
	outdatedKnown := false
	out, code, err := runCode(ctx, ui, nil, "npm", "outdated", "--json", "--long=false")
	if err != nil && code != 1 {
		fail("npm outdated: " + err.Error()) // it did not run, or ended with a code that is not "something is behind"
	} else if l, err := npmOutdatedAnswer(out, code); err != nil {
		fail("npm outdated: " + err.Error())
	} else {
		latest, outdatedKnown = l, true
	}
	var items []Item
	add := func(deps map[string]string, kind, group string) {
		names := make([]string, 0, len(deps))
		for n := range deps {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			used := lock.Packages["node_modules/"+n].Version
			if used == "" {
				used = strings.TrimLeft(deps[n], "^~")
			}
			it := Item{ID: "npm:" + n, Group: group, Name: n, Kind: kind, Used: used, Where: "ui/package.json"}
			if l, ok := latest[n]; ok {
				it.Latest = l
			} else if outdatedKnown {
				it.Latest = used // not listed by npm outdated: already newest
			}
			items = append(items, it)
		}
	}
	add(pkg.Dependencies, "direct", "UI libraries (shipped in the app)")
	add(pkg.DevDependencies, "dev", "UI build and test tools")
	// every package in the lock file, for the counts
	items = append(items, Item{ID: "npm:lock", Group: "UI build and test tools", Name: "(all packages in package-lock.json)", Kind: "indirect", Used: itoa(len(lock.Packages) - 1), Latest: itoa(len(lock.Packages) - 1), Status: "current", Where: "ui/package-lock.json", Note: "Count of installed packages including everything the listed ones pull in."})
	if v, err := run(ctx, root, nil, "node", "--version"); err == nil {
		used := strings.TrimPrefix(strings.TrimSpace(v), "v")
		it := Item{ID: "node", Group: "Language and toolchain", Name: "Node.js (installed in this sandbox)", Kind: "runtime", Used: used, Where: "this machine; CI installs the newest Node 24 (.github/workflows/ci.yml)"}
		// the project's line is Node 24 (CI, and the UI tooling needs 24.3+; decision D9 keeps 22 as the user's floor): compare
		// with the newest release of the installed major and say what the newest line is
		if major, _, ok := strings.Cut(used, "."); ok {
			// nodejs.org's own index first: the npm package "node" lags the releases by days, and the index marks security releases
			if r, ok := nodeIndexLatest(ctx, major); ok {
				it.Latest = r.version
				if r.security {
					it.Note = "The newest " + major + ".x (" + r.version + ", " + r.date + ") is a security release. "
				}
			} else if out := lastLine(run(ctx, root, nil, "npm", "view", "node@"+major, "version")); out != "" {
				it.Latest = strings.Trim(strings.Fields(out)[len(strings.Fields(out))-1], "'")
			}
		}
		if newest := npmLatest(ctx, "node"); newest != "" {
			it.Note += "Newest Node release overall: " + newest + ". The project line is 24 (CI installs the newest 24); the latest release of the installed major is the comparison."
		}
		items = append(items, it)
	}
	if v, err := run(ctx, root, nil, "npm", "--version"); err == nil {
		it := Item{ID: "npm", Group: "Language and toolchain", Name: "npm (installed in this sandbox)", Kind: "runtime", Used: strings.TrimSpace(v), Where: "this machine"}
		it.Latest = npmLatest(ctx, "npm")
		it.Note = "Ships with Node; a newer npm can be installed on top of it (npm install -g npm), which is what the development host does."
		items = append(items, it)
	}
	return items
}

type nodeRelease struct {
	version, date string
	security      bool
}

// nodeIndexLatest reads https://nodejs.org/dist/index.json for the newest release of a major line.
func nodeIndexLatest(ctx context.Context, major string) (nodeRelease, bool) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://nodejs.org/dist/index.json", nil)
	if err != nil {
		return nodeRelease{}, false
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nodeRelease{}, false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nodeRelease{}, false
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nodeRelease{}, false
	}
	return parseNodeIndex(b, major)
}

// parseNodeIndex picks the newest release of the major line from nodejs.org's index (newest first, "v24.22.1"); false when the
// index cannot be read or has no release of that line.
func parseNodeIndex(b []byte, major string) (nodeRelease, bool) {
	var idx []struct {
		Version  string `json:"version"`
		Date     string `json:"date"`
		Security bool   `json:"security"`
	}
	if json.Unmarshal(b, &idx) != nil {
		return nodeRelease{}, false
	}
	for _, r := range idx {
		if strings.HasPrefix(r.Version, "v"+major+".") {
			return nodeRelease{version: strings.TrimPrefix(r.Version, "v"), date: r.Date, security: r.Security}, true
		}
	}
	return nodeRelease{}, false
}

// npmOutdatedAnswer is parseNPMOutdated for a run that ended with exit code code. npm exits 1 when some package is behind and lists
// them, and 0 when none is; exit 1 with nothing listed is therefore npm failing (its error went to the error stream), not
// "nothing is behind".
func npmOutdatedAnswer(out string, code int) (map[string]string, error) {
	if code == 1 && strings.TrimSpace(out) == "" {
		return nil, errors.New("npm exited with code 1 and listed nothing")
	}
	return parseNPMOutdated(out)
}

// parseNPMOutdated reads `npm outdated --json`: the newest version of each package that is behind. Empty output means nothing is
// behind. An error answer from npm (the registry could not be reached prints {"error":{"code":...,"summary":...}}) and output
// that is not JSON are errors, not an empty list of packages.
func parseNPMOutdated(out string) (map[string]string, error) {
	latest := map[string]string{}
	if strings.TrimSpace(out) == "" {
		return latest, nil
	}
	var o map[string]struct {
		Latest  string `json:"latest"`
		Code    string `json:"code"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		return nil, errors.New("unreadable output")
	}
	if e, ok := o["error"]; ok && (e.Code != "" || e.Summary != "") {
		if e.Summary != "" {
			return nil, errors.New(e.Summary)
		}
		return nil, errors.New(e.Code)
	}
	for k, v := range o {
		latest[k] = v.Latest
	}
	return latest, nil
}

func itoa(n int) string {
	if n < 0 {
		n = 0
	}
	b, _ := json.Marshal(n)
	return string(b)
}

func lastLine(out string, err error) string {
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

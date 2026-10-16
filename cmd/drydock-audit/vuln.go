package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// govulncheckModule is the scanner's module at the release the audit runs and the CI workflow (.github/workflows/security.yml)
// runs: pinned, so the code that checks the code is the one Go's checksum database vouches for, not whatever is newest
// (audit 2026-10-16, L51). Move both together; docs/software-notes.json records it as the version "used".
const govulncheckModule = "golang.org/x/vuln/cmd/govulncheck@v1.9.0"

// govulncheck runs the Go vulnerability scanner over the whole module. A finding is "called" when the code can reach the
// vulnerable function; the others are in a dependency or the standard library the build includes but does not call.
func govulncheck(ctx context.Context) ([]Vuln, string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	out, err := run(ctx, root, nil, "go", "run", govulncheckModule, "-format", "json", "./...")
	if err != nil {
		fail("govulncheck: " + err.Error())
		return nil, "did not run: " + firstLine(err.Error())
	}
	vs, err := parseGovulncheck(out)
	if err != nil {
		fail("govulncheck: " + err.Error())
		return nil, "did not run: " + err.Error()
	}
	called := 0
	for _, v := range vs {
		if v.Called {
			called++
		}
	}
	return vs, fmt.Sprintf("%d known vulnerabilities in what is built, %d of them in code that is called", len(vs), called)
}

// parseGovulncheck reads the scanner's JSON stream. Output that stops part-way through is an error: the findings before the
// break are not the findings.
func parseGovulncheck(out string) ([]Vuln, error) {
	type osvT struct {
		ID      string `json:"id"`
		Summary string `json:"summary"`
	}
	type frame struct {
		Module   string `json:"module"`
		Version  string `json:"version"`
		Package  string `json:"package"`
		Function string `json:"function"`
	}
	type msg struct {
		OSV     *osvT `json:"osv"`
		Finding *struct {
			OSV   string  `json:"osv"`
			Fixed string  `json:"fixed_version"`
			Trace []frame `json:"trace"`
		} `json:"finding"`
	}
	if strings.TrimSpace(out) == "" {
		return nil, errors.New("the scanner printed nothing")
	}
	summ := map[string]string{}
	found := map[string]*Vuln{}
	dec := json.NewDecoder(strings.NewReader(out))
	for dec.More() {
		var m msg
		if err := dec.Decode(&m); err != nil {
			return nil, fmt.Errorf("unreadable output: %v", err)
		}
		if m.OSV != nil {
			summ[m.OSV.ID] = m.OSV.Summary
		}
		if f := m.Finding; f != nil && len(f.Trace) > 0 {
			v := found[f.OSV]
			if v == nil {
				v = &Vuln{Source: "govulncheck", ID: f.OSV, Package: f.Trace[0].Module + " " + f.Trace[0].Version, Fixed: strings.TrimPrefix(f.Fixed, "v")}
				found[f.OSV] = v
			}
			if f.Trace[0].Function != "" {
				v.Called = true
			}
		}
	}
	vs := []Vuln{}
	for id, v := range found {
		v.Summary = summ[id]
		vs = append(vs, *v)
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].ID < vs[j].ID })
	return vs, nil
}

// npmAudit asks the npm registry for advisories against the locked UI packages.
func npmAudit(ctx context.Context) ([]Vuln, string) {
	out, err := runAllowing(ctx, filepath.Join(root, "ui"), nil, []int{1}, "npm", "audit", "--json")
	if err != nil {
		fail("npm audit: " + err.Error())
		return nil, "did not run: " + firstLine(err.Error())
	}
	vs, err := parseNPMAudit(out)
	if err != nil {
		fail("npm audit: " + err.Error())
		return nil, "did not run: " + err.Error()
	}
	return vs, fmt.Sprintf("%d advisories against installed UI packages", len(vs))
}

// parseNPMAudit reads `npm audit --json`. An error answer from the registry, or output that is not the report, is an error: it
// is not "no advisories".
func parseNPMAudit(out string) ([]Vuln, error) {
	var a struct {
		Vulnerabilities map[string]struct {
			Severity string `json:"severity"`
			Via      []any  `json:"via"`
			Range    string `json:"range"`
			Fix      any    `json:"fixAvailable"`
		} `json:"vulnerabilities"`
		Error *struct {
			Summary string `json:"summary"`
		} `json:"error"`
		Metadata *struct{} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		return nil, errors.New("unreadable output")
	}
	if a.Error != nil {
		return nil, errors.New(a.Error.Summary)
	}
	if a.Metadata == nil {
		return nil, errors.New("the output is not an audit report")
	}
	vs := []Vuln{}
	for name, v := range a.Vulnerabilities {
		sum := ""
		for _, x := range v.Via {
			if m, ok := x.(map[string]any); ok {
				if t, ok := m["title"].(string); ok {
					sum = t
					break
				}
			}
		}
		vs = append(vs, Vuln{Source: "npm audit", ID: name, Package: name + " " + v.Range, Severity: v.Severity, Summary: sum})
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].ID < vs[j].ID })
	return vs, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

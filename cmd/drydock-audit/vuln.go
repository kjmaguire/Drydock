package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// govulncheck runs the Go vulnerability scanner over the whole module. A finding is "called" when the code can reach the
// vulnerable function; the others are in a dependency or the standard library the build includes but does not call.
func govulncheck(ctx context.Context) ([]Vuln, string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	out, err := run(ctx, root, nil, "go", "run", "golang.org/x/vuln/cmd/govulncheck@latest", "-format", "json", "./...")
	if err != nil {
		fail("govulncheck: " + err.Error())
		return nil, "did not run: " + firstLine(err.Error())
	}
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
	summ := map[string]string{}
	found := map[string]*Vuln{}
	dec := json.NewDecoder(strings.NewReader(out))
	for dec.More() {
		var m msg
		if err := dec.Decode(&m); err != nil {
			break
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
	var vs []Vuln
	for id, v := range found {
		v.Summary = summ[id]
		vs = append(vs, *v)
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].ID < vs[j].ID })
	called := 0
	for _, v := range vs {
		if v.Called {
			called++
		}
	}
	return vs, fmt.Sprintf("%d known vulnerabilities in what is built, %d of them in code that is called", len(vs), called)
}

// npmAudit asks the npm registry for advisories against the installed UI packages.
func npmAudit(ctx context.Context) ([]Vuln, string) {
	out, err := run(ctx, filepath.Join(root, "ui"), nil, "npm", "audit", "--json")
	if err != nil {
		fail("npm audit: " + err.Error())
		return nil, "did not run: " + firstLine(err.Error())
	}
	var a struct {
		Vulnerabilities map[string]struct {
			Severity string `json:"severity"`
			Via      []any  `json:"via"`
			Range    string `json:"range"`
			Fix      any    `json:"fixAvailable"`
		} `json:"vulnerabilities"`
		Error *struct{ Summary string } `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		return nil, "unreadable output"
	}
	if a.Error != nil {
		return nil, "did not run: " + a.Error.Summary
	}
	var vs []Vuln
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
	return vs, fmt.Sprintf("%d advisories against installed UI packages", len(vs))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

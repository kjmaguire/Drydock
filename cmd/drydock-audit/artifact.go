package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// writeArtifactData writes the four small JSON files the "Drydock Software Versions" Artifact is built over: items, vulns,
// drift and meta. They are flat row objects (the Artifact's dataset format) derived from the report, and are not committed;
// docs/software-versions.md says how to upload them.
func writeArtifactData(dir string, rep Report) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	type row = map[string]any
	items := []row{}
	for _, it := range rep.Items {
		if it.ID == "npm:lock" {
			continue
		}
		layer := "chosen"
		if it.Kind == "indirect" {
			layer = "pulled"
			if it.InBuild != nil && !*it.InBuild {
				layer = "graph" // in the module graph, but no package of it is compiled into the app or its tests
			}
		}
		var remark []string
		if it.Hold != "" {
			remark = append(remark, "Held back: "+it.Hold)
		}
		if it.Deprecated != "" {
			remark = append(remark, "Deprecated: "+it.Deprecated)
		}
		if it.UsedBy != nil && *it.UsedBy == 0 {
			remark = append(remark, "No product package imports it.")
		}
		if it.Note != "" {
			remark = append(remark, it.Note)
		}
		items = append(items, row{
			"id": it.ID, "group": it.Group, "name": it.Name, "kind": it.Kind, "layer": layer,
			"used": it.Used, "latest": it.Latest, "status": it.Status, "usedDate": it.UsedDate, "latestDate": it.LatestDate,
			"where": it.Where, "role": it.Role, "remark": strings.Join(remark, " "),
		})
	}
	vulns := []row{}
	for _, v := range rep.Vulns {
		called := "no"
		if v.Called {
			called = "yes"
		}
		if v.Source == "npm audit" {
			called = "n/a"
		}
		vulns = append(vulns, row{"id": v.ID, "source": v.Source, "package": v.Package, "severity": v.Severity, "called": called, "fixed": v.Fixed, "summary": v.Summary})
	}
	drift := []row{}
	for _, d := range rep.Docs {
		drift = append(drift, row{"name": d.Name, "docs": d.Docs, "code": d.Code, "ref": d.Ref})
	}
	meta := []row{{
		"generated": rep.Generated, "commit": rep.Commit, "govulncheck": rep.VulnScans["govulncheck"], "npmAudit": rep.VulnScans["npm audit"],
		"lookupsFailed": fmt.Sprint(len(rep.Offline)),
	}}
	// the Artifact's dashboard type refuses a file with no rows ("The file has no rows this dashboard can read"), so a dataset
	// that is empty (no advisory, no drift: the usual state) carries one placeholder row, which the page leaves out
	for _, ds := range []*[]row{&vulns, &drift} {
		if len(*ds) == 0 {
			*ds = []row{{"none": "yes"}}
		}
	}
	for name, v := range map[string]any{"items": items, "vulns": vulns, "drift": drift, "meta": meta} {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

package scan

import (
	"context"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/checks"
)

// scanDeps runs osv-scanner over dir (`scan source --format json -r .`) and reads its JSON. Exit codes follow the tool: 0
// nothing known, 1 vulnerabilities (the JSON names them), 128 no supported lockfile or manifest (skipped), anything else
// is an error. Only identifiers are kept (checks.ParseOSV): advisory text comes from an outside database.
func scanDeps(ctx context.Context, o *Options, path string, tool Tool) ([]Finding, EngineStatus, string) {
	args := append(append([]string(nil), tool.Args...), "scan", "source", "--format", "json", "-r", ".")
	r := runTool(ctx, o.Procs, path, args, o.Dir, o.env(), "scan:deps", o.timeout())
	if r.StartErr != nil {
		return nil, Errored, "osv-scanner could not be started: " + cleanLine(r.StartErr.Error(), 160)
	}
	if r.TimedOut {
		return nil, Errored, fmt.Sprintf("osv-scanner did not finish within %s", o.timeout())
	}
	if ctx.Err() != nil {
		return nil, Cancelled, ""
	}
	switch r.Code {
	case 0:
		return nil, Done, ""
	case 1:
		fs, err := checks.ParseOSV(r.Stdout)
		if err != nil {
			return nil, Errored, "osv-scanner exited 1 (vulnerabilities found) but its JSON could not be read: " + cleanLine(err.Error(), 120)
		}
		var out []Finding
		for _, f := range fs {
			out = append(out, Finding{Engine: Deps, Severity: High, Rule: f.Ecosystem, Package: f.Package, Version: f.Version, IDs: f.IDs,
				File: relativeURI(f.Source, o.Dir), Title: cleanLine(fmt.Sprintf("%s %s has known vulnerabilities: %s", f.Package, f.Version, strings.Join(f.IDs, ", ")), 200)})
		}
		return out, Done, ""
	case 128:
		return nil, Skipped, "osv-scanner found no supported lockfile or manifest in the project."
	}
	reason := firstStderrLine(r.Stderr)
	if reason == "" {
		reason = "no message"
	}
	return nil, Errored, fmt.Sprintf("osv-scanner exited %d: %s", r.Code, o.redact(reason))
}

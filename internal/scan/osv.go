package scan

import (
	"context"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/checks"
)

// osvArgs are osv-scanner's arguments: `scan source --format json --no-call-analysis=all -r .`. The scan only reads files
// (decision P61): osv-scanner parses lockfiles and manifests itself, and its call analysis, the one part that runs other
// programs (Go's is on by default and runs the go toolchain over the repository, which may fetch a toolchain or modules the
// repository names), is switched off for every language.
func osvArgs(tool Tool) []string {
	return append(append([]string(nil), tool.Args...), "scan", "source", "--format", "json", "--no-call-analysis=all", "-r", ".")
}

// scanDeps runs osv-scanner over dir (osvArgs) and reads its JSON. Exit codes follow the tool: 0 nothing known, 1
// vulnerabilities (the JSON names them), 128 no supported lockfile or manifest (skipped), anything else is an error. Only
// identifiers are kept (checks.ParseOSV): advisory text comes from an outside database.
func scanDeps(ctx context.Context, o *Options, path string, tool Tool) ([]Finding, EngineStatus, string) {
	args := osvArgs(tool)
	r := runTool(ctx, o.Procs, path, args, o.Dir, o.env(), "scan:deps", o.timeout())
	if ctx.Err() != nil {
		return nil, Cancelled, ""
	}
	if r.StartErr != nil {
		return nil, Errored, "osv-scanner could not be started: " + cleanLine(r.StartErr.Error(), 160)
	}
	if r.TimedOut {
		return nil, Errored, fmt.Sprintf("osv-scanner did not finish within %s", o.timeout())
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
			fd := Finding{Engine: Deps, Severity: High, Rule: f.Ecosystem, Package: f.Package, Version: f.Version, IDs: f.IDs, Fixed: f.Fixed, Unfixed: f.Unfixed,
				File: relativeURI(f.Source, o.Dir), Title: cleanLine(fmt.Sprintf("%s %s has known vulnerabilities: %s", f.Package, f.Version, strings.Join(f.IDs, ", ")), 200)}
			fd.Fingerprint = Fingerprint(Deps, fd.Rule, fd.File, depsText(fd), 0)
			out = append(out, fd)
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

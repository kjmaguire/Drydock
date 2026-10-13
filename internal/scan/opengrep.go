package scan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// scanCode runs Opengrep over dir with the rules folder (`scan -f <rules> --sarif-output=<file> .`) and reads the SARIF
// file it writes. Opengrep exits 0 whether or not it found anything (findings change the exit code only with --error, which
// is not passed); another code is an error and its first stderr line is the reason.
func scanCode(ctx context.Context, o *Options, path string, tool Tool) ([]Finding, EngineStatus, string, string) {
	tmp, err := os.MkdirTemp("", "drydock-scan-*")
	if err != nil {
		return nil, Errored, "a temporary folder for the SARIF output could not be made: " + cleanLine(err.Error(), 120), ""
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "opengrep.sarif")
	args := append(append([]string(nil), tool.Args...), "scan", "-f", o.RulesDir, "--sarif-output="+out, "--no-progress-bar", "-q", ".")
	r := runTool(ctx, o.Procs, path, args, o.Dir, o.env(), "scan:sast", o.timeout())
	if ctx.Err() != nil {
		return nil, Cancelled, "", ""
	}
	if r.StartErr != nil {
		return nil, Errored, "opengrep could not be started: " + cleanLine(r.StartErr.Error(), 160), ""
	}
	if r.TimedOut {
		return nil, Errored, fmt.Sprintf("opengrep did not finish within %s", o.timeout()), ""
	}
	if r.Code != 0 {
		reason := firstStderrLine(r.Stderr)
		if reason == "" {
			reason = "no message"
		}
		return nil, Errored, fmt.Sprintf("opengrep exited %d: %s", r.Code, o.redact(reason)), ""
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return nil, Errored, "opengrep exited 0 but wrote no SARIF file", ""
	}
	fs, version, err := ParseSARIF(data, SAST, o.Dir, o.redact)
	if err != nil {
		return nil, Errored, cleanLine(err.Error(), 160), ""
	}
	return fs, Done, "", version
}

// Package scantest holds the LABELLED TEST DOUBLES of the external scan tools: a stand-in osv-scanner and a stand-in opengrep.
// Each is this test binary (or the e2e harness) started again as `<binary> drydock-standin-tool <tool> [options] <tool args>`,
// so no shell script and no PATH change is needed and the same stand-in runs on Windows. They print canned, documented
// output shapes; they never present themselves as the real tools (their --version says so).
package scantest

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Marker is the first argument that tells a binary to run a stand-in instead of itself.
const Marker = "drydock-standin-tool"

// IsStandIn reports whether args (os.Args) ask for a stand-in.
func IsStandIn(args []string) bool { return len(args) > 2 && args[1] == Marker }

// Args are the leading arguments (scan.Tool.Args) that make a binary run the stand-in of name. Options come before the
// tool's own arguments:
//
//	--sleep-ms=N  wait N ms before answering (so a test can see the engine running)
//	--fail        exit 2 with a message on stderr (a tool that broke)
//	--empty       report nothing found
//	--count=FILE  append a line to FILE on every run (a test counts how often the tool was started)
func Args(name string, opts ...string) []string { return append([]string{Marker, name}, opts...) }

// Main runs the stand-in named by os.Args and returns its exit code. Call it from TestMain or the harness's main when
// IsStandIn(os.Args).
func Main(args []string) int {
	name := args[2]
	rest := args[3:]
	var sleep time.Duration
	fail, empty := false, false
	for len(rest) > 0 {
		opt := rest[0]
		switch {
		case strings.HasPrefix(opt, "--sleep-ms="):
			ms, _ := strconv.Atoi(strings.TrimPrefix(opt, "--sleep-ms="))
			sleep = time.Duration(ms) * time.Millisecond
		case opt == "--fail":
			fail = true
		case opt == "--empty":
			empty = true
		case strings.HasPrefix(opt, "--count="):
			if f, err := os.OpenFile(strings.TrimPrefix(opt, "--count="), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
				fmt.Fprintf(f, "%s %s\n", name, strings.Join(rest[1:], " "))
				f.Close()
			}
		default:
			opt = ""
		}
		if opt == "" {
			break
		}
		rest = rest[1:]
	}
	if len(rest) > 0 && rest[0] == "--version" {
		switch name {
		case "osv-scanner":
			fmt.Println("osv-scanner version: 0.0.0-standin (a Drydock test double, not osv-scanner)")
		default:
			fmt.Println("0.0.0-standin (a Drydock test double, not Opengrep)")
		}
		return 0
	}
	time.Sleep(sleep)
	if fail {
		fmt.Fprintf(os.Stderr, "%s stand-in: told to fail (a test double)\n", name)
		return 2
	}
	switch name {
	case "osv-scanner":
		return osv(empty)
	case "opengrep":
		return opengrep(rest, empty)
	}
	fmt.Fprintf(os.Stderr, "no stand-in named %q\n", name)
	return 3
}

// osv prints the documented osv-scanner JSON (results[].packages[].vulnerabilities[], each with its OSV record's affected
// ranges) for one vulnerable Go package and
// exits 1, as the real tool does when it finds something; with --empty it prints an empty result and exits 0.
func osv(empty bool) int {
	if empty {
		fmt.Println(`{"results":[]}`)
		return 0
	}
	b, _ := json.Marshal(map[string]any{"results": []any{map[string]any{
		"source": map[string]any{"path": "go.mod", "type": "lockfile"},
		"packages": []any{map[string]any{
			"package": map[string]any{"name": "golang.org/x/text", "version": "0.3.7", "ecosystem": "Go"},
			// each vulnerability is an OSV record: its affected ranges name the version that fixes it
			"vulnerabilities": []any{
				map[string]any{"id": "GO-2021-0113", "affected": []any{map[string]any{"package": map[string]any{"name": "golang.org/x/text", "ecosystem": "Go"},
					"ranges": []any{map[string]any{"type": "SEMVER", "events": []any{map[string]any{"introduced": "0"}, map[string]any{"fixed": "0.3.7-rc1"}, map[string]any{"fixed": "0.3.8"}}}}}}},
				map[string]any{"id": "CVE-2021-38561", "affected": []any{map[string]any{"package": map[string]any{"name": "golang.org/x/text", "ecosystem": "Go"},
					"ranges": []any{map[string]any{"type": "SEMVER", "events": []any{map[string]any{"introduced": "0"}, map[string]any{"fixed": "0.3.8"}}}}}}},
			},
		}},
	}}})
	fmt.Println(string(b))
	return 1
}

// SARIF is the log the stand-in opengrep writes: SARIF 2.1.0 with two results in calc.go (a PROTOCOL FIXTURE written from
// the SARIF specification and the Semgrep-family shape; not captured from a real run).
const SARIF = `{
  "$schema": "https://json.schemastore.org/sarif-2.1.0.json",
  "version": "2.1.0",
  "runs": [{
    "tool": {"driver": {"name": "Opengrep stand-in (a Drydock test double)", "semanticVersion": "0.0.0-standin",
      "rules": [
        {"id": "go.lang.security.audit.crypto.math-random-used", "shortDescription": {"text": "Do not use math/rand for security purposes"}, "defaultConfiguration": {"level": "warning"}},
        {"id": "go.lang.security.audit.dangerous-exec-command", "shortDescription": {"text": "Detected non-static command inside exec"}, "defaultConfiguration": {"level": "error"}}
      ]}},
    "results": [
      {"ruleId": "go.lang.security.audit.crypto.math-random-used", "level": "warning", "message": {"text": "Do not use math/rand for security purposes: use crypto/rand"},
       "locations": [{"physicalLocation": {"artifactLocation": {"uri": "calc.go", "uriBaseId": "%SRCROOT%"}, "region": {"startLine": 3, "endLine": 3}}}]},
      {"ruleId": "go.lang.security.audit.dangerous-exec-command", "level": "error", "message": {"text": "Detected non-static command inside exec: the argument comes from input"},
       "locations": [{"physicalLocation": {"artifactLocation": {"uri": "calc.go", "uriBaseId": "%SRCROOT%"}, "region": {"startLine": 5, "endLine": 7}}}]}
    ]
  }]
}`

// opengrep checks that each rules folder given with -f is there, writes SARIF to the --sarif-output file and exits 0. Like
// Opengrep 1.31.0 it refuses an option it does not know with exit 2, the usage line, then the error, in colour (what the real
// one printed for --no-progress-bar, decision P95).
func opengrep(args []string, empty bool) int {
	out, rules := "", []string{}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "scan" || args[i] == "-q" || args[i] == "--no-git-ignore" || !strings.HasPrefix(args[i], "-"):
		case strings.HasPrefix(args[i], "--sarif-output="):
			out = strings.TrimPrefix(args[i], "--sarif-output=")
		case args[i] == "-f" && i+1 < len(args):
			rules = append(rules, args[i+1])
			i++
		default:
			fmt.Fprintln(os.Stderr, "Usage: \x1b[01mopengrep scan\x1b[m [\x1b[01m--help\x1b[m] [\x1b[04mOPTION\x1b[m]… [\x1b[04mTARGETS\x1b[m]…")
			fmt.Fprintf(os.Stderr, "opengrep scan: \x1b[31munknown\x1b[m option \x1b[01m%s\x1b[m\n", args[i])
			return 2
		}
	}
	if len(rules) == 0 {
		fmt.Fprintln(os.Stderr, "opengrep stand-in: no rules given (-f)")
		return 2
	}
	// every -f must be a folder that is there (the built-in rules, then the person's own folder)
	for _, dir := range rules {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			fmt.Fprintf(os.Stderr, "opengrep stand-in: rules folder %s is not there\n", dir)
			return 2
		}
	}
	if out == "" {
		fmt.Fprintln(os.Stderr, "opengrep stand-in: no --sarif-output given")
		return 2
	}
	doc := SARIF
	if empty {
		doc = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"Opengrep stand-in","semanticVersion":"0.0.0-standin","rules":[]}},"results":[]}]}`
	}
	if err := os.WriteFile(out, []byte(doc), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "opengrep stand-in:", err)
		return 2
	}
	return 0
}

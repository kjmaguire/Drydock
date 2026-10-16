// Package contract is the catalog of assumptions Drydock makes about the agent CLIs it drives ("contracts"), each tied to the
// Go tests that check it against the real CLI. cmd/drydock-contract runs those tests, maps the results back to the contracts
// and writes contract-report.json and contract-report.md; docs/contract-tests.md explains every contract and the update
// policy (a CLI version that breaks a contract is not adopted until the adapter is fixed).
//
// The worst bugs found in real use lived in the gap between the scripted fake agent most tests use and the real CLI. A
// contract names one such assumption so it is checked against every new CLI release instead of being rediscovered by a user.
package contract

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Contract is one assumption Drydock makes about an agent CLI.
type Contract struct {
	ID string `json:"id"` // stable id, e.g. CLAUDE-STREAM-01; one id can cover several CLIs (ACP-INIT-01)
	// CLI is the program the contract is about, as it is run: claude, claude-agent-acp, codex-acp, gemini, opencode, goose,
	// cline; "drydock" for a check of Drydock's own code.
	CLI string `json:"cli"`
	// Adapter is the manifest id the CLI belongs to (claude for claude-agent-acp, codex for codex-acp); empty for drydock.
	Adapter    string    `json:"adapter,omitempty"`
	Assumption string    `json:"assumption"` // one sentence: what Drydock assumes
	Why        string    `json:"why"`        // why Drydock depends on it
	Files      []string  `json:"files"`      // where Drydock depends on it, relative to the repository root
	Tests      []TestRef `json:"tests"`
	Platform   string    `json:"platform,omitempty"` // "windows" for a contract that can only be checked on Windows
}

// TestRef names a Go test that checks a contract.
type TestRef struct {
	Package string `json:"package"`           // module-relative package path, e.g. ./internal/agents/runtime
	Test    string `json:"test"`              // top-level test function
	Subtest string `json:"subtest,omitempty"` // a subtest name as given to t.Run, when only that subtest checks the contract
	// EstUSD is what one run is expected to cost (0 for a free check). The runner's budget guard uses it for tests that do
	// not report their own spend, and runs free checks first.
	EstUSD float64  `json:"estUsd,omitempty"`
	Env    []string `json:"env,omitempty"` // KEY=VALUE the test needs, added by the runner
	// Slow marks a check that takes minutes (it waits out a long tool call); the runner's --skip-slow leaves it out.
	Slow bool `json:"slow,omitempty"`
	// Enclosing gives the test the enclosing Claude Code session's variables, which the runner removes for every other test:
	// a check that the adapter removes them needs them.
	Enclosing bool `json:"enclosing,omitempty"`
}

// Paid reports whether the test sends real prompts.
func (r TestRef) Paid() bool { return r.EstUSD > 0 }

// Name is the test's name as go test reports it: Test or Test/Subtest (spaces in a subtest name become underscores).
func (r TestRef) Name() string {
	if r.Subtest == "" {
		return r.Test
	}
	return r.Test + "/" + strings.ReplaceAll(r.Subtest, " ", "_")
}

// Key identifies a contract row: one id for one CLI.
func (c Contract) Key() string { return c.ID + " " + c.CLI }

// Matches reports whether the contract concerns any of the given CLI names (a program name or a manifest id).
func (c Contract) Matches(names []string) bool {
	if len(names) == 0 {
		return true
	}
	for _, n := range names {
		n = strings.TrimSpace(strings.ToLower(n))
		if n != "" && (n == c.CLI || n == c.Adapter) {
			return true
		}
	}
	return false
}

// Filter returns the contracts that concern the given CLI names (all of them when names is empty).
func Filter(cs []Contract, names []string) []Contract {
	var out []Contract
	for _, c := range cs {
		if c.Matches(names) {
			out = append(out, c)
		}
	}
	return out
}

// CLIs lists the distinct CLI names of the contracts, in first-seen order.
func CLIs(cs []Contract) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range cs {
		if !seen[c.CLI] {
			seen[c.CLI] = true
			out = append(out, c.CLI)
		}
	}
	return out
}

var idRe = regexp.MustCompile(`^[A-Z]+(-[A-Z]+)*-\d{2}$`)

// Validate checks the catalog's structure: ids, CLIs, a sentence of assumption and of reason, files and at least one test per
// contract, no duplicate id for the same CLI. Whether the referenced tests exist is checked by the package's own tests, which
// parse the test files.
func Validate(cs []Contract) error {
	var p []string
	seen := map[string]bool{}
	for i, c := range cs {
		w := fmt.Sprintf("contract %d (%s %s)", i, c.ID, c.CLI)
		if !idRe.MatchString(c.ID) {
			p = append(p, w+": id must look like CLAUDE-STREAM-01")
		}
		if strings.TrimSpace(c.CLI) == "" {
			p = append(p, w+": no CLI")
		}
		if c.CLI != CLIDrydock && c.Adapter == "" {
			p = append(p, w+": no adapter (manifest id)")
		}
		if strings.TrimSpace(c.Assumption) == "" || strings.TrimSpace(c.Why) == "" {
			p = append(p, w+": the assumption and the reason are required")
		}
		if len(c.Files) == 0 {
			p = append(p, w+": name the file that depends on it")
		}
		if len(c.Tests) == 0 {
			p = append(p, w+": no test checks it")
		}
		for _, t := range c.Tests {
			if !strings.HasPrefix(t.Package, "./") || !strings.HasPrefix(t.Test, "Test") || t.EstUSD < 0 {
				p = append(p, fmt.Sprintf("%s: bad test reference %+v", w, t))
			}
			for _, kv := range t.Env {
				if k, _, ok := strings.Cut(kv, "="); !ok || k == "" {
					p = append(p, fmt.Sprintf("%s: env %q is not KEY=VALUE", w, kv))
				}
			}
		}
		if c.Platform != "" && c.Platform != "windows" {
			p = append(p, w+": platform must be empty or windows")
		}
		if seen[c.Key()] {
			p = append(p, w+": duplicate")
		}
		seen[c.Key()] = true
	}
	if len(p) > 0 {
		sort.Strings(p)
		return fmt.Errorf("contract catalog: %s", strings.Join(p, "; "))
	}
	return nil
}

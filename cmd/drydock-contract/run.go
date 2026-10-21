package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/contract"
)

type runner struct {
	root, out string
	ledger    string
	budget    float64
	timeout   time.Duration
	skipSlow  bool
	env       []string
	enclosing []string // the enclosing Claude Code session's variables suiteEnv removed, for the tests that check their removal
	w         io.Writer
}

// enclosingVars are the enclosing Claude Code session's variables present in env.
func enclosingVars(env []string) []string {
	var out []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		for _, n := range contract.EnclosingSessionEnv {
			if strings.EqualFold(k, n) {
				out = append(out, kv)
			}
		}
	}
	return out
}

// suiteEnv is the environment of every go test the runner starts: the runner's own, without the enclosing Claude Code
// session (a CLI started from it would report and join that session) or the switch that skips real agents, with the budget
// and the ledger, and without an empty sign-in variable (an unset GitHub secret arrives as an empty string).
func suiteEnv(base []string, ledger string, budget float64) []string {
	env := contract.WithoutVars(base, contract.EnclosingSessionEnv...)
	env = contract.WithoutVars(env, contract.EnvNoRealAgents, contract.EnvBudget, contract.EnvLedger, contract.EnvRealClaude, "DRYDOCK_FAKE_AGENT")
	for _, k := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"} {
		if v, ok := lookup(env, k); ok && strings.TrimSpace(v) == "" {
			env = contract.WithoutVars(env, k)
		}
	}
	if _, ok := lookup(env, "GOFLAGS"); !ok {
		env = append(env, "GOFLAGS=-mod=mod") // how the repository builds (CLAUDE.md)
	}
	return append(env, contract.EnvBudget+"="+strconv.FormatFloat(budget, 'f', -1, 64), contract.EnvLedger+"="+ledger)
}

func lookup(env []string, key string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && strings.EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}

// runAll runs the plan and returns the outcome of every test reference, and the estimate for the paid tests that ran without
// reporting their spend.
func (r *runner) runAll(ctx context.Context, cs []contract.Contract) (map[string]contract.Outcome, float64, error) {
	refs := map[string][]contract.TestRef{}
	ids := map[string][]string{}
	platforms := map[string]map[string]bool{}
	seen := map[string]bool{}
	for _, c := range cs {
		for _, ref := range c.Tests {
			k := ref.Package + " " + ref.Test
			if !seen[refKey(ref)] { // a test several contracts refer to runs and is reported once
				seen[refKey(ref)] = true
				refs[k] = append(refs[k], ref)
			}
			ids[k] = appendOnce(ids[k], c.ID)
			if platforms[k] == nil {
				platforms[k] = map[string]bool{}
			}
			platforms[k][c.Platform] = true
		}
	}
	logs := filepath.Join(r.out, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return nil, 0, err
	}
	outcomes := map[string]contract.Outcome{}
	var estimated float64
	groups := contract.Plan(cs)
	fmt.Fprintf(r.w, "Running %d tests for %d contracts (budget $%.2f)\n", len(groups), len(cs), r.budget)
	for i, g := range groups {
		k := g.Package + " " + g.Test
		label := fmt.Sprintf("[%d/%d] %s %s (%s)", i+1, len(groups), g.Package, g.Pattern(), strings.Join(ids[k], ", "))
		skip := ""
		spent, err := contract.LedgerTotal(r.ledger)
		if err != nil {
			return nil, 0, err
		}
		switch {
		case platforms[k]["windows"] && len(platforms[k]) == 1 && runtime.GOOS != "windows":
			skip = "only checked on Windows"
		case g.Slow && r.skipSlow:
			skip = "a slow check, left out by --skip-slow"
		case g.Paid() && spent+estimated+g.EstUSD > r.budget:
			skip = fmt.Sprintf("budget: $%.4f reported and about $%.2f estimated of $%.2f are spent, and this test needs about $%.2f", spent, estimated, r.budget, g.EstUSD)
		}
		if skip != "" {
			for _, ref := range refs[k] {
				outcomes[refKey(ref)] = contract.NotRun(skip)
			}
			fmt.Fprintf(r.w, "%s: skipped, %s\n", label, skip)
			continue
		}
		fmt.Fprintf(r.w, "%s ...\n", label)
		run, raw, err := r.goTest(ctx, g)
		_ = os.WriteFile(filepath.Join(logs, fmt.Sprintf("%02d-%s.jsonl", i+1, g.Test)), raw, 0o644)
		if err != nil {
			return nil, 0, err
		}
		ledger, err := contract.ReadLedger(r.ledger)
		if err != nil {
			return nil, 0, err
		}
		spend := contract.SpendByTest(ledger)
		var ran, reported bool
		for _, ref := range refs[k] {
			oc := contract.OutcomeOf(run, ref, spend)
			outcomes[refKey(ref)] = oc
			ran = ran || oc.Ran
			reported = reported || oc.SpendUSD != nil
			line := fmt.Sprintf("    %-7s %s", oc.Status, ref.Name())
			if oc.Elapsed > 0 {
				line += fmt.Sprintf(" (%.1fs)", oc.Elapsed)
			}
			if oc.SpendUSD != nil {
				line += fmt.Sprintf(" $%.4f", *oc.SpendUSD)
			}
			if oc.Reason != "" {
				line += ": " + oc.Reason
			}
			fmt.Fprintln(r.w, line)
		}
		if g.Paid() && ran && !reported {
			estimated += g.EstUSD
		}
	}
	return outcomes, estimated, nil
}

// goTest runs one group with `go test -json` and parses what it reported. A failing test is a result, not an error; an error
// is a go command that could not run at all.
func (r *runner) goTest(ctx context.Context, g contract.Group) (*contract.Run, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout+5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-json", "-count=1", "-timeout", r.timeout.String(), "-run", g.Pattern(), g.Package)
	cmd.Dir = r.root
	cmd.Env = append(append([]string(nil), r.env...), g.Env...)
	if g.Enclosing {
		cmd.Env = append(cmd.Env, r.enclosing...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	run, err := contract.ParseGoTestJSON(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		return nil, stdout.Bytes(), err
	}
	for _, l := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		if strings.TrimSpace(l) != "" {
			run.PackageOutput = append(run.PackageOutput, l)
		}
	}
	if runErr != nil && len(run.Tests) == 0 && run.Package == "" && !run.BuildFailed {
		if _, ok := runErr.(*exec.ExitError); !ok {
			return nil, stdout.Bytes(), fmt.Errorf("go test %s: %v", g.Package, runErr)
		}
		run.Package = contract.StatusFail // the go command failed before any test, e.g. a broken go.mod
	}
	return run, stdout.Bytes(), nil
}

func appendOnce(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// Command drydock-contract runs Drydock's contract suite (docs/contract-tests.md) against the real agent CLIs installed here.
// It finds each CLI and its version, runs the Go tests that check each contract with `go test -json` (one test at a time, free
// checks first, paid ones while the budget lasts), maps the results back to the contracts and writes contract-report.json and
// contract-report.md. It exits 1 when a contract fails.
//
//	go run ./cmd/drydock-contract                            # every contract, $1.00 budget
//	go run ./cmd/drydock-contract --cli claude --budget-usd 0.50
//	go run ./cmd/drydock-contract --check-updates            # also compare the pins with the newest npm releases
//	go run ./cmd/drydock-contract --check-updates --latest   # and run the suite against the newer releases
//	go run ./cmd/drydock-contract --list                     # print the catalog
//	go run ./cmd/drydock-contract --install-pinned claude    # npm install -g the pinned claude unless one is on PATH
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/pins"
	"github.com/kjmaguire/drydock/internal/contract"
)

type options struct {
	clis         []string
	out          string
	budget       float64
	latest       bool
	checkUpdates bool
	skipSlow     bool
	list         bool
	installPin   string
	timeout      time.Duration
}

func parseFlags(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("drydock-contract", flag.ContinueOnError)
	cli := fs.String("cli", "", "run only the contracts of these CLIs (comma separated program names or manifest ids: claude, goose, codex, ...)")
	fs.StringVar(&o.out, "out", "contract-report", "directory for contract-report.json, contract-report.md and the raw go test logs")
	fs.Float64Var(&o.budget, "budget-usd", 1.00, "stop starting paid checks once this many US dollars are spent (they use haiku)")
	fs.BoolVar(&o.latest, "latest", false, "install the newest published version of each CLI into a temporary npm prefix and run against it")
	fs.BoolVar(&o.checkUpdates, "check-updates", false, "compare the pinned and installed versions with the newest on npm and print what changed")
	fs.BoolVar(&o.skipSlow, "skip-slow", false, "leave out the checks that take minutes (CLAUDE-MCPWAIT-01)")
	fs.BoolVar(&o.list, "list", false, "print the catalog and exit")
	fs.StringVar(&o.installPin, "install-pinned", "", "npm install -g the version the manifests pin for this CLI unless it is already on PATH, then exit")
	fs.DurationVar(&o.timeout, "timeout", 20*time.Minute, "time limit of each go test invocation")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	for _, c := range strings.Split(*cli, ",") {
		if c = strings.TrimSpace(strings.ToLower(c)); c != "" {
			o.clis = append(o.clis, c)
		}
	}
	if o.budget < 0 {
		return o, errors.New("--budget-usd must not be negative")
	}
	return o, nil
}

func main() {
	o, err := parseFlags(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "drydock-contract:", err)
		os.Exit(2)
	}
	code, err := run(context.Background(), o, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "drydock-contract:", err)
		if code == 0 {
			code = 2
		}
	}
	os.Exit(code)
}

func run(ctx context.Context, o options, w io.Writer) (int, error) {
	all := contract.All()
	if err := contract.Validate(all); err != nil {
		return 2, err
	}
	cs := contract.Filter(all, o.clis)
	if len(cs) == 0 {
		return 2, fmt.Errorf("no contract concerns %v", o.clis)
	}
	if o.list {
		printCatalog(w, cs)
		return 0, nil
	}
	if o.installPin != "" {
		return 0, installPinned(ctx, w, o.installPin)
	}
	root, err := repoRoot()
	if err != nil {
		return 2, err
	}
	out := o.out
	if !filepath.IsAbs(out) {
		wd, _ := os.Getwd()
		out = filepath.Join(wd, out)
	}
	work, err := os.MkdirTemp("", "drydock-contract-")
	if err != nil {
		return 2, err
	}
	defer os.RemoveAll(work)

	names := contract.CLIs(cs)
	selected, err := pinsFor(names, o.clis)
	if err != nil {
		return 2, err
	}
	// what is installed now, before --latest puts anything first on PATH
	clis := detectCLIs(ctx, names)
	var updates []contract.Update
	if o.checkUpdates {
		updates = checkUpdates(ctx, selected, installedVersions(ctx, selected, clis))
		printUpdates(w, updates)
	}
	if o.latest {
		// only what a selected contract runs: the codex CLI, say, is not, because the codex-acp bridge brings its own
		var toInstall []pins.Pin
		for i, p := range selected {
			if !slices.Contains(names, p.Bin) || (o.checkUpdates && !updates[i].Newer) {
				continue
			}
			toInstall = append(toInstall, p)
			if o.checkUpdates {
				updates[i].Tested = true
			}
		}
		if len(toInstall) == 0 {
			fmt.Fprintln(w, "--latest: nothing newer to install; running against what is installed")
		} else {
			bin, err := installLatest(ctx, w, filepath.Join(work, "npm"), toInstall)
			if err != nil {
				return 2, err
			}
			os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			clis = detectCLIs(ctx, names)
			for i := range clis {
				if strings.HasPrefix(clis[i].Path, bin) {
					clis[i].Source = "latest"
				}
			}
		}
	}
	printCLIs(w, clis)

	r := &runner{root: root, out: out, ledger: filepath.Join(work, "spend.jsonl"), budget: o.budget, timeout: o.timeout, skipSlow: o.skipSlow, w: w}
	r.env, r.enclosing = suiteEnv(os.Environ(), r.ledger, o.budget), enclosingVars(os.Environ())
	outcomes, estimated, err := r.runAll(ctx, cs)
	if err != nil {
		return 2, err
	}
	rep := buildReport(cs, clis, outcomes, updates, o, root)
	rep.SpentUSD, _ = contract.LedgerTotal(r.ledger)
	rep.EstimatedUSD = estimated
	rep.Finish()
	if err := rep.Write(out); err != nil {
		return 2, err
	}
	if p := os.Getenv("GITHUB_STEP_SUMMARY"); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.WriteString(rep.Markdown())
			_ = f.Close()
		}
	}
	fmt.Fprintf(w, "\n%d passed, %d failed, %d skipped; spend $%.4f reported", rep.Summary[contract.StatusPass], rep.Summary[contract.StatusFail], rep.Summary[contract.StatusSkip], rep.SpentUSD)
	if rep.EstimatedUSD > 0 {
		fmt.Fprintf(w, " + about $%.2f estimated", rep.EstimatedUSD)
	}
	fmt.Fprintf(w, " (budget $%.2f)\nreport: %s\n", o.budget, filepath.Join(out, "contract-report.md"))
	if rep.Failed() {
		return 1, nil
	}
	return 0, nil
}

// buildReport turns the outcomes into one row per contract.
func buildReport(cs []contract.Contract, clis []contract.CLIInfo, outcomes map[string]contract.Outcome, updates []contract.Update, o options, root string) *contract.Report {
	versions := map[string]string{}
	for _, c := range clis {
		versions[c.Name] = c.Version
	}
	rep := &contract.Report{Generated: time.Now().UTC().Format(time.RFC3339), Platform: runtime.GOOS + "/" + runtime.GOARCH, BudgetUSD: o.budget,
		CLIs: clis, Updates: updates, Command: "drydock-contract " + strings.Join(os.Args[1:], " ")}
	if c, err := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD").Output(); err == nil {
		rep.Commit = strings.TrimSpace(string(c))
	}
	for _, c := range cs {
		var ocs []contract.Outcome
		for _, ref := range c.Tests {
			oc, ok := outcomes[refKey(ref)]
			if !ok {
				oc = contract.NotRun("not run")
			}
			ocs = append(ocs, oc)
		}
		rep.Rows = append(rep.Rows, contract.BuildRow(c, versions[c.CLI], ocs))
	}
	return rep
}

func refKey(r contract.TestRef) string { return r.Package + " " + r.Name() }

func printCatalog(w io.Writer, cs []contract.Contract) {
	for _, c := range cs {
		fmt.Fprintf(w, "%s (%s)\n  %s\n  why: %s\n", c.ID, c.CLI, c.Assumption, c.Why)
		for _, t := range c.Tests {
			cost := "free"
			if t.Paid() {
				cost = fmt.Sprintf("about $%.2f", t.EstUSD)
			}
			if t.Slow {
				cost += ", slow"
			}
			fmt.Fprintf(w, "  test: %s %s (%s)\n", t.Package, t.Name(), cost)
		}
	}
}

// installPinned installs the version the manifests pin for one CLI with `npm install -g`, unless the CLI is already on PATH.
func installPinned(ctx context.Context, w io.Writer, name string) error {
	ps, err := pins.Embedded()
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p.Bin != name && !(p.Kind == pins.KindCLI && p.Adapter == name) {
			continue
		}
		if path, err := exec.LookPath(p.Bin); err == nil {
			v, _ := versionOf(ctx, path)
			fmt.Fprintf(w, "%s is already installed: %s %s (the pin is %s)\n", p.Bin, path, v, p.Version)
			return nil
		}
		spec := p.Package + "@" + p.Version
		fmt.Fprintf(w, "installing %s\n", spec)
		cmd := exec.CommandContext(ctx, "npm", "install", "-g", "--no-audit", "--no-fund", spec)
		var errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = w, &errb
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("npm install -g %s: %v: %s", spec, err, strings.TrimSpace(errb.String()))
		}
		return nil
	}
	return fmt.Errorf("no npm pin for %q in the manifests", name)
}

func repoRoot() (string, error) {
	d, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(d, "internal", "contract")); err == nil {
				return d, nil
			}
		}
		p := filepath.Dir(d)
		if p == d {
			return "", errors.New("run drydock-contract inside the Drydock repository")
		}
		d = p
	}
}

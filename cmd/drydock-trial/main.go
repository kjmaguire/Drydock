// Command drydock-trial runs the head-to-head trial of docs/evals.md section 13: each task of evals/tasks done by Drydock
// (arm D), by plain Claude Code (arm B1) and, when asked, by the naive broadcast team (arm B0, section 5) from the same start
// commit with the same model, judged by hidden acceptance checks, and records every run under evals/results. It spends real
// money on the signed-in Claude account (bounded by -ceiling-usd per run).
//
//	drydock-trial run -tasks saasapi-invoice-rules -k 1 -model haiku
//	drydock-trial run -arms D,B1,B0 -k 3 -model haiku   # the broadcast baseline as well
//	drydock-trial report -trial 2026-10-10-pilot
//	drydock-trial selfcheck
//	drydock-trial redact                  # make the files of runs recorded earlier safe to commit (new runs are written that way)
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/sandbox"
	"github.com/kjmaguire/drydock/internal/sandbox/sandboxtest"
	"github.com/kjmaguire/drydock/internal/skills"
	"github.com/kjmaguire/drydock/internal/trial"
)

func main() {
	// arm D's agents start the gateway's stdio shim as this same executable (docs/mcp.md); without this they get no gateway
	if code, ok := mcp.MaybeRunShim(os.Args); ok {
		os.Exit(code)
	}
	if len(os.Args) < 2 {
		usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var err error
	switch os.Args[1] {
	case "run":
		err = run(ctx, os.Args[2:])
	case "report":
		err = report(os.Args[2:])
	case "selfcheck":
		err = selfcheck(ctx, os.Args[2:])
	case "compare":
		err = compare(os.Args[2:])
	case "redact":
		err = redact(os.Args[2:])
	case "fetch-skills":
		err = fetchSkills(ctx, os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "drydock-trial:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: drydock-trial run|report|compare|selfcheck|redact|fetch-skills [flags] (-h for the flags)")
	os.Exit(2)
}

// repoRoot is the Drydock repository the command runs in.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "evals", "tasks")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("run drydock-trial inside the Drydock repository (no evals/tasks found)")
		}
		dir = parent
	}
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	taskList := fs.String("tasks", "", "comma-separated task ids (default: every task of -suite)")
	suiteName := fs.String("suite", "", "only the tasks of this suite (default: every task in evals/tasks)")
	armList := fs.String("arms", "D,B1", "arms to run: D (Drydock), B1 (plain Claude Code), B0 (the naive broadcast team)")
	b0Rounds := fs.Int("b0-rounds", 5, "B0 only: rounds of the team's free-for-all before it is stopped")
	k := fs.Int("k", 1, "repeats of each (task, arm)")
	model := fs.String("model", "haiku", "the model both arms use (a Claude Code --model value)")
	ceiling := fs.Float64("ceiling-usd", 2, "dollar ceiling per run (B1: --max-budget-usd; B0: the whole team's, each turn's --max-budget-usd what is left; D: a project budget)")
	wall := fs.Duration("wall", 30*time.Minute, "wall-clock cap per run")
	mode := fs.String("d-mode", trial.ModeSandbox, "how D's worker runs commands: sandbox (P30) or checks (P24)")
	image := fs.String("sandbox-image", "", "workspace image for -d-mode sandbox (default: build the offline Go image from this machine)")
	name := fs.String("trial", time.Now().UTC().Format("2006-01-02")+"-trial", "name of this trial (groups its runs in the report)")
	out := fs.String("out", "evals/results", "where run records go (relative to the repository)")
	work := fs.String("work", "", "scratch directory for the runs' repositories (default: a new temporary directory)")
	keep := fs.Bool("keep", false, "keep the runs' repositories and data directories")
	bin := fs.String("claude", "claude", "the claude command")
	total := fs.Float64("total-usd", 10, "stop starting runs once what this invocation spent plus one more ceiling would pass this")
	firstRep := fs.Int("first-repeat", 1, "number of the first repeat (to add repeats to a trial in a later batch)")
	startBefore := fs.Duration("start-before", 0, "start no run once this long has passed since the command started (0: no limit)")
	skillsDir := fs.String("skills-dir", "", "D only: the repository's own skills (<name>/SKILL.md), committed under .aet/skills and accepted for both agents")
	prebuilt := fs.Bool("prebuilt-skills", false, "D only: import the prebuilt agents' skills from their registries at the catalog's pins and accept them")
	allowDirty := fs.Bool("allow-dirty", false, "run from a binary built from a tree with uncommitted changes (its records say +dirty; a trial that is to support a claim is run from a commit)")
	skillMode := fs.String("d-skills", "", "D only, with -skills-dir or -prebuilt-skills: how Claude gets them, all (every turn) or relevant (the default when not given)")
	_ = fs.Parse(args)
	if *skillMode != "" && *skillsDir == "" && !*prebuilt {
		return errors.New("-d-skills goes with -skills-dir or -prebuilt-skills")
	}
	if *skillMode != "" && *skillMode != skills.DeliveryAll && *skillMode != skills.DeliveryRelevant {
		return fmt.Errorf("-d-skills is all or relevant, not %q", *skillMode)
	}
	if *skillsDir != "" {
		abs, err := filepath.Abs(*skillsDir)
		if err != nil {
			return err
		}
		*skillsDir = abs
	}
	began := time.Now()

	root, err := repoRoot()
	if err != nil {
		return err
	}
	tasks, err := pickTasks(root, *suiteName, *taskList)
	if err != nil {
		return err
	}
	var armOrder []string
	for _, a := range strings.Split(*armList, ",") {
		switch a = strings.TrimSpace(a); a {
		case trial.ArmDrydock, trial.ArmB1, trial.ArmB0:
			armOrder = append(armOrder, a)
		default:
			return fmt.Errorf("unknown arm %q (D, B1 or B0)", a)
		}
	}
	if *mode != trial.ModeSandbox && *mode != trial.ModeChecks {
		return fmt.Errorf("-d-mode is sandbox or checks, not %q", *mode)
	}
	if *work != "" && insideRepo(root, *work) {
		return errors.New("-work must be outside the repository: plain Claude Code would see the hidden checks next to its task")
	}
	cli, err := trial.CLIVersion(ctx, *bin)
	if err != nil {
		return err
	}
	info, _ := debug.ReadBuildInfo()
	commit := drydockCommit(root, info)
	if err := refuseDirty(commit, *allowDirty); err != nil {
		return err
	}
	if contains(armOrder, trial.ArmDrydock) && *mode == trial.ModeSandbox && *image == "" {
		eng := sandbox.NewEngine()
		fmt.Println("building the offline Go image for the sandbox ...")
		if *image, err = sandboxtest.GoImage(eng.Bin); err != nil {
			return fmt.Errorf("the sandbox image: %w", err)
		}
	}
	if *work == "" {
		if *work, err = os.MkdirTemp("", "drydock-trial-*"); err != nil {
			return err
		}
	}
	if insideRepo(root, *work) {
		return errors.New("-work must be outside the repository: plain Claude Code would see the hidden checks next to its task")
	}
	outDir := filepath.Join(root, *out)
	fmt.Printf("trial %s: %d task(s) x %v x %d, model %s, ceiling $%.2f, cap %s, %s, Drydock %s\n", *name, len(tasks), armOrder, *k, *model, *ceiling, *wall, cli, commit)
	spent := 0.0
	fetcher := &trial.CachingFetcher{Inner: skills.NewGitHubFetcher()} // the runs import the same pinned skills
	for rep := *firstRep; rep < *firstRep+*k; rep++ {
		for i, t := range tasks {
			order := interleave(armOrder, rep+i)
			for _, arm := range order {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if spent+*ceiling > *total {
					fmt.Printf("stopping: $%.4f spent, and one more run could pass -total-usd $%.2f\n", spent, *total)
					return finish(outDir, *name)
				}
				if *startBefore > 0 && time.Since(began) > *startBefore {
					fmt.Printf("stopping: %s have passed, past -start-before %s; %s %s #%d and the runs after it did not start\n",
						time.Since(began).Round(time.Second), *startBefore, t.ID, arm, rep)
					return finish(outDir, *name)
				}
				dir := filepath.Join(*work, fmt.Sprintf("%s-%s-%d", t.ID, arm, rep))
				rec, extra := runOne(ctx, root, dir, t, arm, *model, *ceiling, *wall, *mode, *image, *bin, skillSetup{dir: *skillsDir, prebuilt: *prebuilt, mode: *skillMode, fetcher: fetcher}, *b0Rounds)
				rec.Schema, rec.Trial, rec.Commit, rec.Repeat, rec.CLI = 1, *name, commit, rep, cli
				rec.Host = runtime.GOOS + "/" + runtime.GOARCH
				where, err := rec.Write(outDir, extra)
				if err != nil {
					return err
				}
				spent += rec.CostUSD
				fmt.Printf("%-24s %-2s #%d  %-8s $%.4f  %s  %s\n", t.ID, arm, rep, rec.Outcome, rec.CostUSD,
					(time.Duration(rec.WallMs) * time.Millisecond).Round(time.Second), rel(root, where))
				if !*keep {
					_ = os.RemoveAll(dir)
				}
			}
		}
	}
	return finish(outDir, *name)
}

// interleave is the arms' order for the n-th (repeat, task) pair, so no arm always runs first (docs/evals.md 5): it turns by one
// each time; with two arms that is the swap on every other pair the trial has always made.
func interleave(arms []string, n int) []string {
	order := make([]string, len(arms))
	for j := range arms {
		order[j] = arms[(j+n+1)%len(arms)]
	}
	return order
}

// finish writes the trial's report next to its runs (every batch so far) and prints it.
func finish(outDir, name string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	recs, err := trial.LoadRecords(outDir)
	if err != nil {
		return err
	}
	var mine []trial.Record
	for _, r := range recs {
		if r.Trial == name {
			mine = append(mine, r)
		}
	}
	md := trial.Report(mine)
	if err := os.WriteFile(filepath.Join(outDir, name+"-report.md"), []byte(md), 0o644); err != nil {
		return err
	}
	fmt.Println()
	fmt.Print(md)
	return nil
}

// skillSetup is how arm D's project gets skills (run's -skills-dir, -prebuilt-skills and -d-skills).
type skillSetup struct {
	dir      string
	prebuilt bool
	mode     string
	fetcher  *trial.CachingFetcher
}

func runOne(ctx context.Context, root, dir string, t trial.Task, arm, model string, ceiling float64, wall time.Duration, mode, image, bin string, sk skillSetup, b0Rounds int) (trial.Record, map[string][]byte) {
	started := time.Now()
	extra := map[string][]byte{}
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return trial.Record{Arm: arm, TaskID: t.ID, Model: model, Outcome: trial.Failed, Reason: err.Error(), StartedAt: started}, extra
	}
	startCommit, err := trial.Prepare(ctx, root, t, repo)
	if err != nil {
		return trial.Record{Arm: arm, TaskID: t.ID, Model: model, Outcome: trial.Failed, Reason: "prepare: " + err.Error(), StartedAt: started}, extra
	}
	var rec trial.Record
	switch arm {
	case trial.ArmB1, trial.ArmB0:
		var transcript bytes.Buffer
		if arm == trial.ArmB1 {
			b := trial.B1{Bin: bin, Model: model, CeilingUSD: ceiling, Wall: wall, Root: root, Hidden: []string{"evals/testdata", "zz_hidden_"}}
			rec = b.Run(ctx, t, repo, &transcript)
		} else {
			b := trial.B0{Bin: bin, Model: model, CeilingUSD: ceiling, Wall: wall, Rounds: b0Rounds, Root: root, Hidden: []string{"evals/testdata", "zz_hidden_"}}
			rec, _ = b.Run(ctx, t, repo, &transcript) // the room is in the transcript, every session's stream one after the other
		}
		extra["transcript.jsonl"] = transcript.Bytes()
		res := trial.Accept(ctx, root, t, repo)
		rec.EvalAccept = &res
		if rec.Outcome == "" {
			rec.Outcome = trial.Rejected
			if res.Passed {
				rec.Outcome = trial.Accepted
			} else {
				rec.Reason = "the hidden acceptance check failed"
			}
		} else {
			rec.Notes = append(rec.Notes, fmt.Sprintf("the hidden check was run on what the session left anyway: passed=%v", res.Passed))
		}
		if d, err := trial.ResultDiff(ctx, root, t, repo); err == nil {
			extra["diff.patch"] = []byte(d)
		}
	case trial.ArmDrydock:
		d := trial.Drydock{Root: root, DataDir: filepath.Join(dir, "data"), Model: model, CeilingUSD: ceiling, Wall: wall, Mode: mode, SandboxImage: image,
			SkillsDir: sk.dir, Prebuilt: sk.prebuilt, Skills: sk.mode}
		if sk.prebuilt {
			d.SkillsFetcher = sk.fetcher
		}
		var art trial.Artifacts
		rec, art = d.Run(ctx, t, repo, func(tree string) trial.AcceptResult {
			if diff, err := trial.ResultDiff(ctx, root, t, tree); err == nil {
				extra["diff.patch"] = []byte(diff)
			}
			return trial.Accept(ctx, root, t, tree)
		})
		if art.Events != nil {
			extra["events.json"] = art.Events
		}
		if art.Usage != nil {
			extra["usage.json"] = art.Usage
		}
		if art.Evidence != nil {
			extra["evidence.json"] = art.Evidence
		}
	}
	rec.StartCommit, rec.StartedAt, rec.TaskClass = startCommit, started, t.Class
	return rec, extra
}

// redact rewrites the transcripts, event and evidence files of the recorded runs so they hold no account, connector or machine
// detail (trial.RedactResults). It changes nothing that is already redacted.
func redact(args []string) error {
	fs := flag.NewFlagSet("redact", flag.ExitOnError)
	dir := fs.String("dir", "evals/results", "where the run records are (relative to the repository)")
	_ = fs.Parse(args)
	root, err := repoRoot()
	if err != nil {
		return err
	}
	n, err := trial.RedactResults(filepath.Join(root, *dir))
	fmt.Printf("%d files rewritten\n", n)
	return err
}

func report(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	dir := fs.String("dir", "evals/results", "where the run records are (relative to the repository)")
	name := fs.String("trial", "", "only this trial (default: every run)")
	_ = fs.Parse(args)
	root, err := repoRoot()
	if err != nil {
		return err
	}
	recs, err := trial.LoadRecords(filepath.Join(root, *dir))
	if err != nil {
		return err
	}
	var keep []trial.Record
	for _, r := range recs {
		if *name == "" || r.Trial == *name {
			keep = append(keep, r)
		}
	}
	fmt.Print(trial.Report(keep))
	return nil
}

// compare prints arm D's variants side by side: one trial per variant (for example skills delivered all or relevant).
func compare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	dir := fs.String("dir", "evals/results", "where the run records are (relative to the repository)")
	names := fs.String("trials", "", "comma-separated trial names, the first is the baseline")
	out := fs.String("out", "", "also write the comparison to this file (relative to -dir)")
	_ = fs.Parse(args)
	if *names == "" {
		return errors.New("-trials is required")
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	recs, err := trial.LoadRecords(filepath.Join(root, *dir))
	if err != nil {
		return err
	}
	var groups []trial.Group
	for _, n := range strings.Split(*names, ",") {
		g := trial.Group{Name: strings.TrimSpace(n)}
		for _, r := range recs {
			if r.Trial == g.Name {
				g.Recs = append(g.Recs, r)
			}
		}
		groups = append(groups, g)
	}
	md := trial.Compare(groups)
	if *out != "" {
		if err := os.WriteFile(filepath.Join(root, *dir, *out), []byte(md), 0o644); err != nil {
			return err
		}
	}
	fmt.Print(md)
	return nil
}

// fetchSkills writes the prebuilt agents' skills, at their pinned commits, into a directory to read them (a run imports
// them itself with -prebuilt-skills).
func fetchSkills(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("fetch-skills", flag.ExitOnError)
	out := fs.String("out", "", "directory to write <name>/SKILL.md into (outside this repository: the texts are third-party)")
	_ = fs.Parse(args)
	if *out == "" {
		return errors.New("-out is required")
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	if insideRepo(root, *out) {
		return errors.New("-out must be outside the repository: the skills are third-party texts under their own licenses")
	}
	names, err := trial.FetchPrebuiltSkills(ctx, skills.NewGitHubFetcher(), *out)
	if err != nil {
		return err
	}
	fmt.Printf("%d skills: %s\n", len(names), strings.Join(names, ", "))
	return nil
}

func selfcheck(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("selfcheck", flag.ExitOnError)
	taskList := fs.String("tasks", "", "comma-separated task ids (default: every task of -suite)")
	suiteName := fs.String("suite", "", "only the tasks of this suite (default: all)")
	_ = fs.Parse(args)
	root, err := repoRoot()
	if err != nil {
		return err
	}
	tasks, err := pickTasks(root, *suiteName, *taskList)
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tasks {
		if err := trial.SelfCheck(ctx, root, t); err != nil {
			errs = append(errs, err)
			fmt.Println("FAIL", t.ID)
			continue
		}
		fmt.Println("ok  ", t.ID, "(the hidden check fails on the fixture and passes on the reference solution)")
	}
	return errors.Join(errs...)
}

// insideRepo reports whether dir is the repository or inside it.
func insideRepo(root, dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, abs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

// pickTasks loads the suite and returns the tasks of suite (all when empty), only those named in list when it is set.
func pickTasks(root, suite, list string) ([]trial.Task, error) {
	all, err := trial.LoadSuite(root, filepath.Join(root, "evals", "tasks"))
	if err != nil {
		return nil, err
	}
	if all, err = trial.InSuite(all, suite); err != nil {
		return nil, err
	}
	var ids []string
	if list != "" {
		ids = strings.Split(list, ",")
	}
	return trial.FindTasks(all, ids)
}

// drydockCommit is the Drydock that arm D runs: the commit this binary was built from (its VCS stamp, "+dirty" when the
// tree it was built from had uncommitted changes). The repository's HEAD is not it once the repository moves on while a
// batch runs; it is the answer only for a binary without a stamp (go run).
func drydockCommit(root string, info *debug.BuildInfo) string {
	var rev string
	var modified bool
	if info != nil {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
	}
	if rev == "" {
		rev = gitOut(root, "rev-parse", "--short=12", "HEAD")
		modified = gitOut(root, "status", "--porcelain", "--untracked-files=no") != ""
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if modified {
		rev += "+dirty"
	}
	return rev
}

func gitOut(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func rel(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil {
		return r
	}
	return p
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// refuseDirty stops a trial whose Drydock was built from uncommitted changes: its records could not say which code ran
// (audit F19: 46 of 69 records of the first hard trial carry +dirty). -allow-dirty runs it anyway, for trying the harness.
func refuseDirty(commit string, allow bool) error {
	if strings.HasSuffix(commit, "+dirty") && !allow {
		return fmt.Errorf("drydock-trial was built from a tree with uncommitted changes (%s): commit them first, or pass -allow-dirty for a run that supports no claim", commit)
	}
	return nil
}

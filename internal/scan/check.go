package scan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/proc"
)

// The `scan` check (decision P131): the security scan's code engine (Opengrep with the built-in rules and the rules folder)
// and its dependency engine (osv-scanner) as a workflow check over a task's worktree, with a verdict like the other checks.
// The secrets engine is not part of it: the `secrets` check already reads every line a task adds.

// CheckName is the check's kind in the evidence and the templates.
const CheckName = "scan"

// CheckOptions configure the `scan` check.
type CheckOptions struct {
	// Paths, BuiltinRules and RulesDir are the Security page's tools and rules, as a project's own scan uses them.
	Paths        Paths
	BuiltinRules string
	RulesDir     string
	// Redact is applied to every title and reason (the app's secret scanner).
	Redact func(string) string
	// Env is the environment the tools get (default: the scrubbed tool environment).
	Env func() []string
	// Added returns the task's diff against its base (git diff): a finding counts only when it is on a line the task added,
	// a dependency finding when the task changed its manifest or lockfile. Nil counts every finding in the tree (the Security
	// template, whose task is the findings already there).
	Added func(ctx context.Context) (string, error)
	// BaseFile returns a file of the repository (a path relative to its root, with forward slashes) as the task's base had it;
	// ok is false when the base has no such file. It tells a dependency finding the task caused from one its manifest or
	// lockfile already had: the same package at the same version in the same file at the base does not count (decision P133).
	// Nil: every dependency finding in a manifest or lockfile the task changed counts.
	BaseFile func(ctx context.Context, path string) (content string, ok bool, err error)
	// Dismissed returns the findings a person dismissed in the project (security.finding.dismiss), by fingerprint: they do not
	// count here either. Nil: none.
	Dismissed func(ctx context.Context) map[string]Dismissal
	// Timeout bounds each engine (0: the scan's own Timeout).
	Timeout time.Duration
}

// CheckSpec is the `scan` check on dir. A tool that is not installed, or rules that cannot be read, leave its engine out and
// say so; when neither engine can run the result is skipped with the reason, never a pass.
func CheckSpec(dir string, o CheckOptions) checks.Spec {
	return checks.Spec{Name: CheckName, Argv: []string{"drydock", "security-scan", "--engines", "code,deps"}, Dir: dir,
		InProcess: func(ctx context.Context, pm proc.Manager, s checks.Spec) (checks.Result, error) {
			return runCheck(ctx, pm, s.Dir, o)
		}}
}

func runCheck(ctx context.Context, pm proc.Manager, dir string, o CheckOptions) (checks.Result, error) {
	var res checks.Result
	res.Name = CheckName
	// the change first: a diff that cannot be read is no reason to run the tools, and judging every finding instead would fail
	// the task for what was there before it
	var scope map[string]map[int]bool
	if o.Added != nil {
		diff, err := o.Added(ctx)
		if err != nil {
			res.Errored, res.ExitCode = true, 2
			res.Output = "the task's change could not be read, so the scan was not judged: " + cleanLine(err.Error(), 200)
			return res, nil
		}
		scope = map[string]map[int]bool{}
		for _, f := range checks.ParseAddedLines(diff) {
			lines := map[int]bool{}
			for _, l := range f.Lines {
				lines[l.No] = true
			}
			scope[f.Path] = lines
		}
	}
	opts := Options{ID: "check", Dir: dir, Procs: pm, Redact: o.Redact, Env: o.Env, Paths: o.Paths, BuiltinRules: o.BuiltinRules,
		RulesDir: o.RulesDir, Engines: []EngineID{SAST, Deps},
		// the worktree is what an agent wrote, its .git file included: git is not asked for its files, as for a folder that
		// came from somewhere else (decision P61)
		NoGit: true}
	opts.Timeout = o.Timeout
	rep := Start(ctx, opts).Wait()
	if rep.Status == StatusCancelled {
		return res, ctx.Err()
	}
	if o.Dismissed != nil {
		ApplyDismissals(&rep, o.Dismissed(ctx))
	}
	var judged, notRun []string
	errored := false
	for _, e := range rep.Engines {
		switch e.Status {
		case Done:
			judged = append(judged, e.Name)
		case Errored:
			errored = true
			notRun = append(notRun, fmt.Sprintf("%s failed: %s", e.Name, firstSentence(e.Reason)))
		default:
			notRun = append(notRun, fmt.Sprintf("%s skipped: %s", e.Name, firstSentence(e.Reason)))
		}
	}
	if len(judged) == 0 {
		reason := strings.Join(notRun, "; ")
		if errored {
			res.Errored, res.ExitCode = true, 2
			res.Output = "the security scan did not run: " + reason
			return res, nil
		}
		res.Skipped, res.ExitCode = true, checks.SkippedExitCode
		res.SkipReason = reason
		res.Output = "skipped: " + reason + ". The Security page installs the tools."
		return res, nil
	}
	var counted []Finding
	outside, dismissed := 0, 0
	for _, f := range rep.Findings {
		switch {
		case f.Dismissed:
			dismissed++
		case scope != nil && !inChange(f, scope):
			outside++
		default:
			counted = append(counted, f)
		}
	}
	already := 0
	if scope != nil {
		var note string
		if counted, already, note = o.withoutWhatTheBaseHad(ctx, opts, counted); note != "" {
			notRun = append(notRun, note)
		}
	}
	var sb strings.Builder
	if len(counted) == 0 {
		res.Passed = true
		if scope != nil {
			fmt.Fprintf(&sb, "%s found nothing in this change.\n", strings.Join(judged, " and "))
		} else {
			fmt.Fprintf(&sb, "%s found nothing.\n", strings.Join(judged, " and "))
		}
	} else {
		res.ExitCode = 1
		fmt.Fprintf(&sb, "%d security finding(s) in this change. Fix each one, or ask the person to dismiss it on the Security page:\n", len(counted))
		for _, f := range counted {
			fmt.Fprintf(&sb, "- %s\n", describe(f))
			res.FailingTests = append(res.FailingTests, checkID(f))
		}
		sort.Strings(res.FailingTests)
	}
	if outside > 0 {
		fmt.Fprintf(&sb, "%d finding(s) elsewhere in the tree are not from this change and do not count.\n", outside)
	}
	if already > 0 {
		fmt.Fprintf(&sb, "%d vulnerable package(s) in a manifest or lockfile this change touched were already vulnerable before it and do not count.\n", already)
	}
	if dismissed > 0 {
		fmt.Fprintf(&sb, "%d finding(s) a person dismissed do not count.\n", dismissed)
	}
	for _, n := range notRun {
		fmt.Fprintf(&sb, "Not checked: %s.\n", strings.TrimSuffix(n, "."))
	}
	res.Output = o.redact(sb.String())
	return res, nil
}

// withoutWhatTheBaseHad takes out of counted the dependency findings the task's base already had: the same package at the same
// version in the same manifest or lockfile. A task that changes one line of a lockfile would otherwise own every vulnerable
// package in it, which it cannot fix. The base's copy of each file the findings are in is written to a folder of its own and
// the dependency engine runs over it. already counts what was taken out; note says why the comparison was not made when it
// was asked for and could not be (every finding then still counts).
func (o CheckOptions) withoutWhatTheBaseHad(ctx context.Context, opts Options, counted []Finding) (kept []Finding, already int, note string) {
	if o.BaseFile == nil {
		return counted, 0, ""
	}
	files := map[string]bool{}
	for _, f := range counted {
		if f.Engine == Deps && f.File != "" {
			files[f.File] = true
		}
	}
	if len(files) == 0 {
		return counted, 0, ""
	}
	fail := func(why string) ([]Finding, int, string) {
		return counted, 0, "not compared with the base: " + why
	}
	dir, err := os.MkdirTemp("", "drydock-scan-base-")
	if err != nil {
		return fail(cleanLine(err.Error(), 160))
	}
	defer os.RemoveAll(dir)
	wrote := 0
	for f := range files {
		rel := filepath.FromSlash(f)
		if !filepath.IsLocal(rel) {
			continue // a path the tool reported outside the tree is not read from the repository
		}
		content, ok, err := o.BaseFile(ctx, f)
		if err != nil {
			return fail("the base's " + f + " could not be read: " + cleanLine(err.Error(), 160))
		}
		if !ok {
			continue // the task added this file: all of its findings are the task's
		}
		dst := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fail(cleanLine(err.Error(), 160))
		}
		if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
			return fail(cleanLine(err.Error(), 160))
		}
		wrote++
	}
	if wrote == 0 {
		return counted, 0, ""
	}
	base := opts
	base.ID, base.Dir, base.Engines = "check-base", dir, []EngineID{Deps}
	rep := Start(ctx, base).Wait()
	var eng *EngineResult
	for i := range rep.Engines {
		if rep.Engines[i].ID == Deps {
			eng = &rep.Engines[i]
		}
	}
	if eng == nil || eng.Status != Done {
		why := "the dependency engine did not run over it"
		if eng != nil && eng.Reason != "" {
			why = firstSentence(eng.Reason)
		}
		return fail("the base could not be scanned (" + why + ")")
	}
	had := map[string]bool{}
	for _, f := range rep.Findings {
		if f.Engine == Deps {
			had[depKey(f)] = true
		}
	}
	for _, f := range counted {
		if f.Engine == Deps && had[depKey(f)] {
			already++
			continue
		}
		kept = append(kept, f)
	}
	return kept, already, ""
}

// depKey names a vulnerable package in a file: the same key at the base and at the head is the same finding.
func depKey(f Finding) string { return f.File + "\x00" + f.Package + "\x00" + f.Version }

func (o CheckOptions) redact(s string) string {
	if o.Redact == nil {
		return s
	}
	return o.Redact(s)
}

// inChange reports whether a finding belongs to the task's change: a code finding on a line the task added, a dependency
// finding in a manifest or lockfile the task changed.
func inChange(f Finding, scope map[string]map[int]bool) bool {
	lines, ok := scope[f.File]
	if !ok {
		return false
	}
	if f.Engine == Deps || f.Line == 0 {
		return true
	}
	end := f.EndLine
	if end < f.Line {
		end = f.Line
	}
	if end-f.Line > 200 {
		end = f.Line + 200
	}
	for n := f.Line; n <= end; n++ {
		if lines[n] {
			return true
		}
	}
	return false
}

// describe is one finding for the agent: what, where, which rule; a dependency with its advisories and the version to take.
func describe(f Finding) string {
	where := f.File
	if f.Line > 0 {
		where = fmt.Sprintf("%s:%d", f.File, f.Line)
	}
	s := fmt.Sprintf("%s %s (%s, %s): %s", f.Severity, where, engineNoun(f.Engine), f.Rule, f.Title)
	if f.Engine == Deps && f.Fixed != "" {
		s += "; upgrade to " + f.Fixed
	}
	return s
}

func checkID(f Finding) string {
	if f.Engine == Deps {
		return f.Package + "@" + f.Version
	}
	return fmt.Sprintf("%s:%d %s", f.File, f.Line, f.Rule)
}

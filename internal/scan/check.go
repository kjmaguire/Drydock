package scan

import (
	"context"
	"fmt"
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
	if dismissed > 0 {
		fmt.Fprintf(&sb, "%d finding(s) a person dismissed do not count.\n", dismissed)
	}
	for _, n := range notRun {
		fmt.Fprintf(&sb, "Not checked: %s.\n", strings.TrimSuffix(n, "."))
	}
	res.Output = o.redact(sb.String())
	return res, nil
}

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

package scan

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/safe"
)

// Options configure one scan.
type Options struct {
	ID        string
	ProjectID string
	By        string
	// Dir is the working tree to scan.
	Dir string
	// Procs starts the external tools (default proc.New()).
	Procs proc.Manager
	// Redact is applied to every title and reason (the app's secret scanner). nil leaves text as it is.
	Redact func(string) string
	// Env is the environment the external tools get (default: the scrubbed tool environment, proc.ToolEnv). Nothing of
	// the app's own environment reaches a tool that reads the whole project.
	Env func() []string
	// Paths say where the tools are (default: osv-scanner and opengrep on PATH).
	Paths Paths
	// BuiltinRules is the folder the built-in rules were written to (WriteBuiltinRules, decision P77); the Code engine runs
	// them on every scan. Empty when they could not be written.
	BuiltinRules string
	// RulesDir is a folder of the person's own Opengrep rules, run as well as the built-in ones; empty runs the built-in
	// rules alone. A folder that cannot be used is left out (the page says why).
	RulesDir string
	// NoGit keeps the tools from running git in Dir (decision P61): Opengrep then walks the folder itself instead of asking
	// `git ls-files`, so a .git/config that came with a downloaded folder (core.fsmonitor and the like) can run nothing.
	// Folder scans set it; a project's own working tree keeps git's view of its files.
	NoGit bool
	// Timeout per engine (default Timeout).
	Timeout time.Duration
	// Engines, when set, are the only engines this scan runs and reports (the `scan` check runs the code and dependency
	// engines; the `secrets` check already reads what a task added). Empty runs all three.
	Engines []EngineID
	// OnChange, when set, is called after an engine changes state (started, finished, skipped, errored) with a copy of the
	// report and that engine's id, and once more when the scan ends (engine ""). Progress counters do not call it: read
	// Snapshot for those.
	OnChange func(rep Report, engine EngineID)
}

// ruleSources are the rule folders Opengrep is given: the built-in rules, then the person's own folder when it can be used.
func (o *Options) ruleSources() []string {
	var out []string
	if o.BuiltinRules != "" {
		out = append(out, o.BuiltinRules)
	}
	if ok, _ := checkRules(o.RulesDir); ok {
		out = append(out, o.RulesDir)
	}
	return out
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func (o *Options) redact(s string) string {
	if o.Redact == nil {
		return s
	}
	return o.Redact(s)
}

func (o *Options) env() []string {
	var env []string
	if o.Env != nil {
		env = o.Env()
	} else {
		env = proc.ToolEnv(os.Environ(), nil)
	}
	// a tool that asks git for the project's files (Opengrep's `git ls-files`) must not run what a changed repository
	// configuration names
	return append(env[:len(env):len(env)], proc.GitConfigEnv()...)
}

// runs reports whether the scan runs the engine.
func (o *Options) runs(id EngineID) bool {
	if len(o.Engines) == 0 {
		return true
	}
	for _, e := range o.Engines {
		if e == id {
			return true
		}
	}
	return false
}

func (o *Options) timeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return Timeout
}

// Run is a scan in progress or finished. Snapshot is safe to call from any goroutine.
type Run struct {
	o      Options
	cancel context.CancelFunc
	done   chan struct{}

	mu  sync.Mutex
	rep Report
}

// Start begins the three engines at once and returns at once; Wait or Done tell when they are finished.
func Start(ctx context.Context, o Options) *Run {
	if o.Procs == nil {
		o.Procs = proc.New()
	}
	cctx, cancel := context.WithCancel(ctx)
	r := &Run{o: o, cancel: cancel, done: make(chan struct{})}
	r.rep = Report{ID: o.ID, ProjectID: o.ProjectID, Dir: o.Dir, Status: StatusRunning, By: o.By, StartedAt: time.Now().UnixMilli(), Findings: []Finding{}}
	for _, e := range Engines {
		if o.runs(e.ID) {
			r.rep.Engines = append(r.rep.Engines, EngineResult{ID: e.ID, Name: e.Name, Tool: e.Tool, Status: Pending})
		}
	}
	safe.Go("scan.Start", func() { r.run(cctx) })
	return r
}

// Cancel stops the engines; the report ends cancelled.
func (r *Run) Cancel() { r.cancel() }

// Done is closed when the scan has finished (done or cancelled).
func (r *Run) Done() <-chan struct{} { return r.done }

// Wait blocks until the scan finished and returns the final report.
func (r *Run) Wait() Report {
	<-r.done
	return r.Snapshot()
}

// Snapshot is a copy of the report as it stands.
func (r *Run) Snapshot() Report {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rep.copy()
}

// Clone is a copy of the report that shares nothing it can change (its findings and engines) with it.
func (rep Report) Clone() Report { return rep.copy() }

func (rep Report) copy() Report {
	out := rep
	out.Engines = append([]EngineResult(nil), rep.Engines...)
	out.Findings = append([]Finding{}, rep.Findings...)
	return out
}

func (r *Run) engine(id EngineID) *EngineResult {
	for i := range r.rep.Engines {
		if r.rep.Engines[i].ID == id {
			return &r.rep.Engines[i]
		}
	}
	return nil
}

// set changes an engine under the lock and tells OnChange.
func (r *Run) set(id EngineID, f func(e *EngineResult)) {
	r.mu.Lock()
	f(r.engine(id))
	rep := r.rep.copy()
	r.mu.Unlock()
	if r.o.OnChange != nil {
		r.o.OnChange(rep, id)
	}
}

// note changes an engine under the lock without telling OnChange (a version read, a count): nothing a page must react to.
func (r *Run) note(id EngineID, f func(e *EngineResult)) {
	r.mu.Lock()
	f(r.engine(id))
	r.mu.Unlock()
}

func (r *Run) progress(id EngineID, done, total int) {
	r.mu.Lock()
	e := r.engine(id)
	e.Done, e.Total = done, total
	r.mu.Unlock()
}

// finish records an engine's outcome and its findings (capped at MaxFindings across the report).
func (r *Run) finish(id EngineID, fs []Finding, status EngineStatus, reason, version string) {
	r.mu.Lock()
	e := r.engine(id)
	e.Status, e.Reason, e.FinishedAt, e.Findings = status, r.o.redact(reason), time.Now().UnixMilli(), len(fs)
	if version != "" {
		e.Version = version
	}
	if status == Done && e.Total > 0 {
		e.Done = e.Total // a cancelled or errored engine keeps the count it reached
	}
	for _, f := range fs {
		r.rep.Counts.Total++
		switch f.Severity {
		case Critical:
			r.rep.Counts.Critical++
		case High:
			r.rep.Counts.High++
		case Medium:
			r.rep.Counts.Medium++
		case Low:
			r.rep.Counts.Low++
		default:
			r.rep.Counts.Info++
		}
		if len(r.rep.Findings) < MaxFindings {
			r.rep.Findings = append(r.rep.Findings, f)
		} else {
			r.rep.Truncated = true
		}
	}
	rep := r.rep.copy()
	r.mu.Unlock()
	if r.o.OnChange != nil {
		r.o.OnChange(rep, id)
	}
}

func (r *Run) run(ctx context.Context) {
	defer close(r.done)
	defer r.cancel()
	var wg sync.WaitGroup
	start := func(id EngineID) {
		r.set(id, func(e *EngineResult) { e.Status, e.StartedAt = Running, time.Now().UnixMilli() })
	}
	// probe reads a tool's --version once the engine is running, so the page never shows a tool as waiting while it runs
	probe := func(id EngineID, path string, tool Tool, name string) {
		v := toolVersion(ctx, r.o.Procs, r.o.env(), path, append(append([]string(nil), tool.Args...), "--version"), name)
		r.note(id, func(e *EngineResult) { e.Version = v })
	}
	launch := func(id EngineID, name string, f func()) {
		if !r.o.runs(id) {
			return
		}
		wg.Add(1)
		safe.Go(name, func() {
			defer wg.Done()
			f()
		})
	}
	launch(Secrets, "scan.Run.run", func() {
		start(Secrets)
		r.note(Secrets, func(e *EngineResult) { e.Version = gitleaksVersion() })
		fs, unread, err := scanSecrets(ctx, r.o.Dir, r.o.redact, func(d, t int) { r.progress(Secrets, d, t) })
		r.note(Secrets, func(e *EngineResult) { e.Unread = unread })
		switch {
		case ctx.Err() != nil:
			r.finish(Secrets, fs, Cancelled, "", "")
		case err != nil:
			r.finish(Secrets, nil, Errored, "the secrets engine could not read the tree: "+cleanLine(err.Error(), 160), "")
		case unread > 0:
			r.finish(Secrets, fs, Done, fmt.Sprintf("%d folder(s) or file(s) could not be read and were not scanned.", unread), "")
		default:
			r.finish(Secrets, fs, Done, "", "")
		}
	})
	launch(Deps, "scan.Run.run#2", func() {
		path, _, err := r.o.Paths.OSV.resolve("osv-scanner", r.o.Paths.LookPath)
		if err != nil {
			r.finish(Deps, nil, Skipped, "osv-scanner is not installed, so no dependency was checked. "+osvInstall, "")
			return
		}
		start(Deps)
		probe(Deps, path, r.o.Paths.OSV, "osv-scanner")
		fs, st, reason := scanDeps(ctx, &r.o, path, r.o.Paths.OSV)
		r.finish(Deps, fs, st, reason, "")
	})
	launch(SAST, "scan.Run.run#3", func() {
		path, _, err := r.o.Paths.Opengrep.resolve("opengrep", r.o.Paths.LookPath)
		if err != nil {
			r.finish(SAST, nil, Skipped, "Opengrep is not installed, so no code was checked. "+opengrepInstall, "")
			return
		}
		if len(r.o.ruleSources()) == 0 {
			reason := "The built-in rules could not be written to the app's data folder, and no folder of your own rules is chosen."
			if strings.TrimSpace(r.o.RulesDir) != "" {
				_, note := checkRules(r.o.RulesDir)
				reason = "The built-in rules could not be written to the app's data folder, and your rules folder cannot be used: " + lowerFirst(note)
			}
			r.finish(SAST, nil, Skipped, reason+rulesHelp, "")
			return
		}
		start(SAST)
		probe(SAST, path, r.o.Paths.Opengrep, "opengrep")
		fs, st, reason, version := scanCode(ctx, &r.o, path, r.o.Paths.Opengrep)
		r.finish(SAST, fs, st, reason, version)
	})
	wg.Wait()
	r.mu.Lock()
	r.rep.FinishedAt = time.Now().UnixMilli()
	r.rep.Status = StatusDone
	if ctx.Err() != nil {
		r.rep.Status = StatusCancelled
	}
	sortFindings(r.rep.Findings)
	numberFingerprints(r.rep.Findings)
	r.rep.Summary = summarize(r.rep)
	rep := r.rep.copy()
	r.mu.Unlock()
	if r.o.OnChange != nil {
		r.o.OnChange(rep, "")
	}
}

var severityRank = map[Severity]int{Critical: 0, High: 1, Medium: 2, Low: 3, Info: 4}

// sortFindings orders by severity, then file, then line, then rule.
func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if severityRank[a.Severity] != severityRank[b.Severity] {
			return severityRank[a.Severity] < severityRank[b.Severity]
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Rule < b.Rule
	})
}

// summarize is the report's one sentence: how long it took, what each engine found, and which engines did not run.
func summarize(rep Report) string {
	d := time.Duration(rep.FinishedAt-rep.StartedAt) * time.Millisecond
	if rep.Status == StatusCancelled {
		return fmt.Sprintf("Cancelled after %s.", durText(d))
	}
	var found, notRun []string
	for _, e := range rep.Engines {
		switch e.Status {
		case Done:
			found = append(found, plural(e.Findings, engineNoun(e.ID)))
			if e.Unread > 0 {
				notRun = append(notRun, fmt.Sprintf("the %s engine could not read %d folder(s) or file(s)", e.Name, e.Unread))
			}
		case Skipped:
			notRun = append(notRun, fmt.Sprintf("the %s engine was skipped (%s)", e.Name, strings.TrimSuffix(firstSentence(e.Reason), ".")))
		case Errored:
			notRun = append(notRun, fmt.Sprintf("the %s engine failed (%s)", e.Name, strings.TrimSuffix(firstSentence(e.Reason), ".")))
		}
	}
	s := fmt.Sprintf("Scanned in %s", durText(d))
	if len(found) > 0 {
		s += ": " + strings.Join(found, ", ")
	}
	if len(notRun) > 0 {
		s += "; " + strings.Join(notRun, "; ")
	}
	if n := dismissedNote(rep.Dismissed); n != "" {
		s += "; " + n
	}
	return s + "."
}

func engineNoun(id EngineID) string {
	switch id {
	case Secrets:
		return "secret"
	case Deps:
		return "vulnerable package"
	}
	return "code finding"
}

func plural(n int, noun string) string {
	if n == 0 {
		return "no " + noun + "s"
	}
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i]
	}
	return s
}

func durText(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1f s", d.Seconds())
	}
	return fmt.Sprintf("%d min %d s", int(d.Minutes()), int(d.Seconds())%60)
}

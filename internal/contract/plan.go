package contract

import (
	"regexp"
	"sort"
	"strings"
)

// Group is one `go test` invocation: one top-level test of one package, with the subtests the selected contracts need (all
// of them when Subtests is empty). Running each test on its own lets the runner check the budget between paid tests.
type Group struct {
	Package  string
	Test     string
	Subtests []string // empty: the whole test
	EstUSD   float64  // expected spend of this invocation
	Env      []string // union of the references' environment
	Slow     bool     // one of the references is a slow check
	// Enclosing keeps the enclosing Claude Code session's variables in this test's environment (TestRef.Enclosing).
	Enclosing bool
	order     int
}

// Pattern is the -run expression for the group.
func (g Group) Pattern() string {
	p := "^" + regexp.QuoteMeta(g.Test) + "$"
	if len(g.Subtests) == 0 {
		return p
	}
	subs := make([]string, len(g.Subtests))
	for i, s := range g.Subtests {
		subs[i] = regexp.QuoteMeta(strings.ReplaceAll(s, " ", "_"))
	}
	return p + "/^(" + strings.Join(subs, "|") + ")$"
}

// Paid reports whether the invocation is expected to spend money.
func (g Group) Paid() bool { return g.EstUSD > 0 }

// Plan groups the tests of the contracts into invocations: free checks first, then paid ones in catalog order, each test once
// even when several contracts refer to it.
func Plan(cs []Contract) []Group {
	type acc struct {
		g     Group
		whole bool
		est   map[string]float64 // per Name(): the highest estimate any reference gives
		subs  map[string]bool
		env   map[string]bool
	}
	byKey := map[string]*acc{}
	var keys []string
	for _, c := range cs {
		for _, r := range c.Tests {
			k := r.Package + " " + r.Test
			a := byKey[k]
			if a == nil {
				a = &acc{g: Group{Package: r.Package, Test: r.Test, order: len(keys)}, est: map[string]float64{}, subs: map[string]bool{}, env: map[string]bool{}}
				byKey[k] = a
				keys = append(keys, k)
			}
			if r.Subtest == "" {
				a.whole = true
			} else {
				a.subs[r.Subtest] = true
			}
			if r.EstUSD > a.est[r.Name()] {
				a.est[r.Name()] = r.EstUSD
			}
			a.g.Slow = a.g.Slow || r.Slow
			a.g.Enclosing = a.g.Enclosing || r.Enclosing
			for _, kv := range r.Env {
				a.env[kv] = true
			}
		}
	}
	out := make([]Group, 0, len(keys))
	for _, k := range keys {
		a := byKey[k]
		g := a.g
		var subsEst, wholeEst float64
		for name, e := range a.est {
			if name == g.Test {
				wholeEst = e
			} else {
				subsEst += e
			}
		}
		g.EstUSD = max(wholeEst, subsEst)
		if !a.whole {
			for s := range a.subs {
				g.Subtests = append(g.Subtests, s)
			}
			sort.Strings(g.Subtests)
		}
		for kv := range a.env {
			g.Env = append(g.Env, kv)
		}
		sort.Strings(g.Env)
		out = append(out, g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Paid() != out[j].Paid() {
			return !out[i].Paid()
		}
		return out[i].order < out[j].order
	})
	return out
}

// Outcome is what happened to one test reference in a run.
type Outcome struct {
	Status   string   // pass, fail, skip, missing
	Elapsed  float64  // seconds
	Reason   string   // why it skipped (or did not run)
	Failure  string   // the end of a failing test's output
	Notes    []string // contract-note lines
	Warnings []string // contract-warning lines
	SpendUSD *float64 // what the test reported spending (the ledger); nil when it reported nothing
	Ran      bool     // the test started (it was not skipped by the runner)
}

// OutcomeOf maps one reference to its result in a run.
func OutcomeOf(run *Run, r TestRef, spend map[string]float64) Outcome {
	o := Outcome{Ran: true}
	if s, ok := spend[r.Name()]; ok {
		o.SpendUSD = &s
	}
	tr, ok := run.Lookup(r.Name())
	if !ok {
		o.Status = StatusMissing
		switch {
		case run.BuildFailed || run.Package == StatusFail:
			o.Failure = Excerpt(run.PackageOutput, 20, 2000)
			o.Reason = "the package did not build or failed outside the test"
		default:
			o.Reason = r.Name() + " did not run: no test by that name in " + r.Package
		}
		return o
	}
	o.Status, o.Elapsed = tr.Status, tr.Elapsed
	o.Notes = Tagged(tr.Output, NotePrefix)
	o.Warnings = Tagged(tr.Output, WarningPrefix)
	switch tr.Status {
	case StatusSkip:
		o.Reason = Reason(tr.Output)
		o.Ran = false
	case StatusFail:
		o.Failure = Excerpt(tr.Output, 20, 2000)
	}
	return o
}

// NotRun is the outcome of a reference the runner did not start (budget spent, wrong platform).
func NotRun(reason string) Outcome { return Outcome{Status: StatusSkip, Reason: reason} }

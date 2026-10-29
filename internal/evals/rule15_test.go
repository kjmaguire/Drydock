package evals_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/evals"
)

// Token rule 15 over the eval corpus: a usage row whose purpose is a deterministic job breaks the rule and is counted by purpose;
// the sanctioned uses (a consult, a summary, the router's tiebreak) and agent turns do not.
func TestRule15HoldsOnlyWithoutModelCallsForDeterministicJobs(t *testing.T) {
	w := newWorld(t)
	w.seed() // agent turns, reviews and overhead rows of a small cohort
	w.usage(use{task: "T1", purpose: "consult", in: 3, out: 1})
	w.usage(use{task: "T1", purpose: "route_tiebreak", in: 3, out: 1})
	rep, err := evals.Build(w.ctx, w.db, evals.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Rules.Holds || rep.Rules.ModelCalls != 0 || !strings.Contains(rep.Markdown(), "Holds: no usage row in the window") {
		t.Fatalf("sanctioned uses broke the rule: %+v", rep.Rules)
	}
	w.usage(use{task: "T1", purpose: "watchdog", in: 3, out: 1})
	w.usage(use{task: "T1", purpose: "watchdog", in: 3, out: 1})
	w.usage(use{task: "T1", purpose: "check_summary", in: 3, out: 1})
	if rep, err = evals.Build(w.ctx, w.db, evals.Options{}); err != nil {
		t.Fatal(err)
	}
	if rep.Rules.Holds || rep.Rules.ModelCalls != 3 || rep.Rules.ByPurpose["watchdog"] != 2 || rep.Rules.ByPurpose["check_summary"] != 1 {
		t.Fatalf("%+v", rep.Rules)
	}
	if md := rep.Markdown(); !strings.Contains(md, "**Does not hold**: 3 usage row(s)") || !strings.Contains(md, "| watchdog | 2 |") {
		t.Fatalf("markdown:\n%s", md)
	}
	// another project's rows are not this project's
	if rep, err = evals.Build(w.ctx, w.db, evals.Options{ProjectID: "p2"}); err != nil || !rep.Rules.Holds {
		t.Fatalf("project filter: %+v %v", rep.Rules, err)
	}
}

// No Go code in the product names a deterministic job as the purpose of a model call, so the assertion above can only fail on a
// call nobody meant to make.
func TestNoCodeAsksAModelForADeterministicJob(t *testing.T) {
	re := regexp.MustCompile(`[Pp]urpose\w*\s*[:=]\s*"([a-z_]+)"`)
	seen := 0
	err := filepath.WalkDir(filepath.Join("..", ".."), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == "ui") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			seen++
			if slices.Contains(evals.DeterministicPurposes, m[1]) {
				t.Errorf("%s asks a model for %q, a job rules do", p, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatal("no purpose found in the code: the scan is looking in the wrong place")
	}
}

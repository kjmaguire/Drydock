package pipeline

import (
	"context"
	"encoding/json"

	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The review step's findings about how the acceptance criteria are written (decision P128): a criterion without an id, and one
// that names a check the project does not run. Each is recorded once per task as review_finding evidence without a verdict, so it
// blocks nothing (the review's own verdict and the criteria that have ids go on as before), with a task.review_finding event; the
// task page and the approval show it with the rest of the evidence.

// projectChecks are the checks the project runs for a task: what Verify detects in the worktree, the secrets and dependency
// checks the project switched on, end-to-end and preview when the project set them up, and any other check kind the task already
// has a result of (the CI of a pull request).
func (d Deps) projectChecks(ctx context.Context, t tasks.Task, ev []tasks.Evidence) []string {
	var out []string
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	if t.Worktree != "" {
		for _, s := range d.detect()(t.Worktree) {
			add(s.Name)
		}
	}
	if d.Tasks != nil && d.Tasks.DB != nil {
		cs := checks.LoadSettings(ctx, d.Tasks.DB, t.ProjectID)
		if cs.Secrets {
			add("secrets")
		}
		if cs.Deps {
			add("deps")
		}
		ds := LoadDevSettings(ctx, d.Tasks.DB, t.ProjectID)
		if ds.E2E.Enabled {
			add("e2e")
		}
		if ds.Preview.Enabled {
			add("preview")
		}
	}
	for _, e := range ev {
		if CheckKinds[e.Kind] && e.Kind != "merge-conflicts" {
			add(e.Kind)
		}
	}
	return out
}

// recordAcceptanceFindings records the findings about the task's criteria that are not recorded yet, and returns all of them.
func (d Deps) recordAcceptanceFindings(ctx context.Context, t tasks.Task, ev []tasks.Evidence) []AcceptanceFinding {
	fs := AcceptanceFindings(t.Acceptance, d.projectChecks(ctx, t, ev))
	for _, f := range fs {
		data, _ := json.Marshal(f)
		have := false
		for _, e := range ev {
			have = have || e.Kind == ReviewFindingKind && e.Summary == f.Text
		}
		if have {
			continue
		}
		if _, err := d.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: ReviewFindingKind, Summary: f.Text, Data: data}); err == nil {
			d.emit(ctx, "task.review_finding", t, map[string]any{"kind": f.Kind, "severity": f.Severity, "criterion": f.Criterion, "check": f.Check, "text": f.Text})
		}
	}
	return fs
}

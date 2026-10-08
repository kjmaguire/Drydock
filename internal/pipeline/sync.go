package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The "sync" step keeps parallel work from landing broken (docs/parallel-work.md).
//
// Tasks run on their own branches, made from the base branch as it was when each task started. When another task merges first,
// the base moves, and two changes that each passed their checks alone can break each other once both are in. So before a task
// is reviewed, and again before it is merged, its branch is brought up to date with the base branch and the checks run again on
// the result. What is merged is then exactly what was checked on top of the latest base.
//
//   - up to date: nothing to do, on to review (or, at merge time, merge).
//   - brought in cleanly: the branch changed, so the checks run again (route "changed").
//   - conflicts: the merge is left in progress in the worktree with conflict markers, and the agent that owns the task resolves
//     them (route "conflict"); the checks refuse a tree that still has markers.

// SyncRoutes are the routes of the sync node.
const (
	RouteCurrent  = "current"
	RouteChanged  = "changed"
	RouteConflict = "conflict"
	RouteStale    = "stale"
	// RouteApproved: the branch is current and its change is exactly the one a human already approved, so it goes straight to merge.
	RouteApproved = "approved"
)

func baseTarget(t tasks.Task, pi ProjectInfo) string {
	if pi.DefaultBranch != "" {
		return pi.DefaultBranch
	}
	return t.BaseBranch
}

// Sync brings the base branch into the task's branch.
func Sync(d Deps, project func(ctx context.Context, projectID string) (ProjectInfo, error)) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, d, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		repo, err := d.Repo(ctx, t.ProjectID)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		pi, err := project(ctx, t.ProjectID)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		target := baseTarget(t, pi)
		w := worktreeOf(t)
		route := func(r string, v any) (workflow.NodeOutput, error) {
			b, _ := json.Marshal(v)
			return workflow.NodeOutput{Value: b, Route: r}, nil
		}
		// back to work: a task in REVIEW or VERIFICATION goes back to IN_PROGRESS
		reopen := func(reason string) error {
			if t.Status == tasks.InProgress {
				return nil
			}
			_, err := d.Tasks.Transition(ctx, t.ID, tasks.InProgress, tasks.TransitionOpts{By: "system", Reason: reason, Resync: true})
			return err
		}

		// a merge left in progress by an earlier pass that the agent did not finish
		if repo.MergeInProgress(ctx, w) {
			if left := repo.UnresolvedConflicts(ctx, w); len(left) > 0 {
				if err := reopen("conflicts with " + target + " are not resolved"); err != nil {
					return workflow.NodeOutput{}, err
				}
				return route(RouteConflict, map[string]any{"conflict": map[string]any{"files": left, "target": target}})
			}
		}
		behind, err := repo.Behind(ctx, w, target)
		if err != nil {
			return workflow.NodeOutput{}, fmt.Errorf("compare with %s: %w", target, err)
		}
		current := func() (workflow.NodeOutput, error) {
			if patch, perr := repo.PatchID(ctx, w, target); perr == nil {
				if by, ok := approvalFor(ctx, d, t.ID, patch); ok {
					// the base moved after a human approved this very change, it merged cleanly and the checks passed again
					if t.Status == tasks.Review {
						if _, err := d.Tasks.Transition(ctx, t.ID, tasks.Verification, tasks.TransitionOpts{By: "system", Reason: "the change is the one " + by + " approved; the base moved and the checks pass again"}); err != nil {
							return workflow.NodeOutput{}, err
						}
					}
					d.emit(ctx, "task.reapproved_unchanged", t, map[string]any{"approvedBy": by, "target": target})
					return route(RouteApproved, map[string]any{"target": target, "approvedBy": by})
				}
			}
			return route(RouteCurrent, map[string]any{"target": target, "behind": 0})
		}
		if behind == 0 {
			return current()
		}
		changed, conflicts, err := repo.MergeIn(ctx, w, target)
		if err != nil {
			return workflow.NodeOutput{}, fmt.Errorf("bring %s into %s: %w", target, t.Branch, err)
		}
		if !changed {
			return current()
		}
		if len(conflicts) > 0 {
			d.emit(ctx, "task.base_conflict", t, map[string]any{"target": target, "files": conflicts, "behind": behind})
			_, _ = d.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: "sync", Summary: fmt.Sprintf("%s moved ahead by %d commit(s) and conflicts in %s", target, behind, strings.Join(conflicts, ", "))})
			if err := reopen(target + " moved ahead and conflicts with this task"); err != nil {
				return workflow.NodeOutput{}, err
			}
			return route(RouteConflict, map[string]any{"conflict": map[string]any{"files": conflicts, "target": target}})
		}
		d.emit(ctx, "task.base_merged", t, map[string]any{"target": target, "behind": behind})
		_, _ = d.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: "sync", Summary: fmt.Sprintf("brought %d commit(s) of %s into the branch; checks run again", behind, target)})
		if err := reopen(target + " moved ahead; checks run again on the result"); err != nil {
			return workflow.NodeOutput{}, err
		}
		return route(RouteChanged, map[string]any{"target": target, "behind": behind})
	}
}

// conflictPrompt is what the worker is told when the base branch conflicts with its change.
func conflictPrompt(files []string, target string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Another task was merged into %s while you worked, and it changes the same code as your task. Your branch has been brought up to date, and these files now contain conflict markers (<<<<<<<, =======, >>>>>>>):\n\n", target)
	for _, f := range files {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	b.WriteString("\nEdit each file so that it keeps what BOTH changes were for, and remove every marker line. Do not discard the other change, and do not run git. Then stop.")
	return b.String()
}

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Two person-only actions of the run page (decision P124, docs/ui.md 6.2):
//
//   - Restore a safe point: put the task's worktree back to a safe point recorded before an agent turn
//     (refs/drydock/safe/<task>/<run>/<n>), the files that will change shown first, the current state kept under an undo ref
//     of its own so the restore can itself be undone; refused while an agent turn runs in the worktree. Time travel can do
//     it on the way ("also put the worktree's files back to this point"), with the safe point recorded right after the
//     checkpoint. The engine never resets a worktree: this is the person's action (brief 13).
//   - Run again: a task that failed or was cancelled is run again as a new task with the same work (title, description,
//     criteria, template, worker and reviewer), from its workflow's first node in a fresh worktree from the base branch; the
//     ended task, its runs and its records stay as they ended. Its state machine never leaves FAILED, CANCELLED or COMPLETED
//     (brief 12, docs/state-machines.md), and the evals count cost per task (an accepted task's cost must stay that task's),
//     so "again" is a new task that says which one it runs again (Origin "again:<task>"), not the old one reopened. A
//     completed task is not run again: its work was merged; more work on it is a new task.

// RestorePreview is what restoring a safe point would do now.
type RestorePreview struct {
	Ref     string           `json:"ref"`
	Changes []git.FileChange `json:"changes"`
	// Allowed is false while the restore is refused; Why says why.
	Allowed bool   `json:"allowed"`
	Why     string `json:"why,omitempty"`
}

// RestoreResult is a restore that was made.
type RestoreResult struct {
	Ref     string `json:"ref"`
	UndoRef string `json:"undoRef"` // the state before the restore: restoring it undoes the restore
	Changed int    `json:"changed"`
}

// RunAgainResult is the new task a person ran an ended one again as.
type RunAgainResult struct {
	TaskID string `json:"taskId"`
	Seq    int    `json:"seq"`
	RunID  string `json:"runId"`
	From   string `json:"from"`
}

// restoreRefusal says why the task's worktree cannot be put back now ("" when it can).
func (c *Core) restoreRefusal(ctx context.Context, t tasks.Task) string {
	if t.Worktree == "" {
		return "The task has no worktree yet."
	}
	if st, err := os.Stat(t.Worktree); err != nil || !st.IsDir() {
		return "The task's worktree is gone, so there is nothing to put back."
	}
	if t.RunID != "" {
		if info, err := c.Engine.Status(ctx, workflow.RunID(t.RunID)); err == nil {
			switch info.Status {
			case workflow.StatusRunning, workflow.StatusRecovering, workflow.StatusPending:
				return "The task's run is working in its worktree: pause the task or wait for the run to stop first."
			}
		}
	}
	var working int
	_ = c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_runs WHERE task_id=? AND status='WORKING'`, t.ID).Scan(&working)
	if working > 0 {
		return "An agent turn is running in the task's worktree: wait for it to end."
	}
	return ""
}

// restoreTarget checks that ref is one of the task's safe points and returns the task's repository and worktree.
func (c *Core) restoreTarget(ctx context.Context, t tasks.Task, ref string) (*git.Repo, git.Worktree, error) {
	if !git.IsSafePointRef(t.ID, ref) {
		return nil, git.Worktree{}, fmt.Errorf("%q is not a safe point of task #%d", ref, t.Seq)
	}
	r, err := c.repo(ctx, t.ProjectID)
	if err != nil {
		return nil, git.Worktree{}, err
	}
	return r, git.Worktree{Path: t.Worktree, Branch: t.Branch, Base: t.BaseBranch, TaskID: t.ID}, nil
}

// RestorePreview lists the files restoring safe point ref of a run of task taskID would change, and whether it may be done now.
func (c *Core) RestorePreview(ctx context.Context, taskID, runID, ref string) (RestorePreview, error) {
	t, _, err := c.taskRun(ctx, taskID, runID)
	if err != nil {
		return RestorePreview{}, err
	}
	out := RestorePreview{Ref: ref, Changes: []git.FileChange{}}
	r, w, err := c.restoreTarget(ctx, t, ref)
	if err != nil {
		return out, err
	}
	out.Why = c.restoreRefusal(ctx, t)
	if st, err := os.Stat(w.Path); w.Path != "" && err == nil && st.IsDir() {
		if out.Changes, err = r.SafePointChanges(ctx, w, ref); err != nil {
			return out, err
		}
	}
	out.Allowed = out.Why == ""
	return out, nil
}

// RestoreSafePoint puts the worktree of task taskID back to safe point ref (of its run runID), on a person's word.
func (c *Core) RestoreSafePoint(ctx context.Context, taskID, runID, ref, by string) (RestoreResult, error) {
	if !isPerson(by) {
		return RestoreResult{}, errors.New("only a person puts a worktree's files back")
	}
	if err := c.RequireLicensed(ctx, "start_task"); err != nil {
		return RestoreResult{}, err
	}
	ctx, unlock := c.lockTaskStart(ctx, taskID)
	defer unlock()
	t, _, err := c.taskRun(ctx, taskID, runID)
	if err != nil {
		return RestoreResult{}, err
	}
	return c.restoreTo(ctx, t, runID, ref, by, "")
}

// restoreTo makes the restore: refused while an agent turn runs in the worktree; the state before kept under an undo ref; an
// event, a line in the task channel and an audit row. why names what asked for it ("" for the restore action itself).
func (c *Core) restoreTo(ctx context.Context, t tasks.Task, runID, ref, by, why string) (RestoreResult, error) {
	if refusal := c.restoreRefusal(ctx, t); refusal != "" {
		return RestoreResult{}, errors.New(refusal)
	}
	r, w, err := c.restoreTarget(ctx, t, ref)
	if err != nil {
		return RestoreResult{}, err
	}
	changes, err := r.SafePointChanges(ctx, w, ref)
	if err != nil {
		return RestoreResult{}, err
	}
	undo, err := r.RestoreSafePointTo(ctx, w, ref, git.UndoRef(t.ID, time.Now().UnixMilli()))
	if err != nil {
		return RestoreResult{}, fmt.Errorf("put the worktree back: %w", err)
	}
	res := RestoreResult{Ref: ref, UndoRef: undo, Changed: len(changes)}
	data := map[string]any{"taskId": t.ID, "runId": runID, "ref": ref, "undoRef": undo, "changed": len(changes), "by": by}
	if why != "" {
		data["via"] = why
	}
	c.publishTask(ctx, "task.files_restored", t, "", data)
	if _, err := security.NewAudit(c.DB, c.Scanner).Append(ctx, security.AuditEntry{Actor: by, Action: "worktree.restore", Subject: t.ID, Detail: data}); err != nil {
		c.logf("audit the restore: %v", err)
	}
	line := fmt.Sprintf("%s put the worktree's files back to a safe point (%d %s changed). The state before is kept, so this can be undone from the run page.",
		personName(by), len(changes), plural(len(changes), "file", "files"))
	c.noteInRoom(ctx, t, line, map[string]any{"code": "files_restored", "by": by, "ref": ref, "undoRef": undo})
	return res, nil
}

// safePointAfter is the safe point recorded right after checkpoint step of run: the one taken before the first agent turn
// that ran after it, which holds the worktree's files as they were at that checkpoint. "" when no agent turn ran after it.
func safePointAfter(execs []workflow.ExecRow, step int) string {
	best, bestStep, bestAttempt := "", 0, 0
	for _, x := range execs {
		if x.SafePoint == "" || x.Step <= step {
			continue
		}
		if best == "" || x.Step < bestStep || (x.Step == bestStep && x.Attempt < bestAttempt) {
			best, bestStep, bestAttempt = x.SafePoint, x.Step, x.Attempt
		}
	}
	return best
}

// RunAgain runs an ended task (failed or cancelled) again as a new task with the same work, from its workflow's first node in
// a fresh worktree; the ended task stays as it is.
func (c *Core) RunAgain(ctx context.Context, taskID, by string) (RunAgainResult, error) {
	if !isPerson(by) {
		return RunAgainResult{}, errors.New("only a person runs a task again")
	}
	if err := c.RequireLicensed(ctx, "start_task"); err != nil {
		return RunAgainResult{}, err
	}
	t, err := c.Tasks.Get(ctx, taskID)
	if err != nil {
		return RunAgainResult{}, err
	}
	if why := runAgainRefusal(t); why != "" {
		return RunAgainResult{}, errors.New(why)
	}
	if err := c.requireRepo(ctx, t.ProjectID); err != nil {
		return RunAgainResult{}, err
	}
	nt, err := c.CreateTask(ctx, tasks.CreateReq{ProjectID: t.ProjectID, Title: t.Title, Description: t.Description, Priority: t.Priority, Template: t.Template,
		Files: t.Files, Acceptance: t.Acceptance, Labels: t.Labels, MaxIterations: t.MaxIterations, Origin: tasks.AgainOrigin(t.ID)})
	if err != nil {
		return RunAgainResult{}, err
	}
	res := RunAgainResult{TaskID: nt.ID, Seq: nt.Seq, From: t.ID}
	if nt, err = c.AssignTask(ctx, nt.ID, t.Assignee, t.Reviewer); err != nil {
		return res, fmt.Errorf("task #%d was made, but its agents could not be assigned: %w", nt.Seq, err)
	}
	id, err := c.StartTask(ctx, nt.ID)
	if err != nil {
		return res, fmt.Errorf("task #%d was made, but did not start: %w", nt.Seq, err)
	}
	res.RunID = string(id)
	c.publishTask(ctx, "task.run_again", nt, "", map[string]any{"taskId": nt.ID, "from": t.ID, "runId": res.RunID, "by": by})
	c.noteInRoom(ctx, nt, fmt.Sprintf("%s runs task #%d again as this task: the same work from the start, in a fresh worktree. Task #%d stays as it %s.",
		personName(by), t.Seq, t.Seq, strings.ToLower(string(t.Status))), map[string]any{"code": "run_again", "by": by, "from": t.ID})
	return res, nil
}

// runAgainRefusal says why task t cannot be run again ("" when it can).
func runAgainRefusal(t tasks.Task) string {
	switch {
	case t.Status == tasks.Completed:
		return fmt.Sprintf("Task #%d completed and its work was merged: more work on it is a new task.", t.Seq)
	case t.Status != tasks.Failed && t.Status != tasks.Cancelled:
		return fmt.Sprintf("Task #%d has not ended: only a failed or cancelled task is run again.", t.Seq)
	case t.OwnedByPerson():
		return fmt.Sprintf("A person took task #%d on, so no agent runs it.", t.Seq)
	case t.Assignee == "" || t.Reviewer == "":
		return fmt.Sprintf("Task #%d has no worker and reviewer to run it again with.", t.Seq)
	}
	if t.Template != "" {
		if _, ok := pipeline.TemplateByName(t.Template); !ok {
			return fmt.Sprintf("Task #%d's template %q is not in this version of Drydock.", t.Seq, t.Template)
		}
	}
	return ""
}

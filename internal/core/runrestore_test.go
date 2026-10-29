package core_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The run page's safe point restore and Run again (decision P124) through the methods the UI calls, on a real repository and
// worktree. Every agent is the scripted stand-in (fakeagent: a labelled test double).

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The worktree goes back to the safe point taken before the work turn: the files that change are listed first, the restore is
// refused while an agent turn runs there, the state before is kept under an undo ref (restoring it undoes the restore), and the
// task says what happened, in an event and in the audit log.
func TestASafePointOfATaskRunIsPutBackAndCanBeUndone(t *testing.T) {
	c := newCrew(t, fakeagent.Script{Rules: []fakeagent.Rule{reviewerShips}, Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "done"}}})
	a := c.a
	tk := c.task("Implement Subtract")
	run := c.assignAndStart(tk)
	d := c.awaitApproval(tk.ID)
	wt := d.Task.Worktree
	if readFile(t, wt, "calc.go") != impl {
		t.Fatal("the work turn wrote the implementation")
	}
	x := c.explain(tk.ID, run)
	if x.Actions == nil || !x.Actions.Restore {
		t.Fatalf("a waiting run's worktree can be put back: %+v", x.Actions)
	}
	var workRef string
	for _, e := range x.Executions {
		if e.NodeID == "work" {
			workRef = e.SafePoint
		}
	}
	var afterPrepare, latest core.CheckpointView
	for _, cp := range x.Checkpoints {
		if cp.Step == 1 && cp.Kind == "step" {
			afterPrepare = cp
		}
		if cp.Latest {
			latest = cp
		}
	}
	if workRef == "" || !strings.HasPrefix(workRef, "refs/drydock/safe/"+tk.ID+"/"+run+"/") || afterPrepare.SafePoint != workRef || latest.SafePoint != "" {
		t.Fatalf("the safe point after prepare is the work turn's (%q): %+v; the latest has none: %+v", workRef, afterPrepare, latest)
	}

	var pv core.RestorePreview
	a.call("run.restorePreview", map[string]string{"taskId": tk.ID, "runId": run, "ref": workRef}, &pv)
	if !pv.Allowed || len(pv.Changes) != 1 || pv.Changes[0].Path != "calc.go" || pv.Changes[0].Change != "changed" {
		t.Fatalf("preview %+v", pv)
	}
	// refused while an agent turn runs in the worktree, and for a ref that is not one of the task's safe points
	if _, err := a.c.DB.Writer().Exec(`INSERT INTO agent_runs(id,agent_id,task_id,status,started_at) VALUES('ar_busy',?,?,'WORKING',1)`, c.worker.ID, tk.ID); err != nil {
		t.Fatal(err)
	}
	a.call("run.restorePreview", map[string]string{"taskId": tk.ID, "runId": run, "ref": workRef}, &pv)
	if pv.Allowed || !strings.Contains(pv.Why, "agent turn is running") {
		t.Fatalf("preview while busy %+v", pv)
	}
	mustErr(t, a.callErr("run.restore", map[string]string{"taskId": tk.ID, "runId": run, "ref": workRef}), "agent turn is running")
	a.c.DB.Writer().Exec(`DELETE FROM agent_runs WHERE id='ar_busy'`)
	mustErr(t, a.callErr("run.restore", map[string]string{"taskId": tk.ID, "runId": run, "ref": "refs/heads/main"}), "is not a safe point of task #1")
	if readFile(t, wt, "calc.go") != impl {
		t.Fatal("a refused restore changed the worktree")
	}

	var res core.RestoreResult
	a.call("run.restore", map[string]string{"taskId": tk.ID, "runId": run, "ref": workRef}, &res)
	if res.Changed != 1 || !strings.HasPrefix(res.UndoRef, "refs/drydock/safe/"+tk.ID+"/undo/") {
		t.Fatalf("restore %+v", res)
	}
	if strings.Contains(readFile(t, wt, "calc.go"), "return a - b") {
		t.Fatal("calc.go is back as it was before the work turn")
	}
	if c.eventCount(tk.ID, "task.files_restored") != 1 {
		t.Fatal("the restore is announced on the task")
	}
	var audited int
	a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='worktree.restore' AND subject=? AND actor='human:kyle'`, tk.ID).Scan(&audited)
	if audited != 1 {
		t.Fatalf("audit rows %d", audited)
	}
	// the undo puts the turn's work back
	a.call("run.restore", map[string]string{"taskId": tk.ID, "runId": run, "ref": res.UndoRef}, &res)
	if readFile(t, wt, "calc.go") != impl {
		t.Fatal("the undo did not bring the work back")
	}
}

// Time travel can put the files back on the way: to the safe point after the checkpoint, before the new run starts; from a
// checkpoint after which no agent turn ran it is refused with nothing changed.
func TestTimeTravelPutsTheWorktreesFilesBackWhenAsked(t *testing.T) {
	c := newCrew(t, fakeagent.Script{Rules: []fakeagent.Rule{reviewerShips}, Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "done"}}})
	a := c.a
	tk := c.task("Implement Subtract")
	first := c.assignAndStart(tk)
	d := c.awaitApproval(tk.ID)
	x := c.explain(tk.ID, first)
	var afterPrepare, latest core.CheckpointView
	for _, cp := range x.Checkpoints {
		if cp.Step == 1 && cp.Kind == "step" {
			afterPrepare = cp
		}
		if cp.Latest {
			latest = cp
		}
	}
	mustErr(t, a.callErr("run.timeTravel", map[string]any{"taskId": tk.ID, "runId": first, "checkpointId": latest.ID, "restoreFiles": true}), "no agent turn ran after step")
	if got := c.detail(tk.ID).Task.RunID; got != first {
		t.Fatalf("a refused time travel stopped nothing: %s", got)
	}
	var res core.RunControlResult
	a.call("run.timeTravel", map[string]any{"taskId": tk.ID, "runId": first, "checkpointId": afterPrepare.ID, "restoreFiles": true}, &res)
	if res.Restored == nil || res.Restored.Ref != afterPrepare.SafePoint || res.Restored.Changed != 1 || res.Superseded != first {
		t.Fatalf("result %+v", res)
	}
	// the new run's work turn finds the files as they were and writes the implementation again
	c.awaitApproval(tk.ID)
	if readFile(t, d.Task.Worktree, "calc.go") != impl {
		t.Fatal("the fork's work turn wrote the implementation again")
	}
}

// A task that was cancelled runs again as a new task with the same work, started from the first node in a fresh worktree;
// the cancelled task stays cancelled. A task that has not ended is not run again.
func TestAnEndedTaskRunsAgainAsANewTask(t *testing.T) {
	c := newCrew(t, fakeagent.Script{Rules: []fakeagent.Rule{reviewerShips}, Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "done"}}})
	a := c.a
	tk := c.task("Implement Subtract")
	run := c.assignAndStart(tk)
	c.awaitApproval(tk.ID)
	mustErr(t, a.callErr("task.runAgain", map[string]string{"taskId": tk.ID}), "has not ended")
	a.call("task.cancel", map[string]string{"id": tk.ID}, nil)
	c.a.waitFor("the task cancelled", func() bool { return c.detail(tk.ID).Task.Status == tasks.Cancelled })
	x := c.explain(tk.ID, run)
	if !x.Actions.RunAgain || x.Actions.TimeTravel || !strings.Contains(x.Actions.TimeTravelWhy, "Run it again as a new task") {
		t.Fatalf("actions %+v", x.Actions)
	}

	var res core.RunAgainResult
	a.call("task.runAgain", map[string]string{"taskId": tk.ID}, &res)
	if res.TaskID == "" || res.TaskID == tk.ID || res.RunID == "" || res.From != tk.ID {
		t.Fatalf("result %+v", res)
	}
	nd := c.awaitApproval(res.TaskID)
	nt := nd.Task
	if nt.Title != tk.Title || nt.Description != tk.Description || strings.Join(nt.Acceptance, "|") != strings.Join(tk.Acceptance, "|") ||
		nt.Assignee != c.worker.ID || nt.Reviewer != c.reviewer.ID || nt.AgainOf() != tk.ID || nt.Worktree == "" || nt.Worktree == c.detail(tk.ID).Task.Worktree {
		t.Fatalf("the new task %+v", nt)
	}
	if old := c.detail(tk.ID).Task; old.Status != tasks.Cancelled || old.RunID != run {
		t.Fatalf("the ended task stays as it ended: %s %s", old.Status, old.RunID)
	}
	if c.eventCount(res.TaskID, "task.run_again") != 1 {
		t.Fatal("announced on the new task")
	}
	// running the cancelled task again a second time is another new task, not a reopening
	var res2 core.RunAgainResult
	a.call("task.runAgain", map[string]string{"taskId": tk.ID}, &res2)
	if res2.TaskID == res.TaskID || res2.TaskID == tk.ID {
		t.Fatalf("second %+v", res2)
	}
}

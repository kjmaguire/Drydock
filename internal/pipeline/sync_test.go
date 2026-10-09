package pipeline_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Two tasks on different files are both finished and parked for the human. The human approves the first; main moves. When the
// human approves the second, its branch is brought up to date and checked again on top of the first, and because the change is
// byte for byte the one they approved it merges without asking again and without another worker turn.
func TestTaskApprovedBeforeTheBaseMovedMergesWithoutAskingAgain(t *testing.T) {
	file := func(path, body string) []fakeagent.Step { return []fakeagent.Step{writeStep(path, body), say("done")} }
	e := newTpl(t, map[string]fakeagent.Script{
		aWork: {Rules: []fakeagent.Rule{
			{Match: "Task: Add Multiply", Steps: file("multiply.go", "package gocalc\n\nfunc Multiply(a, b int) int { return a * b }\n")},
			{Match: "Task: Add Divide", Steps: file("divide.go", "package gocalc\n\nfunc Divide(a, b int) int { return a / b }\n")},
		}},
		aReview: shipReviewer(),
	}, tplOpts{title: "Harness task"})
	e.dropHarnessTask()
	if err := e.eng.Register(pipeline.IterateUntilVerified(e.td.Deps, projectInfo, 3)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var runs []workflow.RunID
	var all []tasks.Task
	for _, title := range []string{"Add Multiply", "Add Divide"} {
		tk := e.assign(e.newTask(title, 0))
		id, err := e.eng.Start(ctx, pipeline.WorkflowIUV, pipeline.Input{TaskID: tk.ID}, workflow.StartOptions{ProjectID: "p1", TaskID: tk.ID})
		if err != nil {
			t.Fatal(err)
		}
		all, runs = append(all, tk), append(runs, id)
	}
	for _, id := range runs {
		e.await(id, workflow.StatusWaitingHuman)
	}
	if n := len(e.prompts(aWork)); n != 2 {
		t.Fatalf("each task should have had one worker turn so far, got %d", n)
	}

	// first approval: merges, main moves
	if err := e.respond(runs[0], "approved", "human:kyle", ""); err != nil {
		t.Fatal(err)
	}
	e.await(runs[0], workflow.StatusCompleted)
	if _, err := os.Stat(filepath.Join(e.repo.Root, "multiply.go")); err != nil {
		t.Fatalf("multiply.go should be on main: %v", err)
	}

	// second approval: the same answer, once
	if err := e.respond(runs[1], "approved", "human:kyle", ""); err != nil {
		t.Fatal(err)
	}
	info := e.await(runs[1], workflow.StatusCompleted)
	if info.Status != workflow.StatusCompleted {
		t.Fatalf("second task: %s %s", info.Status, info.FailedReason)
	}
	for _, f := range []string{"multiply.go", "divide.go"} {
		if _, err := os.Stat(filepath.Join(e.repo.Root, f)); err != nil {
			t.Fatalf("%s should be on main: %v", f, err)
		}
	}
	if n := len(e.prompts(aWork)); n != 2 {
		t.Fatalf("bringing the base in must not cost a worker turn, got %d turns", n)
	}
	// the second task shows why it was not asked again
	cur, _ := e.tasks.Get(ctx, all[1].ID)
	if cur.Status != tasks.Completed {
		t.Fatalf("task is %s", cur.Status)
	}
	ev, _ := e.tasks.Evidence(ctx, all[1].ID)
	var synced, approved bool
	for _, x := range ev {
		synced = synced || (x.Kind == "sync" && strings.Contains(x.Summary, "brought"))
		approved = approved || x.Kind == "approval"
	}
	if !synced || !approved {
		t.Fatalf("evidence should record the sync and the remembered approval: %+v", ev)
	}
}

func newCollidingPair(t *testing.T) (*tpl, []tasks.Task, []workflow.RunID) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "first-wrote-calc")
	slow := fakeagent.Step{WaitFile: marker} // the second task starts writing only after the first has written calc.go
	e := newTpl(t, map[string]fakeagent.Script{
		aWork: {Rules: []fakeagent.Rule{
			{Match: "Task: First", Steps: []fakeagent.Step{writeStep("calc.go", greenCalc+"\n// first\n"), writeStep(marker, "x"), say("done")}},
			{Match: "Task: Second", Steps: []fakeagent.Step{slow, writeStep("calc.go", greenCalc+"\n// second\n"), say("done")}},
		}},
		aReview: shipReviewer(),
	}, tplOpts{title: "Harness task"})
	e.dropHarnessTask()
	e.td.Deps.GuardPoll = 50 * time.Millisecond
	if err := e.eng.Register(pipeline.IterateUntilVerified(e.td.Deps, projectInfo, 3)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var all []tasks.Task
	var runs []workflow.RunID
	for _, title := range []string{"First", "Second"} {
		tk := e.assign(e.newTask(title, 0))
		id, err := e.eng.Start(ctx, pipeline.WorkflowIUV, pipeline.Input{TaskID: tk.ID}, workflow.StartOptions{ProjectID: "p1", TaskID: tk.ID})
		if err != nil {
			t.Fatal(err)
		}
		all, runs = append(all, tk), append(runs, id)
		// "earlier" means started first: let this task be under way before the next one is started
		waitFor(t, tk.Title+" to start", func() bool {
			cur, _ := e.tasks.Get(ctx, tk.ID)
			return cur.Started > 0
		})
	}
	return e, all, runs
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A human can release a task that waits for an earlier one; an agent cannot.
func TestWaitingTaskCanBeReleasedByAHumanOnly(t *testing.T) {
	e, all, runs := newCollidingPair(t)
	ctx := context.Background()
	waitFor(t, "the second task to wait", func() bool {
		cur, _ := e.tasks.Get(ctx, all[1].ID)
		return cur.Status == tasks.Blocked
	})
	cur, _ := e.tasks.Get(ctx, all[1].ID)
	if !strings.Contains(cur.BlockedReason, `"First"`) || !strings.Contains(cur.BlockedReason, "calc.go") {
		t.Fatalf("the reason should name the task and the file: %q", cur.BlockedReason)
	}
	if err := pipeline.ReleaseGuard(ctx, e.td.Deps, all[1].ID, aWork); err == nil {
		t.Fatal("an agent must not release a waiting task")
	}
	if err := pipeline.ReleaseGuard(ctx, e.td.Deps, all[0].ID, "human:kyle"); err == nil {
		t.Fatal("only a task that is waiting can be released")
	}
	if err := pipeline.ReleaseGuard(ctx, e.td.Deps, all[1].ID, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the released task to carry on to its approval", func() bool {
		p, _ := e.eng.Pending(ctx, runs[1])
		return len(p) == 1 && p[0].NodeID == "approve"
	})
	cur, _ = e.tasks.Get(ctx, all[1].ID)
	if cur.Status == tasks.Blocked {
		t.Fatalf("still blocked: %q", cur.BlockedReason)
	}
}

// Nothing waits when the human switched the guard off for the project.
func TestCollisionGuardCanBeSwitchedOff(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "first-wrote-calc")
	slow := fakeagent.Step{WaitFile: marker} // the second task starts writing only after the first has written calc.go
	e := newTpl(t, map[string]fakeagent.Script{
		aWork: {Rules: []fakeagent.Rule{
			{Match: "Task: First", Steps: []fakeagent.Step{writeStep("calc.go", greenCalc+"\n// first\n"), writeStep(marker, "x"), say("done")}},
			{Match: "Task: Second", Steps: []fakeagent.Step{slow, writeStep("calc.go", greenCalc+"\n// second\n"), say("done")}},
		}},
		aReview: shipReviewer(),
	}, tplOpts{title: "Harness task"})
	e.dropHarnessTask()
	off := false
	if err := pipeline.SaveDevSettings(context.Background(), e.db, "p1", pipeline.DevSettings{MaxParallel: 2, Respond: true, CollisionGuard: &off}, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	e.td.Deps.GuardPoll = 50 * time.Millisecond
	if err := e.eng.Register(pipeline.IterateUntilVerified(e.td.Deps, projectInfo, 3)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var runs []workflow.RunID
	for _, title := range []string{"First", "Second"} {
		tk := e.assign(e.newTask(title, 0))
		id, err := e.eng.Start(ctx, pipeline.WorkflowIUV, pipeline.Input{TaskID: tk.ID}, workflow.StartOptions{ProjectID: "p1", TaskID: tk.ID})
		if err != nil {
			t.Fatal(err)
		}
		runs = append(runs, id)
		waitFor(t, tk.Title+" to start", func() bool {
			cur, _ := e.tasks.Get(ctx, tk.ID)
			return cur.Started > 0
		})
	}
	for _, id := range runs {
		e.await(id, workflow.StatusWaitingHuman)
	}
}

// The scheduler does not start a task that is planned to change files a running task is changing.
func TestSchedulerHoldsATaskThatPlansToChangeTheSameFiles(t *testing.T) {
	e := newTpl(t, map[string]fakeagent.Script{aWork: shipReviewer(), aReview: shipReviewer()}, tplOpts{title: "Harness task"})
	e.dropHarnessTask()
	var running, peak atomic.Int32
	e.eng.Register(sleepyGraph(e.tasks, &running, &peak, 600*time.Millisecond))
	start := func(ctx context.Context, tk tasks.Task) (workflow.RunID, error) {
		id, err := e.eng.Start(ctx, "sched-sleepy", pipeline.Input{TaskID: tk.ID}, workflow.StartOptions{ProjectID: "p1", TaskID: tk.ID})
		if err == nil {
			rid := string(id)
			e.tasks.Patch(ctx, tk.ID, tasks.Update{RunID: &rid})
		}
		return id, err
	}
	s := schedFor(e, 4, start)
	mk := func(title string, files ...string) tasks.Task {
		tk, err := e.tasks.Create(context.Background(), tasks.CreateReq{ProjectID: "p1", Title: title, Description: title, Files: files})
		if err != nil {
			t.Fatal(err)
		}
		return e.assign(tk)
	}
	one := mk("Auth tokens", "internal/auth/")
	two := mk("Auth refresh", "internal/auth/refresh.go")
	three := mk("Docs", "docs/")
	res, err := s.Tick(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	started := map[string]bool{}
	for _, id := range res.Started {
		started[id] = true
	}
	if !started[one.ID] || !started[three.ID] || started[two.ID] {
		t.Fatalf("started %v", res.Started)
	}
	var why string
	for _, sk := range res.Skipped {
		if sk.TaskID == two.ID {
			why = sk.Reason
		}
	}
	if !strings.Contains(why, `"Auth tokens"`) || !strings.Contains(why, "internal/auth/refresh.go") {
		t.Fatalf("skip reason %q", why)
	}
	// once the first is done the second starts
	waitFor(t, "the first task's run to end", func() bool {
		cur, _ := e.tasks.Get(context.Background(), one.ID)
		info, err := e.eng.Status(context.Background(), workflow.RunID(cur.RunID))
		return err == nil && info.Status == workflow.StatusCompleted
	})
	// the sleepy run leaves its task IN_PROGRESS; finish it the way a merge would
	if _, err := e.tasks.Transition(context.Background(), one.ID, tasks.Cancelled, tasks.TransitionOpts{By: "human:kyle"}); err != nil {
		t.Fatal(err)
	}
	res, err = s.Tick(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range res.Started {
		found = found || id == two.ID
	}
	if !found {
		t.Fatalf("second task should start once the first is gone: %+v", res)
	}
}

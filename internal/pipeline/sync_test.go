package pipeline_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

package pipeline_test

import (
	"context"
	"slices"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// A work turn that changes files names them on the task from its diff, and tells the room (OnFiles) so a join rule that
// matches a path under them can add its agent (decision P133). The hook sees the task with the files already stored.
func TestTheRoomIsToldWhenAWorkTurnNamedTheTasksFiles(t *testing.T) {
	ad, cfg := fakeEnv(t, fakeagent.Script{
		Turns: [][]fakeagent.Step{{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "implemented"}}},
	})
	e := setup(t, cfg, ad)
	var told []string
	var stored [][]string
	e.deps.OnFiles = func(ctx context.Context, tk tasks.Task) {
		told = append(told, tk.ID)
		cur, err := e.tasks.Get(ctx, tk.ID)
		if err != nil {
			t.Error(err)
		}
		stored = append(stored, cur.Files)
	}
	e.run(t, 3, workflow.StatusCompleted)
	if len(told) != 1 || told[0] != e.task.ID {
		t.Fatalf("told about %v", told)
	}
	if !slices.Contains(stored[0], "calc.go") {
		t.Fatalf("the files were not stored before the room was told: %v", stored[0])
	}
}

package core_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
)

// The dock's reads say what the viewer needs to open on the right thing (effort review X21, X210): task.diffFiles says whether an
// agent is working on the task now (runActive), so the Diff tab opens on the live worktree while it works and on the recorded diff
// once the run waits for a person; logs.list names the agent of each stream from the turn's context manifest.
func TestDockReadsSayWhileTheAgentWorksAndWhoseStreamItIs(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "go-on")
	c := newCrew(t, fakeagent.Script{Rules: []fakeagent.Rule{reviewerShips},
		Default: []fakeagent.Step{{WaitFile: gate}, {Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "implemented Subtract"}}})
	a := c.a
	tk := c.task("Implement Subtract")

	var before core.DiffFiles
	a.call("task.diffFiles", map[string]any{"id": tk.ID}, &before)
	if before.RunActive {
		t.Fatalf("nothing runs yet: %+v", before)
	}

	c.assignAndStart(tk)
	a.waitFor("the worker's turn", func() bool { return c.turnsStarted(tk.ID) >= 1 && c.liveAgents(tk.ID) > 0 })
	var working core.DiffFiles
	a.call("task.diffFiles", map[string]any{"id": tk.ID}, &working)
	if !working.RunActive || !working.LiveAvailable {
		t.Fatalf("while the worker works: %+v", working)
	}

	if err := os.WriteFile(gate, []byte("go"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.awaitApproval(tk.ID)
	var waiting core.DiffFiles
	a.call("task.diffFiles", map[string]any{"id": tk.ID}, &waiting)
	if waiting.RunActive || waiting.Source != "recorded" {
		t.Fatalf("waiting for the person: %+v", waiting)
	}

	var logs []core.LogSource
	a.call("logs.list", map[string]string{"taskId": tk.ID}, &logs)
	byAgent := map[string]int{}
	for _, l := range logs {
		if l.Kind == "agent" {
			byAgent[l.AgentID]++
		}
		if l.Kind == "check" && l.AgentID != "" {
			t.Fatalf("a check is nobody's stream: %+v", l)
		}
	}
	if byAgent[c.worker.ID] == 0 || byAgent[c.reviewer.ID] == 0 {
		t.Fatalf("streams by agent %v (worker %s, reviewer %s): %+v", byAgent, c.worker.ID, c.reviewer.ID, logs)
	}
}

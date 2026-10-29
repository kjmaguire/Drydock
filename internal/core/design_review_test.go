package core_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// Decision P124 through the core: the shipped Design Review template holds its discussion in the task's channel on the room's
// responder, and the discussion's turns count toward the template's workflow budget: at its limit the node waits on the budget
// pause (an Inbox budget item), one approval lets one more turn through, raising the limit lets the discussion end, and the
// agreed design reaches the worker. The agents are the scripted stand-in (labelled test double).
func TestTheDesignReviewTemplatesDiscussionIsHeldToItsWorkflowBudget(t *testing.T) {
	script := devScript()
	script.Rules = append([]fakeagent.Rule{
		{Match: "This is round 1 of", Steps: []fakeagent.Step{{Say: "[DECISION] Put Multiply in multiply.go beside calc.go"}}},
		{Match: "Drydock phase: group-discussion", Steps: []fakeagent.Step{{Say: "PASS"}}},
	}, script.Rules...)
	a, plog := openDev(t, script)
	repo := newGreenRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	arch := a.agent(p.ID, "Ari", "architect")
	dev := a.agent(p.ID, "Dana", "backend")
	rev := a.agent(p.ID, "Rex", "reviewer")
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Add Multiply", Description: "Add a Multiply function to the gocalc package."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": dev.ID, "reviewer": rev.ID}, &task)
	var b struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
	}
	a.call("budget.set", map[string]any{"scope": "workflow", "scopeId": pipeline.WorkflowDesignReview, "projectId": p.ID, "limitTurns": 2}, &b)

	var started core.StartTemplateResult
	a.call("task.startTemplate", map[string]any{"taskId": task.ID, "template": "design-review"}, &started)
	if started.Workflow != pipeline.WorkflowDesignReview || started.Roles[pipeline.RoleArchitect] != arch.ID {
		t.Fatalf("%+v", started)
	}
	// two turns of round 1 take the workflow to its limit: the reviewer's turn waits on the budget pause
	d := a.waitPending(task.ID, "discuss")
	if !strings.Contains(string(d.Pending[0].Spec.Payload), `"gate":"budget"`) || !strings.Contains(d.Pending[0].Spec.Title, "workflow design-review budget") {
		t.Fatalf("pause %+v", d.Pending[0].Spec)
	}
	if n := groupTurns(t, plog); n != 2 {
		t.Fatalf("%d discussion turns before the pause", n)
	}
	var items []room.NeedsYouItem
	a.call("needsyou.list", map[string]string{"projectId": p.ID}, &items)
	paused := false
	for _, it := range items {
		paused = paused || (it.Kind == room.NeedsBudget && it.BudgetState == room.BudgetPaused)
	}
	if !paused {
		t.Fatalf("the pause is a budget item in the Inbox: %+v", items)
	}
	// one approval: one more turn, then the limit holds again
	a.call("budget.resume", map[string]any{"runId": string(started.RunID), "decision": "approved"}, nil)
	a.waitFor("the second pause", func() bool {
		d = a.detail(task.ID)
		return len(d.Pending) == 1 && d.Pending[0].NodeID == "discuss" && groupTurns(t, plog) == 3
	})
	// raising the limit lets the discussion end; the decision is the worker's design
	a.call("budget.resume", map[string]any{"runId": string(started.RunID), "decision": "approved", "limitTurns": 100}, nil)
	a.waitPending(task.ID, "approve")
	if n := groupTurns(t, plog); n != 6 {
		t.Fatalf("%d discussion turns (two rounds of three)", n)
	}
	work := ""
	for _, pr := range promptLog(t, plog) {
		if strings.Contains(pr, "Task: Add Multiply") && !strings.Contains(pr, "group-discussion") && !strings.Contains(pr, "independent reviewer") {
			work = pr
		}
	}
	if !strings.Contains(work, "Put Multiply in multiply.go beside calc.go") {
		t.Fatalf("the worker's turn did not carry the agreed design:\n%s", work)
	}
	var sessions int
	a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE workflow_run_id=?`, string(started.RunID)).Scan(&sessions)
	if sessions != 6 {
		t.Fatalf("%d sessions name the run", sessions)
	}
}

// promptLog is every prompt of the log, in order.
func promptLog(t *testing.T, path string) []string {
	t.Helper()
	b, _ := os.ReadFile(path)
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var s string
		if json.Unmarshal([]byte(l), &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

// groupTurns counts the discussion turns in the prompt log.
func groupTurns(t *testing.T, plog string) int {
	n := 0
	for _, p := range promptLog(t, plog) {
		if strings.HasPrefix(p, "Drydock phase: group-discussion") {
			n++
		}
	}
	return n
}

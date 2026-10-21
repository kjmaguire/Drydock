package core_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/memory"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// P92, the learning loop: a worker proposes a lesson through the gateway's propose_memory tool, and a person's change request
// on a review becomes a proposal in their own words. Neither reaches an agent until the person accepts it.
func TestLessonsAreProposedByAgentsAndFromAPersonsReview(t *testing.T) {
	lesson, _ := json.Marshal(map[string]string{"kind": "convention", "title": "Exported functions carry a doc comment", "body": "Every exported function in this package has a doc comment starting with its name."})
	a := openScripted(t, nil, fakeagent.Script{
		Rules: []fakeagent.Rule{
			{Match: "Changes requested by the human", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "added the doc comment"}}},
			{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}},
		},
		Default: []fakeagent.Step{{MCP: "mcp__drydock__propose_memory", Input: lesson}, {Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "implemented Subtract"}},
	})
	p, _ := a.setup("gocalc")
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	review := func() string {
		var d core.TaskDetail
		var id string
		a.waitFor("the review request", func() bool {
			a.call("task.get", map[string]string{"id": task.ID}, &d)
			if len(d.Pending) == 1 && d.Pending[0].Spec.Kind == "review" {
				id = string(d.Pending[0].ID)
				return true
			}
			return false
		})
		return id
	}
	list := func() []memory.Entry {
		var out []memory.Entry
		a.call("memory.list", map[string]any{"projectId": p.ID}, &out)
		return out
	}

	// the worker's proposal waits for the person
	req := review()
	got := list()
	if len(got) != 1 || got[0].Status != memory.Proposed || got[0].Title != "Exported functions carry a doc comment" || got[0].Source != "agent:"+worker.ID+" task:"+task.ID {
		t.Fatalf("the worker's proposal: %+v", got)
	}

	// a change request too short to teach anything proposes nothing; a real one is proposed in the person's words
	a.call("run.respond", map[string]any{"runId": runID, "requestId": req, "decision": "rejected", "data": map[string]string{"feedback": "redo"}}, nil)
	req = review()
	if n := len(list()); n != 1 {
		t.Fatalf("a short request was proposed: %d entries", n)
	}
	words := "Name the error variables errSomething and wrap errors with %w, as the rest of the package does."
	a.call("run.respond", map[string]any{"runId": runID, "requestId": req, "decision": "rejected", "data": map[string]string{"feedback": words}}, nil)
	req = review()
	var fromReview *memory.Entry
	for _, e := range list() {
		if e.Source == "review:"+task.ID {
			e := e
			fromReview = &e
		}
	}
	if fromReview == nil || fromReview.Status != memory.Proposed || fromReview.Kind != "preference" || fromReview.Body != words || !strings.HasPrefix(fromReview.Title, "From the review of #") || !strings.Contains(fromReview.Title, "Implement Subtract") {
		t.Fatalf("the change request's proposal: %+v", fromReview)
	}
	// approving proposes nothing more
	a.call("run.respond", map[string]any{"runId": runID, "requestId": req, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("completion", func() bool {
		var d core.TaskDetail
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		return d.Task.Status == tasks.Completed
	})
	if n := len(list()); n != 2 {
		t.Fatalf("entries after approval: %d", n)
	}
}

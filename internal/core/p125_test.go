package core_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// Decision P125 through the core, with the scripted Claude stand-in (a labelled test double) calling the real gateway's handoff
// tool: the outgoing session is asked for a handoff in a turn of its own before a fresh session replaces it.

const wrongSubtract = "package gocalc\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Subtract(a, b int) int { return a + b }\n"

// handoffAskRun runs a task whose first work turn writes a wrong Subtract and ends with the given context occupancy, so the next
// work turn replaces the session; handoff is what the outgoing session does when Drydock asks it for a handoff. It returns the
// Drydock handoff written for the fresh session, and the prompts the agents were sent.
func handoffAskRun(t *testing.T, occupancy int64, handoff []fakeagent.Step) (pipeline.HandoffArtifact, *app, tasks.Task) {
	t.Helper()
	a := openScripted(t, nil, fakeagent.Script{
		Rules: []fakeagent.Rule{
			{Match: "Drydock phase: handoff", Steps: handoff},
			{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}},
			{Match: "previous attempt needs changes", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "fixed the sign"}}},
		},
		Turns: [][]fakeagent.Step{{{Say: "reading calc.go", Context: occupancy}, {Write: &fakeagent.WriteSpec{Path: "calc.go", Content: wrongSubtract}}, {Say: "implemented Subtract."}}},
	})
	p, _ := a.setup("gocalc")
	a.call("mcp.actTools.set", map[string]any{"projectId": p.ID, "enabled": true}, nil)
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b.",
		Acceptance: []string{"AC1: Given 5 and 3, when Subtract is called, then it returns 2"}}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	a.waitFor("the review request", func() bool {
		var d core.TaskDetail
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		return len(d.Pending) == 1
	})
	var raw string
	if err := a.c.DB.Reader().QueryRow(`SELECT artifact FROM handoffs WHERE task_id=? AND reason=?`, task.ID, pipeline.HandoffContextPressure).Scan(&raw); err != nil {
		t.Fatalf("Drydock's handoff: %v", err)
	}
	var art pipeline.HandoffArtifact
	if err := json.Unmarshal([]byte(raw), &art); err != nil {
		t.Fatal(err)
	}
	if art.HandoffRequest == nil {
		t.Fatalf("no handoff request recorded: %s", raw)
	}
	return art, a, task
}

func handoffCall(h map[string]any) []fakeagent.Step {
	h["action"] = "create"
	b, _ := json.Marshal(h)
	return []fakeagent.Step{{MCP: "mcp__drydock__handoff", Input: b}, {Say: "done"}}
}

func agentHandoffs(t *testing.T, a *app, taskID string) []string {
	t.Helper()
	rows, err := a.c.DB.Reader().Query(`SELECT id FROM handoffs WHERE task_id=? AND reason='agent' ORDER BY created_at`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		out = append(out, id)
	}
	return out
}

// A good handoff: the outgoing session, asked in its own turn, writes one with the tool; it passes Drydock's checks and the fresh
// session starts from it.
func TestTheOutgoingSessionIsAskedAndItsGoodHandoffIsUsed(t *testing.T) {
	art, a, task := handoffAskRun(t, 150_000, handoffCall(map[string]any{"summary": "Subtract adds instead of subtracting.", "files": []string{"calc.go"},
		"remaining": []string{"flip the sign in Subtract"}, "next_step": "change a + b to a - b", "checks": []map[string]any{{"name": "test", "passed": false}},
		"criteria": []map[string]any{{"id": "AC1", "met": true, "evidence": "TestSubtract covers 5 - 3"}}}))
	req := art.HandoffRequest
	ids := agentHandoffs(t, a, task.ID)
	if !req.Asked || req.Outcome != pipeline.HandoffAnswered || len(ids) != 1 || req.Handoff != ids[0] || req.Session == "" {
		t.Fatalf("request %+v, agent handoffs %v", req, ids)
	}
	if art.AgentHandoff == nil || !art.AgentHandoff.Accepted || art.AgentHandoff.ID != ids[0] || art.NextStep != "change a + b to a - b" {
		t.Fatalf("check %+v next %q", art.AgentHandoff, art.NextStep)
	}
	// its claim that AC1 is met is a claim on the task page, not a status; the handoff carries where each criterion stands
	var d core.TaskDetail
	a.call("task.get", map[string]string{"id": task.ID}, &d)
	if len(d.Criteria) != 1 || d.Criteria[0].Status != pipeline.CriterionOpen || d.Criteria[0].Claim == nil || d.Criteria[0].Claim.By != task.Assignee ||
		!strings.Contains(d.Criteria[0].Claim.Evidence, "TestSubtract covers 5 - 3 (handoff "+ids[0]+")") {
		t.Fatalf("criteria %+v", d.Criteria)
	}
	if len(art.Criteria) != 1 || art.Criteria[0].ID != "AC1" || art.Criteria[0].ClaimBy != task.Assignee || !strings.Contains(art.CriteriaSummary, "AC1 open, claimed met by") {
		t.Fatalf("the handoff's criteria %+v %q", art.Criteria, art.CriteriaSummary)
	}
}

// A person confirms, waives and reopens a criterion on the task page; the method takes the core's person as the author, and a
// waiver needs a reason.
func TestAPersonMarksEachAcceptanceCriterion(t *testing.T) {
	_, a, task := handoffAskRun(t, 150_000, []fakeagent.Step{{Say: "no"}})
	var cs []pipeline.CriterionStatus
	a.call("task.criterion.mark", map[string]any{"taskId": task.ID, "index": 0, "mark": "met", "note": "tried 5 - 3 by hand"}, &cs)
	if len(cs) != 1 || cs[0].Status != pipeline.CriterionMet || cs[0].Source != pipeline.SourcePerson || cs[0].By != "human:kyle" || cs[0].Evidence != "tried 5 - 3 by hand" {
		t.Fatalf("after met %+v", cs)
	}
	mustErr(t, a.callErr("task.criterion.mark", map[string]any{"taskId": task.ID, "index": 0, "mark": "waived"}), "say why")
	mustErr(t, a.callErr("task.criterion.mark", map[string]any{"taskId": task.ID, "index": 0, "mark": "claim_met", "note": "x"}), "a claim is an agent's")
	mustErr(t, a.callErr("task.criterion.mark", map[string]any{"taskId": task.ID, "index": 3, "mark": "met"}), "there is no criterion 4")
	a.call("task.criterion.mark", map[string]any{"taskId": task.ID, "index": 0, "mark": "waived", "note": "covered by the calculator's own tests"}, &cs)
	if cs[0].Status != pipeline.CriterionWaived {
		t.Fatalf("after waived %+v", cs)
	}
	a.call("task.criterion.mark", map[string]any{"taskId": task.ID, "index": 0, "mark": "open"}, &cs)
	var d core.TaskDetail
	a.call("task.get", map[string]string{"id": task.ID}, &d)
	if d.Criteria[0].Status != pipeline.CriterionOpen || cs[0].Status != pipeline.CriterionOpen {
		t.Fatalf("after open %+v %+v", cs, d.Criteria)
	}
}

// The handoff tool refuses a report on a criterion the task does not have, so nothing is written.
func TestTheHandoffToolRefusesACriterionTheTaskDoesNotHave(t *testing.T) {
	art, a, task := handoffAskRun(t, 150_000, handoffCall(map[string]any{"summary": "s", "next_step": "n", "files": []string{"calc.go"},
		"criteria": []map[string]any{{"id": "AC9", "met": true, "evidence": "x"}}}))
	if ids := agentHandoffs(t, a, task.ID); len(ids) != 0 || art.HandoffRequest.Outcome != pipeline.HandoffNoAnswer {
		t.Fatalf("handoffs %v request %+v", ids, art.HandoffRequest)
	}
}

// A bad handoff: what it says about a check does not hold, so it is not used; Drydock's is, with the reason.
func TestABadHandoffFromTheOutgoingSessionIsRejected(t *testing.T) {
	art, _, _ := handoffAskRun(t, 150_000, handoffCall(map[string]any{"summary": "All done, the tests pass.", "files": []string{"calc.go"},
		"next_step": "nothing", "checks": []map[string]any{{"name": "test", "passed": true}}}))
	if art.HandoffRequest.Outcome != pipeline.HandoffAnswered || art.AgentHandoff == nil || art.AgentHandoff.Accepted ||
		!strings.Contains(strings.Join(art.AgentHandoff.Reasons, "; "), "it says the test check passed, but its latest recorded result is that it failed") {
		t.Fatalf("request %+v check %+v", art.HandoffRequest, art.AgentHandoff)
	}
	if art.NextStep == "nothing" {
		t.Fatal("the rejected handoff's next step was used")
	}
}

// No answer: the outgoing session ends its turn without calling the tool.
func TestAnOutgoingSessionThatWritesNoHandoffFallsBack(t *testing.T) {
	art, a, task := handoffAskRun(t, 150_000, []fakeagent.Step{{Say: "Nothing to hand off."}})
	if r := art.HandoffRequest; !r.Asked || r.Outcome != pipeline.HandoffNoAnswer || !strings.Contains(r.Reason, "without writing a handoff") || art.AgentHandoff != nil {
		t.Fatalf("request %+v check %+v", r, art.AgentHandoff)
	}
	if ids := agentHandoffs(t, a, task.ID); len(ids) != 0 {
		t.Fatalf("agent handoffs %v", ids)
	}
}

// Over budget: a session whose window has almost no room left is not asked at all.
func TestAnOutgoingSessionWithoutRoomIsNotAsked(t *testing.T) {
	art, a, task := handoffAskRun(t, 198_500, handoffCall(map[string]any{"summary": "s", "next_step": "n"}))
	if r := art.HandoffRequest; r.Asked || r.Outcome != pipeline.HandoffOverBudget || !strings.Contains(r.Reason, "context window left") {
		t.Fatalf("request %+v", r)
	}
	if ids := agentHandoffs(t, a, task.ID); len(ids) != 0 {
		t.Fatalf("the session was asked anyway: %v", ids)
	}
}

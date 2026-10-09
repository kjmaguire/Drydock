package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
)

const hackCalc = "package gocalc\n\nfunc Add(a, b int) int { return a + b }\n\n// HACK: temporary\nfunc Subtract(a, b int) int { return a - b }\n"

// leadScript is one script for every agent of the app (the scripted stand-in, a labelled test double), keyed by what each
// prompt contains: the lead's supervision and stand-up turns, the reviewer, and a worker that only drops its HACK comment
// once the team lead tells it to.
func leadScript() fakeagent.Script {
	say := func(s string) []fakeagent.Step { return []fakeagent.Step{{Say: s}} }
	return fakeagent.Script{Rules: []fakeagent.Rule{
		{Match: "Drydock phase: supervise", Steps: say(`{"action":"nudge","message":"DROPHACK: delete the HACK comment line; the reviewer wants nothing else.","reason":"the worker ignores the review"}`)},
		{Match: "Drydock phase: standup", Steps: say("#1 Implement Subtract: at risk: the reviewer keeps asking for the same change\nSummary: one task, at risk")},
		{Match: "DROPHACK", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "removed the comment"}}},
		{Match: "// HACK", Steps: say(`{"verdict":"REVISE","feedback":"Remove the HACK comment."}`)},
		{Match: "independent reviewer", Steps: say(`{"verdict":"SHIP","feedback":""}`)},
	}, Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: hackCalc}}, {Say: "implemented"}}}
}

// The team lead through the methods the UI calls: the reviewer repeats itself, the lead is asked (not the human), its nudge
// gets the work to the approval; the intervention is listed, posted in the task channel by the lead, and published.
func TestLeadInterventionThroughTheApp(t *testing.T) {
	a, _ := openDev(t, leadScript())
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	var worker, reviewer, lead core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Lead", Adapter: "claude", Role: "Team Lead", Model: "fake-1"}, &lead)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b.", Acceptance: []string{"Given 5 and 3 When Subtract is called Then 2"}}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)

	var detail core.TaskDetail
	a.waitFor("the approval", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1
	})
	if detail.Pending[0].NodeID != "approve" {
		t.Fatalf("the lead's nudge should carry the run to the approval, pending %+v", detail.Pending[0])
	}
	var ivs []pipeline.Intervention
	a.call("lead.interventions", map[string]string{"projectId": p.ID, "taskId": task.ID}, &ivs)
	if len(ivs) != 1 || ivs[0].Trigger != pipeline.TriggerRepeatedRevise || ivs[0].Action != "nudge" || ivs[0].LeadAgentID != lead.ID || ivs[0].MessageID == "" {
		t.Fatalf("interventions %+v", ivs)
	}
	var all []pipeline.Intervention
	a.call("lead.interventions", map[string]string{"projectId": p.ID}, &all)
	if len(all) != 1 {
		t.Fatalf("project interventions %+v", all)
	}
	var msgs []room.Message
	a.call("room.messages", map[string]any{"roomId": detail.RoomID, "limit": 200}, &msgs)
	found := false
	for _, m := range msgs {
		if m.Author.Key() == room.Agent(lead.ID).Key() && strings.HasPrefix(m.Body, "Team lead: nudge") && strings.Contains(m.Body, "DROPHACK") {
			found = true
		}
	}
	if !found {
		t.Fatal("the intervention must be posted in the task channel by the lead")
	}
	var evs []struct {
		Type string `json:"Type"`
	}
	a.call("events.replay", map[string]any{"after": 0, "taskId": task.ID, "types": []string{"lead.intervened"}}, &evs)
	if len(evs) != 1 {
		t.Fatalf("lead.intervened events %d", len(evs))
	}
}

// lead.standup posts a short status in #general, authored by the lead, and returns the message id; without a lead it says so.
func TestLeadStandupPostsInGeneral(t *testing.T) {
	a, _ := openDev(t, leadScript())
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	if _, err := a.c.Call(context.Background(), "lead.standup", mustJSON(map[string]string{"projectId": p.ID})); err == nil || !strings.Contains(err.Error(), "no team lead") {
		t.Fatalf("without a lead: %v", err)
	}
	var worker, lead core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Lead", Adapter: "claude", Role: "lead", Model: "fake-1"}, &lead)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "x"}, &task)
	a.c.Tasks.Transition(context.Background(), task.ID, tasks.Ready, tasks.TransitionOpts{By: "system"})
	var res pipeline.StandupResult
	a.call("lead.standup", map[string]string{"projectId": p.ID}, &res)
	if res.MessageID == "" || res.Tasks != 1 || !strings.Contains(res.Text, "at risk") {
		t.Fatalf("standup %+v", res)
	}
	m, err := a.c.Room.GetMessage(context.Background(), res.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := a.c.Room.GetRoom(context.Background(), m.RoomID)
	if r.Name != "general" || m.Author.Key() != room.Agent(lead.ID).Key() || !strings.HasPrefix(m.Body, "Stand-up from the team lead:") {
		t.Fatalf("message %+v in %s", m, r.Name)
	}
	// the monitor's pass on demand
	var tick pipeline.MonitorResult
	a.call("lead.tick", map[string]string{"projectId": p.ID}, &tick)
	if len(tick.Stalled)+len(tick.Unassigned) != 0 {
		t.Fatalf("nothing to do yet (autoSchedule is off): %+v", tick)
	}
}

package core_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

func TestTeamManagementPurposeEditAndRemove(t *testing.T) {
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	ctx := context.Background()
	create := func(s core.AgentSpec) (core.Agent, error) {
		s.ProjectID = p.ID
		var out core.Agent
		raw, err := a.c.Call(ctx, "agent.create", mustJSON(s))
		if err == nil {
			err = json.Unmarshal(raw, &out)
		}
		return out, err
	}

	// purpose, model and effort are stored and reach the run's system prompt through the runtime hook
	w, err := create(core.AgentSpec{Name: "Pay", Adapter: "claude", Role: "backend", Model: "sonnet", Effort: "high", Purpose: "Own the payments service. Never touch the UI."})
	if err != nil {
		t.Fatal(err)
	}
	if w.Purpose == "" || w.Effort != "high" {
		t.Fatalf("agent %+v", w)
	}
	ins := a.c.Runtime.Instructions(ctx, w.ID)
	if !strings.Contains(ins, "Own the payments service. Never touch the UI.") || !strings.Contains(ins, "You are Pay") || !strings.Contains(ins, "always in force") {
		t.Fatalf("instructions = %q", ins)
	}
	r, err := create(core.AgentSpec{Name: "Rev", Adapter: "claude", Role: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	if got := a.c.Runtime.Instructions(ctx, r.ID); got != "" {
		t.Fatalf("an agent without a purpose must add nothing, got %q", got)
	}

	// values that would fail (or misbehave) when the CLI starts are refused up front
	for name, s := range map[string]core.AgentSpec{
		"effort not offered by claude": {Name: "x", Adapter: "claude", Role: "r", Effort: "ultra"},
		"model that reads as a flag":   {Name: "x", Adapter: "claude", Role: "r", Model: "--dangerously-skip-permissions"},
		"model with a space":           {Name: "x", Adapter: "claude", Role: "r", Model: "two words"},
		"purpose too long":             {Name: "x", Adapter: "claude", Role: "r", Purpose: strings.Repeat("a", core.MaxPurposeRunes+1)},
	} {
		if _, err := create(s); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	// edit
	var u core.Agent
	a.call("agent.update", map[string]any{"id": w.ID, "model": "opus", "effort": "max", "purpose": "Own billing only."}, &u)
	if u.Model != "opus" || u.Effort != "max" || u.Purpose != "Own billing only." || u.Name != "Pay" {
		t.Fatalf("updated %+v", u)
	}
	got, _ := a.c.GetAgent(ctx, w.ID)
	if got.Agent.Model != "opus" || !strings.Contains(a.c.Runtime.Instructions(ctx, w.ID), "Own billing only.") {
		t.Fatalf("stored %+v", got.Agent)
	}
	for name, params := range map[string]map[string]any{
		"bad effort":       {"id": w.ID, "effort": "ultra"},
		"flag-like model":  {"id": w.ID, "model": "-x"},
		"empty role":       {"id": w.ID, "role": "  "},
		"bad permission":   {"id": w.ID, "permissionMode": "yolo"},
		"unknown agent id": {"id": "agt_nope", "model": "opus"},
	} {
		if err := a.callErr("agent.update", params); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if g, _ := a.c.GetAgent(ctx, w.ID); g.Agent.Effort != "max" {
		t.Fatalf("a refused update changed the agent: %+v", g.Agent)
	}

	// removal is refused while the agent owns unfinished work, then works and hides the agent
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Do billing", Description: "d"}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	if err := a.callErr("agent.archive", map[string]string{"id": w.ID}); err == nil || !strings.Contains(err.Error(), "unfinished") {
		t.Fatalf("archive with an open task: %v", err)
	}
	a.call("task.cancel", map[string]string{"id": task.ID}, nil)
	var done core.Agent
	a.call("agent.archive", map[string]string{"id": w.ID}, &done)
	if !done.Archived {
		t.Fatalf("archived %+v", done)
	}
	var team []core.Agent
	a.call("agent.list", map[string]string{"projectId": p.ID}, &team)
	for _, m := range team {
		if m.ID == w.ID {
			t.Fatal("a removed agent is still on the team")
		}
	}
	var t2 tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Another", Description: "d"}, &t2)
	if err := a.callErr("task.assign", map[string]string{"id": t2.ID, "assignee": w.ID, "reviewer": r.ID}); err == nil {
		t.Fatal("a removed agent can still be assigned")
	}
	if err := a.callErr("agent.update", map[string]any{"id": w.ID, "model": "haiku"}); err == nil {
		t.Fatal("a removed agent can still be edited")
	}
}

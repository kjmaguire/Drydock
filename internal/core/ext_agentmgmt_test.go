package core_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/claude"
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
	ins, pv := a.c.Runtime.Instructions(ctx, w.ID, "backend")
	if !strings.Contains(ins, "Working practices for this team") || pv != claude.PracticesVersion {
		t.Fatalf("a Claude agent gets the best-practices text by default: version %d, text %q", pv, ins)
	}
	if !strings.Contains(ins, "Own the payments service. Never touch the UI.") || !strings.Contains(ins, "You are Pay") || !strings.Contains(ins, "always in force") {
		t.Fatalf("instructions = %q", ins)
	}
	r, err := create(core.AgentSpec{Name: "Rev", Adapter: "claude", Role: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	rv, _ := a.c.Runtime.Instructions(ctx, r.ID, "reviewer")
	if strings.Contains(rv, "standing instructions") || !strings.Contains(rv, "You did not write this change") {
		t.Fatalf("an agent without a purpose gets the practices for its role and no purpose text, got %q", rv)
	}

	// values that would fail (or misbehave) when the CLI starts are refused up front
	for name, s := range map[string]core.AgentSpec{
		"effort not offered by claude": {Name: "x", Adapter: "claude", Role: "r", Effort: "ultra"},
		"model that reads as a flag":   {Name: "x", Adapter: "claude", Role: "r", Model: "--dangerously-skip-permissions"},
		"model with a space":           {Name: "x", Adapter: "claude", Role: "r", Model: "two words"},
		"purpose too long":             {Name: "x", Adapter: "claude", Role: "r", Purpose: strings.Repeat("a", core.MaxPurposeRunes+1)},
		"permission mode claude lacks": {Name: "x", Adapter: "claude", Role: "r", PermissionMode: "yolo"},
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
	ins2, _ := a.c.Runtime.Instructions(ctx, w.ID, "backend")
	if got.Agent.Model != "opus" || !strings.Contains(ins2, "Own billing only.") {
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
	// it left every room: not offered for mentions, not listed to the other agents
	var left int
	if err := a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM room_members WHERE member_id=?`, w.ID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("a removed agent is still a member of %d room(s) (%v)", left, err)
	}
	var stays int
	a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM room_members WHERE member_id=?`, r.ID).Scan(&stays)
	if stays == 0 {
		t.Fatal("the other agent left the rooms too")
	}
}

func TestClaudePracticesAreOnByDefaultPerRoleAndCanBeTurnedOff(t *testing.T) {
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	ctx := context.Background()
	off := false
	var on, no core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "On", Adapter: "claude", Role: "backend", Purpose: "Own billing."}, &on)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Off", Adapter: "claude", Role: "backend", Purpose: "Own billing.", Practices: &off}, &no)
	if !on.Practices || no.Practices {
		t.Fatalf("practices flags %v %v", on.Practices, no.Practices)
	}

	// the text depends on the role the session plays: a worker, the reviewer turn of the same agent, a planner
	work, v := a.c.Runtime.Instructions(ctx, on.ID, "backend")
	rev, _ := a.c.Runtime.Instructions(ctx, on.ID, "reviewer")
	plan, _ := a.c.Runtime.Instructions(ctx, on.ID, "architect")
	if v != claude.PracticesVersion || !strings.Contains(work, "Never skip, delete or weaken a test") || strings.Contains(work, "You do not modify files") {
		t.Fatalf("worker text: %q", work)
	}
	if !strings.Contains(rev, "You do not modify files") || strings.Contains(rev, "Never skip, delete") {
		t.Fatalf("reviewer text: %q", rev)
	}
	if !strings.Contains(plan, "do not write or change code") {
		t.Fatalf("planner text: %q", plan)
	}
	for _, text := range []string{work, rev, plan} {
		if !strings.Contains(text, "Own billing.") || strings.Index(text, "Working practices") > strings.Index(text, "Own billing.") {
			t.Fatalf("practices come first, then the human's purpose: %q", text)
		}
	}

	// off: only the purpose is sent, and no practices version is recorded
	only, v2 := a.c.Runtime.Instructions(ctx, no.ID, "backend")
	if strings.Contains(only, "Working practices") || !strings.Contains(only, "Own billing.") || v2 != 0 {
		t.Fatalf("practices off: version %d %q", v2, only)
	}
	// switch it back on through an update
	on2 := true
	var u core.Agent
	a.call("agent.update", map[string]any{"id": no.ID, "practices": on2}, &u)
	again, v3 := a.c.Runtime.Instructions(ctx, no.ID, "backend")
	if !u.Practices || !strings.Contains(again, "Working practices") || v3 == 0 {
		t.Fatalf("after switching on: %+v %q", u, again)
	}

	// the method the UI reads: the text, the family and the suggested model and effort
	var cp core.ClaudePractices
	a.call("claude.practices", map[string]string{"role": "Architect"}, &cp)
	if cp.Family != "planner" || cp.Preset.Model != "opus" || cp.Preset.Effort != "high" || cp.Text == "" {
		t.Fatalf("practices for architect: %+v", cp)
	}
	a.call("claude.practices", map[string]string{"role": "backend"}, &cp)
	if cp.Preset.Model != "sonnet" || cp.Preset.Effort != "medium" {
		t.Fatalf("practices for backend: %+v", cp)
	}
	// the text stays short: every line is paid for on every run
	if n := len(strings.Fields(work)); n > 330 {
		t.Fatalf("the practices text grew to %d words", n)
	}
}

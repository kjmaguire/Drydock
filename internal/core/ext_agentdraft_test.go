package core_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
)

func TestAgentDraftWorksWithoutAModelAndSavesNothing(t *testing.T) {
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	var d core.AgentDraft
	a.call("agent.draft", map[string]any{"description": "Reviews pull requests for security problems. Never edit code, just comment. Use the best model.", "projectId": p.ID}, &d)
	if d.Source != "rules" || d.Name != "Security Reviewer" || d.Adapter != "claude" || d.PermissionMode != "plan" || d.Model != "opus" || d.Effort != "high" || d.Why == "" {
		t.Fatalf("draft %+v", d)
	}
	var agents []core.Agent
	a.call("agent.list", map[string]any{"projectId": p.ID}, &agents)
	if len(agents) != 0 {
		t.Fatalf("a draft is not saved: %+v", agents)
	}
	// drafting put the description in no event (creating the agent then records its instructions, as any agent's)
	var n int
	if err := a.c.DB.Reader().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM events WHERE CAST(data AS TEXT) LIKE '%security problems%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the description reached %d events (%v)", n, err)
	}
	// the draft is what agent.create takes
	var ag core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: d.Name, Role: d.Role, Adapter: d.Adapter, Model: d.Model, Effort: d.Effort, PermissionMode: d.PermissionMode, Purpose: d.Purpose}, &ag)
	if ag.PermissionMode != "plan" || ag.Model != "opus" {
		t.Fatalf("created %+v", ag)
	}
	for name, in := range map[string]map[string]any{
		"empty":           {"description": "  "},
		"too long":        {"description": strings.Repeat("x", core.MaxDraftDescription+1)},
		"unknown project": {"description": "a tester", "projectId": "prj_nope"},
	} {
		if err := a.callErr("agent.draft", in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestEverywhereSavesTheChangeToTheTemplateAndItsOtherAgents(t *testing.T) {
	a := open(t, "")
	var p1, p2 core.Project
	a.call("project.create", map[string]any{"name": "one", "repoPath": newRepo(t)}, &p1)
	a.call("project.create", map[string]any{"name": "two", "repoPath": newRepo(t)}, &p2)
	var tpl core.AgentTemplate
	a.call("agenttemplate.save", core.AgentTemplate{Name: "Backend", Role: "backend", Adapter: "claude", Model: "sonnet", Effort: "medium", Purpose: "Write Go."}, &tpl)
	var r1, r2 core.AppliedTemplate
	a.call("agenttemplate.apply", map[string]string{"templateId": tpl.ID, "projectId": p1.ID}, &r1)
	a.call("agenttemplate.apply", map[string]string{"templateId": tpl.ID, "projectId": p2.ID}, &r2)
	if r1.Agent.TemplateID != tpl.ID {
		t.Fatalf("the applied agent records its template: %+v", r1.Agent)
	}
	ctx := context.Background()
	get := func(id string) core.Agent {
		v, err := a.c.GetAgent(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return v.Agent
	}
	if get(r2.Agent.ID).TemplateID != tpl.ID {
		t.Fatal("the link is read back")
	}

	// This project: only this agent changes
	var ag core.Agent
	a.call("agent.update", map[string]any{"id": r1.Agent.ID, "model": "opus", "effort": "high"}, &ag)
	if ag.Model != "opus" || get(r2.Agent.ID).Model != "sonnet" {
		t.Fatalf("this project only: %+v / %+v", ag, get(r2.Agent.ID))
	}

	// Everywhere: the template and the other project's agent take the fields that were sent; the rest stays theirs
	a.call("agent.update", map[string]any{"id": r2.Agent.ID, "purpose": "Change only the billing code."}, nil)
	var ev core.UpdatedEverywhere
	a.call("agent.update", map[string]any{"id": r1.Agent.ID, "effort": "max", "permissionMode": "plan", "everywhere": true}, &ev)
	if ev.ID != r1.Agent.ID || ev.Effort != "max" || ev.Everywhere == nil || ev.Everywhere.TemplateID != tpl.ID || ev.Everywhere.Agents != 1 || ev.Everywhere.Created {
		t.Fatalf("everywhere %+v %+v", ev, ev.Everywhere)
	}
	var tpls []core.AgentTemplate
	a.call("agenttemplate.list", nil, &tpls)
	if len(tpls) != 1 || tpls[0].Effort != "max" || tpls[0].PermissionMode != "plan" || tpls[0].Model != "sonnet" || tpls[0].Purpose != "Write Go." {
		t.Fatalf("template %+v", tpls)
	}
	o := get(r2.Agent.ID)
	if o.Effort != "max" || o.PermissionMode != "plan" || o.Model != "sonnet" || o.Purpose != "Change only the billing code." {
		t.Fatalf("the other agent %+v", o)
	}

	// a mode that skips approvals needs the confirmation everywhere too
	if err := a.callErr("agent.update", map[string]any{"id": r1.Agent.ID, "permissionMode": "bypassPermissions", "everywhere": true}); err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("unconfirmed: %v", err)
	}

	// an agent without a template is saved as one (and linked), then a later Everywhere updates it
	var solo core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p1.ID, Name: "Solo", Role: "tester", Adapter: "claude", Model: "haiku"}, &solo)
	raw, err := a.c.Call(ctx, "agent.update", mustJSON(map[string]any{"id": solo.ID, "effort": "low", "everywhere": true}))
	if err != nil {
		t.Fatal(err)
	}
	var made core.UpdatedEverywhere
	_ = json.Unmarshal(raw, &made)
	if made.Everywhere == nil || !made.Everywhere.Created || made.Everywhere.TemplateName != "Solo" || get(solo.ID).TemplateID != made.Everywhere.TemplateID {
		t.Fatalf("saved as a template: %s", raw)
	}
	a.call("agent.update", map[string]any{"id": solo.ID, "model": "sonnet", "everywhere": true}, &made)
	a.call("agenttemplate.list", nil, &tpls)
	if len(tpls) != 2 {
		t.Fatalf("one template per agent: %+v", tpls)
	}
	for _, x := range tpls {
		if x.Name == "Solo" && (x.Model != "sonnet" || x.Effort != "low") {
			t.Fatalf("the Solo template follows: %+v", x)
		}
	}

	// deleting a template unlinks its agents; they keep their settings
	a.call("agenttemplate.delete", map[string]string{"id": tpl.ID}, nil)
	if g := get(r1.Agent.ID); g.TemplateID != "" || g.Effort != "max" {
		t.Fatalf("after the template was deleted: %+v", g)
	}
}

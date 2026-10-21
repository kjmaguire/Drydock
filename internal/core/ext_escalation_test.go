package core_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
)

// Audit 2026-10-16, M1: a permission mode that lets an agent act without asking is set only with the person's explicit
// confirmation, on every path that gives an agent or a template its mode, and the confirmation is recorded as an approval of
// kind permission_mode_escalation decided by the person.
func TestAPermissionModeThatSkipsApprovalsNeedsTheirConfirmationOnEveryPath(t *testing.T) {
	a := open(t, "")
	p, _ := a.setup("gocalc")
	agentCount := func() int {
		var list []core.Agent
		a.call("agent.list", map[string]string{"projectId": p.ID}, &list)
		return len(list)
	}
	escalations := func() (n int, decidedBy []string) {
		rows, err := a.c.DB.Reader().Query(`SELECT status, COALESCE(decided_by,'') FROM approvals WHERE action_kind='permission_mode_escalation' ORDER BY created_at, rowid`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var st, by string
			if err := rows.Scan(&st, &by); err != nil {
				t.Fatal(err)
			}
			if st != "CONSUMED" {
				t.Fatalf("an escalation approval is %s; the person's confirmation is decided and used at once", st)
			}
			n++
			decidedBy = append(decidedBy, by)
		}
		return n, decidedBy
	}
	needs := func(err error, what string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "needs your confirmation") || !strings.Contains(err.Error(), "confirmSkipsApprovals") {
			t.Fatalf("%s was not refused for want of the confirmation: %v", what, err)
		}
	}

	// a new agent
	for _, mode := range []string{"bypassPermissions", "auto", "dontAsk"} {
		needs(a.callErr("agent.create", map[string]any{"projectId": p.ID, "name": "W", "adapter": "claude", "role": "backend", "permissionMode": mode}), "agent.create "+mode)
	}
	if agentCount() != 0 {
		t.Fatal("a refused agent was created")
	}
	if n, _ := escalations(); n != 0 {
		t.Fatal("a refusal left an approval behind")
	}
	var ag core.Agent
	a.call("agent.create", map[string]any{"projectId": p.ID, "name": "W", "adapter": "claude", "role": "backend", "permissionMode": "bypassPermissions", "confirmSkipsApprovals": true}, &ag)
	if ag.PermissionMode != "bypassPermissions" {
		t.Fatalf("created %+v", ag)
	}
	if n, by := escalations(); n != 1 || by[0] != "human:kyle" {
		t.Fatalf("escalation approvals %d decided by %v", n, by)
	}
	// the audit log says who allowed it
	var requested, decided int
	_ = a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='approval.requested' AND detail LIKE '%permission_mode_escalation%'`).Scan(&requested)
	_ = a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action LIKE 'approval.%' AND actor='human:kyle'`).Scan(&decided)
	if requested != 1 || decided == 0 {
		t.Fatalf("audit log: %d requests of the kind, %d entries by the person", requested, decided)
	}

	// an ordinary agent changes its mode
	var plain core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "R", Adapter: "claude", Role: "reviewer"}, &plain)
	needs(a.callErr("agent.update", map[string]any{"id": plain.ID, "permissionMode": "auto"}), "agent.update auto")
	a.call("agent.update", map[string]any{"id": plain.ID, "permissionMode": "auto", "confirmSkipsApprovals": true}, &plain)
	if plain.PermissionMode != "auto" {
		t.Fatalf("updated %+v", plain)
	}
	if n, _ := escalations(); n != 2 {
		t.Fatalf("%d escalation approvals", n)
	}
	// an agent that has such a mode keeps it while its other fields are edited, and changing back needs nothing
	a.call("agent.update", map[string]any{"id": plain.ID, "purpose": "review carefully"}, &plain)
	a.call("agent.update", map[string]any{"id": plain.ID, "permissionMode": "plan"}, &plain)
	if plain.PermissionMode != "plan" {
		t.Fatalf("back to plan: %+v", plain)
	}
	if n, _ := escalations(); n != 2 {
		t.Fatalf("editing or lowering the mode created approvals: %d", n)
	}

	// a template
	tpl := map[string]any{"name": "Fast", "role": "backend", "adapter": "claude", "permissionMode": "dontAsk"}
	needs(a.callErr("agenttemplate.save", tpl), "agenttemplate.save")
	tpl["confirmSkipsApprovals"] = true
	var saved core.AgentTemplate
	a.call("agenttemplate.save", tpl, &saved)
	if saved.ID == "" || saved.PermissionMode != "dontAsk" {
		t.Fatalf("template %+v", saved)
	}
	// renaming it does not ask again (the mode did not change); dropping the agent from it needs no confirmation either
	a.call("agenttemplate.save", map[string]any{"id": saved.ID, "name": "Faster", "role": "backend", "adapter": "claude", "permissionMode": "dontAsk"}, &saved)
	before := agentCount()
	needs(a.callErr("agenttemplate.apply", map[string]any{"templateId": saved.ID, "projectId": p.ID}), "agenttemplate.apply")
	if agentCount() != before {
		t.Fatal("a refused apply created an agent")
	}
	var applied core.AppliedTemplate
	a.call("agenttemplate.apply", map[string]any{"templateId": saved.ID, "projectId": p.ID, "confirmSkipsApprovals": true}, &applied)
	if applied.Agent.PermissionMode != "dontAsk" || agentCount() != before+1 {
		t.Fatalf("applied %+v", applied.Agent)
	}

	// a prebuilt agent with the mode chosen for it
	var list []struct {
		ID string `json:"id"`
	}
	a.call("agentcatalog.list", map[string]string{"projectId": p.ID}, &list)
	if len(list) == 0 {
		t.Fatal("no prebuilt agents")
	}
	req := map[string]any{"id": list[0].ID, "projectId": p.ID, "permissionMode": "bypassPermissions"}
	needs(a.callErr("agentcatalog.apply", req), "agentcatalog.apply")
	req["confirmSkipsApprovals"] = true
	var done core.PrebuiltApplied
	a.call("agentcatalog.apply", req, &done)
	if done.Agent.PermissionMode != "bypassPermissions" {
		t.Fatalf("prebuilt %+v", done.Agent)
	}
}

// The new-project crew passes the confirmation on to the agents it creates, and a crew member that lacks it is left out with a
// warning instead of silently getting the mode.
func TestTheCrewOfANewProjectNeedsTheConfirmationToo(t *testing.T) {
	a := open(t, "")
	var res core.SetupResult
	a.call("project.setup", core.SetupRequest{
		Name: "crewed", Repo: core.SetupRepo{Kind: "local", Path: newRepo(t)},
		Agents: []core.SetupAgent{
			{Name: "Careful", Role: "backend", Adapter: "claude", PermissionMode: "acceptEdits"},
			{Name: "Reckless", Role: "backend", Adapter: "claude", PermissionMode: "bypassPermissions"},
			{Name: "Confirmed", Role: "backend", Adapter: "claude", PermissionMode: "auto", ConfirmSkipsApprovals: true},
		},
	}, &res)
	var names []string
	for _, ag := range res.Agents {
		names = append(names, ag.Name+":"+ag.PermissionMode)
	}
	if strings.Join(names, ",") != "Careful:acceptEdits,Confirmed:auto" {
		t.Fatalf("crew %v; warnings %v", names, res.Warnings)
	}
	var warned bool
	for _, w := range res.Warnings {
		warned = warned || (strings.Contains(w, "Reckless") && strings.Contains(w, "needs your confirmation"))
	}
	if !warned {
		t.Fatalf("no warning for the crew member that was left out: %v", res.Warnings)
	}
}

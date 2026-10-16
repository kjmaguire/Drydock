package core_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
)

func TestAgentTemplatesAreDefinedOnceAndAppliedToProjects(t *testing.T) {
	self, _ := os.Executable()
	a := openMCPApp(t, &claude.Adapter{Exe: os.Args[0]})
	p1, _ := a.setup("one")
	p2, _ := a.setup("two")
	var lib core.MCPLibraryServer
	a.call("mcplib.save", core.MCPServerInput{ID: "docs", Display: "Docs", Transport: "stdio", Argv: []string{self}, Env: map[string]string{testMCPEnv: "1"}}, &lib)

	// the same checks an agent gets
	for name, in := range map[string]core.AgentTemplate{
		"no name":               {Role: "backend", Adapter: "claude"},
		"unknown adapter":       {Name: "x", Role: "backend", Adapter: "nonesuch"},
		"effort claude rejects": {Name: "x", Role: "backend", Adapter: "claude", Effort: "ultra"},
		"model like a flag":     {Name: "x", Role: "backend", Adapter: "claude", Model: "--x"},
		"unknown MCP server":    {Name: "x", Role: "backend", Adapter: "claude", MCPServers: []string{"nope"}},
	} {
		if err := a.callErr("agenttemplate.save", in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	var tpl core.AgentTemplate
	a.call("agenttemplate.save", core.AgentTemplate{Name: "Payments dev", Role: "backend", Purpose: "Own payments. Never touch the UI.", Adapter: "claude", Model: "sonnet", Effort: "high", MCPServers: []string{"docs"}}, &tpl)
	if tpl.ID == "" || tpl.Purpose == "" || len(tpl.MCPServers) != 1 {
		t.Fatalf("template %+v", tpl)
	}

	// apply into project one: an ordinary agent with the template's settings, granted the server, and the server is now in the project
	var res core.AppliedTemplate
	a.call("agenttemplate.apply", map[string]string{"templateId": tpl.ID, "projectId": p1.ID}, &res)
	ag := res.Agent
	if ag.ProjectID != p1.ID || ag.Name != "Payments dev" || ag.Role != "backend" || ag.Model != "sonnet" || ag.Effort != "high" || ag.Purpose != tpl.Purpose {
		t.Fatalf("applied %+v", ag)
	}
	if strings.Join(res.Granted, ",") != "docs" || len(res.Skipped) != 0 {
		t.Fatalf("grants %+v", res)
	}
	var servers []core.MCPServerView
	a.call("mcpserver.list", map[string]string{"projectId": p1.ID}, &servers)
	if len(servers) != 1 || len(servers[0].Agents) != 1 || servers[0].Agents[0].AgentID != ag.ID {
		t.Fatalf("project one servers %+v", servers)
	}
	a.call("mcpserver.list", map[string]string{"projectId": p2.ID}, &servers)
	if len(servers) != 0 {
		t.Fatal("applying a template in one project touched another")
	}

	// a second copy in the same project is told apart by a number; a name can be given
	a.call("agenttemplate.apply", map[string]string{"templateId": tpl.ID, "projectId": p1.ID}, &res)
	if res.Agent.Name != "Payments dev 2" {
		t.Fatalf("second copy %q", res.Agent.Name)
	}
	a.call("agenttemplate.apply", map[string]string{"templateId": tpl.ID, "projectId": p2.ID, "name": "Billing"}, &res)
	if res.Agent.Name != "Billing" || res.Agent.ProjectID != p2.ID {
		t.Fatalf("named copy %+v", res.Agent)
	}

	// practices default on and are carried by the template
	if tpl.Practices == nil || !*tpl.Practices || !ag.Practices {
		t.Fatalf("practices should default on: template %v agent %v", tpl.Practices, ag.Practices)
	}

	// no link: editing the template changes nothing that exists
	a.call("agenttemplate.save", core.AgentTemplate{ID: tpl.ID, Name: "Payments dev", Role: "backend", Purpose: "Changed.", Adapter: "claude"}, &tpl)
	got, err := a.c.GetAgent(context.Background(), ag.ID)
	if err != nil || got.Agent.Purpose != "Own payments. Never touch the UI." {
		t.Fatalf("the agent followed the template: %+v %v", got.Agent, err)
	}

	// save an existing agent as a template, with its servers; a server that was disconnected is skipped, not fatal
	var fromAgent core.AgentTemplate
	a.call("agenttemplate.fromAgent", map[string]string{"agentId": ag.ID}, &fromAgent)
	if fromAgent.Purpose == "" || strings.Join(fromAgent.MCPServers, ",") != "docs" {
		t.Fatalf("from agent %+v", fromAgent)
	}
	a.call("mcplib.delete", map[string]string{"id": "docs"}, nil)
	a.call("agenttemplate.apply", map[string]string{"templateId": fromAgent.ID, "projectId": p2.ID, "name": "Late"}, &res)
	if len(res.Granted) != 0 || len(res.Skipped) != 1 || !strings.HasPrefix(res.Skipped[0], "docs") {
		t.Fatalf("a disconnected server must be reported as skipped: %+v", res)
	}

	var all []core.AgentTemplate
	a.call("agenttemplate.list", nil, &all)
	if len(all) != 2 {
		t.Fatalf("templates %+v", all)
	}
	a.call("agenttemplate.delete", map[string]string{"id": tpl.ID}, nil)
	a.call("agenttemplate.list", nil, &all)
	if len(all) != 1 {
		t.Fatal("not deleted")
	}
}

func TestAgentEnvironmentSettingTakesNamesOnlyAndIsolationIsStatedPlainly(t *testing.T) {
	a := open(t, "")
	var v core.AgentEnvView
	a.call("agentenv.get", nil, &v)
	if len(v.Pass) != 0 || len(v.Kept) == 0 || len(v.Prefixes) != 2 {
		t.Fatalf("default %+v", v)
	}
	for _, bad := range [][]string{{"AWS_PROFILE=work"}, {"two words"}, {"TOKEN=abc123"}} {
		err := a.callErr("agentenv.set", map[string]any{"names": bad})
		if err == nil {
			t.Errorf("%v was accepted: a value must never be stored", bad)
			continue
		}
		// a pasted NAME=value may carry a secret: the refusal says which line, and does not repeat it (audit 2026-10-16, L38)
		for _, leak := range []string{"work", "abc123", "two words", "TOKEN", "AWS_PROFILE"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("the refusal of %v repeats %q: %v", bad, leak, err)
			}
		}
		if !strings.Contains(err.Error(), "line 1") {
			t.Errorf("the refusal of %v does not say which line: %v", bad, err)
		}
	}
	// the second line is the one named
	if err := a.callErr("agentenv.set", map[string]any{"names": []string{"AWS_PROFILE", "TOKEN=abc123"}}); err == nil || !strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), "abc123") {
		t.Errorf("the second line was not named alone: %v", err)
	}
	a.call("agentenv.set", map[string]any{"names": []string{"AWS_PROFILE", " aws_profile ", "CLAUDE_CODE_USE_BEDROCK"}}, &v)
	if strings.Join(v.Pass, ",") != "AWS_PROFILE,CLAUDE_CODE_USE_BEDROCK" {
		t.Fatalf("saved %+v", v.Pass)
	}

	p, _ := a.setup("gocalc")
	var ag core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "W", Adapter: "claude", Role: "backend"}, &ag)
	var d core.AgentDetail
	a.call("agent.get", map[string]string{"id": ag.ID}, &d)
	if d.Isolation.Level != "guarded" || !strings.Contains(d.Isolation.Summary, "not an operating-system sandbox") {
		t.Fatalf("isolation %+v", d.Isolation)
	}
	a.call("agent.update", map[string]any{"id": ag.ID, "permissionMode": "bypassPermissions", "confirmSkipsApprovals": true}, &ag)
	a.call("agent.get", map[string]string{"id": ag.ID}, &d)
	if d.Isolation.Level != "open" || !strings.Contains(d.Isolation.Summary, "bypassPermissions") {
		t.Fatalf("a mode that skips approvals must say the agent is not held in: %+v", d.Isolation)
	}
}

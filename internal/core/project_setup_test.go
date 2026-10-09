package core_test

import (
	"os"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
)

// The wizard's one call: a project with a brief, a local repository, a connector from the library, a custom agent granted that
// connector and an agent from a saved template. A step that fails after the project exists becomes a warning.
func TestProjectSetupMakesTheWholeProject(t *testing.T) {
	self, _ := os.Executable()
	a := openMCPApp(t, &claude.Adapter{Exe: os.Args[0]})
	var lib core.MCPLibraryServer
	a.call("mcplib.save", core.MCPServerInput{ID: "docs", Display: "Docs", Transport: "stdio", Argv: []string{self}, Env: map[string]string{testMCPEnv: "1"}}, &lib)
	var tpl core.AgentTemplate
	a.call("agenttemplate.save", core.AgentTemplate{Name: "Reviewer", Role: "reviewer", Adapter: "claude", Model: "sonnet", Effort: "high"}, &tpl)

	for name, req := range map[string]core.SetupRequest{
		"no name and no repository": {},
		"local without a path":      {Name: "x", Repo: core.SetupRepo{Kind: "local"}},
		"unknown repo kind":         {Name: "x", Repo: core.SetupRepo{Kind: "svn"}},
		"bad mode":                  {Name: "x", Mode: "yolo"},
		"incomplete custom agent":   {Name: "x", Agents: []core.SetupAgent{{Name: "a"}}},
		"prebuilt and template":     {Name: "x", Agents: []core.SetupAgent{{Prebuilt: "team-lead", Template: tpl.ID}}},
		"brief too long":            {Name: "x", Brief: strings.Repeat("b", core.MaxBriefLen+1)},
	} {
		if err := a.callErr("project.setup", req); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	var projects []core.Project
	a.call("project.list", nil, &projects)
	if len(projects) != 0 {
		t.Fatalf("a refused request must not leave a project behind: %+v", projects)
	}

	var res core.SetupResult
	a.call("project.setup", core.SetupRequest{
		Name: "Calc", Brief: "A tiny calculator library. Keep the API stable.",
		Repo:           core.SetupRepo{Kind: "local", Path: newRepo(t)},
		LibraryServers: []string{"docs"},
		Agents: []core.SetupAgent{
			{Name: "Wren", Role: "backend", Adapter: "claude", Model: "sonnet", Effort: "medium", Servers: []string{"docs", "nonesuch"}},
			{Template: tpl.ID},
			{Name: "Broken", Role: "backend", Adapter: "nonesuch"},
		},
	}, &res)
	if res.Project.ID == "" || !res.Project.HasRepo || res.Project.Brief != "A tiny calculator library. Keep the API stable." {
		t.Fatalf("project %+v", res.Project)
	}
	if len(res.Agents) != 2 || res.Agents[0].Name != "Wren" || res.Agents[1].Name != "Reviewer" {
		t.Fatalf("agents %+v", res.Agents)
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, "Broken") || !strings.Contains(joined, "nonesuch") {
		t.Fatalf("warnings must name what failed: %v", res.Warnings)
	}
	var servers []core.MCPServerView
	a.call("mcpserver.list", map[string]string{"projectId": res.Project.ID}, &servers)
	if len(servers) != 1 || servers[0].ID != "docs" {
		t.Fatalf("project servers %+v", servers)
	}
	granted := false
	for _, g := range servers[0].Agents {
		if g.AgentID == res.Agents[0].ID {
			granted = true
		}
	}
	if !granted {
		t.Fatalf("Wren must be granted docs: %+v", servers[0].Agents)
	}

	// prebuilt agents and built-in catalog connectors (no network: nothing is contacted when a server is added)
	var pre core.SetupResult
	a.call("project.setup", core.SetupRequest{
		Name: "Docs helpers", CatalogServers: []string{"mslearn"},
		Agents: []core.SetupAgent{{Prebuilt: "team-lead"}, {Prebuilt: "microsoft-engineer", Name: "Azure"}, {Prebuilt: "no-such-agent"}},
	}, &pre)
	if len(pre.Agents) != 2 || pre.Agents[0].Role != "lead" || pre.Agents[1].Name != "Azure" {
		t.Fatalf("prebuilt agents %+v warnings %v", pre.Agents, pre.Warnings)
	}
	if !strings.Contains(strings.Join(pre.Warnings, "\n"), "no-such-agent") {
		t.Fatalf("an unknown prebuilt agent must be a warning: %v", pre.Warnings)
	}
	var preServers []core.MCPServerView
	a.call("mcpserver.list", map[string]string{"projectId": pre.Project.ID}, &preServers)
	foundLearn, azureGranted := false, false
	for _, sv := range preServers {
		if strings.Contains(sv.ID, "mslearn") {
			foundLearn = true
			for _, g := range sv.Agents {
				azureGranted = azureGranted || g.AgentID == pre.Agents[1].ID
			}
		}
	}
	if !foundLearn || !azureGranted {
		t.Fatalf("Microsoft Learn must be in the project and granted to the Microsoft engineer: %+v", preServers)
	}

	// no repository: just a name
	var bare core.SetupResult
	a.call("project.setup", core.SetupRequest{Name: "Someday"}, &bare)
	if bare.Project.HasRepo || bare.Project.Name != "Someday" {
		t.Fatalf("bare %+v", bare.Project)
	}
}

// With no agent CLI usable on this machine, the setup says so (and where to fix it) instead of returning no warning.
func TestProjectSetupWarnsWhenTheTeamsCLIIsNotUsable(t *testing.T) {
	a := openMCPApp(t, &claude.Adapter{Exe: os.Args[0]})
	t.Setenv("PATH", t.TempDir()) // no claude on this machine
	var res core.SetupResult
	a.call("project.setup", core.SetupRequest{Name: "Later", Repo: core.SetupRepo{Kind: "none"},
		Agents: []core.SetupAgent{{Name: "Wren", Role: "backend", Adapter: "claude", Model: "sonnet"}, {Name: "Robin", Role: "reviewer", Adapter: "claude", Model: "sonnet"}}}, &res)
	if len(res.Agents) != 2 {
		t.Fatalf("agents %+v", res.Agents)
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, "Wren, Robin cannot work yet: the claude CLI is not usable") || !strings.Contains(joined, "Connections page") {
		t.Fatalf("warnings %v", res.Warnings)
	}
}

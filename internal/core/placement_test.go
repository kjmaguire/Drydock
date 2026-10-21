package core_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/pipeline"
)

// Decision P58: Claude Code is connected inside a WSL distro only. These tests run on the stand-in WSL (a LABELLED TEST
// DOUBLE, internal/wsl/wsltest) with no claude on this "Windows".

// standup asks the project's lead for a stand-up and returns its error.
func (a *app) standup(projectID string) (pipeline.StandupResult, error) {
	a.t.Helper()
	var res pipeline.StandupResult
	raw, err := a.c.Call(context.Background(), "lead.standup", mustJSON(map[string]string{"projectId": projectID}))
	if err == nil {
		err = json.Unmarshal(raw, &res)
	}
	return res, err
}

// A new project's agents run where Claude Code is: its default "Where agents run" is the distro, set by Drydock with the
// reason, and the team lead's stand-up runs there. Before P58 the project defaulted to Windows and the stand-up failed with
// "Claude Code is not installed on Windows, but it is installed in the WSL distro ...".
func TestANewProjectRunsItsAgentsInTheDistroWhereClaudeCodeIs(t *testing.T) {
	a, _ := openWSLOnly(t, "Ubuntu-26.04")
	p, _ := a.setup("wsl-only")
	var pr core.ProjectRunMode
	a.call("project.runMode", map[string]any{"projectId": p.ID}, &pr)
	if pr.RunMode != "wsl" || pr.WSLDistro != "Ubuntu-26.04" || pr.By != "drydock" || !strings.Contains(pr.Why, "Ubuntu-26.04") {
		t.Fatalf("the new project's default: %+v", pr)
	}
	var lead core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Lead", Adapter: "claude", Role: "lead"}, &lead)
	res, err := a.standup(p.ID)
	if err != nil {
		t.Fatalf("the stand-up failed: %v", err)
	}
	if res.MessageID == "" || !strings.Contains(res.Text, "standin") {
		t.Fatalf("the stand-in claude in the distro did not answer: %+v", res)
	}
	started := a.eventsOf("agent.started")
	if len(started) == 0 || started[len(started)-1]["runMode"] != "wsl" || started[len(started)-1]["wslDistro"] != "Ubuntu-26.04" {
		t.Fatalf("the lead's turn did not run in the distro: %v", started)
	}
	if len(a.eventsOf("agent.run_mode_auto")) != 0 {
		t.Fatal("the project's own default placed the turn; Drydock did not choose at run time")
	}
}

// A project whose default was never set (one made before the Claude Code connection, or before P58) follows Drydock's choice
// at run time: with Claude Code not installed on Windows, the lead's turn runs in the WSL default distro among those where it
// is ready, and the run's events say so. An agent a person set to Windows, and a project a person set to Windows, are not
// moved: the clear error stays.
func TestAnAgentFollowingTheDefaultRunsWhereClaudeCodeIsAndAPersonsChoiceIsKept(t *testing.T) {
	a, _ := openWSLOnly(t, "Ubuntu-26.04", "Debian")
	p, _ := a.setup("older")
	if _, err := a.c.DB.Writer().Exec(`DELETE FROM settings WHERE key=?`, "runmode:"+p.ID); err != nil {
		t.Fatal(err)
	}
	var pr core.ProjectRunMode
	a.call("project.runMode", map[string]any{"projectId": p.ID}, &pr)
	if pr.RunMode != "native" || pr.By != "" {
		t.Fatalf("%+v", pr)
	}
	var lead core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Lead", Adapter: "claude", Role: "lead"}, &lead)
	if _, err := a.standup(p.ID); err != nil {
		t.Fatalf("the stand-up failed: %v", err)
	}
	auto := a.eventsOf("agent.run_mode_auto")
	if len(auto) != 1 || auto[0]["wslDistro"] != "Ubuntu-26.04" || auto[0]["_agent"] != lead.ID ||
		!strings.Contains(auto[0]["why"].(string), "Claude Code is not installed on Windows") {
		t.Fatalf("Drydock's choice is not recorded: %v", auto)
	}
	started := a.eventsOf("agent.started")
	if last := started[len(started)-1]; last["wslDistro"] != "Ubuntu-26.04" || last["runModeWhy"] == nil {
		t.Fatalf("%v", last)
	}

	// a person chose Windows for this agent: no silent move
	native := "native"
	a.call("agent.update", core.AgentUpdate{ID: lead.ID, RunMode: &native}, &lead)
	if _, err := a.standup(p.ID); err == nil || !strings.Contains(err.Error(), "not installed on Windows") || !strings.Contains(err.Error(), "Where agents run") {
		t.Fatalf("an agent set to Windows was moved: %v", err)
	}
	follow := ""
	a.call("agent.update", core.AgentUpdate{ID: lead.ID, RunMode: &follow}, &lead)
	if _, err := a.standup(p.ID); err != nil {
		t.Fatalf("following the default again: %v", err)
	}

	// a person chose Windows for the project's agents: no silent move either
	a.call("project.setRunMode", map[string]any{"projectId": p.ID, "runMode": "native"}, &pr)
	if _, err := a.standup(p.ID); err == nil || !strings.Contains(err.Error(), "not installed on Windows") {
		t.Fatalf("a project set to Windows was moved: %v", err)
	}
	if n := len(a.eventsOf("agent.run_mode_auto")); n != 2 {
		t.Fatalf("%d automatic placements, want 2", n)
	}
}

// The agent CLI pickers count Claude Code installed in a WSL distro as available, naming the distro (decision P58).
func TestTheCLIPickersCountClaudeCodeInWSLAsAvailable(t *testing.T) {
	a, _ := openWSLOnly(t, "Ubuntu-26.04")
	p, _ := a.setup("pickers")
	var list []core.PrebuiltAgent
	a.call("agentcatalog.list", map[string]any{"projectId": p.ID}, &list)
	found := false
	for _, e := range list {
		if e.Preferred.Adapter != "claude" {
			continue
		}
		found = true
		pref := e.Availability.Adapters[0]
		if !e.Availability.PreferredUsable || !pref.Installed || !pref.Usable || pref.WSLDistro != "Ubuntu-26.04" {
			t.Fatalf("%s: %+v", e.ID, e.Availability)
		}
	}
	if !found {
		t.Fatal("no prebuilt agent prefers Claude Code")
	}
	var adapters []map[string]any
	a.call("adapter.list", struct{}{}, &adapters)
	for _, ad := range adapters {
		if ad["id"] == "claude" && ad["wslDistro"] != "Ubuntu-26.04" {
			t.Fatalf("adapter.list: %v", ad)
		}
		if ad["id"] == "codex" && ad["wslDistro"] != nil {
			t.Fatalf("codex cannot run in WSL: %v", ad)
		}
	}
}

// With Claude Code on Windows nothing moves: the new project's default stays on Windows and the pickers name no distro.
func TestWithClaudeCodeOnWindowsNothingMoves(t *testing.T) {
	a, _ := openWSL(t)
	p, _ := a.setup("native")
	var pr core.ProjectRunMode
	a.call("project.runMode", map[string]any{"projectId": p.ID}, &pr)
	if pr.RunMode != "native" || pr.By != "" {
		t.Fatalf("%+v", pr)
	}
	var adapters []map[string]any
	a.call("adapter.list", struct{}{}, &adapters)
	for _, ad := range adapters {
		if ad["id"] == "claude" && ad["wslDistro"] != nil {
			t.Fatalf("%v", ad)
		}
	}
}

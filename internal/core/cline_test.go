package core_test

import (
	"testing"

	"github.com/kjmaguire/drydock/internal/chats"
	"github.com/kjmaguire/drydock/internal/core"
)

// Cline (decisions P29) is built from its manifest like the other ACP CLIs: listed on the Connections page, usable for a team
// member and a chat, and given the MCP gateway only when the human allows it (P20).
func TestClineIsAConnectionATeamMemberAndAChatAdapter(t *testing.T) {
	a := open(t, "")
	p, _ := a.setup("cline")
	var conns []struct {
		Adapter     string `json:"adapter"`
		InstallHint string `json:"installHint"`
		Transport   string `json:"transport"`
	}
	a.call("connection.list", nil, &conns)
	found := false
	for _, c := range conns {
		if c.Adapter == "cline" {
			found = true
			// (the Node prerequisite is checked once the CLI is found; cline is not installed here)
			if c.InstallHint != "cmd: npm i -g --allow-scripts=cline cline" || c.Transport != "acp" {
				t.Fatalf("cline connection %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("the Connections page must list cline")
	}
	var ag core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Cliner", Adapter: "cline", Role: "backend"}, &ag)
	if ag.Adapter != "cline" || ag.PermissionMode != "acceptEdits" {
		t.Fatalf("agent %+v", ag)
	}
	var ch chats.Chat
	a.call("chat.create", map[string]any{"projectId": p.ID, "adapter": "cline", "permissionMode": "plan"}, &ch)
	if ch.Adapter != "cline" || ch.PermissionMode != "plan" {
		t.Fatalf("chat %+v", ch)
	}
	var st struct {
		Adapters []struct {
			Adapter  string `json:"adapter"`
			Attached bool   `json:"attached"`
			CanTry   bool   `json:"canTry"`
			Reason   string `json:"reason"`
		} `json:"adapters"`
	}
	a.call("mcp.status", nil, &st)
	for _, r := range st.Adapters {
		if r.Adapter == "cline" && (r.Attached || !r.CanTry || r.Reason == "") {
			t.Fatalf("cline MCP must be off by default and allowed only on request: %+v", r)
		}
	}
	a.call("mcp.try.set", map[string]any{"adapter": "cline", "enabled": true}, &st)
	for _, r := range st.Adapters {
		if r.Adapter == "cline" && !r.Attached {
			t.Fatalf("after the human allowed it: %+v", r)
		}
	}
}

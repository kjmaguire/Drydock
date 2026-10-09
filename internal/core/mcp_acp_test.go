package core_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// A CLI whose per-run MCP mechanism no real run has proven (Codex here, stood in for by a scripted ACP agent that is a real
// separate process on the real ACP SDK) gets the gateway only after the human allows it. Then the single `drydock` entry reaches
// it in session/new.mcpServers as the stdio shim, and the agent, acting as a real MCP client, can list and call the tools. This
// proves Drydock's side; it does not prove any vendor's CLI honours the entry (docs/mcp.md section 10).
func TestHumanCanAllowTheGatewayForAnUnverifiedACPCLIAndItWorksEndToEnd(t *testing.T) {
	script, _ := json.Marshal(fakeacp.Script{
		Default: []fakeacp.Step{
			{FSWrite: &fakeacp.FSOp{Path: "calc.go", Content: impl}},
			{MCPCall: &fakeacp.MCPCall{Server: "drydock"}},
			{MCPCall: &fakeacp.MCPCall{Server: "drydock", Tool: "search_memory", Args: map[string]any{"query": "subtract"}}},
			{Say: "implemented Subtract"},
		},
	})
	sp := filepath.Join(t.TempDir(), "acp-script.json")
	os.WriteFile(sp, script, 0o644)
	codex := acp.New(acp.Config{ID: "codex", Command: os.Args[0], Env: []string{"DRYDOCK_FAKE_ACP=1", "DRYDOCK_FAKE_ACP_SCRIPT=" + sp}})
	a := openWith(t, map[string]agents.Adapter{"codex": codex})
	p, _ := a.setup("gocalc")

	type statusT struct {
		Adapters []struct {
			Adapter  string
			Attached bool
			CanTry   bool
			Trying   bool
		}
	}
	row := func(id string) (r struct {
		Adapter  string
		Attached bool
		CanTry   bool
		Trying   bool
	}) {
		var st statusT
		a.call("mcp.status", nil, &st)
		for _, x := range st.Adapters {
			if x.Adapter == id {
				return x
			}
		}
		t.Fatalf("no row for %s", id)
		return
	}
	if r := row("codex"); r.Attached || !r.CanTry || r.Trying {
		t.Fatalf("by default an unverified CLI gets nothing but may be tried: %+v", r)
	}
	if r := row("antigravity"); r.CanTry {
		t.Fatalf("a CLI with no known mechanism can never be tried: %+v", r)
	}
	// only a CLI that has a mechanism can be switched on
	if err := a.callErr("mcp.try.set", map[string]any{"adapter": "antigravity", "enabled": true}); err == nil {
		t.Fatal("antigravity has no mechanism to try")
	}
	if err := a.callErr("mcp.try.set", map[string]any{"adapter": "nonesuch", "enabled": true}); err == nil {
		t.Fatal("an unknown adapter was accepted")
	}
	a.call("mcp.try.set", map[string]any{"adapter": "codex", "enabled": true}, nil)
	if r := row("codex"); !r.Attached || !r.Trying {
		t.Fatalf("after the human allowed it: %+v", r)
	}

	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Codex worker", Adapter: "codex", Role: "backend"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "make Subtract return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var detail core.TaskDetail
	a.waitFor("the human request", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1
	})

	// the event says it was attached, over the stdio shim, and that it was unproven
	var seen bool
	for _, e := range a.eventsOf("agent.mcp") {
		if e["_agent"] == worker.ID {
			seen = true
			if e["attached"] != true || e["unverified"] != true || e["transport"] != "stdio-shim" {
				t.Fatalf("agent.mcp for the ACP worker: %+v", e)
			}
		}
	}
	if !seen {
		t.Fatal("no agent.mcp event for the ACP worker")
	}
	// what the agent did as a real MCP client is in its own words: it listed the gateway's tools and called one
	var listed, called int
	rows, _ := a.c.DB.Reader().Query(`SELECT data FROM events WHERE agent_id=?`, worker.ID)
	for rows.Next() {
		var d string
		rows.Scan(&d)
		if strings.Contains(d, "mcp tools") && strings.Contains(d, "read_range") {
			listed++
		}
		if strings.Contains(d, "mcp OK") || strings.Contains(d, "mcp TOOLERR") {
			called++
		}
		if strings.Contains(d, "mcp ERR") {
			t.Fatalf("the ACP agent could not use the attached gateway: %s", d)
		}
	}
	rows.Close()
	if listed == 0 || called == 0 {
		t.Fatalf("the agent never listed (%d) or called (%d) a gateway tool through session/new.mcpServers", listed, called)
	}

	// switching it off again takes it away from the next session
	a.call("mcp.try.set", map[string]any{"adapter": "codex", "enabled": false}, nil)
	if r := row("codex"); r.Attached || r.Trying {
		t.Fatalf("after switching off: %+v", r)
	}
	a.call("run.respond", map[string]any{"runId": runID, "requestId": detail.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("completion", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return detail.Task.Status == tasks.Completed
	})
}

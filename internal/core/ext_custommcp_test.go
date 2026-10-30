package core_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/headless/fakecustom"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// customTurn starts a session on adapter as every turn starts (Runtime.Spawn), sends one message and returns the answer and the
// session's agent.mcp event.
func customTurn(t *testing.T, a *app, p core.Project, ag core.Agent, cwd string) (string, map[string]any) {
	t.Helper()
	m, err := a.c.Runtime.Spawn(context.Background(), rt.Config{AgentID: ag.ID, ProjectID: p.ID, Adapter: ag.Adapter, Spec: agents.RunSpec{AgentID: ag.ID, Role: ag.Role, Cwd: cwd}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Sess.Send(context.Background(), "use the tools"); err != nil {
		t.Fatal(err)
	}
	var text string
	deadline := time.After(60 * time.Second)
read:
	for {
		select {
		case c := <-m.Sess.Output():
			if c.Kind == workflow.ChunkMessageEnd {
				var d workflow.TextDelta
				_ = c.Decode(&d)
				text = d.Text
			}
			if c.Kind == workflow.ChunkDone {
				break read
			}
		case <-deadline:
			t.Fatalf("no answer (stderr %q)", m.Sess.Stderr())
		}
	}
	_ = m.Stop(context.Background())
	var ev map[string]any
	for _, e := range a.eventsOf("agent.mcp") {
		if e["agentRun"] == m.RunID {
			ev = e
		}
	}
	if ev == nil {
		t.Fatal("no agent.mcp event for the session")
	}
	return text, ev
}

// A person's own CLI whose manifest says how it takes an MCP configuration (decision P117) gets the gateway the way the other
// non-Claude CLIs do (P20): only after the person allows it for that CLI, then as the single drydock entry of the mcpServers
// JSON, under the role's row of the tool table. The stand-in, a real process acting as a real MCP client, reads the
// configuration it was given (a --mcp-config flag, or a file in its working folder), starts the shim, lists the gateway's
// tools and calls one. A CLI whose manifest has no mcp section can never be given it, and a file already in the working folder
// is never overwritten.
func TestPersonsOwnCLIGetsTheGatewayThroughItsManifestsMCPSection(t *testing.T) {
	a := openWith(t, nil)
	p, repo := a.setup("custom-mcp")
	t.Setenv("DRYDOCK_FAKE_CUSTOM", "1")
	sp := filepath.Join(t.TempDir(), "script.json")
	b, _ := json.Marshal(fakecustom.Script{Default: []fakecustom.Step{
		{MCP: &fakecustom.MCPCall{Server: "drydock"}},
		{MCP: &fakecustom.MCPCall{Server: "drydock", Tool: "search_memory", Args: map[string]any{"query": "subtract"}}},
	}})
	os.WriteFile(sp, b, 0o644)
	t.Setenv("DRYDOCK_FAKE_CUSTOM_SCRIPT", sp)
	a.call("agentenv.set", map[string]any{"names": []string{"DRYDOCK_FAKE_CUSTOM", "DRYDOCK_FAKE_CUSTOM_SCRIPT"}}, nil)

	withMCP := func(id, section string) string { return customYAML(t, a, id) + section + "\n" }
	// the section is checked as strictly as the rest of the manifest
	for name, c := range map[string]struct{ section, want string }{
		"another format":       {`mcp: {via: arg, format: toml, args: [--mcp-config, "{{mcp_config}}"]}`, "mcpServers"},
		"no placeholder":       {`mcp: {via: arg, format: mcpServers, args: [--mcp-config]}`, "exactly once"},
		"another placeholder":  {`mcp: {via: arg, format: mcpServers, args: [--mcp-config, "{{mcp_config}}", "{{cwd}}"]}`, "only {{mcp_config}}"},
		"a file outside":       {`mcp: {via: file, format: mcpServers, file: ../mcp.json}`, "inside the working folder"},
		"a file in .git":       {`mcp: {via: file, format: mcpServers, file: .git/mcp.json}`, ".git"},
		"an absolute file":     {`mcp: {via: file, format: mcpServers, file: /etc/mcp.json}`, "relative path"},
		"not json":             {`mcp: {via: file, format: mcpServers, file: mcp.yaml}`, ".json"},
		"an unknown key":       {`mcp: {via: arg, format: mcpServers, args: ["{{mcp_config}}"], env: X}`, "unknown key"},
		"an unknown route":     {`mcp: {via: env, format: mcpServers}`, "arg or file"},
		"args and a file both": {`mcp: {via: arg, format: mcpServers, args: ["{{mcp_config}}"], file: m.json}`, "via: file"},
	} {
		if err := a.callErr("connection.custom.add", map[string]string{"yaml": withMCP("bad", c.section)}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}

	a.call("connection.custom.add", map[string]string{"yaml": customYAML(t, a, "plain")}, nil)
	a.call("connection.custom.add", map[string]string{"yaml": withMCP("flagcli", `mcp: {via: arg, format: mcpServers, args: [--mcp-config, "{{mcp_config}}"]}`)}, nil)
	a.call("connection.custom.add", map[string]string{"yaml": withMCP("filecli", "mcp: {via: file, format: mcpServers, file: "+fakecustom.MCPFile+"}")}, nil)

	type row struct {
		Adapter                  string
		Attached, CanTry, Trying bool
		Reason                   string
	}
	rowOf := func(id string) row {
		var st struct{ Adapters []row }
		a.call("mcp.status", nil, &st)
		for _, r := range st.Adapters {
			if r.Adapter == id {
				return r
			}
		}
		t.Fatalf("no mcp.status row for %s", id)
		return row{}
	}
	if r := rowOf("plain"); r.CanTry || !strings.Contains(r.Reason, "no mcp section") {
		t.Fatalf("a custom CLI without an mcp section: %+v", r)
	}
	if err := a.callErr("mcp.try.set", map[string]any{"adapter": "plain", "enabled": true}); err == nil {
		t.Fatal("the gateway was allowed for a CLI that does not say how it takes it")
	}
	if r := rowOf("flagcli"); r.Attached || !r.CanTry || r.Trying {
		t.Fatalf("by default a custom CLI with an mcp section gets nothing but may be tried: %+v", r)
	}

	var agents []core.Agent
	for _, id := range []string{"plain", "flagcli", "filecli"} {
		a.call("connection.acknowledge", map[string]string{"gateId": "custom-" + id}, nil)
		var ag core.Agent
		a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: id, Adapter: id, Role: "backend"}, &ag)
		agents = append(agents, ag)
	}
	// not allowed yet: no gateway, and the CLI says so in its own words
	text, ev := customTurn(t, a, p, agents[1], repo)
	if ev["attached"] == true || !strings.Contains(text, "mcp ERR no configuration") {
		t.Fatalf("before the person allowed it: attached=%v answer %q", ev["attached"], text)
	}

	a.call("mcp.try.set", map[string]any{"adapter": "flagcli", "enabled": true}, nil)
	a.call("mcp.try.set", map[string]any{"adapter": "filecli", "enabled": true}, nil)
	if r := rowOf("flagcli"); !r.Attached || !r.Trying {
		t.Fatalf("after the person allowed it: %+v", r)
	}
	for _, ag := range agents[1:] {
		text, ev := customTurn(t, a, p, ag, repo)
		t.Logf("%s answered %q", ag.Adapter, text)
		if ev["attached"] != true || ev["unverified"] != true || ev["transport"] != "stdio-shim" {
			t.Fatalf("%s: agent.mcp %+v", ag.Adapter, ev)
		}
		// the stand-in read the configuration it was given, started the shim and used the gateway as a real MCP client
		if !strings.Contains(text, "mcp tools ") || !strings.Contains(text, "read_range") || !(strings.Contains(text, "mcp OK") || strings.Contains(text, "mcp TOOLERR")) || strings.Contains(text, "mcp ERR") {
			t.Fatalf("%s did not use the gateway it was given: %q", ag.Adapter, text)
		}
		// the role's row decides what it was given: a backend worker has no act tools while the project does not let agents act
		if strings.Contains(text, ",room") || strings.Contains(text, "handoff") {
			t.Errorf("%s was given tools its role's row does not hold: %q", ag.Adapter, text)
		}
	}
	// the file route: written for the run only, kept out of git, gone when the session ends
	placed := filepath.Join(repo, filepath.FromSlash(fakecustom.MCPFile))
	if _, err := os.Stat(placed); !os.IsNotExist(err) {
		t.Errorf("the configuration stays in the working folder after the session: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(placed)); !os.IsNotExist(err) {
		t.Errorf("the folder Drydock made for it stays: %v", err)
	}
	if ex, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude")); !strings.Contains(string(ex), "/"+fakecustom.MCPFile) {
		t.Errorf("the configuration's path was not kept out of git: %q", ex)
	}
	// a file the person (or the repository) has there is never overwritten: the run goes without the gateway and says why
	os.MkdirAll(filepath.Dir(placed), 0o755)
	os.WriteFile(placed, []byte(`{"mcpServers":{}}`), 0o644)
	text, ev = customTurn(t, a, p, agents[2], repo)
	if ev["attached"] == true || !strings.Contains(ev["reason"].(string), "does not overwrite") {
		t.Fatalf("over a file that was there: %+v", ev)
	}
	if got, _ := os.ReadFile(placed); string(got) != `{"mcpServers":{}}` || !strings.Contains(text, "no server \"drydock\"") {
		t.Fatalf("the file that was there was changed (%q) or read differently (%q)", got, text)
	}
	// and a CLI without an mcp section never gets the gateway
	if _, ev := customTurn(t, a, p, agents[0], repo); ev["attached"] == true || !strings.Contains(ev["reason"].(string), "no mcp section") {
		t.Fatalf("a CLI without an mcp section: %+v", ev)
	}
}

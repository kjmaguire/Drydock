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
	"github.com/kjmaguire/drydock/internal/connections"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// customYAML is the template the page offers for an id, pointed at this test binary as the program (the LABELLED stand-in of
// a person's own CLI, internal/agents/headless/fakecustom).
func customYAML(t *testing.T, a *app, id string) string {
	t.Helper()
	var tpl struct{ YAML string }
	a.call("connection.custom.template", map[string]string{"id": id}, &tpl)
	if !strings.Contains(tpl.YAML, "id: "+id+",") || !strings.Contains(tpl.YAML, "gate: {id: custom-"+id+",") {
		t.Fatalf("template for %s:\n%s", id, tpl.YAML)
	}
	return strings.Replace(tpl.YAML, "binaries: {native: [my-cli.exe]}", "binaries: {native: ['"+os.Args[0]+"']}", 1)
}

// A person adds their own CLI on the Agents page: the manifest is checked strictly and kept as a file, its Connection appears
// behind its own acknowledgement, an agent on it runs a turn through the generic runner, it comes back after a restart, and it
// is removed only once no agent runs on it.
func TestPersonAddsRunsAndRemovesTheirOwnCLI(t *testing.T) {
	a := openWith(t, nil)
	p, repo := a.setup("custom")
	t.Setenv("DRYDOCK_FAKE_CUSTOM", "1")
	sp := filepath.Join(t.TempDir(), "script.json")
	b, _ := json.Marshal(fakecustom.Script{Default: []fakecustom.Step{{Say: "OK from my own CLI"}}})
	os.WriteFile(sp, b, 0o644)
	t.Setenv("DRYDOCK_FAKE_CUSTOM_SCRIPT", sp)
	// agents start from a scrubbed environment: the stand-in's switches reach it the way a person passes a variable on
	a.call("agentenv.set", map[string]any{"names": []string{"DRYDOCK_FAKE_CUSTOM", "DRYDOCK_FAKE_CUSTOM_SCRIPT"}}, nil)

	y := customYAML(t, a, "mine")
	for name, c := range map[string]struct{ yaml, want string }{
		"a built-in id":    {customYAML(t, a, "claude"), "built-in"},
		"a secret":         {strings.Replace(y, "argv: [run, --json, --cwd, \"{{cwd}}\"]", "argv: [run, --json, --token, ghp_1a2B3c4D5e6F7g8H9i0J1k2L3m4N5o6P7q8R]", 1), "secret"},
		"a broken dialect": {strings.Replace(y, "text: $.text", "text: text", 1), "field path"},
		"the template":     {strings.Replace(y, "id: mine,", "id: custom,", 1), "not the template's"},
	} {
		if err := a.callErr("connection.custom.add", map[string]string{"yaml": c.yaml}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	var conn connections.Connection
	a.call("connection.custom.add", map[string]string{"yaml": y}, &conn)
	if !conn.Custom || !conn.Detection.Installed || len(conn.Gates) != 1 || conn.Gates[0].ID != "custom-mine" || conn.Gates[0].Acked {
		t.Fatalf("connection %+v", conn)
	}
	file := filepath.Join(a.dataDir(), "adapters", "custom", "mine.yaml")
	if got, err := os.ReadFile(file); err != nil || string(got) != y {
		t.Fatalf("kept as %s: %v", file, err)
	}
	if err := a.callErr("connection.custom.add", map[string]string{"yaml": y}); err == nil || !strings.Contains(err.Error(), "exists already") {
		t.Errorf("added twice: %v", err)
	}

	// behind its own acknowledgement
	spec := core.AgentSpec{ProjectID: p.ID, Name: "Mine", Adapter: "mine", Role: "backend"}
	if err := a.callErr("agent.create", spec); err == nil || !strings.Contains(err.Error(), "custom-mine") {
		t.Fatalf("an agent before the acknowledgement: %v", err)
	}
	a.call("connection.acknowledge", map[string]string{"gateId": "custom-mine"}, nil)
	var ag core.Agent
	a.call("agent.create", spec, &ag)

	// a turn through the generic runner, as every turn starts (Runtime.Spawn); no MCP for a CLI with no known mechanism
	m, err := a.c.Runtime.Spawn(context.Background(), rt.Config{AgentID: ag.ID, ProjectID: p.ID, Adapter: "mine", Spec: agents.RunSpec{AgentID: ag.ID, Role: ag.Role, Cwd: repo}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Sess.Send(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	var text string
	deadline := time.After(20 * time.Second)
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
	if text != "OK from my own CLI" {
		t.Fatalf("answer %q", text)
	}
	_ = m.Stop(context.Background())
	attached := true
	for _, e := range a.eventsOf("agent.mcp") {
		if e["_agent"] == ag.ID {
			attached = e["attached"] == true
		}
	}
	if attached {
		t.Error("a custom CLI gets no MCP until it declares how (docs/mcp.md section 10)")
	}

	// it is listed, and it is not removed while an agent runs on it
	var list core.CustomCLIs
	a.call("connection.custom.list", nil, &list)
	if len(list.Files) != 1 || list.Files[0].ID != "mine" || list.Files[0].Error != "" {
		t.Fatalf("list %+v", list)
	}
	if err := a.callErr("connection.custom.remove", map[string]string{"id": "mine"}); err == nil || !strings.Contains(err.Error(), "1 agent") {
		t.Fatalf("removed under an agent: %v", err)
	}
	if err := a.callErr("connection.custom.remove", map[string]string{"id": "claude"}); err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Fatalf("a built-in CLI: %v", err)
	}
	a.call("agent.archive", map[string]string{"id": ag.ID}, nil)
	a.call("connection.custom.remove", map[string]string{"id": "mine"}, &list)
	if len(list.Files) != 0 {
		t.Fatalf("after remove %+v", list)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("the file stays: %v", err)
	}
	var conns []connections.Connection
	a.call("connection.list", nil, &conns)
	for _, c := range conns {
		if c.Adapter == "mine" {
			t.Fatal("the Connection stays after remove")
		}
	}
	// added again it is a new program: acknowledged again
	a.call("connection.custom.add", map[string]string{"yaml": y}, &conn)
	if conn.Gates[0].Acked {
		t.Error("the acknowledgement of the removed CLI carried over")
	}

	// a restart loads the folder: the CLI is there, and a file that does not validate is listed with why
	os.WriteFile(filepath.Join(a.dataDir(), "adapters", "custom", "broken.yaml"), []byte("meta: ["), 0o600)
	dir := a.dataDir()
	a.c.Close()
	c2, err := core.Open(context.Background(), core.Config{DataDir: dir, User: "kyle", LeaseTTL: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c2.Close)
	b2 := &app{t: t, c: c2}
	b2.call("connection.custom.list", nil, &list)
	if len(list.Files) != 2 || list.Files[0].ID != "broken" || list.Files[0].Error == "" || list.Files[1].ID != "mine" || list.Files[1].Error != "" {
		t.Fatalf("after a restart %+v", list)
	}
	b2.call("connection.check", map[string]string{"adapter": "mine"}, &conn)
	if !conn.Custom || !conn.Detection.Installed {
		t.Fatalf("loaded at start: %+v", conn)
	}
}

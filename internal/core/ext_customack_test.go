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
)

// The acknowledgement of a person's own CLI is bound to its manifest file (decision P117): when the file's bytes change on disk
// the CLI is owed the acknowledgement again (the Agents page says why), no session of it starts until a person gives it, and
// the acknowledgement given then is for the new file, which is what runs. A file that no longer loads cannot be acknowledged.
// The same holds across a restart, and an acknowledgement kept from before the hash was is asked again once.
func TestPersonsOwnCLIIsAskedAgainWhenItsManifestFileChanges(t *testing.T) {
	a := openWith(t, nil)
	p, repo := a.setup("custom-ack")
	t.Setenv("DRYDOCK_FAKE_CUSTOM", "1")
	sp := filepath.Join(t.TempDir(), "script.json")
	b, _ := json.Marshal(fakecustom.Script{Default: []fakecustom.Step{{Say: "OK"}}})
	os.WriteFile(sp, b, 0o644)
	argsFile := filepath.Join(t.TempDir(), "argv")
	t.Setenv("DRYDOCK_FAKE_CUSTOM_SCRIPT", sp)
	t.Setenv("DRYDOCK_FAKE_CUSTOM_ARGS", argsFile)
	a.call("agentenv.set", map[string]any{"names": []string{"DRYDOCK_FAKE_CUSTOM", "DRYDOCK_FAKE_CUSTOM_SCRIPT", "DRYDOCK_FAKE_CUSTOM_ARGS"}}, nil)

	y := customYAML(t, a, "mine")
	a.call("connection.custom.add", map[string]string{"yaml": y}, nil)
	a.call("connection.acknowledge", map[string]string{"gateId": "custom-mine"}, nil)
	var ag core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Mine", Adapter: "mine", Role: "backend"}, &ag)
	start := func() error {
		m, err := a.c.Runtime.Spawn(context.Background(), rt.Config{AgentID: ag.ID, ProjectID: p.ID, Adapter: "mine", Spec: agents.RunSpec{AgentID: ag.ID, Role: ag.Role, Cwd: repo}})
		if err == nil {
			_ = m.Stop(context.Background())
		}
		return err
	}
	gate := func(a *app) connections.GateStatus {
		var conn connections.Connection
		a.call("connection.check", map[string]string{"adapter": "mine"}, &conn)
		if len(conn.Gates) != 1 {
			t.Fatalf("gates %+v", conn.Gates)
		}
		return conn.Gates[0]
	}
	if err := start(); err != nil {
		t.Fatalf("an acknowledged CLI: %v", err)
	}

	// the file changes on disk (here: another flag); the next session refuses, the page says it changed
	file := filepath.Join(a.dataDir(), "adapters", "custom", "mine.yaml")
	changed := strings.Replace(y, "argv: [run, --json, --cwd, \"{{cwd}}\"]", "argv: [run, --json, --verbose, --cwd, \"{{cwd}}\"]", 1)
	if changed == y {
		t.Fatal("the template's argv line moved")
	}
	os.WriteFile(file, []byte(changed), 0o600)
	err := start()
	if err == nil || !strings.Contains(err.Error(), "not the one you acknowledged") {
		t.Fatalf("a session after the file changed: %v", err)
	}
	if g := gate(a); g.Acked || !g.Changed {
		t.Fatalf("the gate after the change: %+v", g)
	}
	var seen bool
	for _, e := range a.eventsOf("connection.custom_changed") {
		seen = seen || e["adapter"] == "mine"
	}
	if !seen {
		t.Error("no connection.custom_changed event")
	}
	if err := a.callErr("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Another", Adapter: "mine", Role: "backend"}); err == nil {
		t.Error("an agent was made on a CLI owed its acknowledgement")
	}
	// acknowledged again, the new file is what runs
	a.call("connection.acknowledge", map[string]string{"gateId": "custom-mine"}, nil)
	if g := gate(a); !g.Acked || g.Changed {
		t.Fatalf("acknowledged again: %+v", g)
	}
	os.Remove(argsFile)
	if err := a.callErr("connection.custom.list", nil); err != nil {
		t.Fatal(err)
	}
	m, err := a.c.Runtime.Spawn(context.Background(), rt.Config{AgentID: ag.ID, ProjectID: p.ID, Adapter: "mine", Spec: agents.RunSpec{AgentID: ag.ID, Role: ag.Role, Cwd: repo}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Sess.Send(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	a.waitFor("the turn's argv", func() bool { b, _ := os.ReadFile(argsFile); return len(b) > 0 })
	_ = m.Stop(context.Background())
	if got, _ := os.ReadFile(argsFile); !strings.Contains(string(got), "--verbose") {
		t.Fatalf("the changed manifest is not what ran: %q", got)
	}

	// a file that no longer loads takes the acknowledgement back and cannot be acknowledged
	os.WriteFile(file, []byte("meta: ["), 0o600)
	var list core.CustomCLIs
	a.call("connection.custom.list", nil, &list)
	if len(list.Files) != 1 || list.Files[0].Error == "" {
		t.Fatalf("a broken file is listed with why: %+v", list)
	}
	if err := a.callErr("connection.acknowledge", map[string]string{"gateId": "custom-mine"}); err == nil || !strings.Contains(err.Error(), "cannot be acknowledged") {
		t.Fatalf("acknowledging a file that does not load: %v", err)
	}
	if err := start(); err == nil {
		t.Fatal("a session started on a file that does not load")
	}

	// across a restart: acknowledged for one file, changed while the app was closed, asked again at start
	os.WriteFile(file, []byte(y), 0o600)
	a.call("connection.acknowledge", map[string]string{"gateId": "custom-mine"}, nil)
	dir := a.dataDir()
	a.c.Close()
	os.WriteFile(file, []byte(changed), 0o600)
	reopen := func() *app {
		c2, err := core.Open(context.Background(), core.Config{DataDir: dir, User: "kyle", LeaseTTL: 3 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		return &app{t: t, c: c2}
	}
	b2 := reopen()
	if g := gate(b2); g.Acked || !g.Changed {
		t.Fatalf("after a restart with a changed file: %+v", g)
	}
	b2.call("connection.acknowledge", map[string]string{"gateId": "custom-mine"}, nil)
	// an acknowledgement from before the hash was kept (only the gate's own row) is asked again once
	if _, err := b2.c.DB.Writer().Exec(`DELETE FROM settings WHERE key='customcli.ack_sha256.mine'`); err != nil {
		t.Fatal(err)
	}
	b2.c.Close()
	b3 := reopen()
	t.Cleanup(b3.c.Close)
	if g := gate(b3); g.Acked || !g.Changed {
		t.Fatalf("an acknowledgement with no hash: %+v", g)
	}
	b3.call("connection.acknowledge", map[string]string{"gateId": "custom-mine"}, nil)
	if g := gate(b3); !g.Acked {
		t.Fatalf("acknowledged again after the restart: %+v", g)
	}
}

// No session of a gated CLI starts before its acknowledgement, whatever path starts it (Runtime.StartGate, decision P117):
// before, only making an agent and a chat checked it.
func TestNoSessionOfAGatedCLIStartsWithoutItsAcknowledgement(t *testing.T) {
	a := openWith(t, nil)
	p, repo := a.setup("gated")
	_, err := a.c.Runtime.Spawn(context.Background(), rt.Config{AgentID: "nobody", ProjectID: p.ID, Adapter: "antigravity", Spec: agents.RunSpec{Cwd: repo}})
	if err == nil || !strings.Contains(err.Error(), "antigravity-third-party-v1") {
		t.Fatalf("antigravity without its acknowledgement: %v", err)
	}
	failed := false
	for _, e := range a.eventsOf("agent.failed") {
		failed = failed || strings.Contains(e["error"].(string), "acknowledgement")
	}
	if !failed {
		t.Error("the refused start is not on the record")
	}
}

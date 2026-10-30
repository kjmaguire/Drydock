package core_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/core"
)

// Decision P122: every agent session's gateway run answers only the agent's processes. A program that is not the agent (this
// test process, which can read the run's config file and so holds the token) is refused with 403 and mcp.peer_refused; the agent's
// own processes are answered (the end-to-end tests of fake agents, ACP and custom CLIs connect through the shim as children of
// the agent with the check on).
func TestPeerCheckRefusesAProcessOutsideTheAgent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the live lookup runs on Linux; Windows is unverified")
	}
	a := openWith(t, nil)
	p, repo := a.setup("peercheck")
	var ag core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend", Adapter: "claude", Role: "backend", Model: "fake-1"}, &ag)
	m, err := a.c.Runtime.Spawn(context.Background(), rt.Config{AgentID: ag.ID, ProjectID: p.ID, Adapter: "claude", Provider: "anthropic", Tier: "premium",
		Spec: agents.RunSpec{AgentID: ag.ID, Role: ag.Role, Cwd: repo, Model: "fake-1"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()) })
	raw, err := os.ReadFile(filepath.Join(a.dataDir(), "runs", m.RunID, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	url, tok := parseSingleEntry(t, raw)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if s, err := connectMCP(ctx, url, tok); err == nil || !strings.Contains(err.Error(), "Forbidden") {
		if s != nil {
			s.Close()
		}
		t.Fatalf("a process outside the agent was answered: %v", err)
	}
	ev := a.eventsOf("mcp.peer_refused")
	if len(ev) == 0 || strings.Contains(fmt.Sprint(ev), tok) || !strings.Contains(fmt.Sprint(ev), "not the run's agent") {
		t.Fatalf("%v", ev)
	}
}

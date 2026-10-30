package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/events"
)

// The MCP hook: it runs before the process starts, its config path reaches the adapter, and its release runs exactly once when
// the session ends however it ends.

func eventMap(t *testing.T, e events.Event) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(e.Data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func mcpEvents(t *testing.T, h *harness) []map[string]any {
	t.Helper()
	evs, _ := h.bus.Replay(context.Background(), 0, events.Filter{Types: []string{"agent.mcp"}}, 0)
	var out []map[string]any
	for _, e := range evs {
		out = append(out, eventMap(t, e))
	}
	return out
}

// The gateway's peer check is told the pid of the agent's process as soon as it is started (decision P122): the one the session
// reports, before the first turn.
func TestMCPHookIsToldTheAgentProcess(t *testing.T) {
	h := newHarness(t, fakeagent.Script{Turns: [][]fakeagent.Step{{{Say: "ok"}}}})
	var bound []int
	h.rt.MCP = func(ctx context.Context, cfg *rt.Config, runID string) (rt.MCPAttachment, error) {
		return rt.MCPAttachment{Attached: true, Release: func() {}, BindProcess: func(pid int) { bound = append(bound, pid) }}, nil
	}
	m, err := h.rt.Spawn(context.Background(), h.config())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop(context.Background())
	if len(bound) != 1 || bound[0] != m.Sess.PID() || bound[0] <= 0 {
		t.Fatalf("bound %v, the session's process is %d", bound, m.Sess.PID())
	}
}

func TestMCPHookAttachesAndReleasesOnStop(t *testing.T) {
	h := newHarness(t, fakeagent.Script{Turns: [][]fakeagent.Step{{{Say: "ok"}}}})
	var released atomic.Int32
	var gotRunID, gotPath string
	h.rt.MCP = func(ctx context.Context, cfg *rt.Config, runID string) (rt.MCPAttachment, error) {
		gotRunID = runID
		cfg.Spec.MCPConfigPath = filepath.Join(h.dir, "mcp.json")
		cfg.Spec.AllowedTools = append(cfg.Spec.AllowedTools, "mcp__drydock__*")
		gotPath = cfg.Spec.MCPConfigPath
		return rt.MCPAttachment{Attached: true, Transport: "stdio-shim", Tools: []string{"read_range"}, Release: func() { released.Add(1) }}, nil
	}
	m, err := h.rt.Spawn(context.Background(), h.config())
	if err != nil {
		t.Fatal(err)
	}
	if gotRunID != m.RunID || gotRunID == "" {
		t.Fatalf("the hook must see the agent run id: %q vs %q", gotRunID, m.RunID)
	}
	if released.Load() != 0 {
		t.Fatal("released while the session is alive")
	}
	if _, err := m.RunTurn(context.Background(), "go", nil, nil); err != nil {
		t.Fatal(err)
	}
	m.Stop(context.Background())
	m.Stop(context.Background()) // idempotent
	waitFor(t, func() bool { return released.Load() >= 1 })
	if released.Load() != 1 {
		t.Fatalf("release ran %d times", released.Load())
	}
	ev := mcpEvents(t, h)
	if len(ev) != 1 || ev[0]["attached"] != true || ev[0]["transport"] != "stdio-shim" || gotPath == "" {
		t.Fatalf("agent.mcp: %v", ev)
	}
}

func TestMCPHookReleasesWhenTheAgentCrashes(t *testing.T) {
	h := newHarness(t, fakeagent.Script{Turns: [][]fakeagent.Step{{{Say: "about to crash"}, {Exit: 3}}}})
	var released atomic.Int32
	h.rt.MCP = func(ctx context.Context, cfg *rt.Config, runID string) (rt.MCPAttachment, error) {
		return rt.MCPAttachment{Attached: true, Release: func() { released.Add(1) }}, nil
	}
	m, err := h.rt.Spawn(context.Background(), h.config())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.RunTurn(context.Background(), "go", nil, nil); err == nil {
		t.Fatal("expected the crash")
	}
	// no Stop: the process exit alone ends the gateway run
	waitFor(t, func() bool { return released.Load() == 1 })
}

func TestMCPHookReleasesWhenTheTurnIsCancelled(t *testing.T) {
	h := newHarness(t, fakeagent.Script{Turns: [][]fakeagent.Step{{{Say: "thinking"}, {Block: true}}}})
	var released atomic.Int32
	h.rt.MCP = func(ctx context.Context, cfg *rt.Config, runID string) (rt.MCPAttachment, error) {
		return rt.MCPAttachment{Attached: true, Release: func() { released.Add(1) }}, nil
	}
	m, err := h.rt.Spawn(context.Background(), h.config())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	_, err = m.RunTurn(ctx, "go", nil, nil)
	if err == nil {
		t.Fatal("expected cancellation")
	}
	waitFor(t, func() bool { return released.Load() == 1 })
}

func TestMCPHookReleasesWhenTheProcessCannotStart(t *testing.T) {
	h := newHarness(t, fakeagent.Script{})
	h.ad.Exe = filepath.Join(h.dir, "does-not-exist")
	var released atomic.Int32
	h.rt.MCP = func(ctx context.Context, cfg *rt.Config, runID string) (rt.MCPAttachment, error) {
		return rt.MCPAttachment{Attached: true, Release: func() { released.Add(1) }}, nil
	}
	if _, err := h.rt.Spawn(context.Background(), h.config()); err == nil {
		t.Fatal("expected spawn error")
	}
	if released.Load() != 1 {
		t.Fatalf("a run for a process that never started must be ended: %d", released.Load())
	}
}

func TestMCPHookErrorDoesNotStopTheTurnAndIsRecorded(t *testing.T) {
	h := newHarness(t, fakeagent.Script{Turns: [][]fakeagent.Step{{{Say: "still works"}}}})
	h.rt.MCP = func(ctx context.Context, cfg *rt.Config, runID string) (rt.MCPAttachment, error) {
		cfg.Spec.MCPConfigPath = "/half/set"
		return rt.MCPAttachment{}, errors.New("gateway exploded")
	}
	m, err := h.rt.Spawn(context.Background(), h.config())
	if err != nil {
		t.Fatalf("a failed attachment must not fail the turn: %v", err)
	}
	defer m.Stop(context.Background())
	res, err := m.RunTurn(context.Background(), "go", nil, nil)
	if err != nil || res.Text != "still works" {
		t.Fatalf("%+v %v", res, err)
	}
	ev := mcpEvents(t, h)
	if len(ev) != 1 || ev[0]["attached"] != false || !strings.Contains(ev[0]["reason"].(string), "gateway exploded") {
		t.Fatalf("the failure must be recorded: %v", ev)
	}
}

func TestMCPHookNotCalledWhenTheCallerSuppliedItsOwnConfig(t *testing.T) {
	h := newHarness(t, fakeagent.Script{})
	called := false
	h.rt.MCP = func(ctx context.Context, cfg *rt.Config, runID string) (rt.MCPAttachment, error) {
		called = true
		return rt.MCPAttachment{}, nil
	}
	cfg := h.config()
	cfg.Spec.MCPConfigPath = filepath.Join(h.dir, "own.json")
	m, err := h.rt.Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop(context.Background())
	if called {
		t.Fatal("the hook must not override a config the caller chose")
	}
}

package core_test

import (
	"context"
	"testing"

	"github.com/kjmaguire/drydock/internal/events"
)

// events.replay pages backwards and narrows in the core (X53): `before` returns the newest events older than it, oldest first, and
// a project (with the workspace's own events when asked), run or agent selects there and not in the page.
func TestEventsReplayPagesBackAndFiltersByProjectRunAndAgent(t *testing.T) {
	a := open(t, "")
	ctx := context.Background()
	head0 := uint64(0)
	a.call("events.head", struct{}{}, &head0)
	for _, e := range []events.Event{
		{Type: "test.one", ProjectID: "px1", RunID: "rx1", AgentID: "ax1"},
		{Type: "test.two", ProjectID: "px2", RunID: "rx2", AgentID: "ax2"},
		{Type: "test.three"},
		{Type: "test.four", ProjectID: "px1", RunID: "rx1", AgentID: "ax2"},
	} {
		if _, err := a.c.Bus.Publish(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	type ev struct {
		Seq  uint64 `json:"seq"`
		Type string `json:"type"`
	}
	types := func(p map[string]any) []string {
		var evs []ev
		a.call("events.replay", p, &evs)
		var out []string
		for _, e := range evs {
			if e.Seq > head0 && len(e.Type) > 5 && e.Type[:5] == "test." {
				out = append(out, e.Type)
			}
		}
		return out
	}
	eq := func(name string, got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: got %v want %v", name, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: got %v want %v", name, got, want)
			}
		}
	}
	eq("the last two before the head", types(map[string]any{"before": head0 + 5, "limit": 2}), "test.three", "test.four")
	eq("one before the last", types(map[string]any{"before": head0 + 4, "limit": 1}), "test.three")
	eq("project", types(map[string]any{"before": head0 + 100, "projectId": "px1"}), "test.one", "test.four")
	eq("project and workspace", types(map[string]any{"before": head0 + 100, "projectId": "px1", "withWorkspace": true}), "test.one", "test.three", "test.four")
	eq("run", types(map[string]any{"after": head0, "runId": "rx2"}), "test.two")
	eq("agent", types(map[string]any{"before": head0 + 100, "agentId": "ax2"}), "test.two", "test.four")
}

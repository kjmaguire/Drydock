package runtime_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/proc"
)

// specRecorder wraps the real Claude adapter and remembers the RunSpec each session was started with.
type specRecorder struct {
	agents.Adapter
	specs []agents.RunSpec
}

func (r *specRecorder) Start(ctx context.Context, pm proc.Manager, spec agents.RunSpec) (agents.Session, error) {
	r.specs = append(r.specs, spec)
	return r.Adapter.Start(ctx, pm, spec)
}

// The agent's standing instructions reach the session's system prompt through Spawn, whoever asked for the session, and are
// added to (not replacing) what the caller already set.
func TestInstructionsHookAddsPurposeToTheSystemPrompt(t *testing.T) {
	h := newHarness(t, fakeagent.Script{Turns: [][]fakeagent.Step{{{Say: "ok"}}, {{Say: "ok"}}}})
	rec := &specRecorder{Adapter: h.ad}
	h.rt.Adapters["claude"] = rec
	var asked []string
	h.rt.Instructions = func(_ context.Context, agentID string) string {
		asked = append(asked, agentID)
		if agentID == "a1" {
			return "  Own the payments service.  "
		}
		return ""
	}
	cfg := h.config()
	cfg.Spec.SystemAppend = "caller text"
	m, err := h.rt.Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.Stop(context.Background())
	got := rec.specs[0].SystemAppend
	if !strings.HasPrefix(got, "caller text") || !strings.HasSuffix(got, "Own the payments service.") {
		t.Fatalf("system prompt = %q", got)
	}
	if len(asked) != 1 || asked[0] != "a1" {
		t.Fatalf("asked %v", asked)
	}

	// an agent without a purpose changes nothing
	cfg2 := h.config()
	cfg2.AgentID = "someone-else"
	m2, err := h.rt.Spawn(context.Background(), cfg2)
	if err != nil {
		t.Fatal(err)
	}
	m2.Stop(context.Background())
	if rec.specs[1].SystemAppend != "" {
		t.Fatalf("system prompt = %q, want none", rec.specs[1].SystemAppend)
	}
}

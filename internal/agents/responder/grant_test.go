package responder_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/responder"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Every reply turn starts on a grant the room's scheduler issued for it (decision P118), and a fallback hop of the same turn
// (the model router moving it to another model before it did any work) starts on a renewed one: each session start uses a
// grant of its own, once.
func TestEveryReplyTurnStartsOnItsOwnGrant(t *testing.T) {
	h := newHarness(t, responder.Options{})
	a := h.agent("agt_a", "Ada", "architect", say("A plan."))
	b := h.agent("agt_b", "Bo", "backend", say("A reply."))
	r := h.channel("design", a, b)
	// the hook runs Bo's turn a second time on another model, as a fallback hop of the model router does
	h.route = func(ctx context.Context, rt responder.ReplyTurn, attempt responder.Attempt) error {
		if rt.Agent.ID == b {
			_ = attempt(ctx, responder.Routed{Model: "fake-1"}, func(workflow.Chunk) {})
			return attempt(ctx, responder.Routed{Model: "fake-2"}, nil)
		}
		return attempt(ctx, responder.Routed{}, nil)
	}
	first := h.human(r, "@agt_a how should we store sessions?")
	if out, err := h.r.Handle(h.ctx, first.ID); err != nil || len(out.Turns) != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	m := h.human(r, "@agt_b and you?")
	out, err := h.r.Handle(h.ctx, m.ID)
	if err != nil || len(out.Turns) != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	rows, err := h.db.Reader().Query(`SELECT agent_id, used_at, agent_run_id, renews FROM room_turn_grants ORDER BY issued_at, rowid`)
	if err != nil {
		t.Fatal(err)
	}
	type grant struct {
		agent, run, renews string
		used               int64
	}
	var gs []grant
	for rows.Next() {
		var g grant
		rows.Scan(&g.agent, &g.used, &g.run, &g.renews)
		gs = append(gs, g)
	}
	rows.Close()
	if len(gs) != 3 {
		t.Fatalf("one grant per session start (Ada's turn, Bo's two attempts): %+v", gs)
	}
	runs := map[string]bool{}
	for _, g := range gs {
		if g.used == 0 || g.run == "" || runs[g.run] {
			t.Fatalf("every grant started exactly one run: %+v", gs)
		}
		runs[g.run] = true
	}
	if gs[2].agent != b || gs[2].renews == "" {
		t.Fatalf("Bo's second attempt runs on a renewal of the first grant: %+v", gs)
	}
	// the turn row names the run of its last attempt, which a grant started
	var last string
	h.db.Reader().QueryRow(`SELECT agent_run_id FROM room_turns WHERE agent_id=?`, b).Scan(&last)
	if !runs[last] || last != gs[2].run {
		t.Fatalf("turn run %q, grants %+v", last, gs)
	}
}

// A turn in a room that a hook tries to start on another agent's grant is refused by the runtime, and the refusal is posted.
func TestARoomTurnWithoutItsOwnGrantDoesNotStart(t *testing.T) {
	h := newHarness(t, responder.Options{})
	a := h.agent("agt_a", "Ada", "architect", say("A plan."))
	r := h.channel("design", a)
	h.rt.Grants = nil // the scheduler is not connected: fail closed
	m := h.human(r, "@agt_a hello")
	out, err := h.r.Handle(h.ctx, m.ID)
	if err != nil || len(out.Turns) != 1 || out.Turns[0].Status != responder.StatusFailed {
		t.Fatalf("%+v %v", out, err)
	}
	var n int
	h.db.Reader().QueryRow(`SELECT COUNT(*) FROM agent_runs`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d agent runs started without a grant", n)
	}
	last := h.messages(r)
	if body := last[len(last)-1].Body; !strings.Contains(body, "could not answer") || !strings.Contains(body, "turn grant") {
		t.Fatalf("the refusal is posted: %q", body)
	}
}

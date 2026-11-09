package responder_test

import (
	"errors"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/agents/responder"
	"github.com/kjmaguire/drydock/internal/room"
)

// A person's message nobody was addressed in gets no turn, and the outcome says why (X12); the person then asks an agent by
// name and it answers on a grant of its own (reason "asked"), under the message; an empty answer can be asked again, which is
// a new turn of the same agent on the same message; only a person's message can be answered this way, by an agent of the room.
func TestAPersonAsksAnAgentToAnswerAndAsksAgain(t *testing.T) {
	h := newHarness(t, responder.Options{})
	a := h.agent("agt_a", "Ada", "architect", say("Use SQLite."))
	b := h.agent("agt_b", "Bo", "backend", fakeagent.Script{Default: []fakeagent.Step{{Say: ""}}})
	out := h.agent("agt_c", "Cy", "qa", say("hi"))
	r := h.channel("design", a, b)
	q := h.human(r, "how should we store sessions?")
	o, err := h.r.Handle(h.ctx, q.ID)
	if err != nil || len(o.Turns) != 0 || o.Why != "nobody addressed" || o.Room != r.ID {
		t.Fatalf("nobody addressed: %+v %v", o, err)
	}
	if last, ok := h.r.LastOutcome(r.ID); !ok || last.Why != "nobody addressed" || last.Trigger != q.ID {
		t.Fatalf("the room's last outcome: %+v %v", last, ok)
	}
	if n := countEvents(h, responder.ReplyOutcomeEvent); n != 1 {
		t.Fatalf("%d outcome events", n)
	}
	o, err = h.r.Ask(h.ctx, q.ID, a)
	if err != nil || len(o.Turns) != 1 || o.Turns[0].AgentID != a || o.Turns[0].Reason != string(room.ReasonAsked) || o.Turns[0].Status != responder.StatusDone || o.Asked != a {
		t.Fatalf("asked Ada: %+v %v", o, err)
	}
	reply, err := h.room.GetMessage(h.ctx, o.Turns[0].MessageID)
	if err != nil || reply.ReplyToID != q.ID || reply.Body != "Use SQLite." {
		t.Fatalf("the answer sits under the question: %+v %v", reply, err)
	}
	var reason string
	h.db.Reader().QueryRow(`SELECT reason FROM room_turn_grants WHERE agent_id=?`, a).Scan(&reason)
	if reason != string(room.ReasonAsked) {
		t.Fatalf("the grant's reason: %q", reason)
	}
	// Bo answers with nothing; Ask again is a second turn of Bo on the same message
	for i := 0; i < 2; i++ {
		o, err = h.r.Ask(h.ctx, q.ID, b)
		if err != nil || len(o.Turns) != 1 || o.Turns[0].Status != responder.StatusEmpty {
			t.Fatalf("ask %d: %+v %v", i, o, err)
		}
	}
	var n int
	h.db.Reader().QueryRow(`SELECT COUNT(*) FROM room_turns WHERE trigger_id=? AND agent_id=?`, q.ID, b).Scan(&n)
	if n != 2 {
		t.Fatalf("asking again is a new turn: %d rows", n)
	}
	// an agent's message is not answered on request, and an agent outside the room cannot be asked
	if _, err := h.r.Ask(h.ctx, reply.ID, a); !errors.Is(err, room.ErrForbidden) {
		t.Fatalf("an agent's message: %v", err)
	}
	if _, err := h.r.Ask(h.ctx, q.ID, out); !errors.Is(err, room.ErrGrantRefused) {
		t.Fatalf("an agent outside the room: %v", err)
	}
	if _, err := h.r.Ask(h.ctx, q.ID, ""); err == nil {
		t.Fatal("asked nobody")
	}
}

// In a chain a later turn's trigger is another agent's reply, which Ask refuses; the turns list says which turns can be asked
// again (askable: the trigger is the person's message), so the room offers Ask again only on those (X12 review).
func TestOnlyATurnOnThePersonsMessageIsAskable(t *testing.T) {
	h := newHarness(t, responder.Options{})
	a := h.agent("agt_a", "Ada", "architect", say("@Bo can you check?"))
	b := h.agent("agt_b", "Bo", "backend", fakeagent.Script{Default: []fakeagent.Step{{Say: ""}}})
	r := h.channel("design", a, b)
	q := h.human(r, "@Ada how should we store sessions?")
	if _, err := h.r.Handle(h.ctx, q.ID); err != nil {
		t.Fatal(err)
	}
	ts, err := h.r.Turns(h.ctx, r.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tr := range ts {
		seen[tr.AgentID] = true
		switch tr.AgentID {
		case a:
			if tr.TriggerID != q.ID || !tr.Askable {
				t.Errorf("Ada's turn on the person's message is askable: %+v", tr)
			}
		case b:
			if tr.TriggerID == q.ID || tr.Askable {
				t.Errorf("Bo's turn on Ada's reply is not askable: %+v", tr)
			}
			if _, err := h.r.Ask(h.ctx, tr.TriggerID, b); !errors.Is(err, room.ErrForbidden) {
				t.Errorf("asking Bo about Ada's reply: %v", err)
			}
		}
	}
	if !seen[a] || !seen[b] {
		t.Fatalf("the chain ran Ada then Bo: %+v", ts)
	}
}

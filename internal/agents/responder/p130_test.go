package responder_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/agents/responder"
	"github.com/kjmaguire/drydock/internal/room"
)

// Every granted turn is completed through the scheduler's own call (decision P130): its grant records the outcome and the
// reply, and the chain goes on from the reply the scheduler names (Ada's reply mentions Bo, so Bo answers it); an empty turn
// completes as empty and wakes nobody.
func TestEveryTurnIsCompletedByTheScheduler(t *testing.T) {
	h := newHarness(t, responder.Options{})
	a := h.agent("agt_a", "Ada", "architect", say("Use SQLite. @agt_b what do you think?"))
	b := h.agent("agt_b", "Bo", "backend", say("Agreed."))
	c := h.agent("agt_c", "Cy", "qa", fakeagent.Script{Default: []fakeagent.Step{{Say: ""}}})
	r := h.channel("design", a, b, c)
	out, err := h.r.Handle(h.ctx, h.human(r, "@agt_a how should we store sessions?").ID)
	if err != nil || len(out.Turns) != 2 || out.Turns[1].AgentID != b {
		t.Fatalf("%+v %v", out, err)
	}
	rows, err := h.db.Reader().Query(`SELECT agent_id, outcome, reply_id, completed_at FROM room_turn_grants ORDER BY issued_at, rowid`)
	if err != nil {
		t.Fatal(err)
	}
	type g struct {
		agent, outcome, reply string
		at                    int64
	}
	var gs []g
	for rows.Next() {
		var x g
		rows.Scan(&x.agent, &x.outcome, &x.reply, &x.at)
		gs = append(gs, x)
	}
	rows.Close()
	if len(gs) != 2 || gs[0].agent != a || gs[1].agent != b {
		t.Fatalf("%+v", gs)
	}
	for i, x := range gs {
		if x.outcome != room.OutcomeDone || x.at == 0 || x.reply != out.Turns[i].MessageID {
			t.Fatalf("grant %d: %+v (turns %+v)", i, x, out.Turns)
		}
	}
	if n := countEvents(h, "room.turn_completed"); n != 2 {
		t.Fatalf("%d completions announced", n)
	}
	out, err = h.r.Handle(h.ctx, h.human(r, "@agt_c anything?").ID)
	if err != nil || len(out.Turns) != 1 || out.Turns[0].Status != responder.StatusEmpty {
		t.Fatalf("%+v %v", out, err)
	}
	var outcome string
	h.db.Reader().QueryRow(`SELECT outcome FROM room_turn_grants WHERE agent_id=?`, c).Scan(&outcome)
	if outcome != room.OutcomeEmpty {
		t.Fatalf("the empty turn: %q", outcome)
	}
}

// A person stops the agent that is answering (Esc Esc in the room's composer, room.stop): the turn ends as stopped, nothing
// it said is posted, one SYSTEM line says so under the question, and the chain takes no further turn.
func TestStopEndsTheAnsweringTurnAndItsChain(t *testing.T) {
	h := newHarness(t, responder.Options{})
	a := h.agent("agt_a", "Ada", "architect", fakeagent.Script{Default: []fakeagent.Step{{Say: "Thinking"}, {SleepMS: 30000}, {Say: "@agt_b over to you"}}})
	b := h.agent("agt_b", "Bo", "backend", say("Agreed."))
	r := h.channel("design", a, b)
	if n := h.r.Stop(r.ID); n != 0 {
		t.Fatalf("stopped %d with nobody answering", n)
	}
	q := h.human(r, "@agt_a a long question")
	done := make(chan responder.Outcome, 1)
	go func() {
		out, err := h.r.Handle(h.ctx, q.ID)
		if err != nil {
			t.Error(err)
		}
		done <- out
	}()
	deadline := time.Now().Add(20 * time.Second)
	for len(h.spy.all()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the agent never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := h.r.Stop(r.ID); n != 1 {
		t.Fatalf("stopped %d turns", n)
	}
	var out responder.Outcome
	select {
	case out = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the turn did not stop")
	}
	if len(out.Turns) != 1 || out.Turns[0].Status != responder.StatusStopped {
		t.Fatalf("%+v", out)
	}
	var lines []string
	for _, m := range h.messages(r) {
		if m.Author.Kind == room.AuthorAgent {
			t.Fatalf("a stopped turn posted: %q", m.Body)
		}
		if m.Type == room.TypeError {
			t.Fatalf("a stop is not a failure: %q", m.Body)
		}
		if m.Type == room.TypeSystem && m.ReplyToID == q.ID {
			lines = append(lines, m.Body)
		}
	}
	if len(lines) != 1 || lines[0] != "You stopped Ada's answer." {
		t.Fatalf("%q", lines)
	}
	var outcome, status string
	h.db.Reader().QueryRow(`SELECT outcome FROM room_turn_grants WHERE agent_id=?`, a).Scan(&outcome)
	h.db.Reader().QueryRow(`SELECT status FROM room_turns WHERE agent_id=?`, a).Scan(&status)
	if outcome != room.OutcomeStopped || status != responder.StatusStopped {
		t.Fatalf("grant %q, turn %q", outcome, status)
	}
	if strings.Contains(authors(h.messages(r)), b) {
		t.Fatal("the chain went on to Bo")
	}
}

func countEvents(h *harness, typ string) int {
	h.t.Helper()
	var n int
	h.db.Reader().QueryRow(`SELECT COUNT(*) FROM events WHERE type=?`, typ).Scan(&n)
	return n
}

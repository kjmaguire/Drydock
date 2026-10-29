package room

import (
	"errors"
	"testing"
)

// The scheduler grants a group discussion's turns in its fixed order up to its bound (P118): a [DECISION] line promoted to a
// DECISION message is part of its turn, not a turn of its own; a person's message in the thread starts the count again; and
// a muted member is never granted a turn.
func TestGroupTurnsAreGrantedInOrderUpToTheBound(t *testing.T) {
	e := newEnv(t)
	r := e.channel("design")
	a, b := e.agent("Ada"), e.agent("Bo")
	e.join(r, Agent(a), "architect")
	e.join(r, Agent(b), "backend")
	topic := e.post(PostParams{RoomID: r.ID, Type: TypeSystem, Author: System, Body: "Discussion: the parser"}).Message
	gt := GroupTurn{RoomID: r.ID, Thread: topic.ID, Order: []string{a, b}, Rounds: 2}
	next := func(wantAgent string, wantRound int) TurnGrant {
		t.Helper()
		dec, g, err := e.svc.GrantGroupTurn(e.ctx, gt)
		if err != nil || g == nil || g.AgentID != wantAgent || g.Round != wantRound || dec.Round != wantRound || g.ThreadID != topic.ID {
			t.Fatalf("want %s in round %d: %+v %+v %v", wantAgent, wantRound, dec, g, err)
		}
		return *g
	}
	reply := func(agent, body string) {
		t.Helper()
		e.post(PostParams{RoomID: r.ID, Author: Agent(agent), Body: body, ReplyToID: topic.ID})
	}
	next(a, 1)
	reply(a, "Hand-written.")
	next(b, 1)
	reply(b, "Agreed.\n[DECISION] Keep the hand-written parser") // also posts the promoted DECISION
	next(a, 2)
	reply(a, "PASS")
	next(b, 2)
	reply(b, "PASS")
	dec, g, err := e.svc.GrantGroupTurn(e.ctx, gt)
	if err != nil || g != nil || !dec.Bounded || dec.Round != 2 {
		t.Fatalf("the bound: %+v %+v %v", dec, g, err)
	}
	// a person speaks in the thread: the discussion may go on, from the start of a round
	e.post(PostParams{RoomID: r.ID, Author: kyle, Body: "What about error messages?", ReplyToID: topic.ID})
	next(a, 1)
	// a muted member gets no grant even when the order names it
	if err := e.svc.SetMemberState(e.ctx, r.ID, Agent(b), StateMuted, kyle); err != nil {
		t.Fatal(err)
	}
	reply(a, "Line and column in every error.")
	if _, g, err := e.svc.GrantGroupTurn(e.ctx, gt); err != nil || g != nil {
		t.Fatalf("a muted member was granted a turn: %+v %v", g, err)
	}
	// nor can a grant be issued to it any other way
	if _, err := e.svc.issue(e.ctx, r, topic, b, 1, "sequence", ""); !errors.Is(err, ErrGrantRefused) {
		t.Fatalf("issue to a muted member: %v", err)
	}
	if n := len(e.events("room.turn_granted")); n != 5 {
		t.Fatalf("%d grants announced", n)
	}
}

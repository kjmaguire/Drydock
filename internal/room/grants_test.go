package room

import (
	"errors"
	"fmt"
	"strings"
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
	if _, err := e.svc.issue(e.ctx, r, topic, b, 1, 2, "sequence", "", ContextBounds{}, nil); !errors.Is(err, ErrGrantRefused) {
		t.Fatalf("issue to a muted member: %v", err)
	}
	if n := len(e.events("room.turn_granted")); n != 5 {
		t.Fatalf("%d grants announced", n)
	}
}

// A grant carries the debate bound and the context plan the scheduler decided (room-protocol s6 rule 4, decision P127): the
// turn's context is selected by the grant's plan, not recomputed from the room, and a grant whose bound or plan was changed
// after it was issued is refused by the selection and by the redemption alike.
func TestAGrantsPlanDecidesTheTurnsContextAndAChangedPlanIsRefused(t *testing.T) {
	e := newEnv(t)
	r := e.channel("design")
	a, b := e.agent("Ada"), e.agent("Bo")
	e.join(r, Agent(a), "architect")
	e.join(r, Agent(b), "backend")
	topic := e.post(PostParams{RoomID: r.ID, Type: TypeSystem, Author: System, Body: "Discussion: the parser"}).Message
	for i := 1; i <= 6; i++ {
		e.post(PostParams{RoomID: r.ID, Author: kyle, Body: fmt.Sprintf("note %d", i), ReplyToID: topic.ID})
	}
	_, g, err := e.svc.GrantGroupTurn(e.ctx, GroupTurn{RoomID: r.ID, Thread: topic.ID, Order: []string{a, b}, Rounds: 2, Context: ContextBounds{ThreadWindow: 2, MaxTokens: 3000}})
	if err != nil || g == nil {
		t.Fatalf("%+v %v", g, err)
	}
	p := g.ContextPlan
	if g.MaxRounds != 2 || p.RoomID != r.ID || p.AgentKey != Agent(a).Key() || p.ThreadWindow != 2 || p.MaxTokens != 3000 || p.MaxMentions != 10 ||
		len(p.Roles) != 1 || p.Roles[0] != "architect" || p.TriggerID != g.TriggerID {
		t.Fatalf("the grant's bound and plan: %d %+v", g.MaxRounds, p)
	}
	// the plan decides: the root and the last two of the thread, the older notes digested
	sel, err := e.svc.ContextForGrant(e.ctx, *g, nil)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, m := range sel.Messages {
		kept = append(kept, m.Body)
	}
	if strings.Join(kept, "|") != "Discussion: the parser|note 5|note 6" || len(sel.DigestFrom) != 4 {
		t.Fatalf("selected %q, digested %v", kept, sel.DigestFrom)
	}
	// the room's own selection (no grant) would have kept the whole thread: the grant's window is what counted
	if all, _ := e.svc.ContextFor(e.ctx, r.ID, Agent(a), g.TriggerID, ContextOptions{}); len(all.Messages) != 7 {
		t.Fatalf("without the grant's plan: %d messages", len(all.Messages))
	}
	// a changed plan or bound is refused, before anything is selected and at the redemption
	for name, change := range map[string]func(*TurnGrant){
		"a wider thread window": func(x *TurnGrant) { x.ContextPlan.ThreadWindow = 50 },
		"another task":          func(x *TurnGrant) { x.ContextPlan.TaskID = "task_other" },
		"more roles":            func(x *TurnGrant) { x.ContextPlan.Roles = append([]string{"lead"}, x.ContextPlan.Roles...) },
		"a higher bound":        func(x *TurnGrant) { x.MaxRounds = 9 },
		"no plan":               func(x *TurnGrant) { x.ContextPlan = ContextPlan{} },
	} {
		x := *g
		x.ContextPlan.Roles = append([]string(nil), g.ContextPlan.Roles...)
		change(&x)
		if _, err := e.svc.ContextForGrant(e.ctx, x, nil); !errors.Is(err, ErrGrantRefused) {
			t.Fatalf("%s: selected: %v", name, err)
		}
		if err := e.svc.RedeemGrant(e.ctx, x, r.ID, a, "run_x"); !errors.Is(err, ErrGrantRefused) {
			t.Fatalf("%s: redeemed: %v", name, err)
		}
	}
	// the grant as issued is redeemed once, and then selects nothing more
	if err := e.svc.RedeemGrant(e.ctx, *g, r.ID, a, "run_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ContextForGrant(e.ctx, *g, nil); !errors.Is(err, ErrGrantRefused) {
		t.Fatalf("a used grant selected again: %v", err)
	}
	// a fallback hop's renewed grant carries the same bound and plan
	renewed, err := e.svc.RenewGrant(e.ctx, *g)
	if err != nil || renewed.MaxRounds != 2 || encodePlan(renewed.ContextPlan) != encodePlan(g.ContextPlan) {
		t.Fatalf("renewed %+v %v", renewed, err)
	}
	// a grant past its bound is refused even as issued
	over, err := e.svc.issue(e.ctx, r, topic, b, 3, 2, "sequence", "", ContextBounds{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RedeemGrant(e.ctx, over, r.ID, b, "run_2"); !errors.Is(err, ErrGrantRefused) || !strings.Contains(err.Error(), "bounded at 2") {
		t.Fatalf("round 3 of 2: %v", err)
	}
}

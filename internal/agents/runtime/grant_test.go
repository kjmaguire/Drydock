package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/room"
)

// The Agent Runtime starts a turn in a room only on a grant the room's scheduler issued (room-protocol s6 rule 1, token-control
// rule 2, decision P118): with the real room service, the real database and the labelled fake agent (a test double of Claude
// Code, a real process). Every other session (task work here) starts as before, without one.
func TestARoomTurnStartsOnlyOnAValidGrantAndOtherRunsNeedNone(t *testing.T) {
	h := newHarness(t, fakeagent.Script{Default: []fakeagent.Step{{Say: "hello"}}})
	ctx := context.Background()
	rs := room.New(h.db, h.bus, room.Options{})
	h.rt.Grants = rs
	if _, err := h.db.Writer().Exec(`INSERT INTO agents(id,project_id,name,adapter,role,created_at) VALUES('a2','p1','Reviewer #1','claude','reviewer',1)`); err != nil {
		t.Fatal(err)
	}
	ch, err := rs.CreateChannel(ctx, "p1", "design", "", room.Human("kyle"))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"a1", "a2"} {
		if err := rs.AddMember(ctx, ch.ID, room.Agent(a), "", room.StateActive, room.Human("kyle")); err != nil {
			t.Fatal(err)
		}
	}
	msg, err := rs.Post(ctx, room.PostParams{RoomID: ch.ID, Author: room.Human("kyle"), Body: "@a1 what do you think?"})
	if err != nil {
		t.Fatal(err)
	}
	dec, grants, err := rs.GrantTurns(ctx, msg.Message.ID)
	if err != nil || len(dec.Speakers) != 1 || grants["a1"].ID == "" || grants["a2"].ID != "" {
		t.Fatalf("the scheduler grants the agent it chose, and only it: %+v %+v %v", dec, grants, err)
	}
	g := grants["a1"]
	inRoom := func(g *room.TurnGrant) rt.Config {
		c := h.config()
		c.RoomID, c.Grant = ch.ID, g
		return c
	}
	refused := func(name string, cfg rt.Config, want error) {
		t.Helper()
		m, err := h.rt.Spawn(ctx, cfg)
		if m != nil {
			m.Stop(ctx)
		}
		if !errors.Is(err, want) {
			t.Fatalf("%s: started or wrong refusal: %v", name, err)
		}
	}
	refused("no grant", inRoom(nil), room.ErrNoGrant)
	forged := g
	forged.ID = "grant_forged"
	refused("a grant the scheduler never issued", inRoom(&forged), room.ErrGrantRefused)
	moved := g
	moved.Round = 3
	refused("a grant changed after it was issued", inRoom(&moved), room.ErrGrantRefused)
	other := h.config()
	other.AgentID, other.RoomID, other.Grant = "a2", ch.ID, &g
	refused("another agent's grant", other, room.ErrGrantRefused)
	// a grant carried without the room is still a room turn
	noRoom := h.config()
	noRoom.Grant = &forged
	refused("a forged grant on a session that names no room", noRoom, room.ErrGrantRefused)

	// the valid grant starts the turn, once
	m, err := h.rt.Spawn(ctx, inRoom(&g))
	if err != nil {
		t.Fatalf("the granted turn did not start: %v", err)
	}
	res, err := m.RunTurn(ctx, "go", nil, nil)
	m.Stop(ctx)
	if err != nil || res.Text != "hello" {
		t.Fatalf("turn %+v %v", res, err)
	}
	var used int64
	var run string
	h.db.Reader().QueryRow(`SELECT used_at, agent_run_id FROM room_turn_grants WHERE id=?`, g.ID).Scan(&used, &run)
	if used == 0 || run != m.RunID {
		t.Fatalf("the grant records the run it started: %d %q (run %s)", used, run, m.RunID)
	}
	refused("the same grant again", inRoom(&g), room.ErrGrantRefused)

	// a fallback hop of the same turn gets a renewed grant; an unused grant cannot be renewed
	renewed, err := rs.RenewGrant(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rs.RenewGrant(ctx, renewed); !errors.Is(err, room.ErrGrantRefused) {
		t.Fatalf("an unused grant was renewed: %v", err)
	}
	// an expired grant is refused
	if _, err := h.db.Writer().Exec(`UPDATE room_turn_grants SET expires_at=1 WHERE id=?`, renewed.ID); err != nil {
		t.Fatal(err)
	}
	refused("an expired grant", inRoom(&renewed), room.ErrGrantRefused)

	// no scheduler connected: no room turn starts at all (fail closed)
	_, grants, _ = rs.GrantTurns(ctx, msg.Message.ID)
	h.rt.Grants = nil
	fresh := grants["a1"]
	refused("no scheduler", inRoom(&fresh), room.ErrGrantRefused)

	// task work is not a turn in a room: it starts without a grant
	m, err = h.rt.Spawn(ctx, h.config())
	if err != nil {
		t.Fatalf("task work needs no grant: %v", err)
	}
	m.Stop(ctx)
	h.settle(t)

	// every refusal was announced as a start that failed, and none started a process
	evs, _ := h.bus.Replay(ctx, 0, events.Filter{Types: []string{"agent.failed"}}, 100)
	if len(evs) != 8 {
		t.Fatalf("%d agent.failed events, want one per refusal", len(evs))
	}
	var started int
	h.db.Reader().QueryRow(`SELECT COUNT(*) FROM agent_runs`).Scan(&started)
	if started != 2 {
		t.Fatalf("%d agent runs, want the granted turn and the task work", started)
	}
	granted, _ := h.bus.Replay(ctx, 0, events.Filter{Types: []string{"room.turn_granted"}}, 100)
	if len(granted) != 3 {
		t.Fatalf("%d room.turn_granted events", len(granted))
	}
}

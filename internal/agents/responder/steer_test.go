package responder_test

import (
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/responder"
	"github.com/kjmaguire/drydock/internal/room"
)

// A person's steer (task.steer) is posted in the task's channel for the history; it went to the running agent itself, so it
// starts no room conversation, even when it names the agent.
func TestASteerInTheChannelWakesNoAgent(t *testing.T) {
	h := newHarness(t, responder.Options{})
	alice := h.agent("alice", "Alice", "backend", say("alice should not answer a steer"))
	ch := h.channel("task-1", alice)
	p, err := h.room.Post(h.ctx, room.PostParams{RoomID: ch.ID, Author: kyle, Body: "@alice use the existing helper", Payload: map[string]any{"via": room.ViaSteer}})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Message.IsSteer() {
		t.Fatalf("not a steer: %+v", p.Message)
	}
	out, err := h.r.Handle(h.ctx, p.Message.ID)
	if err != nil || len(out.Turns) != 0 || out.Stopped == "" {
		t.Fatalf("a steer started a conversation: %+v %v", out, err)
	}
	if n := len(h.prompts(alice)); n != 0 {
		t.Fatalf("alice was asked %d time(s)", n)
	}
	// the same words as an ordinary message do wake her
	q := h.human(ch, "@alice use the existing helper")
	if q.IsSteer() {
		t.Fatal("an ordinary message is not a steer")
	}
	if out, err := h.r.Handle(h.ctx, q.ID); err != nil || len(out.Turns) != 1 {
		t.Fatalf("an ordinary message: %+v %v", out, err)
	}
}

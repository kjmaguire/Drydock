package room

import (
	"testing"
)

// An agent's answer to the person reaches the Inbox (X18): a reply to the person's own message after the read cursor is one
// item per room (the newest answer), saying where; it goes when the room is read; a reply to someone else, a message that is
// already a mention item and a muted room make none.
func TestAnAgentsAnswerToThePersonIsAnInboxItemUntilRead(t *testing.T) {
	e := newEnv(t)
	r := e.channel("general")
	a := e.agent("Ada")
	b := e.agent("Bo")
	e.join(r, kyle, "owner")
	e.join(r, Agent(a), "dev")
	e.join(r, Agent(b), "dev")
	replyKeys := func() map[string]NeedsYouItem {
		t.Helper()
		items, err := e.svc.NeedsYou(e.ctx, e.project)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]NeedsYouItem{}
		for _, it := range items {
			if it.Kind == NeedsReply {
				out[it.Key] = it
			}
		}
		return out
	}
	q := e.say(r, kyle, "@"+a+" how do we store sessions?")
	if k := replyKeys(); len(k) != 0 {
		t.Fatalf("no answer yet: %v", k)
	}
	// Bo answers Ada (not the person): no item
	other := e.say(r, Agent(a), "thinking out loud")
	e.post(PostParams{RoomID: r.ID, Author: Agent(b), Body: "agreed", ReplyToID: other.ID})
	if k := replyKeys(); len(k) != 0 {
		t.Fatalf("a reply to an agent is no item: %v", k)
	}
	first := e.post(PostParams{RoomID: r.ID, Author: Agent(a), Body: "Use SQLite.", ReplyToID: q.ID}).Message
	second := e.post(PostParams{RoomID: r.ID, Author: Agent(b), Body: "And keep a WAL.", ReplyToID: q.ID}).Message
	k := replyKeys()
	it, ok := k["reply:"+second.ID]
	if len(k) != 1 || !ok || it.RoomID != r.ID || it.AgentID != b || it.AgentName != "Bo" || it.Title != "And keep a WAL." || it.Detail != "Answered you in #general." {
		t.Fatalf("one item per room, the newest answer: %+v", k)
	}
	// reading the room clears it
	if _, err := e.svc.MarkRead(e.ctx, r.ID, kyle, second.Seq); err != nil {
		t.Fatal(err)
	}
	if k := replyKeys(); len(k) != 0 {
		t.Fatalf("read answers stay: %v", k)
	}
	_ = first
	// an answer that mentions the person is the mention item, not a second one
	q2 := e.say(r, kyle, "and the cache?")
	m := e.post(PostParams{RoomID: r.ID, Author: Agent(a), Body: "@kyle use Redis", ReplyToID: q2.ID}).Message
	items, err := e.svc.NeedsYou(e.ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, it := range items {
		if it.MessageID == m.ID {
			n++
			if it.Kind != NeedsMention {
				t.Fatalf("the mention wins: %+v", it)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d items for one message", n)
	}
	// a muted room makes no reply item
	if err := e.svc.SetMemberState(e.ctx, r.ID, kyle, StateMuted, kyle); err != nil {
		t.Fatal(err)
	}
	q3 := e.say(r, kyle, "one more")
	e.post(PostParams{RoomID: r.ID, Author: Agent(a), Body: "sure", ReplyToID: q3.ID})
	if k := replyKeys(); len(k) != 0 {
		t.Fatalf("a muted room: %v", k)
	}
	e.noErrs()
}

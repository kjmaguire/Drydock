package chats

import (
	"context"
	"sync"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
)

// cancelOnFirstPublish is the real event bus with one change: the first event published cancels the request that is sending
// the message, as a page reloaded or a dropped connection does while chat.send is still answering.
type cancelOnFirstPublish struct {
	events.Bus
	cancel context.CancelFunc
	mu     sync.Mutex
	got    []string
}

func (b *cancelOnFirstPublish) Publish(ctx context.Context, e events.Event) (uint64, error) {
	b.cancel()
	seq, err := b.Bus.Publish(ctx, e)
	if err == nil {
		b.mu.Lock()
		b.got = append(b.got, e.Type)
		b.mu.Unlock()
	}
	return seq, err
}

func (b *cancelOnFirstPublish) published() map[string]bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	m := map[string]bool{}
	for _, t := range b.got {
		m[t] = true
	}
	return m
}

// Once Send has claimed a turn, the turn runs whatever the caller does; the events that tell a page about it (the person's
// message, the running chat, chat.turn_started) are published even when the caller's request is cancelled meanwhile.
func TestATurnThatStartedIsAnnouncedEvenWhenTheRequestIsCancelled(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bus := &cancelOnFirstPublish{Bus: events.NewSQLBus(db, 64), cancel: func() {}}
	// no adapter is registered in the runtime: the turn fails to start once claimed, which is all this test needs
	s := New(Deps{DB: db, Bus: bus, Runtime: &rt.Runtime{}, Capabilities: func(string) (agents.Capabilities, bool) { return agents.Capabilities{}, true }}, Options{})
	defer s.Close()
	c, err := s.Create(ctx, CreateReq{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	req, cancel := context.WithCancel(ctx)
	defer cancel()
	bus.cancel = cancel
	if _, err := s.Send(req, SendReq{ID: c.ID, Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if req.Err() == nil {
		t.Fatal("the request was not cancelled: the test proves nothing")
	}
	s.Close() // the turn has recorded its end
	got := bus.published()
	for _, typ := range []string{"chat.message", "chat.updated", "chat.turn_started", "chat.turn"} {
		if !got[typ] {
			t.Errorf("%s was not published after the request was cancelled; published: %v", typ, bus.got)
		}
	}
}

package events

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/storage"
)

// A watcher that stalls while events are published is dropped by the bus (its buffer is full), and used to end for good. Consume
// replays what it missed and goes on: every event is handled once, in order (audit 2026-10-16, L2).
func TestConsumeCarriesOnAfterTheBusDroppedItAsSlow(t *testing.T) {
	db, err := storage.OpenTemp(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := NewSQLBus(db, 4) // a buffer of four
	t.Cleanup(func() { b.Close(); db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	release := make(chan struct{})
	var mu sync.Mutex
	var got []int
	first := true
	// Watch returns once the subscription is taken (a goroutine and a sleep missed the first events on a loaded machine)
	Watch(ctx, b, Filter{Types: []string{"t.tick"}}, func(e Event) {
		var d struct{ N int }
		_ = json.Unmarshal(e.Data, &d)
		if first {
			first = false
			<-release // stalled on the first event while many more are published
		}
		mu.Lock()
		got = append(got, d.N)
		mu.Unlock()
	})
	const total = 60
	for i := 1; i <= total; i++ {
		if _, err := b.Publish(ctx, Event{Type: "t.tick", Data: json.RawMessage(fmt.Sprintf(`{"N":%d}`, i))}); err != nil {
			t.Fatal(err)
		}
		if i == 10 {
			b.Publish(ctx, Event{Type: "t.other", Data: json.RawMessage(`{"N":999}`)}) // not the watcher's
		}
	}
	close(release)
	deadline := time.Now().Add(20 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= total || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// and events published after it caught up arrive live
	b.Publish(ctx, Event{Type: "t.tick", Data: json.RawMessage(`{"N":61}`)})
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != total+1 {
		t.Fatalf("handled %d events, want %d: %v", len(got), total+1, got)
	}
	for i, n := range got {
		if n != i+1 {
			t.Fatalf("event %d is %d: out of order, doubled or missing: %v", i, n, got)
		}
	}
}

// A handler that panics on one event does not end the watcher.
func TestConsumeSurvivesAPanickingHandler(t *testing.T) {
	db, err := storage.OpenTemp(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := NewSQLBus(db, 16)
	t.Cleanup(func() { b.Close(); db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var got []int
	Watch(ctx, b, Filter{Types: []string{"t.tick"}}, func(e Event) {
		var d struct{ N int }
		_ = json.Unmarshal(e.Data, &d)
		if d.N == 2 {
			panic("a handler bug")
		}
		mu.Lock()
		got = append(got, d.N)
		mu.Unlock()
	})
	for i := 1; i <= 4; i++ {
		b.Publish(ctx, Event{Type: "t.tick", Data: json.RawMessage(fmt.Sprintf(`{"N":%d}`, i))})
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(got) != "[1 3 4]" {
		t.Fatalf("handled %v", got)
	}
}

// An event keeps at most MaxEventData of payload and always valid JSON: a larger one is stored as a stand-in that says how large
// it was, and bytes that are not JSON are stored as a stand-in that says so (audit 2026-10-16, L2).
func TestAnEventKeepsAtMostAboundedValidPayload(t *testing.T) {
	b, _, _ := newBus(t)
	ctx := context.Background()
	big := json.RawMessage(`{"text":"` + strings.Repeat("é", MaxEventData) + `"}`)
	seqBig, err := b.Publish(ctx, Event{Type: "agent.output", Data: big})
	if err != nil {
		t.Fatal(err)
	}
	seqBad, err := b.Publish(ctx, Event{Type: "agent.output", Data: json.RawMessage(`{not json`)})
	if err != nil {
		t.Fatal(err)
	}
	seqOK, _ := b.Publish(ctx, Event{Type: "agent.output", Data: json.RawMessage(`{"fine":true}`)})
	evs, err := b.Replay(ctx, seqBig-1, Filter{}, 10)
	if err != nil || len(evs) != 3 {
		t.Fatalf("replay: %v %d", err, len(evs))
	}
	for _, e := range evs {
		if !json.Valid(e.Data) || len(e.Data) > MaxEventData {
			t.Fatalf("event %d holds %d bytes, valid=%v", e.Seq, len(e.Data), json.Valid(e.Data))
		}
	}
	var stand struct {
		Truncated bool   `json:"truncated"`
		Invalid   bool   `json:"invalid"`
		Bytes     int    `json:"bytes"`
		Head      string `json:"head"`
	}
	for _, c := range []struct {
		seq   uint64
		trunc bool
		inv   bool
	}{{seqBig, true, false}, {seqBad, false, true}} {
		for _, e := range evs {
			if e.Seq == c.seq {
				stand = struct {
					Truncated bool   `json:"truncated"`
					Invalid   bool   `json:"invalid"`
					Bytes     int    `json:"bytes"`
					Head      string `json:"head"`
				}{}
				if json.Unmarshal(e.Data, &stand) != nil || stand.Truncated != c.trunc || stand.Invalid != c.inv || stand.Bytes == 0 || stand.Head == "" {
					t.Fatalf("stand-in of %d: %s", c.seq, e.Data)
				}
			}
		}
	}
	if string(evs[2].Data) != `{"fine":true}` || evs[2].Seq != seqOK {
		t.Fatalf("an ordinary event was changed: %s", evs[2].Data)
	}
}

// fakeBus is a labelled test double of Bus: its channel already holds the events the replay returns, as happens when events are
// published between the subscription and the replay.
type fakeBus struct {
	live   chan Event
	replay []Event
	last   uint64
}

func (f *fakeBus) Publish(context.Context, Event) (uint64, error) { return 0, nil }
func (f *fakeBus) Subscribe(Filter) (<-chan Event, func())        { return f.live, func() {} }
func (f *fakeBus) Replay(_ context.Context, after uint64, _ Filter, _ int) ([]Event, error) {
	var out []Event
	for _, e := range f.replay {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	return out, nil
}
func (f *fakeBus) LastSeq(context.Context) (uint64, error) { return f.last, nil }

// An event that is both in the replay and on the live channel is handled once.
func TestConsumeHandlesAnEventOnceWhenTheReplayAndTheSubscriptionOverlap(t *testing.T) {
	evs := []Event{{Seq: 5, Type: "t.tick"}, {Seq: 6, Type: "t.tick"}, {Seq: 7, Type: "t.tick"}}
	fb := &fakeBus{live: make(chan Event, 8), replay: evs, last: 4}
	for _, e := range evs {
		fb.live <- e
	}
	fb.live <- Event{Seq: 8, Type: "t.tick"} // only live
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var got []uint64
	done := make(chan struct{})
	go func() {
		defer close(done)
		Consume(ctx, fb, Filter{}, func(e Event) {
			mu.Lock()
			got = append(got, e.Seq)
			mu.Unlock()
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 4 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(got) != "[5 6 7 8]" {
		t.Fatalf("handled %v", got)
	}
}

// Watch takes its subscription before it returns: an event published right after it is handled, with nothing to wait for first.
func TestWatchTakesItsSubscriptionBeforeItReturns(t *testing.T) {
	db, err := storage.OpenTemp(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := NewSQLBus(db, 16)
	t.Cleanup(func() { b.Close(); db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan uint64, 4)
	done := Watch(ctx, b, Filter{Types: []string{"t.tick"}}, func(e Event) { got <- e.Seq })
	seq, err := b.Publish(ctx, Event{Type: "t.tick"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s != seq {
			t.Fatalf("handled %d, published %d", s, seq)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an event published right after Watch returned was missed")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher did not end with its context")
	}
}

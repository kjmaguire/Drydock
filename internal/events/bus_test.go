package events

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/storage"
)

func newBus(t *testing.T) (*SQLBus, *storage.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.OpenTemp(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	b := NewSQLBus(db, 16)
	t.Cleanup(func() { b.Close(); db.Close() })
	return b, db, dir
}

func TestPublishAssignsMonotonicSequenceAndReplays(t *testing.T) {
	b, _, _ := newBus(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	seqs := make(chan uint64, 200)
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := b.Publish(ctx, Event{Type: "agent.output", AgentID: "a1", Data: json.RawMessage(`{"i":1}`)})
			if err != nil {
				t.Error(err)
				return
			}
			seqs <- s
		}(i)
	}
	wg.Wait()
	close(seqs)
	seen := map[uint64]bool{}
	for s := range seqs {
		if seen[s] {
			t.Fatalf("duplicate seq %d", s)
		}
		seen[s] = true
	}
	if len(seen) != 200 {
		t.Fatalf("got %d seqs", len(seen))
	}
	evs, err := b.Replay(ctx, 0, Filter{}, 1000)
	if err != nil || len(evs) != 200 {
		t.Fatalf("replay: %v len=%d", err, len(evs))
	}
	for i := 1; i < len(evs); i++ {
		if evs[i].Seq <= evs[i-1].Seq {
			t.Fatal("replay not ordered")
		}
	}
	tail, _ := b.Replay(ctx, evs[150].Seq, Filter{}, 1000)
	if len(tail) != 49 {
		t.Fatalf("after-seq replay got %d want 49", len(tail))
	}
}

func TestFilterPrefixAndScope(t *testing.T) {
	b, _, _ := newBus(t)
	ctx := context.Background()
	for _, e := range []Event{
		{Type: "workflow.step_started", RunID: "r1"},
		{Type: "workflow.step_completed", RunID: "r1"},
		{Type: "workflow.step_started", RunID: "r2"},
		{Type: "task.created", TaskID: "t1"},
	} {
		if _, err := b.Publish(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := b.Replay(ctx, 0, Filter{Types: []string{"workflow.*"}, RunID: "r1"}, 0)
	if len(got) != 2 {
		t.Fatalf("want 2 got %d", len(got))
	}
	got, _ = b.Replay(ctx, 0, Filter{Types: []string{"task.created"}}, 0)
	if len(got) != 1 {
		t.Fatalf("want 1 got %d", len(got))
	}
}

func TestSubscribeLiveAndSlowSubscriberIsClosedNotBlocking(t *testing.T) {
	b, _, _ := newBus(t)
	ctx := context.Background()
	live, cancel := b.Subscribe(Filter{Types: []string{"agent.*"}})
	defer cancel()
	slow, cancelSlow := b.Subscribe(Filter{})
	defer cancelSlow()

	// The fast consumer follows the documented contract: if the host is so loaded that even it falls behind and is closed,
	// it re-syncs by Replay from the last sequence it saw (once publishing is done). Either way it must end with all 100
	// events.
	type result struct {
		n    int
		last uint64
	}
	done := make(chan result, 1)
	go func() {
		var r result
		for e := range live {
			r.n++
			r.last = e.Seq
			if r.n == 100 {
				break
			}
		}
		done <- r
	}()
	start := time.Now()
	for i := 0; i < 100; i++ {
		if _, err := b.Publish(ctx, Event{Type: "agent.output"}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case r := <-done:
		if r.n < 100 {
			missed, _ := b.Replay(ctx, r.last, Filter{Types: []string{"agent.*"}}, 0)
			r.n += len(missed)
		}
		if r.n != 100 {
			t.Fatalf("fast subscriber saw %d of 100 events", r.n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fast subscriber starved")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("publishers were blocked by the slow subscriber")
	}
	// the slow subscriber never read: its channel (cap 16) overflowed and was closed
	n := 0
	for range slow {
		n++
	}
	if n != 16 {
		t.Fatalf("slow subscriber should have received exactly its buffer (16) before being closed, got %d", n)
	}
}

func TestDurableAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db, _ := storage.OpenTemp(ctx, dir)
	b := NewSQLBus(db, 8)
	for i := 0; i < 5; i++ {
		if _, err := b.Publish(ctx, Event{Type: "agent.started", AgentID: "a"}); err != nil {
			t.Fatal(err)
		}
	}
	b.Close()
	db.Close()

	db2, err := storage.OpenTemp(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	b2 := NewSQLBus(db2, 8)
	defer b2.Close()
	last, _ := b2.LastSeq(ctx)
	if last != 5 {
		t.Fatalf("last seq %d want 5", last)
	}
	s, _ := b2.Publish(ctx, Event{Type: "agent.stopped"})
	if s != 6 {
		t.Fatalf("sequence must continue monotonically across restart, got %d", s)
	}
	missed, _ := b2.Replay(ctx, 3, Filter{}, 0) // a UI that last saw seq 3 catches up
	if len(missed) != 3 {
		t.Fatalf("catch-up want 3 got %d", len(missed))
	}
}

// Publishing while the bus closes never hangs: an event the writer took is acknowledged, one that arrived after its last
// drain fails with ErrClosed instead of waiting forever for a reply.
func TestPublishDuringCloseNeverHangs(t *testing.T) {
	for round := 0; round < 20; round++ {
		b, _, _ := newBus(t)
		var wg sync.WaitGroup
		errs := make(chan error, 200)
		for i := 0; i < 200; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := b.Publish(context.Background(), Event{Type: "t.x"})
				errs <- err
			}()
		}
		go b.Close()
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatalf("round %d: a Publish never returned after Close", round)
		}
		close(errs)
		for err := range errs {
			if err != nil && err != ErrClosed {
				t.Fatalf("round %d: %v", round, err)
			}
		}
	}
}

// SetRedactor rewrites an event before it is stored and before any subscriber sees it, so the database, a replay and the live
// stream all hold the same text (audit 2026-10-16, H7).
func TestARedactorRewritesAnEventBeforeItIsStoredOrDelivered(t *testing.T) {
	b, db, _ := newBus(t)
	ctx := context.Background()
	live, cancel := b.Subscribe(Filter{})
	defer cancel()
	b.SetRedactor(func(e Event) Event {
		e.Data = json.RawMessage(strings.ReplaceAll(string(e.Data), "hunter2-hunter2", "[REDACTED]"))
		return e
	})
	seq, err := b.Publish(ctx, Event{Type: "agent.failed", AgentID: "a1", Data: json.RawMessage(`{"error":"login hunter2-hunter2 failed"}`)})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"error":"login [REDACTED] failed"}`
	select {
	case e := <-live:
		if string(e.Data) != want {
			t.Fatalf("subscriber saw %s", e.Data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event delivered")
	}
	replayed, err := b.Replay(ctx, seq-1, Filter{}, 10)
	if err != nil || len(replayed) != 1 || string(replayed[0].Data) != want {
		t.Fatalf("%v %+v", err, replayed)
	}
	var stored string
	if err := db.Reader().QueryRow(`SELECT data FROM events WHERE seq=?`, seq).Scan(&stored); err != nil || stored != want {
		t.Fatalf("%v stored %q", err, stored)
	}

	// without a redactor events pass as they are
	b.SetRedactor(nil)
	if _, err := b.Publish(ctx, Event{Type: "agent.failed", Data: json.RawMessage(`{"error":"hunter2-hunter2"}`)}); err != nil {
		t.Fatal(err)
	}
	e := <-live
	if string(e.Data) != `{"error":"hunter2-hunter2"}` {
		t.Fatalf("a removed redactor still ran: %s", e.Data)
	}
}

package events

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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

// SeqBefore finds where a window of history starts without reading it (audit 2026-10-16, M15).
func TestSeqBeforeFindsTheStartOfAWindow(t *testing.T) {
	ctx := context.Background()
	b, _, _ := newBus(t)
	base := time.Now().UTC().Add(-100 * time.Hour)
	var seqs []uint64
	for i := 0; i < 100; i++ {
		s, err := b.Publish(ctx, Event{Type: "x.y", Time: base.Add(time.Duration(i) * time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, s)
	}
	for _, c := range []struct {
		name string
		at   time.Time
		want uint64
	}{
		{"every event is newer", base.Add(-time.Hour), 0},
		{"the first event is the first of the window", base, 0},
		{"in the middle", base.Add(50*time.Hour - time.Minute), seqs[49]},
		{"just after one", base.Add(50*time.Hour + time.Minute), seqs[50]},
		{"every event is older", base.Add(1000 * time.Hour), seqs[99]},
	} {
		got, err := b.SeqBefore(ctx, c.at)
		if err != nil || got != c.want {
			t.Errorf("%s: %d %v, want %d", c.name, got, err, c.want)
		}
	}
	empty, _, _ := newBus(t)
	if got, err := empty.SeqBefore(ctx, time.Now()); err != nil || got != 0 {
		t.Errorf("an empty log: %d %v", got, err)
	}
}

// A replay from a cursor reads the log from the cursor on and not from its start, and one for exact event types does not scan the
// log for a pattern: a rebuild of what happened lately must cost what happened lately (audit 2026-10-16, M15).
func TestReplayStatementsAreBoundedByTheCursor(t *testing.T) {
	_, db, _ := newBus(t)
	for name, f := range map[string]Filter{
		"no types":       {},
		"exact types":    {Types: []string{"agent.started", "agent.failed", "watchdog.paused"}},
		"one project":    {ProjectID: "p1", Types: []string{"agent.*"}},
		"a task's feed":  {TaskID: "t1"},
		"a run's events": {RunID: "r1"},
	} {
		q, args := replayQuery(1000, f, 100)
		rows, err := db.Reader().Query("EXPLAIN QUERY PLAN "+q, args...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		joined := strings.Join(plan, " | ")
		t.Logf("%s: %s", name, joined)
		if strings.Contains(joined, "SCAN events") && !strings.Contains(joined, "USING") {
			t.Errorf("%s reads the whole log: %s", name, joined)
		}
		if name == "exact types" && strings.Contains(joined, "LIKE") {
			t.Errorf("%s: %s", name, joined)
		}
	}
}

// A payload over the limit travels as a stand-in that keeps its start; one under it is untouched; the stored event is whole.
func TestForTransportCutsOnlyWhatIsOverTheLimit(t *testing.T) {
	small := Event{Seq: 1, Type: "x", Data: json.RawMessage(`{"a":1}`)}
	if got := ForTransport(small, 100); string(got.Data) != `{"a":1}` {
		t.Fatalf("a small payload changed: %s", got.Data)
	}
	big := Event{Seq: 2, Type: "agent.output", ProjectID: "p", Data: json.RawMessage(`{"text":"` + strings.Repeat("é", 100_000) + `"}`)}
	got := ForTransport(big, 64<<10)
	var d struct {
		Truncated bool   `json:"truncated"`
		Bytes     int    `json:"bytes"`
		Head      string `json:"head"`
	}
	if err := json.Unmarshal(got.Data, &d); err != nil || !d.Truncated || d.Bytes != len(big.Data) || len(d.Head) == 0 || len(d.Head) > 4<<10 {
		t.Fatalf("stand-in %.200s: %+v %v", got.Data, d, err)
	}
	if !strings.HasPrefix(d.Head, `{"text":"é`) || !utf8.ValidString(d.Head) {
		t.Fatalf("the start was not kept, or was cut inside a character: %q", d.Head[:20])
	}
	if got.Seq != 2 || got.Type != "agent.output" || got.ProjectID != "p" {
		t.Fatalf("the envelope changed: %+v", got)
	}
	if len(big.Data) < 200_000 {
		t.Fatal("the original was changed")
	}
	if ForTransport(big, 0).Data == nil || len(ForTransport(big, 0).Data) != len(big.Data) {
		t.Fatal("a limit of 0 means none")
	}
}

package core_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
)

// The stream to the UI carries a stand-in for an event with a huge payload; the stored event stays whole (audit 2026-10-16, M22).
func TestFollowSendsAStandInForAHugePayload(t *testing.T) {
	a := open(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	big := `{"text":"` + strings.Repeat("x", 2*core.MaxFollowData) + `"}`
	seq, err := a.c.Bus.Publish(ctx, events.Event{Type: "test.huge", Data: json.RawMessage(big)})
	if err != nil {
		t.Fatal(err)
	}
	small, err := a.c.Bus.Publish(ctx, events.Event{Type: "test.small", Data: json.RawMessage(`{"ok":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	got := map[uint64]events.Event{}
	fctx, stop := context.WithCancel(ctx)
	a.c.Follow(fctx, seq-1, events.Filter{Types: []string{"test.*"}}, func(e events.Event) bool {
		got[e.Seq] = e
		if len(got) == 2 {
			stop()
		}
		return true
	}, nil)
	if len(got[seq].Data) > 8<<10 || !strings.Contains(string(got[seq].Data), `"truncated":true`) {
		t.Fatalf("the huge event travelled whole: %d bytes", len(got[seq].Data))
	}
	if string(got[small].Data) != `{"ok":true}` {
		t.Fatalf("a small event changed: %s", got[small].Data)
	}
	stored, err := a.c.Bus.Replay(ctx, seq-1, events.Filter{Types: []string{"test.huge"}}, 1)
	if err != nil || len(stored) != 1 || len(stored[0].Data) != len(big) {
		t.Fatalf("the stored event is not whole: %v %d", err, len(stored))
	}
}

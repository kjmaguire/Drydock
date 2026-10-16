package core_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
)

// An event with a huge payload is stored as a stand-in that says how large it was (events.MaxEventData, audit 2026-10-16, L2), and the
// stream to the UI never carries it whole either (M22).
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
	if len(got[seq].Data) > 40<<10 || !strings.Contains(string(got[seq].Data), `"truncated":true`) || !strings.Contains(string(got[seq].Data), fmt.Sprintf(`"bytes":%d`, len(big))) {
		t.Fatalf("the huge event travelled whole: %d bytes", len(got[seq].Data))
	}
	if string(got[small].Data) != `{"ok":true}` {
		t.Fatalf("a small event changed: %s", got[small].Data)
	}
	stored, err := a.c.Bus.Replay(ctx, seq-1, events.Filter{Types: []string{"test.huge"}}, 1)
	if err != nil || len(stored) != 1 || len(stored[0].Data) > events.MaxEventData || !strings.Contains(string(stored[0].Data), fmt.Sprintf(`"bytes":%d`, len(big))) {
		t.Fatalf("the stored event is not the stand-in: %v %d", err, len(stored))
	}
	// an event between the transport limit and nothing is whole, and is sent whole
	mid := `{"text":"` + strings.Repeat("y", core.MaxFollowData/2) + `"}`
	midSeq, err := a.c.Bus.Publish(ctx, events.Event{Type: "test.mid", Data: json.RawMessage(mid)})
	if err != nil {
		t.Fatal(err)
	}
	var midGot events.Event
	mctx, mstop := context.WithCancel(ctx)
	a.c.Follow(mctx, midSeq-1, events.Filter{Types: []string{"test.mid"}}, func(e events.Event) bool { midGot = e; mstop(); return true }, nil)
	if string(midGot.Data) != mid {
		t.Fatalf("an event that fits was changed: %d bytes", len(midGot.Data))
	}
}

package events

import (
	"context"
	"time"

	"github.com/kjmaguire/drydock/internal/safe"
)

// Watch calls handle for every event that matches f, in order, on a goroutine of its own, until ctx ends. It returns once the
// subscription is taken, so an event published after Watch returns is never missed; the channel it returns is closed when the
// goroutine has ended.
//
// A subscriber the bus drops for falling behind (it closes the channel) used to be the end of a watcher for good. Watch replays
// what the subscriber missed from the last sequence number it handled and subscribes again, so a watcher that stalled for a
// while under a burst carries on (audit 2026-10-16, L2). The replay is taken after the new subscription, so no event falls in
// the gap, and an event seen twice is handled once.
//
// A panic in handle is contained and logged by internal/safe, and the next event is handled.
func Watch(ctx context.Context, b Bus, f Filter, handle func(Event)) <-chan struct{} {
	done := make(chan struct{})
	// where "now" is, read before the subscription: an event between the two is in the replay, not lost
	var last uint64
	known := false // whether last says where "now" was; without it the first subscription takes live events only
	if h, ok := b.(interface {
		LastSeq(context.Context) (uint64, error)
	}); ok {
		if seq, err := h.LastSeq(ctx); err == nil {
			last, known = seq, true
		}
	}
	ch, cancel := b.Subscribe(f)
	safe.Go("events.Watch", func() {
		defer close(done)
		deliver := func(e Event) {
			last = e.Seq
			_ = safe.Do("events.Watch", func() { handle(e) })
		}
		for first := true; ctx.Err() == nil; first = false {
			if !first {
				ch, cancel = b.Subscribe(f)
			}
			if known || last > 0 {
				for { // what was published between the last event handled and the subscription
					evs, err := b.Replay(ctx, last, f, 500)
					if err != nil {
						break // the live events carry on; the next drop replays again
					}
					for _, e := range evs {
						deliver(e)
					}
					if len(evs) < 500 {
						break
					}
				}
			}
		loop:
			for {
				select {
				case <-ctx.Done():
					cancel()
					return
				case e, ok := <-ch:
					if !ok {
						break loop
					}
					if e.Seq > last {
						deliver(e)
					}
				}
			}
			cancel()
			select { // the bus closed the subscription (it was slow, or it is shutting down): do not spin
			case <-ctx.Done():
			case <-time.After(100 * time.Millisecond):
			}
		}
		cancel()
	})
	return done
}

// Consume is Watch that waits for the watcher to end, for a goroutine of the caller's own.
func Consume(ctx context.Context, b Bus, f Filter, handle func(Event)) { <-Watch(ctx, b, f, handle) }

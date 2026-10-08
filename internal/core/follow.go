package core

import (
	"context"

	"github.com/kjmaguire/drydock/internal/events"
)

// Follow delivers every event after the given sequence that matches f to send, in order and exactly once: it subscribes
// first, replays the persisted backlog, then forwards live events. If the live subscription falls behind and is closed it
// replays from the last delivered sequence and resubscribes, so a slow consumer loses nothing (brief 24: the UI is a
// projection of the persisted log). It returns when ctx ends or send returns false. flush, if set, is called after each
// batch (the HTTP transport flushes its response there).
func (c *Core) Follow(ctx context.Context, after uint64, f events.Filter, send func(events.Event) bool, flush func()) {
	last := after
	deliver := func(e events.Event) bool {
		if e.Seq <= last {
			return true
		}
		last = e.Seq
		return send(e)
	}
	for ctx.Err() == nil {
		ch, cancel := c.Bus.Subscribe(f)
		for {
			backlog, err := c.Bus.Replay(ctx, last, f, 1000)
			if err != nil {
				cancel()
				return
			}
			for _, e := range backlog {
				if !deliver(e) {
					cancel()
					return
				}
			}
			if flush != nil {
				flush()
			}
			if len(backlog) < 1000 {
				break
			}
		}
		fellBehind := false
		for !fellBehind {
			select {
			case <-ctx.Done():
				cancel()
				return
			case e, ok := <-ch:
				if !ok {
					fellBehind = true
					continue
				}
				if !deliver(e) {
					cancel()
					return
				}
				if flush != nil {
					flush()
				}
			}
		}
		cancel()
	}
}

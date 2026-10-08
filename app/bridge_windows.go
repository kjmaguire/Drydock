//go:build windows

package app

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
)

// EventName is the Wails event every Drydock event is forwarded on.
const EventName = "drydock:event"

// Bridge is the single Wails service: it carries the core's method registry and event stream to the UI. It owns no
// logic; everything it does is core.Call and core.Follow, the same code the development HTTP transport exposes.
type Bridge struct {
	Core *core.Core
	app  *application.App

	mu     sync.Mutex
	cancel context.CancelFunc
}

// Call invokes a core method with JSON parameters and returns the JSON result.
func (b *Bridge) Call(method string, params string) (string, error) {
	res, err := b.Core.Call(context.Background(), method, json.RawMessage(params))
	if err != nil {
		return "", err
	}
	return string(res), nil
}

// Subscribe starts forwarding events after the given sequence on EventName. The UI calls it once on load with the last
// sequence it has; calling it again restarts the stream from the new sequence (a reload never duplicates or loses events).
func (b *Bridge) Subscribe(after uint64) {
	b.mu.Lock()
	if b.cancel != nil {
		b.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.mu.Unlock()
	go b.Core.Follow(ctx, after, events.Filter{}, func(e events.Event) bool {
		b.app.Event.Emit(EventName, e)
		return ctx.Err() == nil
	}, nil)
}

// ServiceShutdown stops the event forwarding.
func (b *Bridge) ServiceShutdown() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancel != nil {
		b.cancel()
	}
	return nil
}

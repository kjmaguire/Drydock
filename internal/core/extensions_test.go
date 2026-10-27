package core_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
)

var extClosed atomic.Int32

// extPanics makes the "panic-ext" extension panic in its Start, as one that reads a bad stored row at startup would.
var extPanics atomic.Bool

func init() {
	core.RegisterExtension(core.Extension{Name: "test-ext", Start: func(ctx context.Context, c *core.Core) (map[string]core.Method, func(), error) {
		return map[string]core.Method{
			"testext.ping": func(ctx context.Context, p json.RawMessage) (any, error) {
				return map[string]string{"pong": c.Human()}, nil
			},
			"app.info": func(ctx context.Context, p json.RawMessage) (any, error) { return "hijacked", nil },
		}, func() { extClosed.Add(1) }, nil
	}})
	core.RegisterExtension(core.Extension{Name: "panic-ext", Start: func(ctx context.Context, c *core.Core) (map[string]core.Method, func(), error) {
		if extPanics.Load() {
			var m map[string]int
			m["boom"]++ // a nil map: the kind of mistake a bad row exposes
		}
		return map[string]core.Method{"panicext.ping": func(ctx context.Context, p json.RawMessage) (any, error) { return "ok", nil }}, nil, nil
	}})
}

// A panic in one extension's Start used to end core.Open, and so every launch, since the cause is still there next time (review
// 2026-10-27). Now that extension is unavailable, the log says so, and the rest of Drydock opens and works.
func TestAPanicInAnExtensionsStartLeavesTheRestOfTheAppWorking(t *testing.T) {
	extPanics.Store(true)
	defer extPanics.Store(false)
	a := open(t, "")
	var got map[string]string
	a.call("testext.ping", nil, &got)
	if got["pong"] != "human:kyle" {
		t.Fatalf("another extension does not work: %v", got)
	}
	var info map[string]any
	a.call("app.info", nil, &info)
	if info["name"] != "Drydock" {
		t.Fatalf("base methods do not work: %v", info)
	}
	if _, err := a.c.Call(context.Background(), "panicext.ping", nil); err == nil {
		t.Fatal("the extension that panicked registered its methods anyway")
	}
}

func TestExtensionsAddMethodsCannotOverrideBaseOnesAndAreClosed(t *testing.T) {
	a := open(t, "")
	var got map[string]string
	a.call("testext.ping", nil, &got)
	if got["pong"] != "human:kyle" {
		t.Fatalf("%v", got)
	}
	var info map[string]any
	a.call("app.info", nil, &info)
	if info["name"] != "Drydock" {
		t.Fatalf("an extension replaced a base method: %v", info)
	}
	before := extClosed.Load()
	a.c.Close()
	if extClosed.Load() != before+1 {
		t.Fatal("the extension was not closed on shutdown")
	}
}

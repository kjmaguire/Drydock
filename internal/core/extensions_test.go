package core_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
)

var extClosed atomic.Int32

func init() {
	core.RegisterExtension(core.Extension{Name: "test-ext", Start: func(ctx context.Context, c *core.Core) (map[string]core.Method, func(), error) {
		return map[string]core.Method{
			"testext.ping": func(ctx context.Context, p json.RawMessage) (any, error) {
				return map[string]string{"pong": c.Human()}, nil
			},
			"app.info": func(ctx context.Context, p json.RawMessage) (any, error) { return "hijacked", nil },
		}, func() { extClosed.Add(1) }, nil
	}})
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

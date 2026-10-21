package core_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
)

// A method that panics, to see what the application does about it. It is registered for every test of this package; nothing else
// calls it.
func init() {
	core.RegisterExtension(core.Extension{Name: "panictest", Start: func(ctx context.Context, c *core.Core) (map[string]core.Method, func(), error) {
		return map[string]core.Method{
			"test.panic": func(ctx context.Context, params json.RawMessage) (any, error) {
				var p struct{ Text string }
				_ = json.Unmarshal(params, &p)
				var xs []int
				return xs[len(p.Text)], nil // index out of range: a runtime panic
			},
			"test.panicvalue": func(ctx context.Context, params json.RawMessage) (any, error) {
				var p struct{ Text string }
				_ = json.Unmarshal(params, &p)
				panic("the code was working on " + p.Text)
			},
		}, nil, nil
	}})
}

// Audit 2026-10-16, H9: nothing recovered a panic, so one in any method or goroutine ended the whole application and every run in
// it. A call that panics is an error to its caller, the application goes on, and the panic is reported (log and app.panic event) with
// secrets scanned out of it.
func TestAPanicInAMethodIsAnErrorToItsCallerAndIsReportedWithoutSecrets(t *testing.T) {
	a := open(t, "")
	err := a.callErr("test.panic", map[string]string{"Text": "x"})
	if err == nil || !strings.Contains(err.Error(), "internal error in rpc test.panic") || !strings.Contains(err.Error(), "index out of range") {
		t.Fatalf("a panic in a method came back as %v", err)
	}
	// the application still answers
	var projects []core.Project
	a.call("project.list", nil, &projects)

	err = a.callErr("test.panicvalue", map[string]string{"Text": "the key " + plantedToken})
	if err == nil {
		t.Fatal("no error")
	}
	var found []map[string]any
	a.waitFor("the app.panic events", func() bool {
		found = a.eventsOf("app.panic")
		return len(found) >= 2
	})
	var sawNilMap, sawValue bool
	for _, ev := range found {
		raw, _ := json.Marshal(ev)
		if strings.Contains(string(raw), plantedToken) {
			t.Fatalf("an app.panic event carries a secret: %s", raw)
		}
		sawNilMap = sawNilMap || ev["where"] == "rpc test.panic"
		sawValue = sawValue || (ev["where"] == "rpc test.panicvalue" && strings.Contains(ev["message"].(string), "the code was working on"))
	}
	if !sawNilMap || !sawValue {
		t.Fatalf("events %v", found)
	}

	// one event per place every few seconds, not one per panic
	for i := 0; i < 5; i++ {
		_ = a.callErr("test.panic", map[string]string{"Text": "again"})
	}
	time.Sleep(200 * time.Millisecond)
	n := 0
	for _, ev := range a.eventsOf("app.panic") {
		if ev["where"] == "rpc test.panic" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d app.panic events for one place in a burst", n)
	}
}

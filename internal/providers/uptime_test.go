package providers

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The endpoint uptime tests run the real tracker and Registry against a PROTOCOL FIXTURE (a TEST DOUBLE of OpenRouter's
// GET /api/v1/models/{author}/{slug}/endpoints, shaped as docs/phase0/model-providers.md A.12 records it). It proves the
// request, the cache and the decision; it proves nothing about OpenRouter's real figures.

const endpointsPath = "/api/v1/models/acme/coder-7b/endpoints"

type upClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *upClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *upClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func pct(v float64) *float64 { return &v }

// endpointList is the endpoints response: each entry is tag, provider_name and the two uptime windows (nil = null).
func endpointList(eps ...map[string]any) map[string]any {
	return map[string]any{"data": map[string]any{"id": "acme/coder-7b", "name": "Acme: Coder 7B", "endpoints": eps}}
}

func ep(tag string, up30, up5 *float64) map[string]any {
	m := map[string]any{"name": "Up | acme/coder-7b", "provider_name": "Up " + tag, "tag": tag, "status": 0,
		"uptime_last_30m": nil, "uptime_last_5m": nil, "pricing": map[string]any{"prompt": "0.0000001", "completion": "0.0000002"}}
	if up30 != nil {
		m["uptime_last_30m"] = *up30
	}
	if up5 != nil {
		m["uptime_last_5m"] = *up5
	}
	return m
}

func newUptime(t *testing.T, e *env, routes map[string]func(w http.ResponseWriter, r *http.Request)) (*EndpointUptime, Provider, *fixture, *upClock) {
	t.Helper()
	f := newFixture(t, routes)
	p := e.create(t, NewProvider{Name: "OpenRouter", Kind: KindAggregator, BaseURL: f.URL + "/api/v1", APIKey: "sk-or-v1-fixture-secret", Flavor: FlavorOpenRouter})
	clk := &upClock{t: time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)}
	return NewEndpointUptime(e.reg, UptimeOptions{Now: clk.now, Timeout: 300 * time.Millisecond}), p, f, clk
}

func TestEndpointUptimeAvoidsEndpointsBelowTheThreshold(t *testing.T) {
	e := newEnv(t)
	u, p, f, _ := newUptime(t, e, map[string]func(w http.ResponseWriter, r *http.Request){
		endpointsPath: jsonBody(endpointList(
			ep("good", pct(99.9), nil),
			ep("flaky", pct(91.5), nil),
			ep("recent-dip", pct(99.5), pct(80)), // the 5 minute window wins over 30 minutes: it is the most recent
			ep("new", nil, nil),                  // no figure yet (too few requests): never avoided
			ep("", pct(10), nil),                 // no tag: cannot be named to OpenRouter
		)),
	})
	a, changed := u.Avoid(e.ctx, p, "acme/coder-7b")
	want := []AvoidedEndpoint{{Tag: "flaky", Name: "Up flaky", UptimePct: 91.5, Window: "30m"}, {Tag: "recent-dip", Name: "Up recent-dip", UptimePct: 80, Window: "5m"}}
	if !reflect.DeepEqual(a.Avoided, want) || a.KeptBest || a.Threshold != DefaultMinEndpointUptime || !changed {
		t.Fatalf("avoidance %+v changed=%v", a, changed)
	}
	got := f.seen(endpointsPath)
	if len(got) != 1 || got[0].Auth != "Bearer sk-or-v1-fixture-secret" {
		t.Fatalf("lookup %+v: the endpoints list is read with the provider's key, like the probes", got)
	}
	// The same set again is no change; the detail panel lists it.
	if _, changed := u.Avoid(e.ctx, p, "acme/coder-7b"); changed {
		t.Fatal("an unchanged set reported as changed")
	}
	if av := u.Avoiding(p.ID); len(av) != 1 || av[0].Model != "acme/coder-7b" || len(av[0].Avoided) != 2 {
		t.Fatalf("avoiding %+v", av)
	}
}

func TestEndpointUptimeCacheIsRespected(t *testing.T) {
	e := newEnv(t)
	var hits atomic.Int32
	release := make(chan struct{})
	u, p, f, clk := newUptime(t, e, map[string]func(w http.ResponseWriter, r *http.Request){
		endpointsPath: func(w http.ResponseWriter, r *http.Request) {
			if hits.Add(1) == 1 {
				<-release // hold the first lookup so concurrent callers pile up behind it
			}
			jsonBody(endpointList(ep("good", pct(100), nil), ep("bad", pct(50), nil)))(w, r)
		},
	})
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a, _ := u.Avoid(e.ctx, p, "acme/coder-7b"); len(a.Avoided) != 1 {
				t.Errorf("concurrent caller got %+v", a)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if n := len(f.seen(endpointsPath)); n != 1 {
		t.Fatalf("%d lookups for concurrent calls, want 1", n)
	}
	clk.add(4 * time.Minute)
	u.Avoid(e.ctx, p, "acme/coder-7b")
	if n := len(f.seen(endpointsPath)); n != 1 {
		t.Fatalf("%d lookups within the 5 minute cache, want 1", n)
	}
	clk.add(time.Minute + time.Second)
	u.Avoid(e.ctx, p, "acme/coder-7b")
	if n := len(f.seen(endpointsPath)); n != 2 {
		t.Fatalf("%d lookups after the cache expired, want 2", n)
	}
	// Another model has its own entry.
	f.set("/api/v1/models/acme/other/endpoints", jsonBody(endpointList(ep("x", pct(100), nil))))
	u.Avoid(e.ctx, p, "acme/other")
	if n := len(f.seen("/api/v1/models/acme/other/endpoints")); n != 1 {
		t.Fatalf("other model: %d lookups", n)
	}
}

func TestEndpointUptimeFailedLookupAvoidsNothing(t *testing.T) {
	e := newEnv(t)
	u, p, f, clk := newUptime(t, e, map[string]func(w http.ResponseWriter, r *http.Request){
		endpointsPath:                           status(500),
		"/api/v1/models/acme/slow/endpoints":    func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() },
		"/api/v1/models/acme/garbled/endpoints": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) },
	})
	for _, model := range []string{"acme/coder-7b", "acme/slow", "acme/garbled", "acme/missing"} {
		start := time.Now()
		a, changed := u.Avoid(e.ctx, p, model)
		if len(a.Avoided) != 0 || changed {
			t.Errorf("%s: a failed lookup avoided %+v", model, a)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s: the lookup held the call for %s", model, d)
		}
	}
	// A failure is remembered for a minute, then tried again.
	u.Avoid(e.ctx, p, "acme/coder-7b")
	if n := len(f.seen(endpointsPath)); n != 1 {
		t.Fatalf("%d lookups within the failure pause", n)
	}
	clk.add(61 * time.Second)
	u.Avoid(e.ctx, p, "acme/coder-7b")
	if n := len(f.seen(endpointsPath)); n != 2 {
		t.Fatalf("%d lookups after the failure pause", n)
	}
	// Models OpenRouter has no endpoint list for, and other flavors, are never looked up.
	count := func() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.reqs) }
	before := count()
	u.Avoid(e.ctx, p, "openrouter/auto")
	u.Avoid(e.ctx, p, "no-author")
	other := p
	other.Flavor = FlavorOpenAI
	u.Avoid(e.ctx, other, "acme/coder-7b")
	var nilTracker *EndpointUptime
	if a, _ := nilTracker.Avoid(e.ctx, p, "acme/coder-7b"); len(a.Avoided) != 0 {
		t.Fatal("nil tracker avoided")
	}
	if count() != before {
		t.Fatal("unlistable models were looked up")
	}
}

func TestEndpointUptimeAllBelowKeepsTheBest(t *testing.T) {
	e := newEnv(t)
	u, p, _, _ := newUptime(t, e, map[string]func(w http.ResponseWriter, r *http.Request){
		endpointsPath: jsonBody(endpointList(ep("a", pct(60), nil), ep("b", pct(90), nil), ep("c", pct(90), nil), ep("d", pct(75), nil))),
	})
	a, _ := u.Avoid(e.ctx, p, "acme/coder-7b")
	if !reflect.DeepEqual(a.Tags(), []string{"a", "d"}) || !a.KeptBest {
		t.Fatalf("all below the threshold: avoided %v keptBest=%v, want a and d avoided and the two best (90%%) kept", a.Tags(), a.KeptBest)
	}
	// A single endpoint is never excluded, however poor.
	u2, p2, _, _ := newUptime(t, e, map[string]func(w http.ResponseWriter, r *http.Request){endpointsPath: jsonBody(endpointList(ep("only", pct(5), nil)))})
	if a, _ := u2.Avoid(e.ctx, p2, "acme/coder-7b"); len(a.Avoided) != 0 || a.KeptBest {
		t.Fatalf("the only endpoint was avoided: %+v", a)
	}
}

func TestEndpointUptimeThresholdSetting(t *testing.T) {
	e := newEnv(t)
	u, p, f, _ := newUptime(t, e, map[string]func(w http.ResponseWriter, r *http.Request){
		endpointsPath: jsonBody(endpointList(ep("good", pct(99.9), nil), ep("ok", pct(95), nil), ep("poor", pct(85), nil))),
	})
	if v := e.reg.MinEndpointUptime(e.ctx, p.ID); v != 98 {
		t.Fatalf("default threshold %v", v)
	}
	if a, _ := u.Avoid(e.ctx, p, "acme/coder-7b"); !reflect.DeepEqual(a.Tags(), []string{"ok", "poor"}) {
		t.Fatalf("at 98%%: %v", a.Tags())
	}
	if err := e.reg.SetMinEndpointUptime(e.ctx, p.ID, 90); err != nil {
		t.Fatal(err)
	}
	a, changed := u.Avoid(e.ctx, p, "acme/coder-7b")
	if !reflect.DeepEqual(a.Tags(), []string{"poor"}) || a.Threshold != 90 || !changed {
		t.Fatalf("at 90%%: %+v changed=%v", a, changed)
	}
	if n := len(f.seen(endpointsPath)); n != 1 {
		t.Fatalf("a new threshold applies to the cached list: %d lookups", n)
	}
	if err := e.reg.SetMinEndpointUptime(e.ctx, p.ID, 0); err != nil {
		t.Fatal(err)
	}
	if a, changed := u.Avoid(e.ctx, p, "acme/coder-7b"); len(a.Avoided) != 0 || !changed {
		t.Fatalf("0 turns it off: %+v changed=%v", a, changed)
	}
	if len(u.Avoiding(p.ID)) != 0 {
		t.Fatal("the detail panel still lists an avoidance after it was turned off")
	}
	for _, bad := range []float64{-1, 100.5} {
		if err := e.reg.SetMinEndpointUptime(e.ctx, p.ID, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v accepted: %v", bad, err)
		}
	}
	if err := e.reg.SetMinEndpointUptime(e.ctx, "prov_nope", 50); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown provider: %v", err)
	}
	// The setting leaves with its provider.
	if err := e.reg.Delete(e.ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.db.Reader().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM settings WHERE key LIKE 'provider.endpoint_uptime_min.%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d threshold settings left after the provider was deleted (%v)", n, err)
	}
}

package providers

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func recv(t *testing.T, c <-chan HealthEvent, d time.Duration) HealthEvent {
	t.Helper()
	select {
	case ev := <-c:
		return ev
	case <-time.After(d):
		t.Fatal("timed out waiting for a health event")
		return HealthEvent{}
	}
}

func TestHealthLoopEmitsUpDegradedDownAndRecovery(t *testing.T) {
	e := newEnv(t)
	var code atomic.Int32
	code.Store(200)
	f := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/health": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(int(code.Load())) },
	})
	p := e.create(t, NewProvider{Name: "gpu", Kind: KindHost, BaseURL: f.URL + "/v1"})
	m := NewMonitor(e.reg, e.pr, e.bus, MonitorOptions{Interval: 20 * time.Millisecond, DownAfter: 2})
	ch, cancel := m.Subscribe()
	defer cancel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	m.Start(ctx)
	defer m.Stop()

	if ev := recv(t, ch, 2*time.Second); ev.To != HealthUp || ev.From != HealthUnknown || ev.ProviderID != p.ID {
		t.Fatalf("first event %+v", ev)
	}
	st, _ := m.Status(e.ctx, p.ID)
	if st.Health != HealthUp || !st.Available || st.LastCheck.IsZero() {
		t.Fatalf("status %+v", st)
	}
	code.Store(500)
	if ev := recv(t, ch, 2*time.Second); ev.To != HealthDegraded {
		t.Fatalf("want degraded after the first failure, got %+v", ev)
	}
	if ev := recv(t, ch, 2*time.Second); ev.To != HealthDown {
		t.Fatalf("want down after repeated failures, got %+v", ev)
	}
	st, _ = m.Status(e.ctx, p.ID)
	if st.Health != HealthDown || st.Available || st.LastError == "" {
		t.Fatalf("status when down %+v", st)
	}
	code.Store(200)
	if ev := recv(t, ch, 2*time.Second); ev.To != HealthUp || ev.From != HealthDown {
		t.Fatalf("recovery %+v", ev)
	}
	// The same transitions reached the persisted event bus and the providers row.
	types := e.eventTypes(t, "provider.")
	want := []string{"provider.registered", "provider.up", "provider.degraded", "provider.down", "provider.up"}
	if len(types) != len(want) {
		t.Fatalf("events %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("events %v, want %v", types, want)
		}
	}
	if g, _ := e.reg.Get(e.ctx, p.ID); g.Status != string(HealthUp) {
		t.Fatalf("persisted status %s", g.Status)
	}
}

func TestHealthLoopChecksCloudProvidersOnTheirOwnIntervalAndSkipsDisabled(t *testing.T) {
	e := newEnv(t)
	var hits, listings atomic.Int32
	f := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/health": func(w http.ResponseWriter, r *http.Request) { hits.Add(1) },
		"/v1/models": func(w http.ResponseWriter, r *http.Request) {
			listings.Add(1)
			jsonBody(map[string]any{"data": []map[string]any{{"id": "m"}}})(w, r)
		},
	})
	direct := e.create(t, NewProvider{Name: "direct", Kind: KindDirect, BaseURL: f.URL + "/v1"})
	m := NewMonitor(e.reg, e.pr, e.bus, MonitorOptions{})
	if m.o.Interval != 30*time.Second || m.o.CloudInterval != 15*time.Minute || m.o.CatalogEvery != 24*time.Hour {
		t.Fatalf("default intervals %+v", m.o)
	}
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	m.jitter = func(time.Duration) time.Duration { return 0 }
	// the first pass reads the new cloud provider's catalog in full (it has none yet)
	m.CheckAll(e.ctx)
	if listings.Load() != 1 {
		t.Fatalf("listing requests after the first pass: %d", listings.Load())
	}
	if ms, _ := e.reg.Models(e.ctx, direct.ID); len(ms) != 1 {
		t.Fatalf("catalog after the first pass: %v", ms)
	}
	if st, _ := m.Status(e.ctx, direct.ID); st.Health != HealthUp {
		t.Fatalf("a cloud provider that answered is up: %+v", st)
	}
	// a host tick later the cloud provider is not asked again
	now = now.Add(time.Minute)
	m.CheckAll(e.ctx)
	if listings.Load() != 1 {
		t.Fatal("a cloud provider was asked again before its interval")
	}
	// fifteen minutes on it is (one listing request)
	now = now.Add(15 * time.Minute)
	m.CheckAll(e.ctx)
	if listings.Load() != 2 {
		t.Fatalf("listing requests after the cloud interval: %d", listings.Load())
	}
	disabled := e.create(t, NewProvider{Name: "off", Kind: KindHost, BaseURL: f.URL + "/v1", Disabled: true})
	m.CheckAll(e.ctx)
	if hits.Load() != 0 {
		t.Fatal("a disabled host was checked")
	}
	if _, err := m.Acquire(e.ctx, disabled.ID); err != ErrProviderDisabled {
		t.Fatalf("acquire on disabled: %v", err)
	}
}

func TestHealthLoopHonoursRetryAfterAndDoesNotCountA429AsAFailure(t *testing.T) {
	e := newEnv(t)
	var listings atomic.Int32
	var limited atomic.Bool
	f := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/v1/models": func(w http.ResponseWriter, r *http.Request) {
			listings.Add(1)
			if limited.Load() {
				w.Header().Set("Retry-After", "3600")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			jsonBody(map[string]any{"data": []map[string]any{{"id": "m"}}})(w, r)
		},
	})
	p := e.create(t, NewProvider{Name: "agg", Kind: KindAggregator, BaseURL: f.URL + "/v1"})
	m := NewMonitor(e.reg, e.pr, e.bus, MonitorOptions{})
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	m.jitter = func(time.Duration) time.Duration { return 0 }
	m.CheckAll(e.ctx) // the catalog read
	limited.Store(true)
	now = now.Add(16 * time.Minute)
	m.CheckAll(e.ctx) // a 429 with an hour to wait
	if listings.Load() != 2 {
		t.Fatalf("listings %d", listings.Load())
	}
	if st, _ := m.Status(e.ctx, p.ID); st.Health != HealthUp || !st.Available {
		t.Fatalf("a 429 made the provider %+v", st)
	}
	now = now.Add(30 * time.Minute) // past the cloud interval, inside Retry-After
	m.CheckAll(e.ctx)
	if listings.Load() != 2 {
		t.Fatal("the loop asked again before Retry-After")
	}
	now = now.Add(31 * time.Minute)
	m.CheckAll(e.ctx)
	if listings.Load() != 3 {
		t.Fatalf("the loop did not ask after Retry-After: %d", listings.Load())
	}
}

func TestHealthLoopRefreshesCatalogsDailyKeepingUserPrices(t *testing.T) {
	e := newEnv(t)
	var price atomic.Int32
	price.Store(1)
	listing := func(w http.ResponseWriter, r *http.Request) {
		p := fmt.Sprintf("%d", price.Load())
		jsonBody(map[string]any{"data": []map[string]any{
			{"id": "a/one", "context_length": 1000, "pricing": map[string]any{"prompt": "0.00000" + p, "completion": "0.00000" + p}},
			{"id": "a/two", "context_length": 1000, "pricing": map[string]any{"prompt": "0.00000" + p, "completion": "0.00000" + p}},
		}})(w, r)
	}
	f := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/api/v1/models":      listing, // the health check
		"/api/v1/models/user": listing, // the catalog read
		"/api/v1/key":         jsonBody(map[string]any{"data": map[string]any{"label": "k"}}),
	})
	p := e.create(t, NewProvider{Name: "or", Kind: KindAggregator, BaseURL: f.URL + "/api/v1", Flavor: FlavorOpenRouter})
	m := NewMonitor(e.reg, e.pr, e.bus, MonitorOptions{})
	now := time.Now().UTC()
	m.now = func() time.Time { return now }
	m.jitter = func(time.Duration) time.Duration { return 0 }
	m.CheckAll(e.ctx)
	if err := e.reg.SetUserPrice(e.ctx, p.ID, "a/two", 9, 9, 0, 0); err != nil {
		t.Fatal(err)
	}
	refreshed := len(e.events(t, "provider.catalog_refreshed"))
	if refreshed != 1 {
		t.Fatalf("catalog refreshes after the first pass: %d", refreshed)
	}
	price.Store(2)
	now = now.Add(23 * time.Hour)
	m.CheckAll(e.ctx) // a plain check, not a catalog read
	if n := len(e.events(t, "provider.catalog_refreshed")); n != 1 {
		t.Fatalf("the catalog was read again within the day: %d", n)
	}
	now = now.Add(2 * time.Hour)
	m.CheckAll(e.ctx)
	if n := len(e.events(t, "provider.catalog_refreshed")); n != 2 {
		t.Fatalf("the catalog was not read again after a day: %d", n)
	}
	ms, _ := e.reg.Models(e.ctx, p.ID)
	if len(ms) != 2 || ms[0].InputPerM != 2 || ms[1].InputPerM != 9 || ms[1].PriceSource != "user" {
		t.Fatalf("catalog after the daily read %+v", ms)
	}
}

func TestMonitorRefreshProbesANewProviderOnce(t *testing.T) {
	e := newEnv(t)
	f := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/v1/models": jsonBody(map[string]any{"data": []map[string]any{{"id": "m"}}}),
	})
	m := NewMonitor(e.reg, e.pr, e.bus, MonitorOptions{Interval: time.Hour})
	m.Refresh("not-started") // nothing runs while the loop is stopped
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	m.Start(ctx)
	p := e.create(t, NewProvider{Name: "new", Kind: KindDirect, BaseURL: f.URL + "/v1"})
	m.Refresh(p.ID)
	deadline := time.Now().Add(5 * time.Second)
	for len(e.events(t, "provider.probe_completed")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	m.Stop() // waits for the refresh
	if got := f.seen("/v1/models"); len(got) != 1 {
		t.Fatalf("listing requests %d", len(got))
	}
	if g, _ := e.reg.Get(e.ctx, p.ID); g.Status != string(HealthUp) {
		t.Fatalf("status after the refresh %q", g.Status)
	}
	if st, _ := m.Status(e.ctx, p.ID); st.Health != HealthUp {
		t.Fatalf("monitor state %+v", st)
	}
}

func TestHealthSlowHostIsDegraded(t *testing.T) {
	e := newEnv(t)
	f := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/health": func(w http.ResponseWriter, r *http.Request) { time.Sleep(60 * time.Millisecond) },
	})
	p := e.create(t, NewProvider{Name: "slow", Kind: KindHost, BaseURL: f.URL + "/v1"})
	m := NewMonitor(e.reg, e.pr, e.bus, MonitorOptions{SlowAfter: 20 * time.Millisecond})
	ev, err := m.CheckNow(e.ctx, p.ID)
	if err != nil || ev.To != HealthDegraded {
		t.Fatalf("%v %+v", err, ev)
	}
	if st, _ := m.Status(e.ctx, p.ID); !st.Available {
		t.Fatal("a degraded host is still available")
	}
}

func TestAcquireEnforcesHostConcurrency(t *testing.T) {
	e := newEnv(t)
	p := e.create(t, NewProvider{Name: "gpu", Kind: KindHost, BaseURL: "http://127.0.0.1:1/v1", MaxConcurrent: 2})
	m := NewMonitor(e.reg, e.pr, e.bus, MonitorOptions{})
	r1, err := m.Acquire(e.ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := m.Acquire(e.ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := m.TryAcquire(e.ctx, p.ID); ok {
		t.Fatal("third TryAcquire must fail at max_concurrent=2")
	}
	st, _ := m.Status(e.ctx, p.ID)
	if st.InUse != 2 || st.MaxConcurrent != 2 {
		t.Fatalf("status %+v", st)
	}
	// A third caller blocks until a slot is released.
	got := make(chan func(), 1)
	go func() {
		r, err := m.Acquire(e.ctx, p.ID)
		if err != nil {
			t.Error(err)
			return
		}
		got <- r
	}()
	select {
	case <-got:
		t.Fatal("third Acquire did not wait")
	case <-time.After(80 * time.Millisecond):
	}
	r1()
	r1() // release is idempotent: a double release must not free a second slot
	var r3 func()
	select {
	case r3 = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("third Acquire not woken by release")
	}
	if _, ok, _ := m.TryAcquire(e.ctx, p.ID); ok {
		t.Fatal("double release freed an extra slot")
	}
	// Cancellation while waiting.
	cctx, cancel := context.WithTimeout(e.ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := m.Acquire(cctx, p.ID); err != context.DeadlineExceeded {
		t.Fatalf("waiting acquire on cancelled context: %v", err)
	}
	r2()
	r3()
	if st, _ := m.Status(e.ctx, p.ID); st.InUse != 0 {
		t.Fatalf("slots leaked: %+v", st)
	}
}

func TestAcquireNeverExceedsLimitUnderContention(t *testing.T) {
	e := newEnv(t)
	p := e.create(t, NewProvider{Name: "gpu", Kind: KindHost, BaseURL: "http://127.0.0.1:1/v1", MaxConcurrent: 3})
	m := NewMonitor(e.reg, e.pr, e.bus, MonitorOptions{})
	var cur, peak atomic.Int32
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			rel, err := m.Acquire(e.ctx, p.ID)
			if err != nil {
				t.Error(err)
				return
			}
			n := cur.Add(1)
			for {
				old := peak.Load()
				if n <= old || peak.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			cur.Add(-1)
			rel()
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
	if peak.Load() > 3 || peak.Load() == 0 {
		t.Fatalf("peak concurrency %d with limit 3", peak.Load())
	}
}

func TestAcquireUnlimitedWhenZero(t *testing.T) {
	e := newEnv(t)
	p := e.create(t, NewProvider{Name: "api", Kind: KindDirect, BaseURL: "https://api.example.com/v1"})
	m := NewMonitor(e.reg, e.pr, e.bus, MonitorOptions{})
	for i := 0; i < 50; i++ {
		if _, err := m.Acquire(e.ctx, p.ID); err != nil {
			t.Fatal(err)
		}
	}
}

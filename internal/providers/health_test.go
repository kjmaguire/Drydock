package providers

import (
	"context"
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

func TestHealthLoopOnlyChecksHostsAndDefaultsToThirtySeconds(t *testing.T) {
	e := newEnv(t)
	var hits atomic.Int32
	f := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/health": func(w http.ResponseWriter, r *http.Request) { hits.Add(1) },
	})
	e.create(t, NewProvider{Name: "direct", Kind: KindDirect, BaseURL: f.URL + "/v1"})
	m := NewMonitor(e.reg, e.pr, e.bus, MonitorOptions{})
	if m.o.Interval != 30*time.Second {
		t.Fatalf("default interval %s", m.o.Interval)
	}
	m.CheckAll(e.ctx)
	if hits.Load() != 0 {
		t.Fatal("a non-host provider was health-checked by the host loop")
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

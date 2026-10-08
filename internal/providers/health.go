package providers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
)

// HealthEvent is a health transition, delivered to Subscribe channels and published as provider.up, provider.down
// or provider.degraded.
type HealthEvent struct {
	ProviderID string
	From, To   Health
	At         time.Time
	Latency    time.Duration
	Detail     string
}

// MonitorOptions configures the host health loop.
type MonitorOptions struct {
	Interval        time.Duration // default 30s (docs/model-routing.md, brief section 6)
	DownAfter       int           // consecutive failures before "down" (default 2); the first failure is "degraded"
	SlowAfter       time.Duration // a live host slower than this is "degraded" (default 5s)
	Ledger          *QuotaLedger  // optional: Status reports quota exhaustion
	SubscriberQueue int           // per-subscriber channel size (default 64)
}

// Monitor runs the health loop for host providers, hands out host concurrency slots and reports provider status.
type Monitor struct {
	reg    *Registry
	pr     *Prober
	bus    events.Bus
	o      MonitorOptions
	mu     sync.Mutex
	hosts  map[string]*hostState
	subs   map[chan HealthEvent]struct{}
	cancel context.CancelFunc
	done   chan struct{}
}

type hostState struct {
	health    Health
	since     time.Time
	lastCheck time.Time
	latency   time.Duration
	lastErr   string
	failures  int
	inUse     int
	freed     chan struct{} // closed and replaced whenever a slot is released or the limit may have changed
}

// NewMonitor builds a Monitor. bus may be nil.
func NewMonitor(reg *Registry, pr *Prober, bus events.Bus, o MonitorOptions) *Monitor {
	if o.Interval <= 0 {
		o.Interval = 30 * time.Second
	}
	if o.DownAfter <= 0 {
		o.DownAfter = 2
	}
	if o.SlowAfter <= 0 {
		o.SlowAfter = 5 * time.Second
	}
	if o.SubscriberQueue <= 0 {
		o.SubscriberQueue = 64
	}
	return &Monitor{reg: reg, pr: pr, bus: bus, o: o, hosts: map[string]*hostState{}, subs: map[chan HealthEvent]struct{}{}}
}

func (m *Monitor) state(id string) *hostState {
	h, ok := m.hosts[id]
	if !ok {
		h = &hostState{health: HealthUnknown, freed: make(chan struct{})}
		m.hosts[id] = h
	}
	return h
}

// Start runs the loop until ctx ends or Stop is called: one pass immediately, then every Interval.
func (m *Monitor) Start(ctx context.Context) {
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return
	}
	ctx, m.cancel = context.WithCancel(ctx)
	m.done = make(chan struct{})
	m.mu.Unlock()
	go func() {
		defer close(m.done)
		t := time.NewTicker(m.o.Interval)
		defer t.Stop()
		m.CheckAll(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.CheckAll(ctx)
			}
		}
	}()
}

// Stop ends the loop and waits for it.
func (m *Monitor) Stop() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// CheckAll checks every enabled host provider once, concurrently.
func (m *Monitor) CheckAll(ctx context.Context) {
	ps, err := m.reg.List(ctx)
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	for _, p := range ps {
		if p.Kind != KindHost || !p.Enabled {
			continue
		}
		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()
			m.check(ctx, p)
		}(p)
	}
	wg.Wait()
}

// CheckNow checks one provider of any kind immediately and returns the transition (To equals the new state).
func (m *Monitor) CheckNow(ctx context.Context, id string) (HealthEvent, error) {
	p, err := m.reg.Get(ctx, id)
	if err != nil {
		return HealthEvent{}, err
	}
	return m.check(ctx, p), nil
}

func (m *Monitor) check(ctx context.Context, p Provider) HealthEvent {
	r := m.pr.Check(ctx, p)
	now := time.Now().UTC()
	m.mu.Lock()
	h := m.state(p.ID)
	from := h.health
	h.lastCheck, h.latency = now, r.Latency
	switch {
	case r.OK && r.Latency <= m.o.SlowAfter:
		h.failures, h.lastErr, h.health = 0, "", HealthUp
	case r.OK:
		h.failures, h.lastErr, h.health = 0, fmt.Sprintf("slow: %s", r.Latency.Round(time.Millisecond)), HealthDegraded
	default:
		h.failures++
		h.lastErr = r.Detail
		if h.failures >= m.o.DownAfter {
			h.health = HealthDown
		} else if from == HealthUp || from == HealthDegraded || from == HealthUnknown {
			h.health = HealthDegraded
		}
	}
	to := h.health
	if to != from {
		h.since = now
	}
	ev := HealthEvent{ProviderID: p.ID, From: from, To: to, At: now, Latency: r.Latency, Detail: h.lastErr}
	m.mu.Unlock()
	if to != from {
		// Persist and publish before notifying subscribers, so a subscriber that reacts to the channel event can already
		// replay it from the bus.
		_ = m.reg.setStatus(ctx, p.ID, to, now, "", nil)
		if m.bus != nil {
			_, _ = events.Emit(ctx, m.bus, "provider."+string(to), events.Event{}, map[string]any{
				"providerId": p.ID, "from": from, "latencyMs": r.Latency.Milliseconds(), "detail": ev.Detail})
		}
		m.mu.Lock()
		for c := range m.subs {
			select {
			case c <- ev:
			default: // a slow subscriber loses events rather than stalling the loop
			}
		}
		m.mu.Unlock()
	}
	return ev
}

// Subscribe returns a channel of health transitions and a cancel function.
func (m *Monitor) Subscribe() (<-chan HealthEvent, func()) {
	c := make(chan HealthEvent, m.o.SubscriberQueue)
	m.mu.Lock()
	m.subs[c] = struct{}{}
	m.mu.Unlock()
	return c, func() {
		m.mu.Lock()
		if _, ok := m.subs[c]; ok {
			delete(m.subs, c)
			close(c)
		}
		m.mu.Unlock()
	}
}

// ErrProviderDisabled is returned by Acquire for a disabled provider.
var ErrProviderDisabled = errors.New("providers: provider is disabled")

// Acquire takes one concurrency slot on the provider, waiting until one is free or ctx ends. MaxConcurrent 0 is
// unlimited. The returned release function is idempotent. Hosts must never exceed max_concurrent_agents
// (docs/model-routing.md s7 rule 5).
func (m *Monitor) Acquire(ctx context.Context, id string) (release func(), err error) {
	for {
		p, err := m.reg.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if !p.Enabled {
			return nil, ErrProviderDisabled
		}
		m.mu.Lock()
		h := m.state(id)
		if p.MaxConcurrent == 0 || h.inUse < p.MaxConcurrent {
			h.inUse++
			m.mu.Unlock()
			var once sync.Once
			return func() { once.Do(func() { m.release(id) }) }, nil
		}
		wait := h.freed
		m.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// TryAcquire is Acquire without waiting: ok is false when no slot is free.
func (m *Monitor) TryAcquire(ctx context.Context, id string) (release func(), ok bool, err error) {
	p, err := m.reg.Get(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if !p.Enabled {
		return nil, false, ErrProviderDisabled
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.state(id)
	if p.MaxConcurrent != 0 && h.inUse >= p.MaxConcurrent {
		return nil, false, nil
	}
	h.inUse++
	var once sync.Once
	return func() { once.Do(func() { m.release(id) }) }, true, nil
}

func (m *Monitor) release(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.state(id)
	if h.inUse > 0 {
		h.inUse--
	}
	close(h.freed)
	h.freed = make(chan struct{})
}

// Status is a provider's current state for the router and the Providers page.
type Status struct {
	ProviderID     string
	Enabled        bool
	Health         Health
	Since          time.Time
	LastCheck      time.Time
	Latency        time.Duration
	LastError      string
	LANUnencrypted bool
	InUse          int
	MaxConcurrent  int
	QuotaExhausted bool
	QuotaRetryAt   time.Time
	// Available is true when the provider is enabled, not down and not quota-exhausted: a candidate the router may use.
	Available bool
}

// Status reports the provider's state. A provider that was never checked is "unknown" and counts as available
// (non-host providers are not health-checked by the loop).
func (m *Monitor) Status(ctx context.Context, id string) (Status, error) {
	p, err := m.reg.Get(ctx, id)
	if err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	h := m.state(id)
	st := Status{ProviderID: id, Enabled: p.Enabled, Health: h.health, Since: h.since, LastCheck: h.lastCheck, Latency: h.latency,
		LastError: h.lastErr, LANUnencrypted: p.LANUnencrypted, InUse: h.inUse, MaxConcurrent: p.MaxConcurrent}
	m.mu.Unlock()
	if m.o.Ledger != nil {
		if until, bad := m.o.Ledger.Unavailable(ctx, id); bad {
			st.QuotaExhausted, st.QuotaRetryAt = true, until
		}
	}
	st.Available = st.Enabled && st.Health != HealthDown && !st.QuotaExhausted
	return st, nil
}

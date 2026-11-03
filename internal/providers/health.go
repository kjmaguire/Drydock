package providers

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/safe"
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

// MonitorOptions configures the health loop.
type MonitorOptions struct {
	Interval time.Duration // the loop's tick and a host's check interval: default 30s (docs/model-routing.md, brief section 6)
	// CloudInterval is how often a cloud provider (direct or aggregator) is checked: one model listing request with the
	// person's key, never a prompt. Default 15 minutes; a 429 answer's Retry-After pushes the next check further out.
	CloudInterval time.Duration
	// CatalogEvery is how often a cloud provider's catalog and prices are read again (a full Probe; prices the person set
	// are kept), plus a random wait of up to CatalogJitter so providers are not all read at once. Defaults 24h and 2h
	// (decision D4).
	CatalogEvery    time.Duration
	CatalogJitter   time.Duration
	DownAfter       int           // consecutive failures before "down" (default 2); the first failure is "degraded"
	SlowAfter       time.Duration // a live host slower than this is "degraded" (default 5s)
	Ledger          *QuotaLedger  // optional: Status reports quota exhaustion
	SubscriberQueue int           // per-subscriber channel size (default 64)
}

// Monitor runs the health loop for every enabled provider (hosts every Interval, cloud providers every CloudInterval, their
// catalogs once a day), hands out host concurrency slots and reports provider status.
type Monitor struct {
	reg    *Registry
	pr     *Prober
	bus    events.Bus
	o      MonitorOptions
	mu     sync.Mutex
	hosts  map[string]*hostState
	subs   map[chan HealthEvent]struct{}
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	extra  sync.WaitGroup // Refresh calls in flight; Stop waits for them
	now    func() time.Time
	jitter func(max time.Duration) time.Duration
	onPass func() // tests: called after each pass of the loop
}

func (m *Monitor) passed() {
	if m.onPass != nil {
		m.onPass()
	}
}

type hostState struct {
	health    Health
	since     time.Time
	lastCheck time.Time
	latency   time.Duration
	lastErr   string
	failures  int
	next      time.Time // the loop checks the provider again from this time on (zero: at the next pass)
	catalog   time.Time // a cloud provider's catalog is read again from this time on (zero: not worked out yet)
	inUse     int
	// busy is non-nil while a check or a full read of the provider is in flight (closed when it ends): the loop skips the
	// provider then, and Refresh waits for it, so the same provider is never read twice at the same time.
	busy  chan struct{}
	reads int           // full reads (Probe) finished so far: Refresh skips its own read when one finished while it waited
	freed chan struct{} // closed and replaced whenever a slot is released or the limit may have changed
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
	if o.CloudInterval <= 0 {
		o.CloudInterval = 15 * time.Minute
	}
	if o.CatalogEvery <= 0 {
		o.CatalogEvery = 24 * time.Hour
	}
	if o.CatalogJitter <= 0 {
		o.CatalogJitter = 2 * time.Hour
	}
	return &Monitor{reg: reg, pr: pr, bus: bus, o: o, hosts: map[string]*hostState{}, subs: map[chan HealthEvent]struct{}{},
		now: func() time.Time { return time.Now().UTC() },
		jitter: func(max time.Duration) time.Duration {
			if max <= 0 {
				return 0
			}
			return rand.N(max)
		}}
}

// interval is how long the loop waits between two checks of p.
func (m *Monitor) interval(p Provider) time.Duration {
	if p.Kind == KindHost {
		return m.o.Interval
	}
	return m.o.CloudInterval
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
	m.ctx = ctx
	m.done = make(chan struct{})
	m.mu.Unlock()
	safe.Go("providers.Monitor.Start", func() {
		defer close(m.done)
		t := time.NewTicker(m.o.Interval)
		defer t.Stop()
		m.CheckAll(ctx)
		m.passed()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.CheckAll(ctx)
				m.passed()
			}
		}
	})
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
	m.extra.Wait()
}

// Refresh reads one provider in full now, in the background (a Probe: its listing, its catalog and prices, its status), as
// after the person saved a new provider. It does nothing while the loop is not running.
func (m *Monitor) Refresh(id string) {
	m.mu.Lock()
	ctx := m.ctx
	if m.cancel == nil || ctx == nil {
		m.mu.Unlock()
		return
	}
	m.extra.Add(1)
	reads := m.state(id).reads
	m.mu.Unlock()
	safe.Go("providers.Monitor.Refresh", func() {
		defer m.extra.Done()
		p, err := m.reg.Get(ctx, id)
		if err != nil || !p.Enabled {
			return
		}
		for {
			m.mu.Lock()
			h := m.state(id)
			if h.reads != reads {
				// the loop read this provider in full while Refresh waited: that read is the one asked for
				m.mu.Unlock()
				return
			}
			wait := h.busy
			if wait == nil {
				h.busy = make(chan struct{})
				m.mu.Unlock()
				break
			}
			m.mu.Unlock()
			select {
			case <-wait:
			case <-ctx.Done():
				return
			}
		}
		defer m.idle(id)
		m.refresh(ctx, p)
	})
}

// idle marks the provider as no longer being checked or read.
func (m *Monitor) idle(id string) {
	m.mu.Lock()
	h := m.state(id)
	if h.busy != nil {
		close(h.busy)
		h.busy = nil
	}
	m.mu.Unlock()
}

// CheckAll checks every enabled provider that is due, concurrently: a host every Interval, a cloud provider every
// CloudInterval (later when it answered 429 with Retry-After). A cloud provider whose catalog is a day old is read in full
// instead (Probe), which also says whether it is up.
func (m *Monitor) CheckAll(ctx context.Context) {
	ps, err := m.reg.List(ctx)
	if err != nil {
		return
	}
	now := m.now()
	var wg sync.WaitGroup
	for _, p := range ps {
		if !p.Enabled {
			continue
		}
		m.mu.Lock()
		h := m.state(p.ID)
		due := !now.Before(h.next) && h.busy == nil // a provider already being read (Refresh, a slow check) is skipped
		if due {
			h.busy = make(chan struct{})
		}
		m.mu.Unlock()
		if !due {
			continue
		}
		wg.Add(1)
		safe.Go("providers.Monitor.CheckAll", func() {
			func(p Provider) {
				defer wg.Done()
				defer m.idle(p.ID)
				if p.Kind != KindHost && m.catalogDue(ctx, p, now) {
					m.refresh(ctx, p)
					return
				}
				m.check(ctx, p)
			}(p)
		})
	}
	wg.Wait()
}

// catalogDue says whether a cloud provider's catalog should be read again: it has none yet, or it is CatalogEvery (plus
// the jitter) old.
func (m *Monitor) catalogDue(ctx context.Context, p Provider, now time.Time) bool {
	m.mu.Lock()
	at := m.state(p.ID).catalog
	m.mu.Unlock()
	if at.IsZero() {
		fetched, err := m.reg.CatalogFetchedAt(ctx, p.ID)
		if err != nil {
			return false
		}
		if !fetched.IsZero() {
			at = fetched.Add(m.o.CatalogEvery + m.jitter(m.o.CatalogJitter))
		} else {
			at = now
		}
		m.mu.Lock()
		m.state(p.ID).catalog = at
		m.mu.Unlock()
	}
	return !now.Before(at)
}

// Test reads one provider in full now and waits for the answer (Check now, Check again): the same Probe as provider.test,
// with its outcome recorded as a check, so the loop's state (what the router uses) and the saved status (what the pages and
// the Inbox show) always agree afterwards. It waits for a check or read of the provider already in flight.
func (m *Monitor) Test(ctx context.Context, id string) (ProbeResult, error) {
	p, err := m.reg.Get(ctx, id)
	if err != nil {
		return ProbeResult{}, err
	}
	for {
		m.mu.Lock()
		h := m.state(id)
		wait := h.busy
		if wait == nil {
			h.busy = make(chan struct{})
			m.mu.Unlock()
			break
		}
		m.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return ProbeResult{}, ctx.Err()
		}
	}
	defer m.idle(id)
	res, err := m.pr.Probe(ctx, id)
	if err != nil {
		return res, err
	}
	m.recordRead(ctx, p, res)
	return res, nil
}

// refresh reads the provider in full (Probe: listing, catalog, prices the person set kept, provider.catalog_refreshed) and
// records the outcome as a check.
func (m *Monitor) refresh(ctx context.Context, p Provider) HealthEvent {
	res, err := m.pr.Probe(ctx, p.ID)
	if err != nil {
		return HealthEvent{}
	}
	return m.recordRead(ctx, p, res)
}

// recordRead records a full read as a check. Probe has already saved its own verdict, so the monitor's state is saved again
// even without a transition: the saved status never stays at what Probe wrote while the monitor thinks otherwise.
func (m *Monitor) recordRead(ctx context.Context, p Provider, res ProbeResult) HealthEvent {
	m.mu.Lock()
	h := m.state(p.ID)
	h.catalog = m.now().Add(m.o.CatalogEvery + m.jitter(m.o.CatalogJitter))
	h.reads++
	m.mu.Unlock()
	return m.recordAs(ctx, p, CheckResult{OK: res.OK, Latency: res.Latency, Detail: res.Error}, true)
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
	return m.record(ctx, p, m.pr.Check(ctx, p))
}

// record turns one check's result into the provider's health, persists and publishes a change, and schedules the next check.
func (m *Monitor) record(ctx context.Context, p Provider, r CheckResult) HealthEvent {
	return m.recordAs(ctx, p, r, false)
}

// recordAs is record; persist saves the state even when it did not change (after a Probe, which saves its own verdict).
func (m *Monitor) recordAs(ctx context.Context, p Provider, r CheckResult, persist bool) HealthEvent {
	now := m.now()
	m.mu.Lock()
	h := m.state(p.ID)
	from := h.health
	h.lastCheck, h.latency = now, r.Latency
	h.next = now.Add(m.interval(p))
	switch {
	case r.Limited:
		// reachable and the key accepted, but asked to wait: not a failure, and nothing is asked before the wait is over
		h.lastErr = r.Detail
		if r.RetryAfter > m.interval(p) {
			h.next = now.Add(r.RetryAfter)
		}
	case r.OK && (p.Kind != KindHost || r.Latency <= m.o.SlowAfter):
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
	if persist && to == from && to != HealthUnknown {
		_ = m.reg.setStatus(ctx, p.ID, to, time.Time{}, "", nil) // last_seen is Probe's to set
	}
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

// Status reports the provider's state. A provider that was never checked is "unknown" and counts as available.
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

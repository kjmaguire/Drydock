package providers

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clock is a settable time source handed to the ledger through QuotaOptions.Now.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newLedger(t *testing.T, e *env, start time.Time) (*QuotaLedger, *clock) {
	t.Helper()
	c := &clock{t: start}
	return NewQuotaLedger(e.reg, e.pr, e.bus, QuotaOptions{Now: c.now}), c
}

var t0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func TestRPMSlidingWindow(t *testing.T) {
	e := newEnv(t)
	p := e.create(t, NewProvider{Name: "p", BaseURL: "https://api.example.com/v1"})
	l, c := newLedger(t, e, t0)
	if err := l.Configure(e.ctx, Window{ProviderID: p.ID, Kind: WindowRPM, Limit: 20}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		ok, _, err := l.Reserve(e.ctx, p.ID, "m")
		if err != nil || !ok {
			t.Fatalf("reserve %d: ok=%v err=%v", i, ok, err)
		}
		c.add(time.Second) // the 20 requests spread over 20 seconds
	}
	ok, retry, _ := l.Reserve(e.ctx, p.ID, "m")
	if ok {
		t.Fatal("21st request in a minute was allowed")
	}
	// The earliest request (t0) leaves the window at t0+60s: the sliding window frees one slot, not all of them.
	if !retry.Equal(t0.Add(time.Minute)) {
		t.Fatalf("retryAt = %s, want %s", retry, t0.Add(time.Minute))
	}
	c.add(40*time.Second + time.Millisecond) // now t0+60s+1ms
	if ok, _, _ := l.Reserve(e.ctx, p.ID, "m"); !ok {
		t.Fatal("slot not freed when the oldest request aged out")
	}
	if ok, _, _ := l.Reserve(e.ctx, p.ID, "m"); ok {
		t.Fatal("only one slot should have been freed (sliding, not fixed, window)")
	}
	c.add(2 * time.Minute)
	for i := 0; i < 20; i++ {
		if ok, _, _ := l.Reserve(e.ctx, p.ID, "m"); !ok {
			t.Fatalf("window did not fully clear (request %d)", i)
		}
	}
}

func TestDailyWindowResetsAtMidnightInConfiguredZone(t *testing.T) {
	e := newEnv(t)
	p := e.create(t, NewProvider{Name: "g", BaseURL: "https://api.example.com/v1"})
	// 23:30 on 7 Oct in Los Angeles (PDT, UTC-7) is 06:30 UTC on 8 Oct; midnight Pacific is 07:00 UTC.
	l, c := newLedger(t, e, time.Date(2026, 10, 8, 6, 30, 0, 0, time.UTC))
	if err := l.Configure(e.ctx, Window{ProviderID: p.ID, Model: "gemini-2.5-pro", Kind: WindowRPD, Limit: 3, ResetTZ: "America/Los_Angeles"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if ok, _, _ := l.Reserve(e.ctx, p.ID, "gemini-2.5-pro"); !ok {
			t.Fatalf("request %d refused", i)
		}
	}
	ok, retry, _ := l.Reserve(e.ctx, p.ID, "gemini-2.5-pro")
	if ok || !retry.Equal(time.Date(2026, 10, 8, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("ok=%v retryAt=%s, want midnight Pacific = 07:00Z", ok, retry)
	}
	// Another model on the same provider is not covered by the per-model window.
	if ok, _, _ := l.Reserve(e.ctx, p.ID, "gemini-2.5-flash"); !ok {
		t.Fatal("a window scoped to one model blocked another")
	}
	c.add(31 * time.Minute)
	if ok, _, _ := l.Reserve(e.ctx, p.ID, "gemini-2.5-pro"); !ok {
		t.Fatal("daily window did not reset at midnight Pacific")
	}
}

func TestExhaustedWindowMakesProviderUnavailableAndEmitsOnce(t *testing.T) {
	e := newEnv(t)
	p := e.create(t, NewProvider{Name: "p", BaseURL: "https://api.example.com/v1"})
	l, c := newLedger(t, e, t0)
	l.Configure(e.ctx, Window{ProviderID: p.ID, Kind: WindowRPD, Limit: 2, ResetTZ: "UTC"})
	if _, bad := l.Unavailable(e.ctx, p.ID); bad {
		t.Fatal("unavailable before use")
	}
	l.Reserve(e.ctx, p.ID, "m")
	l.Reserve(e.ctx, p.ID, "m")
	// Hammering an exhausted provider must not spam events.
	for i := 0; i < 5; i++ {
		if ok, _, _ := l.Reserve(e.ctx, p.ID, "m"); ok {
			t.Fatal("reserve succeeded on an exhausted window")
		}
	}
	until, bad := l.Unavailable(e.ctx, p.ID)
	if !bad || !until.Equal(time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unavailable=%v until=%s", bad, until)
	}
	evs := e.events(t, "provider.quota_exhausted")
	if len(evs) != 1 {
		t.Fatalf("%d quota_exhausted events, want exactly 1", len(evs))
	}
	var d map[string]any
	json.Unmarshal(evs[0].Data, &d)
	if d["providerId"] != p.ID || d["window"] != "rpd" || d["source"] != "configured" {
		t.Fatalf("event data %v", d)
	}
	// Check does not consume and Status surfaces the exhaustion.
	m := NewMonitor(e.reg, e.pr, e.bus, MonitorOptions{Ledger: l})
	if st, _ := m.Status(e.ctx, p.ID); !st.QuotaExhausted || st.Available || !st.QuotaRetryAt.Equal(until) {
		t.Fatalf("status %+v", st)
	}
	// After the reset the provider is back, and a second exhaustion emits again.
	c.add(13 * time.Hour)
	if _, bad := l.Unavailable(e.ctx, p.ID); bad {
		t.Fatal("still unavailable after reset")
	}
	l.Reserve(e.ctx, p.ID, "m")
	l.Reserve(e.ctx, p.ID, "m")
	l.Reserve(e.ctx, p.ID, "m")
	if n := len(e.events(t, "provider.quota_exhausted")); n != 2 {
		t.Fatalf("%d events after second exhaustion, want 2", n)
	}
}

func TestCheckDoesNotConsume(t *testing.T) {
	e := newEnv(t)
	p := e.create(t, NewProvider{Name: "p", BaseURL: "https://api.example.com/v1"})
	l, _ := newLedger(t, e, t0)
	l.Configure(e.ctx, Window{ProviderID: p.ID, Kind: WindowRPD, Limit: 1})
	for i := 0; i < 5; i++ {
		if ok, _ := l.Check(e.ctx, p.ID, "m"); !ok {
			t.Fatal("check consumed the quota")
		}
	}
	if ok, _, _ := l.Reserve(e.ctx, p.ID, "m"); !ok {
		t.Fatal("reserve refused")
	}
	if ok, _ := l.Check(e.ctx, p.ID, "m"); ok {
		t.Fatal("check passed on an exhausted window")
	}
}

func TestReleaseGivesTheSlotBack(t *testing.T) {
	e := newEnv(t)
	p := e.create(t, NewProvider{Name: "p", BaseURL: "https://api.example.com/v1"})
	l, _ := newLedger(t, e, t0)
	l.Configure(e.ctx, Window{ProviderID: p.ID, Kind: WindowRPM, Limit: 1})
	l.Configure(e.ctx, Window{ProviderID: p.ID, Kind: WindowRPD, Limit: 1})
	if ok, _, _ := l.Reserve(e.ctx, p.ID, "m"); !ok {
		t.Fatal()
	}
	l.Release(e.ctx, p.ID, "m")
	if ok, _, _ := l.Reserve(e.ctx, p.ID, "m"); !ok {
		t.Fatal("released slot not reusable")
	}
}

func TestTokenWindowsAreFedByRecord(t *testing.T) {
	e := newEnv(t)
	p := e.create(t, NewProvider{Name: "p", BaseURL: "https://api.example.com/v1"})
	l, c := newLedger(t, e, t0)
	l.Configure(e.ctx, Window{ProviderID: p.ID, Kind: WindowTPM, Limit: 1000})
	l.Configure(e.ctx, Window{ProviderID: p.ID, Kind: WindowTPD, Limit: 5000})
	if ok, _, _ := l.Reserve(e.ctx, p.ID, "m"); !ok {
		t.Fatal()
	}
	if err := l.Record(e.ctx, p.ID, "m", Usage{Input: 700, Output: 200}, nil); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := l.Reserve(e.ctx, p.ID, "m"); !ok {
		t.Fatal("900 of 1000 tokens used, a request must still be admitted")
	}
	l.Record(e.ctx, p.ID, "m", Usage{Input: 100, Output: 50}, nil) // 1050 >= 1000
	ok, retry, _ := l.Reserve(e.ctx, p.ID, "m")
	if ok || !retry.After(c.now()) || retry.After(c.now().Add(time.Minute)) {
		t.Fatalf("tokens-per-minute not enforced: ok=%v retry=%s", ok, retry)
	}
	c.add(61 * time.Second)
	if ok, _, _ := l.Reserve(e.ctx, p.ID, "m"); !ok {
		t.Fatal("minute token window did not slide out")
	}
	snap, _ := l.Snapshot(e.ctx, p.ID)
	var tpd Window
	for _, w := range snap {
		if w.Kind == WindowTPD {
			tpd = w
		}
	}
	if tpd.Used != 1050 {
		t.Fatalf("daily token window used %v, want 1050", tpd.Used)
	}
}

func TestRecordLearnsAnthropicHeaders(t *testing.T) {
	e := newEnv(t)
	p := e.create(t, NewProvider{Name: "ant", BaseURL: "https://api.anthropic.com", Tier: TierPremium})
	l, c := newLedger(t, e, t0)
	reset := t0.Add(30 * time.Second).Format(time.RFC3339)
	h := http.Header{}
	h.Set("anthropic-ratelimit-requests-limit", "50")
	h.Set("anthropic-ratelimit-requests-remaining", "0")
	h.Set("anthropic-ratelimit-requests-reset", reset)
	h.Set("anthropic-ratelimit-input-tokens-limit", "30000")
	h.Set("anthropic-ratelimit-input-tokens-remaining", "29000")
	h.Set("anthropic-ratelimit-input-tokens-reset", reset)
	if err := l.Record(e.ctx, p.ID, "claude-x", Usage{Input: 1000, Output: 10}, h); err != nil {
		t.Fatal(err)
	}
	snap, _ := l.Snapshot(e.ctx, p.ID)
	if len(snap) != 2 {
		t.Fatalf("windows %+v", snap)
	}
	for _, w := range snap {
		if w.Source != SourceHeaders || w.Model != "claude-x" {
			t.Fatalf("window %+v", w)
		}
	}
	// requests-remaining 0 -> the next request is refused until the header's reset time, for that model only.
	ok, retry, _ := l.Reserve(e.ctx, p.ID, "claude-x")
	if ok || !retry.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("ok=%v retry=%s", ok, retry)
	}
	if ok, _, _ := l.Reserve(e.ctx, p.ID, "claude-other"); !ok {
		t.Fatal("a per-model header window blocked another model")
	}
	if n := len(e.events(t, "provider.quota_exhausted")); n < 1 {
		t.Fatal("no quota_exhausted event from an authoritative zero-remaining header")
	}
	c.add(31 * time.Second)
	if ok, _, _ := l.Reserve(e.ctx, p.ID, "claude-x"); !ok {
		t.Fatal("window did not reset at the header's reset time")
	}
}

func TestParseOpenAIStyleRateLimitHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("x-ratelimit-limit-requests", "500")
	h.Set("x-ratelimit-remaining-requests", "499")
	h.Set("x-ratelimit-reset-requests", "6m0s")
	h.Set("x-ratelimit-limit-tokens", "30000")
	h.Set("x-ratelimit-remaining-tokens", "garbage") // odd values are skipped
	ws := ParseRateLimitHeaders(h, t0)
	if len(ws) != 1 || ws[0].Kind != WindowRPM || ws[0].Limit != 500 || ws[0].Remaining != 499 || !ws[0].ResetAt.Equal(t0.Add(6*time.Minute)) {
		t.Fatalf("%+v", ws)
	}
	if len(ParseRateLimitHeaders(http.Header{}, t0)) != 0 {
		t.Fatal("empty headers must yield nothing (OpenRouter sends none on success)")
	}
}

func TestRetryAfterParsing(t *testing.T) {
	cases := []struct {
		name string
		h    map[string]string
		want time.Duration
		ok   bool
	}{
		{"seconds", map[string]string{"Retry-After": "3"}, 3 * time.Second, true},
		{"fractional", map[string]string{"Retry-After": "0.5"}, 500 * time.Millisecond, true},
		{"ms header wins", map[string]string{"Retry-After": "9", "retry-after-ms": "250"}, 250 * time.Millisecond, true},
		{"http date", map[string]string{"Retry-After": t0.Add(10 * time.Second).UTC().Format(http.TimeFormat)}, 10 * time.Second, true},
		{"openrouter reset epoch ms", map[string]string{"X-RateLimit-Reset": "1791460812000"}, 12 * time.Second, true},
		{"none", map[string]string{"X-RateLimit-Reset": "42"}, 0, false},
		{"garbage", map[string]string{"Retry-After": "soon"}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := http.Header{}
			for k, v := range c.h {
				h.Set(k, v)
			}
			now := t0
			if c.name == "openrouter reset epoch ms" {
				now = time.UnixMilli(1791460800000)
			}
			d, ok := RetryAfter(h, now)
			if ok != c.ok || d != c.want {
				t.Fatalf("got %s %v, want %s %v", d, ok, c.want, c.ok)
			}
		})
	}
}

func TestPenalizeBlocksUntilTheRetryTime(t *testing.T) {
	e := newEnv(t)
	p := e.create(t, NewProvider{Name: "p", BaseURL: "https://api.example.com/v1"})
	l, c := newLedger(t, e, t0)
	l.Penalize(e.ctx, p.ID, "slow-model", t0.Add(20*time.Second), "retry-after")
	ok, retry, _ := l.Reserve(e.ctx, p.ID, "slow-model")
	if ok || !retry.Equal(t0.Add(20*time.Second)) {
		t.Fatalf("ok=%v retry=%s", ok, retry)
	}
	if ok, _, _ := l.Reserve(e.ctx, p.ID, "other"); !ok {
		t.Fatal("model-scoped block affected another model")
	}
	if _, bad := l.Unavailable(e.ctx, p.ID); bad {
		t.Fatal("a model-scoped block made the whole provider unavailable")
	}
	l.Penalize(e.ctx, p.ID, "", t0.Add(time.Minute), "overloaded")
	if until, bad := l.Unavailable(e.ctx, p.ID); !bad || !until.Equal(t0.Add(time.Minute)) {
		t.Fatalf("provider-wide block: %v %s", bad, until)
	}
	c.add(2 * time.Minute)
	if ok, _, _ := l.Reserve(e.ctx, p.ID, "slow-model"); !ok {
		t.Fatal("block did not expire")
	}
}

func TestLedgerSurvivesRestart(t *testing.T) {
	e := newEnv(t)
	p := e.create(t, NewProvider{Name: "p", BaseURL: "https://api.example.com/v1"})
	l, c := newLedger(t, e, t0)
	l.Configure(e.ctx, Window{ProviderID: p.ID, Kind: WindowRPD, Limit: 2})
	l.Configure(e.ctx, Window{ProviderID: p.ID, Kind: WindowRPM, Limit: 2})
	l.Reserve(e.ctx, p.ID, "m")
	l.Reserve(e.ctx, p.ID, "m")
	l2 := NewQuotaLedger(e.reg, e.pr, e.bus, QuotaOptions{Now: c.now}) // a new process reading the same database
	if ok, _, _ := l2.Reserve(e.ctx, p.ID, "m"); ok {
		t.Fatal("usage was lost across a restart")
	}
	// The minute window is rebuilt conservatively and still clears on time.
	c.add(61 * time.Second)
	if ok, _ := l2.Check(e.ctx, p.ID, "m"); ok {
		t.Fatal("daily window should still be exhausted") // rpd is 2/2
	}
}

func TestOpenRouterFreeDefaultsScopeAndPolling(t *testing.T) {
	e := newEnv(t)
	keyResp := map[string]any{"data": map[string]any{"label": "k", "limit": 10.0, "limit_remaining": 0.0, "limit_reset": "daily", "usage": 10.0, "is_free_tier": false,
		"free_model_daily_requests": map[string]any{"used": 1000, "limit": 1000, "remaining": 0}}}
	var calls atomic.Int32
	f := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){})
	f.set("/api/v1/key", func(w http.ResponseWriter, r *http.Request) { calls.Add(1); jsonBody(keyResp)(w, r) })
	p := e.create(t, NewProvider{Name: "or", Kind: KindAggregator, BaseURL: f.URL + "/api/v1", Flavor: FlavorOpenRouter, APIKey: "k"})
	l, _ := newLedger(t, e, t0)

	// Defaults: 20 rpm and 50 rpd for free models below 10 credits.
	if err := l.SeedOpenRouterFree(e.ctx, p.ID, 3); err != nil {
		t.Fatal(err)
	}
	snap, _ := l.Snapshot(e.ctx, p.ID)
	got := map[WindowKind]float64{}
	for _, w := range snap {
		if w.Model != ScopeFreeModels || w.Source != SourceConfigured {
			t.Fatalf("window %+v", w)
		}
		got[w.Kind] = w.Limit
	}
	if got[WindowRPM] != 20 || got[WindowRPD] != 50 {
		t.Fatalf("defaults %v", got)
	}
	// 21st free request in a minute is refused; a paid model is not covered by the free-model windows.
	for i := 0; i < 20; i++ {
		if ok, _, _ := l.Reserve(e.ctx, p.ID, "x/y:free"); !ok {
			t.Fatalf("free request %d refused", i)
		}
	}
	if ok, _, _ := l.Reserve(e.ctx, p.ID, "x/y:free"); ok {
		t.Fatal("free rpm not enforced")
	}
	for i := 0; i < 30; i++ {
		if ok, _, _ := l.Reserve(e.ctx, p.ID, "paid/model"); !ok {
			t.Fatal("free-model window blocked a paid model")
		}
	}
	// With >= 10 credits the daily ceiling is 1000.
	e2 := newEnv(t)
	p2 := e2.create(t, NewProvider{Name: "or", Kind: KindAggregator, BaseURL: "https://openrouter.ai/api/v1"})
	l2, _ := newLedger(t, e2, t0)
	l2.SeedOpenRouterFree(e2.ctx, p2.ID, 10)
	s2, _ := l2.Snapshot(e2.ctx, p2.ID)
	for _, w := range s2 {
		if w.Kind == WindowRPD && w.Limit != 1000 {
			t.Fatalf("rpd with credits = %v", w.Limit)
		}
	}

	// Polling /key replaces the soft default with the authoritative numbers (source api) and exhausts the window.
	k, err := l.PollOpenRouter(e.ctx, p.ID)
	if err != nil || calls.Load() != 1 || k.FreeModelDailyRequests.Used != 1000 {
		t.Fatalf("poll: %v calls=%d %+v", err, calls.Load(), k)
	}
	snap, _ = l.Snapshot(e.ctx, p.ID)
	var rpd, credits *Window
	for i := range snap {
		switch {
		case snap[i].Kind == WindowRPD:
			rpd = &snap[i]
		case snap[i].Kind == WindowCredits:
			credits = &snap[i]
		}
	}
	if rpd == nil || rpd.Source != SourceAPI || rpd.Limit != 1000 || rpd.Used != 1000 {
		t.Fatalf("rpd after poll %+v", rpd)
	}
	if credits == nil || credits.Limit != 10 || credits.Used != 10 || credits.Source != SourceAPI {
		t.Fatalf("credits after poll %+v", credits)
	}
	// ConfigureDefault must not clobber a polled window.
	l.SeedOpenRouterFree(e.ctx, p.ID, 0)
	snap, _ = l.Snapshot(e.ctx, p.ID)
	for _, w := range snap {
		if w.Kind == WindowRPD && w.Source != SourceAPI {
			t.Fatal("a default replaced a polled window")
		}
	}
	// Exhausted credits make the whole provider unavailable (paid calls would get 402).
	if _, bad := l.Unavailable(e.ctx, p.ID); !bad {
		t.Fatal("credit exhaustion must make the provider unavailable")
	}
	if ok, _, _ := l.Reserve(e.ctx, p.ID, "paid/model"); ok {
		t.Fatal("paid call allowed with exhausted credits")
	}
	if len(e.events(t, "provider.quota_updated")) != 1 || len(e.events(t, "provider.quota_exhausted")) < 1 {
		t.Fatal("expected provider.quota_updated and provider.quota_exhausted events")
	}
}

func TestNextMidnight(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	if got := NextMidnight(now, ""); !got.Equal(time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("UTC %s", got)
	}
	// Pacific daylight time ends on 1 Nov 2026: the zone database must be present (embedded tzdata) and respected.
	got := NextMidnight(time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC), "America/Los_Angeles")
	if !got.Equal(time.Date(2026, 11, 1, 7, 0, 0, 0, time.UTC)) { // 31 Oct 20:00 PDT -> midnight PDT = 07:00Z
		t.Fatalf("LA %s", got)
	}
	if got := NextMidnight(now, "Not/AZone"); !got.Equal(time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unknown zone must fall back to UTC: %s", got)
	}
}

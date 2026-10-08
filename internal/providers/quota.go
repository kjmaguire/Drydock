package providers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // Windows has no zoneinfo database; daily windows reset in named zones (Gemini: America/Los_Angeles)

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
)

// WindowKind names a quota window.
type WindowKind string

const (
	WindowRPM     WindowKind = "rpm"  // requests per minute
	WindowRPD     WindowKind = "rpd"  // requests per day
	WindowTPM     WindowKind = "tpm"  // tokens per minute
	WindowTPD     WindowKind = "tpd"  // tokens per day
	WindowITPM    WindowKind = "itpm" // input tokens per minute
	WindowOTPM    WindowKind = "otpm" // output tokens per minute
	WindowCredits WindowKind = "credits"
)

// QuotaSource says how trustworthy a window is: configured windows are soft (decremented locally); headers and api
// windows are authoritative (docs/model-routing.md s6).
type QuotaSource string

const (
	SourceConfigured QuotaSource = "configured"
	SourceHeaders    QuotaSource = "headers"
	SourceAPI        QuotaSource = "api"
)

// ScopeFreeModels scopes a window to OpenRouter free models (ids ending ":free", and openrouter/free).
const ScopeFreeModels = "*:free"

// OpenRouter free-model limits (docs/phase0/model-providers.md A.5, snippets/exports/constants.mdx).
const (
	OpenRouterFreeRPM             = 20
	OpenRouterFreeRPDNoCredits    = 50
	OpenRouterFreeRPDHasCredits   = 1000
	OpenRouterFreeCreditThreshold = 10.0 // credits purchased all-time; the docs say the higher ceiling starts one credit lower (9)
)

// Window is one quota window. Model "" is provider-wide.
type Window struct {
	ProviderID string
	Model      string
	Kind       WindowKind
	Limit      float64
	Used       float64
	ResetAt    time.Time // zero = unknown
	ResetTZ    string    // zone for daily resets, default UTC
	Source     QuotaSource
	UpdatedAt  time.Time
}

// Remaining is Limit minus Used, never negative.
func (w Window) Remaining() float64 {
	if r := w.Limit - w.Used; r > 0 {
		return r
	}
	return 0
}

func (k WindowKind) minute() bool {
	return k == WindowRPM || k == WindowTPM || k == WindowITPM || k == WindowOTPM
}
func (k WindowKind) requests() bool { return k == WindowRPM || k == WindowRPD }

type logEntry struct {
	at  time.Time
	amt float64
}

type win struct {
	Window
	log      []logEntry // sliding log, used only by configured minute windows
	notified bool       // provider.quota_exhausted already emitted for this exhaustion
}

type block struct {
	model  string
	until  time.Time
	reason string
}

// QuotaLedger tracks provider quota windows (table provider_quotas): Reserve before a call, Record after it.
// Exhausted windows make a provider (or model) unavailable until they reset and emit provider.quota_exhausted.
type QuotaLedger struct {
	reg  *Registry
	pr   *Prober // optional, for OpenRouter /key polling
	bus  events.Bus
	now  func() time.Time
	poll time.Duration

	mu     sync.Mutex
	wins   map[string]*win // provider|model|kind
	loaded map[string]bool
	blocks map[string][]block // provider -> temporary blocks from 429s (memory only)
	cancel context.CancelFunc
	done   chan struct{}
}

// QuotaOptions configures NewQuotaLedger.
type QuotaOptions struct {
	Now          func() time.Time // default time.Now; tests advance it
	PollInterval time.Duration    // OpenRouter /key polling period, default 5 minutes
}

// NewQuotaLedger builds the ledger. pr and bus may be nil.
func NewQuotaLedger(reg *Registry, pr *Prober, bus events.Bus, o QuotaOptions) *QuotaLedger {
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 5 * time.Minute
	}
	return &QuotaLedger{reg: reg, pr: pr, bus: bus, now: o.Now, poll: o.PollInterval,
		wins: map[string]*win{}, loaded: map[string]bool{}, blocks: map[string][]block{}}
}

func wkey(provider, model string, k WindowKind) string {
	return provider + "\x00" + model + "\x00" + string(k)
}

func scopeMatches(scope, model string) bool {
	switch scope {
	case "":
		return true
	case ScopeFreeModels:
		return strings.HasSuffix(model, ":free") || model == "openrouter/free"
	}
	return scope == model
}

// loadLocked reads the provider's windows once.
func (l *QuotaLedger) loadLocked(ctx context.Context, provider string) error {
	if l.loaded[provider] {
		return nil
	}
	rows, err := l.reg.db.Reader().QueryContext(ctx, `SELECT model,window_kind,limit_value,used,reset_at,reset_tz,source,updated_at
		FROM provider_quotas WHERE provider_id=?`, provider)
	if err != nil {
		return err
	}
	defer rows.Close()
	now := l.now()
	for rows.Next() {
		w := &win{}
		var reset, upd int64
		w.ProviderID = provider
		if err := rows.Scan(&w.Model, &w.Kind, &w.Limit, &w.Used, &reset, &w.ResetTZ, &w.Source, &upd); err != nil {
			return err
		}
		w.ResetAt, w.UpdatedAt = fromMS(reset), fromMS(upd)
		if sliding(w) && w.Used > 0 && w.ResetAt.After(now) {
			// Rebuild conservatively: everything used is treated as the oldest entry, which expires at ResetAt.
			w.log = []logEntry{{at: w.ResetAt.Add(-time.Minute), amt: w.Used}}
		} else if sliding(w) {
			w.Used = 0
		}
		l.wins[wkey(provider, w.Model, w.Kind)] = w
	}
	if err := rows.Err(); err != nil {
		return err
	}
	l.loaded[provider] = true
	return nil
}

func sliding(w *win) bool { return w.Kind.minute() && w.Source == SourceConfigured }

// nextReset is the next reset boundary after now for a kind: a minute window resets a minute from now, a daily
// window at the next midnight in its zone, credits never on their own.
func nextReset(kind WindowKind, tz string, now time.Time) time.Time {
	switch {
	case kind.minute():
		return now.Add(time.Minute)
	case kind == WindowRPD || kind == WindowTPD:
		return NextMidnight(now, tz)
	}
	return time.Time{}
}

// NextMidnight returns the next midnight after now in the named zone (UTC when empty or unknown).
func NextMidnight(now time.Time, tz string) time.Time {
	loc := time.UTC
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	n := now.In(loc)
	return time.Date(n.Year(), n.Month(), n.Day()+1, 0, 0, 0, 0, loc).UTC()
}

// rollLocked brings a window up to now: sliding windows drop expired entries; counter windows reset when their
// reset time has passed.
func (l *QuotaLedger) rollLocked(w *win, now time.Time) {
	if sliding(w) {
		kept := w.log[:0]
		var sum float64
		for _, e := range w.log {
			if e.at.Add(time.Minute).After(now) {
				kept = append(kept, e)
				sum += e.amt
			}
		}
		w.log, w.Used = kept, sum
		if len(kept) > 0 {
			w.ResetAt = kept[0].at.Add(time.Minute)
		} else {
			w.ResetAt = time.Time{}
		}
		if sum < w.Limit {
			w.notified = false
		}
		return
	}
	if !w.ResetAt.IsZero() && !now.Before(w.ResetAt) {
		w.Used, w.notified = 0, false
		w.ResetAt = nextReset(w.Kind, w.ResetTZ, now)
		if w.Kind == WindowCredits {
			w.ResetAt = time.Time{}
		}
	}
	if w.Used < w.Limit {
		w.notified = false
	}
}

// blockedLocked reports whether the window denies a new request, and when it may be retried.
func (l *QuotaLedger) blockedLocked(w *win, now time.Time) (bool, time.Time) {
	if w.Limit <= 0 {
		return false, time.Time{}
	}
	exhausted := w.Used >= w.Limit // token and credit windows block once exhausted; the next call's size is unknown
	if w.Kind.requests() {
		exhausted = w.Used+1 > w.Limit
	}
	if !exhausted {
		return false, time.Time{}
	}
	retry := w.ResetAt
	if w.Kind == WindowCredits && retry.IsZero() {
		retry = now.Add(l.poll) // re-check after the next /key poll
	}
	if retry.IsZero() || !retry.After(now) {
		retry = now.Add(time.Second)
	}
	return true, retry
}

func (l *QuotaLedger) applicableLocked(provider, model string) []*win {
	var out []*win
	for _, w := range l.wins {
		if w.ProviderID == provider && scopeMatches(w.Model, model) {
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// Reserve takes one request slot on every window that applies to (provider, model) BEFORE a call. ok is false when a
// window is exhausted or the provider is blocked by an earlier 429; retryAt is when to try again. Call Release if the
// request never reached the provider, and Record after it did.
func (l *QuotaLedger) Reserve(ctx context.Context, provider, model string) (ok bool, retryAt time.Time, err error) {
	return l.reserve(ctx, provider, model, true)
}

// Check is Reserve without consuming anything, for the router's candidate filter.
func (l *QuotaLedger) Check(ctx context.Context, provider, model string) (ok bool, retryAt time.Time) {
	ok, retryAt, _ = l.reserve(ctx, provider, model, false)
	return ok, retryAt
}

func (l *QuotaLedger) reserve(ctx context.Context, provider, model string, consume bool) (bool, time.Time, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(ctx, provider); err != nil {
		return false, time.Time{}, err
	}
	now := l.now()
	var retry time.Time
	var hit []*win
	ws := l.applicableLocked(provider, model)
	for _, w := range ws {
		l.rollLocked(w, now)
		if b, at := l.blockedLocked(w, now); b {
			hit = append(hit, w)
			if at.After(retry) {
				retry = at
			}
		}
	}
	if b := l.activeBlockLocked(provider, model, now); b != nil {
		if b.until.After(retry) {
			retry = b.until
		}
		hit = append(hit, nil)
	}
	if len(hit) > 0 {
		if consume {
			for _, w := range hit {
				if w != nil && !w.notified {
					w.notified = true
					l.emitExhausted(ctx, w.ProviderID, w.Model, string(w.Kind), w.Limit, w.Used, retry, string(w.Source))
				}
			}
		}
		return false, retry, nil
	}
	if !consume {
		return true, time.Time{}, nil
	}
	var dirty []*win
	for _, w := range ws {
		if !w.Kind.requests() {
			continue
		}
		l.addLocked(w, 1, now)
		dirty = append(dirty, w)
	}
	if err := l.persistLocked(ctx, dirty...); err != nil {
		return false, time.Time{}, err
	}
	return true, time.Time{}, nil
}

func (l *QuotaLedger) addLocked(w *win, amt float64, now time.Time) {
	if sliding(w) {
		w.log = append(w.log, logEntry{at: now, amt: amt})
		w.Used += amt
		w.ResetAt = w.log[0].at.Add(time.Minute)
		return
	}
	w.Used += amt
	if w.ResetAt.IsZero() {
		w.ResetAt = nextReset(w.Kind, w.ResetTZ, now)
	}
}

// Release gives back a request slot taken by Reserve for a call that never reached the provider.
func (l *QuotaLedger) Release(ctx context.Context, provider, model string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.loadLocked(ctx, provider) != nil {
		return
	}
	var dirty []*win
	for _, w := range l.applicableLocked(provider, model) {
		if !w.Kind.requests() || w.Used <= 0 {
			continue
		}
		if sliding(w) {
			if n := len(w.log); n > 0 {
				w.Used -= w.log[n-1].amt
				w.log = w.log[:n-1]
			}
		} else {
			w.Used--
		}
		dirty = append(dirty, w)
	}
	_ = l.persistLocked(ctx, dirty...)
}

// Record settles a finished call: usage is added to the token windows and the response headers (when they carry
// rate-limit information) replace the matching windows with the provider's authoritative numbers.
func (l *QuotaLedger) Record(ctx context.Context, provider, model string, u Usage, h http.Header) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(ctx, provider); err != nil {
		return err
	}
	now := l.now()
	var dirty []*win
	for _, w := range l.applicableLocked(provider, model) {
		var amt float64
		switch w.Kind {
		case WindowTPM, WindowTPD:
			amt = float64(u.Total())
		case WindowITPM:
			amt = float64(u.Input)
		case WindowOTPM:
			amt = float64(u.Output)
		default:
			continue
		}
		l.rollLocked(w, now)
		l.addLocked(w, amt, now)
		dirty = append(dirty, w)
	}
	for _, hw := range ParseRateLimitHeaders(h, now) {
		w := l.wins[wkey(provider, model, hw.Kind)]
		if w == nil {
			w = &win{Window: Window{ProviderID: provider, Model: model, Kind: hw.Kind, ResetTZ: "UTC"}}
			l.wins[wkey(provider, model, hw.Kind)] = w
		}
		w.Source, w.log = SourceHeaders, nil
		w.Limit, w.Used = hw.Limit, hw.Limit-hw.Remaining
		if w.Used < 0 {
			w.Used = 0
		}
		w.ResetAt = hw.ResetAt
		if w.Used < w.Limit {
			w.notified = false
		}
		dirty = append(dirty, w)
	}
	for _, w := range dirty {
		if w.Limit > 0 && w.Used >= w.Limit && !w.notified {
			w.notified = true
			l.emitExhausted(ctx, provider, w.Model, string(w.Kind), w.Limit, w.Used, w.ResetAt, string(w.Source))
		}
	}
	return l.persistLocked(ctx, dirty...)
}

// Penalize blocks (provider, model) until the given time after a 429 or similar signal (a Retry-After above the
// caller's backoff ceiling, a Gemini per-day quota). Model "" blocks the whole provider. Held in memory only.
func (l *QuotaLedger) Penalize(ctx context.Context, provider, model string, until time.Time, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	kept := l.blocks[provider][:0]
	for _, b := range l.blocks[provider] {
		if b.until.After(now) && b.model != model {
			kept = append(kept, b)
		}
	}
	l.blocks[provider] = append(kept, block{model: model, until: until, reason: reason})
	l.emitExhausted(ctx, provider, model, "blocked:"+reason, 0, 0, until, "headers")
}

func (l *QuotaLedger) activeBlockLocked(provider, model string, now time.Time) *block {
	var best *block
	for i := range l.blocks[provider] {
		b := &l.blocks[provider][i]
		if b.until.After(now) && scopeMatches(b.model, model) && (best == nil || b.until.After(best.until)) {
			best = b
		}
	}
	return best
}

// Unavailable reports whether the provider as a whole is unavailable (a provider-wide window is exhausted or a
// provider-wide block is active) and until when. Per-model windows make only that model unavailable (see Check).
func (l *QuotaLedger) Unavailable(ctx context.Context, provider string) (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.loadLocked(ctx, provider) != nil {
		return time.Time{}, false
	}
	now := l.now()
	var until time.Time
	for _, w := range l.wins {
		if w.ProviderID != provider || w.Model != "" {
			continue
		}
		l.rollLocked(w, now)
		if b, at := l.blockedLocked(w, now); b && at.After(until) {
			until = at
		}
	}
	for _, b := range l.blocks[provider] {
		if b.model == "" && b.until.After(now) && b.until.After(until) {
			until = b.until
		}
	}
	return until, !until.IsZero()
}

// Snapshot returns the provider's windows, rolled to now.
func (l *QuotaLedger) Snapshot(ctx context.Context, provider string) ([]Window, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(ctx, provider); err != nil {
		return nil, err
	}
	now := l.now()
	var out []Window
	for _, w := range l.applicableAll(provider) {
		l.rollLocked(w, now)
		out = append(out, w.Window)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

func (l *QuotaLedger) applicableAll(provider string) []*win {
	var out []*win
	for _, w := range l.wins {
		if w.ProviderID == provider {
			out = append(out, w)
		}
	}
	return out
}

// Configure sets (or replaces) a window, resetting its usage. Source defaults to configured.
func (l *QuotaLedger) Configure(ctx context.Context, w Window) error {
	return l.configure(ctx, w, true)
}

// ConfigureDefault sets a window only when none exists yet (so a polled or header-learned window is never replaced
// by a default).
func (l *QuotaLedger) ConfigureDefault(ctx context.Context, w Window) error {
	return l.configure(ctx, w, false)
}

func (l *QuotaLedger) configure(ctx context.Context, w Window, replace bool) error {
	if w.ProviderID == "" || w.Kind == "" || w.Limit <= 0 {
		return fmt.Errorf("%w: quota window needs a provider, a kind and a positive limit", ErrInvalid)
	}
	if w.Source == "" {
		w.Source = SourceConfigured
	}
	if w.ResetTZ == "" {
		w.ResetTZ = "UTC"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(ctx, w.ProviderID); err != nil {
		return err
	}
	k := wkey(w.ProviderID, w.Model, w.Kind)
	if _, exists := l.wins[k]; exists && !replace {
		return nil
	}
	if w.ResetAt.IsZero() {
		w.ResetAt = nextReset(w.Kind, w.ResetTZ, l.now())
	}
	nw := &win{Window: w}
	if sliding(nw) {
		nw.ResetAt, nw.Used = time.Time{}, 0
	}
	l.wins[k] = nw
	return l.persistLocked(ctx, nw)
}

// SeedOpenRouterFree installs the OpenRouter free-tier defaults for ":free" models: 20 requests per minute, and 50 (or
// 1000 once at least 10 credits were purchased) requests per day. They are soft defaults until a /key poll replaces
// the daily window with the provider's own numbers.
func (l *QuotaLedger) SeedOpenRouterFree(ctx context.Context, providerID string, creditsPurchased float64) error {
	rpd := float64(OpenRouterFreeRPDNoCredits)
	if creditsPurchased >= OpenRouterFreeCreditThreshold {
		rpd = OpenRouterFreeRPDHasCredits
	}
	if err := l.ConfigureDefault(ctx, Window{ProviderID: providerID, Model: ScopeFreeModels, Kind: WindowRPM, Limit: OpenRouterFreeRPM}); err != nil {
		return err
	}
	return l.ConfigureDefault(ctx, Window{ProviderID: providerID, Model: ScopeFreeModels, Kind: WindowRPD, Limit: rpd, ResetTZ: "UTC"})
}

// PollOpenRouter reads GET /api/v1/key and replaces the free-model daily window and the credit window with the
// authoritative numbers (source api).
func (l *QuotaLedger) PollOpenRouter(ctx context.Context, providerID string) (OpenRouterKey, error) {
	if l.pr == nil {
		return OpenRouterKey{}, fmt.Errorf("%w: ledger has no prober", ErrInvalid)
	}
	k, err := l.pr.OpenRouterKey(ctx, providerID)
	if err != nil {
		return OpenRouterKey{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(ctx, providerID); err != nil {
		return OpenRouterKey{}, err
	}
	now := l.now()
	var dirty []*win
	set := func(model string, kind WindowKind, limit, used float64, reset time.Time) {
		key := wkey(providerID, model, kind)
		w := l.wins[key]
		if w == nil {
			w = &win{Window: Window{ProviderID: providerID, Model: model, Kind: kind, ResetTZ: "UTC"}}
			l.wins[key] = w
		}
		w.Source, w.log, w.Limit, w.Used, w.ResetAt = SourceAPI, nil, limit, used, reset
		if used < limit {
			w.notified = false
		}
		dirty = append(dirty, w)
	}
	if f := k.FreeModelDailyRequests; f != nil && f.Limit > 0 {
		set(ScopeFreeModels, WindowRPD, float64(f.Limit), float64(f.Used), NextMidnight(now, "UTC"))
	}
	if k.Limit != nil && k.LimitRemaining != nil && *k.Limit > 0 {
		var reset time.Time
		switch k.LimitReset {
		case "daily":
			reset = NextMidnight(now, "UTC")
		case "weekly":
			d := NextMidnight(now, "UTC")
			for d.Weekday() != time.Monday {
				d = d.AddDate(0, 0, 1)
			}
			reset = d
		case "monthly":
			reset = time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		}
		set("", WindowCredits, *k.Limit, *k.Limit-*k.LimitRemaining, reset)
	}
	for _, w := range dirty {
		if w.Limit > 0 && w.Used >= w.Limit && !w.notified {
			w.notified = true
			l.emitExhausted(ctx, providerID, w.Model, string(w.Kind), w.Limit, w.Used, w.ResetAt, string(w.Source))
		}
	}
	if err := l.persistLocked(ctx, dirty...); err != nil {
		return OpenRouterKey{}, err
	}
	if l.bus != nil {
		_, _ = events.Emit(ctx, l.bus, "provider.quota_updated", events.Event{}, map[string]any{"providerId": providerID, "source": "api", "windows": len(dirty)})
	}
	return k, nil
}

// StartPolling polls every OpenRouter provider's /key on the ledger's interval until ctx ends or StopPolling.
func (l *QuotaLedger) StartPolling(ctx context.Context) {
	l.mu.Lock()
	if l.cancel != nil {
		l.mu.Unlock()
		return
	}
	ctx, l.cancel = context.WithCancel(ctx)
	l.done = make(chan struct{})
	l.mu.Unlock()
	go func() {
		defer close(l.done)
		t := time.NewTicker(l.poll)
		defer t.Stop()
		for {
			l.pollAll(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

func (l *QuotaLedger) pollAll(ctx context.Context) {
	ps, err := l.reg.List(ctx)
	if err != nil {
		return
	}
	for _, p := range ps {
		if p.Flavor == FlavorOpenRouter && p.Enabled {
			_, _ = l.PollOpenRouter(ctx, p.ID)
		}
	}
}

// StopPolling ends the polling loop and waits for it.
func (l *QuotaLedger) StopPolling() {
	l.mu.Lock()
	cancel, done := l.cancel, l.done
	l.cancel = nil
	l.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (l *QuotaLedger) emitExhausted(ctx context.Context, provider, model, window string, limit, used float64, resetAt time.Time, source string) {
	if l.bus == nil {
		return
	}
	_, _ = events.Emit(ctx, l.bus, "provider.quota_exhausted", events.Event{}, map[string]any{
		"providerId": provider, "model": model, "window": window, "limit": limit, "used": used, "resetAt": ms(resetAt), "source": source})
}

func (l *QuotaLedger) persistLocked(ctx context.Context, ws ...*win) error {
	if len(ws) == 0 {
		return nil
	}
	now := l.now()
	return l.reg.db.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, w := range ws {
			w.UpdatedAt = now
			if _, err := tx.Exec(`INSERT INTO provider_quotas(provider_id,model,window_kind,limit_value,used,reset_at,reset_tz,source,updated_at)
				VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(provider_id,model,window_kind) DO UPDATE SET limit_value=excluded.limit_value,
				used=excluded.used, reset_at=excluded.reset_at, reset_tz=excluded.reset_tz, source=excluded.source, updated_at=excluded.updated_at`,
				w.ProviderID, w.Model, string(w.Kind), w.Limit, w.Used, ms(w.ResetAt), w.ResetTZ, string(w.Source), storage.Now()); err != nil {
				return err
			}
		}
		return nil
	})
}

// RemainingPct is the smallest remaining fraction (0..1) over the windows that apply to (provider, model), or -1 when
// no window with a limit applies (unknown).
func (l *QuotaLedger) RemainingPct(ctx context.Context, provider, model string) float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.loadLocked(ctx, provider) != nil {
		return -1
	}
	now := l.now()
	best := -1.0
	for _, w := range l.applicableLocked(provider, model) {
		l.rollLocked(w, now)
		if w.Limit <= 0 {
			continue
		}
		if f := w.Remaining() / w.Limit; best < 0 || f < best {
			best = f
		}
	}
	return best
}

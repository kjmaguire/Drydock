package providers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OpenRouter endpoint uptime (decision P53 c, borrowed from claude-code-router). OpenRouter serves most models from
// several upstream endpoints and publishes each one's recent uptime at GET /api/v1/models/{author}/{slug}/endpoints
// (fields uptime_last_5m and uptime_last_30m, percentages; docs/phase0/model-providers.md A.12). EndpointUptime reads
// that list for the model a call is about to use, caches it briefly, and says which endpoints the request should ask
// OpenRouter to skip (the request's provider.ignore). It only ever narrows OpenRouter's choice within one model: it
// never blocks a call (a failed lookup avoids nothing) and never removes every endpoint (when all are below the
// threshold the best ones stay). Drydock's own router is unaffected: OpenRouter as a provider stays healthy.

// DefaultMinEndpointUptime is the threshold, in percent, below which an OpenRouter endpoint is avoided.
const DefaultMinEndpointUptime = 98.0

// settingMinEndpointUptime is the settings key prefix of a provider's threshold (followed by the provider id).
const settingMinEndpointUptime = "provider.endpoint_uptime_min."

// UptimeOptions configures NewEndpointUptime. Every field is optional.
type UptimeOptions struct {
	TTL     time.Duration    // how long a model's endpoint list is reused (default 5 minutes)
	FailTTL time.Duration    // how long a failed lookup is remembered before trying again (default 1 minute)
	Timeout time.Duration    // per lookup (default 5 seconds)
	Now     func() time.Time // default time.Now
}

// AvoidedEndpoint is one endpoint a request asks OpenRouter to skip.
type AvoidedEndpoint struct {
	Tag       string  `json:"tag"`            // the endpoint's routing slug, what provider.ignore takes
	Name      string  `json:"name,omitempty"` // OpenRouter's display name of the upstream provider
	UptimePct float64 `json:"uptimePct"`
	Window    string  `json:"window"` // "5m" or "30m": which of OpenRouter's uptime windows the figure is
}

// EndpointAvoidance is the decision for one provider and model.
type EndpointAvoidance struct {
	ProviderID string            `json:"providerId"`
	Model      string            `json:"model"`
	Threshold  float64           `json:"threshold"`
	Avoided    []AvoidedEndpoint `json:"avoided"`
	// KeptBest is true when every endpoint was below the threshold: the best of them were kept rather than all excluded.
	KeptBest bool      `json:"keptBest"`
	At       time.Time `json:"at"`
}

// Tags lists the avoided endpoints' tags, for the request's provider.ignore.
func (a EndpointAvoidance) Tags() []string {
	out := make([]string, 0, len(a.Avoided))
	for _, e := range a.Avoided {
		out = append(out, e.Tag)
	}
	return out
}

// endpointUptime is one endpoint as read from OpenRouter.
type endpointUptime struct {
	tag, name string
	uptime    *float64
	window    string
}

type uptimeEntry struct {
	eps   []endpointUptime
	err   error
	at    time.Time
	ready chan struct{} // closed when the lookup finished
}

// EndpointUptime is the cache of OpenRouter endpoint uptime and the record of what is being avoided.
type EndpointUptime struct {
	reg *Registry
	o   UptimeOptions

	mu      sync.Mutex
	entries map[string]*uptimeEntry       // provider id + "\x00" + model
	last    map[string]*EndpointAvoidance // the latest decision per provider and model
}

// NewEndpointUptime builds the tracker over the registry (which holds the providers, their keys and thresholds).
func NewEndpointUptime(reg *Registry, o UptimeOptions) *EndpointUptime {
	if o.TTL <= 0 {
		o.TTL = 5 * time.Minute
	}
	if o.FailTTL <= 0 {
		o.FailTTL = time.Minute
	}
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	return &EndpointUptime{reg: reg, o: o, entries: map[string]*uptimeEntry{}, last: map[string]*EndpointAvoidance{}}
}

// Avoid says which endpoints of model a request to OpenRouter provider p should skip. changed reports that the set
// differs from the previous decision for this provider and model (the caller announces it). A provider that is not
// OpenRouter, a model id OpenRouter has no endpoint list for, a threshold of 0 or a failed lookup avoid nothing.
func (u *EndpointUptime) Avoid(ctx context.Context, p Provider, model string) (a EndpointAvoidance, changed bool) {
	if u == nil {
		return EndpointAvoidance{ProviderID: p.ID, Model: model}, false
	}
	a = EndpointAvoidance{ProviderID: p.ID, Model: model, At: u.o.Now()}
	if p.Flavor != FlavorOpenRouter || !endpointListable(model) {
		return a, false
	}
	a.Threshold = u.reg.MinEndpointUptime(ctx, p.ID)
	if a.Threshold > 0 {
		if eps, err := u.endpoints(ctx, p, model); err == nil {
			a.Avoided, a.KeptBest = chooseAvoided(eps, a.Threshold)
		}
	}
	return a, u.remember(a)
}

// Avoiding lists the provider's current non-empty decisions, newest first, for the provider's detail panel.
func (u *EndpointUptime) Avoiding(providerID string) []EndpointAvoidance {
	out := []EndpointAvoidance{}
	if u == nil {
		return out
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, a := range u.last {
		if a.ProviderID == providerID && len(a.Avoided) > 0 {
			c := *a
			c.Avoided = append([]AvoidedEndpoint(nil), a.Avoided...)
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].Model < out[j].Model
	})
	return out
}

// remember stores the decision and reports whether its avoided set changed.
func (u *EndpointUptime) remember(a EndpointAvoidance) bool {
	key := a.ProviderID + "\x00" + a.Model
	u.mu.Lock()
	defer u.mu.Unlock()
	prev := u.last[key]
	c := a
	u.last[key] = &c
	if prev == nil {
		return len(a.Avoided) > 0
	}
	return strings.Join(prev.Tags(), "\x00") != strings.Join(a.Tags(), "\x00") || prev.KeptBest != a.KeptBest
}

// endpointListable reports whether OpenRouter can list endpoints for the model id: author/slug, and not one of
// OpenRouter's own routers (openrouter/auto, openrouter/free), which choose a model per request.
func endpointListable(model string) bool {
	author, slug, ok := strings.Cut(model, "/")
	return ok && author != "" && slug != "" && author != "openrouter"
}

// endpoints returns the model's endpoint list from the cache, or looks it up once for all concurrent callers.
func (u *EndpointUptime) endpoints(ctx context.Context, p Provider, model string) ([]endpointUptime, error) {
	key := p.ID + "\x00" + model
	u.mu.Lock()
	e := u.entries[key]
	if e != nil {
		select {
		case <-e.ready:
			ttl := u.o.TTL
			if e.err != nil {
				ttl = u.o.FailTTL
			}
			if u.o.Now().Sub(e.at) < ttl {
				u.mu.Unlock()
				return e.eps, e.err
			}
			e = nil // stale: look up again
		default: // another call is looking it up: wait for it
		}
	}
	if e != nil {
		u.mu.Unlock()
		select {
		case <-e.ready:
			return e.eps, e.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	e = &uptimeEntry{ready: make(chan struct{})}
	u.entries[key] = e
	u.pruneLocked()
	u.mu.Unlock()

	// The lookup outlives the caller's cancellation (its result serves the next calls) but never its own timeout.
	lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), u.o.Timeout)
	eps, err := u.fetch(lctx, p, model)
	cancel()
	u.mu.Lock()
	e.eps, e.err, e.at = eps, err, u.o.Now()
	close(e.ready)
	u.mu.Unlock()
	return eps, err
}

// pruneLocked drops finished entries past their lifetime once the cache grows.
func (u *EndpointUptime) pruneLocked() {
	if len(u.entries) <= 256 {
		return
	}
	now := u.o.Now()
	for k, e := range u.entries {
		select {
		case <-e.ready:
			if now.Sub(e.at) >= u.o.TTL {
				delete(u.entries, k)
			}
		default:
		}
	}
}

// fetch performs GET {base}/models/{author}/{slug}/endpoints with the provider's key, the way the probes authenticate
// to OpenRouter. Errors carry the status, never the key or the response body.
func (u *EndpointUptime) fetch(ctx context.Context, p Provider, model string) ([]endpointUptime, error) {
	key, err := u.reg.APIKey(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("endpoint uptime: api key: %w", err)
	}
	hc, err := u.reg.HTTPClient(p)
	if err != nil {
		return nil, err
	}
	author, slug, _ := strings.Cut(model, "/")
	rawURL := strings.TrimRight(p.BaseURL, "/") + "/models/" + url.PathEscape(author) + "/" + url.PathEscape(slug) + "/endpoints"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	applyAuth(req, p.Flavor, key, "")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("endpoint uptime: GET endpoints of %s: %w", model, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("endpoint uptime: GET endpoints of %s: HTTP %d", model, resp.StatusCode)
	}
	var env struct {
		Data struct {
			Endpoints []struct {
				Name         string   `json:"name"`
				ProviderName string   `json:"provider_name"`
				Tag          string   `json:"tag"`
				Uptime5m     *float64 `json:"uptime_last_5m"`
				Uptime30m    *float64 `json:"uptime_last_30m"`
			} `json:"endpoints"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&env); err != nil {
		return nil, fmt.Errorf("endpoint uptime: endpoints of %s: unreadable: %w", model, err)
	}
	out := make([]endpointUptime, 0, len(env.Data.Endpoints))
	for _, ep := range env.Data.Endpoints {
		e := endpointUptime{tag: strings.TrimSpace(ep.Tag), name: ep.ProviderName}
		if e.name == "" {
			e.name = ep.Name
		}
		// The shortest window OpenRouter reports is the most recent; null means too few requests to tell.
		switch {
		case validPct(ep.Uptime5m):
			e.uptime, e.window = ep.Uptime5m, "5m"
		case validPct(ep.Uptime30m):
			e.uptime, e.window = ep.Uptime30m, "30m"
		}
		out = append(out, e)
	}
	return out, nil
}

func validPct(v *float64) bool {
	return v != nil && !math.IsNaN(*v) && *v >= 0 && *v <= 100
}

// chooseAvoided picks the endpoints to skip: every endpoint below the threshold while at least one endpoint is at or
// above it (or has no uptime figure yet); when all are below it, every endpoint except the best (ties kept). An
// endpoint without a tag cannot be named to OpenRouter and is never avoided.
func chooseAvoided(eps []endpointUptime, threshold float64) (avoided []AvoidedEndpoint, keptBest bool) {
	healthy, best := false, -1.0
	for _, e := range eps {
		if e.uptime == nil || *e.uptime >= threshold {
			healthy = true
		}
		if e.uptime != nil && *e.uptime > best {
			best = *e.uptime
		}
	}
	cut := threshold
	if !healthy {
		cut, keptBest = best, true
	}
	for _, e := range eps {
		if e.tag == "" || e.uptime == nil || *e.uptime >= cut {
			continue
		}
		avoided = append(avoided, AvoidedEndpoint{Tag: e.tag, Name: e.name, UptimePct: *e.uptime, Window: e.window})
	}
	sort.Slice(avoided, func(i, j int) bool { return avoided[i].Tag < avoided[j].Tag })
	return avoided, keptBest && len(avoided) > 0
}

// MinEndpointUptime is the provider's threshold in percent: DefaultMinEndpointUptime unless the person set one; 0 turns
// the avoidance off.
func (r *Registry) MinEndpointUptime(ctx context.Context, id string) float64 {
	var v string
	err := r.db.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingMinEndpointUptime+id).Scan(&v)
	if err != nil {
		return DefaultMinEndpointUptime
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || !(f >= 0 && f <= 100) {
		return DefaultMinEndpointUptime
	}
	return f
}

// SetMinEndpointUptime stores the provider's threshold (0 to 100 percent; 0 turns the avoidance off).
func (r *Registry) SetMinEndpointUptime(ctx context.Context, id string, pct float64) error {
	if !(pct >= 0 && pct <= 100) {
		return fmt.Errorf("%w: the endpoint uptime threshold must be between 0 and 100 percent", ErrInvalid)
	}
	if _, err := r.Get(ctx, id); err != nil {
		return err
	}
	err := r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
			settingMinEndpointUptime+id, strconv.FormatFloat(pct, 'f', -1, 64))
		return err
	})
	if err != nil {
		return err
	}
	r.emit(ctx, "provider.updated", map[string]any{"providerId": id})
	return nil
}

package llm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/storage"
)

// BackendFactory builds the single-provider client for p (llm/openai or llm/anthropic). hc carries the provider's
// transport rule and pinned fingerprint; the backend must not retry on its own (the Wrapper owns retry and quota).
type BackendFactory func(p providers.Provider, apiKey string, hc *http.Client) (Client, error)

// RetryPolicy governs retries of retryable provider errors. A Retry-After from the provider is a minimum wait; if the
// wait would exceed Ceiling the call fails with a QuotaError instead of sleeping.
type RetryPolicy struct {
	MaxAttempts int           // total tries per target, default 4
	BaseDelay   time.Duration // first backoff, doubled each attempt, default 500ms
	Ceiling     time.Duration // longest wait the Wrapper will sit out, default 30s
	Jitter      float64       // fraction of random spread added to backoff, default 0.2
}

// Options configures NewWrapper.
type Options struct {
	Registry *providers.Registry // required
	DB       *storage.DB         // required (llm_cache, model_usage)
	Factory  BackendFactory      // required
	// Scanner scans every outbound request; required. Use NopScanner{} to opt out explicitly.
	Scanner Scanner
	Ledger  *providers.QuotaLedger
	Monitor *providers.Monitor // takes host concurrency slots for host providers
	// Uptime, when set, steers OpenRouter calls away from endpoints with poor recent uptime (decision P53 c).
	Uptime    *providers.EndpointUptime
	Bus       events.Bus
	Retry     RetryPolicy
	CacheTTLs map[string]time.Duration // purpose -> TTL; default DefaultCacheTTLs
	Now       func() time.Time         // default time.Now
	// Sleep waits for d or until ctx ends; default a timer. Tests shorten waits through the policy, not by faking the clock.
	Sleep func(ctx context.Context, d time.Duration) error
	// Tracer, when set, is told about every call once it ends (the telemetry sink, decision P113). It sees no content unless it
	// is a ContentTracer that asks for it (content export, decision P121).
	Tracer CallTracer
}

// Wrapper is the policy layer over the provider backends. It implements Client.
type Wrapper struct {
	reg     *providers.Registry
	db      *storage.DB
	factory BackendFactory
	scanner Scanner
	ledger  *providers.QuotaLedger
	mon     *providers.Monitor
	uptime  *providers.EndpointUptime
	bus     events.Bus
	retry   RetryPolicy
	ttls    map[string]time.Duration
	now     func() time.Time
	sleep   func(ctx context.Context, d time.Duration) error
	tracer  CallTracer
}

// NewWrapper validates o and builds the Wrapper.
func NewWrapper(o Options) (*Wrapper, error) {
	switch {
	case o.Registry == nil || o.DB == nil || o.Factory == nil:
		return nil, errors.New("llm: Registry, DB and Factory are required")
	case o.Scanner == nil:
		return nil, errors.New("llm: Scanner is required (use NopScanner{} to opt out explicitly)")
	}
	if o.Retry.MaxAttempts <= 0 {
		o.Retry.MaxAttempts = 4
	}
	if o.Retry.BaseDelay <= 0 {
		o.Retry.BaseDelay = 500 * time.Millisecond
	}
	if o.Retry.Ceiling <= 0 {
		o.Retry.Ceiling = 30 * time.Second
	}
	if o.Retry.Jitter < 0 {
		o.Retry.Jitter = 0
	} else if o.Retry.Jitter == 0 {
		o.Retry.Jitter = 0.2
	}
	if o.CacheTTLs == nil {
		o.CacheTTLs = DefaultCacheTTLs
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return &Wrapper{reg: o.Registry, db: o.DB, factory: o.Factory, scanner: o.Scanner, ledger: o.Ledger, mon: o.Monitor,
		uptime: o.Uptime, bus: o.Bus, retry: o.Retry, ttls: o.CacheTTLs, now: o.Now, sleep: o.Sleep, tracer: o.Tracer}, nil
}

// Complete runs one call through the policy pipeline.
func (w *Wrapper) Complete(ctx context.Context, req Request) (Response, error) {
	return w.run(ctx, req, nil)
}

// Stream is Complete with incremental output.
func (w *Wrapper) Stream(ctx context.Context, req Request, fn func(Delta) error) (Response, error) {
	if fn == nil {
		return Response{}, errors.New("llm: Stream needs a delta callback")
	}
	return w.run(ctx, req, fn)
}

func (w *Wrapper) run(ctx context.Context, req Request, fn func(Delta) error) (Response, error) {
	start := w.now()
	resp, err := w.call(ctx, req, fn)
	w.trace(ctx, req, start, fn != nil, resp, err)
	return resp, err
}

func (w *Wrapper) call(ctx context.Context, req Request, fn func(Delta) error) (Response, error) {
	if req.Provider == "" || req.Model == "" {
		return Response{}, errors.New("llm: request needs a provider and a model")
	}
	if err := w.scan(ctx, req); err != nil {
		return Response{}, err
	}
	targets := []Target{{Provider: req.Provider, Model: req.Model}}
	for _, t := range req.Fallbacks {
		dup := false
		for _, e := range targets {
			dup = dup || e == t
		}
		if !dup && t.Provider != "" && t.Model != "" {
			targets = append(targets, t)
		}
	}
	var errs []error
	for i, t := range targets {
		resp, err := w.attempt(ctx, req, t, fn)
		if err == nil {
			if i > 0 {
				resp.Fallback = true
				w.emitFallback(ctx, req, targets[0], t, errs)
			}
			return resp, nil
		}
		errs = append(errs, err)
		if ctx.Err() != nil {
			return Response{}, stopped(ctx, err)
		}
		var dce *DataClassError
		if i == 0 && errors.As(err, &dce) {
			return Response{}, err // the router approved a target the guard refuses: a bug upstream, never papered over
		}
		if !fallbackEligible(err) {
			return Response{}, err
		}
	}
	if len(errs) == 1 {
		return Response{}, errs[0]
	}
	return Response{}, errors.Join(errs...)
}

// stopped is what a call whose context ended while err was being made returns: the caller's own reason (ctx.Err(), with the
// cause the caller gave to context.WithCancelCause), then what the attempt said, kept for errors.As (a stream that had already
// delivered output, say). An attempt can turn the ended context into another error (a provider read reported as unavailable,
// release CI 2026-10-27), so the caller is told here, once, for every attempt.
func stopped(ctx context.Context, err error) error {
	reason, cause := ctx.Err(), context.Cause(ctx)
	switch {
	case errors.Is(err, cause):
		return err // says it already (cause is ctx.Err() when the caller gave none)
	case errors.Is(err, reason):
		return fmt.Errorf("%w: %w", err, cause)
	case cause != reason:
		return fmt.Errorf("%w: %w: %w", reason, cause, err)
	}
	return fmt.Errorf("%w: %w", reason, err)
}

func fallbackEligible(err error) bool {
	var (
		qe *QuotaError
		ue *UnavailableError
		de *DataClassError
		ae *APIError
		si *StreamInterruptedError
	)
	switch {
	case errors.As(err, &si):
		return false // output already reached the caller: another provider would duplicate it
	case errors.As(err, &qe), errors.As(err, &ue), errors.As(err, &de):
		return true
	case errors.As(err, &ae):
		return ae.Kind.FallbackEligible()
	}
	return false
}

// scan runs the secret scanner over every outbound part; a finding or a scanner error refuses the call.
func (w *Wrapper) scan(ctx context.Context, req Request) error {
	var found []Finding
	for _, part := range requestParts(req) {
		fs, err := w.scanner.Scan(ctx, part.text)
		if err != nil {
			return fmt.Errorf("llm: secret scan failed, call refused: %w", err)
		}
		for _, f := range fs {
			f.Where = part.where
			found = append(found, f)
		}
	}
	if len(found) == 0 {
		return nil
	}
	rules := make([]string, 0, len(found))
	wheres := make([]string, 0, len(found))
	for _, f := range found {
		rules, wheres = append(rules, f.Rule), append(wheres, f.Where)
	}
	w.emit(ctx, "security.secret_blocked", req, map[string]any{"rules": rules, "where": wheres, "provider": req.Provider, "model": req.Model})
	return &SecretError{Findings: found}
}

// attempt runs the pipeline against one target: availability, data_class guard, cache, slot, quota, call and retry,
// recording.
func (w *Wrapper) attempt(ctx context.Context, req Request, t Target, fn func(Delta) error) (Response, error) {
	p, err := w.reg.Get(ctx, t.Provider)
	if errors.Is(err, providers.ErrNotFound) {
		return Response{}, &UnavailableError{Provider: t.Provider, Reason: "not configured", Cause: err}
	}
	if err != nil {
		// a local failure (the database), not the provider: another provider would hide it (run says an ended context as such)
		return Response{}, fmt.Errorf("llm: read provider %s: %w", t.Provider, err)
	}
	if !p.Enabled {
		return Response{}, &UnavailableError{Provider: p.ID, Reason: "disabled"}
	}

	// data_class guard: private context never reaches a trainable provider or model. It runs before the cache, the
	// slot and the quota reservation (docs/security.md s9 E3), so a refused call consumes nothing.
	class, classErr := w.reg.DataClassOf(ctx, p, t.Model)
	if req.Manifest.Ceiling() == SensitivityPrivate {
		switch {
		case classErr != nil:
			// the model's class cannot be read: it could be trainable, so private context does not go
			return Response{}, w.refuse(ctx, req, p, t, &DataClassError{Provider: p.ID, Model: t.Model, Target: class, Sources: req.Manifest.PrivateSources(),
				Reason: "its data class could not be read (" + classErr.Error() + ")"})
		case class == providers.DataTrainable:
			return Response{}, w.refuse(ctx, req, p, t, &DataClassError{Provider: p.ID, Model: t.Model, Target: providers.DataTrainable, Sources: req.Manifest.PrivateSources()})
		case !p.PrivateEligible():
			return Response{}, w.refuse(ctx, req, p, t, &DataClassError{Provider: p.ID, Model: t.Model, Target: p.DataClass, Sources: req.Manifest.PrivateSources(),
				Reason: "plain http on a private network address without the LAN attestation"})
		}
	}

	ttl := w.ttlFor(req)
	var key string
	if ttl > 0 {
		key = CacheKey(p, t.Model, class, req)
		if c, ok := w.cacheGet(ctx, key); ok {
			return w.serveCached(ctx, req, p, t, c, fn)
		}
	}

	if w.mon != nil && p.Kind == providers.KindHost {
		release, err := w.mon.Acquire(ctx, p.ID)
		if err != nil {
			return Response{}, err
		}
		defer release()
	}
	apiKey, err := w.reg.APIKey(ctx, p)
	if err != nil {
		return Response{}, fmt.Errorf("llm: api key for %s: %w", p.ID, err)
	}
	hc, err := w.reg.HTTPClient(p)
	if err != nil {
		return Response{}, err
	}
	backend, err := w.factory(p, apiKey, hc)
	if err != nil {
		return Response{}, err
	}

	avoid := w.avoidEndpoints(ctx, req, p, t.Model)

	start := w.now()
	var resp Response
	for n := 1; ; n++ {
		if err := w.reserve(ctx, p, t.Model); err != nil {
			return Response{}, err
		}
		emitted := false
		var wfn func(Delta) error
		if fn != nil {
			wfn = func(d Delta) error { emitted = true; return fn(d) }
		}
		r2 := req
		r2.Provider, r2.Model, r2.AvoidEndpoints = p.ID, t.Model, avoid
		if fn == nil {
			resp, err = backend.Complete(ctx, r2)
		} else {
			resp, err = backend.Stream(ctx, r2, wfn)
		}
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		ae := asAPIError(p.ID, err)
		if emitted {
			return Response{}, &StreamInterruptedError{Cause: err}
		}
		if len(avoid) > 0 && ae.Kind == KindNoEndpoint {
			// Skipping the low-uptime endpoints left none that fits the request (a private route's data policy
			// narrows the set further than the uptime list shows): the avoidance never blocks a call, so send it as
			// it would have gone without.
			avoid = nil
			continue
		}
		if w.ledger != nil && ae.Header != nil {
			_ = w.ledger.Record(ctx, p.ID, t.Model, Usage{}, ae.Header) // error responses carry the fresh limits too
		}
		if herr := w.handleAPIError(ctx, p, t, ae, n); herr != nil {
			return Response{}, herr
		}
	}

	resp.Provider = p.ID
	if resp.ModelUsed == "" {
		resp.ModelUsed = t.Model
	}
	if !resp.UsageReported {
		resp.Usage = estimateUsage(req, resp)
	}
	resp.Cost = w.costOf(ctx, p, t.Model, resp)
	resp.Latency = w.now().Sub(start)
	if w.ledger != nil {
		_ = w.ledger.Record(ctx, p.ID, t.Model, resp.Usage, resp.Header)
	}
	w.recordUsage(ctx, p, req, resp, false)
	if ttl > 0 && resp.FinishReason != "length" && resp.FinishReason != "error" && w.cacheable(ctx, resp) {
		w.cachePut(ctx, key, p, class, req, resp, ttl)
	}
	return resp, nil
}

// avoidEndpoints asks the uptime tracker which OpenRouter endpoints of model to skip, and announces a change in that set
// as provider.endpoints_avoided (the provider's detail panel lists the current ones).
func (w *Wrapper) avoidEndpoints(ctx context.Context, req Request, p providers.Provider, model string) []string {
	if w.uptime == nil || p.Flavor != providers.FlavorOpenRouter {
		return nil
	}
	a, changed := w.uptime.Avoid(ctx, p, model)
	if changed {
		w.emit(ctx, "provider.endpoints_avoided", req, map[string]any{"providerId": p.ID, "model": model, "threshold": a.Threshold,
			"avoided": a.Avoided, "keptBest": a.KeptBest})
	}
	if len(a.Avoided) == 0 {
		return nil
	}
	return a.Tags()
}

// cacheable scans the response before it is stored: a completion that echoes a secret is not kept.
func (w *Wrapper) cacheable(ctx context.Context, r Response) bool {
	text := r.Content
	for _, tc := range r.ToolCalls {
		text += "\n" + tc.Arguments
	}
	fs, err := w.scanner.Scan(ctx, text)
	return err == nil && len(fs) == 0
}

func (w *Wrapper) refuse(ctx context.Context, req Request, p providers.Provider, t Target, e *DataClassError) error {
	w.emit(ctx, "security.data_class_refused", req, map[string]any{"provider": p.ID, "model": t.Model, "providerClass": p.DataClass,
		"targetClass": e.Target, "sources": e.Sources, "reason": e.Reason})
	return e
}

// reserve takes a quota slot, sitting out a short wait but never one past the ceiling.
func (w *Wrapper) reserve(ctx context.Context, p providers.Provider, model string) error {
	if w.ledger == nil {
		return nil
	}
	for i := 0; ; i++ {
		ok, retryAt, err := w.ledger.Reserve(ctx, p.ID, model)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		wait := retryAt.Sub(w.now())
		if wait > w.retry.Ceiling || i >= 2 {
			return &QuotaError{Provider: p.ID, Model: model, RetryAt: retryAt, Scope: "quota_window"}
		}
		if wait > 0 {
			if err := w.sleep(ctx, wait); err != nil {
				return err
			}
		}
	}
}

// handleAPIError decides what to do with a failed attempt: nil means "retry" (after sleeping), otherwise the error to return.
func (w *Wrapper) handleAPIError(ctx context.Context, p providers.Provider, t Target, ae *APIError, attempt int) error {
	now := w.now()
	switch {
	case ae.Kind == KindRetention:
		// the organization cannot use this model until it enables data retention: the router leaves it out from now on (until a
		// person clears it), and this call goes on to the next approved target, if any (decision P112)
		if _, err := w.reg.MarkRetentionRequired(ctx, p.ID, t.Model, "api", providers.RetentionRefusal); err != nil {
			return fmt.Errorf("%w (and the model could not be marked: %v)", ae, err)
		}
		return fmt.Errorf("%w; %s", ae, providers.RetentionAdvice)
	case ae.Kind == KindQuota:
		qe := &QuotaError{Provider: p.ID, Model: t.Model, Scope: "credits", Cause: ae}
		model := "" // credits and spend caps are account-wide
		switch {
		case ae.DailyQuota:
			tz := "UTC"
			if p.Flavor == providers.FlavorGemini {
				tz = "America/Los_Angeles" // Gemini daily quotas reset at midnight Pacific
			}
			qe.RetryAt, qe.Scope, model = providers.NextMidnight(now, tz), "daily_quota", t.Model
		case ae.HasRetryAfter:
			qe.RetryAt = now.Add(ae.RetryAfter)
		default:
			qe.RetryAt = now.Add(10 * time.Minute) // no reset is known; re-check later (OpenRouter /key polling will correct it sooner)
		}
		if w.ledger != nil {
			w.ledger.Penalize(ctx, p.ID, model, qe.RetryAt, string(ae.Kind))
		}
		return qe
	case ae.Kind.Retryable():
		delay := w.backoff(attempt)
		if ae.HasRetryAfter && ae.RetryAfter > delay {
			delay = ae.RetryAfter // Retry-After is a minimum
		}
		if delay > w.retry.Ceiling || attempt >= w.retry.MaxAttempts {
			if ae.Kind == KindRateLimit || delay > w.retry.Ceiling {
				qe := &QuotaError{Provider: p.ID, Model: t.Model, RetryAt: now.Add(delay), Scope: "retry-after", Cause: ae}
				if w.ledger != nil {
					w.ledger.Penalize(ctx, p.ID, t.Model, qe.RetryAt, "retry-after")
				}
				return qe
			}
			return ae
		}
		if err := w.sleep(ctx, delay); err != nil {
			return err
		}
		return nil
	}
	return ae
}

// backoff is BaseDelay doubled per attempt with jitter, never above the ceiling.
func (w *Wrapper) backoff(attempt int) time.Duration {
	d := w.retry.BaseDelay << (attempt - 1)
	if d <= 0 || d > w.retry.Ceiling {
		d = w.retry.Ceiling
	}
	if w.retry.Jitter > 0 {
		d += time.Duration(rand.Float64() * w.retry.Jitter * float64(d))
	}
	return d
}

func asAPIError(provider string, err error) *APIError {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae
	}
	return &APIError{Provider: provider, Kind: KindNetwork, Message: err.Error(), Cause: err}
}

func estimateUsage(req Request, resp Response) Usage {
	in, out := 0, len(resp.Content)+len(resp.Reasoning)
	for _, p := range requestParts(req) {
		in += len(p.text)
	}
	for _, tc := range resp.ToolCalls {
		out += len(tc.Arguments) + len(tc.Name)
	}
	return Usage{Input: (in + 3) / 4, Output: (out + 3) / 4} // 4 characters per token: a labeled estimate, not a tokenizer
}

// serveCached answers from llm_cache: no provider call, no quota, zero tokens and cost, a usage row marked cache_hit.
func (w *Wrapper) serveCached(ctx context.Context, req Request, p providers.Provider, t Target, c cached, fn func(Delta) error) (Response, error) {
	resp := Response{Content: c.Content, Reasoning: c.Reasoning, ToolCalls: c.ToolCalls, FinishReason: c.FinishReason, ModelUsed: c.ModelUsed,
		Provider: p.ID, CacheHit: true, UsageReported: true, Cost: Cost{Source: CostEstimated, Known: true}}
	if c.CostKnown {
		resp.EstSavedUSD = c.CostUSD
	}
	if fn != nil {
		if c.Reasoning != "" {
			if err := fn(Delta{Reasoning: c.Reasoning}); err != nil {
				return Response{}, err
			}
		}
		if c.Content != "" {
			if err := fn(Delta{Text: c.Content}); err != nil {
				return Response{}, err
			}
		}
	}
	w.recordUsage(ctx, p, req, resp, true)
	return resp, nil
}

// recordUsage writes the model_usage row. Tokens are "reported" when the provider returned usage and "estimated"
// otherwise; cost_source says whether the cost was reported, computed from the price table, or unknown.
func (w *Wrapper) recordUsage(ctx context.Context, p providers.Provider, req Request, r Response, hit bool) {
	tier := string(p.Tier)
	if m, err := w.reg.Model(ctx, p.ID, r.ModelUsed); err == nil && m.Tier != "" {
		tier = string(m.Tier)
	}
	tokSrc := "reported"
	if !r.UsageReported {
		tokSrc = "estimated"
	}
	costSrc := map[CostSource]string{CostReported: "reported", CostEstimated: "computed_from_table", CostUnknown: "unknown"}[r.Cost.Source]
	var hostID any
	if p.Kind == providers.KindHost {
		hostID = p.ID
	}
	err := w.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO model_usage(ts,run_id,task_id,agent_id,provider,host_id,model,tier,input_tokens,output_tokens,cached_tokens,
			cache_write_tokens,reasoning_tokens,cost_usd,source,cache_hit,purpose,decision_id,latency_ms,cost_source,est_saved_usd)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			ms(w.now()), req.RunID, req.TaskID, req.AgentID, p.ID, hostID, r.ModelUsed, tier, r.Usage.Input, r.Usage.Output, r.Usage.Cached,
			r.Usage.CacheWrite, r.Usage.Reasoning, r.Cost.USD, tokSrc, boolInt(hit), req.Purpose, req.DecisionID, r.Latency.Milliseconds(), costSrc, r.EstSavedUSD)
		return err
	})
	if err != nil {
		w.emit(ctx, "llm.usage_record_failed", req, map[string]any{"provider": p.ID, "model": r.ModelUsed, "error": err.Error()})
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (w *Wrapper) emitFallback(ctx context.Context, req Request, from, to Target, causes []error) {
	cause := ""
	if len(causes) > 0 {
		cause = causes[len(causes)-1].Error()
		var ae *APIError
		var qe *QuotaError
		switch {
		case errors.As(causes[len(causes)-1], &qe):
			cause = "quota:" + qe.Scope
		case errors.As(causes[len(causes)-1], &ae):
			cause = "api:" + string(ae.Kind)
		}
	}
	w.emit(ctx, "routing.fallback", req, map[string]any{"decisionId": req.DecisionID,
		"from": map[string]string{"provider": from.Provider, "model": from.Model}, "to": map[string]string{"provider": to.Provider, "model": to.Model}, "cause": cause})
}

func (w *Wrapper) emit(ctx context.Context, typ string, req Request, data any) {
	if w.bus == nil {
		return
	}
	_, _ = events.Emit(ctx, w.bus, typ, events.Event{ProjectID: req.ProjectID, RunID: req.RunID, TaskID: req.TaskID, AgentID: req.AgentID}, data)
}

var _ Client = (*Wrapper)(nil)

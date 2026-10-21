package llm_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/llm/backends"
	"github.com/kjmaguire/drydock/internal/llm/llmtest"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Wrapper tests run the real Wrapper, Registry, QuotaLedger, SQLite and event bus against PROTOCOL FIXTURES
// (llmtest): local servers speaking the OpenAI chat completions wire protocol.

var t0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// clock is the time source for the Wrapper and the ledger. Sleep advances it instead of waiting, and records the
// requested waits so tests can assert backoff and Retry-After behaviour exactly.
type clock struct {
	mu     sync.Mutex
	t      time.Time
	sleeps []time.Duration
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}
func (c *clock) sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.sleeps = append(c.sleeps, d)
	c.t = c.t.Add(d)
	c.mu.Unlock()
	return nil
}
func (c *clock) slept() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

type env struct {
	t      *testing.T
	ctx    context.Context
	db     *storage.DB
	bus    *events.SQLBus
	reg    *providers.Registry
	ledger *providers.QuotaLedger
	mon    *providers.Monitor
	clk    *clock
	w      *llm.Wrapper
}

func newEnv(t *testing.T, mutate func(*llm.Options)) *env {
	t.Helper()
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 512)
	t.Cleanup(func() { bus.Close(); db.Close() })
	reg := providers.NewRegistry(db, providers.RegistryOptions{Secrets: providers.NewMemorySecretStoreForTests(), Bus: bus})
	clk := &clock{t: t0}
	ledger := providers.NewQuotaLedger(reg, nil, bus, providers.QuotaOptions{Now: clk.now})
	mon := providers.NewMonitor(reg, providers.NewProber(reg, bus), bus, providers.MonitorOptions{Ledger: ledger})
	o := llm.Options{Registry: reg, DB: db, Factory: backends.Factory, Scanner: llm.NopScanner{}, Ledger: ledger, Monitor: mon, Bus: bus,
		Retry: llm.RetryPolicy{MaxAttempts: 4, BaseDelay: 100 * time.Millisecond, Ceiling: 30 * time.Second, Jitter: -1},
		Now:   clk.now, Sleep: clk.sleep}
	if mutate != nil {
		mutate(&o)
	}
	w, err := llm.NewWrapper(o)
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, ctx: ctx, db: db, bus: bus, reg: reg, ledger: ledger, mon: mon, clk: clk, w: w}
}

func (e *env) provider(np providers.NewProvider) providers.Provider {
	e.t.Helper()
	if np.Kind == "" {
		np.Kind = providers.KindDirect
	}
	if np.DataClass == "" {
		np.DataClass = providers.DataPrivate
	}
	if np.Tier == "" {
		np.Tier = providers.TierCheap
	}
	if np.APIKey == "" && np.Kind != providers.KindHost {
		np.APIKey = "sk-test"
	}
	p, err := e.reg.Create(e.ctx, np)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *env) events(typ string) []events.Event {
	e.t.Helper()
	evs, err := e.bus.Replay(e.ctx, 0, events.Filter{Types: []string{typ}}, 1000)
	if err != nil {
		e.t.Fatal(err)
	}
	return evs
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.Reader().QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

type usageRow struct {
	Provider, Model, Tier, Source, CostSource, Purpose, DecisionID string
	In, Out, Cached, CacheWrite                                    int
	Cost, Saved                                                    float64
	Hit                                                            bool
}

func (e *env) usage() []usageRow {
	e.t.Helper()
	rows, err := e.db.Reader().Query(`SELECT provider,model,tier,source,cost_source,purpose,decision_id,input_tokens,output_tokens,cached_tokens,cache_write_tokens,cost_usd,est_saved_usd,cache_hit FROM model_usage ORDER BY id`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []usageRow
	for rows.Next() {
		var r usageRow
		var hit int
		if err := rows.Scan(&r.Provider, &r.Model, &r.Tier, &r.Source, &r.CostSource, &r.Purpose, &r.DecisionID, &r.In, &r.Out, &r.Cached, &r.CacheWrite, &r.Cost, &r.Saved, &hit); err != nil {
			e.t.Fatal(err)
		}
		r.Hit = hit == 1
		out = append(out, r)
	}
	return out
}

func okHandler(content string, u *llmtest.Usage) llmtest.Handler {
	return func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.Respond(w, c, llmtest.Completion{Model: "m1", Content: content, Usage: u})
	}
}

var privateManifest = &llm.Manifest{Entries: []llm.ManifestEntry{{SourceType: "file_range", SourceRef: "src/a.go", Sensitivity: llm.SensitivityPrivate}, {SourceType: "task", Sensitivity: llm.SensitivityPublicDocs}}}
var publicManifest = &llm.Manifest{Entries: []llm.ManifestEntry{{SourceType: "pack_doc", SourceRef: "cloudflare/workers", Sensitivity: llm.SensitivityPublicDocs}}}

func req(p providers.Provider, m *llm.Manifest) llm.Request {
	return llm.Request{Provider: p.ID, Model: "m1", MaxTokens: 100, Manifest: m, Messages: []llm.Message{{Role: "user", Content: "question"}}}
}

func asQuota(t *testing.T, err error) *llm.QuotaError {
	t.Helper()
	var qe *llm.QuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("want *QuotaError, got %v", err)
	}
	if !errors.Is(err, llm.ErrQuota) {
		t.Fatal("QuotaError does not match ErrQuota")
	}
	return qe
}

// ---------------------------------------------------------------- data_class guard (bug-level invariant)

func TestDataClassGuardRefusesPrivateContextToTrainableProvider(t *testing.T) {
	e := newEnv(t, nil)
	var hits atomic.Int32
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		okHandler("x", nil)(c, w, r)
	})
	trainable := e.provider(providers.NewProvider{Name: "free", BaseURL: srv.URL + "/v1", DataClass: providers.DataTrainable, Tier: providers.TierFree})
	e.ledger.Configure(e.ctx, providers.Window{ProviderID: trainable.ID, Kind: providers.WindowRPD, Limit: 10})

	_, err := e.w.Complete(e.ctx, req(trainable, privateManifest))
	var dce *llm.DataClassError
	if !errors.Is(err, llm.ErrDataClass) || !errors.As(err, &dce) {
		t.Fatalf("err = %v, want a DataClassError", err)
	}
	if dce.Target != providers.DataTrainable || len(dce.Sources) != 1 || dce.Sources[0] != "file_range:src/a.go" {
		t.Fatalf("refusal details %+v", dce)
	}
	if hits.Load() != 0 {
		t.Fatal("private context reached a trainable provider")
	}
	if snap, _ := e.ledger.Snapshot(e.ctx, trainable.ID); snap[0].Used != 0 {
		t.Fatalf("a refused call consumed quota: %+v", snap[0])
	}
	if n := e.count(`SELECT count(*) FROM model_usage`); n != 0 {
		t.Fatalf("a refused call wrote %d usage rows", n)
	}
	evs := e.events("security.data_class_refused")
	if len(evs) != 1 || !strings.Contains(string(evs[0].Data), "file_range:src/a.go") {
		t.Fatalf("refusal event %v", evs)
	}
	// The same provider accepts public-docs-only context (CONSULT).
	if _, err := e.w.Complete(e.ctx, req(trainable, publicManifest)); err != nil {
		t.Fatalf("public docs to a trainable provider: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits %d", hits.Load())
	}
}

func TestDataClassGuardFailsClosedOnMissingOrUntaggedManifest(t *testing.T) {
	e := newEnv(t, nil)
	srv := llmtest.New(t, okHandler("x", nil))
	trainable := e.provider(providers.NewProvider{Name: "free", BaseURL: srv.URL + "/v1", DataClass: providers.DataTrainable, Tier: providers.TierFree})
	cases := map[string]*llm.Manifest{
		"nil manifest":       nil,
		"empty manifest":     {},
		"untagged entry":     {Entries: []llm.ManifestEntry{{SourceType: "file_range"}}},
		"one private of two": {Entries: []llm.ManifestEntry{{SourceType: "doc", Sensitivity: llm.SensitivityPublicDocs}, {SourceType: "diff", Sensitivity: llm.SensitivityPrivate}}},
		"unknown class":      {Entries: []llm.ManifestEntry{{SourceType: "doc", Sensitivity: "internal"}}},
	}
	for name, m := range cases {
		if _, err := e.w.Complete(e.ctx, req(trainable, m)); !errors.Is(err, llm.ErrDataClass) {
			t.Fatalf("%s: err = %v, want ErrDataClass", name, err)
		}
	}
	if len(srv.Calls()) != 0 {
		t.Fatal("a request reached the provider")
	}
	// Synthetic probe content (no user data) is allowed.
	probe := &llm.Manifest{Entries: []llm.ManifestEntry{{SourceType: "probe", Sensitivity: llm.SensitivitySynthetic}}}
	if _, err := e.w.Complete(e.ctx, req(trainable, probe)); err != nil {
		t.Fatalf("synthetic probe: %v", err)
	}
}

func TestDataClassGuardAlsoCoversTrainableModelsOnPrivateProviders(t *testing.T) {
	e := newEnv(t, nil)
	srv := llmtest.New(t, okHandler("x", nil))
	or := e.provider(providers.NewProvider{Name: "or", Kind: providers.KindAggregator, BaseURL: srv.URL + "/v1", Flavor: providers.FlavorOpenRouter})
	// A ":free" model is trainable even on a private aggregator route.
	r := req(or, privateManifest)
	r.Model = "cohere/north-mini-code:free"
	if _, err := e.w.Complete(e.ctx, r); !errors.Is(err, llm.ErrDataClass) {
		t.Fatalf("free model with private context: %v", err)
	}
	// So is a model the catalog marks trainable.
	e.reg.UpsertModels(e.ctx, or.ID, []providers.Model{{ID: "odd/model", DataClass: providers.DataTrainable, Tier: providers.TierFree}})
	r.Model = "odd/model"
	if _, err := e.w.Complete(e.ctx, r); !errors.Is(err, llm.ErrDataClass) {
		t.Fatalf("catalog-trainable model with private context: %v", err)
	}
	if len(srv.Calls()) != 0 {
		t.Fatal("private context reached a trainable model")
	}
	// The same provider with a paid model and a private manifest is fine, and carries the privacy filter.
	r.Model = "paid/model"
	if _, err := e.w.Complete(e.ctx, r); err != nil {
		t.Fatal(err)
	}
	prov := srv.Calls()[0].Body["provider"].(map[string]any)
	if prov["data_collection"] != "deny" || prov["zdr"] != true {
		t.Fatalf("private OpenRouter route without the privacy filter: %v", prov)
	}
}

// TestDataClassInvariantHoldsForEveryCombination is the property check: whatever the manifest, provider class and
// model class, a private-or-untagged manifest never produces a request to a trainable target.
func TestDataClassInvariantHoldsForEveryCombination(t *testing.T) {
	e := newEnv(t, nil)
	type target struct {
		name  string
		p     providers.Provider
		model string
		srv   *llmtest.Server
		train bool
	}
	mk := func(name string, np providers.NewProvider, model string, train bool) target {
		srv := llmtest.New(t, okHandler("x", nil))
		np.Name, np.BaseURL = name, srv.URL+"/v1"
		return target{name: name, p: e.provider(np), model: model, srv: srv, train: train}
	}
	targets := []target{
		mk("private-direct", providers.NewProvider{}, "m1", false),
		mk("trainable-direct", providers.NewProvider{DataClass: providers.DataTrainable, Tier: providers.TierFree}, "m1", true),
		mk("or-paid", providers.NewProvider{Kind: providers.KindAggregator, Flavor: providers.FlavorOpenRouter}, "a/b", false),
		mk("or-free", providers.NewProvider{Kind: providers.KindAggregator, Flavor: providers.FlavorOpenRouter}, "a/b:free", true),
		mk("or-trainable", providers.NewProvider{Kind: providers.KindAggregator, Flavor: providers.FlavorOpenRouter, DataClass: providers.DataTrainable}, "a/b", true),
	}
	manifests := map[string]*llm.Manifest{
		"nil": nil, "empty": {}, "private": privateManifest, "untagged": {Entries: []llm.ManifestEntry{{SourceType: "x"}}},
		"public": publicManifest, "synthetic": {Entries: []llm.ManifestEntry{{SourceType: "p", Sensitivity: llm.SensitivitySynthetic}}},
	}
	for mname, m := range manifests {
		private := m.Ceiling() == llm.SensitivityPrivate
		for _, tg := range targets {
			before := len(tg.srv.Calls())
			r := req(tg.p, m)
			r.Model = tg.model
			_, err := e.w.Complete(e.ctx, r)
			reached := len(tg.srv.Calls()) > before
			switch {
			case private && tg.train:
				if reached || !errors.Is(err, llm.ErrDataClass) {
					t.Errorf("INVARIANT VIOLATED: manifest %s -> %s: reached=%v err=%v", mname, tg.name, reached, err)
				}
			default:
				if err != nil || !reached {
					t.Errorf("manifest %s -> %s should be allowed: reached=%v err=%v", mname, tg.name, reached, err)
				}
			}
		}
	}
}

func TestPrimaryDataClassViolationNeverFallsBack(t *testing.T) {
	e := newEnv(t, nil)
	good := llmtest.New(t, okHandler("x", nil))
	bad := llmtest.New(t, okHandler("x", nil))
	trainable := e.provider(providers.NewProvider{Name: "free", BaseURL: bad.URL + "/v1", DataClass: providers.DataTrainable, Tier: providers.TierFree})
	private := e.provider(providers.NewProvider{Name: "priv", BaseURL: good.URL + "/v1"})
	r := req(trainable, privateManifest)
	r.Fallbacks = []llm.Target{{Provider: private.ID, Model: "m1"}}
	if _, err := e.w.Complete(e.ctx, r); !errors.Is(err, llm.ErrDataClass) {
		t.Fatalf("err = %v", err)
	}
	if len(good.Calls()) != 0 || len(bad.Calls()) != 0 {
		t.Fatal("a primary violation was papered over by a fallback")
	}
	if len(e.events("routing.fallback")) != 0 {
		t.Fatal("fallback event after a guard refusal")
	}
}

func TestTrainableFallbackIsSkippedForPrivateContext(t *testing.T) {
	e := newEnv(t, nil)
	primary := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.WriteError(w, 401, nil, map[string]any{"error": map[string]any{"message": "bad key"}})
	})
	trainSrv := llmtest.New(t, okHandler("x", nil))
	privSrv := llmtest.New(t, okHandler("from private fallback", nil))
	p1 := e.provider(providers.NewProvider{Name: "primary", BaseURL: primary.URL + "/v1"})
	tr := e.provider(providers.NewProvider{Name: "trainable", BaseURL: trainSrv.URL + "/v1", DataClass: providers.DataTrainable, Tier: providers.TierFree})
	p3 := e.provider(providers.NewProvider{Name: "private-b", BaseURL: privSrv.URL + "/v1"})
	r := req(p1, privateManifest)
	r.Fallbacks = []llm.Target{{Provider: tr.ID, Model: "m1"}, {Provider: p3.ID, Model: "m1"}}
	resp, err := e.w.Complete(e.ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "from private fallback" || !resp.Fallback || resp.Provider != p3.ID {
		t.Fatalf("resp %+v", resp)
	}
	if len(trainSrv.Calls()) != 0 {
		t.Fatal("private context reached the trainable fallback")
	}
}

// ---------------------------------------------------------------- secrets

func TestSecretScannerBlocksOutboundPrompts(t *testing.T) {
	secret := "sk-live-ABCDEF0123456789"
	e := newEnv(t, func(o *llm.Options) { o.Scanner = llm.NewExactValueScanner(secret) })
	srv := llmtest.New(t, okHandler("x", nil))
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: srv.URL + "/v1"})
	for _, tc := range []struct {
		name  string
		mod   func(*llm.Request)
		where string
	}{
		{"system", func(r *llm.Request) { r.System = []llm.Block{{Text: "key is " + secret}} }, "system[0]"},
		{"message", func(r *llm.Request) {
			r.Messages = append(r.Messages, llm.Message{Role: "user", Content: "use " + secret})
		}, "messages[1]"},
		{"tool args", func(r *llm.Request) {
			r.Messages = append(r.Messages, llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "1", Name: "x", Arguments: `{"k":"` + secret + `"}`}}})
		}, "messages[1]"},
		{"tool description", func(r *llm.Request) { r.Tools = []llm.ToolDef{{Name: "t", Description: secret}} }, "tools[0]"},
	} {
		r := req(p, publicManifest)
		tc.mod(&r)
		_, err := e.w.Complete(e.ctx, r)
		var se *llm.SecretError
		if !errors.Is(err, llm.ErrSecretBlocked) || !errors.As(err, &se) || !strings.Contains(se.Findings[0].Where, tc.where[:6]) {
			t.Fatalf("%s: err = %v", tc.name, err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("%s: the secret value is in the error message", tc.name)
		}
	}
	if len(srv.Calls()) != 0 {
		t.Fatal("a prompt containing a secret was sent")
	}
	evs := e.events("security.secret_blocked")
	if len(evs) != 4 {
		t.Fatalf("%d secret_blocked events", len(evs))
	}
	for _, ev := range evs {
		if strings.Contains(string(ev.Data), secret) {
			t.Fatal("the secret value is in an event")
		}
	}
	if _, err := e.w.Complete(e.ctx, req(p, publicManifest)); err != nil {
		t.Fatalf("clean prompt: %v", err)
	}
}

type failingScanner struct{}

func (failingScanner) Scan(context.Context, string) ([]llm.Finding, error) {
	return nil, errors.New("scanner crashed")
}

func TestScannerErrorFailsClosedAndScannerIsRequired(t *testing.T) {
	e := newEnv(t, func(o *llm.Options) { o.Scanner = failingScanner{} })
	srv := llmtest.New(t, okHandler("x", nil))
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: srv.URL + "/v1"})
	if _, err := e.w.Complete(e.ctx, req(p, publicManifest)); err == nil || !strings.Contains(err.Error(), "scan failed") || len(srv.Calls()) != 0 {
		t.Fatalf("a scanner failure must refuse the call: %v", err)
	}
	if _, err := llm.NewWrapper(llm.Options{Registry: e.reg, DB: e.db, Factory: backends.Factory}); err == nil {
		t.Fatal("NewWrapper accepted a nil Scanner")
	}
}

func TestResponseWithASecretIsNotCached(t *testing.T) {
	secret := "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123"
	e := newEnv(t, func(o *llm.Options) { o.Scanner = llm.NewExactValueScanner(secret) })
	srv := llmtest.New(t, okHandler("here is the token "+secret, nil))
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: srv.URL + "/v1"})
	r := req(p, publicManifest)
	r.Purpose = "summarize"
	for i := 0; i < 2; i++ {
		if _, err := e.w.Complete(e.ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if len(srv.Calls()) != 2 || e.count(`SELECT count(*) FROM llm_cache`) != 0 {
		t.Fatalf("calls=%d cache rows=%d", len(srv.Calls()), e.count(`SELECT count(*) FROM llm_cache`))
	}
}

// ---------------------------------------------------------------- response cache

func cacheEnv(t *testing.T) (*env, providers.Provider, *llmtest.Server) {
	e := newEnv(t, nil)
	cost := 0.02
	srv := llmtest.New(t, okHandler("cached answer", &llmtest.Usage{PromptTokens: 800, CompletionTokens: 40, Cost: &cost}))
	p := e.provider(providers.NewProvider{Name: "or", Kind: providers.KindAggregator, BaseURL: srv.URL + "/v1", Flavor: providers.FlavorOpenRouter})
	return e, p, srv
}

func TestCacheHitSkipsProviderAndRecordsASavedUsageRow(t *testing.T) {
	e, p, srv := cacheEnv(t)
	r := req(p, privateManifest)
	r.Purpose, r.PackVersion, r.ProjectID, r.DecisionID = "classify", "pack-1", "proj-1", "dec-1"
	first, err := e.w.Complete(e.ctx, r)
	if err != nil || first.CacheHit {
		t.Fatalf("first: %v %+v", err, first)
	}
	second, err := e.w.Complete(e.ctx, r)
	if err != nil || !second.CacheHit || second.Content != "cached answer" {
		t.Fatalf("second: %v %+v", err, second)
	}
	if len(srv.Calls()) != 1 {
		t.Fatalf("provider called %d times, want 1", len(srv.Calls()))
	}
	if second.Usage != (llm.Usage{}) || second.Cost.USD != 0 || second.EstSavedUSD != 0.02 {
		t.Fatalf("hit accounting: %+v", second)
	}
	rows := e.usage()
	if len(rows) != 2 {
		t.Fatalf("%d usage rows", len(rows))
	}
	if rows[0].Hit || rows[0].In != 800 || rows[0].Cost != 0.02 || rows[0].CostSource != "reported" || rows[0].Source != "reported" {
		t.Fatalf("fill row %+v", rows[0])
	}
	if !rows[1].Hit || rows[1].In != 0 || rows[1].Out != 0 || rows[1].Cost != 0 || rows[1].Saved != 0.02 || rows[1].Purpose != "classify" || rows[1].DecisionID != "dec-1" {
		t.Fatalf("hit row %+v", rows[1])
	}
	if e.count(`SELECT hits FROM llm_cache`) != 1 {
		t.Fatal("hit counter not incremented")
	}
}

func TestCacheMissesOnAnyKeyComponent(t *testing.T) {
	e, p, srv := cacheEnv(t)
	base := req(p, publicManifest)
	base.Purpose, base.PackVersion, base.ProjectID = "classify", "pack-1", "proj-1"
	if _, err := e.w.Complete(e.ctx, base); err != nil {
		t.Fatal(err)
	}
	variants := map[string]func(*llm.Request){
		"model":        func(r *llm.Request) { r.Model = "m2" },
		"message":      func(r *llm.Request) { r.Messages[0].Content = "different" },
		"system":       func(r *llm.Request) { r.System = []llm.Block{{Text: "s"}} },
		"pack version": func(r *llm.Request) { r.PackVersion = "pack-2" },
		"project":      func(r *llm.Request) { r.ProjectID = "proj-2" },
		"effort":       func(r *llm.Request) { r.Effort = llm.EffortHigh },
		"max tokens":   func(r *llm.Request) { r.MaxTokens = 7 },
		"tools":        func(r *llm.Request) { r.Tools = []llm.ToolDef{{Name: "t"}} },
	}
	n := 1
	for name, mod := range variants {
		r := base
		r.Messages = append([]llm.Message(nil), base.Messages...)
		mod(&r)
		resp, err := e.w.Complete(e.ctx, r)
		if err != nil || resp.CacheHit {
			t.Fatalf("%s: err=%v hit=%v (the key ignores this field)", name, err, resp.CacheHit)
		}
		n++
		if len(srv.Calls()) != n {
			t.Fatalf("%s: %d calls", name, len(srv.Calls()))
		}
	}
	// Whitespace and line endings are normalized, so these are the same request.
	r := base
	r.Messages = []llm.Message{{Role: "user", Content: "question\r\n  "}}
	if resp, _ := e.w.Complete(e.ctx, r); !resp.CacheHit {
		t.Fatal("normalized-equal request missed the cache")
	}
}

func TestCacheTTLExpiry(t *testing.T) {
	e, p, srv := cacheEnv(t)
	r := req(p, publicManifest)
	r.Purpose = "probe" // default TTL 10 minutes
	e.w.Complete(e.ctx, r)
	e.clk.add(9 * time.Minute)
	if resp, _ := e.w.Complete(e.ctx, r); !resp.CacheHit {
		t.Fatal("entry expired early")
	}
	e.clk.add(2 * time.Minute)
	resp, err := e.w.Complete(e.ctx, r)
	if err != nil || resp.CacheHit || len(srv.Calls()) != 2 {
		t.Fatalf("entry served past its TTL: hit=%v calls=%d err=%v", resp.CacheHit, len(srv.Calls()), err)
	}
	// The refill is live again, and the expired original can be purged.
	if resp, _ := e.w.Complete(e.ctx, r); !resp.CacheHit {
		t.Fatal("refill not cached")
	}
	e.clk.add(time.Hour)
	if n, err := e.w.PurgeExpiredCache(e.ctx); err != nil || n != 1 {
		t.Fatalf("purge removed %d: %v", n, err)
	}
	// An explicit per-request TTL overrides the purpose default.
	r.CacheTTL = time.Hour
	e.w.Complete(e.ctx, r)
	e.clk.add(50 * time.Minute)
	if resp, _ := e.w.Complete(e.ctx, r); !resp.CacheHit {
		t.Fatal("explicit TTL ignored")
	}
}

func TestSideEffectingAndUnlistedCallsAreNeverCached(t *testing.T) {
	e, p, srv := cacheEnv(t)
	r := req(p, publicManifest)

	r.Purpose, r.SideEffecting = "classify", true // an allowlisted purpose, but side-effecting
	for i := 0; i < 2; i++ {
		resp, err := e.w.Complete(e.ctx, r)
		if err != nil || resp.CacheHit {
			t.Fatalf("side-effecting call served from cache: %v", err)
		}
	}
	r.CacheTTL = time.Hour // even an explicit TTL cannot override
	e.w.Complete(e.ctx, r)
	if e.count(`SELECT count(*) FROM llm_cache`) != 0 {
		t.Fatal("a side-effecting response was written to the cache")
	}

	r.SideEffecting, r.CacheTTL, r.Purpose = false, 0, "implement" // not on the allowlist
	for i := 0; i < 2; i++ {
		if resp, _ := e.w.Complete(e.ctx, r); resp.CacheHit {
			t.Fatal("a purpose off the allowlist was cached")
		}
	}
	r.Purpose, r.NoCache = "classify", true
	e.w.Complete(e.ctx, r)
	if e.count(`SELECT count(*) FROM llm_cache`) != 0 {
		t.Fatal("NoCache request was stored")
	}
	if len(srv.Calls()) != 6 {
		t.Fatalf("calls %d", len(srv.Calls()))
	}
}

func TestCacheServesStreamsAndPurgesWithProject(t *testing.T) {
	e, p, srv := cacheEnv(t)
	r := req(p, publicManifest)
	r.Purpose, r.ProjectID = "summarize", "proj-9"
	e.w.Complete(e.ctx, r)
	var got strings.Builder
	resp, err := e.w.Stream(e.ctx, r, func(d llm.Delta) error { got.WriteString(d.Text); return nil })
	if err != nil || !resp.CacheHit || got.String() != "cached answer" || len(srv.Calls()) != 1 {
		t.Fatalf("stream from cache: %v hit=%v text=%q calls=%d", err, resp.CacheHit, got.String(), len(srv.Calls()))
	}
	if err := e.w.PurgeProjectCache(e.ctx, "proj-9"); err != nil || e.count(`SELECT count(*) FROM llm_cache`) != 0 {
		t.Fatalf("project purge: %v", err)
	}
}

func TestCacheDoesNotCrossDataClass(t *testing.T) {
	// The same prompt to a private and a trainable provider never shares an entry: the data class is in the key.
	e := newEnv(t, nil)
	srv := llmtest.New(t, okHandler("x", nil))
	priv := e.provider(providers.NewProvider{Name: "a", BaseURL: srv.URL + "/v1"})
	train := e.provider(providers.NewProvider{Name: "b", BaseURL: srv.URL + "/v1", DataClass: providers.DataTrainable, Tier: providers.TierFree})
	kp := llm.CacheKey(priv, "m1", priv.DataClass, req(priv, publicManifest))
	kt := llm.CacheKey(train, "m1", train.DataClass, req(train, publicManifest))
	if kp == kt {
		t.Fatal("cache key ignores provider and data class")
	}
}

// ---------------------------------------------------------------- quota and Retry-After

func TestQuotaIsReservedBeforeTheCallAndRecordedAfter(t *testing.T) {
	e := newEnv(t, nil)
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.Respond(w, c, llmtest.Completion{Model: "m1", Content: "ok", Usage: &llmtest.Usage{PromptTokens: 900, CompletionTokens: 100},
			Header: map[string]string{"x-ratelimit-limit-requests": "100", "x-ratelimit-remaining-requests": "98", "x-ratelimit-reset-requests": "30s"}})
	})
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: srv.URL + "/v1"})
	e.ledger.Configure(e.ctx, providers.Window{ProviderID: p.ID, Kind: providers.WindowTPM, Limit: 1500})
	e.ledger.Configure(e.ctx, providers.Window{ProviderID: p.ID, Kind: providers.WindowRPD, Limit: 3})
	if _, err := e.w.Complete(e.ctx, req(p, publicManifest)); err != nil {
		t.Fatal(err)
	}
	snap, _ := e.ledger.Snapshot(e.ctx, p.ID)
	got := map[providers.WindowKind]providers.Window{}
	for _, w := range snap {
		got[w.Kind+providers.WindowKind(w.Model)] = w
	}
	if got["rpd"].Used != 1 || got["tpm"].Used != 1000 {
		t.Fatalf("windows %+v", snap)
	}
	if w := got["rpmm1"]; w.Source != providers.SourceHeaders || w.Limit != 100 || w.Used != 2 {
		t.Fatalf("headers not recorded into the ledger: %+v", snap)
	}
	e.w.Complete(e.ctx, req(p, publicManifest)) // tpm now 2000 >= 1500
	_, err := e.w.Complete(e.ctx, req(p, publicManifest))
	qe := asQuota(t, err)
	if qe.Scope != "quota_window" || qe.Provider != p.ID || !qe.RetryAt.After(e.clk.now()) {
		t.Fatalf("quota error %+v", qe)
	}
	if len(srv.Calls()) != 2 {
		t.Fatalf("the exhausted call still reached the provider (%d calls)", len(srv.Calls()))
	}
}

func TestShortQuotaWaitIsSatOutLongOneIsATypedError(t *testing.T) {
	e := newEnv(t, func(o *llm.Options) { o.Retry.Ceiling = 5 * time.Second })
	srv := llmtest.New(t, okHandler("ok", nil))
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: srv.URL + "/v1"})
	// A header-learned window that frees in 2 seconds: within the ceiling, so the Wrapper waits.
	h := http.Header{}
	h.Set("anthropic-ratelimit-requests-limit", "10")
	h.Set("anthropic-ratelimit-requests-remaining", "0")
	h.Set("anthropic-ratelimit-requests-reset", t0.Add(2*time.Second).Format(time.RFC3339))
	e.ledger.Record(e.ctx, p.ID, "m1", llm.Usage{}, h)
	if _, err := e.w.Complete(e.ctx, req(p, publicManifest)); err != nil {
		t.Fatal(err)
	}
	if s := e.clk.slept(); len(s) != 1 || s[0] != 2*time.Second {
		t.Fatalf("slept %v, want one 2s wait", s)
	}
	// A window that frees in an hour: beyond the ceiling, so a QuotaError with the retry time and no call.
	h.Set("anthropic-ratelimit-requests-remaining", "0")
	h.Set("anthropic-ratelimit-requests-reset", e.clk.now().Add(time.Hour).Format(time.RFC3339))
	e.ledger.Record(e.ctx, p.ID, "m1", llm.Usage{}, h)
	before := len(srv.Calls())
	_, err := e.w.Complete(e.ctx, req(p, publicManifest))
	qe := asQuota(t, err)
	if qe.RetryAt.Sub(e.clk.now()) < 59*time.Minute || len(srv.Calls()) != before {
		t.Fatalf("qe=%+v calls=%d", qe, len(srv.Calls())-before)
	}
}

func TestRetryAfterIsAMinimumAndRetriesSucceed(t *testing.T) {
	e := newEnv(t, nil)
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		if c.N <= 2 {
			llmtest.WriteError(w, 429, map[string]string{"retry-after-ms": "2500"}, map[string]any{"error": map[string]any{"message": "slow down"}})
			return
		}
		llmtest.Respond(w, c, llmtest.Completion{Model: "m1", Content: "finally", Usage: &llmtest.Usage{PromptTokens: 10, CompletionTokens: 2}})
	})
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: srv.URL + "/v1"})
	e.ledger.Configure(e.ctx, providers.Window{ProviderID: p.ID, Kind: providers.WindowRPD, Limit: 100})
	resp, err := e.w.Complete(e.ctx, req(p, publicManifest))
	if err != nil || resp.Content != "finally" {
		t.Fatalf("%v %+v", err, resp)
	}
	if s := e.clk.slept(); len(s) != 2 || s[0] != 2500*time.Millisecond || s[1] != 2500*time.Millisecond {
		t.Fatalf("slept %v: Retry-After (2.5s) is a minimum above the 100ms/200ms backoff", s)
	}
	if len(srv.Calls()) != 3 {
		t.Fatalf("%d calls", len(srv.Calls()))
	}
	// Every attempt, including retries, counts against the request window.
	if snap, _ := e.ledger.Snapshot(e.ctx, p.ID); snap[0].Used != 3 {
		t.Fatalf("rpd used %v, want 3", snap[0].Used)
	}
	if rows := e.usage(); len(rows) != 1 {
		t.Fatalf("%d usage rows: only the successful call is usage", len(rows))
	}
}

func TestExponentialBackoffWithoutRetryAfterThenGivesUp(t *testing.T) {
	e := newEnv(t, nil)
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.AnthropicError(w, 529, nil, "overloaded_error", "Overloaded")
	})
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: srv.URL + "/v1"})
	_, err := e.w.Complete(e.ctx, req(p, publicManifest))
	var ae *llm.APIError
	if !errors.As(err, &ae) || ae.Kind != llm.KindOverloaded {
		t.Fatalf("err = %v, want the overloaded APIError after the attempts run out", err)
	}
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}
	got := e.clk.slept()
	if len(got) != 3 {
		t.Fatalf("slept %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("backoff %v, want %v", got, want)
		}
	}
	if len(srv.Calls()) != 4 {
		t.Fatalf("%d calls, want MaxAttempts=4", len(srv.Calls()))
	}
}

func TestRetryAfterBeyondTheCeilingBecomesAQuotaErrorAndBlocksTheProvider(t *testing.T) {
	e := newEnv(t, nil)
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.WriteError(w, 429, map[string]string{"Retry-After": "120"}, map[string]any{"error": map[string]any{"message": "rate"}})
	})
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: srv.URL + "/v1"})
	_, err := e.w.Complete(e.ctx, req(p, publicManifest))
	qe := asQuota(t, err)
	if qe.Scope != "retry-after" || !qe.RetryAt.Equal(t0.Add(120*time.Second)) {
		t.Fatalf("quota error %+v", qe)
	}
	var ae *llm.APIError
	if !errors.As(err, &ae) || ae.Kind != llm.KindRateLimit {
		t.Fatal("the provider error is not reachable through Unwrap")
	}
	if len(e.clk.slept()) != 0 || len(srv.Calls()) != 1 {
		t.Fatalf("slept %v, calls %d: a wait past the ceiling must not sleep or retry", e.clk.slept(), len(srv.Calls()))
	}
	// The ledger now holds the provider's model until the retry time: the next call never leaves the machine.
	_, err = e.w.Complete(e.ctx, req(p, publicManifest))
	qe2 := asQuota(t, err)
	if !qe2.RetryAt.Equal(qe.RetryAt) || len(srv.Calls()) != 1 {
		t.Fatalf("second call: %+v calls=%d", qe2, len(srv.Calls()))
	}
	if len(e.events("provider.quota_exhausted")) < 1 {
		t.Fatal("no provider.quota_exhausted event")
	}
	e.clk.add(121 * time.Second)
	if ok, _ := e.ledger.Check(e.ctx, p.ID, "m1"); !ok {
		t.Fatal("block did not lift at the retry time")
	}
}

func TestNonRetryableQuotaErrorsStopImmediately(t *testing.T) {
	cases := []struct {
		name   string
		flavor providers.Flavor
		status int
		hdr    map[string]string
		body   any
		scope  string
		check  func(t *testing.T, e *env, qe *llm.QuotaError)
	}{
		{"insufficient_quota", providers.FlavorOpenAI, 429, nil, map[string]any{"error": map[string]any{"message": "quota", "code": "insufficient_quota"}}, "credits", nil},
		{"openrouter credits", providers.FlavorOpenRouter, 402, nil, map[string]any{"error": map[string]any{"code": 402, "message": "Insufficient credits"}}, "credits", nil},
		{"gemini daily quota resets at midnight Pacific", providers.FlavorGemini, 429, nil, map[string]any{"error": map[string]any{"code": 429, "status": "RESOURCE_EXHAUSTED", "message": "quota",
			"details": []any{map[string]any{"@type": "type.googleapis.com/google.rpc.QuotaFailure", "violations": []any{map[string]any{"quotaId": "GenerateRequestsPerDayPerProjectPerModel-FreeTier"}}},
				map[string]any{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "3s"}}}}, "daily_quota",
			func(t *testing.T, e *env, qe *llm.QuotaError) {
				// 2026-10-08 12:00Z is 05:00 PDT; the next midnight Pacific is 2026-10-09 07:00Z, despite the 3s retryDelay.
				if !qe.RetryAt.Equal(time.Date(2026, 10, 9, 7, 0, 0, 0, time.UTC)) {
					t.Fatalf("retry at %s", qe.RetryAt)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, nil)
			srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
				llmtest.WriteError(w, tc.status, tc.hdr, tc.body)
			})
			np := providers.NewProvider{Name: "p", BaseURL: srv.URL + "/v1", Flavor: tc.flavor}
			if tc.flavor == providers.FlavorGemini {
				np.DataClass, np.Tier = providers.DataTrainable, providers.TierFree
			}
			p := e.provider(np)
			r := req(p, publicManifest)
			_, err := e.w.Complete(e.ctx, r)
			qe := asQuota(t, err)
			if qe.Scope != tc.scope || len(srv.Calls()) != 1 || len(e.clk.slept()) != 0 {
				t.Fatalf("scope=%s calls=%d slept=%v", qe.Scope, len(srv.Calls()), e.clk.slept())
			}
			if tc.check != nil {
				tc.check(t, e, qe)
			}
			if ok, _ := e.ledger.Check(e.ctx, p.ID, "m1"); ok {
				t.Fatal("the exhausted provider/model is still considered available")
			}
		})
	}
}

func TestInvalidRequestIsNotRetriedAndNotSentToFallbacks(t *testing.T) {
	e := newEnv(t, nil)
	bad := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.WriteError(w, 400, nil, map[string]any{"error": map[string]any{"message": "messages: field required"}})
	})
	alt := llmtest.New(t, okHandler("x", nil))
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: bad.URL + "/v1"})
	q := e.provider(providers.NewProvider{Name: "q", BaseURL: alt.URL + "/v1"})
	r := req(p, publicManifest)
	r.Fallbacks = []llm.Target{{Provider: q.ID, Model: "m1"}}
	_, err := e.w.Complete(e.ctx, r)
	var ae *llm.APIError
	if !errors.As(err, &ae) || ae.Kind != llm.KindInvalid {
		t.Fatalf("err = %v", err)
	}
	if len(bad.Calls()) != 1 || len(alt.Calls()) != 0 || len(e.clk.slept()) != 0 {
		t.Fatalf("calls %d/%d slept %v", len(bad.Calls()), len(alt.Calls()), e.clk.slept())
	}
}

// ---------------------------------------------------------------- fallback

func TestFallbackToRouterApprovedAlternativeEmitsRoutingFallback(t *testing.T) {
	e := newEnv(t, nil)
	down := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.WriteError(w, 401, nil, map[string]any{"error": map[string]any{"message": "revoked"}})
	})
	alt := llmtest.New(t, okHandler("from alternative", &llmtest.Usage{PromptTokens: 50, CompletionTokens: 5}))
	p := e.provider(providers.NewProvider{Name: "primary", BaseURL: down.URL + "/v1"})
	q := e.provider(providers.NewProvider{Name: "alt", BaseURL: alt.URL + "/v1"})
	r := req(p, privateManifest)
	r.DecisionID, r.RunID, r.TaskID = "dec-7", "run-1", "task-3"
	r.Fallbacks = []llm.Target{{Provider: q.ID, Model: "m-alt"}}
	resp, err := e.w.Complete(e.ctx, r)
	if err != nil || resp.Content != "from alternative" || !resp.Fallback || resp.Provider != q.ID {
		t.Fatalf("%v %+v", err, resp)
	}
	if alt.Calls()[0].Body["model"] != "m-alt" {
		t.Fatalf("fallback model %v", alt.Calls()[0].Body["model"])
	}
	evs := e.events("routing.fallback")
	if len(evs) != 1 || evs[0].RunID != "run-1" || evs[0].TaskID != "task-3" {
		t.Fatalf("fallback events %v", evs)
	}
	var d struct {
		DecisionID string            `json:"decisionId"`
		From       map[string]string `json:"from"`
		To         map[string]string `json:"to"`
		Cause      string            `json:"cause"`
	}
	if err := json.Unmarshal(evs[0].Data, &d); err != nil {
		t.Fatal(err)
	}
	if d.DecisionID != "dec-7" || d.From["provider"] != p.ID || d.From["model"] != "m1" || d.Cause != "api:auth" {
		t.Fatalf("payload %s", evs[0].Data)
	}
	if d.To["provider"] != q.ID || d.To["model"] != "m-alt" {
		t.Fatalf("to %v", d.To)
	}
	// The usage row belongs to the alternative that actually served the call.
	if rows := e.usage(); len(rows) != 1 || rows[0].Provider != q.ID || rows[0].Model != "m1" && rows[0].Model != "m-alt" {
		t.Fatalf("usage rows %+v", rows)
	}
	// No fallback event when the primary succeeds.
	r2 := req(q, privateManifest)
	if _, err := e.w.Complete(e.ctx, r2); err != nil || len(e.events("routing.fallback")) != 1 {
		t.Fatalf("spurious fallback event: %v", err)
	}
}

func TestQuotaExhaustedPrimaryFallsBack(t *testing.T) {
	e := newEnv(t, nil)
	primary := llmtest.New(t, okHandler("primary", nil))
	alt := llmtest.New(t, okHandler("alt", nil))
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: primary.URL + "/v1"})
	q := e.provider(providers.NewProvider{Name: "q", BaseURL: alt.URL + "/v1"})
	e.ledger.Configure(e.ctx, providers.Window{ProviderID: p.ID, Kind: providers.WindowRPD, Limit: 1})
	r := req(p, publicManifest)
	r.Fallbacks = []llm.Target{{Provider: q.ID, Model: "m1"}}
	if resp, err := e.w.Complete(e.ctx, r); err != nil || resp.Fallback {
		t.Fatalf("first: %v %+v", err, resp)
	}
	resp, err := e.w.Complete(e.ctx, r)
	if err != nil || !resp.Fallback || resp.Content != "alt" {
		t.Fatalf("second: %v %+v", err, resp)
	}
	evs := e.events("routing.fallback")
	if len(evs) != 1 || !strings.Contains(string(evs[0].Data), "quota:quota_window") {
		t.Fatalf("events %v", evs)
	}
	// All targets exhausted: the joined error still matches ErrQuota.
	e.ledger.Configure(e.ctx, providers.Window{ProviderID: q.ID, Kind: providers.WindowRPD, Limit: 1})
	e.w.Complete(e.ctx, r)
	if _, err := e.w.Complete(e.ctx, r); !errors.Is(err, llm.ErrQuota) {
		t.Fatalf("all exhausted: %v", err)
	}
}

func TestDisabledAndUnknownProvidersAreUnavailable(t *testing.T) {
	e := newEnv(t, nil)
	srv := llmtest.New(t, okHandler("x", nil))
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: srv.URL + "/v1"})
	q := e.provider(providers.NewProvider{Name: "q", BaseURL: srv.URL + "/v1"})
	e.reg.SetEnabled(e.ctx, p.ID, false)
	if _, err := e.w.Complete(e.ctx, req(p, publicManifest)); !errors.Is(err, llm.ErrProviderUnavailable) {
		t.Fatalf("disabled: %v", err)
	}
	r := req(p, publicManifest)
	r.Fallbacks = []llm.Target{{Provider: "prov_nope", Model: "m"}, {Provider: q.ID, Model: "m1"}}
	resp, err := e.w.Complete(e.ctx, r)
	if err != nil || !resp.Fallback || resp.Provider != q.ID {
		t.Fatalf("%v %+v", err, resp)
	}
}

// ---------------------------------------------------------------- usage and cost

func TestCostFromThePriceTableIsLabeledEstimated(t *testing.T) {
	e := newEnv(t, nil)
	srv := llmtest.New(t, okHandler("ok", &llmtest.Usage{PromptTokens: 10000, CompletionTokens: 2000, CachedTokens: 6000, CacheWriteTokens: 1000}))
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: srv.URL + "/v1", Tier: providers.TierPremium})
	e.reg.UpsertModels(e.ctx, p.ID, []providers.Model{{ID: "m1", DataClass: providers.DataPrivate, Tier: providers.TierCheap,
		InputPerM: 1.0, OutputPerM: 5.0, CacheReadPerM: 0.1, CacheWritePerM: 1.25}})
	r := req(p, publicManifest)
	r.RunID, r.TaskID, r.AgentID, r.Purpose = "run-1", "task-1", "agent-1", "implement"
	resp, err := e.w.Complete(e.ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	// uncached = 10000 - 6000 - 1000 = 3000; 3000*1 + 6000*0.1 + 1000*1.25 + 2000*5 = 3000+600+1250+10000 = 14850 per-M units.
	want := 14850.0 / 1e6
	if !resp.Cost.Known || resp.Cost.Source != llm.CostEstimated || resp.Cost.USD < want-1e-12 || resp.Cost.USD > want+1e-12 {
		t.Fatalf("cost %+v, want %v", resp.Cost, want)
	}
	rows := e.usage()
	if len(rows) != 1 {
		t.Fatalf("%d rows", len(rows))
	}
	row := rows[0]
	if row.CostSource != "computed_from_table" || row.Source != "reported" || row.Tier != "cheap" || row.In != 10000 || row.Out != 2000 || row.Cached != 6000 || row.CacheWrite != 1000 || row.Purpose != "implement" {
		t.Fatalf("row %+v", row)
	}
	var run, task, agent string
	e.db.Reader().QueryRow(`SELECT run_id,task_id,agent_id FROM model_usage`).Scan(&run, &task, &agent)
	if run != "run-1" || task != "task-1" || agent != "agent-1" {
		t.Fatalf("ids %s %s %s", run, task, agent)
	}
}

func TestUnknownPriceIsUnknownNotZero(t *testing.T) {
	e := newEnv(t, nil)
	srv := llmtest.New(t, okHandler("ok", &llmtest.Usage{PromptTokens: 100, CompletionTokens: 10}))
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: srv.URL + "/v1"})
	resp, err := e.w.Complete(e.ctx, req(p, publicManifest))
	if err != nil || resp.Cost.Known || resp.Cost.Source != llm.CostUnknown {
		t.Fatalf("%v %+v", err, resp.Cost)
	}
	if rows := e.usage(); rows[0].CostSource != "unknown" {
		t.Fatalf("row %+v", rows[0])
	}
}

func TestFreeModelHasKnownZeroCost(t *testing.T) {
	e := newEnv(t, nil)
	srv := llmtest.New(t, okHandler("ok", &llmtest.Usage{PromptTokens: 100, CompletionTokens: 10}))
	p := e.provider(providers.NewProvider{Name: "gpu", Kind: providers.KindHost, BaseURL: srv.URL + "/v1"})
	e.reg.UpsertModels(e.ctx, p.ID, []providers.Model{{ID: "m1", IsFree: true, DataClass: providers.DataPrivate, Tier: providers.TierCheap}})
	resp, err := e.w.Complete(e.ctx, req(p, publicManifest))
	if err != nil || !resp.Cost.Known || resp.Cost.USD != 0 || resp.Cost.Source != llm.CostEstimated {
		t.Fatalf("%v %+v", err, resp.Cost)
	}
	var host sql.NullString
	e.db.Reader().QueryRow(`SELECT host_id FROM model_usage`).Scan(&host)
	if host.String != p.ID {
		t.Fatalf("host_id %v", host)
	}
}

func TestMissingUsageIsEstimatedAndLabeled(t *testing.T) {
	e := newEnv(t, nil)
	srv := llmtest.New(t, okHandler("a response of forty characters exactly.", nil))
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: srv.URL + "/v1"})
	r := req(p, publicManifest)
	r.Messages[0].Content = strings.Repeat("x", 400)
	resp, err := e.w.Complete(e.ctx, r)
	if err != nil || resp.UsageReported {
		t.Fatalf("%v %+v", err, resp)
	}
	if resp.Usage.Input != 100 || resp.Usage.Output != 10 { // 4 characters per token
		t.Fatalf("estimated usage %+v", resp.Usage)
	}
	if rows := e.usage(); rows[0].Source != "estimated" || rows[0].In != 100 {
		t.Fatalf("row %+v", rows[0])
	}
}

// ---------------------------------------------------------------- concurrency, streaming

func TestHostConcurrencySlotsLimitParallelCalls(t *testing.T) {
	e := newEnv(t, nil)
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		okHandler("ok", nil)(c, w, r)
	})
	p := e.provider(providers.NewProvider{Name: "gpu", Kind: providers.KindHost, BaseURL: srv.URL + "/v1", MaxConcurrent: 2})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.w.Complete(e.ctx, req(p, privateManifest)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if srv.Peak() != 2 || len(srv.Calls()) != 8 {
		t.Fatalf("peak concurrency %d (limit 2), calls %d", srv.Peak(), len(srv.Calls()))
	}
	if st, _ := e.mon.Status(e.ctx, p.ID); st.InUse != 0 {
		t.Fatalf("slots leaked: %+v", st)
	}
}

func TestStreamDeliversDeltasAndRecordsUsage(t *testing.T) {
	e := newEnv(t, nil)
	srv := llmtest.New(t, okHandler("streamed text", &llmtest.Usage{PromptTokens: 20, CompletionTokens: 4}))
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: srv.URL + "/v1"})
	var got strings.Builder
	resp, err := e.w.Stream(e.ctx, req(p, publicManifest), func(d llm.Delta) error { got.WriteString(d.Text); return nil })
	if err != nil || got.String() != "streamed text" || resp.Usage.Input != 20 {
		t.Fatalf("%v %q %+v", err, got.String(), resp)
	}
	if rows := e.usage(); len(rows) != 1 || rows[0].In != 20 {
		t.Fatalf("rows %+v", rows)
	}
	if _, err := e.w.Stream(e.ctx, req(p, publicManifest), nil); err == nil {
		t.Fatal("Stream accepted a nil callback")
	}
}

func TestStreamFailureAfterOutputIsNeitherRetriedNorSentToFallbacks(t *testing.T) {
	e := newEnv(t, nil)
	flaky := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.WriteStream(w, llmtest.Completion{Model: "m1", Content: "half an ans", ErrorAfter: map[string]any{"code": 503, "message": "upstream died"}})
	})
	alt := llmtest.New(t, okHandler("x", nil))
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: flaky.URL + "/v1", Kind: providers.KindAggregator, Flavor: providers.FlavorOpenRouter})
	q := e.provider(providers.NewProvider{Name: "q", BaseURL: alt.URL + "/v1"})
	r := req(p, privateManifest)
	r.Fallbacks = []llm.Target{{Provider: q.ID, Model: "m1"}}
	var got strings.Builder
	_, err := e.w.Stream(e.ctx, r, func(d llm.Delta) error { got.WriteString(d.Text); return nil })
	var sie *llm.StreamInterruptedError
	if !errors.As(err, &sie) {
		t.Fatalf("err = %v", err)
	}
	if got.String() != "half an ans" || len(flaky.Calls()) != 1 || len(alt.Calls()) != 0 || len(e.clk.slept()) != 0 {
		t.Fatalf("delivered %q; calls %d/%d; slept %v: replaying would duplicate output", got.String(), len(flaky.Calls()), len(alt.Calls()), e.clk.slept())
	}
}

func TestStreamFailureBeforeOutputIsRetried(t *testing.T) {
	e := newEnv(t, nil)
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		if c.N == 1 {
			llmtest.WriteError(w, 503, nil, map[string]any{"error": map[string]any{"message": "busy"}})
			return
		}
		okHandler("fine", &llmtest.Usage{PromptTokens: 1, CompletionTokens: 1})(c, w, r)
	})
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: srv.URL + "/v1"})
	var got strings.Builder
	resp, err := e.w.Stream(e.ctx, req(p, publicManifest), func(d llm.Delta) error { got.WriteString(d.Text); return nil })
	if err != nil || got.String() != "fine" || resp.Content != "fine" || len(srv.Calls()) != 2 {
		t.Fatalf("%v %q calls=%d", err, got.String(), len(srv.Calls()))
	}
}

func TestContextCancellationWhileWaitingIsReturned(t *testing.T) {
	e := newEnv(t, func(o *llm.Options) { o.Sleep = nil })
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.WriteError(w, 429, map[string]string{"retry-after-ms": "20000"}, map[string]any{"error": map[string]any{"message": "rate"}})
	})
	p := e.provider(providers.NewProvider{Name: "p", BaseURL: srv.URL + "/v1"})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := e.w.Complete(ctx, req(p, publicManifest))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(start))
	}
}

func TestRequestValidation(t *testing.T) {
	e := newEnv(t, nil)
	if _, err := e.w.Complete(e.ctx, llm.Request{Model: "m"}); err == nil {
		t.Fatal("request without a provider accepted")
	}
	if _, err := e.w.Complete(e.ctx, llm.Request{Provider: "x"}); err == nil {
		t.Fatal("request without a model accepted")
	}
	if _, err := llm.NewWrapper(llm.Options{}); err == nil {
		t.Fatal("empty options accepted")
	}
}

func TestPriceCostFallsBackToDocumentedCacheMultipliers(t *testing.T) {
	m := providers.Model{InputPerM: 3, OutputPerM: 15} // no cache prices known
	u := llm.Usage{Input: 10000, Output: 1000, Cached: 5000, CacheWrite: 1000}
	// Anthropic: read 0.1x, write 1.25x: 4000*3 + 5000*0.3 + 1000*3.75 + 1000*15 = 12000+1500+3750+15000 = 32250
	got, ok := llm.PriceCost(m, providers.FlavorAnthropic, u)
	if !ok || got != 32250.0/1e6 {
		t.Fatalf("anthropic %v %v", got, ok)
	}
	// Providers without a documented multiplier get no assumed discount: 4000*3 + 5000*3 + 1000*3 + 1000*15 = 45000.
	got, _ = llm.PriceCost(m, providers.FlavorOpenAI, u)
	if got != 45000.0/1e6 {
		t.Fatalf("openai %v", got)
	}
	if _, ok := llm.PriceCost(providers.Model{}, providers.FlavorOpenAI, u); ok {
		t.Fatal("a zero, non-free row must not price anything")
	}
}

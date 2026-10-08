package probe_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/llm/backends"
	"github.com/kjmaguire/drydock/internal/llm/llmtest"
	"github.com/kjmaguire/drydock/internal/llm/probe"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/storage"
)

// The probe runs against a PROTOCOL FIXTURE (llmtest): a local HTTP server speaking the OpenAI-compatible chat-completions
// wire format whose behavior is chosen by the model name in the request. Everything else is real: the Registry, SQLite, the
// llm.Wrapper (data_class guard, quota, usage rows), the OpenAI-compatible backend. It proves how Drydock probes and
// selects; it proves nothing about what a real model does (the live tests at the bottom do, when keys are set).

type world struct {
	t    *testing.T
	ctx  context.Context
	db   *storage.DB
	reg  *providers.Registry
	bus  *events.SQLBus
	pr   *probe.Prober
	srv  *llmtest.Server
	prov providers.Provider
	mu   sync.Mutex
	hits map[string]int
}

func okCall() []llmtest.ToolCall {
	return []llmtest.ToolCall{{ID: "call_1", Name: "add_numbers", Arguments: `{"a":17,"b":25}`}}
}

func behave(w *world, model string, n int) llmtest.Completion {
	u := &llmtest.Usage{PromptTokens: 120, CompletionTokens: 20}
	switch model {
	case "pass-a", "pass-b", "pass-c", "slow-pass":
		return llmtest.Completion{Model: model, ToolCalls: okCall(), Usage: u}
	case "prose-only":
		return llmtest.Completion{Model: model, Content: "17 + 25 = 42", Usage: u}
	case "leaky":
		return llmtest.Completion{Model: model, Content: `add_numbers(a=17, b=25)`, Usage: u}
	case "bad-args":
		return llmtest.Completion{Model: model, ToolCalls: []llmtest.ToolCall{{ID: "c", Name: "add_numbers", Arguments: `{"a":"seventeen","b":25}`}}, Usage: u}
	case "wrong-tool":
		return llmtest.Completion{Model: model, ToolCalls: []llmtest.ToolCall{{ID: "c", Name: "word_length", Arguments: `{"word":"x"}`}}, Usage: u}
	case "flaky": // passes twice, fails the third run: all runs must pass
		if n >= 3 {
			return llmtest.Completion{Model: model, Content: "sorry", Usage: u}
		}
		return llmtest.Completion{Model: model, ToolCalls: okCall(), Usage: u}
	}
	return llmtest.Completion{Model: model, Content: "?", Usage: u}
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	w := &world{t: t, ctx: ctx, hits: map[string]int{}}
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w.db = db
	w.bus = events.NewSQLBus(db, 256)
	t.Cleanup(func() { w.bus.Close(); db.Close() })
	w.reg = providers.NewRegistry(db, providers.RegistryOptions{Secrets: providers.NewMemorySecretStoreForTests(), Bus: w.bus})
	w.srv = llmtest.New(t, func(c llmtest.Call, rw http.ResponseWriter, r *http.Request) {
		model, _ := c.Body["model"].(string)
		w.mu.Lock()
		w.hits[model]++
		n := w.hits[model]
		w.mu.Unlock()
		if model == "no-private-endpoint" {
			llmtest.WriteError(rw, 404, nil, map[string]any{"error": map[string]any{"code": 404, "message": "No endpoints found matching your data policy (Zero data retention)"}})
			return
		}
		llmtest.Respond(rw, c, behave(w, model, n))
	})
	wrapper, err := llm.NewWrapper(llm.Options{Registry: w.reg, DB: db, Factory: backends.Factory, Scanner: llm.NopScanner{}, Bus: w.bus,
		Retry: llm.RetryPolicy{MaxAttempts: 1, BaseDelay: time.Millisecond, Jitter: -1}})
	if err != nil {
		t.Fatal(err)
	}
	w.pr = &probe.Prober{Client: wrapper, Registry: w.reg, DB: db, Bus: w.bus}
	w.prov = w.addProvider("fixture", providers.DataPrivate, nil)
	return w
}

func (w *world) addProvider(name string, class providers.DataClass, overrides json.RawMessage) providers.Provider {
	p, err := w.reg.Create(w.ctx, providers.NewProvider{Name: name, Kind: providers.KindDirect, BaseURL: w.srv.URL + "/v1", APIKey: "sk-test", DataClass: class,
		Tier: providers.TierCheap, Flavor: providers.FlavorOpenRouter, RequestOverrides: overrides})
	if err != nil {
		w.t.Fatal(err)
	}
	return p
}

func (w *world) catalog(p providers.Provider, ms ...providers.Model) {
	w.t.Helper()
	for i := range ms {
		ms[i].ProviderID, ms[i].DataClass, ms[i].Tier = p.ID, p.DataClass, providers.TierCheap
	}
	if err := w.reg.UpsertModels(w.ctx, p.ID, ms); err != nil {
		w.t.Fatal(err)
	}
}

func model(id string, in, out float64, ctx int) providers.Model {
	return providers.Model{ID: id, InputPerM: in, OutputPerM: out, ContextLength: ctx, SupportsTools: true}
}

func (w *world) calls(m string) int { w.mu.Lock(); defer w.mu.Unlock(); return w.hits[m] }

func TestSelectionChoosesTheCheapestPrivateModelThatPassesAndRanksTheRestAsFallback(t *testing.T) {
	w := newWorld(t)
	free := model("cheapest:free", 0, 0, 128000)
	free.IsFree = true
	noTools := model("no-tools-model", 0.01, 0.01, 128000)
	noTools.SupportsTools = false
	w.catalog(w.prov,
		free,                                    // free route: never a worker
		noTools,                                 // tool calling not advertised
		model("tiny-context", 0.02, 0.02, 8000), // below the 64k minimum
		model("prose-only", 0.05, 0.05, 128000), // cheapest candidate, but answers in prose: fails the probe
		model("pass-a", 0.10, 0.30, 128000),     // cheapest that passes -> cheap_default
		model("pass-b", 0.20, 0.60, 200000),     // next -> cheap_fallback
		model("pass-c", 3, 15, 200000),          // pricey: also passes, but not selected
	)
	sel, err := w.pr.Select(w.ctx, w.prov.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if sel.Default != "pass-a" || sel.Fallback != "pass-b" {
		t.Fatalf("selection %+v", sel)
	}
	got := map[string]string{}
	for _, s := range sel.Skipped {
		got[s.Model] = s.Reason
	}
	for m, frag := range map[string]string{"cheapest:free": "free route", "no-tools-model": "tool calling not advertised", "tiny-context": "below the minimum"} {
		if !strings.Contains(got[m], frag) {
			t.Errorf("%s should be skipped (%s), reasons: %v", m, frag, got)
		}
	}
	if w.calls("cheapest:free") != 0 || w.calls("no-tools-model") != 0 || w.calls("tiny-context") != 0 {
		t.Fatal("a model that is not a candidate must never be probed")
	}
	if w.calls("prose-only") != 1 {
		t.Fatalf("a failing model stops at its first failing run, got %d calls", w.calls("prose-only"))
	}
	if w.calls("pass-a") != 3 || w.calls("pass-b") != 3 {
		t.Fatalf("a passing model is probed 3 times: %d %d", w.calls("pass-a"), w.calls("pass-b"))
	}
	// statuses are in the catalog, where the router reads them
	m, _ := w.reg.Model(w.ctx, w.prov.ID, "pass-a")
	f, _ := w.reg.Model(w.ctx, w.prov.ID, "prose-only")
	if m.ProbeStatus != "pass" || m.ProbeAt.IsZero() || f.ProbeStatus != "fail:no_tool_call" {
		t.Fatalf("probe status: %q / %q", m.ProbeStatus, f.ProbeStatus)
	}
	// results table and stored selection
	var rows, fails int
	w.db.Reader().QueryRow(`SELECT COUNT(*), COALESCE(SUM(1-pass),0) FROM probe_results`).Scan(&rows, &fails)
	if rows != 4 || fails != 1 {
		t.Fatalf("probe_results rows=%d fails=%d", rows, fails)
	}
	st, ok, err := probe.Stored(w.ctx, w.db, w.prov.ID)
	if err != nil || !ok || st.Default != "pass-a" {
		t.Fatalf("stored %+v %v %v", st, ok, err)
	}
	// the probe's own calls are recorded as overhead usage, never as task usage
	var n, tasked int
	w.db.Reader().QueryRow(`SELECT COUNT(*), COALESCE(SUM(task_id<>''),0) FROM model_usage WHERE purpose='tool_call_probe'`).Scan(&n, &tasked)
	if n != 10 || tasked != 0 {
		t.Fatalf("probe usage rows %d (task-attributed %d)", n, tasked)
	}
}

func TestEveryFailureModeIsDetected(t *testing.T) {
	w := newWorld(t)
	w.catalog(w.prov, model("prose-only", 1, 1, 128000), model("leaky", 1.1, 1, 128000), model("bad-args", 1.2, 1, 128000),
		model("wrong-tool", 1.3, 1, 128000), model("flaky", 1.4, 1, 128000), model("no-private-endpoint", 1.5, 1, 128000))
	for want, id := range map[string]string{probe.FailNoToolCall: "prose-only", probe.FailLeakedToolText: "leaky", probe.FailBadArguments: "bad-args",
		probe.FailWrongTool: "wrong-tool", probe.FailNoToolCall + "/flaky": "flaky", probe.FailUnavailablePvt: "no-private-endpoint"} {
		r, err := w.pr.Probe(w.ctx, w.prov.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		mode := strings.TrimSuffix(strings.TrimSuffix(want, "/flaky"), "")
		if strings.HasSuffix(want, "/flaky") {
			mode = probe.FailNoToolCall
			if r.Passes != 2 || r.Pass {
				t.Errorf("flaky must pass twice then fail, all runs must pass: %+v", r)
			}
		}
		if r.Pass || r.FailureMode != mode {
			t.Errorf("%s: pass=%v mode=%q want %q", id, r.Pass, r.FailureMode, mode)
		}
	}
	m, _ := w.reg.Model(w.ctx, w.prov.ID, "no-private-endpoint")
	if m.ProbeStatus != probe.FailUnavailablePvt {
		t.Fatalf("a model with no private endpoint is marked %q", m.ProbeStatus)
	}
}

func TestNoModelPassingIsSaidPlainlyAndLeavesCheapDefaultUnset(t *testing.T) {
	w := newWorld(t)
	w.catalog(w.prov, model("prose-only", 1, 1, 128000), model("leaky", 2, 1, 128000))
	sel, err := w.pr.Select(w.ctx, w.prov.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if sel.Default != "" || sel.Fallback != "" || !strings.Contains(sel.Message, "none of the 2 probed model(s) passed") || !strings.Contains(sel.Message, "degrade") {
		t.Fatalf("%+v", sel)
	}
}

func TestOnlyPrivateModelsAreCandidatesAndTheProbeCarriesTheDataPolicy(t *testing.T) {
	w := newWorld(t)
	trainable := w.addProvider("trainable-host", providers.DataTrainable, nil)
	w.catalog(trainable, model("pass-a", 0.01, 0.01, 128000))
	sel, err := w.pr.Select(w.ctx, trainable.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if sel.Default != "" || len(sel.Probed) != 0 || len(sel.Skipped) != 1 || !strings.Contains(sel.Skipped[0].Reason, "not private") {
		t.Fatalf("a trainable provider yields no cheap_default and no probe call: %+v", sel)
	}
	if w.calls("pass-a") != 0 {
		t.Fatal("a trainable model must not be probed for private selection")
	}
	// a private OpenRouter-flavored provider: decision D4, the data-policy object is on every probe request (even without stored overrides)
	w.catalog(w.prov, model("pass-a", 0.1, 0.1, 128000))
	if _, err := w.pr.Select(w.ctx, w.prov.ID, false); err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, c := range w.srv.Calls() {
		if p, ok := c.Body["provider"].(map[string]any); ok && p["data_collection"] == "deny" && p["zdr"] == true {
			seen = true
		}
		if c.Body["tool_choice"] != "auto" {
			t.Fatalf("tool_choice %v", c.Body["tool_choice"])
		}
		if ts, _ := c.Body["tools"].([]any); len(ts) != 2 {
			t.Fatalf("the probe sends the fixed two-tool schema: %v", c.Body["tools"])
		}
	}
	if !seen {
		t.Fatal("the provider's data-policy object is missing from the probe request")
	}
}

func TestAFreshPassIsNotProbedAgainUnlessForced(t *testing.T) {
	w := newWorld(t)
	w.catalog(w.prov, model("pass-a", 0.1, 0.1, 128000))
	if _, err := w.pr.Select(w.ctx, w.prov.ID, false); err != nil {
		t.Fatal(err)
	}
	before := w.calls("pass-a")
	sel, _ := w.pr.Select(w.ctx, w.prov.ID, false)
	if w.calls("pass-a") != before || sel.Default != "pass-a" || !sel.Probed[0].Cached {
		t.Fatalf("a pass inside the TTL is reused: calls %d->%d %+v", before, w.calls("pass-a"), sel)
	}
	w.pr.Select(w.ctx, w.prov.ID, true)
	if w.calls("pass-a") != before+3 {
		t.Fatalf("force probes again: %d", w.calls("pass-a"))
	}
	// an expired pass is probed again
	w.pr.Now = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
	b2 := w.calls("pass-a")
	w.pr.Select(w.ctx, w.prov.ID, false)
	if w.calls("pass-a") != b2+3 {
		t.Fatal("a pass older than the TTL must be probed again")
	}
}

func TestRankIsPureAndOrdersByBlendedPrice(t *testing.T) {
	prov := providers.Provider{Enabled: true, DataClass: providers.DataPrivate}
	ms := []providers.Model{model("b", 1, 1, 100000), model("a", 0.5, 1.5, 100000), model("c", 0.1, 0.1, 0)}
	ranked, _ := probe.Rank(prov, ms, func(providers.Model) providers.DataClass { return providers.DataPrivate }, 64000)
	if len(ranked) != 3 || ranked[0].Model.ID != "c" || !ranked[0].ContextUnknown {
		t.Fatalf("an unlisted context is probed and flagged, not dropped: %+v", ranked)
	}
	// a and b both blend to 1.0 at 3:1 -> tie broken by id
	if ranked[1].Model.ID != "a" || ranked[2].Model.ID != "b" {
		t.Fatalf("%v %v", ranked[1].Model.ID, ranked[2].Model.ID)
	}
}

// ---- live tests: skipped unless a key is provided ----

func TestLiveProbeAgainstARealProvider(t *testing.T) {
	key, base, mdl := os.Getenv("OPENROUTER_API_KEY"), "https://openrouter.ai/api/v1", os.Getenv("DRYDOCK_LIVE_PROBE_MODEL")
	flavor := providers.FlavorOpenRouter
	if key == "" {
		key, base, mdl = os.Getenv("DRYDOCK_LIVE_API_KEY"), os.Getenv("DRYDOCK_LIVE_BASE_URL"), os.Getenv("DRYDOCK_LIVE_PROBE_MODEL")
		flavor = ""
	}
	if key == "" || base == "" || mdl == "" {
		t.Skip("live probe needs OPENROUTER_API_KEY (or DRYDOCK_LIVE_API_KEY + DRYDOCK_LIVE_BASE_URL) and DRYDOCK_LIVE_PROBE_MODEL: no model is hard-coded (decision D4)")
	}
	w := newWorld(t)
	over := json.RawMessage(nil)
	if flavor == providers.FlavorOpenRouter {
		over = json.RawMessage(`{"provider":{"data_collection":"deny","zdr":true}}`)
	}
	p, err := w.reg.Create(w.ctx, providers.NewProvider{Name: "live", Kind: providers.KindAggregator, BaseURL: base, APIKey: key, DataClass: providers.DataPrivate,
		Tier: providers.TierCheap, Flavor: flavor, RequestOverrides: over})
	if err != nil {
		t.Fatal(err)
	}
	w.catalog(p, model(mdl, 0, 0, 0))
	r, err := w.pr.Probe(w.ctx, p.ID, mdl)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("live probe of %s: pass=%v passes=%d/%d mode=%q latency=%dms cost known=%v $%.6f", mdl, r.Pass, r.Passes, r.Runs, r.FailureMode, r.LatencyMS, r.CostKnown, r.CostUSD)
}

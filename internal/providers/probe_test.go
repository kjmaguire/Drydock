package providers

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestProbeOpenAICompatibleHostVLLM(t *testing.T) {
	e := newEnv(t)
	f := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/health": status(200),
		"/load":   jsonBody(map[string]any{"server_load": 2}),
		"/v1/models": jsonBody(map[string]any{"object": "list", "data": []map[string]any{
			{"id": "CohereLabs/North-Mini-Code-1.0", "object": "model", "max_model_len": 320000},
		}}),
	})
	p := e.create(t, NewProvider{Name: "vllm", Kind: KindHost, BaseURL: f.URL + "/v1", APIKey: "host-key"})
	res, err := e.pr.Probe(e.ctx, p.ID)
	if err != nil || !res.OK {
		t.Fatalf("probe: %v %+v", err, res)
	}
	if res.Flavor != FlavorVLLM || res.ServerInfo["server_load"] != float64(2) {
		t.Fatalf("flavor/info %v %v", res.Flavor, res.ServerInfo)
	}
	if len(res.Models) != 1 || res.Models[0].ContextLength != 320000 || !res.Models[0].IsFree {
		t.Fatalf("models %+v", res.Models)
	}
	if got := f.seen("/v1/models"); len(got) != 1 || got[0].Auth != "Bearer host-key" {
		t.Fatalf("listing request %+v", got)
	}
	// The catalog, status and flavor were persisted.
	m, err := e.reg.Model(e.ctx, p.ID, "CohereLabs/North-Mini-Code-1.0")
	if err != nil || m.ContextLength != 320000 || m.DataClass != DataPrivate {
		t.Fatalf("catalog %+v %v", m, err)
	}
	got, _ := e.reg.Get(e.ctx, p.ID)
	if got.Flavor != FlavorVLLM || got.Status != string(HealthUp) || got.LastSeen.IsZero() {
		t.Fatalf("provider after probe %+v", got)
	}
	types := e.eventTypes(t, "provider.")
	if !strings.Contains(strings.Join(types, ","), "provider.probe_completed") || !strings.Contains(strings.Join(types, ","), "provider.catalog_refreshed") {
		t.Fatalf("events %v", types)
	}
}

func TestProbeSGLangStoresOnlyWhitelistedServerInfo(t *testing.T) {
	e := newEnv(t)
	f := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/v1/models": jsonBody(map[string]any{"data": []map[string]any{{"id": "m"}}}),
		// SGLang's /server_info echoes its server args, which can include the API key.
		"/server_info": jsonBody(map[string]any{"version": "0.5.1", "model_path": "/models/x", "tool_call_parser": "cohere_command4",
			"enable_cache_report": false, "api_key": "LEAKED-SERVER-KEY", "admin_api_key": "LEAKED-ADMIN"}),
	})
	p := e.create(t, NewProvider{Name: "sg", Kind: KindHost, BaseURL: f.URL + "/v1"})
	res, _ := e.pr.Probe(e.ctx, p.ID)
	if !res.OK || res.Flavor != FlavorSGLang {
		t.Fatalf("%+v", res)
	}
	if _, leaked := res.ServerInfo["api_key"]; leaked || res.ServerInfo["tool_call_parser"] != "cohere_command4" {
		t.Fatalf("server info %v", res.ServerInfo)
	}
	found := false
	for _, w := range res.Warnings {
		found = found || strings.Contains(w, "enable-cache-report")
	}
	if !found {
		t.Fatalf("expected a cache-report warning, got %v", res.Warnings)
	}
	e.db.Writer().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	for _, fn := range []string{e.db.Path(), e.db.Path() + "-wal"} {
		if b, err := os.ReadFile(fn); err == nil && strings.Contains(string(b), "LEAKED-") {
			t.Fatalf("server key persisted in %s", fn)
		}
	}
}

func TestProbeOllamaWarnsOnSmallContext(t *testing.T) {
	e := newEnv(t)
	f := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/v1/models":   jsonBody(map[string]any{"data": []map[string]any{{"id": "qwen3-coder"}}}),
		"/api/version": jsonBody(map[string]any{"version": "0.15.0"}),
		"/api/ps":      jsonBody(map[string]any{"models": []map[string]any{{"name": "qwen3-coder", "context_length": 4096}}}),
	})
	p := e.create(t, NewProvider{Name: "ollama", Kind: KindHost, BaseURL: f.URL + "/v1"})
	res, _ := e.pr.Probe(e.ctx, p.ID)
	if res.Flavor != FlavorOllama || len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "4096") {
		t.Fatalf("%+v", res)
	}
	got, _ := e.reg.Get(e.ctx, p.ID)
	if got.Flavor != FlavorOllama || got.MaxConcurrent != 1 {
		t.Fatalf("flavor %s max_concurrent %d (Ollama serves one request at a time by default)", got.Flavor, got.MaxConcurrent)
	}
}

func TestProbeOpenRouterKeyAndUserCatalog(t *testing.T) {
	e := newEnv(t)
	f := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/api/v1/key": jsonBody(map[string]any{"data": map[string]any{"label": "k", "limit": 10.0, "limit_remaining": 7.5, "limit_reset": "monthly",
			"usage": 2.5, "is_free_tier": false, "free_model_daily_requests": map[string]any{"used": 3, "limit": 1000, "remaining": 997}}}),
		"/api/v1/models/user": jsonBody(map[string]any{"data": []map[string]any{
			{"id": "qwen/qwen3-coder-next", "context_length": 262144, "supported_parameters": []string{"tools", "temperature", "reasoning"},
				"pricing":      map[string]any{"prompt": "0.00000012", "completion": "0.0000008", "input_cache_read": "0.00000007"},
				"top_provider": map[string]any{"max_completion_tokens": 235929}},
			{"id": "cohere/north-mini-code:free", "context_length": 256000, "supported_parameters": []string{"tools"},
				"pricing": map[string]any{"prompt": "0", "completion": "0"}},
			{"id": "no/tools", "context_length": 8000, "supported_parameters": []string{"temperature"}, "pricing": map[string]any{"prompt": "0.000001", "completion": "0.000002"}},
		}}),
	})
	p := e.create(t, NewProvider{Name: "or", Kind: KindAggregator, BaseURL: f.URL + "/api/v1", Flavor: FlavorOpenRouter, APIKey: "or-key"})
	res, err := e.pr.Probe(e.ctx, p.ID)
	if err != nil || !res.OK {
		t.Fatalf("%v %+v", err, res)
	}
	if res.Key == nil || res.Key.FreeModelDailyRequests.Limit != 1000 || *res.Key.LimitRemaining != 7.5 {
		t.Fatalf("key %+v", res.Key)
	}
	paid, _ := e.reg.Model(e.ctx, p.ID, "qwen/qwen3-coder-next")
	if paid.InputPerM < 0.1199 || paid.InputPerM > 0.1201 || paid.OutputPerM < 0.7999 || paid.OutputPerM > 0.8001 || paid.CacheReadPerM < 0.0699 || paid.CacheReadPerM > 0.0701 {
		t.Fatalf("prices per Mtok %+v", paid)
	}
	if !paid.SupportsTools || !paid.SupportsReasoning || paid.MaxOutput != 235929 || paid.ContextLength != 262144 || paid.DataClass != DataPrivate || paid.IsFree {
		t.Fatalf("paid %+v", paid)
	}
	free, _ := e.reg.Model(e.ctx, p.ID, "cohere/north-mini-code:free")
	if !free.IsFree || free.DataClass != DataTrainable || free.Tier != TierFree || !free.PriceKnown() {
		t.Fatalf("free %+v", free)
	}
	notools, _ := e.reg.Model(e.ctx, p.ID, "no/tools")
	if notools.SupportsTools {
		t.Fatal("model without the tools parameter must not be marked tool-capable")
	}
	if len(f.seen("/api/v1/models/user")) != 1 || f.seen("/api/v1/key")[0].Auth != "Bearer or-key" {
		t.Fatal("expected /key and /models/user to be called with the bearer key")
	}
}

func TestProbeAuthFailures(t *testing.T) {
	e := newEnv(t)
	// Gemini answers an invalid key with HTTP 400, not 401 (docs/phase0/model-providers.md C).
	gem := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/v1beta/openai/models": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":400,"message":"Please pass a valid API key","status":"INVALID_ARGUMENT"}}`))
		},
	})
	p := e.create(t, NewProvider{Name: "gem", BaseURL: gem.URL + "/v1beta/openai", Flavor: FlavorGemini, DataClass: DataTrainable, Tier: TierFree, APIKey: "bad"})
	res, _ := e.pr.Probe(e.ctx, p.ID)
	if res.OK || !res.AuthFailed {
		t.Fatalf("400 must read as an auth failure: %+v", res)
	}
	if g, _ := e.reg.Get(e.ctx, p.ID); g.Status != string(HealthDown) {
		t.Fatalf("status %s", g.Status)
	}
	// Plain OpenAI-compatible 401.
	f := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){"/v1/models": status(401)})
	q := e.create(t, NewProvider{Name: "oa", BaseURL: f.URL + "/v1", APIKey: "bad"})
	if res, _ := e.pr.Probe(e.ctx, q.ID); res.OK || !res.AuthFailed {
		t.Fatalf("%+v", res)
	}
	// 500 is a failure but not an auth failure.
	f.set("/v1/models", status(500))
	if res, _ := e.pr.Probe(e.ctx, q.ID); res.OK || res.AuthFailed || !strings.Contains(res.Error, "500") {
		t.Fatalf("%+v", res)
	}
}

func TestProbeGeminiCompatListingAndNativeFallback(t *testing.T) {
	e := newEnv(t)
	compat := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/v1beta/openai/models": jsonBody(map[string]any{"object": "list", "data": []map[string]any{{"id": "models/gemini-2.5-flash", "owned_by": "google"}}}),
	})
	p := e.create(t, NewProvider{Name: "gem", BaseURL: compat.URL + "/v1beta/openai", Flavor: FlavorGemini, DataClass: DataTrainable, Tier: TierFree, APIKey: "g"})
	res, _ := e.pr.Probe(e.ctx, p.ID)
	if !res.OK || res.Models[0].ID != "gemini-2.5-flash" || !res.Models[0].SupportsTools {
		t.Fatalf("%+v", res)
	}
	// When the compat endpoint has no model listing (unverified upstream), the native listing is used with x-goog-api-key.
	native := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/v1beta/models": jsonBody(map[string]any{"models": []map[string]any{
			{"name": "models/gemini-2.5-pro", "inputTokenLimit": 1048576, "outputTokenLimit": 65536, "supportedGenerationMethods": []string{"generateContent"}},
			{"name": "models/text-embedding-004", "supportedGenerationMethods": []string{"embedContent"}},
		}}),
	})
	q := e.create(t, NewProvider{Name: "gem2", BaseURL: native.URL + "/v1beta/openai", Flavor: FlavorGemini, DataClass: DataTrainable, Tier: TierFree, APIKey: "g2"})
	res, _ = e.pr.Probe(e.ctx, q.ID)
	if !res.OK || len(res.Models) != 1 || res.Models[0].ContextLength != 1048576 || res.Models[0].MaxOutput != 65536 {
		t.Fatalf("%+v", res)
	}
	if got := native.seen("/v1beta/models"); len(got) != 1 || got[0].GoogKey != "g2" || got[0].Auth != "" {
		t.Fatalf("native request %+v", got)
	}
}

func TestProbeAnthropicUsesXAPIKey(t *testing.T) {
	e := newEnv(t)
	f := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/v1/models": jsonBody(map[string]any{"data": []map[string]any{{"id": "claude-x", "display_name": "X", "max_input_tokens": 200000, "max_tokens": 64000}}}),
	})
	p := e.create(t, NewProvider{Name: "ant", BaseURL: f.URL, Flavor: FlavorAnthropic, Tier: TierPremium, APIKey: "sk-ant"})
	res, _ := e.pr.Probe(e.ctx, p.ID)
	if !res.OK || res.Models[0].ContextLength != 200000 || !res.Models[0].SupportsTools {
		t.Fatalf("%+v", res)
	}
	if got := f.seen("/v1/models"); got[0].APIKey != "sk-ant" || got[0].Auth != "" {
		t.Fatalf("request %+v", got)
	}
}

func TestProbeKeepsUserPriceAcrossRefresh(t *testing.T) {
	e := newEnv(t)
	price := "0.000001"
	f := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/api/v1/key": jsonBody(map[string]any{"data": map[string]any{}}),
	})
	f.set("/api/v1/models/user", func(w http.ResponseWriter, r *http.Request) {
		jsonBody(map[string]any{"data": []map[string]any{{"id": "a/b", "supported_parameters": []string{"tools"}, "pricing": map[string]any{"prompt": price, "completion": price}}}})(w, r)
	})
	p := e.create(t, NewProvider{Name: "or", Kind: KindAggregator, BaseURL: f.URL + "/api/v1", Flavor: FlavorOpenRouter})
	e.pr.Probe(e.ctx, p.ID)
	if err := e.reg.SetUserPrice(e.ctx, p.ID, "a/b", 5, 5, 0, 0); err != nil {
		t.Fatal(err)
	}
	price = "0.00001"
	e.pr.Probe(e.ctx, p.ID)
	m, _ := e.reg.Model(e.ctx, p.ID, "a/b")
	if m.InputPerM != 5 || m.PriceSource != "user" {
		t.Fatalf("user price overwritten: %+v", m)
	}
}

// A llama-server host (the local models of decision P65) is told apart by /props: its models are listed as taking tool calls
// (the listing says nothing about it, the server's chat template handles them) with the context the server runs with, and a
// small context is warned about. A server not reachable at /props stays a plain OpenAI-compatible host.
func TestProbeLlamaCppHostListsToolCapableModels(t *testing.T) {
	e := newEnv(t)
	f := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/health": status(200),
		"/props": jsonBody(map[string]any{"build_info": "b11669-abc", "total_slots": 1,
			"default_generation_settings": map[string]any{"n_ctx": 32768, "params": map[string]any{"n_predict": -1}}}),
		"/v1/models": jsonBody(map[string]any{"object": "list", "data": []map[string]any{
			{"id": "ggml-org/gpt-oss-20b-GGUF:MXFP4", "object": "model", "owned_by": "llamacpp", "meta": map[string]any{"n_ctx_train": 131072}},
		}}),
	})
	p := e.create(t, NewProvider{Name: "local", Kind: KindHost, BaseURL: f.URL + "/v1", Tier: TierCheap})
	res, err := e.pr.Probe(e.ctx, p.ID)
	if err != nil || !res.OK {
		t.Fatalf("probe: %v %+v", err, res)
	}
	if res.Flavor != FlavorLlamaCpp || res.ServerInfo["build_info"] != "b11669-abc" || res.ServerInfo["n_ctx"] != 32768 {
		t.Fatalf("flavor/info %v %v", res.Flavor, res.ServerInfo)
	}
	if len(res.Models) != 1 || !res.Models[0].SupportsTools || res.Models[0].ContextLength != 32768 || !res.Models[0].IsFree {
		t.Fatalf("models %+v", res.Models)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "32768-token context") {
		t.Fatalf("warnings %v", res.Warnings)
	}
	m, err := e.reg.Model(e.ctx, p.ID, "ggml-org/gpt-oss-20b-GGUF:MXFP4")
	if err != nil || !m.SupportsTools || m.Tier != TierCheap {
		t.Fatalf("catalog %+v %v", m, err)
	}
	if got, _ := e.reg.Get(e.ctx, p.ID); got.Flavor != FlavorLlamaCpp {
		t.Fatalf("provider after probe %+v", got)
	}
	// probed again with its flavor already known, the models still take tools
	res, _ = e.pr.Probe(e.ctx, p.ID)
	if res.Flavor != FlavorLlamaCpp || !res.Models[0].SupportsTools {
		t.Fatalf("second probe %+v", res)
	}
}

// llama-server's router mode (decision P111) lists every model of its preset with its state: the provider offers each loaded
// model (and one asleep), not one that is loading, unloaded or failed, each with the context its own instance runs with
// (/props?model=), since the router's own /props names no model.
func TestProbeLlamaCppRouterListsEachLoadedModel(t *testing.T) {
	e := newEnv(t)
	listed := func(id, state string, failed bool) map[string]any {
		st := map[string]any{"value": state, "args": []string{"llama-server"}}
		if failed {
			st["failed"], st["exit_code"] = true, 1
		}
		return map[string]any{"id": id, "object": "model", "owned_by": "llamacpp", "status": st, "context_length": 262144}
	}
	ctxOf := map[string]int{"acme/coder-GGUF:Q4_K_M": 131072, "acme/vision-GGUF:Q4_K_M": 16384, "acme/asleep-GGUF:Q8_0": 65536}
	f := newFixture(t, map[string]func(http.ResponseWriter, *http.Request){
		"/health": status(200),
		"/props": func(w http.ResponseWriter, r *http.Request) {
			m := r.URL.Query().Get("model")
			if m == "" {
				jsonBody(map[string]any{"role": "router", "build_info": "b11936-router", "default_generation_settings": map[string]any{"n_ctx": 0}})(w, r)
				return
			}
			jsonBody(map[string]any{"build_info": "b11936", "model_alias": m, "default_generation_settings": map[string]any{"n_ctx": ctxOf[m]}})(w, r)
		},
		"/v1/models": jsonBody(map[string]any{"object": "list", "data": []map[string]any{
			listed("acme/coder-GGUF:Q4_K_M", "loaded", false), listed("acme/vision-GGUF:Q4_K_M", "loaded", false),
			listed("acme/asleep-GGUF:Q8_0", "sleeping", false), listed("acme/loading-GGUF:Q4_0", "loading", false),
			listed("acme/broken-GGUF:Q4_0", "unloaded", true), listed("acme/idle-GGUF:Q4_0", "unloaded", false),
		}}),
	})
	p := e.create(t, NewProvider{Name: "local", Kind: KindHost, BaseURL: f.URL + "/v1", Tier: TierCheap})
	res, err := e.pr.Probe(e.ctx, p.ID)
	if err != nil || !res.OK || res.Flavor != FlavorLlamaCpp || res.ServerInfo["role"] != "router" {
		t.Fatalf("probe: %v %+v", err, res)
	}
	var ids []string
	for _, m := range res.Models {
		ids = append(ids, m.ID)
		if !m.SupportsTools || m.ContextLength != ctxOf[m.ID] {
			t.Fatalf("%+v", m)
		}
	}
	if strings.Join(ids, ",") != "acme/coder-GGUF:Q4_K_M,acme/vision-GGUF:Q4_K_M,acme/asleep-GGUF:Q8_0" {
		t.Fatalf("models %v", ids)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "acme/vision-GGUF:Q4_K_M with a 16384-token context") {
		t.Fatalf("warnings %v", res.Warnings)
	}
}

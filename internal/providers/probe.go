package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
)

// Prober runs the Test-connection checks and catalog refreshes (docs/model-routing.md s4-s5). Endpoints follow the
// Phase 0 provider appendix (docs/phase0/model-providers.md); everything marked unverified there is best effort here
// and never turns a reachable provider into an error.
type Prober struct {
	reg     *Registry
	bus     events.Bus
	timeout time.Duration
	now     func() time.Time
}

// NewProber builds a Prober. bus may be nil.
func NewProber(reg *Registry, bus events.Bus) *Prober {
	return &Prober{reg: reg, bus: bus, timeout: 15 * time.Second, now: func() time.Time { return time.Now().UTC() }}
}

// ProbeResult is what a probe learned.
type ProbeResult struct {
	ProviderID string
	OK         bool // reachable, authenticated, listing parsed
	AuthFailed bool // 400, 401 or 403 on the listing (Gemini answers an invalid key with 400, MP C)
	Latency    time.Duration
	Flavor     Flavor
	Models     []Model
	ServerInfo map[string]any // whitelisted fields only (server args can carry keys)
	Key        *OpenRouterKey
	Warnings   []string
	Error      string
}

// OpenRouterKey is GET /api/v1/key (docs/phase0/model-providers.md A.4).
type OpenRouterKey struct {
	Label                  string   `json:"label"`
	Limit                  *float64 `json:"limit"`
	LimitRemaining         *float64 `json:"limit_remaining"`
	LimitReset             string   `json:"limit_reset"`
	Usage                  float64  `json:"usage"`
	UsageDaily             float64  `json:"usage_daily"`
	IsFreeTier             bool     `json:"is_free_tier"`
	FreeModelDailyRequests *struct {
		Used      int `json:"used"`
		Limit     int `json:"limit"`
		Remaining int `json:"remaining"`
	} `json:"free_model_daily_requests"`
}

const maxBody = 16 << 20

// httpGet performs an authenticated GET and returns status and body.
func (pr *Prober) httpGet(ctx context.Context, p Provider, key, rawURL string) (int, []byte, http.Header, error) {
	return pr.httpGetAs(ctx, p, key, rawURL, "")
}

// httpGetAs is httpGet with an explicit auth style: "" is the flavor's default, "goog" the native Gemini header.
func (pr *Prober) httpGetAs(ctx context.Context, p Provider, key, rawURL, style string) (int, []byte, http.Header, error) {
	c, err := pr.reg.HTTPClient(p)
	if err != nil {
		return 0, nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, pr.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Accept", "application/json")
	applyAuth(req, p.Flavor, key, style)
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return resp.StatusCode, b, resp.Header, err
}

func applyAuth(req *http.Request, f Flavor, key, style string) {
	if key == "" {
		return
	}
	switch {
	case style == "goog":
		req.Header.Set("x-goog-api-key", key) // native Gemini API
	case f == FlavorAnthropic:
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	default:
		req.Header.Set("Authorization", "Bearer "+key)
	}
}

func origin(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}
	return u.Scheme + "://" + u.Host
}

// Probe checks one provider: listing, auth, host endpoints; upserts the catalog; records status; emits events.
func (pr *Prober) Probe(ctx context.Context, id string) (ProbeResult, error) {
	p, err := pr.reg.Get(ctx, id)
	if err != nil {
		return ProbeResult{}, err
	}
	key, err := pr.reg.APIKey(ctx, p)
	if err != nil {
		return ProbeResult{ProviderID: id, Error: "api key unavailable: " + err.Error()}, nil
	}
	res := ProbeResult{ProviderID: id, Flavor: p.Flavor}
	start := time.Now()
	switch p.Flavor {
	case FlavorOpenRouter:
		pr.probeOpenRouter(ctx, p, key, &res)
	case FlavorAnthropic:
		pr.probeAnthropic(ctx, p, key, &res)
	case FlavorGemini:
		pr.probeGemini(ctx, p, key, &res)
	default:
		pr.probeOpenAI(ctx, p, key, &res)
	}
	res.Latency = time.Since(start)
	pr.finish(ctx, p, &res)
	return res, nil
}

func (pr *Prober) finish(ctx context.Context, p Provider, res *ProbeResult) {
	now := pr.now()
	status := HealthDown
	if res.OK {
		status = HealthUp
		for i := range res.Models {
			if res.Models[i].DataClass == "" {
				res.Models[i].DataClass = p.DataClass
			}
			if res.Models[i].Tier == "" {
				res.Models[i].Tier = p.Tier
			}
		}
		if err := pr.reg.UpsertModels(ctx, p.ID, res.Models); err != nil {
			res.Warnings = append(res.Warnings, "catalog upsert failed: "+err.Error())
		}
	}
	if res.OK && res.Flavor == FlavorOllama && p.MaxConcurrent == 0 {
		// OLLAMA_NUM_PARALLEL defaults to 1 (docs/phase0/model-providers.md E): serialize agents until the user raises it.
		if err := pr.reg.setMaxConcurrentIfZero(ctx, p.ID, 1); err != nil {
			res.Warnings = append(res.Warnings, "could not set the ollama concurrency default: "+err.Error())
		}
	}
	var info json.RawMessage
	if len(res.ServerInfo) > 0 {
		info, _ = json.Marshal(res.ServerInfo)
	}
	flavor := Flavor("")
	if res.Flavor != p.Flavor {
		flavor = res.Flavor
	}
	seen := time.Time{}
	if res.OK {
		seen = now
	}
	if err := pr.reg.setStatus(ctx, p.ID, status, seen, flavor, info); err != nil {
		res.Warnings = append(res.Warnings, "status update failed: "+err.Error())
	}
	if pr.bus != nil {
		_, _ = events.Emit(ctx, pr.bus, "provider.probe_completed", events.Event{}, map[string]any{
			"providerId": p.ID, "ok": res.OK, "authFailed": res.AuthFailed, "models": len(res.Models), "latencyMs": res.Latency.Milliseconds()})
		if res.OK {
			_, _ = events.Emit(ctx, pr.bus, "provider.catalog_refreshed", events.Event{}, map[string]any{"providerId": p.ID, "models": len(res.Models)})
		}
	}
}

// listStatus converts a non-200 listing answer into the result.
func listStatus(res *ProbeResult, status int, body []byte, err error, what string) bool {
	if err != nil {
		res.Error = what + ": " + err.Error()
		return false
	}
	if status >= 200 && status < 300 {
		return true
	}
	if status == 400 || status == 401 || status == 403 {
		res.AuthFailed = true
		res.Error = fmt.Sprintf("%s: HTTP %d (the key was rejected)", what, status)
		return false
	}
	res.Error = fmt.Sprintf("%s: HTTP %d", what, status)
	return false
}

func (pr *Prober) probeOpenAI(ctx context.Context, p Provider, key string, res *ProbeResult) {
	status, body, _, err := pr.httpGet(ctx, p, key, p.BaseURL+"/models")
	if !listStatus(res, status, body, err, "GET /models") {
		return
	}
	models, perr := parseListing(body, p, nil)
	if perr != nil {
		res.Error = "GET /models: " + perr.Error()
		return
	}
	res.Models, res.OK = models, true
	if p.Kind == KindHost {
		pr.probeHost(ctx, p, key, res)
	}
}

// probeHost detects the server flavor and collects server info and warnings (MP E). The endpoints are those the
// appendix read from source; none was run against a live server (no GPU host here).
func (pr *Prober) probeHost(ctx context.Context, p Provider, key string, res *ProbeResult) {
	o := origin(p.BaseURL)
	info := map[string]any{}
	if st, b, _, err := pr.httpGet(ctx, p, key, o+"/api/version"); err == nil && st == 200 {
		var v struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(b, &v) == nil && v.Version != "" {
			res.Flavor = FlavorOllama
			info["version"] = v.Version
			if st, b, _, err := pr.httpGet(ctx, p, key, o+"/api/ps"); err == nil && st == 200 {
				var ps struct {
					Models []struct {
						Name          string `json:"name"`
						ContextLength int    `json:"context_length"`
					} `json:"models"`
				}
				if json.Unmarshal(b, &ps) == nil {
					for _, m := range ps.Models {
						if m.ContextLength > 0 && m.ContextLength < 64000 {
							res.Warnings = append(res.Warnings, fmt.Sprintf("ollama model %s runs with a %d-token context; coding agents need at least 64000 (set OLLAMA_CONTEXT_LENGTH)", m.Name, m.ContextLength))
						}
					}
				}
			}
		}
	}
	if res.Flavor != FlavorOllama {
		if st, b, _, err := pr.httpGet(ctx, p, key, o+"/server_info"); err == nil && st == 200 {
			var raw map[string]any
			if json.Unmarshal(b, &raw) == nil {
				res.Flavor = FlavorSGLang
				for _, k := range []string{"version", "model_path", "served_model_name", "tool_call_parser", "reasoning_parser", "context_length", "enable_cache_report", "max_running_requests"} {
					if v, ok := raw[k]; ok {
						info[k] = v
					}
				}
				if on, _ := raw["enable_cache_report"].(bool); !on {
					res.Warnings = append(res.Warnings, "sglang is running without --enable-cache-report: cached tokens will not be reported")
				}
			}
		} else if st, b, _, err := pr.httpGet(ctx, p, key, o+"/load"); err == nil && st == 200 {
			var l struct {
				ServerLoad *float64 `json:"server_load"`
			}
			if json.Unmarshal(b, &l) == nil && l.ServerLoad != nil {
				res.Flavor = FlavorVLLM
				info["server_load"] = *l.ServerLoad
			}
		}
	}
	if res.Flavor == "" || res.Flavor == FlavorOpenAI || res.Flavor == FlavorLlamaCpp {
		// llama-server answers /props (its build and the context it runs the model with); its listing carries no
		// supported_parameters, but every model it serves takes tool calls through its Jinja chat template
		if st, b, _, err := pr.httpGet(ctx, p, key, o+"/props"); err == nil && st == 200 {
			var props struct {
				BuildInfo string `json:"build_info"`
				Defaults  *struct {
					NCtx int `json:"n_ctx"`
				} `json:"default_generation_settings"`
				Slots int `json:"total_slots"`
			}
			if json.Unmarshal(b, &props) == nil && (props.BuildInfo != "" || props.Defaults != nil) {
				res.Flavor = FlavorLlamaCpp
				info["build_info"] = props.BuildInfo
				info["total_slots"] = props.Slots
				nctx := 0
				if props.Defaults != nil {
					nctx = props.Defaults.NCtx
					info["n_ctx"] = nctx
				}
				for i := range res.Models {
					res.Models[i].SupportsTools = true
					if nctx > 0 {
						res.Models[i].ContextLength = nctx // the context the server runs with, not the model's training context
					}
				}
				if nctx > 0 && nctx < 64000 {
					res.Warnings = append(res.Warnings, fmt.Sprintf("llama-server runs with a %d-token context; coding agents need at least 64000 (set the context size on the Providers page)", nctx))
				}
			}
		}
	}
	if res.Flavor != FlavorOllama { // Ollama has no /health; its liveness is /api/version, answered above
		if st, _, _, err := pr.httpGet(ctx, p, "", o+"/health"); err != nil {
			res.Warnings = append(res.Warnings, "GET /health failed: "+err.Error())
		} else if st < 200 || st > 299 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("GET /health returned HTTP %d", st))
		}
	}
	if len(info) > 0 {
		res.ServerInfo = info
	}
	if p.DataClass == DataPrivate && p.LANUnencrypted {
		res.Warnings = append(res.Warnings, "lan_unencrypted: plain http on a private network address")
	}
}

func (pr *Prober) probeOpenRouter(ctx context.Context, p Provider, key string, res *ProbeResult) {
	k, err := pr.openRouterKey(ctx, p, key)
	if err != nil {
		res.Error = err.Error()
		if strings.Contains(err.Error(), "HTTP 401") || strings.Contains(err.Error(), "HTTP 403") {
			res.AuthFailed = true
		}
		return
	}
	res.Key = &k
	status, body, _, err := pr.httpGet(ctx, p, key, p.BaseURL+"/models/user")
	if !listStatus(res, status, body, err, "GET /models/user") {
		return
	}
	models, perr := parseListing(body, p, nil)
	if perr != nil {
		res.Error = "GET /models/user: " + perr.Error()
		return
	}
	res.Models, res.OK = models, true
}

func (pr *Prober) openRouterKey(ctx context.Context, p Provider, key string) (OpenRouterKey, error) {
	status, body, _, err := pr.httpGet(ctx, p, key, p.BaseURL+"/key")
	if err != nil {
		return OpenRouterKey{}, fmt.Errorf("GET /key: %w", err)
	}
	if status != 200 {
		return OpenRouterKey{}, fmt.Errorf("GET /key: HTTP %d", status)
	}
	var env struct {
		Data OpenRouterKey `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return OpenRouterKey{}, fmt.Errorf("GET /key: %w", err)
	}
	return env.Data, nil
}

// OpenRouterKey polls GET /api/v1/key for a registered OpenRouter provider.
func (pr *Prober) OpenRouterKey(ctx context.Context, providerID string) (OpenRouterKey, error) {
	p, err := pr.reg.Get(ctx, providerID)
	if err != nil {
		return OpenRouterKey{}, err
	}
	if p.Flavor != FlavorOpenRouter {
		return OpenRouterKey{}, fmt.Errorf("%w: provider %s is not an OpenRouter provider", ErrInvalid, providerID)
	}
	key, err := pr.reg.APIKey(ctx, p)
	if err != nil {
		return OpenRouterKey{}, err
	}
	return pr.openRouterKey(ctx, p, key)
}

func (pr *Prober) probeAnthropic(ctx context.Context, p Provider, key string, res *ProbeResult) {
	status, body, _, err := pr.httpGet(ctx, p, key, p.BaseURL+"/v1/models")
	if !listStatus(res, status, body, err, "GET /v1/models") {
		return
	}
	models, perr := parseListing(body, p, func(m *Model, raw map[string]any) {
		m.SupportsTools = true // every Messages API model accepts tools
		m.ContextLength = intOf(raw["max_input_tokens"])
		m.MaxOutput = intOf(raw["max_tokens"])
		m.SupportsReasoning = true
	})
	if perr != nil {
		res.Error = "GET /v1/models: " + perr.Error()
		return
	}
	res.Models, res.OK = models, true
}

// probeGemini lists models through the OpenAI-compat endpoint; whether that endpoint lists with a key is unverified
// (MP C), so a 404 falls back to the native v1beta listing, which also carries token limits.
func (pr *Prober) probeGemini(ctx context.Context, p Provider, key string, res *ProbeResult) {
	status, body, _, err := pr.httpGet(ctx, p, key, p.BaseURL+"/models")
	if err == nil && status == http.StatusNotFound {
		pr.probeGeminiNative(ctx, p, key, res)
		return
	}
	if !listStatus(res, status, body, err, "GET /models") {
		return
	}
	models, perr := parseListing(body, p, func(m *Model, _ map[string]any) {
		m.ID = strings.TrimPrefix(m.ID, "models/")
		m.SupportsTools = strings.HasPrefix(m.ID, "gemini") // function calling is documented for the compat endpoint
	})
	if perr != nil {
		res.Error = "GET /models: " + perr.Error()
		return
	}
	res.Models, res.OK = models, true
}

func (pr *Prober) probeGeminiNative(ctx context.Context, p Provider, key string, res *ProbeResult) {
	status, body, _, err := pr.httpGetAs(ctx, p, key, origin(p.BaseURL)+"/v1beta/models?pageSize=1000", "goog")
	if !listStatus(res, status, body, err, "GET /v1beta/models") {
		return
	}
	var l struct {
		Models []struct {
			Name             string   `json:"name"`
			InputTokenLimit  int      `json:"inputTokenLimit"`
			OutputTokenLimit int      `json:"outputTokenLimit"`
			Methods          []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &l); err != nil {
		res.Error = "GET /v1beta/models: " + err.Error()
		return
	}
	for _, m := range l.Models {
		gen := false
		for _, x := range m.Methods {
			gen = gen || x == "generateContent"
		}
		if !gen {
			continue
		}
		id := strings.TrimPrefix(m.Name, "models/")
		res.Models = append(res.Models, Model{ProviderID: p.ID, ID: id, ContextLength: m.InputTokenLimit, MaxOutput: m.OutputTokenLimit,
			SupportsTools: strings.HasPrefix(id, "gemini"), DataClass: p.DataClass, Tier: p.Tier, PriceSource: "catalog", Source: "listing"})
	}
	res.OK = true
	res.Warnings = append(res.Warnings, "gemini prices are not in the listing: set them in the price table")
}

// parseListing parses an OpenAI-style {"data":[...]} listing, including OpenRouter and vLLM extensions.
func parseListing(body []byte, p Provider, tweak func(*Model, map[string]any)) ([]Model, error) {
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("unreadable listing: %w", err)
	}
	out := make([]Model, 0, len(env.Data))
	for _, raw := range env.Data {
		id, _ := raw["id"].(string)
		if id == "" {
			continue
		}
		m := Model{ProviderID: p.ID, ID: id, PriceSource: "catalog", Source: "listing", DataClass: p.DataClass, Tier: p.Tier}
		m.ContextLength = intOf(raw["context_length"])
		if m.ContextLength == 0 {
			m.ContextLength = intOf(raw["max_model_len"]) // vLLM
		}
		if tp, ok := raw["top_provider"].(map[string]any); ok {
			m.MaxOutput = intOf(tp["max_completion_tokens"])
			if m.ContextLength == 0 {
				m.ContextLength = intOf(tp["context_length"])
			}
		}
		if sp, ok := raw["supported_parameters"].([]any); ok {
			for _, s := range sp {
				if str, ok := s.(string); ok {
					m.SupportedParameters = append(m.SupportedParameters, str)
					switch str {
					case "tools":
						m.SupportsTools = true
					case "reasoning", "include_reasoning":
						m.SupportsReasoning = true
					}
				}
			}
			sort.Strings(m.SupportedParameters)
		}
		if pr, ok := raw["pricing"].(map[string]any); ok { // OpenRouter: USD per token, as strings
			in, out2 := perM(pr["prompt"]), perM(pr["completion"])
			m.InputPerM, m.OutputPerM = in, out2
			m.CacheReadPerM, m.CacheWritePerM = perM(pr["input_cache_read"]), perM(pr["input_cache_write"])
			if p.Flavor == FlavorOpenRouter && in == 0 && out2 == 0 {
				m.IsFree = true
			}
		}
		if p.Flavor == FlavorOpenRouter && strings.HasSuffix(id, ":free") {
			m.IsFree = true
		}
		if m.IsFree && p.Flavor == FlavorOpenRouter { // free OpenRouter endpoints may train or publish (MP A.10)
			m.DataClass, m.Tier = DataTrainable, TierFree
		}
		if p.Kind == KindHost {
			m.IsFree = true // no marginal token price; the host's hourly_cost is accounted separately
		}
		if tweak != nil {
			tweak(&m, raw)
		}
		out = append(out, m)
	}
	return out, nil
}

func perM(v any) float64 {
	f := floatOf(v)
	return f * 1e6
}

func floatOf(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	}
	return 0
}

func intOf(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	}
	return 0
}

// CheckResult is one liveness check.
type CheckResult struct {
	OK      bool
	Latency time.Duration
	Detail  string
}

// Check runs the liveness check used by the health loop: hosts answer /health (Ollama /api/version); other kinds
// answer their model listing.
func (pr *Prober) Check(ctx context.Context, p Provider) CheckResult {
	key, _ := pr.reg.APIKey(ctx, p) // /health is unauthenticated on vLLM and SGLang; a missing key must not mask liveness
	start := time.Now()
	target := p.BaseURL + "/models"
	if p.Kind == KindHost {
		target = origin(p.BaseURL) + "/health"
		if p.Flavor == FlavorOllama {
			target = origin(p.BaseURL) + "/api/version"
		}
	}
	status, _, _, err := pr.httpGet(ctx, p, key, target)
	lat := time.Since(start)
	switch {
	case err != nil:
		return CheckResult{Latency: lat, Detail: err.Error()}
	case status >= 200 && status < 300:
		return CheckResult{OK: true, Latency: lat}
	default:
		return CheckResult{Latency: lat, Detail: fmt.Sprintf("HTTP %d", status)}
	}
}

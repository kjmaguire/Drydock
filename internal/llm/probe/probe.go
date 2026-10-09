// Package probe is the startup tool-call probe and the cheap-model selection of docs/model-routing.md s5 (decision D4).
//
// No model is hard-coded as "the cheap model". The catalog of a provider (what the user's account exposes), the prices in
// it and the result of a tiny tool-calling request decide. A candidate must be paid, private, tool-capable and large enough;
// the cheapest few are probed through the llm.Wrapper (so the data_class guard, quota, retry and usage recording apply);
// the cheapest model that passes every run is cheap_default and the next one cheap_fallback. The probe sends only fixed
// synthetic text (no user data). It proves protocol-level tool calling, not agentic competence.
//
// router must not import llm (deterministic packages stay LLM-free), so this package lives beside llm and only writes the
// catalog's probe_status, which the router reads through the registry.
package probe

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Purpose labels the probe's usage rows; evals exclude it from cost per accepted task and list it as overhead
// (docs/evals.md s3.9).
const Purpose = "tool_call_probe"

// Defaults are design assumptions from docs/model-routing.md s5, tuned with the eval harness.
const (
	DefaultRuns       = 3
	DefaultTopN       = 5
	DefaultMinContext = 64_000
	DefaultTTL        = 7 * 24 * time.Hour
	// the blended price weights input:output 3:1
	inputWeight, outputWeight = 3.0, 1.0
)

// Prober runs probes and selects.
type Prober struct {
	Client   llm.Client // the Wrapper
	Registry *providers.Registry
	DB       *storage.DB
	Bus      events.Bus // optional
	Now      func() time.Time

	Runs       int           // probe runs per model, all of which must pass (default 3)
	TopN       int           // how many candidates to probe (default 5)
	MinContext int           // minimum context window; a model that does not list one is probed and flagged (default 64k)
	TTL        time.Duration // a pass younger than this is not probed again unless forced (default 7 days)
}

func (p *Prober) defaults() {
	if p.Now == nil {
		p.Now = func() time.Time { return time.Now().UTC() }
	}
	if p.Runs <= 0 {
		p.Runs = DefaultRuns
	}
	if p.TopN <= 0 {
		p.TopN = DefaultTopN
	}
	if p.MinContext <= 0 {
		p.MinContext = DefaultMinContext
	}
	if p.TTL <= 0 {
		p.TTL = DefaultTTL
	}
}

// Candidate is a model that may be probed.
type Candidate struct {
	Model          providers.Model `json:"model"`
	BlendedPerM    float64         `json:"blendedPerM"` // USD per million tokens at 3:1 input:output
	ContextUnknown bool            `json:"contextUnknown,omitempty"`
}

// Skip names a model that is not a candidate and why.
type Skip struct {
	Model  string `json:"model"`
	Reason string `json:"reason"`
}

// Rank filters a provider's catalog to the candidates for cheap_default and orders them cheapest first. It is a pure
// function of the catalog rows and the provider; the effective data class of each model is passed in.
func Rank(prov providers.Provider, models []providers.Model, class func(providers.Model) providers.DataClass, minContext int) (ranked []Candidate, skipped []Skip) {
	// a self-hosted model server (Ollama, vLLM) has no per-token price: its models are free to call and private when the
	// host is, and its cost is the host's own (hourly_cost), so "free" and "no price" do not disqualify them
	host := prov.Kind == providers.KindHost
	for _, m := range models {
		skip := func(r string) { skipped = append(skipped, Skip{Model: m.ID, Reason: r}) }
		switch {
		case !prov.Enabled:
			skip("provider disabled")
		case !host && (m.IsFree || strings.HasSuffix(m.ID, ":free") || m.ID == "openrouter/free"):
			skip("free route (trainable, public-docs CONSULT only)")
		case class(m) != providers.DataPrivate:
			skip("not private (trainable models never carry private work)")
		case !prov.PrivateEligible():
			skip("provider cannot carry private context (plain http LAN host without attestation)")
		case !m.SupportsTools:
			skip("tool calling not advertised")
		case !host && (!m.PriceKnown() || (m.InputPerM <= 0 && m.OutputPerM <= 0)):
			skip("price unknown or zero")
		case m.ContextLength > 0 && m.ContextLength < minContext:
			skip(fmt.Sprintf("context %d below the minimum %d", m.ContextLength, minContext))
		default:
			ranked = append(ranked, Candidate{Model: m, BlendedPerM: (inputWeight*m.InputPerM + outputWeight*m.OutputPerM) / (inputWeight + outputWeight), ContextUnknown: m.ContextLength == 0})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].BlendedPerM != ranked[j].BlendedPerM {
			return ranked[i].BlendedPerM < ranked[j].BlendedPerM
		}
		return ranked[i].Model.ID < ranked[j].Model.ID
	})
	return ranked, skipped
}

// Failure modes recorded in probe_results.
const (
	FailNoToolCall     = "no_tool_call"
	FailBadArguments   = "bad_arguments"
	FailWrongTool      = "wrong_tool"
	FailLeakedToolText = "leaked_tool_text"
	FailUnavailablePvt = "unavailable_private"
)

var tools = []llm.ToolDef{
	{Name: "add_numbers", Description: "Add two integers and return the sum.",
		Parameters: json.RawMessage(`{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}},"required":["a","b"],"additionalProperties":false}`)},
	{Name: "word_length", Description: "Return the number of characters in a word.",
		Parameters: json.RawMessage(`{"type":"object","properties":{"word":{"type":"string"}},"required":["word"],"additionalProperties":false}`)},
}

// request is the fixed probe request: two tools, one deterministic instruction, synthetic content only.
func request(providerID, model string) llm.Request {
	zero := 0.0
	return llm.Request{
		Provider: providerID, Model: model, MaxTokens: 256, Temperature: &zero,
		System:     []llm.Block{{Text: "You are being tested for tool calling. Answer only by calling the right tool."}},
		Messages:   []llm.Message{{Role: "user", Content: "Use the add_numbers tool to add 17 and 25."}},
		Tools:      tools,
		ToolChoice: "auto",
		Purpose:    Purpose, NoCache: true,
		Manifest: &llm.Manifest{Entries: []llm.ManifestEntry{{SourceType: "tool_call_probe", Sensitivity: llm.SensitivitySynthetic}}},
	}
}

// Judge decides whether one response passes: structured tool_calls present, arguments valid JSON matching the schema,
// the correct tool chosen, and no tool-call text leaked into the content (the failure shape of models that emit calls as
// prose). It returns "" on a pass or the failure mode.
func Judge(resp llm.Response) string {
	leaked := leakedToolText(resp.Content)
	if len(resp.ToolCalls) == 0 {
		if leaked {
			return FailLeakedToolText
		}
		return FailNoToolCall
	}
	if leaked {
		return FailLeakedToolText
	}
	tc := resp.ToolCalls[0]
	if tc.Name != "add_numbers" {
		return FailWrongTool
	}
	var args map[string]any
	dec := json.NewDecoder(strings.NewReader(tc.Arguments))
	dec.UseNumber()
	if err := dec.Decode(&args); err != nil || len(args) != 2 {
		return FailBadArguments
	}
	for k, want := range map[string]int64{"a": 17, "b": 25} {
		n, ok := args[k].(json.Number)
		if !ok {
			return FailBadArguments
		}
		if v, err := n.Int64(); err != nil || v != want {
			return FailBadArguments
		}
	}
	return ""
}

func leakedToolText(content string) bool {
	c := strings.ToLower(content)
	for _, marker := range []string{"add_numbers(", "<tool_call", "<function", "\"name\": \"add_numbers\"", "\"name\":\"add_numbers\"", "[tool_calls]", "<|tool_call"} {
		if strings.Contains(c, marker) {
			return true
		}
	}
	return false
}

// ErrQuotaReached means the probe did not run because Drydock's own quota for the provider refused the call.
var ErrQuotaReached = errors.New("the request limit you set for this provider is reached, so the probe did not run")

// ModelResult is the outcome of probing one model.
type ModelResult struct {
	Provider    string    `json:"provider"`
	Model       string    `json:"model"`
	Pass        bool      `json:"pass"`
	Runs        int       `json:"runs"`
	Passes      int       `json:"passes"`
	FailureMode string    `json:"failureMode,omitempty"`
	LatencyMS   int64     `json:"latencyMs"`
	Served      string    `json:"served,omitempty"` // the model id the provider reports having served
	CostUSD     float64   `json:"costUsd"`          // what the probe cost, from the call's own cost figure; unknown is not counted
	CostKnown   bool      `json:"costKnown"`
	Cached      bool      `json:"cached,omitempty"` // a pass still inside its TTL, not probed again
	ProbedAt    time.Time `json:"probedAt"`
}

// Probe runs the probe against one model: Runs requests, all of which must pass. The status is recorded in the catalog
// (Registry.SetProbeStatus) and a probe_results row is written.
func (p *Prober) Probe(ctx context.Context, providerID, model string) (ModelResult, error) {
	p.defaults()
	res := ModelResult{Provider: providerID, Model: model, Runs: p.Runs, ProbedAt: p.Now()}
	var totalLatency time.Duration
	var ran int
	for i := 0; i < p.Runs; i++ {
		resp, err := p.Client.Complete(ctx, request(providerID, model))
		if err != nil {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			var qe *llm.QuotaError
			if errors.As(err, &qe) {
				// Drydock's own request limit for this provider refused the call: nothing was learned about the model, so
				// nothing is recorded (it would read as a failed probe and unset a good cheap_default)
				return res, fmt.Errorf("%w: %v", ErrQuotaReached, err)
			}
			res.FailureMode = classify(err)
			break
		}
		ran++
		totalLatency += resp.Latency
		if resp.ModelUsed != "" {
			res.Served = resp.ModelUsed
		}
		if resp.Cost.Known {
			res.CostUSD += resp.Cost.USD
			res.CostKnown = true
		}
		if mode := Judge(resp); mode != "" {
			res.FailureMode = mode
			break
		}
		res.Passes++
	}
	res.Pass = res.Passes == p.Runs
	if ran > 0 {
		res.LatencyMS = (totalLatency / time.Duration(ran)).Milliseconds()
	}
	if err := p.record(ctx, res); err != nil {
		return res, err
	}
	return res, nil
}

func classify(err error) string {
	var ae *llm.APIError
	var de *llm.DataClassError
	var qe *llm.QuotaError
	switch {
	case errors.As(err, &de):
		return "data_class_refused"
	case errors.As(err, &qe):
		return "error:quota"
	case errors.As(err, &ae):
		if ae.Kind == llm.KindNoEndpoint {
			return FailUnavailablePvt
		}
		return "error:" + string(ae.Kind)
	}
	return "error:" + trunc(err.Error(), 80)
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (p *Prober) record(ctx context.Context, r ModelResult) error {
	status := "pass"
	if !r.Pass {
		status = "fail:" + r.FailureMode
		if r.FailureMode == FailUnavailablePvt {
			status = FailUnavailablePvt
		}
	}
	class := ""
	if prov, err := p.Registry.Get(ctx, r.Provider); err == nil {
		class = string(p.Registry.EffectiveDataClass(ctx, prov, r.Model))
	}
	err := p.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO probe_results(provider_id,model,ts,pass,runs,passes,latency_ms,failure_mode,endpoint,data_class) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			r.Provider, r.Model, r.ProbedAt.UnixMilli(), b2i(r.Pass), r.Runs, r.Passes, r.LatencyMS, r.FailureMode, r.Served, class)
		return err
	})
	if err != nil {
		return err
	}
	return p.Registry.SetProbeStatus(ctx, r.Provider, r.Model, status, r.ProbedAt)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Selection is the result of selecting a provider's cheap models.
type Selection struct {
	ProviderID string        `json:"providerId"`
	Default    string        `json:"default,omitempty"`  // cheap_default: the cheapest candidate that passed
	Fallback   string        `json:"fallback,omitempty"` // cheap_fallback: the next one
	Probed     []ModelResult `json:"probed"`
	Skipped    []Skip        `json:"skipped"`
	At         time.Time     `json:"at"`
	Message    string        `json:"message"`
}

// Select probes the cheapest TopN candidates of the provider and chooses cheap_default and cheap_fallback. force probes
// even models whose pass is still inside the TTL. The selection is stored (settings key route.cheap.<provider>) so the
// router's callers and the Providers page can read it; when nothing passes, Default is empty and Message says so plainly
// (the cheap-first rules then degrade to the next available private tier, brief 0).
func (p *Prober) Select(ctx context.Context, providerID string, force bool) (Selection, error) {
	p.defaults()
	prov, err := p.Registry.Get(ctx, providerID)
	if err != nil {
		return Selection{}, err
	}
	models, err := p.Registry.Models(ctx, providerID)
	if err != nil {
		return Selection{}, err
	}
	ranked, skipped := Rank(prov, models, func(m providers.Model) providers.DataClass { return p.Registry.EffectiveDataClass(ctx, prov, m.ID) }, p.MinContext)
	sel := Selection{ProviderID: providerID, Skipped: skipped, At: p.Now(), Probed: []ModelResult{}}
	if sel.Skipped == nil {
		sel.Skipped = []Skip{}
	}
	if len(models) == 0 {
		sel.Message = "the provider's catalog is empty: refresh it first (Test on the provider)"
		return sel, p.store(ctx, sel)
	}
	for i, c := range ranked {
		if i >= p.TopN {
			break
		}
		if !force && c.Model.ProbeStatus == "pass" && p.Now().Sub(c.Model.ProbeAt) < p.TTL {
			sel.Probed = append(sel.Probed, ModelResult{Provider: providerID, Model: c.Model.ID, Pass: true, Cached: true, ProbedAt: c.Model.ProbeAt})
			continue
		}
		r, err := p.Probe(ctx, providerID, c.Model.ID)
		if errors.Is(err, ErrQuotaReached) {
			// keep what was chosen before: the limit says nothing about the models
			prev, ok, perr := Stored(ctx, p.DB, providerID)
			if perr != nil || !ok {
				prev = Selection{ProviderID: providerID, Skipped: sel.Skipped, Probed: sel.Probed, At: sel.At}
			}
			prev.Message = "probing stopped: " + ErrQuotaReached.Error() + "; the earlier selection is kept"
			return prev, nil
		}
		if err != nil {
			return sel, err
		}
		sel.Probed = append(sel.Probed, r)
		p.emit(ctx, "provider.probe_completed", map[string]any{"provider": providerID, "model": c.Model.ID, "pass": r.Pass, "failureMode": r.FailureMode, "runs": r.Runs, "passes": r.Passes})
	}
	for _, r := range sel.Probed {
		switch {
		case !r.Pass:
		case sel.Default == "":
			sel.Default = r.Model
		case sel.Fallback == "":
			sel.Fallback = r.Model
		}
	}
	switch {
	case len(ranked) == 0:
		sel.Message = "no model of this provider qualifies (paid, private, tool-capable, large enough); cheap-first rules degrade to the next available private tier"
	case sel.Default == "":
		sel.Message = fmt.Sprintf("none of the %d probed model(s) passed the tool-call probe; cheap_default is unset and cheap-first rules degrade to the next available private tier", len(sel.Probed))
	default:
		sel.Message = "cheap_default " + sel.Default
		if sel.Fallback != "" {
			sel.Message += ", cheap_fallback " + sel.Fallback
		}
		sel.Message += " (the probe proves protocol-level tool calling, not agentic competence)"
	}
	return sel, p.store(ctx, sel)
}

func (p *Prober) emit(ctx context.Context, typ string, data map[string]any) {
	if p.Bus != nil {
		_, _ = events.Emit(ctx, p.Bus, typ, events.Event{}, data)
	}
}

// SettingKey is where a provider's selection is stored.
func SettingKey(providerID string) string { return "route.cheap." + providerID }

func (p *Prober) store(ctx context.Context, s Selection) error {
	b, _ := json.Marshal(s)
	return p.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, SettingKey(s.ProviderID), string(b))
		return err
	})
}

// Stored returns the last selection for a provider.
func Stored(ctx context.Context, db *storage.DB, providerID string) (Selection, bool, error) {
	var v string
	err := db.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, SettingKey(providerID)).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return Selection{}, false, nil
	}
	if err != nil {
		return Selection{}, false, err
	}
	var s Selection
	if err := json.Unmarshal([]byte(v), &s); err != nil {
		return Selection{}, false, err
	}
	return s, true, nil
}

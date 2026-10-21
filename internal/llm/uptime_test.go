package llm_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/llm/llmtest"
	"github.com/kjmaguire/drydock/internal/providers"
)

// OpenRouter endpoint uptime through the Wrapper (decision P53 c). The server is a TEST DOUBLE of OpenRouter: the
// llmtest PROTOCOL FIXTURE for POST /api/v1/chat/completions plus a stand-in for GET
// /api/v1/models/{author}/{slug}/endpoints in the shape docs/phase0/model-providers.md A.12 records. What is asserted is
// the outgoing request body; nothing here proves OpenRouter honours provider.ignore.

const orModel = "acme/coder-7b"
const orEndpoints = "/api/v1/models/acme/coder-7b/endpoints"

type orDouble struct {
	srv     *llmtest.Server
	lookups atomic.Int32
	p       providers.Provider
}

func endpoint(tag string, uptime30m any) map[string]any {
	return map[string]any{"name": "X | " + orModel, "provider_name": "Upstream " + tag, "tag": tag, "status": 0,
		"uptime_last_30m": uptime30m, "pricing": map[string]any{"prompt": "0.0000001", "completion": "0.0000002"}}
}

// newORDouble registers an OpenRouter provider (with the given stored overrides) over the double. endpoints answers the
// endpoints lookup; nil answers 500.
func newORDouble(t *testing.T, e *env, chat llmtest.Handler, overrides string, endpoints ...map[string]any) *orDouble {
	t.Helper()
	d := &orDouble{srv: llmtest.New(t, chat)}
	d.srv.Routes[orEndpoints] = func(w http.ResponseWriter, r *http.Request) {
		d.lookups.Add(1)
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			http.Error(w, "unauthorized", 401)
			return
		}
		if endpoints == nil {
			http.Error(w, "upstream trouble", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": orModel, "endpoints": endpoints}})
	}
	np := providers.NewProvider{Name: "OpenRouter", Kind: providers.KindAggregator, BaseURL: d.srv.URL + "/api/v1", Flavor: providers.FlavorOpenRouter}
	if overrides != "" {
		np.RequestOverrides = json.RawMessage(overrides)
	}
	d.p = e.provider(np)
	return d
}

// newUptimeEnv is newEnv with the uptime tracker on the Wrapper's clock.
func newUptimeEnv(t *testing.T) *env {
	return newEnv(t, func(o *llm.Options) {
		o.Uptime = providers.NewEndpointUptime(o.Registry, providers.UptimeOptions{Now: o.Now, Timeout: 300 * time.Millisecond})
	})
}

func orReq(d *orDouble, m *llm.Manifest) llm.Request {
	r := req(d.p, m)
	r.Model = orModel
	return r
}

func provObj(t *testing.T, c llmtest.Call) map[string]any {
	t.Helper()
	p, _ := c.Body["provider"].(map[string]any)
	return p
}

func ignoreOf(t *testing.T, c llmtest.Call) []string {
	t.Helper()
	raw, ok := provObj(t, c)["ignore"].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range raw {
		out = append(out, v.(string))
	}
	return out
}

func TestOpenRouterLowUptimeEndpointIsIgnoredInTheRequest(t *testing.T) {
	e := newUptimeEnv(t)
	d := newORDouble(t, e, okHandler("ok", nil), `{"provider":{"ignore":["banned"]}}`,
		endpoint("steady", 99.9), endpoint("flaky", 91.2), endpoint("fresh", nil))
	for _, stream := range []bool{false, true} {
		var err error
		if stream {
			_, err = e.w.Stream(e.ctx, orReq(d, privateManifest), func(llm.Delta) error { return nil })
		} else {
			_, err = e.w.Complete(e.ctx, orReq(d, privateManifest))
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range d.srv.Calls() {
		if got := ignoreOf(t, c); !reflect.DeepEqual(got, []string{"banned", "flaky"}) {
			t.Fatalf("provider.ignore %v, want the stored ignore plus the low-uptime endpoint", got)
		}
		if p := provObj(t, c); p["data_collection"] != "deny" || p["zdr"] != true || p["order"] != nil {
			t.Fatalf("provider object %v: the privacy filter must stay and no order may be set (it would end sticky routing)", p)
		}
	}
	// The change is announced once, with what the person needs to see; the key is in no event.
	evs := e.events("provider.endpoints_avoided")
	if len(evs) != 1 {
		t.Fatalf("%d provider.endpoints_avoided events, want 1 for an unchanged set", len(evs))
	}
	var data struct {
		ProviderID string                      `json:"providerId"`
		Model      string                      `json:"model"`
		Threshold  float64                     `json:"threshold"`
		Avoided    []providers.AvoidedEndpoint `json:"avoided"`
	}
	if err := json.Unmarshal(evs[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.ProviderID != d.p.ID || data.Model != orModel || data.Threshold != 98 || len(data.Avoided) != 1 || data.Avoided[0].Tag != "flaky" || data.Avoided[0].UptimePct != 91.2 {
		t.Fatalf("event %+v", data)
	}
	if strings.Contains(string(evs[0].Data), "sk-test") {
		t.Fatal("the API key reached an event")
	}
}

func TestOpenRouterUptimeLookupIsCached(t *testing.T) {
	e := newUptimeEnv(t)
	d := newORDouble(t, e, okHandler("ok", nil), "", endpoint("steady", 100), endpoint("flaky", 50))
	for range 3 {
		if _, err := e.w.Complete(e.ctx, orReq(d, publicManifest)); err != nil {
			t.Fatal(err)
		}
	}
	if n := d.lookups.Load(); n != 1 {
		t.Fatalf("%d endpoint lookups for 3 calls within 5 minutes, want 1", n)
	}
	e.clk.add(5*time.Minute + time.Second)
	if _, err := e.w.Complete(e.ctx, orReq(d, publicManifest)); err != nil {
		t.Fatal(err)
	}
	if n := d.lookups.Load(); n != 2 {
		t.Fatalf("%d endpoint lookups after the cache expired, want 2", n)
	}
	for _, c := range d.srv.Calls() {
		if got := ignoreOf(t, c); !reflect.DeepEqual(got, []string{"flaky"}) {
			t.Fatalf("call %d: provider.ignore %v", c.N, got)
		}
	}
}

func TestOpenRouterFailedUptimeLookupLeavesTheRequestUnchanged(t *testing.T) {
	// The same request with no tracker at all is the reference body.
	ref := newEnv(t, nil)
	rd := newORDouble(t, ref, okHandler("ok", nil), "")
	if _, err := ref.w.Complete(ref.ctx, orReq(rd, privateManifest)); err != nil {
		t.Fatal(err)
	}
	want := rd.srv.Calls()[0].Body

	e := newUptimeEnv(t)
	d := newORDouble(t, e, okHandler("ok", nil), "") // nil endpoints: the lookup answers 500
	if _, err := e.w.Complete(e.ctx, orReq(d, privateManifest)); err != nil {
		t.Fatalf("a failed uptime lookup blocked the call: %v", err)
	}
	if got := d.srv.Calls()[0].Body; !reflect.DeepEqual(got, want) {
		t.Fatalf("request changed by a failed lookup:\n got %v\nwant %v", got, want)
	}
	if d.lookups.Load() != 1 || len(e.events("provider.endpoints_avoided")) != 0 {
		t.Fatal("lookup not attempted, or an avoidance announced without one")
	}

	// A lookup that hangs is cut off by its timeout and the call goes out unchanged.
	e2 := newUptimeEnv(t)
	d2 := newORDouble(t, e2, okHandler("ok", nil), "", endpoint("x", 10), endpoint("y", 100))
	d2.srv.Routes[orEndpoints] = func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	start := time.Now()
	if _, err := e2.w.Complete(e2.ctx, orReq(d2, privateManifest)); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second || ignoreOf(t, d2.srv.Calls()[0]) != nil {
		t.Fatalf("hung lookup: took %s, ignore %v", time.Since(start), ignoreOf(t, d2.srv.Calls()[0]))
	}
}

func TestOpenRouterAllEndpointsBelowThresholdKeepsTheBest(t *testing.T) {
	e := newUptimeEnv(t)
	d := newORDouble(t, e, okHandler("ok", nil), "", endpoint("a", 70), endpoint("b", 93.5), endpoint("c", 88))
	if _, err := e.w.Complete(e.ctx, orReq(d, publicManifest)); err != nil {
		t.Fatal(err)
	}
	if got := ignoreOf(t, d.srv.Calls()[0]); !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Fatalf("provider.ignore %v, want every endpoint but the best (b)", got)
	}
	var data struct {
		KeptBest bool `json:"keptBest"`
	}
	if evs := e.events("provider.endpoints_avoided"); len(evs) != 1 || json.Unmarshal(evs[0].Data, &data) != nil || !data.KeptBest {
		t.Fatalf("the event must say the best were kept: %d events %+v", len(evs), data)
	}
}

func TestOpenRouterUptimeThresholdSetting(t *testing.T) {
	e := newUptimeEnv(t)
	d := newORDouble(t, e, okHandler("ok", nil), "", endpoint("steady", 99.5), endpoint("ok", 95), endpoint("poor", 80))
	call := func() []string {
		t.Helper()
		if _, err := e.w.Complete(e.ctx, orReq(d, publicManifest)); err != nil {
			t.Fatal(err)
		}
		cs := d.srv.Calls()
		return ignoreOf(t, cs[len(cs)-1])
	}
	if got := call(); !reflect.DeepEqual(got, []string{"ok", "poor"}) {
		t.Fatalf("default 98%%: %v", got)
	}
	if err := e.reg.SetMinEndpointUptime(e.ctx, d.p.ID, 90); err != nil {
		t.Fatal(err)
	}
	if got := call(); !reflect.DeepEqual(got, []string{"poor"}) {
		t.Fatalf("90%%: %v", got)
	}
	if err := e.reg.SetMinEndpointUptime(e.ctx, d.p.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got := call(); got != nil {
		t.Fatalf("0 turns it off, got %v", got)
	}
	if n := len(e.events("provider.endpoints_avoided")); n != 3 {
		t.Fatalf("%d announcements for three different sets, want 3", n)
	}
}

// When skipping the low-uptime endpoints leaves OpenRouter nothing that fits (a private route's data policy narrows the
// set further than the uptime list shows), the call is sent again as it would have gone without the avoidance.
func TestOpenRouterNoEndpointAfterAvoidingRetriesWithout(t *testing.T) {
	e := newUptimeEnv(t)
	d := newORDouble(t, e, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		if p, _ := c.Body["provider"].(map[string]any); p["ignore"] != nil {
			llmtest.WriteError(w, 404, nil, map[string]any{"error": map[string]any{"code": 404, "message": "No endpoints found matching your data policy (Zero data retention)."}})
			return
		}
		llmtest.Respond(w, c, llmtest.Completion{Model: orModel, Content: "ok"})
	}, "", endpoint("zdr-only", 90), endpoint("retains", 100))
	resp, err := e.w.Complete(e.ctx, orReq(d, privateManifest))
	if err != nil || resp.Content != "ok" {
		t.Fatalf("avoidance blocked the call: %v", err)
	}
	cs := d.srv.Calls()
	if len(cs) != 2 || ignoreOf(t, cs[0]) == nil || ignoreOf(t, cs[1]) != nil {
		t.Fatalf("calls %d: first with ignore, second without, got %v / %v", len(cs), ignoreOf(t, cs[0]), ignoreOf(t, cs[len(cs)-1]))
	}
	if p := provObj(t, cs[1]); p["data_collection"] != "deny" || p["zdr"] != true {
		t.Fatalf("retry lost the privacy filter: %v", p)
	}
}

// Other flavors are never looked up or changed.
func TestUptimeAvoidanceIsOpenRouterOnly(t *testing.T) {
	e := newUptimeEnv(t)
	srv := llmtest.New(t, okHandler("ok", nil))
	var lookups atomic.Int32
	srv.Routes["/v1/models/acme/coder-7b/endpoints"] = func(w http.ResponseWriter, r *http.Request) { lookups.Add(1) }
	p := e.provider(providers.NewProvider{Name: "compat", BaseURL: srv.URL + "/v1", Flavor: providers.FlavorOpenAI})
	r := req(p, publicManifest)
	r.Model = orModel
	if _, err := e.w.Complete(e.ctx, r); err != nil {
		t.Fatal(err)
	}
	if lookups.Load() != 0 || srv.Calls()[0].Body["provider"] != nil {
		t.Fatalf("a non-OpenRouter provider was looked up (%d) or given a provider object", lookups.Load())
	}
}

package openai_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/llm/llmtest"
	"github.com/kjmaguire/drydock/internal/llm/openai"
	"github.com/kjmaguire/drydock/internal/providers"
)

// These tests run the OpenAI-compatible backend against PROTOCOL FIXTURES (llmtest): local servers speaking the
// chat completions wire protocol. Tests against real providers are at the bottom, gated on environment variables.

func prov(srv *llmtest.Server, flavor providers.Flavor, class providers.DataClass) providers.Provider {
	return providers.Provider{ID: "prov_x", Name: "x", BaseURL: srv.URL + "/v1", Flavor: flavor, DataClass: class, Tier: providers.TierCheap,
		RequestOverrides: []byte(`{}`)}
}

func newClient(t *testing.T, p providers.Provider, key string) llm.Client {
	t.Helper()
	c, err := openai.New(p, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func f64(v float64) *float64 { return &v }

func TestCompleteParsesContentToolCallsUsageAndHeaders(t *testing.T) {
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.Respond(w, c, llmtest.Completion{
			Model: "vendor/model-x", Content: "hello", Reasoning: "thinking...",
			ToolCalls: []llmtest.ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`}},
			Usage:     &llmtest.Usage{PromptTokens: 1000, CompletionTokens: 50, CachedTokens: 600, CacheWriteTokens: 80, ReasoningTokens: 20, Cost: f64(0.00123)},
			Header:    map[string]string{"X-RateLimit-Remaining-Requests": "41", "X-RateLimit-Limit-Requests": "50"},
		})
	})
	c := newClient(t, prov(srv, providers.FlavorOpenRouter, providers.DataPrivate), "sk-key")
	resp, err := c.Complete(context.Background(), llm.Request{Model: "vendor/model-x", MaxTokens: 256, Effort: llm.EffortHigh,
		System:     []llm.Block{{Text: "sys A"}, {Text: "sys B", Cache: true}},
		Messages:   []llm.Message{{Role: "user", Content: "hi"}},
		Tools:      []llm.ToolDef{{Name: "read_file", Description: "read", Parameters: []byte(`{"type":"object","properties":{"path":{"type":"string"}}}`)}},
		ToolChoice: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello" || resp.Reasoning != "thinking..." || resp.FinishReason != "tool_calls" || resp.ModelUsed != "vendor/model-x" {
		t.Fatalf("response %+v", resp)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_1" || resp.ToolCalls[0].Name != "read_file" || resp.ToolCalls[0].Arguments != `{"path":"a.go"}` {
		t.Fatalf("tool calls %+v", resp.ToolCalls)
	}
	want := llm.Usage{Input: 1000, Output: 50, Cached: 600, CacheWrite: 80, Reasoning: 20}
	if resp.Usage != want || !resp.UsageReported {
		t.Fatalf("usage %+v reported=%v", resp.Usage, resp.UsageReported)
	}
	if resp.ReportedCostUSD == nil || *resp.ReportedCostUSD != 0.00123 {
		t.Fatalf("openrouter cost %v", resp.ReportedCostUSD)
	}
	if resp.Header.Get("X-RateLimit-Remaining-Requests") != "41" || !resp.EffortApplied {
		t.Fatalf("headers %v effort %v", resp.Header, resp.EffortApplied)
	}

	call := srv.Calls()[0]
	if call.Header.Get("Authorization") != "Bearer sk-key" || call.Path != "/v1/chat/completions" {
		t.Fatalf("request %s %v", call.Path, call.Header)
	}
	b := call.Body
	if b["model"] != "vendor/model-x" || b["max_tokens"] != float64(256) || b["tool_choice"] != "auto" {
		t.Fatalf("body %v", b)
	}
	if _, ok := b["stream_options"]; ok {
		t.Fatal("stream_options sent on a non-streaming call")
	}
	if rs := b["reasoning"].(map[string]any); rs["effort"] != "high" { // OpenRouter spells effort as a reasoning object
		t.Fatalf("reasoning %v", rs)
	}
	msgs := b["messages"].([]any)
	if sys := msgs[0].(map[string]any); sys["role"] != "system" || sys["content"] != "sys A\n\nsys B" {
		t.Fatalf("system message %v", sys)
	}
	tool := b["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if tool["name"] != "read_file" || tool["parameters"].(map[string]any)["type"] != "object" {
		t.Fatalf("tool %v", tool)
	}
}

func TestCostOnlyTrustedFromOpenRouter(t *testing.T) {
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.Respond(w, c, llmtest.Completion{Model: "m", Content: "x", Usage: &llmtest.Usage{PromptTokens: 1, CompletionTokens: 1, Cost: f64(5)}})
	})
	c := newClient(t, prov(srv, providers.FlavorOpenAI, providers.DataPrivate), "k")
	resp, err := c.Complete(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: "user", Content: "q"}}})
	if err != nil || resp.ReportedCostUSD != nil {
		t.Fatalf("a non-OpenRouter usage.cost must be ignored: %v %v", resp.ReportedCostUSD, err)
	}
}

func TestOpenRouterPrivacyFilterIsForcedOnPrivateRoutes(t *testing.T) {
	cases := []struct {
		name      string
		class     providers.DataClass
		model     string
		overrides string
		want      map[string]any // nil = no provider object
	}{
		{"private paid", providers.DataPrivate, "a/b", `{}`, map[string]any{"data_collection": "deny", "zdr": true}},
		{"weaker override is overruled", providers.DataPrivate, "a/b", `{"provider":{"data_collection":"allow","zdr":false,"order":["x"]}}`, map[string]any{"data_collection": "deny", "zdr": true, "order": []any{"x"}}},
		{"free model is not filtered", providers.DataPrivate, "a/b:free", `{}`, nil},
		{"trainable provider is not filtered", providers.DataTrainable, "a/b", `{}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
				llmtest.Respond(w, c, llmtest.Completion{Model: tc.model, Content: "x"})
			})
			p := prov(srv, providers.FlavorOpenRouter, tc.class)
			p.RequestOverrides = []byte(tc.overrides)
			c := newClient(t, p, "k")
			if _, err := c.Complete(context.Background(), llm.Request{Model: tc.model, SessionID: "sess-1", Messages: []llm.Message{{Role: "user", Content: "q"}}}); err != nil {
				t.Fatal(err)
			}
			b := srv.Calls()[0].Body
			got, _ := b["provider"].(map[string]any)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("provider object %v sent", got)
				}
			} else {
				for k, v := range tc.want {
					if gv := got[k]; !equal(gv, v) {
						t.Fatalf("provider.%s = %v, want %v (full %v)", k, gv, v, got)
					}
				}
			}
			if b["session_id"] != "sess-1" {
				t.Fatalf("session_id %v", b["session_id"])
			}
			if _, ok := b["x-openrouter-cache"]; ok || srv.Calls()[0].Header.Get("X-OpenRouter-Cache") != "" {
				t.Fatal("the OpenRouter response cache must never be requested")
			}
		})
	}
}

func equal(a, b any) bool {
	switch x := b.(type) {
	case []any:
		y, ok := a.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if x[i] != y[i] {
				return false
			}
		}
		return true
	}
	return a == b
}

func TestProviderObjectNeverSentToOtherFlavors(t *testing.T) {
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.Respond(w, c, llmtest.Completion{Model: "m", Content: "x"})
	})
	c := newClient(t, prov(srv, providers.FlavorVLLM, providers.DataPrivate), "")
	c.Complete(context.Background(), llm.Request{Model: "m", Effort: llm.EffortMax, SessionID: "s", Messages: []llm.Message{{Role: "user", Content: "q"}}})
	b := srv.Calls()[0].Body
	if _, ok := b["provider"]; ok {
		t.Fatal("provider object sent to a non-OpenRouter flavor")
	}
	if _, ok := b["session_id"]; ok {
		t.Fatal("session_id sent to a non-OpenRouter flavor")
	}
	if b["reasoning_effort"] != "high" { // max has no chat completions spelling; it is sent as high
		t.Fatalf("reasoning_effort %v", b["reasoning_effort"])
	}
	if srv.Calls()[0].Header.Get("Authorization") != "" {
		t.Fatal("a keyless host received an Authorization header")
	}
}

func TestEnvironmentCredentialsAreNotSentToTheProvider(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-from-env")
	t.Setenv("OPENAI_ORG_ID", "org-from-env")
	t.Setenv("OPENAI_PROJECT_ID", "proj-from-env")
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.Respond(w, c, llmtest.Completion{Model: "m", Content: "x"})
	})
	c := newClient(t, prov(srv, providers.FlavorOpenAI, providers.DataPrivate), "") // keyless host
	if _, err := c.Complete(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: "user", Content: "q"}}}); err != nil {
		t.Fatal(err)
	}
	h := srv.Calls()[0].Header
	for k, v := range h {
		if strings.Contains(strings.Join(v, ""), "from-env") {
			t.Fatalf("environment credential leaked in header %s: %v", k, v)
		}
	}
}

func TestReasoningReplayFieldDependsOnServer(t *testing.T) {
	for _, tc := range []struct {
		flavor providers.Flavor
		field  string
	}{{providers.FlavorVLLM, "reasoning"}, {providers.FlavorSGLang, "reasoning_content"}} {
		srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
			llmtest.Respond(w, c, llmtest.Completion{Model: "m", Content: "x"})
		})
		c := newClient(t, prov(srv, tc.flavor, providers.DataPrivate), "")
		_, err := c.Complete(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{
			{Role: "user", Content: "q"},
			{Role: "assistant", Reasoning: "step 1", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "ls", Arguments: `{}`}}},
			{Role: "tool", ToolCallID: "c1", Content: "out"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		msgs := srv.Calls()[0].Body["messages"].([]any)
		asst := msgs[1].(map[string]any)
		if asst[tc.field] != "step 1" || asst["content"] != nil {
			t.Fatalf("%s: assistant message %v", tc.flavor, asst)
		}
		if msgs[2].(map[string]any)["tool_call_id"] != "c1" {
			t.Fatalf("tool message %v", msgs[2])
		}
	}
}

func TestStreamAssemblesDeltasAndReadsFinalChunkUsage(t *testing.T) {
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.Respond(w, c, llmtest.Completion{Model: "m-served", Content: "streamed answer", Reasoning: "ponder", ReasoningField: "reasoning_content",
			ToolCalls: []llmtest.ToolCall{{ID: "call_9", Name: "grep", Arguments: `{"pattern":"TODO","path":"."}`}},
			Usage:     &llmtest.Usage{PromptTokens: 500, CompletionTokens: 30, CachedTokens: 200, CacheWriteTokens: 40, Cost: f64(0.01)},
			Header:    map[string]string{"X-RateLimit-Remaining-Tokens": "9000"}})
	})
	c := newClient(t, prov(srv, providers.FlavorOpenRouter, providers.DataPrivate), "k")
	var text, reasoning strings.Builder
	var deltas, toolFrags int
	resp, err := c.Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: "user", Content: "q"}}}, func(d llm.Delta) error {
		text.WriteString(d.Text)
		reasoning.WriteString(d.Reasoning)
		if d.ToolCall != nil {
			toolFrags++
		}
		deltas++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "streamed answer" || reasoning.String() != "ponder" || resp.Content != "streamed answer" || resp.Reasoning != "ponder" || toolFrags != 2 {
		t.Fatalf("deltas: text=%q reasoning=%q frags=%d resp=%+v", text.String(), reasoning.String(), toolFrags, resp)
	}
	// The accumulator in the SDK drops CacheWriteTokens; reading the final chunk ourselves keeps it.
	if resp.Usage.CacheWrite != 40 || resp.Usage.Cached != 200 || resp.Usage.Input != 500 || resp.Usage.Output != 30 || !resp.UsageReported {
		t.Fatalf("usage %+v", resp.Usage)
	}
	if resp.ReportedCostUSD == nil || *resp.ReportedCostUSD != 0.01 || resp.ModelUsed != "m-served" || resp.FinishReason != "tool_calls" {
		t.Fatalf("resp %+v", resp)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Arguments != `{"pattern":"TODO","path":"."}` || resp.ToolCalls[0].Name != "grep" {
		t.Fatalf("tool call assembled from fragments: %+v", resp.ToolCalls)
	}
	if resp.Header.Get("X-RateLimit-Remaining-Tokens") != "9000" {
		t.Fatal("stream response headers not captured")
	}
	b := srv.Calls()[0].Body
	if b["stream"] != true || b["stream_options"].(map[string]any)["include_usage"] != true {
		t.Fatalf("stream body %v", b)
	}
}

func TestStreamWithoutUsageIsMarkedNotReported(t *testing.T) {
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.Respond(w, c, llmtest.Completion{Model: "m", Content: "no usage here"})
	})
	c := newClient(t, prov(srv, providers.FlavorVLLM, providers.DataPrivate), "")
	resp, err := c.Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: "user", Content: "q"}}}, func(llm.Delta) error { return nil })
	if err != nil || resp.UsageReported || resp.Usage != (llm.Usage{}) {
		t.Fatalf("%v %+v", err, resp)
	}
}

func TestMidStreamErrorEventBecomesClassifiedError(t *testing.T) {
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.WriteStream(w, llmtest.Completion{Model: "m", Content: "partial text", ErrorAfter: map[string]any{"code": 429, "message": "Provider returned error"}})
	})
	c := newClient(t, prov(srv, providers.FlavorOpenRouter, providers.DataPrivate), "k")
	var got strings.Builder
	_, err := c.Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: "user", Content: "q"}}}, func(d llm.Delta) error { got.WriteString(d.Text); return nil })
	var ae *llm.APIError
	if !errors.As(err, &ae) || ae.Kind != llm.KindRateLimit || ae.Status != 429 {
		t.Fatalf("err = %v", err)
	}
	if got.String() != "partial text" {
		t.Fatalf("deltas before the error: %q", got.String())
	}
}

func TestErrorsAreClassifiedWithRetryHeaders(t *testing.T) {
	cases := []struct {
		name   string
		status int
		hdr    map[string]string
		body   any
		kind   llm.ErrorKind
		retry  bool
	}{
		{"429 with retry-after-ms", 429, map[string]string{"retry-after-ms": "250"}, map[string]any{"error": map[string]any{"message": "slow down", "type": "rate_limit_error"}}, llm.KindRateLimit, true},
		{"429 insufficient_quota is not retryable", 429, nil, map[string]any{"error": map[string]any{"message": "You exceeded your current quota", "code": "insufficient_quota", "type": "insufficient_quota"}}, llm.KindQuota, false},
		{"401", 401, nil, map[string]any{"error": map[string]any{"message": "bad key"}}, llm.KindAuth, false},
		{"400 invalid", 400, nil, map[string]any{"error": map[string]any{"message": "messages: field required"}}, llm.KindInvalid, false},
		{"500", 500, nil, `{"error":"boom"}`, llm.KindServer, false},
		{"openrouter 402 credits", 402, nil, map[string]any{"error": map[string]any{"code": 402, "message": "Insufficient credits"}}, llm.KindQuota, false},
		{"openrouter 402 in-flight budget carries Retry-After", 402, map[string]string{"Retry-After": "3"}, map[string]any{"error": map[string]any{"code": 402, "message": "budget", "metadata": map[string]any{"limit_source": "openrouter_in_flight_budget"}}}, llm.KindRateLimit, true},
		{"openrouter no endpoint matches the data policy", 404, nil, map[string]any{"error": map[string]any{"code": 404, "message": "No endpoints found matching your data policy"}}, llm.KindNoEndpoint, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
				llmtest.WriteError(w, tc.status, tc.hdr, tc.body)
			})
			c := newClient(t, prov(srv, providers.FlavorOpenRouter, providers.DataPrivate), "k")
			_, err := c.Complete(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: "user", Content: "q"}}})
			var ae *llm.APIError
			if !errors.As(err, &ae) {
				t.Fatalf("err = %v", err)
			}
			if ae.Kind != tc.kind || ae.Status != tc.status || ae.HasRetryAfter != tc.retry {
				t.Fatalf("got kind=%s status=%d hasRetryAfter=%v, want %s %d %v (%v)", ae.Kind, ae.Status, ae.HasRetryAfter, tc.kind, tc.status, tc.retry, ae)
			}
			if n := len(srv.Calls()); n != 1 {
				t.Fatalf("the SDK retried on its own (%d calls); retry belongs to the Wrapper", n)
			}
		})
	}
}

func TestNetworkFailureIsKindNetwork(t *testing.T) {
	srv := llmtest.New(t, nil)
	p := prov(srv, providers.FlavorOpenAI, providers.DataPrivate)
	srv.Close()
	c := newClient(t, p, "k")
	_, err := c.Complete(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: "user", Content: "q"}}})
	var ae *llm.APIError
	if !errors.As(err, &ae) || ae.Kind != llm.KindNetwork {
		t.Fatalf("err = %v", err)
	}
}

func TestContextCancelStopsAStream(t *testing.T) {
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.WriteStream(w, llmtest.Completion{Model: "m", Content: "abcdef"})
	})
	c := newClient(t, prov(srv, providers.FlavorOpenAI, providers.DataPrivate), "k")
	ctx, cancel := context.WithCancel(context.Background())
	_, err := c.Stream(ctx, llm.Request{Model: "m", Messages: []llm.Message{{Role: "user", Content: "q"}}}, func(d llm.Delta) error { cancel(); return nil })
	if err == nil {
		t.Fatal("stream ignored cancellation")
	}
}

// TestLiveOpenRouter talks to the real OpenRouter. It needs OPENROUTER_API_KEY and network access to openrouter.ai;
// neither exists in the build sandbox, so it is skipped there and never faked.
func TestLiveOpenRouter(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set: live OpenRouter test skipped (no network route to openrouter.ai in the sandbox)")
	}
	model := os.Getenv("OPENROUTER_TEST_MODEL")
	if model == "" {
		t.Skip("OPENROUTER_TEST_MODEL not set: name a paid model that can serve data_collection=deny and zdr=true")
	}
	p := providers.Provider{ID: "live", BaseURL: "https://openrouter.ai/api/v1", Flavor: providers.FlavorOpenRouter, DataClass: providers.DataPrivate, RequestOverrides: []byte(`{}`)}
	c := newClient(t, p, key)
	resp, err := c.Complete(context.Background(), llm.Request{Model: model, MaxTokens: 16, Messages: []llm.Message{{Role: "user", Content: "Reply with the word ok."}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("live usage=%+v cost=%v model=%s", resp.Usage, resp.ReportedCostUSD, resp.ModelUsed)
	if !resp.UsageReported || resp.ReportedCostUSD == nil {
		t.Fatal("OpenRouter returned no usage or cost: the usage fields did not survive the SDK path (docs/phase0 go-platform s6 concern)")
	}
}

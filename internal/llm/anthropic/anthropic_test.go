package anthropic_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/llm/anthropic"
	"github.com/kjmaguire/drydock/internal/llm/llmtest"
	"github.com/kjmaguire/drydock/internal/providers"
)

// PROTOCOL FIXTURE tests: a local server speaking the Messages API wire format (llmtest). Live tests at the bottom
// need ANTHROPIC_API_KEY and are skipped without it.

func prov(srv *llmtest.Server) providers.Provider {
	return providers.Provider{ID: "prov_a", Name: "anthropic", BaseURL: srv.URL, Flavor: providers.FlavorAnthropic, DataClass: providers.DataPrivate, Tier: providers.TierPremium}
}

func newClient(t *testing.T, srv *llmtest.Server, cfg anthropic.Config) llm.Client {
	t.Helper()
	c, err := anthropic.NewWithConfig(prov(srv), "sk-ant-test", nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func msgOK(c llmtest.Call, w http.ResponseWriter) {
	m := llmtest.AnthropicMessage{Model: "claude-fixture", Text: "answer text", Usage: llmtest.AnthropicUsage{Input: 40, Output: 12, CacheRead: 3000, CacheCreation: 500},
		Header: map[string]string{"anthropic-ratelimit-requests-remaining": "49", "anthropic-ratelimit-requests-limit": "50"}}
	if s, _ := c.Body["stream"].(bool); s {
		llmtest.WriteAnthropicStream(w, m)
		return
	}
	llmtest.WriteAnthropic(w, m)
}

func request() llm.Request {
	return llm.Request{Model: "claude-fixture", MaxTokens: 1000, Effort: llm.EffortMedium,
		System: []llm.Block{{Text: "tools-and-rules prefix", Cache: true}, {Text: "volatile system tail"}},
		Messages: []llm.Message{
			{Role: "user", Content: "first question", Cache: true},
			{Role: "assistant", Content: "calling", ToolCalls: []llm.ToolCall{{ID: "toolu_1", Name: "read_file", Arguments: `{"path":"a.go"}`}}},
			{Role: "tool", ToolCallID: "toolu_1", Content: "file body"},
			{Role: "user", Content: "now summarize"},
		},
		Tools:      []llm.ToolDef{{Name: "read_file", Description: "Read a file", Parameters: []byte(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)}},
		ToolChoice: "auto"}
}

func TestRequestCarriesCacheControlOnTheStablePrefix(t *testing.T) {
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) { msgOK(c, w) })
	c := newClient(t, srv, anthropic.Config{})
	if _, err := c.Complete(context.Background(), request()); err != nil {
		t.Fatal(err)
	}
	call := srv.Calls()[0]
	if call.Path != "/v1/messages" || call.Header.Get("x-api-key") != "sk-ant-test" || call.Header.Get("anthropic-version") == "" {
		t.Fatalf("request %s %v", call.Path, call.Header)
	}
	b := call.Body
	sys := b["system"].([]any)
	if len(sys) != 2 {
		t.Fatalf("system blocks %v", sys)
	}
	first := sys[0].(map[string]any)
	if cc, _ := first["cache_control"].(map[string]any); cc["type"] != "ephemeral" {
		t.Fatalf("stable system block has no cache_control: %v", first)
	}
	if _, has := sys[1].(map[string]any)["cache_control"]; has {
		t.Fatal("volatile tail must not carry a cache marker")
	}
	msgs := b["messages"].([]any)
	if len(msgs) != 3 { // user, assistant(tool_use), user(tool_result + text) merged
		t.Fatalf("messages %d: %v", len(msgs), msgs)
	}
	u0 := msgs[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if cc, _ := u0["cache_control"].(map[string]any); cc["type"] != "ephemeral" {
		t.Fatalf("message breakpoint missing: %v", u0)
	}
	asst := msgs[1].(map[string]any)["content"].([]any)
	if asst[1].(map[string]any)["type"] != "tool_use" || asst[1].(map[string]any)["input"].(map[string]any)["path"] != "a.go" {
		t.Fatalf("assistant blocks %v", asst)
	}
	last := msgs[2].(map[string]any)["content"].([]any)
	if last[0].(map[string]any)["type"] != "tool_result" || last[0].(map[string]any)["tool_use_id"] != "toolu_1" || last[1].(map[string]any)["type"] != "text" {
		t.Fatalf("tool result not merged into the user turn: %v", last)
	}
	tool := b["tools"].([]any)[0].(map[string]any)
	schema := tool["input_schema"].(map[string]any)
	if tool["name"] != "read_file" || schema["type"] != "object" || schema["additionalProperties"] != false || schema["required"].([]any)[0] != "path" {
		t.Fatalf("tool %v", tool)
	}
	if b["tool_choice"].(map[string]any)["type"] != "auto" || b["output_config"].(map[string]any)["effort"] != "medium" || b["max_tokens"] != float64(1000) {
		t.Fatalf("body %v", b)
	}
}

func TestOneHourCacheTTL(t *testing.T) {
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) { msgOK(c, w) })
	c := newClient(t, srv, anthropic.Config{CacheTTL1h: true})
	c.Complete(context.Background(), request())
	cc := srv.Calls()[0].Body["system"].([]any)[0].(map[string]any)["cache_control"].(map[string]any)
	if cc["ttl"] != "1h" {
		t.Fatalf("cache_control %v", cc)
	}
}

func TestBreakpointsAreCappedAtFour(t *testing.T) {
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) { msgOK(c, w) })
	c := newClient(t, srv, anthropic.Config{})
	req := llm.Request{Model: "m", System: []llm.Block{{Text: "a", Cache: true}, {Text: "b", Cache: true}, {Text: "c", Cache: true}}}
	for i := 0; i < 3; i++ {
		req.Messages = append(req.Messages, llm.Message{Role: "user", Content: "u", Cache: true}, llm.Message{Role: "assistant", Content: "a"})
	}
	req.Messages = append(req.Messages, llm.Message{Role: "user", Content: "end"})
	if _, err := c.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	body := srv.Calls()[0].Body
	n := 0
	for _, s := range body["system"].([]any) {
		if _, ok := s.(map[string]any)["cache_control"]; ok {
			n++
		}
	}
	for _, m := range body["messages"].([]any) {
		for _, b := range m.(map[string]any)["content"].([]any) {
			if _, ok := b.(map[string]any)["cache_control"]; ok {
				n++
			}
		}
	}
	if n != 4 {
		t.Fatalf("%d cache_control markers, the API allows 4", n)
	}
	// The earliest markers are the ones dropped: the last message marker must survive.
	msgs := body["messages"].([]any)
	lastMarked := msgs[4].(map[string]any)["content"].([]any)[0].(map[string]any)
	if _, ok := lastMarked["cache_control"]; !ok {
		t.Fatal("the latest breakpoint (longest prefix) was dropped")
	}
}

func TestUsageNormalizationIncludesCacheTokensInInput(t *testing.T) {
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) { msgOK(c, w) })
	c := newClient(t, srv, anthropic.Config{})
	resp, err := c.Complete(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	// Anthropic's input_tokens excludes cached tokens; the normalized Input is the total (D12).
	want := llm.Usage{Input: 40 + 3000 + 500, Output: 12, Cached: 3000, CacheWrite: 500}
	if resp.Usage != want || !resp.UsageReported || resp.Content != "answer text" || resp.FinishReason != "stop" || resp.ModelUsed != "claude-fixture" {
		t.Fatalf("resp %+v", resp)
	}
	if resp.Header.Get("anthropic-ratelimit-requests-remaining") != "49" || !resp.EffortApplied {
		t.Fatalf("headers %v", resp.Header)
	}
}

func TestStreamAccumulatesTextToolUseAndUsage(t *testing.T) {
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.WriteAnthropicStream(w, llmtest.AnthropicMessage{Model: "claude-fixture", Thinking: "mulling", Text: "I will read it.",
			ToolUse: &llmtest.ToolCall{ID: "toolu_7", Name: "read_file", Arguments: `{"path":"main.go"}`},
			Usage:   llmtest.AnthropicUsage{Input: 10, Output: 25, CacheRead: 100, CacheCreation: 20}})
	})
	c := newClient(t, srv, anthropic.Config{})
	var text, think strings.Builder
	var args strings.Builder
	var toolID string
	resp, err := c.Stream(context.Background(), request(), func(d llm.Delta) error {
		text.WriteString(d.Text)
		think.WriteString(d.Reasoning)
		if d.ToolCall != nil {
			if d.ToolCall.ID != "" {
				toolID = d.ToolCall.ID
			}
			args.WriteString(d.ToolCall.Arguments)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "I will read it." || think.String() != "mulling" || toolID != "toolu_7" || args.String() != `{"path":"main.go"}` {
		t.Fatalf("deltas text=%q think=%q tool=%q args=%q", text.String(), think.String(), toolID, args.String())
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read_file" || resp.ToolCalls[0].Arguments != `{"path":"main.go"}` || resp.FinishReason != "tool_calls" {
		t.Fatalf("resp %+v", resp)
	}
	if resp.Usage != (llm.Usage{Input: 130, Output: 25, Cached: 100, CacheWrite: 20}) {
		t.Fatalf("usage %+v", resp.Usage)
	}
	if srv.Calls()[0].Body["stream"] != true {
		t.Fatal("stream flag missing")
	}
}

func TestEnvironmentCredentialsAreIgnored(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "oauth-from-env")
	t.Setenv("ANTHROPIC_API_KEY", "key-from-env")
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) { msgOK(c, w) })
	c := newClient(t, srv, anthropic.Config{})
	if _, err := c.Complete(context.Background(), request()); err != nil {
		t.Fatal(err)
	}
	h := srv.Calls()[0].Header
	if h.Get("x-api-key") != "sk-ant-test" || h.Get("Authorization") != "" {
		t.Fatalf("credentials: %v", h)
	}
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		name   string
		status int
		hdr    map[string]string
		typ    string
		msg    string
		kind   llm.ErrorKind
		retry  bool
		daily  bool
	}{
		{"429 with retry-after", 429, map[string]string{"retry-after": "7"}, "rate_limit_error", "rate", llm.KindRateLimit, true, false},
		{"529 overloaded", 529, nil, "overloaded_error", "Overloaded", llm.KindOverloaded, false, false},
		{"401", 401, nil, "authentication_error", "invalid x-api-key", llm.KindAuth, false, false},
		{"400 retention required for Covered Models", 400, nil, "invalid_request_error", "In order to access this model, your organization or workspace must have data retention enabled.", llm.KindRetention, false, false},
		{"400 invalid", 400, nil, "invalid_request_error", "max_tokens: Field required", llm.KindInvalid, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
				llmtest.AnthropicError(w, tc.status, tc.hdr, tc.typ, tc.msg)
			})
			c := newClient(t, srv, anthropic.Config{})
			_, err := c.Complete(context.Background(), request())
			var ae *llm.APIError
			if !errors.As(err, &ae) || ae.Kind != tc.kind || ae.HasRetryAfter != tc.retry {
				t.Fatalf("err = %v (kind %v)", err, ae)
			}
			if len(srv.Calls()) != 1 {
				t.Fatalf("SDK retried by itself: %d calls", len(srv.Calls()))
			}
		})
	}
}

func TestSpendLimitIsAQuotaErrorNotARetry(t *testing.T) {
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.WriteError(w, 429, nil, map[string]any{"type": "error", "error": map[string]any{"type": "rate_limit_error", "message": "spend limit reached",
			"details": map[string]any{"error_code": "enforced_spend_limit_reached"}}})
	})
	c := newClient(t, srv, anthropic.Config{})
	_, err := c.Complete(context.Background(), request())
	var ae *llm.APIError
	if !errors.As(err, &ae) || ae.Kind != llm.KindQuota || ae.Kind.Retryable() {
		t.Fatalf("err = %v", err)
	}
}

func TestToolArgumentsMustBeJSON(t *testing.T) {
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) { msgOK(c, w) })
	c := newClient(t, srv, anthropic.Config{})
	req := request()
	req.Messages[1].ToolCalls[0].Arguments = `{not json`
	_, err := c.Complete(context.Background(), req)
	var ae *llm.APIError
	if !errors.As(err, &ae) || ae.Kind != llm.KindInvalid || len(srv.Calls()) != 0 {
		t.Fatalf("err = %v calls=%d", err, len(srv.Calls()))
	}
}

// TestLiveAnthropicPromptCache needs ANTHROPIC_API_KEY and network access to api.anthropic.com; both are absent in
// the sandbox, so it is skipped there and never faked. It proves the second identical-prefix call reports a cache read.
func TestLiveAnthropicPromptCache(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	model := os.Getenv("ANTHROPIC_TEST_MODEL")
	if key == "" || model == "" {
		t.Skip("ANTHROPIC_API_KEY and ANTHROPIC_TEST_MODEL not set: live prompt-cache test skipped (no network route to api.anthropic.com in the sandbox)")
	}
	p := providers.Provider{ID: "live", BaseURL: "https://api.anthropic.com", Flavor: providers.FlavorAnthropic, DataClass: providers.DataPrivate}
	c, _ := anthropic.New(p, key, nil)
	prefix := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 800) // well above every model's minimum cacheable length
	req := llm.Request{Model: model, MaxTokens: 16, System: []llm.Block{{Text: prefix, Cache: true}}, Messages: []llm.Message{{Role: "user", Content: "Say ok."}}}
	first, err := c.Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("first=%+v second=%+v", first.Usage, second.Usage)
	if first.Usage.CacheWrite == 0 && first.Usage.Cached == 0 {
		t.Fatal("no cache activity on the first call: the prefix is below the model minimum or the marker was dropped")
	}
	if second.Usage.Cached == 0 {
		t.Fatal("second identical-prefix call reported no cache read")
	}
}

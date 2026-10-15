// Package openai is the llm.Client for OpenAI-compatible chat completions endpoints (OpenAI, OpenRouter, Gemini's
// compat endpoint, vLLM, SGLang, Ollama) on github.com/openai/openai-go/v3. The SDK supplies transport, auth and SSE
// decoding; request bodies are built here so provider extensions (OpenRouter's provider object, replayed reasoning)
// are exact, and usage is read from the raw final chunk because openai.ChatCompletionAccumulator drops
// CacheWriteTokens (docs/phase0/go-platform.md s6). The SDK's own retries are off: the llm.Wrapper owns retry,
// Retry-After and quota.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"

	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/providers"
)

// Client talks to one OpenAI-compatible provider.
type Client struct {
	c sdk.Client
	p providers.Provider
}

// New builds a Client for p. apiKey may be empty for a keyless host. hc carries the transport rule; nil uses the
// default client.
func New(p providers.Provider, apiKey string, hc *http.Client) (llm.Client, error) {
	opts := []option.RequestOption{
		option.WithBaseURL(strings.TrimRight(p.BaseURL, "/") + "/"),
		option.WithMaxRetries(0),
	}
	if hc != nil {
		opts = append(opts, option.WithHTTPClient(hc))
	}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	// Built by hand, not with sdk.NewClient: NewClient reads OPENAI_API_KEY, OPENAI_ORG_ID and OPENAI_PROJECT_ID from
	// the environment and would send them to whatever provider this is.
	return &Client{c: sdk.Client{Options: opts}, p: p}, nil
}

// Complete performs a non-streaming chat completion.
func (c *Client) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	body, err := c.body(req, false)
	if err != nil {
		return llm.Response{}, &llm.APIError{Provider: c.p.ID, Kind: llm.KindInvalid, Message: err.Error(), Cause: err}
	}
	var raw []byte
	var hr *http.Response
	start := time.Now()
	if err := c.c.Post(ctx, "chat/completions", body, &raw, option.WithResponseInto(&hr)); err != nil {
		return llm.Response{}, c.fail(err, hr)
	}
	var out completion
	if err := json.Unmarshal(raw, &out); err != nil {
		return llm.Response{}, &llm.APIError{Provider: c.p.ID, Status: 200, Kind: llm.KindServer, Message: "unreadable completion: " + err.Error(), Cause: err}
	}
	resp := llm.Response{ModelUsed: out.Model, EffortApplied: req.Effort != "", Latency: time.Since(start)}
	if hr != nil {
		resp.Header = hr.Header
	}
	if len(out.Choices) > 0 {
		ch := out.Choices[0]
		if ch.Message.Content != nil {
			resp.Content = *ch.Message.Content
		}
		resp.Reasoning = firstNonEmpty(ch.Message.Reasoning, ch.Message.ReasoningContent)
		resp.FinishReason = ch.FinishReason
		for _, tc := range ch.Message.ToolCalls {
			resp.ToolCalls = append(resp.ToolCalls, llm.ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
		}
	}
	c.applyUsage(&resp, out.Usage)
	return resp, nil
}

// Stream performs a streaming chat completion. Usage comes from the final chunk (stream_options.include_usage).
func (c *Client) Stream(ctx context.Context, req llm.Request, fn func(llm.Delta) error) (llm.Response, error) {
	body, err := c.body(req, true)
	if err != nil {
		return llm.Response{}, &llm.APIError{Provider: c.p.ID, Kind: llm.KindInvalid, Message: err.Error(), Cause: err}
	}
	var raw *http.Response
	start := time.Now()
	if err := c.c.Execute(ctx, http.MethodPost, "chat/completions", body, &raw); err != nil {
		return llm.Response{}, c.fail(err, raw)
	}
	stream := ssestream.NewStream[chunk](ssestream.NewDecoder(raw), nil)
	defer func() { _ = stream.Close() }()
	resp := llm.Response{EffortApplied: req.Effort != "", Header: raw.Header}
	var usage *wireUsage
	calls := map[int]*llm.ToolCall{}
	var order []int
	var text, reasoning strings.Builder
	for stream.Next() {
		if err := ctx.Err(); err != nil {
			return llm.Response{}, err
		}
		ck := stream.Current()
		if ck.Model != "" {
			resp.ModelUsed = ck.Model
		}
		if ck.Usage != nil {
			usage = ck.Usage // the final chunk, with an empty choices array on most servers
		}
		for _, ch := range ck.Choices {
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				resp.FinishReason = *ch.FinishReason
			}
			d := ch.Delta
			if d.Content != nil && *d.Content != "" {
				text.WriteString(*d.Content)
				if err := fn(llm.Delta{Text: *d.Content}); err != nil {
					return llm.Response{}, err
				}
			}
			if r := firstNonEmpty(deref(d.Reasoning), deref(d.ReasoningContent)); r != "" {
				reasoning.WriteString(r)
				if err := fn(llm.Delta{Reasoning: r}); err != nil {
					return llm.Response{}, err
				}
			}
			for _, tc := range d.ToolCalls {
				cur := calls[tc.Index]
				if cur == nil {
					cur = &llm.ToolCall{}
					calls[tc.Index] = cur
					order = append(order, tc.Index)
				}
				if tc.ID != "" {
					cur.ID = tc.ID
				}
				if tc.Function.Name != "" {
					cur.Name = tc.Function.Name
				}
				cur.Arguments += tc.Function.Arguments
				if err := fn(llm.Delta{ToolCall: &llm.ToolCallDelta{Index: tc.Index, ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments}}); err != nil {
					return llm.Response{}, err
				}
			}
		}
	}
	if err := stream.Err(); err != nil {
		if ctx.Err() != nil {
			return llm.Response{}, ctx.Err()
		}
		return llm.Response{}, c.fail(err, raw)
	}
	resp.Content, resp.Reasoning = text.String(), reasoning.String()
	for _, i := range order {
		resp.ToolCalls = append(resp.ToolCalls, *calls[i])
	}
	resp.Latency = time.Since(start)
	c.applyUsage(&resp, usage)
	return resp, nil
}

// applyUsage maps wire usage to llm usage. prompt_tokens already includes cached tokens (the OTel view, D12).
func (c *Client) applyUsage(r *llm.Response, u *wireUsage) {
	if u == nil {
		return
	}
	r.UsageReported = true
	r.Usage = llm.Usage{Input: u.PromptTokens, Output: u.CompletionTokens}
	if d := u.PromptTokensDetails; d != nil {
		r.Usage.Cached, r.Usage.CacheWrite = d.CachedTokens, d.CacheWriteTokens
	}
	if d := u.CompletionTokensDetails; d != nil {
		r.Usage.Reasoning = d.ReasoningTokens
	}
	// OpenRouter returns the charged cost on every response (usage accounting is always on, MP A.2). Other providers
	// put no dollar figure here, so it is trusted only for OpenRouter.
	if c.p.Flavor == providers.FlavorOpenRouter && u.Cost != nil {
		r.ReportedCostUSD = u.Cost
	}
}

// fail turns an SDK or stream error into an llm.APIError.
func (c *Client) fail(err error, hr *http.Response) error {
	var api *sdk.Error
	if errors.As(err, &api) {
		var h http.Header
		if api.Response != nil {
			h = api.Response.Header
		} else if hr != nil {
			h = hr.Header
		}
		return llm.ClassifyHTTP(c.p.ID, api.StatusCode, h, []byte(api.RawJSON()), time.Now())
	}
	if hr != nil && hr.StatusCode >= 400 { // an error body the SDK could not parse (for example {"error":"text"}): the status still classifies it
		return llm.ClassifyHTTP(c.p.ID, hr.StatusCode, hr.Header, nil, time.Now())
	}
	var se *ssestream.StreamError
	if errors.As(err, &se) { // an error event in the stream: OpenRouter sends {"error":{"code":429,...}} mid-stream
		status := 502
		if m := streamErrCode(se.Event.Data); m != 0 {
			status = m
		}
		var h http.Header
		if hr != nil {
			h = hr.Header
		}
		return llm.ClassifyHTTP(c.p.ID, status, h, se.Event.Data, time.Now())
	}
	return &llm.APIError{Provider: c.p.ID, Kind: llm.KindNetwork, Message: err.Error(), Cause: err}
}

func streamErrCode(data []byte) int {
	var v struct {
		Error struct {
			Code any `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &v) != nil {
		return 0
	}
	switch t := v.Error.Code.(type) {
	case float64:
		return int(t)
	}
	return 0
}

// protectedKeys cannot be set through request_overrides.
var protectedKeys = map[string]bool{"model": true, "messages": true, "tools": true, "stream": true, "stream_options": true, "tool_choice": true, "max_tokens": true}

// body renders the request. Order of keys is irrelevant to servers.
func (c *Client) body(r llm.Request, stream bool) ([]byte, error) {
	m := map[string]any{"model": r.Model, "messages": c.messages(r)}
	if r.MaxTokens > 0 {
		m["max_tokens"] = r.MaxTokens
	}
	if r.Temperature != nil {
		m["temperature"] = *r.Temperature
	}
	if r.TopP != nil {
		m["top_p"] = *r.TopP
	}
	if len(r.Tools) > 0 {
		var tools []map[string]any
		for _, t := range r.Tools {
			fn := map[string]any{"name": t.Name}
			if t.Description != "" {
				fn["description"] = t.Description
			}
			if len(t.Parameters) > 0 {
				fn["parameters"] = json.RawMessage(t.Parameters)
			}
			tools = append(tools, map[string]any{"type": "function", "function": fn})
		}
		m["tools"] = tools
		switch r.ToolChoice {
		case "":
		case "auto", "none", "required":
			m["tool_choice"] = r.ToolChoice
		default:
			m["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": r.ToolChoice}}
		}
	}
	if stream {
		m["stream"] = true
		m["stream_options"] = map[string]any{"include_usage": true}
	}
	if r.Effort != "" {
		eff := string(r.Effort)
		if r.Effort == llm.EffortMax {
			eff = "high" // the chat completions vocabulary stops at high
		}
		if c.p.Flavor == providers.FlavorOpenRouter {
			m["reasoning"] = map[string]any{"effort": eff}
		} else {
			m["reasoning_effort"] = eff
		}
	}
	// Provider-specific extensions.
	var over map[string]json.RawMessage
	if len(c.p.RequestOverrides) > 0 {
		_ = json.Unmarshal(c.p.RequestOverrides, &over)
	}
	for k, v := range over {
		if k != "provider" && !protectedKeys[k] {
			m[k] = v
		}
	}
	if c.p.Flavor == providers.FlavorOpenRouter {
		prov := map[string]any{}
		if raw, ok := over["provider"]; ok {
			_ = json.Unmarshal(raw, &prov)
		}
		// D4: every private OpenRouter route fails closed on a provider that may retain or train. This overrides any
		// weaker value in the stored overrides. Free models are trainable by definition and are not sent the filter.
		if c.p.DataClass == providers.DataPrivate && !strings.HasSuffix(r.Model, ":free") && r.Model != "openrouter/free" {
			prov["data_collection"] = "deny"
			prov["zdr"] = true
		}
		// P53 c: endpoints with poor recent uptime are skipped, added to any the stored overrides already ignore. ignore,
		// unlike order, leaves OpenRouter's sticky routing (and so the provider's prompt cache) in place.
		if len(r.AvoidEndpoints) > 0 {
			prov["ignore"] = mergeIgnore(prov["ignore"], r.AvoidEndpoints)
		}
		if len(prov) > 0 {
			m["provider"] = prov
		}
		if r.SessionID != "" {
			m["session_id"] = r.SessionID
		}
	}
	return json.Marshal(m)
}

// mergeIgnore is the union of an existing provider.ignore value (a JSON array of strings, or absent) and more tags.
func mergeIgnore(existing any, more []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if arr, ok := existing.([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok {
				add(s)
			}
		}
	}
	for _, v := range more {
		add(v)
	}
	return out
}

func (c *Client) messages(r llm.Request) []map[string]any {
	var out []map[string]any
	if len(r.System) > 0 {
		var parts []string
		for _, b := range r.System {
			parts = append(parts, b.Text)
		}
		out = append(out, map[string]any{"role": "system", "content": strings.Join(parts, "\n\n")})
	}
	reasoningKey := "reasoning" // vLLM and OpenRouter; SGLang calls it reasoning_content (docs/phase0 E)
	if c.p.Flavor == providers.FlavorSGLang {
		reasoningKey = "reasoning_content"
	}
	for _, m := range r.Messages {
		msg := map[string]any{"role": m.Role}
		switch m.Role {
		case "tool":
			msg["tool_call_id"] = m.ToolCallID
			msg["content"] = m.Content
		case "assistant":
			if m.Content != "" || len(m.ToolCalls) == 0 {
				msg["content"] = m.Content
			} else {
				msg["content"] = nil
			}
			if len(m.ToolCalls) > 0 {
				var tcs []map[string]any
				for _, tc := range m.ToolCalls {
					tcs = append(tcs, map[string]any{"id": tc.ID, "type": "function", "function": map[string]any{"name": tc.Name, "arguments": tc.Arguments}})
				}
				msg["tool_calls"] = tcs
			}
			if m.Reasoning != "" {
				msg[reasoningKey] = m.Reasoning
			}
		default:
			msg["content"] = m.Content
		}
		out = append(out, msg)
	}
	return out
}

// Wire shapes (only the fields read).
type wireUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	Cost *float64 `json:"cost"`
}

type wireToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type completion struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content          *string        `json:"content"`
			Reasoning        string         `json:"reasoning"`
			ReasoningContent string         `json:"reasoning_content"`
			ToolCalls        []wireToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
}

type chunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content          *string        `json:"content"`
			Reasoning        *string        `json:"reasoning"`
			ReasoningContent *string        `json:"reasoning_content"`
			ToolCalls        []wireToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

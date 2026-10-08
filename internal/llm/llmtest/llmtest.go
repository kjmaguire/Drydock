// Package llmtest holds PROTOCOL FIXTURES for tests: httptest servers that speak the real wire protocols of the
// OpenAI chat completions API (streaming with a final usage chunk, rate-limit headers, 429 with Retry-After,
// OpenRouter's usage.cost and mid-stream error events) and of the Anthropic Messages API (SSE events, cache token
// usage). They exist so request construction, parsing and failure boundaries can be tested without network access; they
// are not mocks of Drydock code and prove nothing about a real provider. Tests against real providers are gated on
// environment variables and skipped otherwise.
package llmtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Call is one request the fixture received.
type Call struct {
	N      int // 1-based index among chat requests
	Method string
	Path   string
	Header http.Header
	Body   map[string]any
}

// Handler answers one chat request.
type Handler func(c Call, w http.ResponseWriter, r *http.Request)

// Server is a fixture server. Requests to paths ending in /chat/completions or /messages are "chat requests" and go
// to the Handler; /models and /health style paths are answered by Routes if present.
type Server struct {
	*httptest.Server
	mu      sync.Mutex
	calls   []Call
	handler Handler
	Routes  map[string]http.HandlerFunc
	cur     atomic.Int32
	peak    atomic.Int32
}

// New starts a fixture server; handler may be nil for a server that only serves Routes.
func New(t testing.TB, handler Handler) *Server {
	t.Helper()
	s := &Server{handler: handler, Routes: map[string]http.HandlerFunc{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, ok := s.Routes[r.URL.Path]; ok {
			h(w, r)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") && !strings.HasSuffix(r.URL.Path, "/messages") {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		c := Call{N: len(s.calls) + 1, Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body}
		s.calls = append(s.calls, c)
		s.mu.Unlock()
		n := s.cur.Add(1)
		for {
			old := s.peak.Load()
			if n <= old || s.peak.CompareAndSwap(old, n) {
				break
			}
		}
		defer s.cur.Add(-1)
		if s.handler == nil {
			http.Error(w, "no handler", 500)
			return
		}
		s.handler(c, w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// Calls returns the chat requests received so far.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Peak is the highest number of chat requests that were in flight at once.
func (s *Server) Peak() int { return int(s.peak.Load()) }

// ToolCall in a completion.
type ToolCall struct {
	ID, Name, Arguments string
}

// Usage is the OpenAI-shaped usage object, with the OpenRouter extensions.
type Usage struct {
	PromptTokens, CompletionTokens int
	CachedTokens, CacheWriteTokens int
	ReasoningTokens                int
	Cost                           *float64
}

// Completion is what the OpenAI-shaped fixture returns.
type Completion struct {
	Model          string
	Content        string
	Reasoning      string
	ReasoningField string // "reasoning" (default) or "reasoning_content"
	ToolCalls      []ToolCall
	Finish         string // default "stop" or "tool_calls"
	Usage          *Usage
	Header         map[string]string
	// StreamPieces splits Content into this many deltas when streaming (default 3).
	StreamPieces int
	// ErrorAfter makes the stream send an OpenRouter-style error event after the content (HTTP stays 200).
	ErrorAfter map[string]any
}

func usageJSON(u *Usage) map[string]any {
	m := map[string]any{"prompt_tokens": u.PromptTokens, "completion_tokens": u.CompletionTokens, "total_tokens": u.PromptTokens + u.CompletionTokens}
	if u.CachedTokens > 0 || u.CacheWriteTokens > 0 {
		m["prompt_tokens_details"] = map[string]any{"cached_tokens": u.CachedTokens, "cache_write_tokens": u.CacheWriteTokens}
	}
	if u.ReasoningTokens > 0 {
		m["completion_tokens_details"] = map[string]any{"reasoning_tokens": u.ReasoningTokens}
	}
	if u.Cost != nil {
		m["cost"] = *u.Cost
	}
	return m
}

func setHeaders(w http.ResponseWriter, h map[string]string) {
	for k, v := range h {
		w.Header().Set(k, v)
	}
}

func finish(c Completion) string {
	if c.Finish != "" {
		return c.Finish
	}
	if len(c.ToolCalls) > 0 {
		return "tool_calls"
	}
	return "stop"
}

// WriteCompletion answers a non-streaming chat completion.
func WriteCompletion(w http.ResponseWriter, c Completion) {
	setHeaders(w, c.Header)
	w.Header().Set("Content-Type", "application/json")
	msg := map[string]any{"role": "assistant", "content": c.Content}
	if c.Reasoning != "" {
		f := c.ReasoningField
		if f == "" {
			f = "reasoning"
		}
		msg[f] = c.Reasoning
	}
	if len(c.ToolCalls) > 0 {
		var tcs []map[string]any
		for _, t := range c.ToolCalls {
			tcs = append(tcs, map[string]any{"id": t.ID, "type": "function", "function": map[string]any{"name": t.Name, "arguments": t.Arguments}})
		}
		msg["tool_calls"] = tcs
		if c.Content == "" {
			msg["content"] = nil
		}
	}
	out := map[string]any{"id": "chatcmpl-fixture", "object": "chat.completion", "model": c.Model,
		"choices": []map[string]any{{"index": 0, "message": msg, "finish_reason": finish(c)}}}
	if c.Usage != nil {
		out["usage"] = usageJSON(c.Usage)
	}
	_ = json.NewEncoder(w).Encode(out)
}

func sse(w http.ResponseWriter, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "data: %s\n\n", b)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// WriteStream answers a streaming chat completion: role chunk, content deltas, reasoning, tool call fragments, a
// finish chunk, then (when Usage is set) the final usage chunk with an empty choices array, then [DONE].
func WriteStream(w http.ResponseWriter, c Completion) {
	setHeaders(w, c.Header)
	w.Header().Set("Content-Type", "text/event-stream")
	chunk := func(delta map[string]any, fin any) map[string]any {
		return map[string]any{"id": "chatcmpl-fixture", "object": "chat.completion.chunk", "model": c.Model,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": fin}}}
	}
	sse(w, chunk(map[string]any{"role": "assistant", "content": ""}, nil))
	if c.Reasoning != "" {
		f := c.ReasoningField
		if f == "" {
			f = "reasoning"
		}
		sse(w, chunk(map[string]any{f: c.Reasoning}, nil))
	}
	pieces := c.StreamPieces
	if pieces <= 0 {
		pieces = 3
	}
	runes := []rune(c.Content)
	for i := 0; i < pieces && len(runes) > 0; i++ {
		lo, hi := i*len(runes)/pieces, (i+1)*len(runes)/pieces
		if hi > lo {
			sse(w, chunk(map[string]any{"content": string(runes[lo:hi])}, nil))
		}
	}
	for i, t := range c.ToolCalls {
		half := len(t.Arguments) / 2
		sse(w, chunk(map[string]any{"tool_calls": []map[string]any{{"index": i, "id": t.ID, "type": "function", "function": map[string]any{"name": t.Name, "arguments": t.Arguments[:half]}}}}, nil))
		sse(w, chunk(map[string]any{"tool_calls": []map[string]any{{"index": i, "function": map[string]any{"arguments": t.Arguments[half:]}}}}, nil))
	}
	if c.ErrorAfter != nil {
		sse(w, map[string]any{"error": c.ErrorAfter, "choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": ""}, "finish_reason": "error"}}})
		return
	}
	sse(w, chunk(map[string]any{}, finish(c)))
	if c.Usage != nil {
		sse(w, map[string]any{"id": "chatcmpl-fixture", "object": "chat.completion.chunk", "model": c.Model, "choices": []any{}, "usage": usageJSON(c.Usage)})
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// Respond answers per the request's "stream" flag.
func Respond(w http.ResponseWriter, c Call, comp Completion) {
	if s, _ := c.Body["stream"].(bool); s {
		WriteStream(w, comp)
		return
	}
	WriteCompletion(w, comp)
}

// WriteError answers with an error status, headers and a JSON body.
func WriteError(w http.ResponseWriter, status int, header map[string]string, body any) {
	setHeaders(w, header)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if s, ok := body.(string); ok {
		fmt.Fprint(w, s)
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

// AnthropicUsage is the Messages API usage object.
type AnthropicUsage struct {
	Input, Output, CacheRead, CacheCreation int
}

// AnthropicMessage is what the Anthropic-shaped fixture returns.
type AnthropicMessage struct {
	Model    string
	Text     string
	Thinking string
	ToolUse  *ToolCall
	Stop     string // end_turn | tool_use | max_tokens
	Usage    AnthropicUsage
	Header   map[string]string
}

func (m AnthropicMessage) usage() map[string]any {
	return map[string]any{"input_tokens": m.Usage.Input, "output_tokens": m.Usage.Output,
		"cache_read_input_tokens": m.Usage.CacheRead, "cache_creation_input_tokens": m.Usage.CacheCreation}
}

func (m AnthropicMessage) stop() string {
	if m.Stop != "" {
		return m.Stop
	}
	if m.ToolUse != nil {
		return "tool_use"
	}
	return "end_turn"
}

// WriteAnthropic answers a non-streaming Messages request.
func WriteAnthropic(w http.ResponseWriter, m AnthropicMessage) {
	setHeaders(w, m.Header)
	w.Header().Set("Content-Type", "application/json")
	var content []map[string]any
	if m.Thinking != "" {
		content = append(content, map[string]any{"type": "thinking", "thinking": m.Thinking, "signature": "sig"})
	}
	if m.Text != "" {
		content = append(content, map[string]any{"type": "text", "text": m.Text})
	}
	if m.ToolUse != nil {
		var in any
		_ = json.Unmarshal([]byte(m.ToolUse.Arguments), &in)
		content = append(content, map[string]any{"type": "tool_use", "id": m.ToolUse.ID, "name": m.ToolUse.Name, "input": in})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "model": m.Model,
		"content": content, "stop_reason": m.stop(), "stop_sequence": nil, "usage": m.usage()})
}

func event(w http.ResponseWriter, name string, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// WriteAnthropicStream answers a streaming Messages request with the real SSE event sequence.
func WriteAnthropicStream(w http.ResponseWriter, m AnthropicMessage) {
	setHeaders(w, m.Header)
	w.Header().Set("Content-Type", "text/event-stream")
	start := m.usage()
	start["output_tokens"] = 1
	event(w, "message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_fixture", "type": "message",
		"role": "assistant", "model": m.Model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": start}})
	idx := 0
	if m.Thinking != "" {
		event(w, "content_block_start", map[string]any{"type": "content_block_start", "index": idx, "content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""}})
		event(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": idx, "delta": map[string]any{"type": "thinking_delta", "thinking": m.Thinking}})
		event(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": idx})
		idx++
	}
	if m.Text != "" {
		event(w, "content_block_start", map[string]any{"type": "content_block_start", "index": idx, "content_block": map[string]any{"type": "text", "text": ""}})
		half := len(m.Text) / 2
		for _, part := range []string{m.Text[:half], m.Text[half:]} {
			if part != "" {
				event(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": idx, "delta": map[string]any{"type": "text_delta", "text": part}})
			}
		}
		event(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": idx})
		idx++
	}
	if m.ToolUse != nil {
		event(w, "content_block_start", map[string]any{"type": "content_block_start", "index": idx, "content_block": map[string]any{"type": "tool_use", "id": m.ToolUse.ID, "name": m.ToolUse.Name, "input": map[string]any{}}})
		half := len(m.ToolUse.Arguments) / 2
		for _, part := range []string{m.ToolUse.Arguments[:half], m.ToolUse.Arguments[half:]} {
			event(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": idx, "delta": map[string]any{"type": "input_json_delta", "partial_json": part}})
		}
		event(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": idx})
	}
	event(w, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": m.stop(), "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": m.Usage.Output}})
	event(w, "message_stop", map[string]any{"type": "message_stop"})
}

// AnthropicError answers with an Anthropic-shaped error body.
func AnthropicError(w http.ResponseWriter, status int, header map[string]string, errType, message string) {
	WriteError(w, status, header, map[string]any{"type": "error", "error": map[string]any{"type": errType, "message": message}})
}

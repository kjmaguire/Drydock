package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/llm"
)

// WantsContent implements llm.ContentTracer: the Wrapper hands over a call's content only while telemetry records and a person
// has turned content export on (decision P121).
func (s *Sink) WantsContent() bool { return s.Enabled() && s.ContentEnabled() }

// TraceCall records one LLM Wrapper call (decision P113): a span named for the operation, and its token, cost and duration
// metrics. It implements llm.CallTracer. What it is given is content-free (llm.CallTrace carries no prompt, completion or error
// message) unless content export is on, and every attribute still goes through the allowlist, so a model or purpose name that
// is not identifier-shaped is dropped and counted like any other. With content export on, the span also carries the GenAI
// content attributes, redacted and bounded (contentAttrs).
func (s *Sink) TraceCall(ctx context.Context, c llm.CallTrace) {
	if !s.Enabled() {
		return
	}
	provider := c.ServedBy
	if provider == "" {
		provider = c.Provider
	}
	outcome, status := "ok", "ok"
	if c.ErrorType != "" {
		outcome, status = "error", "error"
	}
	base := map[string]any{AttrGenAIOperation: "chat", AttrGenAIProvider: provider, AttrGenAIRequestModel: c.RequestModel}
	if c.ResponseModel != "" {
		base[AttrGenAIResponseModel] = c.ResponseModel
	}
	if c.Purpose != "" {
		base[AttrPurpose] = c.Purpose
	}
	attrs := with(base, map[string]any{AttrOutcome: outcome, AttrLLMCacheHit: c.CacheHit, AttrLLMFallback: c.Fallback})
	for k, v := range map[string]string{AttrProjectID: c.ProjectID, AttrTaskID: c.TaskID, AttrRunID: c.RunID, AttrErrorType: c.ErrorType} {
		if v != "" {
			attrs[k] = v
		}
	}
	if c.ErrorType == "" {
		attrs[AttrGenAIInputTokens], attrs[AttrGenAIOutputTokens] = int64(c.Usage.Input), int64(c.Usage.Output)
		if c.Usage.Cached > 0 || c.Usage.CacheWrite > 0 {
			attrs[AttrCacheRead], attrs[AttrCacheWrite] = int64(c.Usage.Cached), int64(c.Usage.CacheWrite)
		}
		if c.Cost.Known {
			attrs[AttrCostUSD], attrs[AttrCostSource] = c.Cost.USD, string(c.Cost.Source)
		}
	}
	if c.Content != nil && s.ContentEnabled() {
		for k, v := range s.contentAttrs(c.Content) {
			attrs[k] = v
		}
	}
	trace := newTraceID()
	if c.RunID != "" {
		trace = TraceID(c.RunID) // a call made for a run joins that run's trace
	}
	end := c.End
	if end.Before(c.Start) {
		end = c.Start
	}
	_ = s.RecordSpan(ctx, Span{TraceID: trace, SpanID: NewSpanID(), Name: "drydock.llm.chat", StartMS: c.Start.UnixMilli(), EndMS: end.UnixMilli(), Status: status, Attrs: attrs})

	at := end.UnixMilli()
	_ = s.RecordMetric(ctx, Metric{Name: MetricLLMTime, Kind: "histogram", Unit: "ms", Value: float64(end.Sub(c.Start).Milliseconds()), TimeMS: at,
		Attrs: with(base, map[string]any{AttrOutcome: outcome})})
	if c.ErrorType != "" || c.CacheHit {
		return // a refused or failed call used no tokens, and a cache hit spent none (its saving is in model_usage)
	}
	// D12: input includes cached tokens (llm.Usage is that view already); cache read and write are kept apart.
	for kind, v := range map[string]int{"input": c.Usage.Input, "output": c.Usage.Output} {
		if v > 0 {
			_ = s.RecordMetric(ctx, Metric{Name: MetricTokenUsage, Kind: "counter", Unit: "{token}", Value: float64(v), TimeMS: at, Attrs: with(base, map[string]any{AttrGenAITokenType: kind})})
		}
	}
	if c.Cost.Known && c.Cost.USD > 0 {
		_ = s.RecordMetric(ctx, Metric{Name: MetricCostUSD, Kind: "counter", Unit: "USD", Value: c.Cost.USD, TimeMS: at, Attrs: with(base, map[string]any{AttrCostSource: string(c.Cost.Source)})})
	}
}

// contentAttrs is a call's content in the OpenTelemetry GenAI conventions' shape (gen_ai.system_instructions, gen_ai.input.messages
// and gen_ai.output.messages, each a JSON document of messages made of parts), every text redacted by the secret scanner first
// and cut so that each document stays within MaxContentBytes. The sink's Sanitize redacts the documents once more before
// storing them.
func (s *Sink) contentAttrs(c *llm.CallContent) map[string]any {
	red := func(t string) string {
		if s.opt.Redact != nil {
			return s.opt.Redact(t)
		}
		return t
	}
	out := map[string]any{}
	put := func(key string, build func(limit int) any) {
		for limit := MaxContentBytes; limit >= 64; limit /= 2 {
			b, err := json.Marshal(build(limit))
			if err == nil && len(b) <= MaxContentBytes*7/8 { // room for the masks the second redaction may write
				out[key] = string(b)
				return
			}
		}
	}
	if len(c.System) > 0 {
		put(AttrGenAISystemInstructions, func(limit int) any {
			parts := []map[string]any{}
			for _, t := range c.System {
				parts = append(parts, map[string]any{"type": "text", "content": cut(red(t), limit)})
			}
			return parts
		})
	}
	if len(c.Messages) > 0 {
		put(AttrGenAIInputMessages, func(limit int) any {
			msgs := []map[string]any{}
			for _, m := range c.Messages {
				var parts []map[string]any
				if m.Role == "tool" {
					parts = append(parts, map[string]any{"type": "tool_call_response", "id": m.ToolCallID, "response": cut(red(m.Content), limit)})
				} else if m.Content != "" {
					parts = append(parts, map[string]any{"type": "text", "content": cut(red(m.Content), limit)})
				}
				for _, tc := range m.ToolCalls {
					parts = append(parts, toolCallPart(tc, red, limit))
				}
				msgs = append(msgs, map[string]any{"role": m.Role, "parts": parts})
			}
			return msgs
		})
	}
	if c.Output != "" || len(c.ToolCalls) > 0 {
		put(AttrGenAIOutputMessages, func(limit int) any {
			var parts []map[string]any
			if c.Output != "" {
				parts = append(parts, map[string]any{"type": "text", "content": cut(red(c.Output), limit)})
			}
			for _, tc := range c.ToolCalls {
				parts = append(parts, toolCallPart(tc, red, limit))
			}
			msg := map[string]any{"role": "assistant", "parts": parts}
			if c.FinishReason != "" {
				msg["finish_reason"] = c.FinishReason
			}
			return []map[string]any{msg}
		})
	}
	return out
}

func toolCallPart(tc llm.ToolCall, red func(string) string, limit int) map[string]any {
	return map[string]any{"type": "tool_call", "id": tc.ID, "name": tc.Name, "arguments": cut(red(tc.Arguments), limit)}
}

// cut keeps at most limit bytes of t, on a character boundary, and says when it cut.
func cut(t string, limit int) string {
	if len(t) <= limit {
		return t
	}
	i := limit
	for i > 0 && !utf8.RuneStart(t[i]) {
		i--
	}
	return t[:i] + " [cut]"
}

// with returns a new map holding base and extra.
func with(base, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// newTraceID is a random 128-bit trace id, for a call that belongs to no run.
func newTraceID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

var _ llm.ContentTracer = (*Sink)(nil)

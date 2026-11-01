package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/kjmaguire/drydock/internal/llm"
)

// TraceCall records one LLM Wrapper call (decision P113): a span named for the operation, and its token, cost and duration
// metrics. It implements llm.CallTracer. What it is given is already content-free (llm.CallTrace has no field for a prompt, a
// completion or an error message), and every attribute still goes through the allowlist, so a model or purpose name that is
// not identifier-shaped is dropped and counted like any other.
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

var _ llm.CallTracer = (*Sink)(nil)

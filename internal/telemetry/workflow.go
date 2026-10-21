package telemetry

import (
	"context"
	"time"

	"github.com/kjmaguire/drydock/internal/workflow"
)

// Middleware records one span per node execution (outermost middleware, docs/workflow-engine.md s7). It reads only the
// node's identity and whether it succeeded, failed or parked on a human request: never the input, the output or any chunk.
func (s *Sink) Middleware() workflow.Middleware {
	return func(next workflow.NodeHandler) workflow.NodeHandler {
		return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
			if !s.Enabled() {
				return next(ctx, in)
			}
			start := time.Now()
			out, err := next(ctx, in)
			outcome, status := "ok", "ok"
			switch {
			case err != nil:
				outcome, status = "error", "error"
			case out.Request != nil:
				outcome = "paused"
			}
			attrs := map[string]any{AttrProjectID: in.ProjectID, AttrTaskID: in.TaskID, AttrRunID: string(in.RunID), AttrNodeID: string(in.NodeID),
				AttrNodeKind: string(in.Kind), AttrWorkflow: in.Workflow, AttrAttempt: in.Attempt, AttrOutcome: outcome}
			end := time.Now()
			_ = s.RecordSpan(ctx, Span{TraceID: TraceID(string(in.RunID)), SpanID: NewSpanID(), Name: "drydock.node." + string(in.Kind), StartMS: start.UnixMilli(), EndMS: end.UnixMilli(), Status: status, Attrs: attrs})
			_ = s.RecordMetric(ctx, Metric{Name: MetricNodeTime, Kind: "histogram", Unit: "ms", Value: float64(end.Sub(start).Milliseconds()), TimeMS: end.UnixMilli(),
				Attrs: map[string]any{AttrNodeKind: string(in.Kind), AttrWorkflow: in.Workflow, AttrOutcome: outcome}})
			return out, err
		}
	}
}

// Observer returns the stream observer that turns usage chunks into token and cost metrics. It decodes only the usage chunk;
// text, thinking, tool calls and tool results are never looked at.
func (s *Sink) Observer() workflow.ObserverFactory {
	return func(n workflow.NodeInfo) workflow.StreamObserver {
		if n.Kind != workflow.KindAgentTask {
			return nil
		}
		return &usageObserver{s: s}
	}
}

type usageObserver struct {
	s *Sink
}

func (u *usageObserver) OnStart(context.Context, workflow.NodeInfo) {}
func (u *usageObserver) OnEnd(context.Context, workflow.NodeInfo, workflow.NodeOutput) {
}
func (u *usageObserver) OnError(context.Context, workflow.NodeInfo, error) {}

func (u *usageObserver) OnChunk(ctx context.Context, n workflow.NodeInfo, c workflow.Chunk) {
	if c.Kind != workflow.ChunkUsage || !u.s.Enabled() {
		return
	}
	var uc workflow.UsageChunk
	if c.Decode(&uc) != nil {
		return
	}
	base := map[string]any{AttrWorkflow: n.Workflow, AttrNodeKind: string(n.Kind), AttrGenAIOperation: "invoke_agent"}
	if uc.Model != "" {
		base[AttrGenAIResponseModel] = uc.Model
	}
	tok := func(kind string, v int64) {
		if v <= 0 {
			return
		}
		a := map[string]any{AttrGenAITokenType: kind}
		for k, x := range base {
			a[k] = x
		}
		_ = u.s.RecordMetric(ctx, Metric{Name: MetricTokenUsage, Kind: "counter", Unit: "{token}", Value: float64(v), Attrs: a})
	}
	// D12: the OTel view of input includes cached tokens; cache read and write are kept apart as our own attributes.
	tok("input", uc.InputTokens+uc.CachedTokens)
	tok("output", uc.OutputTokens)
	if uc.CachedTokens > 0 || uc.CacheWriteTokens > 0 {
		a := map[string]any{AttrCacheRead: uc.CachedTokens, AttrCacheWrite: uc.CacheWriteTokens}
		for k, x := range base {
			a[k] = x
		}
		_ = u.s.RecordMetric(ctx, Metric{Name: "drydock.usage.cache", Kind: "counter", Unit: "{token}", Value: float64(uc.CachedTokens + uc.CacheWriteTokens), Attrs: a})
	}
	if uc.CostUSD > 0 { // an unreported cost is unknown, not zero: no data point
		src := uc.Source
		if src != "estimated" {
			src = "reported"
		}
		a := map[string]any{AttrCostSource: src}
		for k, x := range base {
			a[k] = x
		}
		_ = u.s.RecordMetric(ctx, Metric{Name: MetricCostUSD, Kind: "counter", Unit: "USD", Value: uc.CostUSD, Attrs: a})
	}
}

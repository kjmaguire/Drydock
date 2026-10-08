package acp

import (
	"github.com/coder/acp-go-sdk"

	"github.com/kjmaguire/drydock/internal/workflow"
)

// ACP usage reporting is uneven (docs/phase0/agent-clis.md section 7.2) and the SDK marks both `usage_update` and the
// prompt response `usage` UNSTABLE. A usage chunk is produced only from numbers the agent actually supplied, and is
// labelled Source "reported"; when nothing was supplied no chunk is produced and nothing is estimated here.
//
// Sources, in order of preference per turn:
//  1. PromptResponse.usage (input/output/cachedRead/cachedWrite/thought tokens),
//  2. PromptResponse._meta.quota (Gemini: token_count and per-model model_usage; no cache split),
//  3. usage_update{used,size,cost}: context occupancy always, and cost as the delta of the cumulative session cost.

// usageTracker accumulates one turn's usage signals.
type usageTracker struct {
	costBase float64 // cumulative session cost already attributed to earlier turns
	update   *acp.SessionUsageUpdate
	costUSD  float64 // cost attributed to the current turn so far
	haveCost bool
	rateMeta any // raw _meta["_claude/rateLimit"] from a usage_update, if any
}

// onUpdate records a usage_update notification.
func (u *usageTracker) onUpdate(upd *acp.SessionUsageUpdate) {
	u.update = upd
	if upd.Cost != nil && (upd.Cost.Currency == "USD" || upd.Cost.Currency == "") {
		d := upd.Cost.Amount - u.costBase
		if d < 0 { // the agent restarted its accounting
			d = upd.Cost.Amount
		}
		u.costUSD = d
		u.haveCost = true
	}
	if upd.Meta != nil {
		if v, ok := upd.Meta["_claude/rateLimit"]; ok {
			u.rateMeta = v
		}
	}
}

// finishTurn returns the usage chunks for a completed turn and resets the per-turn state.
func (u *usageTracker) finishTurn(resp acp.PromptResponse, model string) []workflow.UsageChunk {
	var chunks []workflow.UsageChunk
	switch {
	case resp.Usage != nil && (resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 || resp.Usage.TotalTokens > 0):
		c := workflow.UsageChunk{Model: model, Source: "reported",
			InputTokens: int64(resp.Usage.InputTokens), OutputTokens: int64(resp.Usage.OutputTokens)}
		if resp.Usage.CachedReadTokens != nil {
			c.CachedTokens = int64(*resp.Usage.CachedReadTokens)
		}
		if resp.Usage.CachedWriteTokens != nil {
			c.CacheWriteTokens = int64(*resp.Usage.CachedWriteTokens)
		}
		if resp.Usage.ThoughtTokens != nil {
			c.ReasoningTokens = int64(*resp.Usage.ThoughtTokens)
		}
		chunks = append(chunks, c)
	default:
		chunks = append(chunks, geminiQuota(resp.Meta, model)...)
	}
	if u.update != nil || u.haveCost {
		if len(chunks) == 0 {
			chunks = append(chunks, workflow.UsageChunk{Model: model, Source: "reported"})
		}
		if u.update != nil {
			chunks[0].ContextUsed, chunks[0].ContextSize = int64(u.update.Used), int64(u.update.Size)
		}
		if u.haveCost {
			chunks[0].CostUSD = u.costUSD
			if u.update != nil && u.update.Cost != nil {
				u.costBase = u.update.Cost.Amount
			}
		}
	}
	u.update, u.costUSD, u.haveCost = nil, 0, false
	return chunks
}

// geminiQuota reads Gemini's non-standard `PromptResponse._meta.quota`:
//
//	{"token_count":{"input_tokens":N,"output_tokens":N},"model_usage":[{"model":"..","token_count":{...}}]}
//
// Per-model entries win over the total (one chunk per model); an all-zero report (Gemini answers slash commands this way)
// is not usage and yields nothing.
func geminiQuota(meta map[string]any, model string) []workflow.UsageChunk {
	q, ok := meta["quota"].(map[string]any)
	if !ok {
		return nil
	}
	var out []workflow.UsageChunk
	if arr, ok := q["model_usage"].([]any); ok {
		for _, e := range arr {
			m, _ := e.(map[string]any)
			in, o := tokenCount(m["token_count"])
			if in == 0 && o == 0 {
				continue
			}
			name, _ := m["model"].(string)
			if name == "" {
				name = model
			}
			out = append(out, workflow.UsageChunk{Model: name, InputTokens: in, OutputTokens: o, Source: "reported"})
		}
	}
	if len(out) > 0 {
		return out
	}
	in, o := tokenCount(q["token_count"])
	if in == 0 && o == 0 {
		return nil
	}
	return []workflow.UsageChunk{{Model: model, InputTokens: in, OutputTokens: o, Source: "reported"}}
}

func tokenCount(v any) (in, out int64) {
	m, _ := v.(map[string]any)
	return num(m["input_tokens"]), num(m["output_tokens"])
}

func num(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	}
	return 0
}

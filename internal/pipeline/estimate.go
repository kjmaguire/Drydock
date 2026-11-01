package pipeline

import (
	"context"
	"encoding/json"
	"math"

	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Drydock's own estimate of a session's context occupancy, for a CLI that reports none (docs/token-control.md rule 5, decision
// P39 (5), built by P128). Claude Code and the ACP agents that send usage_update report the occupancy themselves; a CLI that does
// not (a custom CLI, Antigravity, an ACP agent without usage_update) was never continued. Now Drydock counts what went into the
// session: every package it sent (chars/4, with the instruction files the CLI reads), and what came back during each turn, the
// larger of the bytes of the messages, tool calls and tool results the runtime saw (chars/4) and the output tokens the CLI
// reported. To that it adds a margin for what it cannot see and a fixed allowance for the CLI's own system prompt and tool
// definitions, and holds the result to a lower threshold than a reported figure:
//
//   - EstimateMarginPct (25 %): chars/4 undercounts code and other dense text, which tokenizes nearer 3 characters a token;
//   - EstimateCLIOverhead (10,000 tokens): an agent CLI's own system prompt and tool definitions, which never pass through
//     Drydock (Claude Code's are about 15,000; a smaller CLI's less);
//   - EstimatedHandoffPct (50 %): what neither sees, a file the CLI read outside the stream or the reasoning it keeps, has 20
//     points of the window before the 70 % a reported figure is held to; an estimate 40 % under the real figure still hands off
//     before the real occupancy reaches 70 %.
//
// The window is the model's context length from the provider catalog when Drydock knows it (the smallest any provider lists for
// the model), else DefaultEstimateWindow, a window every current coding model meets. The estimate is recorded in the work node's
// output (contextEstimate), and a decision taken on it says so in its reason, the manifest's line and the agent.session event.

// Bounds of the occupancy estimate.
const (
	EstimatedHandoffPct   = 50
	EstimateMarginPct     = 25
	EstimateCLIOverhead   = 10_000
	DefaultEstimateWindow = 128_000
)

// Where an estimate's window came from (ContextEstimate.Window).
const (
	WindowCatalog = "catalog"
	WindowDefault = "default"
)

// ContextEstimate is Drydock's estimate of a session's context occupancy after a turn.
type ContextEstimate struct {
	// Used is the estimate (margin and overhead included) and Size the window it is held against, in tokens.
	Used int64 `json:"used"`
	Size int64 `json:"size"`
	// Sent is what Drydock sent into the session over all its turns, Seen what came back, in estimated tokens.
	Sent int64 `json:"sent"`
	Seen int64 `json:"seen"`
	// MarginPct and Overhead are what was added; Window is where Size came from (catalog, default).
	MarginPct int    `json:"marginPct"`
	Overhead  int64  `json:"overhead"`
	Window    string `json:"window"`
}

// EstimateOccupancy estimates a session's occupancy after a turn: base is the estimate after the session's previous turn (nil for
// the session's first), sent the tokens of the package this turn sent, res the turn's result and window the model's context
// length (0: unknown). It returns nil when the CLI reported an occupancy itself.
func EstimateOccupancy(base *ContextEstimate, sent int, res rt.TurnResult, window int64) *ContextEstimate {
	var output int64
	for _, u := range res.Usage {
		if u.ContextSize > 0 {
			return nil
		}
		output += u.OutputTokens
	}
	e := &ContextEstimate{MarginPct: EstimateMarginPct, Overhead: EstimateCLIOverhead, Size: window, Window: WindowCatalog}
	if window <= 0 {
		e.Size, e.Window = DefaultEstimateWindow, WindowDefault
	}
	if base != nil {
		e.Sent, e.Seen = base.Sent, base.Seen
	}
	e.Sent += int64(sent)
	e.Seen += max((res.SeenBytes+3)/4, output)
	e.Used = int64(math.Ceil(float64(e.Sent+e.Seen)*(100+EstimateMarginPct)/100)) + EstimateCLIOverhead
	return e
}

// contextWindow is the turn's model's context length, 0 when unknown.
func (d Deps) contextWindow(ctx context.Context, cfg rt.Config) int64 {
	if d.ContextWindow == nil {
		return 0
	}
	return d.ContextWindow(ctx, cfg)
}

// estimateTurn adds the occupancy estimate to a work turn's output when its CLI reported none: from the estimate of the session's
// previous turn when the session was continued, else from nothing.
func (d Deps) estimateTurn(ctx context.Context, cfg rt.Config, prev *WorkOutput, dec SessionDecision, sent int, res rt.TurnResult) *ContextEstimate {
	var base *ContextEstimate
	if dec.Action == SessionContinue && prev != nil {
		base = prev.Estimate
	}
	return EstimateOccupancy(base, sent, res, d.contextWindow(ctx, cfg))
}

// Corrected reports whether a work turn runs because its previous work was sent back: a failing check, or a review (the
// reviewer's REVISE, a person's Request changes) in the inbox. Guidance from the lead or a person, a merge conflict and a watchdog
// notice are not corrections.
func Corrected(in workflow.NodeInput) bool {
	for _, m := range in.Inbox {
		var cr workflow.CheckResult
		if json.Unmarshal(m.Value, &cr) == nil && cr.Name != "" && !cr.Passed {
			return true
		}
		var o map[string]any
		if json.Unmarshal(m.Value, &o) != nil {
			continue
		}
		_, conflict := o["conflict"]
		_, wd := o["watchdog"]
		_, guidance := o["guidance"]
		if fb, ok := o["feedback"].(string); ok && fb != "" && !conflict && !wd && !guidance {
			return true
		}
	}
	return false
}

// Package claude adapts Claude Code driven headlessly (decision D1): the user's own, unmodified, user-logged-in `claude`
// process speaking stream-json on stdin and stdout. Drydock never reads or moves Claude credentials.
package claude

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/kjmaguire/drydock/internal/workflow"
)

// Parser converts Claude Code stream-json lines into normalized chunks. Unknown event types and fields are tolerated.
type Parser struct {
	mu          sync.Mutex
	calls       map[string]workflow.ToolCall // tool_use id -> call, so results carry name and args
	lastContext int64                        // input+cache tokens of the latest assistant message (context occupancy)
	interrupted bool
	sessionID   string
	partialSeen bool // true once stream_event text deltas were seen for the current message
	// seenCost and seenModels are what the session's earlier results reported. Claude Code reports total_cost_usd and
	// modelUsage as running totals of the whole session, while usage is the turn's own (contracts CLAUDE-USAGE-02 and -03,
	// measured with 2.1.296), so a process that answers several turns (an interrupt and its follow-up, a watchdog nudge, a
	// kept session) or resumes a session would count earlier turns again; each result is recorded as the difference instead.
	seenCost   float64
	seenModels map[string]modelTotals
	// gateway is the MCP server the session cannot do without (GatewayServer, when the session was given the gateway's
	// configuration). Claude Code starts the session even when a server fails to connect and only says so in system/init
	// ("status":"failed", measured with 2.1.296), so the agent would work with none of Drydock's tools: for a sandboxed worker
	// no way to run a command, for every agent no Drydock approvals. The parser makes that a fatal error instead.
	gateway string
}

// GatewayServer is the name of Drydock's MCP server in the configuration the gateway writes (mcp.ServerName).
const GatewayServer = "drydock"

// modelTotals are one model's running totals in a session.
type modelTotals struct {
	In    int64   `json:"in"`
	Out   int64   `json:"out"`
	Read  int64   `json:"read"`
	Write int64   `json:"write"`
	Cost  float64 `json:"cost"`
}

// sessionTotals are what a session's results have reported so far: UsageChunk.Totals after each result, and
// RunSpec.UsageBaseline for a process that resumes the session. Claude Code reports a resumed session's totals including its
// earlier processes (measured with 2.1.296: the resumed turn said 17,824 tokens = 8,880 before + 8,944 its own).
type sessionTotals struct {
	CostUSD float64                `json:"costUsd"`
	Models  map[string]modelTotals `json:"models,omitempty"`
}

// SetBaseline starts the running totals from a resumed session's last totals (RunSpec.UsageBaseline); a baseline that does not
// parse is ignored.
func (p *Parser) SetBaseline(raw json.RawMessage) {
	var b sessionTotals
	if len(raw) == 0 || json.Unmarshal(raw, &b) != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seenCost, p.seenModels = b.CostUSD, b.Models
}

func (p *Parser) totals() json.RawMessage {
	b, _ := json.Marshal(sessionTotals{CostUSD: p.seenCost, Models: p.seenModels})
	return b
}

// NewParser creates a parser.
func NewParser() *Parser { return &Parser{calls: map[string]workflow.ToolCall{}} }

// RequireGateway makes a gateway server that failed to connect a fatal error of the session (see Parser.gateway).
func (p *Parser) RequireGateway(name string) { p.mu.Lock(); p.gateway = name; p.mu.Unlock() }

// MarkInterrupted records that an interrupt was requested so the next error_during_execution result is reported as "interrupted".
func (p *Parser) MarkInterrupted() { p.mu.Lock(); p.interrupted = true; p.mu.Unlock() }

// SessionID returns the session id seen in system/init.
func (p *Parser) SessionID() string { p.mu.Lock(); defer p.mu.Unlock(); return p.sessionID }

type rawEvent struct {
	Type    string          `json:"type"`
	Subtype string          `json:"subtype"`
	Message json.RawMessage `json:"message"`
	Event   json.RawMessage `json:"event"`
	Session string          `json:"session_id"`
	Model   string          `json:"model"`
	Tools   []any           `json:"tools"`
	// MCPServers is system/init's list of the session's MCP servers and whether each connected.
	MCPServers []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"mcp_servers"`
	// result fields
	IsError    bool            `json:"is_error"`
	Result     json.RawMessage `json:"result"`
	StopReason string          `json:"stop_reason"`
	TotalCost  float64         `json:"total_cost_usd"`
	Usage      *usage          `json:"usage"`
	ModelUsage map[string]struct {
		InputTokens              int64   `json:"inputTokens"`
		OutputTokens             int64   `json:"outputTokens"`
		CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
		CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
		CostUSD                  float64 `json:"costUSD"`
		ContextWindow            int64   `json:"contextWindow"`
	} `json:"modelUsage"`
	// Errors is what a result reports when the CLI failed before or outside a model turn, for example "No conversation found
	// with session ID: ..." for a --resume of a session that does not exist (observed with claude 2.1.295).
	Errors []string `json:"errors"`
	// PermissionDenials are the tool uses the CLI refused in this turn because the permission mode and allow rules did not
	// cover them (headless claude never asks; observed with claude 2.1.295 in --permission-mode default).
	PermissionDenials []struct {
		ToolName  string          `json:"tool_name"`
		ToolUseID string          `json:"tool_use_id"`
		ToolInput json.RawMessage `json:"tool_input"`
	} `json:"permission_denials"`
	// control
	Response json.RawMessage `json:"response"`
}

type usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	OutputDetails            struct {
		Thinking int64 `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

type message struct {
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *usage          `json:"usage"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// ControlAck is returned for control_response lines so the session can complete interrupt waits.
type ControlAck struct {
	RequestID string
	Success   bool
}

// Parse converts one stdout line. ack is non-nil for control_response lines.
func (p *Parser) Parse(line []byte) (chunks []workflow.Chunk, ack *ControlAck) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return nil, nil
	}
	var ev rawEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch ev.Type {
	case "system":
		if ev.Subtype == "init" {
			p.sessionID = ev.Session
			chunks = append(chunks, workflow.NewChunk(workflow.ChunkSession, workflow.SessionChunk{SessionID: ev.Session, Model: ev.Model, Tools: len(ev.Tools)}))
			for _, srv := range ev.MCPServers {
				if p.gateway != "" && srv.Name == p.gateway && srv.Status == "failed" {
					chunks = append(chunks, workflow.NewChunk(workflow.ChunkError, workflow.ErrorChunk{Fatal: true,
						Message: "Drydock's tool gateway did not connect to Claude Code (its MCP server " + p.gateway + " failed to start), so the agent would work without Drydock's tools; the turn was stopped"}))
				}
			}
		}
	case "stream_event":
		var se struct {
			Type  string `json:"type"`
			Delta struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			} `json:"delta"`
		}
		if json.Unmarshal(ev.Event, &se) == nil && se.Type == "content_block_delta" {
			switch se.Delta.Type {
			case "text_delta":
				p.partialSeen = true
				chunks = append(chunks, workflow.NewChunk(workflow.ChunkText, workflow.TextDelta{Text: se.Delta.Text}))
			case "thinking_delta":
				chunks = append(chunks, workflow.NewChunk(workflow.ChunkThinking, workflow.TextDelta{Text: se.Delta.Thinking}))
			}
		}
	case "assistant":
		var m message
		if json.Unmarshal(ev.Message, &m) != nil {
			return nil, nil
		}
		if m.Usage != nil {
			p.lastContext = m.Usage.InputTokens + m.Usage.CacheReadInputTokens + m.Usage.CacheCreationInputTokens
		}
		for _, b := range blocks(m.Content) {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) != "" {
					chunks = append(chunks, workflow.NewChunk(workflow.ChunkMessageEnd, workflow.TextDelta{Text: b.Text}))
				}
			case "thinking":
				if b.Thinking != "" {
					chunks = append(chunks, workflow.NewChunk(workflow.ChunkThinking, workflow.TextDelta{Text: b.Thinking}))
				}
			case "tool_use":
				tc := workflow.ToolCall{ID: b.ID, Name: b.Name, Args: b.Input}
				p.calls[b.ID] = tc
				chunks = append(chunks, workflow.NewChunk(workflow.ChunkToolCall, tc))
			}
		}
		p.partialSeen = false
	case "user":
		var m message
		if json.Unmarshal(ev.Message, &m) != nil {
			return nil, nil
		}
		for _, b := range blocks(m.Content) {
			if b.Type != "tool_result" {
				continue
			}
			tc := p.calls[b.ToolUseID]
			delete(p.calls, b.ToolUseID)
			chunks = append(chunks, workflow.NewChunk(workflow.ChunkToolResult, workflow.ToolResult{
				ID: b.ToolUseID, Name: tc.Name, Args: tc.Args, Output: flatten(b.Content), IsError: b.IsError}))
		}
	case "rate_limit_event":
		chunks = append(chunks, workflow.Chunk{Kind: workflow.ChunkRateLimit, Data: json.RawMessage(append([]byte(nil), line...))})
	case "control_response":
		var cr struct {
			Subtype   string `json:"subtype"`
			RequestID string `json:"request_id"`
		}
		if json.Unmarshal(ev.Response, &cr) == nil {
			ack = &ControlAck{RequestID: cr.RequestID, Success: cr.Subtype == "success"}
		}
	case "result":
		chunks = append(chunks, p.result(ev)...)
	}
	return chunks, ack
}

// turnCost is this result's own cost: the running total less what earlier results reported. A total that went down cannot
// be a running total, so it is taken as the turn's own (and becomes the new baseline).
func (p *Parser) turnCost(total float64) float64 {
	turn := total - p.seenCost
	if turn < 0 {
		turn = total
	}
	p.seenCost = total
	return turn
}

// turnModel is one model's share of this result, by the same rule as turnCost.
func (p *Parser) turnModel(model string, total modelTotals) modelTotals {
	if p.seenModels == nil {
		p.seenModels = map[string]modelTotals{}
	}
	prev := p.seenModels[model]
	p.seenModels[model] = total
	turn := modelTotals{In: total.In - prev.In, Out: total.Out - prev.Out, Read: total.Read - prev.Read, Write: total.Write - prev.Write, Cost: total.Cost - prev.Cost}
	if turn.In < 0 || turn.Out < 0 || turn.Read < 0 || turn.Write < 0 || turn.Cost < 0 {
		return total
	}
	return turn
}

func (p *Parser) result(ev rawEvent) []workflow.Chunk {
	var out []workflow.Chunk
	for _, d := range ev.PermissionDenials {
		if d.ToolName != "" {
			out = append(out, workflow.NewChunk(workflow.ChunkPermissionDenied, workflow.PermissionDenial{ToolName: d.ToolName, ToolUseID: d.ToolUseID, Input: d.ToolInput}))
		}
	}
	u := workflow.UsageChunk{Source: "reported", CostUSD: p.turnCost(ev.TotalCost), ContextUsed: p.lastContext}
	// A turn can use more than one model (the CLI's own helper calls, or Sonnet answering a plan-mode haiku session): the
	// turn is labeled with the model that cost the most, then the one with the most tokens, then by name, never by Go's
	// random map order.
	bestCost, bestTokens := -1.0, int64(-1)
	models := make([]string, 0, len(ev.ModelUsage))
	for model := range ev.ModelUsage {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		mu := ev.ModelUsage[model]
		turn := p.turnModel(model, modelTotals{In: mu.InputTokens, Out: mu.OutputTokens, Read: mu.CacheReadInputTokens, Write: mu.CacheCreationInputTokens, Cost: mu.CostUSD})
		tokens := turn.In + turn.Out + turn.Read + turn.Write
		if tokens == 0 && turn.Cost == 0 {
			continue // a model of an earlier turn that this turn did not use
		}
		if turn.Cost > bestCost || turn.Cost == bestCost && (tokens > bestTokens || tokens == bestTokens && model < u.Model) {
			u.Model, bestCost, bestTokens = model, turn.Cost, tokens
		}
		u.InputTokens += turn.In
		u.OutputTokens += turn.Out
		u.CachedTokens += turn.Read
		u.CacheWriteTokens += turn.Write
		if mu.ContextWindow > u.ContextSize {
			u.ContextSize = mu.ContextWindow
		}
	}
	if len(ev.ModelUsage) == 0 && ev.Usage != nil {
		u.InputTokens, u.OutputTokens = ev.Usage.InputTokens, ev.Usage.OutputTokens
		u.CachedTokens, u.CacheWriteTokens = ev.Usage.CacheReadInputTokens, ev.Usage.CacheCreationInputTokens
	}
	if ev.Usage != nil {
		u.ReasoningTokens = ev.Usage.OutputDetails.Thinking
	}
	u.Totals = p.totals()
	out = append(out, workflow.NewChunk(workflow.ChunkUsage, u))
	stop := ev.StopReason
	if ev.IsError || strings.HasPrefix(ev.Subtype, "error") {
		if ev.Subtype == "error_during_execution" && p.interrupted {
			stop = "interrupted"
			p.interrupted = false
		} else {
			if stop == "" {
				stop = ev.Subtype
			}
			var msg string
			_ = json.Unmarshal(ev.Result, &msg)
			if msg == "" && len(ev.Errors) > 0 {
				msg = strings.Join(ev.Errors, "; ")
			}
			if msg == "" {
				msg = ev.Subtype
			}
			out = append(out, workflow.NewChunk(workflow.ChunkError, workflow.ErrorChunk{Message: msg}))
		}
	}
	if stop == "" {
		stop = "end_turn"
	}
	out = append(out, workflow.NewChunk(workflow.ChunkDone, workflow.DoneChunk{StopReason: stop}))
	return out
}

// blocks decodes message content, which is either a string or a list of blocks.
func blocks(raw json.RawMessage) []block {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' {
		var s string
		_ = json.Unmarshal(raw, &s)
		return []block{{Type: "text", Text: s}}
	}
	var bs []block
	_ = json.Unmarshal(raw, &bs)
	return bs
}

// flatten renders tool_result content (a string or a list of text blocks) as text.
func flatten(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var sb strings.Builder
		for i, p := range parts {
			if p.Type == "text" || p.Text != "" {
				if i > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(p.Text)
			}
		}
		return sb.String()
	}
	return string(raw)
}

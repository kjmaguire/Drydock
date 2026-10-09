// Package claude adapts Claude Code driven headlessly (decision D1): the user's own, unmodified, user-logged-in `claude`
// process speaking stream-json on stdin and stdout. Drydock never reads or moves Claude credentials.
package claude

import (
	"bytes"
	"encoding/json"
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
}

// NewParser creates a parser.
func NewParser() *Parser { return &Parser{calls: map[string]workflow.ToolCall{}} }

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

func (p *Parser) result(ev rawEvent) []workflow.Chunk {
	var out []workflow.Chunk
	for _, d := range ev.PermissionDenials {
		if d.ToolName != "" {
			out = append(out, workflow.NewChunk(workflow.ChunkPermissionDenied, workflow.PermissionDenial{ToolName: d.ToolName, ToolUseID: d.ToolUseID, Input: d.ToolInput}))
		}
	}
	u := workflow.UsageChunk{Source: "reported", CostUSD: ev.TotalCost, ContextUsed: p.lastContext}
	for model, mu := range ev.ModelUsage { // a turn normally has one model; the first wins for labeling
		if u.Model == "" || mu.InputTokens+mu.OutputTokens > 0 && u.Model == "" {
			u.Model = model
		}
		u.InputTokens += mu.InputTokens
		u.OutputTokens += mu.OutputTokens
		u.CachedTokens += mu.CacheReadInputTokens
		u.CacheWriteTokens += mu.CacheCreationInputTokens
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

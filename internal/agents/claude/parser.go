// Package claude adapts Claude Code driven headlessly (decision D1): the user's own, unmodified, user-logged-in `claude`
// process speaking stream-json on stdin and stdout. Drydock never reads or moves Claude credentials.
package claude

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/workflow"
)

// Parser converts Claude Code stream-json lines into normalized chunks. Unknown event types and fields are tolerated.
type Parser struct {
	mu          sync.Mutex
	calls       map[string]workflow.ToolCall // tool_use id -> call, so results carry name and args
	lastContext int64                        // input+cache tokens of the latest assistant message (context occupancy)
	interrupted bool
	sessionID   string
	// thinkingStreamed is set while the current assistant message's thinking arrived as stream_event thinking deltas, so the
	// whole thinking block the following assistant event repeats is not reported a second time (the deltas were). It is cleared
	// when that assistant event is read. Text has no such flag: the whole of a text message is always reported as message_end,
	// deltas or not, because the chat and the Activity take that as the message's end and its final text.
	thinkingStreamed bool
	// apiError is the text of the API-error message Claude Code wrote for the current turn (see apiErrorMessage), so the
	// result that follows, which repeats the same text as its error, does not report it twice.
	apiError string
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
	// unknown counts the top-level event types this session sent that the parser does not know (docs/connections.md
	// "drift"): a CLI release that renames or adds events shows up here instead of being dropped without a word.
	unknown map[string]int
}

// ignoredEvents are the top-level event types Claude Code sends that carry nothing a turn needs (seen in recorded streams of
// 2.1.296: progress of a running tool, the auto-compact state, the active goal; keep-alives; requests the CLI only sends to an
// SDK host). Anything else unknown is drift.
var ignoredEvents = map[string]bool{"tool_progress": true, "autocompact_state": true, "active_goal": true, "keep_alive": true, "control_request": true}

// UnknownEvents returns how many events of each unknown top-level type the session sent.
func (p *Parser) UnknownEvents() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]int, len(p.unknown))
	for k, v := range p.unknown {
		out[k] = v
	}
	return out
}

// GatewayServer is the name of Drydock's MCP server in the configuration the gateway writes (mcp.ServerName).
const GatewayServer = "drydock"

// PermissionPromptTool is the gateway tool a worker's permission prompts go to (mcp.ToolPermissionPrompt as Claude names it).
const PermissionPromptTool = "mcp__" + GatewayServer + "__permission_prompt"

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
	// PermissionMode is system/init's permission mode of the session ("default", "acceptEdits", ...).
	PermissionMode string `json:"permissionMode"`
	// IsAPIErrorMessage marks an assistant event that is not the model's words but the CLI's report of an API call that
	// failed ("API Error: 529 ..."), written as an assistant message whose text is the error. Claude Code 2.1.310 sets it on
	// the event beside "message", not inside it (read from the binary: the flag is among the wrapper-level fields, next to
	// api_error_status and api_error).
	IsAPIErrorMessage bool `json:"is_api_error_message"`
	// MCPServers is system/init's list of the session's MCP servers and whether each connected.
	MCPServers []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"mcp_servers"`
	// result fields
	IsError bool            `json:"is_error"`
	Result  json.RawMessage `json:"result"`
	// StopReason is the stop reason of the turn's last assistant message, not of the turn: a turn that failed after the model
	// said anything carries "tool_use" or "end_turn" here (Claude Code 2.1.x), so a failed result's stop reason is never read
	// from it (see result).
	StopReason string `json:"stop_reason"`
	// TerminalReason is why the turn ended, on every result of Claude Code 2.1.310: "completed", or for a turn that did not
	// complete "api_error", "max_turns", "aborted_streaming", "aborted_tools", "blocking_limit", "prompt_too_long",
	// "model_error", "budget_exhausted", ... (the values read from the binary).
	TerminalReason string  `json:"terminal_reason"`
	TotalCost      float64 `json:"total_cost_usd"`
	Usage          *usage  `json:"usage"`
	ModelUsage     map[string]struct {
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
		return p.unreadable(line), nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch ev.Type {
	case "system":
		if ev.Subtype == "init" {
			p.sessionID = ev.Session
			chunks = append(chunks, workflow.NewChunk(workflow.ChunkSession, workflow.SessionChunk{SessionID: ev.Session, Model: ev.Model, Tools: len(ev.Tools), PermissionMode: ev.PermissionMode}))
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
		if err := json.Unmarshal(ev.Event, &se); err != nil {
			return p.driftShape("stream_event"), nil
		}
		if se.Type == "content_block_delta" {
			switch se.Delta.Type { // an empty delta carries nothing to show
			case "text_delta":
				if se.Delta.Text != "" {
					chunks = append(chunks, workflow.NewChunk(workflow.ChunkText, workflow.TextDelta{Text: se.Delta.Text}))
				}
			case "thinking_delta":
				if se.Delta.Thinking != "" {
					p.thinkingStreamed = true
					chunks = append(chunks, workflow.NewChunk(workflow.ChunkThinking, workflow.TextDelta{Text: se.Delta.Thinking}))
				}
			}
		}
	case "assistant":
		var m message
		if json.Unmarshal(ev.Message, &m) != nil {
			return p.driftShape("assistant"), nil
		}
		if m.Usage != nil {
			p.lastContext = m.Usage.InputTokens + m.Usage.CacheReadInputTokens + m.Usage.CacheCreationInputTokens
		}
		if ev.IsAPIErrorMessage {
			chunks = append(chunks, p.apiErrorMessage(m)...)
			p.thinkingStreamed = false
			break
		}
		// The text blocks of one assistant event are one message, reported as one message_end: joined as they are, since the
		// deltas streamed for them concatenate to the same text, which is what the chat and the Activity match the whole
		// against.
		var text strings.Builder
		for _, b := range blocks(m.Content) {
			switch b.Type {
			case "text":
				text.WriteString(b.Text)
			case "thinking":
				if b.Thinking != "" && !p.thinkingStreamed {
					chunks = append(chunks, workflow.NewChunk(workflow.ChunkThinking, workflow.TextDelta{Text: b.Thinking}))
				}
			case "tool_use":
				tc := workflow.ToolCall{ID: b.ID, Name: b.Name, Args: b.Input}
				p.calls[b.ID] = tc
				chunks = append(chunks, workflow.NewChunk(workflow.ChunkToolCall, tc))
			}
		}
		if strings.TrimSpace(text.String()) != "" {
			chunks = append(chunks, workflow.NewChunk(workflow.ChunkMessageEnd, workflow.TextDelta{Text: text.String()}))
		}
		p.thinkingStreamed = false
	case "user":
		var m message
		if json.Unmarshal(ev.Message, &m) != nil {
			return p.driftShape("user"), nil
		}
		for _, b := range blocks(m.Content) {
			if b.Type != "tool_result" {
				continue
			}
			tc := p.calls[b.ToolUseID]
			delete(p.calls, b.ToolUseID)
			chunks = append(chunks, workflow.NewChunk(workflow.ChunkToolResult, workflow.ToolResult{
				ID: b.ToolUseID, Name: tc.Name, Args: tc.Args, Output: clipOutput(flatten(b.Content)), IsError: b.IsError}))
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
	default:
		if ignoredEvents[ev.Type] || ev.Type == "" {
			break
		}
		// once per type and session, as a warning the run log and the chat show: the turn goes on
		chunks = append(chunks, p.driftUnknown(ev.Type)...)
	}
	return chunks, ack
}

// apiErrorMessage reads the assistant event Claude Code writes when an API call fails (rawEvent.IsAPIErrorMessage): its text
// is the error, not the agent's words, so it is the turn's non-fatal error and never a message. Reported as a message it was
// shown as the agent's answer and counted as one, and the turn was marked done (AG-UI review 2026-10-25). The result that
// follows says is_error, and workflow.TurnFailed then fails the turn with this text. Called with p.mu held.
func (p *Parser) apiErrorMessage(m message) []workflow.Chunk {
	var text strings.Builder
	for _, b := range blocks(m.Content) {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	msg := strings.TrimSpace(text.String())
	if msg == "" {
		msg = "Claude Code reported an API error"
	}
	p.apiError = msg
	return []workflow.Chunk{workflow.NewChunk(workflow.ChunkError, workflow.ErrorChunk{Message: msg})}
}

// note counts an event of a kind the parser could not use and reports whether it is the first of its kind in the session. Called
// with p.mu held.
func (p *Parser) note(kind string) bool {
	if p.unknown == nil {
		p.unknown = map[string]int{}
	}
	p.unknown[kind]++
	return p.unknown[kind] == 1
}

// drift counts an event the parser could not use and returns the warning for its kind the first time the session sends one (the
// run log and the chat show it, the turn goes on). say is what Claude Code sent, in words. Called with p.mu held.
func (p *Parser) drift(kind, say string) []workflow.Chunk {
	if !p.note(kind) {
		return nil
	}
	return []workflow.Chunk{workflow.NewChunk(workflow.ChunkError, workflow.ErrorChunk{Message: "adapter drift: Claude Code sent " + say +
		". If turns stop finishing or lose their results, this CLI version changed its output; check the Connections page and the contract suite.",
		Code: workflow.CodeAdapterDrift, Detail: kind})}
}

// driftUnknown is drift for an event type the parser does not know; driftShape for a type it knows, in a shape it cannot read.
func (p *Parser) driftUnknown(typ string) []workflow.Chunk {
	return p.drift(typ, "an event of type "+strconv.Quote(typ)+" that Drydock does not read")
}

func (p *Parser) driftShape(typ string) []workflow.Chunk {
	return p.drift(typ+" (unreadable)", "an event of type "+strconv.Quote(typ)+" in a shape Drydock cannot read")
}

// unreadable answers a line that starts like an event but cannot be read into one: a type the parser knows in a shape it does not
// (a CLI release that changed a field's type), or text that is not JSON. It used to be dropped without a word (audit 2026-10-16,
// L27). A result that cannot be read leaves the turn's outcome and cost unknown, so it ends the turn with an error; any other
// event is drift: counted, warned about once, and the turn goes on.
func (p *Parser) unreadable(line []byte) []workflow.Chunk {
	var head struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(line, &head)
	p.mu.Lock()
	defer p.mu.Unlock()
	switch head.Type {
	case "result":
		p.note("result (unreadable)")
		return []workflow.Chunk{workflow.NewChunk(workflow.ChunkError, workflow.ErrorChunk{Fatal: true, Code: workflow.CodeAdapterDrift, Detail: "result (unreadable)",
			Message: "Claude Code's result for the turn could not be read, so what the turn did and what it cost are unknown; the turn was stopped. This CLI version may have changed its output: check the Connections page and the contract suite."})}
	case "":
		return p.drift("(unreadable line)", "a line that starts like an event but is not one Drydock can read")
	}
	return p.driftShape(head.Type)
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
			// A failed turn's stop reason says that it failed, never what the last assistant message stopped with (which
			// is "tool_use" when a tool loop ran out of turns, "end_turn" when the API failed after an answer): the chat and
			// the Activity read "error" and "error_*" as the turn's failure (workflow.TurnFailed), and a turn that was
			// marked done with the error beside it was the AG-UI review's finding (2026-10-25). The subtype names the
			// failure when the CLI gave it one (error_max_turns, error_during_execution, ...), else terminal_reason does
			// (error_api_error, error_prompt_too_long, ...), else it is plain "error".
			switch {
			case strings.HasPrefix(ev.Subtype, "error"):
				stop = ev.Subtype
			case ev.TerminalReason != "" && ev.TerminalReason != "completed":
				stop = "error_" + ev.TerminalReason
			default:
				stop = "error"
			}
			var msg string
			_ = json.Unmarshal(ev.Result, &msg)
			if msg == "" && len(ev.Errors) > 0 {
				msg = strings.Join(ev.Errors, "; ")
			}
			if msg == "" {
				msg = ev.Subtype
			}
			// the API error the turn's assistant message already reported (apiErrorMessage) is not reported twice
			if strings.TrimSpace(msg) != p.apiError {
				out = append(out, workflow.NewChunk(workflow.ChunkError, workflow.ErrorChunk{Message: msg}))
			}
		}
	}
	p.apiError = ""
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
// maxToolOutput is the most of one tool result that is kept: its start and its end, which are what a person and the watchdog read.
const maxToolOutput = 256 << 10

// clipOutput keeps the start and the end of a tool result over maxToolOutput, and says how much was left out (audit 2026-10-16, M22):
// a file read of tens of megabytes was kept whole in the run's events, the UI's ring and the window's messages.
func clipOutput(s string) string {
	if len(s) <= maxToolOutput {
		return s
	}
	head, tail := maxToolOutput/2, maxToolOutput/2
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	cut := len(s) - tail
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	return s[:head] + "\n[... " + strconv.Itoa(cut-head) + " bytes left out of this result ...]\n" + s[cut:]
}

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

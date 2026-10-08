package workflow

import "encoding/json"

// Chunk kinds produced by agent adapters and consumed by the UI, the watchdog and usage recording.
const (
	ChunkText       = "text"        // assistant text delta: TextDelta
	ChunkThinking   = "thinking"    // reasoning delta: TextDelta
	ChunkMessageEnd = "message_end" // a complete assistant message: TextDelta (full text)
	ChunkToolCall   = "tool_call"   // ToolCall
	ChunkToolResult = "tool_result" // ToolResult
	ChunkUsage      = "usage"       // UsageChunk
	ChunkPermission = "permission_request"
	ChunkDone       = "done"  // end of a turn: DoneChunk
	ChunkError      = "error" // ErrorChunk
	ChunkLog        = "log"   // free-form check/tool-member output
)

// TextDelta is the payload of text, thinking and message_end chunks.
type TextDelta struct {
	Text string `json:"text"`
}

// ToolCall is the payload of a tool_call chunk (adapters must emit it: ToolCallVisibility).
type ToolCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// ToolResult is the payload of a tool_result chunk. Name and Args repeat the call so observers need no join.
type ToolResult struct {
	ID      string          `json:"id,omitempty"`
	Name    string          `json:"name"`
	Args    json.RawMessage `json:"args,omitempty"`
	Output  string          `json:"output"`
	IsError bool            `json:"isError,omitempty"`
}

// UsageChunk reports token usage; Source is "reported" or "estimated".
type UsageChunk struct {
	Model            string  `json:"model,omitempty"`
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CachedTokens     int64   `json:"cachedTokens,omitempty"`
	CacheWriteTokens int64   `json:"cacheWriteTokens,omitempty"`
	ReasoningTokens  int64   `json:"reasoningTokens,omitempty"`
	CostUSD          float64 `json:"costUsd,omitempty"`
	ContextUsed      int64   `json:"contextUsed,omitempty"`
	ContextSize      int64   `json:"contextSize,omitempty"`
	Source           string  `json:"source"`
}

// DoneChunk ends a turn.
type DoneChunk struct {
	StopReason string `json:"stopReason,omitempty"`
}

// ErrorChunk reports an agent-side error.
type ErrorChunk struct {
	Message string `json:"message"`
	Fatal   bool   `json:"fatal,omitempty"`
}

// NewChunk marshals v into a chunk of the given kind.
func NewChunk(kind string, v any) Chunk {
	b, _ := json.Marshal(v)
	return Chunk{Kind: kind, Data: b}
}

// Decode unmarshals the chunk payload into v.
func (c Chunk) Decode(v any) error { return json.Unmarshal(c.Data, v) }

// CheckResult is the outcome of a deterministic check node, fed to the watchdog at loop back edges.
type CheckResult struct {
	Name         string   `json:"name"`
	Passed       bool     `json:"passed"`
	ExitCode     int      `json:"exitCode"`
	Output       string   `json:"output"` // normalized/truncated stderr+stdout tail
	FailingTests []string `json:"failingTests,omitempty"`
	DiffHash     string   `json:"diffHash,omitempty"` // worktree diff hash at the time of the check
}

// Additional chunk kinds.
const (
	ChunkSession   = "session"    // SessionChunk: the CLI reported its session id/model
	ChunkRateLimit = "rate_limit" // raw plan-quota/rate-limit information (Claude rate_limit_event)
)

// SessionChunk announces the CLI session.
type SessionChunk struct {
	SessionID string `json:"sessionId"`
	Model     string `json:"model,omitempty"`
	Tools     int    `json:"tools,omitempty"`
}

// ChunkPlan is an agent's own task plan (ACP `plan` updates): PlanChunk. Informational; nothing decides on it.
const ChunkPlan = "plan"

// PlanChunk is the full current plan (each ACP plan update replaces the previous one).
type PlanChunk struct {
	Entries []PlanEntry `json:"entries"`
}

// PlanEntry is one step of a plan.
type PlanEntry struct {
	Content  string `json:"content"`
	Status   string `json:"status,omitempty"`   // pending, in_progress, completed
	Priority string `json:"priority,omitempty"` // high, medium, low
}

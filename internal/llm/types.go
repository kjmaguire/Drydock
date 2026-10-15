// Package llm is the app's own LLM client layer (brief section 8): one Client interface, wire implementations in
// llm/openai and llm/anthropic, and the Wrapper that every Drydock-originated model call goes through. The Wrapper
// enforces the invariants no backend may skip: the data_class guard, secret scanning of outbound prompts, quota
// reservation, Retry-After handling, the exact-match response cache, and usage and cost recording
// (docs/model-routing.md, docs/token-control.md s8-s9, docs/security.md s9).
package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/kjmaguire/drydock/internal/providers"
)

// Usage is token usage for one call; see providers.Usage (Input includes cached tokens, D12).
type Usage = providers.Usage

// Effort is the abstract reasoning effort (docs/model-routing.md s1); a backend maps it or ignores it.
type Effort string

const (
	EffortNone   Effort = ""
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortMax    Effort = "max"
)

// Sensitivity is the class of a context manifest entry (docs/security.md s9). Anything not explicitly tagged is private.
type Sensitivity string

const (
	SensitivityPrivate    Sensitivity = "private"
	SensitivityPublicDocs Sensitivity = "public_docs"
	SensitivitySynthetic  Sensitivity = "synthetic" // fixed probe content that contains no user data
)

// ManifestEntry describes one source in the request's context package.
type ManifestEntry struct {
	SourceType  string      `json:"sourceType"`
	SourceRef   string      `json:"sourceRef,omitempty"`
	Sensitivity Sensitivity `json:"sensitivity"`
}

// Manifest is the request's context manifest as far as the guard needs it.
type Manifest struct {
	Entries []ManifestEntry `json:"entries"`
}

// Ceiling is the highest sensitivity in the manifest. It is private when the manifest is nil or empty, or any entry
// is private or untagged: the guard fails closed.
func (m *Manifest) Ceiling() Sensitivity {
	if m == nil || len(m.Entries) == 0 {
		return SensitivityPrivate
	}
	for _, e := range m.Entries {
		if e.Sensitivity != SensitivityPublicDocs && e.Sensitivity != SensitivitySynthetic {
			return SensitivityPrivate
		}
	}
	return SensitivityPublicDocs
}

// PrivateSources lists the references of private or untagged entries (for the refusal message).
func (m *Manifest) PrivateSources() []string {
	if m == nil || len(m.Entries) == 0 {
		return []string{"(no manifest: treated as private)"}
	}
	var out []string
	for _, e := range m.Entries {
		if e.Sensitivity != SensitivityPublicDocs && e.Sensitivity != SensitivitySynthetic {
			ref := e.SourceType
			if e.SourceRef != "" {
				ref += ":" + e.SourceRef
			}
			out = append(out, ref)
		}
	}
	return out
}

// Block is one system block. Cache marks the end of a stable prefix region (tools, then system blocks, then messages
// is the cache order, docs/token-control.md s4); the Anthropic backend turns it into a cache_control marker.
type Block struct {
	Text  string `json:"text"`
	Cache bool   `json:"cache,omitempty"`
}

// ToolDef is a tool the model may call. Parameters is a JSON Schema object.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolCall is a model-requested tool call. Arguments is the raw JSON object.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Message is a conversation message. Role is user, assistant or tool.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`  // assistant
	ToolCallID string     `json:"toolCallId,omitempty"` // tool result
	// Reasoning replays an earlier interleaved-thinking turn to models that need it (North Mini Code via vLLM or
	// SGLang). Backends that cannot replay reasoning (Anthropic needs signatures) drop it.
	Reasoning string `json:"reasoning,omitempty"`
	Cache     bool   `json:"cache,omitempty"` // cache breakpoint after this message (Anthropic)
}

// Target is a provider and model.
type Target struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// Request is one model call.
type Request struct {
	Provider  string // provider id in the registry
	Model     string
	Effort    Effort
	MaxTokens int // 0 = backend default
	System    []Block
	Messages  []Message
	Tools     []ToolDef
	// ToolChoice is "", "auto", "none", "required", or the name of a tool.
	ToolChoice  string
	Temperature *float64
	TopP        *float64
	SessionID   string // stable per conversation: OpenRouter sticky routing wants it (docs/phase0 G)
	// AvoidEndpoints names OpenRouter endpoints (their tags) the request asks OpenRouter to skip, as provider.ignore.
	// The Wrapper sets it from recent endpoint uptime (decision P53 c); other flavors ignore it.
	AvoidEndpoints []string

	// Purpose names why the call is made (classify, summarize, consult, probe, ...). It decides cacheability.
	Purpose string
	// SideEffecting marks a call whose result is acted on in a way that makes repeating or replaying it wrong. Such a
	// call is never served from or written to the response cache.
	SideEffecting bool
	NoCache       bool
	CacheTTL      time.Duration // overrides the purpose default; ignored for side-effecting calls
	PackVersion   string        // part of the cache key: a new pack version invalidates

	// Manifest describes the context package. A nil manifest is treated as private (fail closed).
	Manifest *Manifest

	// Fallbacks are alternatives the router approved, tried in order when the primary target is unavailable.
	Fallbacks []Target

	ProjectID, RunID, TaskID, AgentID, DecisionID string
}

// CostSource labels how a cost figure was obtained; estimated and reported are never mixed silently.
type CostSource string

const (
	CostReported  CostSource = "reported"  // the provider returned the cost (OpenRouter usage.cost)
	CostEstimated CostSource = "estimated" // computed from the model_catalog price table
	CostUnknown   CostSource = "unknown"   // no price known; USD is 0 and Known is false
)

// Cost of a call in USD.
type Cost struct {
	USD    float64    `json:"usd"`
	Source CostSource `json:"source"`
	Known  bool       `json:"known"`
}

// Response is a finished call.
type Response struct {
	Content      string
	Reasoning    string
	ToolCalls    []ToolCall
	FinishReason string
	Usage        Usage
	// UsageReported is false when the provider returned no usage and Usage is a character-count estimate.
	UsageReported bool
	// ReportedCostUSD is the provider-reported cost (OpenRouter), nil otherwise. Backends set it; the Wrapper turns
	// it into Cost.
	ReportedCostUSD *float64
	Cost            Cost
	ModelUsed       string // the model that served the call (may differ from the request on routers)
	Provider        string // provider id that served the call
	CacheHit        bool
	EstSavedUSD     float64 // cache hits: the cost avoided, estimated from the fill (labeled estimated, never reported)
	Fallback        bool    // served by a router-approved alternative, not the primary target
	EffortApplied   bool
	Latency         time.Duration
	Header          http.Header `json:"-"` // response headers (rate limits)
}

// Delta is a streamed fragment.
type Delta struct {
	Text      string
	Reasoning string
	ToolCall  *ToolCallDelta
}

// ToolCallDelta is a streamed fragment of a tool call.
type ToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	Arguments string // JSON fragment
}

// Client is the LLM client contract. Implementations: llm/openai, llm/anthropic (single provider, no policy) and
// *Wrapper (policy over a registry of providers).
type Client interface {
	Complete(ctx context.Context, req Request) (Response, error)
	// Stream calls fn for each fragment and returns the assembled Response (usage comes from the final chunk).
	Stream(ctx context.Context, req Request, fn func(Delta) error) (Response, error)
}

// Package providers is the provider registry and model catalog, the quota ledger, the probes and the host health
// loop (docs/model-routing.md s4-s6). It never decides which model runs a task (that is internal/router) and never
// sends a prompt (that is internal/llm); it holds the facts both rely on: what a provider is, whether it is
// reachable, what it costs, how much quota is left, and which secrets it uses.
package providers

import (
	"encoding/json"
	"errors"
	"time"
)

// Kind is how a provider is reached (brief section 6).
type Kind string

const (
	KindAggregator Kind = "aggregator"
	KindDirect     Kind = "direct"
	KindHost       Kind = "host"
)

// DataClass is the provider's data-handling class. "trainable" is the brief's term (D13).
type DataClass string

const (
	DataPrivate   DataClass = "private"
	DataTrainable DataClass = "trainable"
)

// Tier is the price tier.
type Tier string

const (
	TierPremium Tier = "premium"
	TierCheap   Tier = "cheap"
	TierFree    Tier = "free"
)

// Flavor selects the wire details and probe endpoints. Detected from the base URL or the host's endpoints.
type Flavor string

const (
	FlavorOpenAI     Flavor = "openai" // any OpenAI-compatible chat completions endpoint
	FlavorOpenRouter Flavor = "openrouter"
	FlavorGemini     Flavor = "gemini" // Google AI Studio, OpenAI-compat endpoint
	FlavorAnthropic  Flavor = "anthropic"
	FlavorVLLM       Flavor = "vllm"
	FlavorSGLang     Flavor = "sglang"
	FlavorOllama     Flavor = "ollama"
)

// TLSMode records how the transport is secured (docs/model-routing.md s4, brief section 16).
type TLSMode string

const (
	TLSSystem      TLSMode = "system"            // https, system roots
	TLSPinned      TLSMode = "pinned_fingerprint" // https, self-signed leaf pinned by SHA-256
	TLSNoneLoop    TLSMode = "none_loopback"     // plain http on loopback
	TLSNonePrivate TLSMode = "none_private"      // plain http on a private or link-local address
)

// Attestation keys users may set on a provider.
const (
	AttestBillingEnabled = "billing_enabled" // Google project has an active billing account (Gemini private route)
	AttestLANAttested    = "lan_attested"    // user accepts plain http on a private network for a private route
)

// Provider is a registered model provider. The API key is never held here: KeyRef names a secret in the SecretStore.
type Provider struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	Kind                 Kind            `json:"kind"`
	BaseURL              string          `json:"baseUrl"`
	KeyRef               string          `json:"keyRef"`
	DataClass            DataClass       `json:"dataClass"`
	Tier                 Tier            `json:"tier"`
	Flavor               Flavor          `json:"flavor"`
	Enabled              bool            `json:"enabled"`
	MaxConcurrent        int             `json:"maxConcurrent"` // 0 = unlimited
	HourlyCost           float64         `json:"hourlyCost"`
	TLSMode              TLSMode         `json:"tlsMode"`
	TLSFingerprint       string          `json:"tlsFingerprint,omitempty"`
	LANUnencrypted       bool            `json:"lanUnencrypted"`
	Attestations         map[string]bool `json:"attestations,omitempty"`
	WireAPIs             []string        `json:"wireApis"`
	RequestOverrides     json.RawMessage `json:"requestOverrides,omitempty"`
	ResponsesValidatedAt time.Time       `json:"responsesValidatedAt,omitempty"`
	Status               string          `json:"status"`
	LastSeen             time.Time       `json:"lastSeen,omitempty"`
	ServerInfo           json.RawMessage `json:"serverInfo,omitempty"`
	CreatedAt            time.Time       `json:"createdAt"`
	UpdatedAt            time.Time       `json:"updatedAt"`
}

// Attested reports whether the user set the named attestation.
func (p Provider) Attested(key string) bool { return p.Attestations[key] }

// PrivateEligible reports whether the provider may carry private content at all: its class is private and a plain
// http LAN host carries the user's attestation (docs/security.md s9, Q3).
func (p Provider) PrivateEligible() bool {
	if p.DataClass != DataPrivate {
		return false
	}
	return !p.LANUnencrypted || p.Attested(AttestLANAttested)
}

// Model is a model catalog row (table model_catalog).
type Model struct {
	ProviderID          string    `json:"providerId"`
	ID                  string    `json:"id"`
	ContextLength       int       `json:"contextLength"`
	MaxOutput           int       `json:"maxOutput"`
	SupportsTools       bool      `json:"supportsTools"`
	SupportsReasoning   bool      `json:"supportsReasoning"`
	DataClass           DataClass `json:"dataClass"`
	Tier                Tier      `json:"tier"`
	IsFree              bool      `json:"isFree"`
	InputPerM           float64   `json:"inputPerM"` // USD per million tokens
	OutputPerM          float64   `json:"outputPerM"`
	CacheReadPerM       float64   `json:"cacheReadPerM"`
	CacheWritePerM      float64   `json:"cacheWritePerM"`
	PriceSource         string    `json:"priceSource"` // catalog | builtin | user
	PriceUpdatedAt      time.Time `json:"priceUpdatedAt,omitempty"`
	SupportedParameters []string  `json:"supportedParameters,omitempty"`
	Source              string    `json:"source"` // listing | endpoints | user
	FetchedAt           time.Time `json:"fetchedAt,omitempty"`
	ProbeStatus         string    `json:"probeStatus,omitempty"`
	ProbeAt             time.Time `json:"probeAt,omitempty"`
}

// PriceKnown reports whether the row carries a usable price: a non-zero price, or a free model (a known zero).
func (m Model) PriceKnown() bool {
	return m.IsFree || m.InputPerM > 0 || m.OutputPerM > 0
}

// Usage is token usage for one call. Input is the total input including cached tokens (the OTel view, D12);
// Cached and CacheWrite are subsets of Input.
type Usage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	Cached     int `json:"cached"`
	CacheWrite int `json:"cacheWrite"`
	Reasoning  int `json:"reasoning,omitempty"`
}

// Total is Input plus Output.
func (u Usage) Total() int { return u.Input + u.Output }

// Health is a host's liveness state.
type Health string

const (
	HealthUnknown  Health = "unknown"
	HealthUp       Health = "up"
	HealthDegraded Health = "degraded"
	HealthDown     Health = "down"
)

// Errors.
var (
	ErrNotFound          = errors.New("providers: not found")
	ErrInvalid           = errors.New("providers: invalid provider")
	ErrInsecureTransport = errors.New("providers: plain http is only allowed on loopback, link-local or private addresses; use https")
	ErrAttestationNeeded = errors.New("providers: attestation required")
	ErrSecretNotFound    = errors.New("providers: secret not found")
	ErrNoSecretStore     = errors.New("providers: no secret store configured")
)

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

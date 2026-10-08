// Package router is the Model Router (docs/model-routing.md): a deterministic, rule-based chooser of agent, provider,
// model and effort for a run. It never calls a provider and never opens the network. Slice 1 uses StaticRouter
// (assignment from configuration); RuleRouter adds CEL policies and the cheap-first ladder (Slice 2). The ladder,
// escalation and fallback decisions are pure functions (ladder.go) with table-driven tests.
package router

import (
	"context"
	"errors"
	"time"

	"github.com/kjmaguire/drydock/internal/providers"
)

type (
	ProviderID    string
	ReservationID string
)

// RunKind is why the model is wanted.
type RunKind string

const (
	RunAgentTask RunKind = "agent_task"
	RunReview    RunKind = "review"
	RunConsult   RunKind = "consult"
	RunSummarize RunKind = "summarize"
	RunClassify  RunKind = "classify"
	RunProbe     RunKind = "probe"
)

// Role is the agent role (brief section 1).
type Role string

// Level is the project or task level (docs/model-routing.md s3).
type Level string

const (
	LevelCheapest       Level = "cheapest"
	LevelFast           Level = "fast"
	LevelBalanced       Level = "balanced"
	LevelQuality        Level = "quality"
	LevelMaximumQuality Level = "maximum_quality"
	LevelCustom         Level = "custom"
)

// Effort is the abstract reasoning effort; adapters map it (docs/model-routing.md s9).
type Effort string

const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortMax    Effort = "max"
)

// ContextClass is the highest sensitivity in the context package.
type ContextClass string

const (
	ContextPrivate    ContextClass = "private"
	ContextPublicDocs ContextClass = "public_docs"
)

// DataClass aliases the provider data class.
type DataClass = providers.DataClass

// Cause explains why Next is being asked for another decision.
type Cause string

const (
	CauseQuotaExhausted   Cause = "quota_exhausted"
	CauseProviderDown     Cause = "provider_down"
	CauseOverloaded       Cause = "overloaded"
	CauseModelUnavailable Cause = "model_unavailable"
	CauseCheapStuck       Cause = "cheap_stuck"      // cheap iterations without passing checks
	CausePremiumRevise    Cause = "premium_revise"   // premium reviewer returned REVISE repeatedly
	CauseWatchdogStage2   Cause = "watchdog_stage2"  // second watchdog trip
	CausePremiumResolved  Cause = "premium_resolved" // the premium resolution finished; the next unit starts cheap again
	CauseBudgetDownshift  Cause = "budget_downshift"
)

// FailureCounts are the per-task counters that drive escalation.
type FailureCounts struct {
	CheapIterations      int
	PremiumRevise        int
	WatchdogStage1       int
	WatchdogStage2       int
	SameSignatureRepeats int
}

// Pin is an explicit user instruction (an @mention, a per-task pin, a slash command).
type Pin struct {
	Provider ProviderID
	Model    string
	Effort   Effort
	Agent    string
	Tier     providers.Tier
}

// BudgetState is the remaining budget for the narrowest scope in play.
type BudgetState struct {
	Scope        string
	PctUsed      float64
	RemainingUSD float64
	HardLimit    bool
}

// SessionState is the current session, for the switch-cost estimate.
type SessionState struct {
	Provider      ProviderID
	Model         string
	ContextTokens int
	CacheAge      time.Duration
}

// WorkerRef names the worker model of a task, so a review can prefer a different model (brief section 12).
type WorkerRef struct {
	Provider ProviderID
	Model    string
}

// RouteRequest is the input to Decide. The first block is exactly docs/model-routing.md s1; the second block holds the
// further inputs the CEL variable table of s7 needs (ambiguity, files touched, ...).
type RouteRequest struct {
	RunKind    RunKind
	TaskID     string
	RunID      string
	NodeID     string
	Attempt    int
	Role       Role
	TaskType   string
	Complexity float64
	Risk       bool
	DiffLines  int
	Failures   FailureCounts
	Level      Level
	Explicit   *Pin
	Policies   []Rule
	Budget     BudgetState
	// ContextCeiling is the highest sensitivity in the context package: private (the default when unsure) or public_docs.
	ContextCeiling ContextClass
	Session        *SessionState
	Worker         *WorkerRef

	// Additional inputs for the CEL variables (docs/model-routing.md s7 table).
	Ambiguity     float64
	FilesTouched  int
	ChecksPassing bool
	Workflow      string
	Template      string
	Iteration     int
	MaxIterations int
	ContextTokens int
	HasUntrusted  bool
	ProjectID     string
	AgentID       string
	// EstOutputTokens is the expected output size for the cost estimate (default 2000).
	EstOutputTokens int
}

// AgentRef is the agent adapter and instance a decision is for.
type AgentRef struct {
	Adapter  string
	Instance string
}

// TokenEstimate is an estimate (labeled so), never a reported figure.
type TokenEstimate struct {
	Input  int
	Output int
	Cached int
}

// PriorRef points at the decision an escalation or de-escalation came from.
type PriorRef struct {
	DecisionID string
	Provider   ProviderID
	Model      string
	Tier       providers.Tier
}

// Alternative is a rejected candidate with the rule or filter that rejected it.
type Alternative struct {
	Provider ProviderID
	Model    string
	Tier     providers.Tier
	Rejected string // the rule id or filter name
	Detail   string
}

// DecisionFacts are the inputs Explain needs to render "Why this model?" from the record alone.
type DecisionFacts struct {
	Level             Level
	RunKind           RunKind
	TaskType          string
	DiffLines         int
	Risk              bool
	Attempt           int
	ContextCeiling    ContextClass
	Tier              providers.Tier
	DataClass         DataClass
	ExcludedTrainable int
	QuotaRemainingPct float64 // -1 unknown
	BudgetPctUsed     float64
	BudgetDownshift   bool
	ProbePassed       bool
	PriceUpdatedAt    time.Time
	NextEscalation    string // "escalates to premium after 1 more failed iteration (3 cheap iterations) with a handoff artifact"
	FallbackFrom      *Alternative
}

// RoutingDecision is the record of one routing choice (docs/model-routing.md s1).
type RoutingDecision struct {
	ID              string
	Agent           AgentRef
	Provider        ProviderID
	Model           string
	Effort          Effort
	EffortApplied   bool // false when the adapter has no effort control (recorded, not hidden)
	TeamSize        int
	Reason          string
	Confidence      float64
	EstTokens       TokenEstimate
	EstCostUSD      *float64 // nil when the price is unknown; never silently zero
	PoliciesApplied []string // rule ids, invariants first
	EscalatedFrom   *PriorRef
	DeescalatedFrom *PriorRef
	Alternatives    []Alternative
	// Reservation is empty: the llm.Wrapper reserves quota at call time against the ledger. The router only checks
	// (QuotaLedger.Check) so a decision never holds a slot that a crash could leak.
	Reservation      ReservationID
	DecidedBy        string // static | rules | explicit | llm
	SwitchCostTokens int
	Created          time.Time
	Facts            DecisionFacts
}

// ModelRouter is the router contract (docs/model-routing.md s1).
type ModelRouter interface {
	Decide(ctx context.Context, req RouteRequest) (RoutingDecision, error)
	Next(ctx context.Context, prev RoutingDecision, cause Cause) (RoutingDecision, error) // escalate | deescalate | fallback
	Explain(d RoutingDecision) string                                                     // "Why this model?"
}

// Errors.
var (
	// ErrBlocked means no candidate satisfies the invariants or the pins; the Reason names why. The router never
	// substitutes silently.
	ErrBlocked  = errors.New("router: blocked")
	ErrNotFound = errors.New("router: not found")
)

// BlockedError is ErrBlocked with the reason and the rejected candidates.
type BlockedError struct {
	Reason       string
	Alternatives []Alternative
}

func (e *BlockedError) Error() string   { return "router: blocked: " + e.Reason }
func (e *BlockedError) Is(t error) bool { return t == ErrBlocked }

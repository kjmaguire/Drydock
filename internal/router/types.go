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

	// The turn kinds of an agent beyond its task work and review (decision P63): every one is routed on the agent's own
	// list of models like a work turn, and a rule can match each with run.kind.
	RunRoomReply      RunKind = "room_reply"      // an agent's reply in a channel, a task channel or a DM
	RunSpec           RunKind = "spec"            // the pm writes (or revises) the spec of a template run
	RunTests          RunKind = "tests"           // the test writer writes the tests first (or the reproducing test of a bug)
	RunDesign         RunKind = "design"          // the architect writes the design notes
	RunSecurityReview RunKind = "security_review" // the security reviewer reviews the change

	// The team lead's turns and the recommendation turns (decision P112, the turns of P52): read-only turns of an agent of
	// the team, routed on that agent's list like every other turn.
	RunLead      RunKind = "lead"      // the team lead decides about a task (stuck, unassigned, a split asked for) or the standup
	RunRecommend RunKind = "recommend" // an agent recommends which agent gets which skill, MCP server or integration
)

// WritesFiles reports whether a turn of this kind changes the worktree, so its model must call tools.
func (k RunKind) WritesFiles() bool { return k == RunAgentTask || k == RunTests }

// TurnSignal is what the router is told about an agent turn of one kind (P63): the task class the rules match
// (task.type), how heavy the turn usually is (task.complexity, task.ambiguity), and the words people read. Work and review
// turns carry no class (they keep the signals they had); the other kinds get one so the shipped rules treat each sensibly.
type TurnSignal struct {
	TaskType   string
	Complexity float64
	Ambiguity  float64
	Label      string // the turn kind for people ("room reply")
	Why        string // why the router is told this
}

var turnSignals = map[RunKind]TurnSignal{
	RunAgentTask: {Label: "work turn"},
	RunReview:    {Label: "review"},
	RunRoomReply: {TaskType: "room_reply", Complexity: 0.2, Label: "room reply",
		Why: "a reply in a channel or a DM is usually light: the ladder tries the cheap tier first, and no escalation counters apply (each reply stands alone)"},
	RunSpec: {TaskType: "spec", Complexity: 0.6, Ambiguity: 0.7, Label: "spec (pm)",
		Why: "the spec settles an ambiguous request for every turn after it: ambiguity 0.7, which the shipped premium-only rule sends to the premium tier"},
	RunTests: {TaskType: "tests", Complexity: 0.4, Label: "tests first",
		Why: "writing the tests is work: cheap first, and escalation to premium after cheap test turns came back, as for work turns"},
	RunDesign: {TaskType: "architecture", Complexity: 0.7, Ambiguity: 0.5, Label: "design (architect)",
		Why: "architecture is premium-only in the shipped rules"},
	RunSecurityReview: {TaskType: "security_review", Complexity: 0.6, Label: "security review",
		Why: "a security review is premium-only in the shipped rules and, like a review, prefers a model other than the worker's"},
	RunLead: {TaskType: "lead", Complexity: 0.5, Ambiguity: 0.4, Label: "team lead",
		Why: "the lead reads the task and the team and answers with one decision (reassign, split, steer or ask a person); it changes no files: the ladder tries the cheap tier first, and no escalation counters apply (each decision stands alone)"},
	RunRecommend: {TaskType: "recommendation", Complexity: 0.3, Label: "recommendation",
		Why: "a recommendation reads the project and lists who gets which skill, server or integration; a person applies each item: the ladder tries the cheap tier first, and no escalation counters apply"},
}

// IsAgentTurn reports whether a run kind is a turn of an agent (the kinds TurnSignals knows), not one of Drydock's own calls.
func IsAgentTurn(k RunKind) bool {
	_, ok := turnSignals[k]
	return ok
}

// TurnSignals returns the signals of a turn kind; false for a kind that is not an agent turn (consult, summarize, ...).
func TurnSignals(k RunKind) (TurnSignal, bool) {
	s, ok := turnSignals[k]
	return s, ok
}

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

// Where a turn's level came from (decision P118): the project's own level, or the workspace's when the project sets none.
const (
	LevelFromProject   = "project"
	LevelFromWorkspace = "workspace"
)

// Levels are the levels a person can choose, in the order the pages list them.
var Levels = []Level{LevelCheapest, LevelFast, LevelBalanced, LevelQuality, LevelMaximumQuality, LevelCustom}

// LevelWords is a level as the pages name it ("Maximum Quality").
func LevelWords(l Level) string {
	switch l {
	case LevelCheapest:
		return "Cheapest"
	case LevelFast:
		return "Fast"
	case LevelBalanced:
		return "Balanced"
	case LevelQuality:
		return "Quality"
	case LevelMaximumQuality:
		return "Maximum Quality"
	case LevelCustom:
		return "Custom"
	}
	return string(l)
}

// ValidLevel reports whether l is a level.
func ValidLevel(l Level) bool {
	for _, x := range Levels {
		if x == l {
			return true
		}
	}
	return false
}

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
	CauseRateLimited      Cause = "rate_limited"
	CauseProviderDown     Cause = "provider_down"
	CauseOverloaded       Cause = "overloaded"
	CauseModelUnavailable Cause = "model_unavailable"
	// CauseRetentionRequired: the provider refused the model because the organization has no data retention enabled
	// (Anthropic's Covered Models); the model is left out until a person clears it (decision P112).
	CauseRetentionRequired Cause = "retention_required"
	// CauseWallTime: Drydock stopped the turn at its wall-time ceiling (decision P112). It moves along the fallback chain only
	// when the turn did no work (a model that hung without a tool call).
	CauseWallTime        Cause = "wall_time"
	CauseCheapStuck      Cause = "cheap_stuck"      // cheap iterations without passing checks
	CausePremiumRevise   Cause = "premium_revise"   // premium reviewer returned REVISE repeatedly
	CauseWatchdogStage2  Cause = "watchdog_stage2"  // second watchdog trip
	CausePremiumResolved Cause = "premium_resolved" // the premium resolution finished; the next unit starts cheap again
	CauseBudgetDownshift Cause = "budget_downshift"
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
	// LevelSource says where Level came from (LevelFromProject, LevelFromWorkspace); "" when the caller did not say.
	LevelSource string
	Explicit    *Pin
	Policies    []Rule
	Budget      BudgetState
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
	TaskID            string
	Level             Level
	LevelSource       string `json:",omitempty"` // where the level came from: the project's own or the workspace's (P118)
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
	BudgetScope       string  // "" when no budget applies
	BudgetPctUsed     float64
	BudgetDownshift   bool
	ProbePassed       bool
	PriceUpdatedAt    time.Time
	NextEscalation    string // "escalates to premium after 1 more failed iteration (3 cheap iterations) with a handoff artifact"
	FallbackFrom      *Alternative
	// Tiebreak is the LLM tiebreak of this decision, when the ranking left a tie and the tiebreak is on (tiebreak.go).
	Tiebreak *TiebreakRecord `json:",omitempty"`
	// Stopped says why the turn this decision ran was stopped by Drydock (a wall-time ceiling), written when it happens.
	Stopped string `json:",omitempty"`
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
	// Handoff is true when the decision requires a handoff artifact: an escalation (a level threshold, an escalation rule, or
	// Next with an escalate cause). The turn that runs on a different model than the previous one then starts a fresh session
	// seeded with the artifact (internal/pipeline, docs/model-routing.md s8), never the transcript.
	Handoff bool
	// FallbackChain is the models this turn moves to, in order, if the chosen one is unavailable (a rule's fallback action,
	// FallbackRule); empty when no rule sets one. A fallback decision carries the chain of the turn's first decision and
	// FallbackHop, its hop number (1 for the first fallback). See fallback.go.
	FallbackChain []string
	FallbackRule  string
	FallbackHop   int
	Alternatives  []Alternative
	// Reservation is empty: the llm.Wrapper reserves quota at call time against the ledger. The router only checks
	// (QuotaLedger.Check) so a decision never holds a slot that a crash could leak.
	Reservation      ReservationID
	DecidedBy        string // static | rules | explicit | llm (the rules ranked, an LLM tiebreak chose between tied candidates)
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
	// Budget is set when the block is a budget at its hard limit: the turn must not run on any model until a human answers (a
	// caller that falls back to the agent's configured model on other blocks must not do so for this one; audit 2026-10-16, M4).
	Budget bool
}

func (e *BlockedError) Error() string   { return "router: blocked: " + e.Reason }
func (e *BlockedError) Is(t error) bool { return t == ErrBlocked }

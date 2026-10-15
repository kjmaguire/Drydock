package router

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"cel.dev/cel-go/cel"

	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Action is the declarative effect of a rule (CEL does not compute actions, which keeps rules auditable;
// docs/model-routing.md s7).
type Action struct {
	PreferTier        providers.Tier `json:"prefer_tier,omitempty"`
	RequireTier       providers.Tier `json:"require_tier,omitempty"`
	RequireCapability []string       `json:"require_capability,omitempty"` // "tool_calls"
	Deny              bool           `json:"deny,omitempty"`
	EscalateTo        providers.Tier `json:"escalate_to,omitempty"`
	Handoff           bool           `json:"handoff,omitempty"`
	SetEffort         Effort         `json:"set_effort,omitempty"`
	SetTeamSize       int            `json:"set_team_size,omitempty"`
	Penalty           float64        `json:"penalty,omitempty"`
	// Fallback is the chain of models a turn moves to, in order, when the chosen model or its provider is unavailable
	// (rate-limited, overloaded, down, model not found): equivalent or cheaper only, at most MaxFallbackHops per turn
	// (fallback.go, docs/model-routing.md s8).
	Fallback []string `json:"fallback,omitempty"`
}

// Validate checks the enumerated fields.
func (a Action) Validate() error {
	okTier := func(t providers.Tier) bool {
		return t == "" || t == providers.TierPremium || t == providers.TierCheap || t == providers.TierFree
	}
	if !okTier(a.PreferTier) || !okTier(a.RequireTier) || !okTier(a.EscalateTo) {
		return errors.New("tier must be premium, cheap or free")
	}
	switch a.SetEffort {
	case "", EffortLow, EffortMedium, EffortHigh, EffortMax:
	default:
		return fmt.Errorf("unknown effort %q", a.SetEffort)
	}
	if a.SetTeamSize < 0 || a.SetTeamSize > 4 {
		return errors.New("set_team_size must be between 1 and 4")
	}
	if a.Penalty < 0 {
		return errors.New("penalty must be >= 0")
	}
	for _, c := range a.RequireCapability {
		if c != "tool_calls" {
			return fmt.Errorf("unknown capability %q", c)
		}
	}
	return validateChain(a.Fallback)
}

// Rule is a stored routing policy. When is CEL source that must type-check to bool against the environment of
// docs/model-routing.md s7. Lower Priority numbers run first.
type Rule struct {
	ID       string `json:"id"`
	Scope    string `json:"scope"` // workspace | project:<id>
	Priority int    `json:"priority"`
	When     string `json:"when"`
	Then     Action `json:"then"`
	Enabled  bool   `json:"enabled"`
	Version  int    `json:"version"`
}

// DefaultCostLimit bounds one rule evaluation (CEL cost units). The cel-go cost accounting was only compiled in
// Phase 0, never exercised; rules_test.go proves the limit trips.
const DefaultCostLimit = 100_000

// MaxRuleSource is the longest accepted CEL source.
const MaxRuleSource = 2048

// Vars are the CEL variables of one evaluation (docs/model-routing.md s7).
type Vars struct {
	Task      TaskVars
	Run       RunVars
	Ctx       CtxVars
	Budget    BudgetVars
	Level     string
	Candidate CandidateVars
	Now       time.Time
	// QuotaResetsInMinutes is minutes until the candidate's tightest quota window resets (0 when none).
	QuotaResetsInMinutes int
}

type TaskVars struct {
	Type                 string
	Role                 string
	Complexity           float64
	Ambiguity            float64
	Risk                 bool
	DiffLines            int
	FilesTouched         int
	Attempt              int
	CheapIterations      int
	PremiumReviseCount   int
	WatchdogStage1       int
	WatchdogStage2       int
	SameSignatureRepeats int
	ChecksPassing        bool
	PinnedTier           string
	WorkerModel          string
}

type RunVars struct {
	Kind          string
	Workflow      string
	Template      string
	Iteration     int
	MaxIterations int
}

type CtxVars struct {
	Ceiling      string
	Tokens       int
	HasUntrusted bool
}

type BudgetVars struct {
	Scope        string
	PctUsed      float64
	RemainingUSD float64
	HardLimit    bool
}

// CandidateVars describe the candidate being judged. They are the candidate.* CEL variables.
type CandidateVars struct {
	Provider          string
	Model             string
	Tier              string
	DataClass         string
	Kind              string
	SupportsTools     bool
	ResponsesVerified bool
	EstCostUSD        float64 // -1 when the price is unknown
	QuotaRemainingPct float64 // -1 when unknown
	Healthy           bool
	Concurrent        int
	MaxConcurrent     int
	ProbePassed       bool
	Context           int
	AgentNative       bool
}

func (v Vars) activation() map[string]any {
	return map[string]any{
		"task.type": v.Task.Type, "task.role": v.Task.Role, "task.complexity": v.Task.Complexity, "task.ambiguity": v.Task.Ambiguity,
		"task.risk": v.Task.Risk, "task.diff_lines": int64(v.Task.DiffLines), "task.files_touched": int64(v.Task.FilesTouched),
		"task.attempt": int64(v.Task.Attempt), "task.cheap_iterations": int64(v.Task.CheapIterations),
		"task.premium_revise_count": int64(v.Task.PremiumReviseCount), "task.watchdog_stage1": int64(v.Task.WatchdogStage1),
		"task.watchdog_stage2": int64(v.Task.WatchdogStage2), "task.same_signature_repeats": int64(v.Task.SameSignatureRepeats),
		"task.checks_passing": v.Task.ChecksPassing, "task.pinned_tier": v.Task.PinnedTier, "task.worker_model": v.Task.WorkerModel,
		"run.kind": v.Run.Kind, "run.workflow": v.Run.Workflow, "run.template": v.Run.Template,
		"run.iteration": int64(v.Run.Iteration), "run.max_iterations": int64(v.Run.MaxIterations),
		"ctx.ceiling": v.Ctx.Ceiling, "ctx.tokens": int64(v.Ctx.Tokens), "ctx.has_untrusted": v.Ctx.HasUntrusted,
		"budget.scope": v.Budget.Scope, "budget.pct_used": v.Budget.PctUsed, "budget.remaining_usd": v.Budget.RemainingUSD, "budget.hard_limit": v.Budget.HardLimit,
		"level":              v.Level,
		"candidate.provider": v.Candidate.Provider, "candidate.model": v.Candidate.Model, "candidate.tier": v.Candidate.Tier,
		"candidate.data_class": v.Candidate.DataClass, "candidate.kind": v.Candidate.Kind, "candidate.supports_tools": v.Candidate.SupportsTools,
		"candidate.responses_validated": v.Candidate.ResponsesVerified, "candidate.est_cost_usd": v.Candidate.EstCostUSD,
		"candidate.quota_remaining_pct": v.Candidate.QuotaRemainingPct, "candidate.healthy": v.Candidate.Healthy,
		"candidate.concurrent": int64(v.Candidate.Concurrent), "candidate.max_concurrent": int64(v.Candidate.MaxConcurrent),
		"candidate.probe_passed": v.Candidate.ProbePassed, "candidate.context": int64(v.Candidate.Context), "candidate.agent_native": v.Candidate.AgentNative,
		"time.now": v.Now, "time.quota_resets_in_minutes": int64(v.QuotaResetsInMinutes),
	}
}

// Engine compiles and evaluates rules against the typed environment. The environment declares every variable of the
// s7 table with its type, so a misspelled field or a wrong type is a compile error at save time.
type Engine struct {
	env *cel.Env
}

// NewEngine builds the CEL environment (cel.dev/cel-go, the old github.com/google/cel-go path fails, GP s10).
func NewEngine() (*Engine, error) {
	S, I, D, B := cel.StringType, cel.IntType, cel.DoubleType, cel.BoolType
	decl := func(t *cel.Type, names ...string) []cel.EnvOption {
		var out []cel.EnvOption
		for _, n := range names {
			out = append(out, cel.Variable(n, t))
		}
		return out
	}
	var opts []cel.EnvOption
	opts = append(opts, decl(S, "task.type", "task.role", "task.pinned_tier", "task.worker_model", "run.kind", "run.workflow", "run.template",
		"ctx.ceiling", "budget.scope", "level", "candidate.provider", "candidate.model", "candidate.tier", "candidate.data_class", "candidate.kind")...)
	opts = append(opts, decl(D, "task.complexity", "task.ambiguity", "budget.pct_used", "budget.remaining_usd", "candidate.est_cost_usd", "candidate.quota_remaining_pct")...)
	opts = append(opts, decl(B, "task.risk", "task.checks_passing", "ctx.has_untrusted", "budget.hard_limit", "candidate.supports_tools",
		"candidate.responses_validated", "candidate.healthy", "candidate.probe_passed", "candidate.agent_native")...)
	opts = append(opts, decl(I, "task.diff_lines", "task.files_touched", "task.attempt", "task.cheap_iterations", "task.premium_revise_count",
		"task.watchdog_stage1", "task.watchdog_stage2", "task.same_signature_repeats", "run.iteration", "run.max_iterations", "ctx.tokens",
		"candidate.concurrent", "candidate.max_concurrent", "candidate.context", "time.quota_resets_in_minutes")...)
	opts = append(opts, cel.Variable("time.now", cel.TimestampType))
	opts = append(opts, cel.CrossTypeNumericComparisons(true), cel.ParserExpressionSizeLimit(MaxRuleSource))
	env, err := cel.NewEnv(opts...)
	if err != nil {
		return nil, fmt.Errorf("router: cel environment: %w", err)
	}
	return &Engine{env: env}, nil
}

// Compiled is a rule ready to evaluate.
type Compiled struct {
	Rule
	prg cel.Program
	// UsesCandidate is true when the expression reads candidate.*: such a rule is evaluated per candidate, the others
	// once per request.
	UsesCandidate bool
}

// Compile type-checks r.When to bool and builds the program. Rules are compiled once, at save time.
func (e *Engine) Compile(r Rule) (*Compiled, error) {
	if strings.TrimSpace(r.ID) == "" {
		return nil, errors.New("router: rule needs an id")
	}
	if strings.TrimSpace(r.When) == "" || len(r.When) > MaxRuleSource {
		return nil, fmt.Errorf("router: rule %s: when must be 1..%d characters", r.ID, MaxRuleSource)
	}
	if err := r.Then.Validate(); err != nil {
		return nil, fmt.Errorf("router: rule %s: %w", r.ID, err)
	}
	ast, iss := e.env.Compile(r.When)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("router: rule %s: %w", r.ID, iss.Err())
	}
	if !ast.OutputType().IsEquivalentType(cel.BoolType) {
		return nil, fmt.Errorf("router: rule %s: when must evaluate to bool, not %s", r.ID, ast.OutputType())
	}
	prg, err := e.env.Program(ast, cel.CostLimit(DefaultCostLimit))
	if err != nil {
		return nil, fmt.Errorf("router: rule %s: %w", r.ID, err)
	}
	c := &Compiled{Rule: r, prg: prg}
	for _, ref := range ast.NativeRep().ReferenceMap() {
		if strings.HasPrefix(ref.Name, "candidate.") {
			c.UsesCandidate = true
		}
	}
	return c, nil
}

// Eval evaluates the rule. A runtime error (including the cost limit) is returned; the caller treats the rule as not
// matched and records the error.
func (c *Compiled) Eval(v Vars) (bool, error) {
	out, _, err := c.prg.Eval(v.activation())
	if err != nil {
		return false, fmt.Errorf("router: rule %s: %w", c.ID, err)
	}
	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("router: rule %s: result is %T, not bool", c.ID, out.Value())
	}
	return b, nil
}

// CompileAll compiles the enabled rules and returns them ordered by priority then id.
func (e *Engine) CompileAll(rules []Rule) ([]*Compiled, error) {
	var out []*Compiled
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		c, err := e.Compile(r)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// DefaultRules are the shipped, editable rules of docs/model-routing.md s7. The data-class invariant is code, not a rule.
func DefaultRules() []Rule {
	return []Rule{
		{ID: "cheap-first-classes", Scope: "workspace", Priority: 50, Enabled: true,
			When: `task.type in ['iteration','test_fix','boilerplate','tested_refactor','exploration','docs','first_pass_review'] && !task.risk && level != 'maximum_quality'`,
			Then: Action{PreferTier: providers.TierCheap, RequireCapability: []string{"tool_calls"}}},
		{ID: "premium-only", Scope: "workspace", Priority: 40, Enabled: true,
			When: `task.type in ['architecture','security_review'] || task.risk || task.ambiguity >= 0.7 || (task.type == 'final_review' && task.diff_lines >= 400)`,
			Then: Action{RequireTier: providers.TierPremium}},
		{ID: "no-free-for-agent-work", Scope: "workspace", Priority: 10, Enabled: true,
			When: `candidate.tier == 'free' && run.kind != 'consult'`, Then: Action{Deny: true}},
		{ID: "escalate-premium-revise", Scope: "workspace", Priority: 60, Enabled: true,
			When: `task.premium_revise_count >= 2`, Then: Action{EscalateTo: providers.TierPremium, Handoff: true}},
		{ID: "escalate-cheap-stuck", Scope: "workspace", Priority: 60, Enabled: true,
			When: `task.cheap_iterations >= 3 && !task.checks_passing`, Then: Action{EscalateTo: providers.TierPremium, Handoff: true}},
		{ID: "escalate-watchdog", Scope: "workspace", Priority: 60, Enabled: true,
			When: `task.watchdog_stage2 >= 1`, Then: Action{EscalateTo: providers.TierPremium, Handoff: true}},
		{ID: "reviewer-differs", Scope: "workspace", Priority: 70, Enabled: true,
			When: `run.kind in ['review','security_review'] && candidate.model == task.worker_model`, Then: Action{Penalty: 1.0}},
		{ID: "budget-downshift", Scope: "workspace", Priority: 80, Enabled: true,
			When: `budget.pct_used >= 0.8 && level != 'maximum_quality' && !task.risk`, Then: Action{PreferTier: providers.TierCheap}},
		{ID: "quota-avoid-low", Scope: "workspace", Priority: 90, Enabled: true,
			When: `candidate.quota_remaining_pct >= 0 && candidate.quota_remaining_pct < 0.10`, Then: Action{Penalty: 0.5}},
		{ID: "security-high-effort", Scope: "workspace", Priority: 90, Enabled: true,
			When: `task.role == 'Security' && candidate.tier == 'premium'`, Then: Action{SetEffort: EffortHigh}},
	}
}

// shippedWhenUpgrades are shipped rules whose condition changed after it was first shipped: the old text, by rule id. A
// stored copy whose condition is still exactly the old text (the person never edited it) is brought up to date by
// UpgradeShipped; an edited condition is the person's and is left alone.
var shippedWhenUpgrades = map[string]string{
	// P63: the security reviewer's turn is routed too and, like a review, prefers a model other than the worker's
	"reviewer-differs": `run.kind == 'review' && candidate.model == task.worker_model`,
}

// UpgradeShipped brings the stored copies of shipped rules up to date where the person did not edit the changed condition
// (shippedWhenUpgrades). It returns the ids it updated. A rule the person deleted stays deleted.
func (s *RuleStore) UpgradeShipped(ctx context.Context) ([]string, error) {
	have, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	cur := map[string]Rule{}
	for _, r := range DefaultRules() {
		cur[r.ID] = r
	}
	var done []string
	for _, r := range have {
		old, ok := shippedWhenUpgrades[r.ID]
		if !ok || r.When != old || cur[r.ID].When == old {
			continue
		}
		r.When = cur[r.ID].When
		if _, err := s.Put(ctx, r); err != nil {
			if errors.Is(err, ErrVersionConflict) {
				continue // the person is editing it right now: theirs wins
			}
			return done, err
		}
		done = append(done, r.ID)
	}
	return done, nil
}

// ErrVersionConflict is returned by RuleStore.Put when the stored version moved (compare-and-swap).
var ErrVersionConflict = errors.New("router: rule version conflict")

// RuleStore persists rules in routing_rules.
type RuleStore struct {
	db *storage.DB
	e  *Engine
}

// NewRuleStore builds a store; rules are compiled by e before they are saved.
func NewRuleStore(db *storage.DB, e *Engine) *RuleStore { return &RuleStore{db: db, e: e} }

// Put validates and saves a rule. A rule that does not compile, or whose when is not a bool, is rejected. Version 0
// inserts a new rule (version 1); otherwise Version must equal the stored version and is incremented.
func (s *RuleStore) Put(ctx context.Context, r Rule) (Rule, error) {
	if r.Scope == "" {
		r.Scope = "workspace"
	}
	if _, err := s.e.Compile(r); err != nil {
		return Rule{}, err
	}
	then, _ := json.Marshal(r.Then)
	now := storage.Now()
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if r.Version == 0 {
			if _, err := tx.Exec(`INSERT INTO routing_rules(id,scope,priority,when_expr,then_json,enabled,version,created_at,updated_at) VALUES(?,?,?,?,?,?,1,?,?)`,
				r.ID, r.Scope, r.Priority, r.When, string(then), b2i(r.Enabled), now, now); err != nil {
				return err
			}
			r.Version = 1
			return nil
		}
		res, err := tx.Exec(`UPDATE routing_rules SET scope=?,priority=?,when_expr=?,then_json=?,enabled=?,version=version+1,updated_at=? WHERE id=? AND version=?`,
			r.Scope, r.Priority, r.When, string(then), b2i(r.Enabled), now, r.ID, r.Version)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrVersionConflict
		}
		r.Version++
		return nil
	})
	return r, err
}

// List returns stored rules for the scopes (all when none given), by priority.
func (s *RuleStore) List(ctx context.Context, scopes ...string) ([]Rule, error) {
	q := `SELECT id,scope,priority,when_expr,then_json,enabled,version FROM routing_rules`
	var args []any
	if len(scopes) > 0 {
		q += ` WHERE scope IN (?` + strings.Repeat(",?", len(scopes)-1) + `)`
		for _, sc := range scopes {
			args = append(args, sc)
		}
	}
	q += ` ORDER BY priority, id`
	rows, err := s.db.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		var r Rule
		var then string
		var en int
		if err := rows.Scan(&r.ID, &r.Scope, &r.Priority, &r.When, &then, &en, &r.Version); err != nil {
			return nil, err
		}
		r.Enabled = en != 0
		if err := json.Unmarshal([]byte(then), &r.Then); err != nil {
			return nil, fmt.Errorf("router: rule %s: stored action is unreadable: %w", r.ID, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Delete removes a rule.
func (s *RuleStore) Delete(ctx context.Context, id string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM routing_rules WHERE id=?`, id)
		return err
	})
}

// SeedDefaults inserts the default rules that are not stored yet (existing, possibly edited, rules are left alone).
func (s *RuleStore) SeedDefaults(ctx context.Context) error {
	have, err := s.List(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, r := range have {
		seen[r.ID] = true
	}
	for _, r := range DefaultRules() {
		if !seen[r.ID] {
			if _, err := s.Put(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

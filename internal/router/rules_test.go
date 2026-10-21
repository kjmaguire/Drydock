package router

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/storage"
)

func newEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := NewEngine()
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func baseVars() Vars {
	return Vars{
		Task:      TaskVars{Type: "test_fix", Role: "Backend", Complexity: 0.3, Ambiguity: 0.1, DiffLines: 38, Attempt: 2, CheapIterations: 2},
		Run:       RunVars{Kind: "agent_task", Workflow: "iterate", Template: "feature", Iteration: 2, MaxIterations: 5},
		Ctx:       CtxVars{Ceiling: "private", Tokens: 14000},
		Budget:    BudgetVars{Scope: "project", PctUsed: 0.34, RemainingUSD: 12},
		Level:     "balanced",
		Candidate: CandidateVars{Provider: "or", Model: "m", Tier: "cheap", DataClass: "private", Kind: "aggregator", SupportsTools: true, EstCostUSD: 0.004, QuotaRemainingPct: 0.71, Healthy: true, Context: 128000},
		Now:       time.Date(2026, 10, 8, 23, 30, 0, 0, time.UTC),
	}
}

func TestDefaultRulesCompileAgainstTheTypedEnvironment(t *testing.T) {
	e := newEngine(t)
	cs, err := e.CompileAll(DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != len(DefaultRules()) {
		t.Fatalf("%d compiled", len(cs))
	}
	// Priority order, ties by id.
	for i := 1; i < len(cs); i++ {
		if cs[i-1].Priority > cs[i].Priority || (cs[i-1].Priority == cs[i].Priority && cs[i-1].ID > cs[i].ID) {
			t.Fatalf("order: %s(%d) before %s(%d)", cs[i-1].ID, cs[i-1].Priority, cs[i].ID, cs[i].Priority)
		}
	}
	uses := map[string]bool{}
	for _, c := range cs {
		uses[c.ID] = c.UsesCandidate
	}
	for id, want := range map[string]bool{"cheap-first-classes": false, "premium-only": false, "no-free-for-agent-work": true,
		"reviewer-differs": true, "quota-avoid-low": true, "security-high-effort": true, "budget-downshift": false, "escalate-watchdog": false} {
		if uses[id] != want {
			t.Errorf("rule %s UsesCandidate = %v, want %v", id, uses[id], want)
		}
	}
}

func TestDefaultRuleEvaluationTable(t *testing.T) {
	e := newEngine(t)
	cs, _ := e.CompileAll(DefaultRules())
	byID := map[string]*Compiled{}
	for _, c := range cs {
		byID[c.ID] = c
	}
	cases := []struct {
		rule string
		mod  func(*Vars)
		want bool
	}{
		{"cheap-first-classes", func(v *Vars) {}, true},
		{"cheap-first-classes", func(v *Vars) { v.Task.Risk = true }, false},
		{"cheap-first-classes", func(v *Vars) { v.Level = "maximum_quality" }, false},
		{"cheap-first-classes", func(v *Vars) { v.Task.Type = "architecture" }, false},
		{"premium-only", func(v *Vars) {}, false},
		{"premium-only", func(v *Vars) { v.Task.Type = "security_review" }, true},
		{"premium-only", func(v *Vars) { v.Task.Risk = true }, true},
		{"premium-only", func(v *Vars) { v.Task.Ambiguity = 0.7 }, true},
		{"premium-only", func(v *Vars) { v.Task.Ambiguity = 0.69 }, false},
		{"premium-only", func(v *Vars) { v.Task.Type, v.Task.DiffLines = "final_review", 400 }, true},
		{"premium-only", func(v *Vars) { v.Task.Type, v.Task.DiffLines = "final_review", 399 }, false},
		{"no-free-for-agent-work", func(v *Vars) { v.Candidate.Tier = "free" }, true},
		{"no-free-for-agent-work", func(v *Vars) { v.Candidate.Tier = "free"; v.Run.Kind = "consult" }, false},
		{"no-free-for-agent-work", func(v *Vars) {}, false},
		{"escalate-premium-revise", func(v *Vars) { v.Task.PremiumReviseCount = 2 }, true},
		{"escalate-premium-revise", func(v *Vars) { v.Task.PremiumReviseCount = 1 }, false},
		{"escalate-cheap-stuck", func(v *Vars) { v.Task.CheapIterations = 3 }, true},
		{"escalate-cheap-stuck", func(v *Vars) { v.Task.CheapIterations = 3; v.Task.ChecksPassing = true }, false},
		{"escalate-cheap-stuck", func(v *Vars) { v.Task.CheapIterations = 2 }, false},
		{"escalate-watchdog", func(v *Vars) { v.Task.WatchdogStage2 = 1 }, true},
		{"escalate-watchdog", func(v *Vars) { v.Task.WatchdogStage1 = 3 }, false},
		{"reviewer-differs", func(v *Vars) { v.Run.Kind, v.Task.WorkerModel = "review", "m" }, true},
		{"reviewer-differs", func(v *Vars) { v.Run.Kind, v.Task.WorkerModel = "review", "other" }, false},
		{"budget-downshift", func(v *Vars) { v.Budget.PctUsed = 0.8 }, true},
		{"budget-downshift", func(v *Vars) { v.Budget.PctUsed = 0.79 }, false},
		{"budget-downshift", func(v *Vars) { v.Budget.PctUsed = 0.9; v.Task.Risk = true }, false},
		{"quota-avoid-low", func(v *Vars) { v.Candidate.QuotaRemainingPct = 0.09 }, true},
		{"quota-avoid-low", func(v *Vars) { v.Candidate.QuotaRemainingPct = -1 }, false}, // unknown is not low
		{"quota-avoid-low", func(v *Vars) { v.Candidate.QuotaRemainingPct = 0.10 }, false},
		{"security-high-effort", func(v *Vars) { v.Task.Role, v.Candidate.Tier = "Security", "premium" }, true},
		{"security-high-effort", func(v *Vars) { v.Task.Role = "Security" }, false},
	}
	for _, c := range cases {
		v := baseVars()
		c.mod(&v)
		got, err := byID[c.rule].Eval(v)
		if err != nil {
			t.Fatalf("%s: %v", c.rule, err)
		}
		if got != c.want {
			t.Errorf("%s: got %v, want %v (vars %+v)", c.rule, got, c.want, v.Task)
		}
	}
}

func TestCompileRejectsBadRules(t *testing.T) {
	e := newEngine(t)
	cases := map[string]Rule{
		"syntax error":            {ID: "r", When: `task.type ==`},
		"unknown variable":        {ID: "r", When: `task.nonsense == 1`},
		"wrong type":              {ID: "r", When: `task.risk == 'yes'`},
		"not a bool":              {ID: "r", When: `task.diff_lines + 1`},
		"unknown namespace":       {ID: "r", When: `workflow.name == 'x'`},
		"empty":                   {ID: "r", When: ``},
		"missing id":              {When: `true`},
		"bad tier in action":      {ID: "r", When: `true`, Then: Action{PreferTier: "gold"}},
		"bad effort":              {ID: "r", When: `true`, Then: Action{SetEffort: "turbo"}},
		"team size too large":     {ID: "r", When: `true`, Then: Action{SetTeamSize: 9}},
		"unknown capability":      {ID: "r", When: `true`, Then: Action{RequireCapability: []string{"telepathy"}}},
		"negative penalty":        {ID: "r", When: `true`, Then: Action{Penalty: -1}},
		"too long":                {ID: "r", When: `task.type == '` + strings.Repeat("x", MaxRuleSource) + `'`},
		"int compared to string":  {ID: "r", When: `task.attempt == 'two'`},
		"timestamp method misuse": {ID: "r", When: `time.now.nosuchmethod() > 1`},
	}
	for name, r := range cases {
		if _, err := e.Compile(r); err == nil {
			t.Errorf("%s: compiled, want an error", name)
		}
	}
	// A type error is reported with the rule id.
	if _, err := e.Compile(Rule{ID: "my-rule", When: `task.diff_lines == 'x'`}); err == nil || !strings.Contains(err.Error(), "my-rule") {
		t.Fatalf("error should name the rule: %v", err)
	}
}

func TestUserPolicyExamplesFromTheDesign(t *testing.T) {
	e := newEngine(t)
	// Docs s7 illustrative policies.
	v := baseVars()
	pin, err := e.Compile(Rule{ID: "database-pin", When: `task.role == 'Database'`})
	if err != nil {
		t.Fatal(err)
	}
	v.Task.Role = "Database"
	if ok, _ := pin.Eval(v); !ok {
		t.Fatal("role pin did not match")
	}
	afterHours, err := e.Compile(Rule{ID: "after-hours", When: `time.now.getHours() >= 22 && candidate.provider == 'openrouter'`})
	if err != nil {
		t.Fatal(err)
	}
	v.Candidate.Provider = "openrouter"
	if ok, err := afterHours.Eval(v); err != nil || !ok { // 23:30 UTC
		t.Fatalf("after-hours at 23:30: %v %v", ok, err)
	}
	v.Now = time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	if ok, _ := afterHours.Eval(v); ok {
		t.Fatal("after-hours matched at 09:00")
	}
	cap, err := e.Compile(Rule{ID: "premium-cap", When: `budget.pct_used > 0.5 && candidate.tier == 'premium' && !task.risk`, Then: Action{Deny: true}})
	if err != nil {
		t.Fatal(err)
	}
	v.Budget.PctUsed, v.Candidate.Tier = 0.6, "premium"
	if ok, _ := cap.Eval(v); !ok {
		t.Fatal("premium spend cap did not match")
	}
	// Mixed int and double comparison needs cross-type numeric comparison, which the environment enables.
	if _, err := e.Compile(Rule{ID: "mixed", When: `task.diff_lines > 150.5 && candidate.quota_remaining_pct < 1`}); err != nil {
		t.Fatalf("cross-type numeric comparison rejected: %v", err)
	}
}

func TestRuntimeCostLimitStopsAnExpensiveRule(t *testing.T) {
	e := newEngine(t)
	// A nested comprehension over a list literal: legal CEL, cost grows with the product of the list sizes.
	list := "[" + strings.TrimRight(strings.Repeat("1,2,3,4,5,6,7,8,9,10,", 30), ",") + "]"
	src := `` + list + `.all(a, ` + list + `.all(b, ` + list + `.all(c, a + b + c > 0)))`
	c, err := e.Compile(Rule{ID: "heavy", When: src})
	if err != nil {
		t.Skipf("the heavy expression did not compile within the size limit: %v", err) // the size limit may stop it first
	}
	_, err = c.Eval(baseVars())
	if err == nil || !strings.Contains(err.Error(), "cost") {
		t.Fatalf("a 27M-step rule ran to completion or failed differently: %v", err)
	}
}

func TestRuleStoreCompareAndSwapAndSeeding(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := newEngine(t)
	s := NewRuleStore(db, e)

	// Type errors are rejected at save time; nothing is stored.
	if _, err := s.Put(ctx, Rule{ID: "bad", When: `task.nope`, Enabled: true}); err == nil {
		t.Fatal("saved a rule that does not compile")
	}
	if rs, _ := s.List(ctx); len(rs) != 0 {
		t.Fatalf("stored %v", rs)
	}
	r, err := s.Put(ctx, Rule{ID: "mine", Priority: 5, When: `task.role == 'Database'`, Then: Action{RequireTier: providers.TierPremium}, Enabled: true})
	if err != nil || r.Version != 1 || r.Scope != "workspace" {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := s.Put(ctx, Rule{ID: "mine", When: `true`, Enabled: true}); err == nil {
		t.Fatal("inserting a duplicate id (version 0) must fail")
	}
	stale := r
	r.When = `task.role == 'Backend'`
	r2, err := s.Put(ctx, r)
	if err != nil || r2.Version != 2 {
		t.Fatalf("update: %v %+v", err, r2)
	}
	stale.When = `task.role == 'QA'`
	if _, err := s.Put(ctx, stale); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale write: %v", err)
	}
	got, _ := s.List(ctx)
	if len(got) != 1 || got[0].When != `task.role == 'Backend'` || got[0].Then.RequireTier != providers.TierPremium || got[0].Version != 2 {
		t.Fatalf("stored %+v", got)
	}
	if err := s.SeedDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SeedDefaults(ctx); err != nil { // idempotent, never overwrites
		t.Fatal(err)
	}
	all, _ := s.List(ctx)
	if len(all) != len(DefaultRules())+1 {
		t.Fatalf("%d rules after seeding", len(all))
	}
	if cs, err := e.CompileAll(all); err != nil || len(cs) != len(all) {
		t.Fatalf("stored rules do not recompile: %v", err)
	}
	if err := s.Delete(ctx, "mine"); err != nil {
		t.Fatal(err)
	}
}

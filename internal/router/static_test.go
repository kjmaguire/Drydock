package router

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/providers"
)

func (w *world) static(cfg StaticConfig) *StaticRouter {
	w.t.Helper()
	r, err := NewStaticRouter(cfg, StaticOptions{Source: w.src, Recorder: w.rec, Now: func() time.Time { return w.clock }})
	if err != nil {
		w.t.Fatal(err)
	}
	return r
}

func (w *world) assign(p providers.Provider, model string, eff Effort) Assignment {
	return Assignment{Agent: AgentRef{Adapter: "opencode", Instance: "oc-1"}, Provider: w.id(p), Model: model, Effort: eff}
}

func TestStaticRouterReturnsTheConfiguredAssignment(t *testing.T) {
	w := newWorld(t)
	r := w.static(StaticConfig{
		Default: w.assign(w.or, "qwen-cheap", ""),
		ByRole:  map[Role]Assignment{"Architect": w.assign(w.ant, "prem-1", EffortHigh)},
		ByAgent: map[string]Assignment{"agent_special": w.assign(w.ant, "prem-2", EffortMax)},
	})
	q := req()
	d := mustDecide(t, r, q)
	if key(d) != w.or.ID+"/qwen-cheap" || d.DecidedBy != "static" || d.Effort != EffortMedium || d.TeamSize != 1 || d.Agent.Adapter != "opencode" || !d.EffortApplied {
		t.Fatalf("default: %+v", d)
	}
	if d.EstCostUSD == nil || !near(*d.EstCostUSD, (14000*0.12+2000*0.8)/1e6) {
		t.Fatalf("estimate %v", d.EstCostUSD)
	}
	q.Role = "Architect"
	if d := mustDecide(t, r, q); d.Model != "prem-1" || d.Effort != EffortHigh {
		t.Fatalf("role: %+v", d)
	}
	q.AgentID = "agent_special" // an agent assignment outranks the role
	if d := mustDecide(t, r, q); d.Model != "prem-2" || d.Effort != EffortMax {
		t.Fatalf("agent: %+v", d)
	}
	q.Explicit = &Pin{Provider: w.id(w.or), Model: "glm-cheaper", Effort: EffortLow}
	if d := mustDecide(t, r, q); d.Model != "glm-cheaper" || d.DecidedBy != "explicit" || d.Effort != EffortLow {
		t.Fatalf("pin: %+v", d)
	}
	// Recorded from day one, with events.
	var n int
	w.db.Reader().QueryRow(`SELECT count(*) FROM routing_decisions WHERE decided_by='static'`).Scan(&n)
	if n != 3 || len(w.events("routing.decided")) != 4 {
		t.Fatalf("static rows %d, decided events %d", n, len(w.events("routing.decided")))
	}
	if txt := r.Explain(mustDecide(t, r, req())); !strings.Contains(txt, "decided by static") {
		t.Fatalf("explain: %s", txt)
	}
}

func TestStaticRouterEnforcesInvariantsWithoutSubstituting(t *testing.T) {
	w := newWorld(t)
	good := w.assign(w.ant, "prem-1", "")
	r := w.static(StaticConfig{Default: w.assign(w.gem, "gem-free", ""), Fallbacks: []Assignment{good}})
	// A trainable assignment for private context is BLOCKED; the configured fallback must not paper over it.
	_, err := r.Decide(w.ctx, req())
	var be *BlockedError
	if !errors.As(err, &be) || !strings.Contains(be.Reason, FilterDataClass) {
		t.Fatalf("err = %v", err)
	}
	if len(w.events("routing.blocked")) != 1 {
		t.Fatal("no routing.blocked event")
	}
	// Free tier for agent work is an invariant too, even with public-docs context.
	q := req()
	q.ContextCeiling = ContextPublicDocs
	if _, err := r.Decide(w.ctx, q); !errors.Is(err, ErrBlocked) {
		t.Fatalf("free tier agent work: %v", err)
	}
	// The same assignment serves a CONSULT on public documentation.
	q.RunKind = RunConsult
	if d := mustDecide(t, r, q); d.Model != "gem-free" {
		t.Fatalf("consult: %+v", d)
	}
	// A disabled provider and an unregistered one are blocked.
	w.reg.SetEnabled(w.ctx, w.ant.ID, false)
	r2 := w.static(StaticConfig{Default: good, Fallbacks: []Assignment{w.assign(w.or, "qwen-cheap", "")}})
	if _, err := r2.Decide(w.ctx, req()); !errors.Is(err, ErrBlocked) {
		t.Fatalf("disabled provider was replaced by a fallback: %v", err)
	}
	r3 := w.static(StaticConfig{Default: Assignment{Provider: "prov_nope", Model: "m"}})
	if _, err := r3.Decide(w.ctx, req()); !errors.Is(err, ErrBlocked) {
		t.Fatalf("unknown provider: %v", err)
	}
	r4 := w.static(StaticConfig{})
	if _, err := r4.Decide(w.ctx, req()); !errors.Is(err, ErrBlocked) {
		t.Fatalf("no assignment: %v", err)
	}
	// Pinned models are not replaced.
	q = req()
	q.Explicit = &Pin{Provider: w.id(w.gem), Model: "gem-free"}
	if _, err := r.Decide(w.ctx, q); !errors.Is(err, ErrBlocked) {
		t.Fatalf("pinned trainable: %v", err)
	}
}

func TestStaticRouterFallsBackWhenTheAssignmentIsUnavailable(t *testing.T) {
	w := newWorld(t)
	r := w.static(StaticConfig{
		Default:   w.assign(w.or, "qwen-cheap", ""),
		Fallbacks: []Assignment{w.assign(w.or, "glm-cheaper", ""), w.assign(w.ant, "prem-1", "")},
	})
	w.ledger.Penalize(w.ctx, w.or.ID, "qwen-cheap", w.clock.Add(time.Hour), "retry-after")
	d := mustDecide(t, r, req())
	if d.Model != "glm-cheaper" || !strings.Contains(d.Reason, "unavailable") || len(d.Alternatives) != 1 || d.Alternatives[0].Rejected != FilterQuota {
		t.Fatalf("first fallback: %+v", d)
	}
	w.ledger.Penalize(w.ctx, w.or.ID, "", w.clock.Add(time.Hour), "overloaded")
	if d := mustDecide(t, r, req()); d.Model != "prem-1" {
		t.Fatalf("second fallback: %+v", d)
	}
	w.ledger.Penalize(w.ctx, w.ant.ID, "", w.clock.Add(time.Hour), "overloaded")
	if _, err := r.Decide(w.ctx, req()); !errors.Is(err, ErrBlocked) {
		t.Fatalf("everything unavailable: %v", err)
	}
}

func TestStaticRouterNext(t *testing.T) {
	w := newWorld(t)
	esc := w.assign(w.ant, "prem-1", EffortHigh)
	r := w.static(StaticConfig{Default: w.assign(w.or, "qwen-cheap", ""), Fallbacks: []Assignment{w.assign(w.or, "glm-cheaper", "")}, Escalation: &esc})
	d1 := mustDecide(t, r, req())
	w.ledger.Penalize(w.ctx, w.or.ID, "qwen-cheap", w.clock.Add(time.Hour), "retry-after")
	d2, err := r.Next(w.ctx, d1, CauseQuotaExhausted)
	if err != nil || d2.Model != "glm-cheaper" || d2.Facts.FallbackFrom == nil {
		t.Fatalf("fallback: %v %+v", err, d2)
	}
	if len(w.events("routing.fallback")) != 1 {
		t.Fatal("no routing.fallback event")
	}
	d3, err := r.Next(w.ctx, d1, CauseCheapStuck)
	if err != nil || d3.Model != "prem-1" || d3.Effort != EffortHigh || d3.EscalatedFrom == nil || d3.EscalatedFrom.DecisionID != d1.ID {
		t.Fatalf("escalate: %v %+v", err, d3)
	}
	// After the premium resolution the original assignment is used again (not sticky).
	w.clock = w.clock.Add(2 * time.Hour) // the penalty expired
	d4, err := r.Next(w.ctx, d3, CausePremiumResolved)
	if err != nil || d4.Model != "qwen-cheap" || d4.DeescalatedFrom == nil {
		t.Fatalf("deescalate: %v %+v", err, d4)
	}
	// Without an escalation assignment the static router says so instead of guessing.
	r2 := w.static(StaticConfig{Default: w.assign(w.or, "qwen-cheap", "")})
	e1 := mustDecide(t, r2, req())
	if _, err := r2.Next(w.ctx, e1, CauseWatchdogStage2); !errors.Is(err, ErrBlocked) {
		t.Fatalf("escalation without an assignment: %v", err)
	}
	// With no fallback configured a fallback cause cannot produce a different model.
	if _, err := r2.Next(w.ctx, e1, CauseOverloaded); !errors.Is(err, ErrBlocked) {
		t.Fatalf("fallback with nothing configured: %v", err)
	}
}

func TestStaticAssignmentToAnUnlistedModelHasUnknownPrice(t *testing.T) {
	w := newWorld(t)
	r := w.static(StaticConfig{Default: w.assign(w.ant, "claude-not-in-listing", "")})
	d := mustDecide(t, r, req())
	if d.EstCostUSD != nil {
		t.Fatalf("an unlisted model has no known price; the estimate must be nil, got %v", *d.EstCostUSD)
	}
	if txt := r.Explain(d); !strings.Contains(txt, "price unknown") {
		t.Fatalf("explain: %s", txt)
	}
}

func TestStaticRouterNeedsACatalog(t *testing.T) {
	if _, err := NewStaticRouter(StaticConfig{}, StaticOptions{}); err == nil {
		t.Fatal("static router without a catalog accepted")
	}
	if _, err := NewRuleRouter(RuleOptions{}); err == nil {
		t.Fatal("rule router without an engine accepted")
	}
}

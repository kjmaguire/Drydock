package router

import (
	"context"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/storage"
)

var t0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// world is a real SQLite database with a real registry, health monitor, quota ledger and event bus, populated with
// catalog rows (the rows a probe would have written; no network is involved).
type world struct {
	t            *testing.T
	ctx          context.Context
	db           *storage.DB
	bus          *events.SQLBus
	reg          *providers.Registry
	ledger       *providers.QuotaLedger
	mon          *providers.Monitor
	clock        time.Time
	or, ant, gem providers.Provider
	src          *RegistrySource
	rec          *Recorder
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 512)
	t.Cleanup(func() { bus.Close(); db.Close() })
	w := &world{t: t, ctx: ctx, db: db, bus: bus, clock: t0}
	w.reg = providers.NewRegistry(db, providers.RegistryOptions{Secrets: providers.NewMemorySecretStoreForTests(), Bus: bus})
	w.ledger = providers.NewQuotaLedger(w.reg, nil, bus, providers.QuotaOptions{Now: func() time.Time { return w.clock }})
	w.mon = providers.NewMonitor(w.reg, providers.NewProber(w.reg, bus), bus, providers.MonitorOptions{Ledger: w.ledger})
	w.src = &RegistrySource{Catalog: w.reg, Avail: w.mon, Quota: w.ledger}
	w.rec = &Recorder{DB: db, Bus: bus}

	mk := func(np providers.NewProvider) providers.Provider {
		p, err := w.reg.Create(ctx, np)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	w.or = mk(providers.NewProvider{Name: "openrouter", Kind: providers.KindAggregator, BaseURL: "https://openrouter.ai/api/v1", DataClass: providers.DataPrivate, Tier: providers.TierCheap})
	w.ant = mk(providers.NewProvider{Name: "anthropic", Kind: providers.KindDirect, BaseURL: "https://api.anthropic.com", DataClass: providers.DataPrivate, Tier: providers.TierPremium})
	w.gem = mk(providers.NewProvider{Name: "gemini-free", Kind: providers.KindDirect, BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", DataClass: providers.DataTrainable, Tier: providers.TierFree})

	up := func(p providers.Provider, ms ...providers.Model) {
		if err := w.reg.UpsertModels(ctx, p.ID, ms); err != nil {
			t.Fatal(err)
		}
	}
	m := func(id string, class providers.DataClass, tier providers.Tier, tools bool, in, out float64, free bool) providers.Model {
		return providers.Model{ID: id, ContextLength: 200000, SupportsTools: tools, DataClass: class, Tier: tier, InputPerM: in, OutputPerM: out, IsFree: free}
	}
	up(w.or,
		m("qwen-cheap", providers.DataPrivate, providers.TierCheap, true, 0.12, 0.8, false),
		m("glm-cheaper", providers.DataPrivate, providers.TierCheap, true, 0.06, 0.4, false),
		m("no-tools", providers.DataPrivate, providers.TierCheap, false, 0.01, 0.01, false),
		m("north-mini:free", providers.DataTrainable, providers.TierFree, true, 0, 0, true))
	up(w.ant,
		m("prem-1", providers.DataPrivate, providers.TierPremium, true, 3, 15, false),
		m("prem-2", providers.DataPrivate, providers.TierPremium, true, 5, 25, false))
	up(w.gem, m("gem-free", providers.DataTrainable, providers.TierFree, true, 0, 0, true))
	// The tool-call probe passed for qwen-cheap only (docs/model-routing.md s5).
	if err := w.reg.SetProbeStatus(ctx, w.or.ID, "qwen-cheap", "pass", t0); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *world) router(opts ...func(*RuleOptions)) *RuleRouter {
	w.t.Helper()
	eng, err := NewEngine()
	if err != nil {
		w.t.Fatal(err)
	}
	o := RuleOptions{Engine: eng, Source: w.src, Recorder: w.rec, Now: func() time.Time { return w.clock }}
	for _, f := range opts {
		f(&o)
	}
	r, err := NewRuleRouter(o)
	if err != nil {
		w.t.Fatal(err)
	}
	return r
}

func (w *world) events(typ string) []events.Event {
	w.t.Helper()
	evs, err := w.bus.Replay(w.ctx, 0, events.Filter{Types: []string{typ}}, 1000)
	if err != nil {
		w.t.Fatal(err)
	}
	return evs
}

// req is a typical cheap-first request: a failing-test fix on private context.
func req() RouteRequest {
	return RouteRequest{RunKind: RunAgentTask, TaskID: "task_42", RunID: "run_1", NodeID: "n1", Attempt: 2, Role: "Backend", TaskType: "test_fix",
		DiffLines: 38, Level: LevelBalanced, ContextCeiling: ContextPrivate, ContextTokens: 14000, EstOutputTokens: 2000,
		Budget: BudgetState{Scope: "project", PctUsed: 0.34}, AgentID: "agent_1"}
}

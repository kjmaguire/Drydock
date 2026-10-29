package core

// Slice 2, "Routing and economics": budgets, evals, routing wiring, the tool-call probe, the extra verification checks'
// settings and the local telemetry sink. Everything is added through the Extension mechanism (extensions.go); no base file
// is edited. Methods that change budgets, routing rules, check settings or telemetry are human actions: they carry
// c.Human() and the stores refuse any other identity.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/budget"
	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/evals"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/llm/backends"
	"github.com/kjmaguire/drydock/internal/llm/probe"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/router"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/security/secretadapt"
	"github.com/kjmaguire/drydock/internal/telemetry"
	"github.com/kjmaguire/drydock/internal/workflow"
)

func init() {
	RegisterExtension(Extension{Name: "economics", Start: startEconomics})
}

// economics is the state of the extension for one Core.
type economics struct {
	c        *Core
	budgets  *budget.Store
	enforcer *budget.Enforcer
	ledger   *providers.QuotaLedger
	uptime   *providers.EndpointUptime // OpenRouter endpoint uptime: which endpoints the Wrapper's calls skip (P53 c)
	wrapper  *llm.Wrapper
	rules    *router.RuleStore
	rec      *router.Recorder
	live     *router.RuleRouter // records decisions
	dry      *router.RuleRouter // route.preview: the same rules and candidates, nothing recorded
	// agentRouter routes agent turns among the models an agent's human listed (turnrouting.go); it records decisions.
	agentRouter *router.RuleRouter
	src         *router.RegistrySource
	prober      *probe.Prober
	sink        *telemetry.Sink
	outcomes    *evals.Outcomes
}

var (
	econMu     sync.Mutex
	econByCore = map[*Core]*economics{}
)

// EconomicsLLM returns the app's llm.Client (the Wrapper: data_class guard, secret scan, quota, retry, response cache and
// usage recording) built over the provider registry, or nil if the extension did not start. Other features that need
// the app's own model calls (expert-pack consults, summaries) use it so that every call is guarded and recorded the same way.
func EconomicsLLM(c *Core) llm.Client {
	econMu.Lock()
	defer econMu.Unlock()
	if e := econByCore[c]; e != nil && e.wrapper != nil {
		return e.wrapper
	}
	return nil
}

// EconomicsRouter returns the app's model router: the rule router over the provider registry, the stored policies and the
// probe results, recording every decision, with the budget state of the request's scopes filled in before it decides. A
// budget at its hard limit therefore sits above the router's heuristics (brief s6 override priority). It is nil if the
// extension did not start. Agent turns are routed separately, among each agent's own models (turnrouting.go); features that
// choose a model for the app's own calls use this one with EconomicsLLM.
func EconomicsRouter(c *Core) router.ModelRouter {
	econMu.Lock()
	defer econMu.Unlock()
	if e := econByCore[c]; e != nil {
		return budgetRouter{e}
	}
	return nil
}

// EconomicsBudgetBlock reports why an agent turn outside a workflow (a room or direct-message reply) must not start: a budget
// that applies to it (the project's, the agent's, the task's) is at its hard limit. It returns nil when the turn may start. It
// returns an error when the budgets did not start: no turn runs without them. Workflow turns are gated by the budget middleware
// instead, where a human can resume one turn.
func EconomicsBudgetBlock(ctx context.Context, c *Core, projectID, taskID, agentID string) error {
	store := c.budgets.Load()
	if store == nil {
		// no budget store means the economics extension did not start: fail closed, as the workflow gate does
		return errors.New("the budgets did not start, so the turn does not start (see the log)")
	}
	sts, err := store.StatusFor(ctx, budget.ScopeIDs{Project: projectID, Task: taskID, Agent: agentID})
	if err != nil {
		return fmt.Errorf("budgets could not be read, so the turn does not start: %w", err) // fail closed, as the middleware does
	}
	for _, st := range sts {
		if st.State == budget.StateExceeded {
			return fmt.Errorf("the %s, so agents do not answer until it is raised on the Budgets page", st.Message)
		}
	}
	return nil
}

type budgetRouter struct{ e *economics }

func (b budgetRouter) Decide(ctx context.Context, req router.RouteRequest) (router.RoutingDecision, error) {
	if err := b.e.fillBudget(ctx, &req); err != nil {
		return router.RoutingDecision{}, err
	}
	return b.e.live.Decide(ctx, req)
}

func (b budgetRouter) Next(ctx context.Context, prev router.RoutingDecision, cause router.Cause) (router.RoutingDecision, error) {
	return b.e.live.Next(ctx, prev, cause)
}

func (b budgetRouter) Explain(d router.RoutingDecision) string { return b.e.live.Explain(d) }

// fillBudget sets the request's budget state from the budget store unless the caller supplied one.
func (e *economics) fillBudget(ctx context.Context, req *router.RouteRequest) error {
	if req.Budget.Scope != "" {
		return nil
	}
	sum, err := e.budgets.SummaryFor(ctx, budget.ScopeIDs{Project: req.ProjectID, Task: req.TaskID, Run: req.RunID, Agent: req.AgentID})
	if err != nil {
		return err
	}
	if sum.Any {
		req.Budget = router.BudgetState{Scope: sum.Scope, PctUsed: sum.PctUsed, RemainingUSD: sum.RemainingUSD, HardLimit: sum.HardLimit}
	}
	return nil
}

func startEconomics(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	e := &economics{c: c}

	// --- budgets: the gate on every agent turn ---
	// The gate is installed before anything that can fail: an extension that does not start leaves no budget gate otherwise, and
	// agents then run without a limit (audit 2026-10-16, M4). If even this fails, Open installs a gate that refuses every turn.
	e.budgets = &budget.Store{DB: c.DB}
	e.enforcer = &budget.Enforcer{Store: e.budgets, Bus: c.Bus, Consume: c.Engine.MarkConsumed, Notify: e.budgetNotice}
	c.budgets.Store(e.budgets)
	// a reservation still held belongs to a turn of the process before this one (a crash, a forced quit): it holds nothing now
	if n, err := e.budgets.ExpireStale(ctx); err != nil {
		c.logf("budget reservations: %v", err)
	} else if n > 0 {
		c.logf("budget reservations: %d held by turns of the last run expired", n)
	}

	// --- telemetry (off until a human turns it on) ---
	sink, terr := telemetry.New(ctx, c.DB, telemetry.Options{Redact: c.Scanner.Redact})

	// Middleware order for agent_task nodes: Watchdog (registered by the core), then Telemetry, Budget, the decision
	// recorder. docs/workflow-engine.md s7 puts Telemetry first; the core registers the watchdog before extensions start,
	// so it stays outermost. A budget pause is still recorded as a paused span because Telemetry wraps Budget.
	mws := []workflow.Middleware{e.enforcer.Middleware(), e.routeTurn}
	if terr != nil {
		c.logf("telemetry unavailable: %v (the budget gate still runs)", terr)
	} else {
		e.sink = sink
		mws = append([]workflow.Middleware{sink.Middleware()}, mws...)
		c.Engine.Use(workflow.KindCheck, sink.Middleware())
		c.Engine.Observe(sink.Observer())
	}
	c.Engine.Use(workflow.KindAgentTask, mws...)
	c.budgetGate.Store(true)

	// --- LLM wrapper, quota ledger, router ---
	e.ledger = providers.NewQuotaLedger(c.Providers, c.Prober, c.Bus, providers.QuotaOptions{})
	e.ledger.StartPolling(ctx)
	e.uptime = providers.NewEndpointUptime(c.Providers, providers.UptimeOptions{})
	var err error
	e.wrapper, err = llm.NewWrapper(llm.Options{Registry: c.Providers, DB: c.DB, Factory: backends.Factory, Scanner: secretadapt.LLM(c.Scanner),
		Ledger: e.ledger, Monitor: c.Health, Uptime: e.uptime, Bus: c.Bus})
	if err != nil {
		return nil, nil, fmt.Errorf("llm wrapper: %w", err)
	}
	eng, err := router.NewEngine()
	if err != nil {
		return nil, nil, fmt.Errorf("router rule engine: %w", err)
	}
	e.rules = router.NewRuleStore(c.DB, eng)
	if err := e.seedDefaultRules(ctx); err != nil {
		c.logf("routing rules: %v", err)
	}
	if up, err := e.rules.UpgradeShipped(ctx); err != nil {
		c.logf("routing rules: upgrade: %v", err)
	} else if len(up) > 0 {
		c.logf("routing rules: brought the shipped rules %v up to date (their conditions were not edited)", up)
	}
	src := &router.RegistrySource{Catalog: c.Providers, Avail: c.Health, Quota: e.ledger, Retention: c.Providers}
	e.src = src
	e.rec = &router.Recorder{DB: c.DB, Bus: c.Bus}
	if e.live, err = router.NewRuleRouter(router.RuleOptions{Engine: eng, Source: src, Store: e.rules, Recorder: e.rec, Tiebreaker: tiebreaker{e}}); err != nil {
		return nil, nil, err
	}
	if e.agentRouter, err = router.NewRuleRouter(router.RuleOptions{Engine: eng, Source: agentSource{e}, Store: e.rules, Recorder: e.rec, Tiebreaker: tiebreaker{e}}); err != nil {
		return nil, nil, err
	}
	if e.dry, err = router.NewRuleRouter(router.RuleOptions{Engine: eng, Source: src, Store: e.rules, Tiebreaker: dryTiebreaker{e}}); err != nil {
		return nil, nil, err
	}
	e.prober = &probe.Prober{Client: e.wrapper, Registry: c.Providers, DB: c.DB, Bus: c.Bus}

	// --- routing outcomes from day one ---
	e.outcomes = &evals.Outcomes{DB: c.DB, Bus: c.Bus, OnError: func(err error) { c.logf("routing outcome: %v", err) }}
	if err := e.outcomes.Start(ctx); err != nil {
		return nil, nil, err
	}

	// --- inconclusive checks (skipped / errored) reach the task channel as the tool member, status skipped or error ---
	safe.Go("core.startEconomics", func() { e.relayInconclusiveChecks(ctx) })

	// --- opt-in startup probe (it spends a few synthetic requests, so it is the user's switch) ---
	if v, _ := e.setting(ctx, "route.probe.startup"); v == "1" {
		safe.Go("core.startEconomics#2", func() { e.startupProbe(ctx) })
	}

	econMu.Lock()
	econByCore[c] = e
	econMu.Unlock()
	closeFn := func() {
		e.outcomes.Stop()
		e.ledger.StopPolling()
		econMu.Lock()
		delete(econByCore, c)
		econMu.Unlock()
	}
	return e.methods(), closeFn, nil
}

func (e *economics) setting(ctx context.Context, key string) (string, error) {
	var v string
	err := e.c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (e *economics) putSetting(ctx context.Context, key, value string) error {
	return e.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
		return err
	})
}

// seedDefaultRules stores the shipped rules once. Without a stored rule the router falls back to the shipped defaults, but
// the moment a user stores the first rule the defaults would disappear; seeding keeps them as editable stored copies. A
// rule the user deleted is not brought back on restart, because this runs only once.
func (e *economics) seedDefaultRules(ctx context.Context) error {
	if v, _ := e.setting(ctx, "route.rules.seeded"); v == "1" {
		return nil
	}
	if err := e.rules.SeedDefaults(ctx); err != nil {
		return err
	}
	return e.putSetting(ctx, "route.rules.seeded", "1")
}

// budgetNotice posts the SYSTEM message in the task channel. The Needs-you item for a pause is the engine's own approval
// request (an open approvals row); the room has no budget-specific inbox kind, so warnings are messages and events only.
func (e *economics) budgetNotice(ctx context.Context, n budget.Notice) {
	if n.TaskID == "" {
		return
	}
	rm, err := e.c.Room.RoomByTask(ctx, n.TaskID)
	if err != nil {
		return
	}
	if _, err := e.c.Room.Post(ctx, room.PostParams{RoomID: rm.ID, Type: room.TypeSystem, Author: room.System, Body: n.Message, TaskID: n.TaskID, RunID: n.RunID,
		Payload: map[string]any{"budget_level": n.Level, "budget_id": n.Status.Budget.ID, "budget_pct": n.Status.Pct, "cost_label": n.Status.CostLabel}}); err != nil {
		e.c.logf("budget notice: %v", err)
	}
}

// relayInconclusiveChecks posts skipped and errored checks (osv-scanner missing, a tool that broke) in the task channel with
// their honest status. The core's postCheck only knows pass and fail, and a skipped check must never read as either.
func (e *economics) relayInconclusiveChecks(ctx context.Context) {
	events.Consume(ctx, e.c.Bus, events.Filter{Types: []string{"task.check_inconclusive"}}, func(ev events.Event) { e.relay(ctx, ev) })
}

func (e *economics) relay(ctx context.Context, ev events.Event) {
	var d struct {
		Kind, Status, Reason, LogRef, DiffHash string
	}
	if json.Unmarshal(ev.Data, &d) != nil || ev.TaskID == "" {
		return
	}
	rm, err := e.c.Room.RoomByTask(ctx, ev.TaskID)
	if err != nil {
		return
	}
	status := room.CheckSkipped
	if d.Status == "error" {
		status = room.CheckError
	}
	if _, err := e.c.Room.PostCheckResult(ctx, rm.ID, room.CheckResult{Kind: d.Kind, Status: status, Summary: d.Reason, LogRef: nonEmpty(d.LogRef, "(no log)"),
		DiffHash: d.DiffHash, TaskID: ev.TaskID, IdempotencyKey: fmt.Sprintf("inconclusive:%d", ev.Seq)}); err != nil {
		e.c.logf("post inconclusive check: %v", err)
	}
}

// saveStatic writes a routing_decisions row, decided_by "static", for an agent turn that runs the agent's own configured
// adapter, model and effort (routing is off for the agent, or the router could not choose; reason says which), so outcomes
// join to what actually ran. It returns the decision's id ("" when it could not be stored).
func (e *economics) saveStatic(ctx context.Context, s turnSubject, reason string) string {
	a := s.agent
	tier := providers.TierPremium // a subscription CLI is premium (docs/evals.md s2)
	provider := a.Adapter
	if a.ProviderID != "" {
		provider = a.ProviderID
		if p, err := e.c.Providers.Get(ctx, a.ProviderID); err == nil {
			tier = p.Tier
		}
	}
	level := router.LevelBalanced
	req := router.RouteRequest{RunKind: s.kind, TaskID: s.taskID, RunID: s.runID, NodeID: s.nodeID, Attempt: s.attempt, Role: router.Role(a.Role), Level: level,
		ProjectID: s.projectID, AgentID: a.ID, Workflow: s.workflow, ContextCeiling: router.ContextPrivate, ContextTokens: s.contextTokens}
	if sig, ok := router.TurnSignals(s.kind); ok {
		req.TaskType, req.Complexity, req.Ambiguity = sig.TaskType, sig.Complexity, sig.Ambiguity
	}
	d := router.RoutingDecision{ID: ids.New("dec"), Agent: router.AgentRef{Adapter: a.Adapter, Instance: a.ID}, Provider: router.ProviderID(provider), Model: a.Model,
		Effort: router.Effort(a.Effort), TeamSize: 1, Confidence: 1, DecidedBy: "static", Created: time.Now().UTC(),
		Reason: reason,
		Facts: router.DecisionFacts{TaskID: s.taskID, Level: level, RunKind: s.kind, TaskType: req.TaskType, Attempt: s.attempt, ContextCeiling: router.ContextPrivate, Tier: tier,
			DataClass: providers.DataPrivate, QuotaRemainingPct: -1}}
	if err := e.rec.Save(ctx, req, d); err != nil {
		e.c.logf("record decision: %v", err)
		return ""
	}
	b, _ := json.Marshal(map[string]any{"decisionId": d.ID, "provider": d.Provider, "model": d.Model, "effort": d.Effort, "decidedBy": d.DecidedBy, "alternatives": 0,
		"runKind": s.kind})
	_, _ = e.c.Bus.Publish(ctx, events.Event{Type: "routing.decided", ProjectID: s.projectID, RunID: s.runID, TaskID: s.taskID, AgentID: a.ID, Data: b})
	return d.ID
}

// startupProbe selects the cheap model of every enabled cheap-tier private provider (decision D4), in the background.
func (e *economics) startupProbe(ctx context.Context) {
	ps, err := e.c.Providers.List(ctx)
	if err != nil {
		return
	}
	for _, p := range ps {
		if ctx.Err() != nil {
			return
		}
		if !p.Enabled || p.Tier != providers.TierCheap || p.DataClass != providers.DataPrivate {
			continue
		}
		if _, err := e.prober.Select(ctx, p.ID, false); err != nil {
			e.c.logf("startup probe %s: %v", p.Name, err)
		}
	}
}

// --- methods ---

func (e *economics) methods() map[string]Method {
	c := e.c
	human := c.Human
	return map[string]Method{
		// budgets
		"budget.list": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
			Scope     string `json:"scope"`
		}) (any, error) {
			return e.budgetList(ctx, p.ProjectID, budget.Scope(p.Scope))
		}),
		"budget.set": bind(func(ctx context.Context, p struct {
			budget.Budget
			Enabled *bool `json:"enabled"`
		}) (any, error) {
			b := p.Budget
			b.Enabled = p.Enabled == nil || *p.Enabled
			out, err := e.budgets.Set(ctx, b, human())
			if err == nil {
				c.publish(ctx, "budget.changed", out.ProjectID, map[string]any{"budgetId": out.ID, "scope": out.Scope, "scopeId": out.ScopeID, "by": human()})
			}
			return out, err
		}),
		"budget.delete": bind(func(ctx context.Context, p idParam) (any, error) {
			err := e.budgets.Delete(ctx, p.ID, human())
			if err == nil {
				c.publish(ctx, "budget.changed", "", map[string]any{"budgetId": p.ID, "deleted": true, "by": human()})
			}
			return nil, err
		}),
		"budget.status": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
			TaskID    string `json:"taskId"`
			RunID     string `json:"runId"`
			AgentID   string `json:"agentId"`
		}) (any, error) {
			return e.budgetStatus(ctx, budget.ScopeIDs{Project: p.ProjectID, Task: p.TaskID, Run: p.RunID, Agent: p.AgentID})
		}),
		"budget.resume": bind(func(ctx context.Context, p budgetResume) (any, error) { return e.budgetResume(ctx, p) }),

		// evals
		"evals.report": bind(func(ctx context.Context, p evals.Options) (any, error) {
			rep, err := evals.Build(ctx, c.DB, p)
			if err != nil {
				return nil, err
			}
			return map[string]any{"markdown": rep.Markdown(), "report": rep}, nil
		}),
		"evals.export": bind(func(ctx context.Context, p evals.Options) (any, error) { return e.evalsExport(ctx, p) }),

		// routing
		"route.explain": bind(func(ctx context.Context, p struct {
			DecisionID string `json:"decisionId"`
		}) (any, error) {
			_, d, err := e.rec.Load(ctx, p.DecisionID)
			if err != nil {
				return nil, err
			}
			text := e.live.Explain(d)
			if d.DecidedBy == "static" {
				text += "- Decided by: static assignment. " + d.Reason + ".\n"
			}
			return map[string]any{"text": text, "decision": d}, nil
		}),
		"route.decisions": bind(func(ctx context.Context, p struct {
			TaskID string `json:"taskId"`
		}) (any, error) {
			return e.routeDecisions(ctx, p.TaskID)
		}),
		"route.rules.list": bind(func(ctx context.Context, _ struct{}) (any, error) {
			rs, err := e.rules.List(ctx)
			if rs == nil {
				rs = []router.Rule{}
			}
			return map[string]any{"rules": rs}, err
		}),
		"route.rules.set": bind(func(ctx context.Context, p struct {
			Rule router.Rule `json:"rule"`
		}) (any, error) {
			if strings.TrimSpace(p.Rule.ID) == "" && p.Rule.Version == 0 {
				p.Rule.ID = ids.New("rule") // a new rule from the editor: the core names it
			}
			r, err := e.rules.Put(ctx, p.Rule)
			if errors.Is(err, router.ErrVersionConflict) {
				return nil, fmt.Errorf("rule %s changed since you read it (version %d is stale): reload and retry", p.Rule.ID, p.Rule.Version)
			}
			if err == nil {
				c.publish(ctx, "routing.rule_changed", "", map[string]any{"ruleId": r.ID, "version": r.Version, "by": human()})
			}
			return r, err
		}),
		"route.rules.delete": bind(func(ctx context.Context, p idParam) (any, error) {
			err := e.rules.Delete(ctx, p.ID)
			if err == nil {
				c.publish(ctx, "routing.rule_changed", "", map[string]any{"ruleId": p.ID, "deleted": true, "by": human()})
			}
			return nil, err
		}),
		// the tiebreak and the wall-time ceilings (P112): what the Routing page shows and a person changes
		"route.settings.get": bind(func(ctx context.Context, _ struct{}) (any, error) {
			return e.routeSettings(ctx), nil
		}),
		"route.settings.set": bind(func(ctx context.Context, p routeSettingsPatch) (any, error) {
			return e.setRouteSettings(ctx, p)
		}),
		// models a provider refused for want of data retention (P112): listed on the Routing page, cleared by a person
		"route.retention.list": bind(func(ctx context.Context, _ struct{}) (any, error) {
			return c.Providers.RetentionBlocks(ctx)
		}),
		"route.retention.clear": bind(func(ctx context.Context, p struct {
			ProviderID string `json:"providerId"`
			Model      string `json:"model"`
		}) (any, error) {
			ok, err := c.Providers.ClearRetentionBlock(ctx, p.ProviderID, p.Model)
			if err == nil && !ok {
				return nil, fmt.Errorf("%s/%s is not set aside", p.ProviderID, p.Model)
			}
			return map[string]bool{"cleared": ok}, err
		}),
		"route.preview": bind(func(ctx context.Context, p struct {
			Request router.RouteRequest `json:"request"`
		}) (any, error) {
			return e.routePreview(ctx, p.Request)
		}),
		// several dry runs in one call (the Routing page's wiring, decision P108): each answer in the order asked, a failure as
		// {"error": ...} in its place, at most maxPreviews
		"route.previews": bind(func(ctx context.Context, p struct {
			Requests []router.RouteRequest `json:"requests"`
		}) (any, error) {
			if len(p.Requests) > maxPreviews {
				return nil, fmt.Errorf("at most %d previews in one call, got %d", maxPreviews, len(p.Requests))
			}
			out := make([]any, len(p.Requests))
			for i, r := range p.Requests {
				v, err := e.routePreview(ctx, r)
				if err != nil {
					out[i] = map[string]string{"error": err.Error()}
					continue
				}
				out[i] = v
			}
			return out, nil
		}),
		"route.cheap.select": bind(func(ctx context.Context, p struct {
			ProviderID string `json:"providerId"`
			Force      bool   `json:"force"`
		}) (any, error) {
			return e.prober.Select(ctx, p.ProviderID, p.Force)
		}),
		"route.cheap.get": bind(func(ctx context.Context, p struct {
			ProviderID string `json:"providerId"`
		}) (any, error) {
			sel, ok, err := probe.Stored(ctx, c.DB, p.ProviderID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"selection": sel, "selected": ok}, nil
		}),
		"route.probe.startup": bind(func(ctx context.Context, p struct {
			Enabled bool `json:"enabled"`
		}) (any, error) {
			v := "0"
			if p.Enabled {
				v = "1"
			}
			return map[string]any{"enabled": p.Enabled, "by": human()}, e.putSetting(ctx, "route.probe.startup", v)
		}),

		// verification checks beyond build/test/lint
		"checks.settings.get": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			return checks.LoadSettings(ctx, c.DB, p.ProjectID), nil
		}),
		"checks.settings.set": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
			checks.Settings
		}) (any, error) {
			if _, err := c.GetProject(ctx, p.ProjectID); err != nil {
				return nil, err
			}
			if err := checks.SaveSettings(ctx, c.DB, p.ProjectID, p.Settings, human()); err != nil {
				return nil, err
			}
			return checks.LoadSettings(ctx, c.DB, p.ProjectID), nil
		}),

		// telemetry (decision D12)
		"telemetry.status": bind(func(ctx context.Context, _ struct{}) (any, error) { return e.sink.Status(ctx) }),
		"telemetry.enable": bind(func(ctx context.Context, p struct {
			Enabled bool `json:"enabled"`
		}) (any, error) {
			if err := e.sink.SetEnabled(ctx, p.Enabled, human()); err != nil {
				return nil, err
			}
			return e.sink.Status(ctx)
		}),
		"telemetry.preview": bind(func(ctx context.Context, p struct {
			Limit int `json:"limit"`
		}) (any, error) {
			return e.sink.Preview(ctx, p.Limit)
		}),
		"telemetry.export": bind(func(ctx context.Context, _ struct{}) (any, error) {
			path, doc, err := e.sink.ExportToFile(ctx, filepath.Join(c.cfg.DataDir, "telemetry"))
			if err != nil {
				return nil, err
			}
			return map[string]any{"path": path, "spans": len(doc.Spans), "metrics": len(doc.Metrics), "document": doc}, nil
		}),
	}
}

// --- budget methods ---

type budgetRow struct {
	Budget budget.Budget  `json:"budget"`
	Status *budget.Status `json:"status,omitempty"` // for a budget on a specific id; a '*' ceiling is judged per instance
}

func (e *economics) budgetList(ctx context.Context, projectID string, scope budget.Scope) ([]budgetRow, error) {
	bs, err := e.budgets.List(ctx, budget.Filter{Scope: scope, ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	out := make([]budgetRow, 0, len(bs))
	for _, b := range bs {
		r := budgetRow{Budget: b}
		if b.ScopeID != budget.Any {
			st, err := budget.Evaluate(ctx, e.c.DB, b, b.ScopeID)
			if err != nil {
				return nil, err
			}
			r.Status = &st
		}
		out = append(out, r)
	}
	return out, nil
}

func (e *economics) budgetStatus(ctx context.Context, ids budget.ScopeIDs) (any, error) {
	sts, err := e.budgets.StatusFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	usage := map[string]budget.Spend{}
	for _, sc := range []budget.Scope{budget.ScopeProject, budget.ScopeTask, budget.ScopeRun, budget.ScopeAgent} {
		id := map[budget.Scope]string{budget.ScopeProject: ids.Project, budget.ScopeTask: ids.Task, budget.ScopeRun: ids.Run, budget.ScopeAgent: ids.Agent}[sc]
		if id == "" {
			continue
		}
		sp, err := budget.Usage(ctx, e.c.DB, sc, id)
		if err != nil {
			return nil, err
		}
		usage[string(sc)] = sp
	}
	sum, err := e.budgets.SummaryFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	return map[string]any{"statuses": sts, "usage": usage, "summary": sum,
		"note": "cost is reported, estimated or unknown and never merged; unknown calls are counted, not priced at zero"}, nil
}

type budgetResume struct {
	RunID     string   `json:"runId"`
	RequestID string   `json:"requestId"`
	Decision  string   `json:"decision"` // approved | rejected (default approved)
	LimitUSD  *float64 `json:"limitUsd"`
	LimitToks *int64   `json:"limitTokens"`
	LimitTurn *int64   `json:"limitTurns"`
}

// budgetResume is the human's answer to a budget pause: optionally raise the limit of each exceeded budget, then answer the
// request. Approving without raising the limit lets exactly one more turn through.
func (e *economics) budgetResume(ctx context.Context, p budgetResume) (any, error) {
	if p.RunID == "" {
		return nil, errors.New("runId is required")
	}
	decision := p.Decision
	if decision == "" {
		decision = "approved"
	}
	if decision != "approved" && decision != "rejected" {
		return nil, errors.New("decision must be approved or rejected")
	}
	pend, err := e.c.Engine.Pending(ctx, workflow.RunID(p.RunID))
	if err != nil {
		return nil, err
	}
	var req *workflow.PendingRequest
	for i := range pend {
		if (p.RequestID == "" || string(pend[i].ID) == p.RequestID) && strings.Contains(string(pend[i].Spec.Payload), `"gate":"`+budget.PauseGate+`"`) {
			req = &pend[i]
			break
		}
	}
	if req == nil {
		return nil, errors.New("this run has no pending budget pause")
	}
	var payload struct {
		Exceeded []budget.Status `json:"exceeded"`
	}
	_ = json.Unmarshal(req.Spec.Payload, &payload)
	raised := []string{}
	if decision == "approved" && (p.LimitUSD != nil || p.LimitToks != nil || p.LimitTurn != nil) {
		for _, st := range payload.Exceeded {
			b, err := e.budgets.Get(ctx, st.Budget.ID)
			if err != nil {
				return nil, err
			}
			changed := false
			if p.LimitUSD != nil && b.LimitUSD != nil {
				b.LimitUSD, changed = p.LimitUSD, true
			}
			if p.LimitToks != nil && b.LimitTokens != nil {
				b.LimitTokens, changed = p.LimitToks, true
			}
			if p.LimitTurn != nil && b.LimitTurns != nil {
				b.LimitTurns, changed = p.LimitTurn, true
			}
			if !changed {
				continue
			}
			if _, err := e.budgets.Set(ctx, b, e.c.Human()); err != nil {
				return nil, fmt.Errorf("raise limit of %s: %w", b.ID, err)
			}
			raised = append(raised, b.ID)
		}
	}
	if err := e.c.Respond(ctx, p.RunID, string(req.ID), decision, nil); err != nil {
		return nil, err
	}
	e.enforcer.MarkResumed(ctx, "", "", p.RunID, e.c.Human(), decision)
	return map[string]any{"runId": p.RunID, "requestId": req.ID, "decision": decision, "raised": raised, "by": e.c.Human()}, nil
}

// --- evals export ---

func (e *economics) evalsExport(ctx context.Context, o evals.Options) (any, error) {
	rep, err := evals.Build(ctx, e.c.DB, o)
	if err != nil {
		return nil, err
	}
	doc := map[string]any{"schema": "drydock.evals/v1", "generatedAt": rep.GeneratedAt, "report": rep, "markdown": rep.Markdown()}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(e.c.cfg.DataDir, "evals")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("evals-%s.json", time.UnixMilli(rep.GeneratedAt).UTC().Format("20060102-150405.000")))
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	return map[string]any{"path": path, "bytes": len(b), "sha256": hex.EncodeToString(sum[:]), "tasks": len(rep.Rows), "accepted": rep.Quality.Accepted}, nil
}

// --- routing methods ---

type decisionRow struct {
	ID            string   `json:"id"`
	TS            int64    `json:"ts"`
	RunID         string   `json:"runId,omitempty"`
	NodeID        string   `json:"nodeId,omitempty"`
	Attempt       int      `json:"attempt"`
	RunKind       string   `json:"runKind"`
	Level         string   `json:"level"`
	Agent         string   `json:"agent"`
	Provider      string   `json:"provider"`
	Model         string   `json:"model"`
	Effort        string   `json:"effort"`
	DecidedBy     string   `json:"decidedBy"`
	EstCostUSD    *float64 `json:"estCostUsd"` // nil = price unknown, never zero
	EscalatedFrom string   `json:"escalatedFrom,omitempty"`
	// StopReason says why Drydock stopped the turn (a wall-time ceiling); Tiebreak what an LLM tiebreak did (P112).
	StopReason string `json:"stopReason,omitempty"`
	Tiebreak   string `json:"tiebreak,omitempty"`
	Outcome    *struct {
		Outcome    string  `json:"outcome"`
		Accepted   bool    `json:"accepted"`
		Escalated  bool    `json:"escalated"`
		Iterations int     `json:"iterations"`
		CostUSD    float64 `json:"costUsd"`
		CostSource string  `json:"costSource"`
	} `json:"outcome,omitempty"`
}

func (e *economics) routeDecisions(ctx context.Context, taskID string) ([]decisionRow, error) {
	rows, err := e.c.DB.Reader().QueryContext(ctx, `SELECT d.id,d.ts,d.run_id,d.node_id,d.attempt,d.run_kind,d.level,d.agent,d.provider_id,d.model,d.effort,d.decided_by,d.est_cost_usd,d.escalated_from,
		d.stop_reason, COALESCE(json_extract(d.decision,'$.Facts.Tiebreak.chose'),''), COALESCE(json_extract(d.decision,'$.Facts.Tiebreak.ranked'),''),
		COALESCE(json_extract(d.decision,'$.Facts.Tiebreak.failed'),''), COALESCE(json_extract(d.decision,'$.Facts.Tiebreak.by'),''),
		o.outcome,o.accepted,o.escalated,o.iterations,o.cost_usd,o.cost_source
		FROM routing_decisions d LEFT JOIN routing_outcomes o ON o.decision_id=d.id WHERE d.task_id=? ORDER BY d.ts, d.id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []decisionRow{}
	for rows.Next() {
		var r decisionRow
		var est sql.NullFloat64
		var oc sql.NullString
		var acc, esc, it sql.NullInt64
		var cost sql.NullFloat64
		var cs sql.NullString
		var chose, ranked, failed, by string
		if err := rows.Scan(&r.ID, &r.TS, &r.RunID, &r.NodeID, &r.Attempt, &r.RunKind, &r.Level, &r.Agent, &r.Provider, &r.Model, &r.Effort, &r.DecidedBy, &est, &r.EscalatedFrom,
			&r.StopReason, &chose, &ranked, &failed, &by, &oc, &acc, &esc, &it, &cost, &cs); err != nil {
			return nil, err
		}
		switch {
		case failed != "":
			r.Tiebreak = "tiebreak failed (" + failed + "): the ranking's choice stands"
		case chose != "" && chose != ranked:
			r.Tiebreak = "tiebreak by " + by + " chose " + chose + " over " + ranked
		case chose != "":
			r.Tiebreak = "tiebreak by " + by + " kept " + chose
		}
		if est.Valid {
			v := est.Float64
			r.EstCostUSD = &v
		}
		if oc.Valid {
			r.Outcome = &struct {
				Outcome    string  `json:"outcome"`
				Accepted   bool    `json:"accepted"`
				Escalated  bool    `json:"escalated"`
				Iterations int     `json:"iterations"`
				CostUSD    float64 `json:"costUsd"`
				CostSource string  `json:"costSource"`
			}{oc.String, acc.Int64 != 0, esc.Int64 != 0, int(it.Int64), cost.Float64, cs.String}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// routePreview dry-runs the router: the same rules, candidates, quotas and budget state as a real decision, but nothing is
// recorded, no event is published and no quota slot is taken. A budget at its hard limit is read from the budget store.
// maxPreviews bounds route.previews: the wiring asks fourteen.
const maxPreviews = 32

func (e *economics) routePreview(ctx context.Context, req router.RouteRequest) (any, error) {
	if sig, ok := router.TurnSignals(req.RunKind); ok && req.TaskType == "" {
		// an agent turn kind is previewed with the signals a real turn of that kind gives the router (P63)
		req.TaskType, req.Complexity, req.Ambiguity = sig.TaskType, sig.Complexity, sig.Ambiguity
	}
	if err := e.fillBudget(ctx, &req); err != nil {
		return nil, err
	}
	d, err := e.dry.Decide(ctx, req)
	var be *router.BlockedError
	if errors.As(err, &be) {
		return map[string]any{"blocked": true, "reason": be.Reason, "alternatives": be.Alternatives, "budget": req.Budget}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]any{"blocked": false, "decision": d, "explain": e.dry.Explain(d), "budget": req.Budget, "recorded": false}
	// the wall-time ceiling a turn of this level would run under (an agent's own is not known to a preview)
	if sec := e.ceilingSettings(ctx).Levels[string(orLevel(req.Level))]; sec > 0 && router.IsAgentTurn(req.RunKind) {
		out["ceilingSeconds"] = sec
	}
	return out, nil
}

func orLevel(l router.Level) router.Level {
	if l == "" {
		return router.LevelBalanced
	}
	return l
}

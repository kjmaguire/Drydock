package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kjmaguire/drydock/internal/budget"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/router"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// What decision P112 puts around one run of an agent turn on one model (turnrouting.go calls attempt for every turn and every
// fallback hop):
//
//   - Reservation. The turn's estimate (the decision's estimated cost when its price is known, its estimated tokens, and one
//     turn) is reserved against every budget the turn counts toward (project, task, run, agent) before it starts, together
//     with what the turns in flight hold (budget.Store.Reserve). A turn that does not fit does not start: a workflow turn waits
//     on the budget pause, a reply or a lead turn says why. On a registered provider the estimate is also held on the
//     provider's quota windows (providers.QuotaLedger.Hold). When the turn ends the reservation is settled with what it
//     reported spending, or released when it never reached the model.
//   - Wall-time ceiling. A turn that runs past the ceiling of its agent, or of its level, is stopped: its context ends with
//     errWallTime, the reason is written on its decision (routing_decisions.stop_reason, "Why this model?") and published as
//     routing.stopped, and a workflow turn waits for a person (an approval: run it again, or stop the run). A turn that did no
//     work before the ceiling (a model that hung) may move along its fallback chain instead.

// errWallTime is the cause a turn's context ends with at its wall-time ceiling.
var errWallTime = errors.New("the turn ran past its wall-time ceiling")

// turnOn is the model one attempt runs on, as far as its reservation needs it.
type turnOn struct {
	decisionID string
	model      string
	estUSD     *float64 // nil: the price is unknown, no dollars are held
	estTokens  int
	force      bool // a human approved this turn over a budget's hard limit
}

// attemptResult is how one attempt ended.
type attemptResult struct {
	out     workflow.NodeOutput
	err     error
	w       *turnWatch
	refused *budget.RefusedError // the reservation did not fit: the turn did not start
	quota   bool                 // the provider's quota windows could not take it: the turn did not start
	stop    *ceilingStop         // Drydock stopped it at its wall-time ceiling
}

// ceilingStop is a turn stopped at its wall-time ceiling. For a workflow node it is a gate (workflow.GateError): the run waits
// for a person, who runs the turn again (approve) or stops the run (reject).
type ceilingStop struct {
	reason  string
	ceiling time.Duration
	node    string
}

func (c *ceilingStop) Error() string { return "stopped: " + c.reason }
func (c *ceilingStop) Unwrap() error { return errWallTime }

// Gate implements workflow.GateError.
func (c *ceilingStop) Gate() (string, string, map[string]any) {
	return "wall_time", fmt.Sprintf("Drydock stopped the %s turn: %s. Approve to run it again (the ceiling still applies) or reject to stop the run.", c.node, c.reason),
		map[string]any{"reason": c.reason, "ceilingSeconds": int(c.ceiling.Seconds())}
}

func providerOf(a Agent) string {
	if a.ProviderID != "" {
		return a.ProviderID
	}
	return "agent:" + a.Adapter
}

func (e *economics) attempt(ctx, rctx context.Context, s turnSubject, in workflow.NodeInput, next workflow.NodeHandler, on turnOn) attemptResult {
	w := &turnWatch{}
	ceiling, from := e.ceilingFor(ctx, s)
	ttl := budget.DefaultReservationTTL
	if ceiling > 0 {
		ttl = ceiling + 10*time.Minute
	}
	usd := 0.0
	if on.estUSD != nil {
		usd = *on.estUSD
	}
	res, err := e.budgets.Reserve(ctx, budget.Hold{Project: s.projectID, Task: s.taskID, Run: s.runID, Agent: s.agent.ID, DecisionID: on.decisionID,
		ProviderID: providerOf(s.agent), Model: on.model, EstUSD: usd, EstTokens: int64(on.estTokens), TTL: ttl, Force: on.force})
	var re *budget.RefusedError
	if errors.As(err, &re) {
		e.c.publish(ctx, "budget.reservation_refused", s.projectID, map[string]any{"taskId": s.taskID, "runId": s.runID, "agentId": s.agent.ID,
			"decisionId": on.decisionID, "model": on.model, "estUsd": usd, "estTokens": on.estTokens, "exceeded": len(re.Over)})
		return attemptResult{refused: re, w: w}
	}
	if err != nil {
		// the budgets cannot be read: no turn runs without them (fail closed, as the budget gate does)
		return attemptResult{err: fmt.Errorf("budget: the turn did not start: its reservation could not be made: %w", err), w: w}
	}
	held := false
	if s.agent.ProviderID != "" && e.ledger != nil {
		ok, retry, herr := e.ledger.Hold(ctx, res.ID, s.agent.ProviderID, on.model, float64(on.estTokens), usd)
		switch {
		case herr != nil:
			e.c.logf("route turn: quota hold: %v", herr) // the ledger could not be read: the router's own check already passed
		case !ok:
			_ = e.budgets.Release(ctx, res.ID, "the provider's quota windows could not take the turn")
			when := "later"
			if !retry.IsZero() {
				when = "after " + retry.UTC().Format(time.RFC3339)
			}
			return attemptResult{quota: true, w: w, err: fmt.Errorf("the quota windows of provider %s cannot take a turn on %s now (try again %s)", s.agent.ProviderID, on.model, when)}
		default:
			held = true
		}
	}
	tctx, cancel := rctx, context.CancelFunc(func() {})
	if ceiling > 0 {
		tctx, cancel = context.WithTimeoutCause(rctx, ceiling, errWallTime)
	}
	w.progress = func(started bool, usd float64, tokens int64) {
		if err := e.budgets.Progress(context.WithoutCancel(ctx), res.ID, started, usd, tokens); err != nil {
			e.c.logf("route turn: reservation progress: %v", err)
		}
	}
	hin := in
	hin.Emit = w.wrap(in.Emit)
	out, err := next(tctx, hin)
	stopped := err != nil && ceiling > 0 && ctx.Err() == nil && errors.Is(context.Cause(tctx), errWallTime)
	cancel()

	var stop *ceilingStop
	if stopped {
		stop = &ceilingStop{ceiling: ceiling, node: orDefault(string(in.NodeID), string(s.kind)),
			reason: fmt.Sprintf("the turn ran past the %s wall-time ceiling of %s", fmtCeiling(ceiling), from)}
		e.recordStop(ctx, s, on.decisionID, stop, w)
	}
	w.mu.Lock()
	u, cost := w.usage, w.costUSD
	w.mu.Unlock()
	if err == nil || w.ran() {
		if serr := e.budgets.Settle(ctx, res.ID, cost, int64(u.Total())); serr != nil {
			e.c.logf("route turn: settle reservation: %v", serr)
		}
		if held {
			if serr := e.ledger.Settle(context.WithoutCancel(ctx), res.ID, u, cost); serr != nil {
				e.c.logf("route turn: settle quota hold: %v", serr)
			}
		}
	} else {
		note := "the turn ended before it reached the model"
		if ctx.Err() != nil {
			note = "the turn was cancelled"
		}
		_ = e.budgets.Release(ctx, res.ID, note)
		if held {
			e.ledger.Drop(context.WithoutCancel(ctx), res.ID)
		}
	}
	return attemptResult{out: out, err: err, w: w, stop: stop}
}

// recordStop writes why Drydock stopped a turn on the decision it ran on, so "Why this model?" and the routing outcome show
// it, and publishes routing.stopped.
func (e *economics) recordStop(ctx context.Context, s turnSubject, decisionID string, stop *ceilingStop, w *turnWatch) {
	ctx = context.WithoutCancel(ctx)
	if decisionID != "" {
		if err := e.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE routing_decisions SET stop_reason=?, decision=json_set(decision,'$.Facts.Stopped',?) WHERE id=?`, stop.reason, stop.reason, decisionID)
			return err
		}); err != nil {
			e.c.logf("route turn: record the stop: %v", err)
		}
	}
	w.mu.Lock()
	calls := w.toolCalls
	w.mu.Unlock()
	e.c.publish(ctx, "routing.stopped", s.projectID, map[string]any{"decisionId": decisionID, "taskId": s.taskID, "runId": s.runID, "agentId": s.agent.ID,
		"runKind": s.kind, "reason": stop.reason, "ceilingSeconds": int(stop.ceiling.Seconds()), "toolCalls": calls})
}

// markRetention records that the turn's model needs data retention the organization has not enabled, so the router leaves it
// out from now on (until a person clears it on the Routing page).
func (e *economics) markRetention(ctx context.Context, s turnSubject, d router.RoutingDecision) {
	if _, err := e.c.Providers.MarkRetentionRequired(ctx, string(d.Provider), d.Model, "agent", providers.RetentionRefusal); err != nil {
		e.c.logf("route turn: mark %s as needing data retention: %v", d.Model, err)
	}
}

func fmtCeiling(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return d.String()
}

func orDefault(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

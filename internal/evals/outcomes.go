package evals

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Outcomes records routing_outcomes when a task ends. The tasks service's Hooks.OnTerminal is taken by the core (it
// archives the task channel), so this subscribes to the event bus for task.completed, task.failed and task.cancelled
// instead. A cursor in settings makes it exactly-once across restarts: on start it replays whatever it missed while the app
// was down, then follows live events.
type Outcomes struct {
	DB  *storage.DB
	Bus *events.SQLBus
	// OnError receives recording errors (they never stop the app); may be nil.
	OnError func(error)

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

const cursorKey = "evals.outcomes.cursor"

var terminalTypes = []string{"task.completed", "task.failed", "task.cancelled"}

// Start catches up from the saved cursor and then follows live events until Stop.
func (o *Outcomes) Start(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cancel != nil {
		return nil
	}
	cctx, cancel := context.WithCancel(ctx)
	o.cancel, o.done = cancel, make(chan struct{})
	ch, unsub := o.Bus.Subscribe(events.Filter{Types: terminalTypes})
	safe.Go("evals.Outcomes.Start", func() {
		defer close(o.done)
		defer func() { unsub() }()
		last := o.cursor(cctx)
		catchUp := func() {
			for {
				evs, err := o.Bus.Replay(cctx, last, events.Filter{Types: terminalTypes}, 200)
				if err != nil || len(evs) == 0 {
					if err != nil && cctx.Err() == nil {
						o.fail(err)
					}
					return
				}
				for _, e := range evs {
					o.handle(cctx, e)
					last = e.Seq
				}
				o.saveCursor(cctx, last)
			}
		}
		catchUp()
		for {
			select {
			case <-cctx.Done():
				return
			case e, ok := <-ch:
				if !ok { // fell behind (the bus closes slow subscribers): resubscribe first, then replay what was missed
					ch, unsub = o.Bus.Subscribe(events.Filter{Types: terminalTypes})
					catchUp()
					continue
				}
				if e.Seq <= last {
					continue
				}
				o.handle(cctx, e)
				last = e.Seq
				o.saveCursor(cctx, last)
			}
		}
	})
	return nil
}

// Stop ends the subscription.
func (o *Outcomes) Stop() {
	o.mu.Lock()
	cancel, done := o.cancel, o.done
	o.cancel = nil
	o.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (o *Outcomes) fail(err error) {
	if o.OnError != nil {
		o.OnError(err)
	}
}

func (o *Outcomes) cursor(ctx context.Context) uint64 {
	var v string
	if err := o.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, cursorKey).Scan(&v); err != nil {
		return 0
	}
	n, _ := strconv.ParseUint(v, 10, 64)
	return n
}

func (o *Outcomes) saveCursor(ctx context.Context, seq uint64) {
	err := o.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, cursorKey, strconv.FormatUint(seq, 10))
		return err
	})
	if err != nil && ctx.Err() == nil {
		o.fail(err)
	}
}

func (o *Outcomes) handle(ctx context.Context, e events.Event) {
	if e.TaskID == "" {
		return
	}
	if err := Record(ctx, o.DB, e.TaskID); err != nil && ctx.Err() == nil {
		o.fail(fmt.Errorf("routing outcome for %s: %w", e.TaskID, err))
	}
}

// Record writes the outcome rows of a task that has ended: one per routing decision, or one task-level row (decision_id
// ”) when the task had none. It is idempotent: a task's outcome is recorded once. A task that has not ended records
// nothing.
func Record(ctx context.Context, db *storage.DB, taskID string) error {
	f, err := Facts(ctx, db, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	outcome := ""
	switch f.Status {
	case "COMPLETED":
		outcome = "accepted"
		if !f.Accepted {
			outcome = "failed" // completed without a human identity cannot happen through the service; never count it as accepted
		}
	case "FAILED":
		outcome = "failed"
	case "CANCELLED":
		outcome = "cancelled"
	default:
		return nil
	}
	premRevise := 0
	ship, revise := 0, 0
	for _, rv := range f.Reviews {
		if rv.Verdict == "SHIP" {
			ship++
		} else {
			revise++
			if rv.Premium != nil && *rv.Premium {
				premRevise++
			}
		}
	}
	cs := "none"
	switch {
	case f.Total.Rows == 0:
	case f.Total.UnknownRows == f.Total.Rows:
		cs = "unknown"
	case f.Total.Mixed():
		cs = "mixed"
	case f.Total.ReportedUSD > 0 || f.Total.EstimatedUSD == 0:
		cs = "reported"
	default:
		cs = "estimated"
	}
	if f.Total.UnknownRows > 0 && f.Total.UnknownRows < f.Total.Rows && cs != "mixed" {
		cs = "mixed" // some calls have no cost at all: the figure is a lower bound, never presented as complete
	}
	var checks sql.NullInt64
	if f.ChecksAllPassing != nil {
		checks = sql.NullInt64{Int64: b2i(*f.ChecksAllPassing), Valid: true}
	}
	wall := int64(0)
	if f.Completed > 0 && f.Started > 0 && f.Completed >= f.Started {
		wall = f.Completed - f.Started
	}
	class := ""
	switch {
	case outcome == "accepted":
	case f.TripsStage2 > 0:
		class = "watchdog"
	case outcome == "cancelled":
		class = "cancelled"
	default:
		class = "failed"
	}
	acceptedAt := int64(0)
	if outcome == "accepted" {
		acceptedAt = f.Completed
	}
	decisions := f.Decisions
	if len(decisions) == 0 {
		decisions = []string{""}
	}
	now := storage.Now()
	return db.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, d := range decisions {
			_, err := tx.ExecContext(ctx, `INSERT INTO routing_outcomes(decision_id,task_id,project_id,outcome,accepted,accepted_at,accepted_by,iterations,ship_count,revise_count,
				premium_revise_count,escalated,checks_passed,watchdog_trips,tokens_in,tokens_out,tokens_cached,cost_usd,cost_reported_usd,cost_estimated_usd,cost_unknown_rows,
				cost_source,premium_subscription_units,premium_turns,wall_ms,failure_class,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
				ON CONFLICT(task_id, decision_id) DO NOTHING`,
				d, taskID, f.ProjectID, outcome, b2i(outcome == "accepted"), acceptedAt, f.AcceptedBy, f.Iteration, ship, revise,
				premRevise, b2i(f.Escalated), checks, f.Trips(), f.Tokens.Input, f.Tokens.Output, f.Tokens.Cached, f.Total.Known(), f.Total.ReportedUSD, f.Total.EstimatedUSD, f.Total.UnknownRows,
				cs, f.PremiumTokens.Total(), f.PremiumTurns, wall, class, now)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

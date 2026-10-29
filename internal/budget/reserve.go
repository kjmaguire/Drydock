package budget

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Pre-call reservations (decision P112, migration 00140). The Enforcer pauses a turn when a budget is already at its limit,
// but it reads only what was spent: two turns that start together both see the same spend, and both start even when only one
// of them fits in what is left. A reservation closes that gap. Before a routed turn starts, its estimate (dollars when the
// price is known, tokens, and one turn) is checked against every budget it counts toward, together with what the turns still
// running hold, and held, all in one write transaction (the database has one writer, so a second reservation sees the
// first). When the turn ends the reservation is settled (its real cost is then in the usage rows the budgets read) or
// released (the turn never ran, or was cancelled before it spent). A reservation still held at start belongs to a process that
// is gone and is expired (ExpireStale); one past its expiry no longer counts.

// DefaultReservationTTL is how long a reservation counts when its turn has no wall-time ceiling. A turn that runs longer still
// runs; its reservation simply stops counting.
const DefaultReservationTTL = 3 * time.Hour

// Hold is what a turn asks to reserve.
type Hold struct {
	Project, Task, Run, Agent string
	DecisionID                string
	ProviderID, Model         string
	EstUSD                    float64 // 0 when the price is unknown: nothing is guessed
	EstTokens                 int64
	TTL                       time.Duration // default DefaultReservationTTL
	// Force holds the reservation without checking: a human approved this one turn over a budget's hard limit. Later turns
	// still see what it holds.
	Force bool
}

// Reservation is a held reservation.
type Reservation struct {
	ID        string `json:"id"`
	ExpiresAt int64  `json:"expiresAt"`
}

// Held is what the turns in flight hold against one budget instance.
type Held struct {
	USD    float64 `json:"usd"`
	Tokens int64   `json:"tokens"`
	Turns  int64   `json:"turns"`
}

// RefusedError is a reservation that does not fit: Over holds each budget it would take past its limit, Message says why.
type RefusedError struct {
	Over    []Status
	Message string
}

func (e *RefusedError) Error() string { return "budget: " + e.Message }

// ErrRefused matches a RefusedError with errors.Is.
var ErrRefused = errors.New("budget: the turn's estimate does not fit the budget")

func (e *RefusedError) Is(t error) bool { return t == ErrRefused }

// heldWhere is the budget_reservations predicate for a scope instance.
func heldWhere(sc Scope) string {
	switch sc {
	case ScopeProject:
		return "project_id=?"
	case ScopeTask:
		return "task_id=?"
	case ScopeRun:
		return "run_id=?"
	}
	return "agent_id=?"
}

// HeldFor sums what the reservations in flight hold against one scope instance.
func (s *Store) HeldFor(ctx context.Context, sc Scope, instance string) (Held, error) {
	var h Held
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT COALESCE(SUM(est_usd),0), COALESCE(SUM(est_tokens),0), COUNT(*) FROM budget_reservations
		WHERE state='held' AND expires_at>? AND `+heldWhere(sc), storage.Now(), instance).Scan(&h.USD, &h.Tokens, &h.Turns)
	return h, err
}

// Reserve checks the turn's estimate against every budget that applies to it, with what the turns in flight hold, and holds
// it. A budget it does not fit returns a *RefusedError and holds nothing.
func (s *Store) Reserve(ctx context.Context, h Hold) (Reservation, error) {
	if h.TTL <= 0 {
		h.TTL = DefaultReservationTTL
	}
	if h.EstUSD < 0 || math.IsNaN(h.EstUSD) {
		h.EstUSD = 0
	}
	if h.EstTokens < 0 {
		h.EstTokens = 0
	}
	scope := ScopeIDs{Project: h.Project, Task: h.Task, Run: h.Run, Agent: h.Agent}
	r := Reservation{ID: ids.New("brs")}
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		now := storage.Now()
		// the write lock is held from here: every other reservation is either committed (and read below) or waits for this one
		if _, err := tx.ExecContext(ctx, `UPDATE budget_reservations SET state='expired', ended_at=?, note='expired: its turn outlived the reservation'
			WHERE state='held' AND expires_at<=?`, now, now); err != nil {
			return err
		}
		if !h.Force {
			sts, err := s.StatusFor(ctx, scope)
			if err != nil {
				return err
			}
			var over []Status
			var why []string
			for _, st := range sts {
				held, err := s.HeldFor(ctx, st.Budget.Scope, st.InstanceID)
				if err != nil {
					return err
				}
				if reason := doesNotFit(st, held, h); reason != "" {
					st.Held = &held
					over = append(over, st)
					why = append(why, reason)
				}
			}
			if len(over) > 0 {
				return &RefusedError{Over: over, Message: "the turn did not start: " + strings.Join(why, "; ")}
			}
		}
		r.ExpiresAt = now + h.TTL.Milliseconds()
		_, err := tx.ExecContext(ctx, `INSERT INTO budget_reservations(id,project_id,task_id,run_id,agent_id,decision_id,provider_id,model,est_usd,est_tokens,state,created_at,expires_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,'held',?,?)`, r.ID, h.Project, h.Task, h.Run, h.Agent, h.DecisionID, h.ProviderID, h.Model, h.EstUSD, h.EstTokens, now, r.ExpiresAt)
		return err
	})
	if err != nil {
		return Reservation{}, err
	}
	return r, nil
}

// doesNotFit says why the estimate does not fit one budget instance ("" when it does): what was spent, what the turns in
// flight hold and this turn's estimate together may not pass a limit.
func doesNotFit(st Status, held Held, h Hold) string {
	b := st.Budget
	what := fmt.Sprintf("%s %s budget", b.Scope, shortID(st.InstanceID))
	if b.LimitUSD != nil {
		spent := st.Spend.KnownUSD()
		if spent+held.USD+h.EstUSD > *b.LimitUSD+1e-9 {
			return fmt.Sprintf("the %s has $%.4f left after $%.4f spent and $%.4f held by %d turn(s) in flight; this turn is estimated at $%.4f",
				what, math.Max(0, *b.LimitUSD-spent-held.USD), spent, held.USD, held.Turns, h.EstUSD)
		}
	}
	if b.LimitTokens != nil {
		used := st.Spend.Tokens()
		if used+held.Tokens+h.EstTokens > *b.LimitTokens {
			return fmt.Sprintf("the %s has %d tokens left after %d used and %d held by %d turn(s) in flight; this turn is estimated at %d",
				what, max(0, *b.LimitTokens-used-held.Tokens), used, held.Tokens, held.Turns, h.EstTokens)
		}
	}
	if b.LimitTurns != nil {
		if st.Spend.Turns+held.Turns+1 > *b.LimitTurns {
			return fmt.Sprintf("the %s allows %d turns: %d done and %d in flight", what, *b.LimitTurns, st.Spend.Turns, held.Turns)
		}
	}
	return ""
}

// Settle ends a reservation whose turn ran, recording its real cost (the budgets read it from the usage rows from now on).
func (s *Store) Settle(ctx context.Context, id string, realUSD float64, realTokens int64) error {
	return s.end(ctx, id, "settled", realUSD, realTokens, "")
}

// Release ends a reservation whose turn did not run or was cancelled; note says why.
func (s *Store) Release(ctx context.Context, id, note string) error {
	return s.end(ctx, id, "released", 0, 0, note)
}

func (s *Store) end(ctx context.Context, id, state string, usd float64, tokens int64, note string) error {
	if id == "" {
		return nil
	}
	// the turn's context may be ending: the reservation must end all the same
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE budget_reservations SET state=?, real_usd=?, real_tokens=?, note=?, ended_at=? WHERE id=? AND state='held'`,
			state, usd, tokens, note, storage.Now(), id)
		return err
	})
}

// ExpireStale ends every reservation still held: at start, they belong to turns of a process that is gone (a crash, a forced
// quit). It returns how many it ended.
func (s *Store) ExpireStale(ctx context.Context) (int64, error) {
	var n int64
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE budget_reservations SET state='expired', ended_at=?, note='expired at start: its turn did not survive the restart'
			WHERE state='held'`, storage.Now())
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return n, err
}

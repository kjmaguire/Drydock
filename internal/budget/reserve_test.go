package budget_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/budget"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Pre-call reservations (decision P112): the estimate of a turn about to start is held against every budget it counts toward,
// so two turns that start together cannot both pass a budget only one of them fits in.

func TestTwoTurnsStartingTogetherCannotBothPassABudgetOnlyOneFitsIn(t *testing.T) {
	e := newEnv(t)
	e.task("t1", "prj1", "agtW", "agtR")
	e.set(budget.Budget{Scope: budget.ScopeProject, ScopeID: "prj1", LimitUSD: f(1), Enabled: true})
	e.usage(row{task: "t1", agent: "agtW", in: 10, out: 10, cost: 0.40})
	// $0.60 left; each turn is estimated at $0.50: only one fits
	h := budget.Hold{Project: "prj1", Task: "t1", Agent: "agtW", EstUSD: 0.50, EstTokens: 1000}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ok, refused int
	var refusal error
	for range 2 {
		wg.Go(func() {
			_, err := e.st.Reserve(e.ctx, h)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, budget.ErrRefused):
				refused, refusal = refused+1, err
			default:
				t.Errorf("reserve: %v", err)
			}
		})
	}
	wg.Wait()
	if ok != 1 || refused != 1 {
		t.Fatalf("%d turns reserved and %d were refused; exactly one fits", ok, refused)
	}
	var re *budget.RefusedError
	if !errors.As(refusal, &re) || len(re.Over) != 1 || re.Over[0].Held == nil || re.Over[0].Held.Turns != 1 {
		t.Fatalf("the refusal must name the budget and what is held: %+v", refusal)
	}
	if !strings.Contains(re.Message, "$0.1000 left after $0.4000 spent and $0.5000 held by 1 turn(s) in flight") {
		t.Fatalf("message: %s", re.Message)
	}
	// the budget's own status is still what was spent: a reservation is not spend
	sts, err := e.st.StatusFor(e.ctx, budget.ScopeIDs{Project: "prj1"})
	if err != nil || len(sts) != 1 || sts[0].Spend.KnownUSD() != 0.40 || sts[0].State != budget.StateOK {
		t.Fatalf("status %+v %v", sts, err)
	}
}

func TestReservationsSettleReleaseAndCountTokensAndTurns(t *testing.T) {
	e := newEnv(t)
	e.task("t1", "prj1", "agtW", "agtR")
	e.set(budget.Budget{Scope: budget.ScopeAgent, ScopeID: "agtW", LimitTokens: i(7000), LimitTurns: i(2), Enabled: true})
	h := budget.Hold{Project: "prj1", Task: "t1", Agent: "agtW", EstTokens: 2000} // price unknown: no dollars held
	r1, err := e.st.Reserve(e.ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := e.st.Reserve(e.ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	// a third turn: 4000 tokens and two turns are held; the turn limit is two
	if _, err := e.st.Reserve(e.ctx, h); !errors.Is(err, budget.ErrRefused) || !strings.Contains(err.Error(), "allows 2 turns: 0 done and 2 in flight") {
		t.Fatalf("a third turn must not fit a two-turn budget: %v", err)
	}
	// one turn ran: its usage is in the rows and its reservation is settled; the other never ran and is released
	e.usage(row{task: "t1", agent: "agtW", in: 1500, out: 1000, cost: 0.02})
	e.exec(`INSERT INTO agent_runs(id,agent_id,task_id,status,started_at) VALUES('ar1','agtW','t1','STOPPED',?)`, storage.Now())
	if err := e.st.Settle(e.ctx, r1.ID, 0.02, 2500); err != nil {
		t.Fatal(err)
	}
	if err := e.st.Release(e.ctx, r2.ID, "the turn failed before it ran"); err != nil {
		t.Fatal(err)
	}
	held, err := e.st.HeldFor(e.ctx, budget.ScopeAgent, "agtW")
	if err != nil || held.Turns != 0 || held.Tokens != 0 {
		t.Fatalf("nothing is held once both ended: %+v %v", held, err)
	}
	var state1, state2 string
	var real float64
	e.db.Reader().QueryRow(`SELECT state, real_usd FROM budget_reservations WHERE id=?`, r1.ID).Scan(&state1, &real)
	e.db.Reader().QueryRow(`SELECT state FROM budget_reservations WHERE id=?`, r2.ID).Scan(&state2)
	if state1 != "settled" || real != 0.02 || state2 != "released" {
		t.Fatalf("states %s (%v) %s", state1, real, state2)
	}
	// 2500 tokens used, one turn done: a turn estimated at 2000 tokens fits, then one at 3000 does not
	if _, err := e.st.Reserve(e.ctx, h); err != nil {
		t.Fatalf("2500 used + 2000 fits 7000: %v", err)
	}
	big := h
	big.EstTokens = 3000
	if _, err := e.st.Reserve(e.ctx, big); !errors.Is(err, budget.ErrRefused) || !strings.Contains(err.Error(), "tokens left") {
		t.Fatalf("2500 used + 2000 held + 3000 passes 7000: %v", err)
	}
	// a human approved this one turn over the limit: it is held without the check, and later turns see it
	force := big
	force.Force = true
	if _, err := e.st.Reserve(e.ctx, force); err != nil {
		t.Fatalf("a forced reservation is held: %v", err)
	}
	if held, _ := e.st.HeldFor(e.ctx, budget.ScopeAgent, "agtW"); held.Turns != 2 || held.Tokens != 5000 {
		t.Fatalf("held %+v", held)
	}
}

func TestStaleReservationsExpireAtStartAndAfterTheirTTL(t *testing.T) {
	e := newEnv(t)
	e.task("t1", "prj1", "agtW", "agtR")
	e.set(budget.Budget{Scope: budget.ScopeTask, ScopeID: "t1", LimitTurns: i(1), Enabled: true})
	h := budget.Hold{Project: "prj1", Task: "t1", Agent: "agtW"}
	if _, err := e.st.Reserve(e.ctx, h); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Reserve(e.ctx, h); !errors.Is(err, budget.ErrRefused) {
		t.Fatalf("the held turn uses the one turn: %v", err)
	}
	// the app crashed with the turn running; at the next start the reservation is expired
	n, err := e.st.ExpireStale(e.ctx)
	if err != nil || n != 1 {
		t.Fatalf("expired %d: %v", n, err)
	}
	r, err := e.st.Reserve(e.ctx, budget.Hold{Project: "prj1", Task: "t1", Agent: "agtW", TTL: time.Millisecond})
	if err != nil {
		t.Fatalf("after the restart the turn may start again: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	// the reservation outlived its TTL: it no longer counts, and the next reservation marks it expired
	if _, err := e.st.Reserve(e.ctx, h); err != nil {
		t.Fatalf("an expired reservation must not count: %v", err)
	}
	var state string
	e.db.Reader().QueryRow(`SELECT state FROM budget_reservations WHERE id=?`, r.ID).Scan(&state)
	if state != "expired" {
		t.Fatalf("state %s", state)
	}
}

// While a turn runs, what it reported spending is in the usage rows and its agent_runs row counts the turn: the reservation
// holds only the rest, so nothing is counted twice.
func TestARunningTurnsReservationHoldsOnlyWhatItHasNotSpentYet(t *testing.T) {
	e := newEnv(t)
	e.task("t1", "prj1", "agtW", "agtR")
	e.set(budget.Budget{Scope: budget.ScopeAgent, ScopeID: "agtW", LimitTokens: i(5000), LimitTurns: i(2), Enabled: true})
	r, err := e.st.Reserve(e.ctx, budget.Hold{Project: "prj1", Task: "t1", Agent: "agtW", EstUSD: 0.10, EstTokens: 2000})
	if err != nil {
		t.Fatal(err)
	}
	// the turn started (its agent_runs row now exists) and reported 1500 tokens and $0.04
	e.exec(`INSERT INTO agent_runs(id,agent_id,task_id,status,started_at) VALUES('ar1','agtW','t1','RUNNING',?)`, storage.Now())
	e.usage(row{task: "t1", agent: "agtW", in: 1000, out: 500, cost: 0.04})
	if err := e.st.Progress(e.ctx, r.ID, true, 0.04, 1500); err != nil {
		t.Fatal(err)
	}
	held, err := e.st.HeldFor(e.ctx, budget.ScopeAgent, "agtW")
	if err != nil || held.Turns != 0 || held.Tokens != 500 || held.USD < 0.0599 || held.USD > 0.0601 {
		t.Fatalf("held %+v %v", held, err)
	}
	// a refusal still names the started turn as in flight beside what it holds (windows-latest verify, dfdceac: "2000 held by 0
	// turn(s) in flight" once the first turn had started)
	if held.InFlight != 1 {
		t.Fatalf("in flight %+v", held)
	}
	if _, err := e.st.Reserve(e.ctx, budget.Hold{Project: "prj1", Task: "t1", Agent: "agtW", EstTokens: 4000}); err == nil || !strings.Contains(err.Error(), "500 held by 1 turn(s) in flight") {
		t.Fatalf("refusal: %v", err)
	}
	// one turn running and one more fits the two-turn limit: 1 (its agent_runs row) + 0 held + 1
	if _, err := e.st.Reserve(e.ctx, budget.Hold{Project: "prj1", Task: "t1", Agent: "agtW", EstTokens: 1000}); err != nil {
		t.Fatalf("the running turn must not be counted twice: %v", err)
	}
	// spending past the estimate holds nothing more (never negative)
	if err := e.st.Progress(e.ctx, r.ID, true, 0.50, 9000); err != nil {
		t.Fatal(err)
	}
	if held, _ := e.st.HeldFor(e.ctx, budget.ScopeAgent, "agtW"); held.Tokens != 1000 || held.Turns != 1 {
		t.Fatalf("held %+v", held)
	}
}

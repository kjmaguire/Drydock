package budget

import (
	"context"
	"database/sql"
	"fmt"
	"math"

	"github.com/kjmaguire/drydock/internal/storage"
)

// State is where a budget instance stands.
type State string

const (
	StateOK       State = "ok"
	StateWarning  State = "warning"  // at or past warnPct
	StateExceeded State = "exceeded" // at or past 100%: the next agent turn does not start
)

// ScopeIDs are the concrete ids of one place in the app (a node execution, a task view).
type ScopeIDs struct {
	Project, Task, Run, Agent string
	// Workflow is the name of the workflow the run belongs to ("" outside a workflow run).
	Workflow string
}

func (s ScopeIDs) of(sc Scope) string {
	switch sc {
	case ScopeProject:
		return s.Project
	case ScopeWorkflow:
		return s.Workflow
	case ScopeTask:
		return s.Task
	case ScopeRun:
		return s.Run
	case ScopeAgent:
		return s.Agent
	}
	return ""
}

// Target is a budget that applies to a concrete instance.
type Target struct {
	Budget     Budget `json:"budget"`
	InstanceID string `json:"instanceId"`
}

// Applicable lists the enabled budgets that apply to the ids: an explicit budget whose scope id equals the instance, and
// '*' templates (restricted to the project when they name one). A workflow budget applies to the runs of its workflow, in its
// project when it names one.
func (s *Store) Applicable(ctx context.Context, ids ScopeIDs) ([]Target, error) {
	var out []Target
	for _, sc := range []Scope{ScopeProject, ScopeWorkflow, ScopeTask, ScopeRun, ScopeAgent} {
		id := ids.of(sc)
		if id == "" {
			continue
		}
		rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+cols+` FROM budgets WHERE enabled=1 AND scope=? AND scope_id IN (?, '*')
			AND (scope_id<>'*' OR project_id='' OR project_id=?) AND (scope<>'workflow' OR project_id='' OR project_id=?) ORDER BY scope_id DESC, id`,
			string(sc), id, ids.Project, ids.Project)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			b, err := scan(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			inst := id
			if sc == ScopeWorkflow {
				inst = InstanceOf(b)
			}
			out = append(out, Target{Budget: b, InstanceID: inst})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Status is the live evaluation of one budget instance.
type Status struct {
	Budget     Budget   `json:"budget"`
	InstanceID string   `json:"instanceId"`
	Spend      Spend    `json:"spend"`
	PctUSD     *float64 `json:"pctUsd,omitempty"` // known spend / limit; nil without a dollar limit
	PctTokens  *float64 `json:"pctTokens,omitempty"`
	PctTurns   *float64 `json:"pctTurns,omitempty"`
	Pct        float64  `json:"pct"`               // the highest of the defined percentages
	Binding    string   `json:"binding,omitempty"` // which dimension is highest: usd | tokens | turns
	State      State    `json:"state"`
	// CostComplete is false when a dollar limit is set and some rows have an unknown cost: the dollar percentage is then a
	// lower bound, and only the token and turn limits are exact.
	CostComplete bool   `json:"costComplete"`
	CostLabel    string `json:"costLabel"` // "reported", "estimated", "reported + estimated" or "none"
	Message      string `json:"message"`
	// Held is what turns in flight hold against the budget (reserve.go), set when a reservation was refused.
	Held *Held `json:"held,omitempty"`
}

// Evaluate computes a budget instance's status from the usage rows.
func Evaluate(ctx context.Context, db *storage.DB, b Budget, instance string) (Status, error) {
	sp, err := Usage(ctx, db, b.Scope, instance)
	if err != nil {
		return Status{}, err
	}
	return evaluate(b, instance, sp), nil
}

func evaluate(b Budget, instance string, sp Spend) Status {
	st := Status{Budget: b, InstanceID: instance, Spend: sp, State: StateOK, CostComplete: true}
	set := func(dim string, used, limit float64) *float64 {
		p := used / limit
		if p > st.Pct || st.Binding == "" {
			st.Pct, st.Binding = p, dim
		}
		return &p
	}
	if b.LimitUSD != nil {
		st.PctUSD = set("usd", sp.KnownUSD(), *b.LimitUSD)
		st.CostComplete = sp.UnknownCostRows == 0
	}
	if b.LimitTokens != nil {
		st.PctTokens = set("tokens", float64(sp.Tokens()), float64(*b.LimitTokens))
	}
	if b.LimitTurns != nil {
		st.PctTurns = set("turns", float64(sp.Turns), float64(*b.LimitTurns))
	}
	switch {
	case st.Pct >= 1:
		st.State = StateExceeded
	case st.Pct >= b.WarnPct:
		st.State = StateWarning
	}
	switch {
	case sp.ReportedUSD > 0 && sp.EstimatedUSD > 0:
		st.CostLabel = "reported + estimated"
	case sp.ReportedUSD > 0:
		st.CostLabel = "reported"
	case sp.EstimatedUSD > 0:
		st.CostLabel = "estimated"
	default:
		st.CostLabel = "none"
	}
	st.Message = describe(st)
	return st
}

func describe(st Status) string {
	b := st.Budget
	what := scopeLabel(b, st.InstanceID)
	var parts []string
	if b.LimitUSD != nil {
		s := fmt.Sprintf("$%.4f of $%.4f (%s)", st.Spend.KnownUSD(), *b.LimitUSD, st.CostLabel)
		if st.Spend.UnknownCostRows > 0 {
			s += fmt.Sprintf(", %d call(s) with unknown cost not included", st.Spend.UnknownCostRows)
		}
		parts = append(parts, s)
	}
	if b.LimitTokens != nil {
		parts = append(parts, fmt.Sprintf("%d of %d tokens", st.Spend.Tokens(), *b.LimitTokens))
	}
	if b.LimitTurns != nil {
		parts = append(parts, fmt.Sprintf("%d of %d turns", st.Spend.Turns, *b.LimitTurns))
	}
	s := what + " budget at " + fmt.Sprintf("%.0f%%", math.Floor(st.Pct*100))
	for i, p := range parts {
		if i == 0 {
			s += ": "
		} else {
			s += "; "
		}
		s += p
	}
	return s
}

// scopeLabel names a budget instance in a sentence: its scope and a short id, or a workflow's whole name (a name, not an id).
func scopeLabel(b Budget, instance string) string {
	if b.Scope == ScopeWorkflow {
		return "workflow " + b.ScopeID
	}
	return fmt.Sprintf("%s %s", b.Scope, shortID(instance))
}

func shortID(id string) string {
	if len(id) > 14 {
		return id[:14]
	}
	return id
}

// StatusFor evaluates every budget that applies to the ids.
func (s *Store) StatusFor(ctx context.Context, ids ScopeIDs) ([]Status, error) {
	ts, err := s.Applicable(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Status, 0, len(ts))
	for _, t := range ts {
		st, err := Evaluate(ctx, s.DB, t.Budget, t.InstanceID)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

// Summary is the narrowest view the router needs: the tightest applicable budget.
type Summary struct {
	Scope        string  `json:"scope"`
	PctUsed      float64 `json:"pctUsed"`
	RemainingUSD float64 `json:"remainingUsd"` // -1 when no applicable budget has a dollar limit
	HardLimit    bool    `json:"hardLimit"`    // every budget is a hard limit: it pauses at 100%
	Any          bool    `json:"any"`
}

// SummaryFor picks the budget with the highest percentage and the smallest remaining dollars among those that apply.
func (s *Store) SummaryFor(ctx context.Context, ids ScopeIDs) (Summary, error) {
	sts, err := s.StatusFor(ctx, ids)
	if err != nil {
		return Summary{}, err
	}
	sum := Summary{RemainingUSD: -1}
	for _, st := range sts {
		sum.Any = true
		sum.HardLimit = true
		if st.Pct >= sum.PctUsed {
			sum.PctUsed, sum.Scope = st.Pct, string(st.Budget.Scope)
		}
		if st.Budget.LimitUSD != nil {
			rem := *st.Budget.LimitUSD - st.Spend.KnownUSD()
			if rem < 0 {
				rem = 0
			}
			if sum.RemainingUSD < 0 || rem < sum.RemainingUSD {
				sum.RemainingUSD = rem
			}
		}
	}
	return sum, nil
}

// alert records that a threshold fired; it returns true only the first time (so each threshold notifies once).
func (s *Store) alert(ctx context.Context, budgetID, instance, level string) (bool, error) {
	var fresh bool
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO budget_alerts(budget_id,instance_id,level,ts) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, budgetID, instance, level, storage.Now())
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		fresh = n > 0
		return nil
	})
	return fresh, err
}

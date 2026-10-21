package budget

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Notice is what the Enforcer tells the rest of the app (the room posts it as a SYSTEM message in the task channel).
type Notice struct {
	Level     string // warning | exceeded | paused
	Message   string
	ProjectID string
	TaskID    string
	RunID     string
	Status    Status
}

// Enforcer is the budget middleware for agent_task nodes.
type Enforcer struct {
	Store *Store
	Bus   events.Bus
	// Notify receives warnings, crossings and pauses; it may be nil. It must not block for long.
	Notify func(ctx context.Context, n Notice)
	// Consume marks an approved pause request used (workflow.Engine.MarkConsumed).
	Consume func(ctx context.Context, requestID workflow.RequestID) error
}

// FailClosed is a middleware that refuses every agent turn with message. It stands in for the Enforcer when the budgets could not
// be started: no limit is no reason to let agents spend without one.
func FailClosed(message string) workflow.Middleware {
	return func(next workflow.NodeHandler) workflow.NodeHandler {
		return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{}, errors.New("budget: " + message)
		}
	}
}

// PauseGate is the payload marker of a budget pause request.
const PauseGate = "budget"

// pausePayload is the structured payload of the approval request a pause raises.
type pausePayload struct {
	Gate     string   `json:"gate"`
	Message  string   `json:"message"`
	Exceeded []Status `json:"exceeded"`
	Resume   string   `json:"resume"` // how a human resumes
	Node     string   `json:"node"`
	TaskID   string   `json:"taskId,omitempty"`
}

// Middleware gates every agent turn: before it starts, any applicable budget at 100% parks the run on an approval request;
// after it, thresholds that were crossed notify once. A human decision on an earlier pause is read from the approvals row.
func (e *Enforcer) Middleware() workflow.Middleware {
	return func(next workflow.NodeHandler) workflow.NodeHandler {
		return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
			ids := e.scopeIDs(ctx, in)
			// a decision on this node's earlier pause: approved lets this one turn through, rejected ends the run
			bypass, err := e.priorDecision(ctx, in)
			if err != nil {
				return workflow.NodeOutput{}, err
			}
			if bypass {
				ctx = context.WithValue(ctx, approvedTurnKey{}, true)
			} else {
				sts, err := e.Store.StatusFor(ctx, ids)
				if err != nil {
					return workflow.NodeOutput{}, fmt.Errorf("budget: cannot evaluate budgets, agent turn not started: %w", err) // fail closed
				}
				var over []Status
				for _, st := range sts {
					if st.State == StateExceeded {
						over = append(over, st)
					}
				}
				if len(over) > 0 {
					return e.pause(ctx, in, ids, over), nil
				}
			}
			out, err := next(ctx, in)
			e.afterTurn(ctx, in, ids)
			return out, err
		}
	}
}

type approvedTurnKey struct{}

// ApprovedOverLimit reports whether this turn runs because a human approved one more turn over a budget at its hard limit.
// The model router then chooses as usual instead of treating the limit as a block (which would fall back to the agent's
// configured, often premium, model for exactly the turn the human allowed).
func ApprovedOverLimit(ctx context.Context) bool {
	v, _ := ctx.Value(approvedTurnKey{}).(bool)
	return v
}

// scopeIDs names the project, task, run and agent a turn belongs to. The agent of a work or review turn is the task's assignee or
// reviewer; the agent of a turn of a template role (spec, tests, design, security review) is the one the run pinned for the role
// at its start, and the planner's is the one the plan names. A per-agent budget used to apply only to work and review turns: a
// role or planner turn of an agent over its limit ran (audit 2026-10-16, M4).
func (e *Enforcer) scopeIDs(ctx context.Context, in workflow.NodeInput) ScopeIDs {
	ids := ScopeIDs{Project: in.ProjectID, Task: in.TaskID, Run: string(in.RunID)}
	role := in.Meta["agent"]
	switch role {
	case "", "work", "review":
	case "planner":
		var pi struct {
			PlannerAgent string `json:"plannerAgent"`
		}
		if json.Unmarshal(in.Input, &pi) == nil {
			ids.Agent = pi.PlannerAgent
		}
	default: // a template role: the run's input holds the agent pinned for it
		var ti struct {
			Roles map[string]string `json:"roles"`
		}
		if json.Unmarshal(in.Input, &ti) == nil {
			ids.Agent = ti.Roles[role]
		}
	}
	if in.TaskID == "" {
		return ids
	}
	var project, assignee, reviewer string
	if err := e.Store.DB.Reader().QueryRowContext(ctx, `SELECT project_id,assignee,reviewer FROM tasks WHERE id=?`, in.TaskID).Scan(&project, &assignee, &reviewer); err != nil {
		return ids
	}
	if ids.Project == "" {
		ids.Project = project
	}
	switch role {
	case "work":
		ids.Agent = assignee
	case "review":
		ids.Agent = reviewer
	}
	return ids
}

// PauseBlocked parks the run on the budget pause for a turn the model router refused because a budget is at its hard limit and
// no human has allowed the turn. The router's refusal used to send the turn on its way on the agent's configured model, which is
// often the premium one: the turn the budget exists to stop.
func (e *Enforcer) PauseBlocked(ctx context.Context, in workflow.NodeInput, reason string) (workflow.NodeOutput, error) {
	ids := e.scopeIDs(ctx, in)
	sts, err := e.Store.StatusFor(ctx, ids)
	if err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("budget: cannot evaluate budgets, agent turn not started: %w", err)
	}
	var over []Status
	for _, st := range sts {
		if st.State == StateExceeded {
			over = append(over, st)
		}
	}
	if len(over) == 0 {
		// the router saw a limit the store does not: stop the turn all the same, with the router's reason
		return workflow.NodeOutput{}, fmt.Errorf("budget: the agent turn did not start: %s", reason)
	}
	return e.pause(ctx, in, ids, over), nil
}

// priorDecision reads the human's answer to this node's budget pause. It returns true when an approved, human-decided
// request exists (and consumes it: one approval lets one turn through), and an error when the human rejected.
func (e *Enforcer) priorDecision(ctx context.Context, in workflow.NodeInput) (bool, error) {
	var id, status, by string
	err := e.Store.DB.Reader().QueryRowContext(ctx, `SELECT id,status,decided_by FROM approvals WHERE run_id=? AND node_id=? AND kind='approval'
		AND status IN ('APPROVED','REJECTED') AND CAST(payload AS TEXT) LIKE '%"gate":"budget"%' ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		string(in.RunID), string(in.NodeID)).Scan(&id, &status, &by)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !IsHuman(by) {
		return false, fmt.Errorf("budget: pause %s was answered by %q, which is not a human identity; the turn does not start", id, by)
	}
	if status == "REJECTED" {
		return false, fmt.Errorf("budget: the budget pause was rejected by %s; this run does not continue", by)
	}
	if e.Consume != nil {
		if err := e.Consume(ctx, workflow.RequestID(id)); err != nil && !errors.Is(err, workflow.ErrRequestConsumed) {
			return false, err
		}
	}
	return true, nil
}

func (e *Enforcer) pause(ctx context.Context, in workflow.NodeInput, ids ScopeIDs, over []Status) workflow.NodeOutput {
	sort.SliceStable(over, func(i, j int) bool { return over[i].Pct > over[j].Pct })
	top := over[0]
	msg := fmt.Sprintf("Budget limit reached: %s. The next agent turn (%s) did not start. Raise the limit or approve one more turn to continue.", top.Message, in.NodeID)
	p := pausePayload{Gate: PauseGate, Message: msg, Exceeded: over, Node: string(in.NodeID), TaskID: in.TaskID,
		Resume: "budget.resume (raise the limit and/or approve), or run.respond on this request; only a human can answer"}
	raw, _ := json.Marshal(p)
	e.emit(ctx, "budget.paused", in, ids, map[string]any{"budgetId": top.Budget.ID, "scope": top.Budget.Scope, "instance": top.InstanceID, "pct": top.Pct,
		"node": in.NodeID, "exceeded": len(over)})
	e.notify(ctx, in, Notice{Level: "paused", Message: msg, Status: top})
	return workflow.NodeOutput{Request: &workflow.RequestSpec{
		Kind:    "approval",
		Title:   fmt.Sprintf("Budget limit reached: %s", top.Message),
		Payload: raw,
		Retry:   true,
	}}
}

// afterTurn raises warning and exceeded notices once per budget instance.
func (e *Enforcer) afterTurn(ctx context.Context, in workflow.NodeInput, ids ScopeIDs) {
	sts, err := e.Store.StatusFor(ctx, ids)
	if err != nil {
		return
	}
	for _, st := range sts {
		if st.State == StateOK {
			continue
		}
		level := "warning"
		if st.State == StateExceeded {
			level = "exceeded"
			// the crossing implies the warning: do not announce both
			_, _ = e.Store.alert(ctx, st.Budget.ID, st.InstanceID, "warning")
		}
		fresh, err := e.Store.alert(ctx, st.Budget.ID, st.InstanceID, level)
		if err != nil || !fresh {
			continue
		}
		data := map[string]any{"budgetId": st.Budget.ID, "scope": st.Budget.Scope, "instance": st.InstanceID, "pct": st.Pct, "binding": st.Binding,
			"knownUsd": st.Spend.KnownUSD(), "reportedUsd": st.Spend.ReportedUSD, "estimatedUsd": st.Spend.EstimatedUSD,
			"unknownCostRows": st.Spend.UnknownCostRows, "tokens": st.Spend.Tokens(), "turns": st.Spend.Turns}
		msg := "Budget warning: " + st.Message + "."
		typ := "budget.warning"
		if level == "exceeded" {
			typ = "budget.exceeded"
			msg = "Budget exceeded: " + st.Message + ". The run pauses before its next agent turn; only you can resume it."
		}
		e.emit(ctx, typ, in, ids, data)
		e.notify(ctx, in, Notice{Level: level, Message: msg, Status: st})
	}
}

func (e *Enforcer) emit(ctx context.Context, typ string, in workflow.NodeInput, ids ScopeIDs, data map[string]any) {
	if e.Bus == nil {
		return
	}
	data["runId"], data["taskId"] = string(in.RunID), in.TaskID
	_, _ = events.Emit(ctx, e.Bus, typ, events.Event{ProjectID: ids.Project, RunID: string(in.RunID), TaskID: in.TaskID, AgentID: ids.Agent}, data)
}

func (e *Enforcer) notify(ctx context.Context, in workflow.NodeInput, n Notice) {
	if e.Notify == nil {
		return
	}
	n.ProjectID, n.TaskID, n.RunID = in.ProjectID, in.TaskID, string(in.RunID)
	e.Notify(ctx, n)
}

// MarkResumed records that a human resumed a budget pause (event budget.resumed).
func (e *Enforcer) MarkResumed(ctx context.Context, projectID, taskID, runID, by, how string) {
	if e.Bus == nil {
		return
	}
	_, _ = events.Emit(ctx, e.Bus, "budget.resumed", events.Event{ProjectID: projectID, RunID: runID, TaskID: taskID}, map[string]any{"by": by, "how": how, "at": storage.Now()})
}

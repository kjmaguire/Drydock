package pipeline

import "context"

// The team lead (supervisor). Drydock decides deterministically WHEN the lead is asked (the triggers below); the lead decides
// WHAT to do, as one strict JSON answer, from a compact digest. See lead_service.go for the turn and the actions.

// Lead triggers.
const (
	TriggerWatchdog       = "watchdog"        // a stage-1 watchdog trip on a task's turn (spin, cap, wall clock, error streak, echo)
	TriggerIterationLimit = "iteration_limit" // a loop of the task's workflow used up its budget
	TriggerRepeatedRevise = "repeated_revise" // the reviewer sent the same REVISE feedback in consecutive rounds
	TriggerStalled        = "stalled"         // a RUNNING task produced no progress events for the configured minutes
	TriggerUnassigned     = "unassigned"      // a READY/BACKLOG task has no worker or reviewer while automatic scheduling is on
)

// Lead actions.
const (
	LeadNudge    = "nudge"
	LeadReassign = "reassign"
	LeadSplit    = "split"
	LeadEscalate = "escalate"
	LeadContinue = "continue"
	// LeadFailed is stored, never decided: the supervision turn failed (an error or the time limit). It counts toward the
	// bound of interventions per task and run like any other turn.
	LeadFailed = "failed"
)

// LeadTrigger is one firing of a trigger, with the evidence the trigger site has.
type LeadTrigger struct {
	Kind   string
	TaskID string
	RunID  string
	// NodeAgent is the node's agent slot ("work", "review", a template role) when the trigger came from a turn, so the lead's
	// own turns can be excluded.
	NodeAgent string
	// Evidence is the trigger's evidence in a sentence or two (the watchdog's reason, the repeated feedback, the stall time).
	Evidence string
	// LastText is the last turn's final text, when the trigger site has it.
	LastText string
}

// LeadTask is a follow-up task the lead proposes when it splits a task.
type LeadTask struct {
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria []string `json:"acceptanceCriteria"`
}

// LeadDecision is the lead's validated answer.
type LeadDecision struct {
	Action  string     `json:"action"`
	Message string     `json:"message"`
	Agent   string     `json:"agent,omitempty"`
	Tasks   []LeadTask `json:"tasks,omitempty"`
	Reason  string     `json:"reason"`
}

// LeadOutcome is what a trigger site gets back.
type LeadOutcome struct {
	// Intervened is true when the lead ran and its decision (possibly an escalation after two invalid answers) was recorded
	// and posted. False: no lead on the team, the per-run bound is used up, a supervision turn is already in flight for the
	// task, a budget refused the turn, or the turn failed (stored as action failed, counted toward the bound); the trigger
	// site then behaves exactly as without a lead.
	Intervened bool
	Decision   LeadDecision
	LeadID     string
	LeadName   string
	// AgentID is the agent a reassignment went to (resolved from the lead's name or id).
	AgentID string
	// CreatedTasks are the follow-up tasks a split created.
	CreatedTasks []string
	// Exhausted is true when the bound of interventions per task and run is used up: the human decides from here.
	Exhausted bool
	// Invalid is true when the lead's answers failed validation twice and the decision became an escalation.
	Invalid bool
	// InterventionID is the stored intervention's id.
	InterventionID string
}

// Continues reports whether the decision lets the work go on with the lead's message as guidance (nudge, reassign, split).
func (o LeadOutcome) Continues() bool {
	if !o.Intervened {
		return false
	}
	switch o.Decision.Action {
	case LeadNudge, LeadReassign, LeadSplit:
		return true
	}
	return false
}

// LeadSupervisor runs the lead's supervision turn for a trigger.
type LeadSupervisor interface {
	Supervise(ctx context.Context, tr LeadTrigger) LeadOutcome
}

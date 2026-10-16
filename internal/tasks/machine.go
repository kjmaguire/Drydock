// Package tasks implements the task state machine (brief section 12), its guards, and storage with compare-and-swap.
package tasks

import "fmt"

// Status is a task state.
type Status string

const (
	Backlog      Status = "BACKLOG"
	Ready        Status = "READY"
	Claimed      Status = "CLAIMED"
	InProgress   Status = "IN_PROGRESS"
	Blocked      Status = "BLOCKED"       // something prevents progress; clears when the impediment clears
	WaitingHuman Status = "WAITING_HUMAN" // the agent can continue as soon as a human decides
	Review       Status = "REVIEW"
	Verification Status = "VERIFICATION"
	Completed    Status = "COMPLETED"
	Failed       Status = "FAILED"
	Cancelled    Status = "CANCELLED"
)

// Terminal reports whether no further transition is allowed.
func (s Status) Terminal() bool { return s == Completed || s == Failed || s == Cancelled }

var transitions = map[Status][]Status{
	Backlog:      {Ready},
	Ready:        {Claimed},
	Claimed:      {InProgress},
	InProgress:   {Blocked, WaitingHuman, Review},
	Blocked:      {InProgress},
	WaitingHuman: {InProgress},
	Review:       {Verification, InProgress},
	Verification: {Completed, InProgress},
}

// CanTransition reports whether from -> to is allowed by the state machine alone (guards are separate).
func CanTransition(from, to Status) bool {
	if from.Terminal() {
		return false
	}
	if to == Failed || to == Cancelled {
		return true // any non-terminal state
	}
	for _, t := range transitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// Next lists the states reachable from s.
func Next(s Status) []Status {
	if s.Terminal() {
		return nil
	}
	return append(append([]Status(nil), transitions[s]...), Failed, Cancelled)
}

// TransitionError explains a rejected transition.
type TransitionError struct {
	From, To Status
	Reason   string
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("tasks: cannot move %s -> %s: %s", e.From, e.To, e.Reason)
}

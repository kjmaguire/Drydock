package tasks

// The step a task is at on its way through Spec, Code, Checks, Review and You (Crew HQ, decision P64). The task page draws the
// steps (ui/app/screens/tasks/steps.ts); the board, the inbox and the crew read the step from task.summaries. Both follow one
// rule, kept in step by the cases in testdata/steps.json that the Go and the TypeScript tests both pass.

// StepNames are the steps in order; StepDone is a closed task past the last of them.
var StepNames = []string{"spec", "code", "checks", "review", "you"}

// StepDone is the step of a completed task.
const StepDone = "done"

// stepCheckKinds are the evidence kinds the step bar counts as checks: the tests and the end-to-end or preview runs.
var stepCheckKinds = map[string]bool{"test": true, "red_test": true, "green_test": true, "e2e": true, "preview": true}

// StepIndex is the index in StepNames of the step the task is at, len(StepNames) when it is completed. A pending spec gate keeps
// the task at Spec. A failed or cancelled task is at the first step its evidence does not cover; any other task is where its
// status puts it. reviews is how many reviewer verdicts it has.
func StepIndex(t Task, ev []Evidence, reviews int, specGate bool) int {
	if specGate {
		return 0
	}
	if t.Status == Failed || t.Status == Cancelled {
		var specAccepted, coded, checked bool
		for _, e := range ev {
			switch {
			case e.Kind == "spec_accept" && (e.Passed == nil || *e.Passed):
				specAccepted = true
			case e.Kind == "diff" || e.Kind == "commit":
				coded = true
			}
			if e.Kind == "green_test" {
				coded = true
			}
			if stepCheckKinds[e.Kind] {
				checked = true
			}
		}
		switch {
		case coded && checked && reviews > 0:
			return 4
		case coded && checked:
			return 3
		case coded:
			return 2
		case specAccepted || t.Started != 0:
			return 1
		}
		return 0
	}
	switch t.Status {
	case Backlog, Ready, Claimed:
		return 0
	case InProgress, Blocked:
		return 1
	case Review:
		return 3
	case Verification, WaitingHuman:
		return 4
	case Completed:
		return len(StepNames)
	}
	return 1
}

// StepName is the name of the step at index i of StepIndex: one of StepNames, or StepDone.
func StepName(i int) string {
	if i >= 0 && i < len(StepNames) {
		return StepNames[i]
	}
	return StepDone
}

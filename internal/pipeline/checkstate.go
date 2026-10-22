package pipeline

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/kjmaguire/drydock/internal/tasks"
)

// What a task's evidence says about its checks and its acceptance criteria, read the way the pipeline records them, for the
// screens that show a task without reading all of its evidence (task.summaries, task.get; decision P97).

// CheckStartedEvent is published when one check of a pass starts: {kind, round}. Its result is the task.evidence that follows.
const CheckStartedEvent = "task.check_started"

// The titles of the review requests a person answers, told apart by RequestPhase.
const (
	SpecRequestTitlePrefix  = "Accept the spec: "
	MergeRequestTitlePrefix = "Review and merge: "
)

// RequestPhase is what a pending request of a task asks a person: "spec" for the spec gate, "merge" for the review and merge of
// the finished work, else its kind (approval, question, review).
func RequestPhase(kind, title string, payload json.RawMessage) string {
	var p struct {
		Phase string `json:"phase"`
	}
	_ = json.Unmarshal(payload, &p)
	switch {
	case kind == "review" && (p.Phase == "spec" || strings.HasPrefix(title, SpecRequestTitlePrefix)):
		return "spec"
	case kind == "review" && strings.HasPrefix(title, MergeRequestTitlePrefix):
		return "merge"
	}
	return kind
}

// CheckKinds are the evidence kinds that are a check's result: the project's checks (Verify), the end-to-end and preview runs,
// the CI of a pull request and unresolved merge conflicts.
var CheckKinds = map[string]bool{"build": true, "test": true, "lint": true, "typecheck": true, "secrets": true, "deps": true,
	"e2e": true, "ci": true, "preview": true, "merge-conflicts": true}

// CheckRound is the pass of Verify an evidence's data says it is from (empty for a result recorded outside it, or before passes
// had rounds).
func CheckRound(data json.RawMessage) string {
	var d struct {
		Round string `json:"round"`
	}
	_ = json.Unmarshal(data, &d)
	return d.Round
}

// Check states.
const (
	CheckPassed       = "passed"
	CheckFailed       = "failed"
	CheckRunning      = "running"
	CheckInconclusive = "inconclusive" // skipped, or the tool broke: neither a pass nor a failure
	CheckNotRun       = "not_run"      // not in the newest pass (it stopped at an earlier failure, or has not got there)
)

// CheckState is the newest result of one kind of check.
type CheckState struct {
	Kind   string `json:"kind"`
	Status string `json:"status"`
	// Reason is the result's summary on one line ('' for not_run and running).
	Reason string `json:"reason,omitempty"`
	// Failing names what failed (tests, findings), at most MaxFailingNames of them.
	Failing []string `json:"failing,omitempty"`
}

// RunningCheck is the check a pass is running now, as CheckStartedEvent said.
type RunningCheck struct {
	Kind  string
	Round string
}

// MaxFailingNames is how many failing names a CheckState keeps.
const MaxFailingNames = 10

// LatestChecks is the newest attempt's result of each kind of check, in the order the kinds first ran. Within the attempt the
// newest pass of Verify counts: a kind it has no result for is not_run (the pass stopped at an earlier failure, or is still
// going), and a result recorded outside a pass (end-to-end, preview, CI) counts only when it came after that pass started.
// Evidence from before passes had rounds is read as one pass. running, if not nil, is the check a pass is running now.
func LatestChecks(ev []tasks.Evidence, running *RunningCheck) []CheckState {
	attempt := 0
	for _, e := range ev {
		if CheckKinds[e.Kind] && e.Attempt > attempt {
			attempt = e.Attempt
		}
	}
	var rows []tasks.Evidence
	var kinds []string
	seen := map[string]bool{}
	for _, e := range ev {
		if !CheckKinds[e.Kind] || e.Attempt != attempt {
			continue
		}
		rows = append(rows, e)
		if !seen[e.Kind] {
			seen[e.Kind] = true
			kinds = append(kinds, e.Kind)
		}
	}
	round := ""
	for i := len(rows) - 1; i >= 0 && round == ""; i-- {
		round = CheckRound(rows[i].Data)
	}
	if running != nil && running.Round != "" && running.Round != round {
		round = running.Round // a pass that has recorded nothing yet
	}
	if running != nil && running.Kind != "" && !seen[running.Kind] {
		kinds = append(kinds, running.Kind)
	}
	// where the newest pass starts: its first result, or past every result when it has none yet
	start := int64(-1)
	if round != "" {
		start = int64(1) << 62
		for _, e := range rows {
			if CheckRound(e.Data) == round {
				start = e.ID
				break
			}
		}
	}
	out := make([]CheckState, 0, len(kinds))
	for _, k := range kinds {
		var newest *tasks.Evidence
		for i := range rows {
			e := &rows[i]
			if e.Kind != k {
				continue
			}
			if r := CheckRound(e.Data); round == "" || r == round || (r == "" && e.ID > start) {
				newest = e
			}
		}
		st := CheckState{Kind: k, Status: CheckNotRun}
		switch {
		case newest != nil:
			st = checkStateOf(*newest)
		case running != nil && running.Kind == k && running.Round == round:
			st.Status = CheckRunning
		}
		out = append(out, st)
	}
	return out
}

func checkStateOf(e tasks.Evidence) CheckState {
	st := CheckState{Kind: e.Kind, Reason: shortLine(e.Summary, 200), Status: CheckInconclusive}
	if e.Passed != nil {
		st.Status = CheckFailed
		if *e.Passed {
			st.Status = CheckPassed
		}
	}
	if st.Status != CheckPassed {
		var d struct {
			Failing []string `json:"failing"`
		}
		_ = json.Unmarshal(e.Data, &d)
		for _, n := range d.Failing {
			if len(st.Failing) == MaxFailingNames {
				break
			}
			st.Failing = append(st.Failing, shortLine(n, 160))
		}
	}
	return st
}

// Criterion states.
const (
	CriterionPassed  = "passed"
	CriterionFailing = "failing"
	CriterionUnknown = "unknown"
)

// CriterionState is what the newest test run says about one acceptance criterion.
type CriterionState struct {
	Index int    `json:"index"` // its place in the task's acceptance list, from 0
	ID    string `json:"id"`    // AC1, AC2...: '' for a criterion written without one
	State string `json:"state"`
}

// AcceptanceState reads each acceptance criterion's state off the newest `test` check. The test-first step makes the tests
// carry each criterion's id (testScope), so: failing when a failing test's name mentions the id; passed when the newest test
// check passed and a test is known to mention it (a failing name recorded earlier names it, or the task went through the
// test-first step of a spec-gated feature, whose tests were made to mention every accepted id); unknown otherwise, also for a
// criterion without an id or a test check that gave no verdict.
func AcceptanceState(acceptance []string, ev []tasks.Evidence) []CriterionState {
	var newest *tasks.Evidence
	var named []string
	var specAccepted, red bool
	for i := range ev {
		e := &ev[i]
		switch e.Kind {
		case "test":
			newest = e
			named = append(named, failingNames(e.Data)...)
		case "red_test", "green_test":
			red = red || e.Kind == "red_test"
			named = append(named, failingNames(e.Data)...)
		case "spec_accept":
			specAccepted = specAccepted || e.Passed == nil || *e.Passed
		}
	}
	traced := specAccepted && red
	out := make([]CriterionState, 0, len(acceptance))
	for i, a := range acceptance {
		cs := CriterionState{Index: i, ID: criterionID(a), State: CriterionUnknown}
		if cs.ID != "" && newest != nil && newest.Passed != nil {
			switch {
			case !*newest.Passed && mentions(failingNames(newest.Data), cs.ID):
				cs.State = CriterionFailing
			case *newest.Passed && (traced || mentions(named, cs.ID)):
				cs.State = CriterionPassed
			}
		}
		out = append(out, cs)
	}
	return out
}

func failingNames(data json.RawMessage) []string {
	var d struct {
		Failing []string `json:"failing"`
	}
	_ = json.Unmarshal(data, &d)
	return d.Failing
}

// mentions reports whether a name carries the criterion id as testScope looks for it: the id not followed by another digit, so
// AC1 is not AC12.
func mentions(names []string, id string) bool {
	re := regexp.MustCompile(regexp.QuoteMeta(id) + `([^0-9]|$)`)
	for _, n := range names {
		if re.MatchString(n) {
			return true
		}
	}
	return false
}

// shortLine is s on one line, at most n bytes (cut on a character boundary, with "...").
func shortLine(s string, n int) string {
	s = oneLine(s)
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "..."
}

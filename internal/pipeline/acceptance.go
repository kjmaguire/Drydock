package pipeline

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/kjmaguire/drydock/internal/tasks"
)

// Each acceptance criterion's own status (decision P125): open, met, not met, or waived by a person, with what showed it. It is
// worked out, never stored as such, from three kinds of fact about the criterion's current text:
//
//   - a check result: the newest test run passed and a test is known to carry the criterion's id (met), or a failing test's name
//     carries it (not met). This is AcceptanceState, the trace the test-first templates make;
//   - a review finding: the newest review asked for changes and its feedback names the criterion's id (not met). A reviewer is an
//     agent: a review never confirms a criterion met;
//   - a person's word (tasks.CriterionMark): met, not met, waived with a reason, or open again.
//
// Since P128 a criterion may also name one of the project's checks ("[check: lint]", "(check: e2e)"): the newest result of that
// check with a verdict confirms it met or shows it not met, beside the test-name trace. A name that is not a check kind never
// gives a fact (the criterion stays open, never a silent pass), and the review step raises a finding for a check the project does
// not run (AcceptanceFindings), as it does for a criterion written without an id.
//
// A waiver holds until the person reopens or marks the criterion otherwise. Otherwise the newest of the person's word, the check
// result and the review finding counts (on the same moment, the person's). An agent's claim that a criterion is met (claim_met,
// with its evidence) is shown beside the status while nothing newer has settled it; it never sets the status. A criterion whose
// text changed after a mark is not bound by that mark.

// Statuses of an acceptance criterion.
const (
	CriterionOpen   = "open"
	CriterionMet    = "met"
	CriterionNotMet = "not_met"
	CriterionWaived = "waived"
)

// Sources of a criterion's status.
const (
	SourceCheck  = "check"
	SourceReview = "review"
	SourcePerson = "person"
)

// CriterionStatus is one acceptance criterion's status and what showed it.
type CriterionStatus struct {
	Index  int    `json:"index"` // its place in the task's acceptance list, from 0
	ID     string `json:"id"`    // AC1, AC2...: '' for a criterion written without one
	Status string `json:"status"`
	// Check is the check the criterion names to confirm it (CriterionCheck); '' when it names none.
	Check string `json:"check,omitempty"`
	// Source, By, Evidence and At say what showed the status: a check (By its kind), a review (By the reviewer) or a person
	// (By their human: identity), what it was (the test, the finding, the person's reason) and when (Unix ms). Empty when open.
	Source   string `json:"source,omitempty"`
	By       string `json:"by,omitempty"`
	Evidence string `json:"evidence,omitempty"`
	At       int64  `json:"at,omitempty"`
	// Claim is an agent's claim that the criterion is met that nothing newer has settled; it does not make it met.
	Claim *CriterionClaim `json:"claim,omitempty"`
}

// CriterionClaim is an agent's claim that a criterion is met, with its evidence.
type CriterionClaim struct {
	By       string `json:"by"`
	Evidence string `json:"evidence"`
	At       int64  `json:"at"`
}

// CriteriaStatus works out each criterion's status, in the order of the task's acceptance list.
func CriteriaStatus(acceptance []string, ev []tasks.Evidence, reviews []tasks.ReviewRecord, marks []tasks.CriterionMark) []CriterionStatus {
	checks := AcceptanceState(acceptance, ev)
	var testAt int64
	var failing []string
	for _, e := range ev {
		if e.Kind == "test" {
			testAt, failing = e.Created, failingNames(e.Data)
		}
	}
	var last *tasks.ReviewRecord
	if len(reviews) > 0 {
		last = &reviews[len(reviews)-1]
	}
	out := make([]CriterionStatus, 0, len(acceptance))
	for i, text := range acceptance {
		cs := CriterionStatus{Index: i, ID: criterionID(text), Status: CriterionOpen, Check: CriterionCheck(text)}
		sha := tasks.CriterionSHA(text)
		type fact struct {
			status, source, by, evidence string
			at                           int64
		}
		var facts []fact
		var person *tasks.CriterionMark
		var claim *tasks.CriterionMark
		for j := range marks {
			m := &marks[j]
			if m.Criterion != i || m.SHA != sha {
				continue
			}
			if m.Mark == tasks.MarkClaimMet {
				claim = m
			} else {
				person = m
			}
		}
		if i < len(checks) {
			switch checks[i].State {
			case CriterionPassed:
				facts = append(facts, fact{CriterionMet, SourceCheck, "test", "the newest test run passed, and its tests carry " + cs.ID, testAt})
			case CriterionFailing:
				facts = append(facts, fact{CriterionNotMet, SourceCheck, "test", "failing: " + shortLine(strings.Join(namesWith(failing, cs.ID), ", "), 300), testAt})
			}
		}
		if e := newestVerdict(ev, cs.Check); e != nil {
			if *e.Passed {
				facts = append(facts, fact{CriterionMet, SourceCheck, cs.Check, "the newest " + cs.Check + " check passed", e.Created})
			} else {
				facts = append(facts, fact{CriterionNotMet, SourceCheck, cs.Check, "the newest " + cs.Check + " check failed: " + shortLine(e.Summary, 300), e.Created})
			}
		}
		if last != nil && cs.ID != "" && strings.EqualFold(last.Verdict, "REVISE") {
			if line := lineWith(last.Feedback, cs.ID); line != "" {
				facts = append(facts, fact{CriterionNotMet, SourceReview, last.Reviewer, shortLine(line, 300), last.Created})
			}
		}
		if person != nil {
			switch person.Mark {
			case tasks.MarkWaived:
				facts = []fact{{CriterionWaived, SourcePerson, person.Actor, person.Note, person.Created}}
			case tasks.MarkMet:
				facts = append(facts, fact{CriterionMet, SourcePerson, person.Actor, person.Note, person.Created})
			case tasks.MarkNotMet:
				facts = append(facts, fact{CriterionNotMet, SourcePerson, person.Actor, person.Note, person.Created})
			}
		}
		var best *fact
		for k := range facts {
			f := &facts[k]
			if best == nil || f.at > best.at || (f.at == best.at && f.source == SourcePerson) {
				best = f
			}
		}
		if best != nil {
			cs.Status, cs.Source, cs.By, cs.Evidence, cs.At = best.status, best.source, best.by, best.evidence, best.at
		}
		if claim != nil && cs.Status != CriterionMet && cs.Status != CriterionWaived && claim.Created >= cs.At {
			cs.Claim = &CriterionClaim{By: claim.Actor, Evidence: claim.Note, At: claim.Created}
		}
		out = append(out, cs)
	}
	return out
}

// CriteriaSummary says where the criteria stand, for a handoff: "AC1 met (check: test); AC2 open, claimed met by a1".
func CriteriaSummary(cs []CriterionStatus) string {
	var parts []string
	for _, c := range cs {
		name := c.ID
		if name == "" {
			name = "criterion " + strconv.Itoa(c.Index+1)
		}
		s := name + " " + strings.ReplaceAll(c.Status, "_", " ")
		if c.Source != "" {
			s += " (" + c.Source + ": " + c.By + ")"
		}
		if c.Claim != nil {
			s += ", claimed met by " + c.Claim.By
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

// checkRef is how a criterion names the check that confirms it: "[check: lint]" or "(check: e2e)", anywhere in its text.
var checkRef = regexp.MustCompile(`(?i)[\[(]\s*check\s*:\s*([a-z0-9][a-z0-9_.-]*)\s*[\])]`)

// CriterionCheck is the check a criterion names to confirm it, in lower case; empty when it names none.
func CriterionCheck(text string) string {
	if m := checkRef.FindStringSubmatch(text); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

// newestVerdict is the newest result with a verdict of a check kind: nil when there is none, and for a name that is not a check
// kind (an approval, a spec acceptance or any other evidence never confirms a criterion).
func newestVerdict(ev []tasks.Evidence, kind string) *tasks.Evidence {
	if !CheckKinds[kind] {
		return nil
	}
	var out *tasks.Evidence
	for i := range ev {
		if ev[i].Kind == kind && ev[i].Passed != nil {
			out = &ev[i]
		}
	}
	return out
}

// What the review step finds wrong with how the acceptance criteria are written (decision P128). The severities are those of the
// security scan's findings (scan.Severity); no finding blocks anything: each is recorded as evidence without a verdict.
const (
	FindingNoID         = "criterion_without_id"
	FindingUnknownCheck = "criterion_unknown_check"
	// ReviewFindingKind is the evidence kind of a finding.
	ReviewFindingKind = "review_finding"
)

// AcceptanceFinding is one finding about how an acceptance criterion is written.
type AcceptanceFinding struct {
	Kind      string `json:"kind"`
	Severity  string `json:"severity"`  // low | medium
	Criterion int    `json:"criterion"` // its place in the task's acceptance list, from 0
	Check     string `json:"check,omitempty"`
	Text      string `json:"text"`
}

// AcceptanceFindings lists what is wrong with how the criteria are written: a criterion without an id (no test, review or handoff
// can name it; low), and one that names a check the project does not run, which no check could ever confirm (medium). known are
// the checks the project runs.
func AcceptanceFindings(acceptance []string, known []string) []AcceptanceFinding {
	var out []AcceptanceFinding
	next := 1
	for _, a := range acceptance {
		if n, err := strconv.Atoi(strings.TrimPrefix(criterionID(a), "AC")); err == nil && n >= next {
			next = n + 1
		}
	}
	have := map[string]bool{}
	for _, k := range known {
		have[k] = true
	}
	for i, a := range acceptance {
		name := "criterion " + criterionID(a)
		if criterionID(a) == "" {
			name = "criterion " + strconv.Itoa(i+1)
			want := fmt.Sprintf("AC%d %s", next, shortLine(strings.TrimSpace(a), 60))
			next++
			out = append(out, AcceptanceFinding{Kind: FindingNoID, Severity: "low", Criterion: i, Text: fmt.Sprintf("Acceptance criterion %d has no id, so no "+
				"test, review or handoff can name it and no test can be traced to it: start it with an id, for example %q.", i+1, want)})
		}
		if c := CriterionCheck(a); c != "" && !have[c] {
			runs := "none"
			if len(known) > 0 {
				runs = strings.Join(known, ", ")
			}
			out = append(out, AcceptanceFinding{Kind: FindingUnknownCheck, Severity: "medium", Criterion: i, Check: c, Text: fmt.Sprintf("Acceptance %s names "+
				"the check %q, which this project does not run (its checks: %s), so no check can confirm it: name one of those, or a person marks it.", name, c, runs)})
		}
	}
	return out
}

// namesWith are the names that carry a criterion's id.
func namesWith(names []string, id string) []string {
	var out []string
	for _, n := range names {
		if mentions([]string{n}, id) {
			out = append(out, n)
		}
	}
	return out
}

// lineWith is the first line of text that names the criterion's id ("" when none does).
func lineWith(text, id string) string {
	re := regexp.MustCompile(`(^|[^A-Za-z0-9])` + regexp.QuoteMeta(id) + `([^0-9]|$)`)
	for _, l := range strings.Split(text, "\n") {
		if re.MatchString(l) {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

// CriterionIndex finds a criterion by what an agent calls it: its id (AC1) or its number in the list ("2", "#2"). -1 when the
// task has no such criterion.
func CriterionIndex(acceptance []string, ref string) int {
	ref = strings.TrimSpace(ref)
	for i, a := range acceptance {
		if id := criterionID(a); id != "" && strings.EqualFold(id, ref) {
			return i
		}
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(ref, "#")); err == nil && n >= 1 && n <= len(acceptance) {
		return n - 1
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(ref), "AC")); err == nil && n >= 1 && n <= len(acceptance) && !hasCriterionIDs(acceptance) {
		return n - 1 // "AC2" for the second of criteria written without ids
	}
	return -1
}

// hasCriterionIDs reports whether any acceptance criterion carries an id (AC1...).
func hasCriterionIDs(acceptance []string) bool {
	for _, a := range acceptance {
		if criterionID(a) != "" {
			return true
		}
	}
	return false
}

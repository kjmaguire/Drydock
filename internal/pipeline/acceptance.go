package pipeline

import (
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
		cs := CriterionStatus{Index: i, ID: criterionID(text), Status: CriterionOpen}
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

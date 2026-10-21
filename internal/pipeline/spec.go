package pipeline

import (
	"fmt"
	"regexp"
	"strings"
)

// This file is the spec gate's parser (brief section 22): a PM-role agent writes a spec whose acceptance criteria are
// Given/When/Then, and nothing is implemented until a human accepts it. The parser is deterministic and strict about the
// one thing the rest of the pipeline relies on: every criterion has a Given, a When and a Then.

// Criterion is one acceptance criterion in Given/When/Then form.
type Criterion struct {
	ID    string `json:"id"` // AC1, AC2, ...: the tests reference these ids
	Given string `json:"given"`
	When  string `json:"when"`
	Then  string `json:"then"`
}

// Text renders the criterion on one line.
func (c Criterion) Text() string {
	return fmt.Sprintf("Given %s, when %s, then %s", c.Given, c.When, c.Then)
}

// Spec is a parsed specification.
type Spec struct {
	Title    string      `json:"title"`
	Body     string      `json:"body"` // the full text as written (untrusted: it is agent output)
	Criteria []Criterion `json:"criteria"`
}

const (
	maxCriteria  = 30
	maxSpecBytes = 48 << 10
	minSpecBytes = 40
)

var (
	// one-line form: "- AC1: Given x, when y, then z" (bullets, numbers and an id prefix are all optional)
	oneLineGWT = regexp.MustCompile(`(?is)^\s*(?:[-*+]\s+|\d+[.)]\s+)?(?:\**ac\d+\**\s*[:.)-]\s*)?\**given\**[\s:,]+(.+?)[\s,;.]+\**when\**[\s:,]+(.+?)[\s,;.]+\**then\**[\s:,]+(.+?)\s*$`)
	// multi-line (Gherkin) form: lines that start with Given / When / Then / And / But
	gherkinLine = regexp.MustCompile(`(?i)^\s*(?:[-*+]\s+)?\**(given|when|then|and|but)\**[\s:]+(.+?)\s*$`)
	headingLine = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.+?)\s*#*\s*$`)
)

// ParseSpec extracts the title and the Given/When/Then criteria from a spec text. It returns the problems that make the
// spec unusable (no criteria, an incomplete criterion, too long); a spec with problems is sent back to the PM, never
// shown to the human as acceptable.
func ParseSpec(text string) (Spec, []string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	sp := Spec{Body: strings.TrimSpace(text)}
	var problems []string
	if len(sp.Body) < minSpecBytes {
		problems = append(problems, "the spec is empty or too short to be a spec")
	}
	if len(sp.Body) > maxSpecBytes {
		problems = append(problems, fmt.Sprintf("the spec is %d bytes; keep it under %d", len(sp.Body), maxSpecBytes))
	}
	for _, l := range strings.Split(sp.Body, "\n") {
		if m := headingLine.FindStringSubmatch(l); m != nil {
			sp.Title = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m[1]), "Spec:"))
			break
		}
	}
	cs, more := parseCriteria(sp.Body)
	sp.Criteria = cs
	return sp, append(problems, more...)
}

// parseCriteria finds the Given/When/Then criteria in a text.
func parseCriteria(body string) ([]Criterion, []string) {
	var problems []string
	var criteria []Criterion

	type pending struct{ given, when, then []string }
	var cur *pending
	var raw []pending
	flush := func() {
		if cur != nil {
			raw = append(raw, *cur)
			cur = nil
		}
	}
	phase := ""
	for _, l := range strings.Split(body, "\n") {
		if m := oneLineGWT.FindStringSubmatch(l); m != nil {
			flush()
			raw = append(raw, pending{given: []string{m[1]}, when: []string{m[2]}, then: []string{m[3]}})
			phase = ""
			continue
		}
		g := gherkinLine.FindStringSubmatch(l)
		if g == nil {
			if strings.TrimSpace(l) == "" {
				flush()
				phase = ""
			}
			continue
		}
		kw, rest := strings.ToLower(g[1]), g[2]
		switch kw {
		case "given":
			flush()
			cur = &pending{given: []string{rest}}
			phase = "given"
		case "when":
			if cur == nil {
				cur = &pending{}
			}
			cur.when = append(cur.when, rest)
			phase = "when"
		case "then":
			if cur == nil {
				cur = &pending{}
			}
			cur.then = append(cur.then, rest)
			phase = "then"
		default: // and / but continue the previous clause
			if cur == nil {
				continue
			}
			switch phase {
			case "given":
				cur.given = append(cur.given, rest)
			case "when":
				cur.when = append(cur.when, rest)
			case "then":
				cur.then = append(cur.then, rest)
			}
		}
	}
	flush()

	seen := map[string]bool{}
	for _, p := range raw {
		c := Criterion{Given: clause(p.given), When: clause(p.when), Then: clause(p.then)}
		id := len(criteria) + 1
		switch {
		case c.Given == "" || c.When == "" || c.Then == "":
			problems = append(problems, fmt.Sprintf("criterion %d is incomplete: it needs a Given, a When and a Then", id))
			continue
		case seen[strings.ToLower(c.Text())]:
			problems = append(problems, fmt.Sprintf("criterion %d repeats an earlier one", id))
			continue
		}
		seen[strings.ToLower(c.Text())] = true
		c.ID = fmt.Sprintf("AC%d", len(criteria)+1)
		criteria = append(criteria, c)
	}
	switch {
	case len(criteria) == 0:
		problems = append(problems, "the spec has no acceptance criteria in Given/When/Then form")
	case len(criteria) > maxCriteria:
		problems = append(problems, fmt.Sprintf("the spec has %d criteria; split it (the limit is %d)", len(criteria), maxCriteria))
	}
	return criteria, problems
}

func clause(parts []string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(strings.Join(parts, " and ")), " "))
}

// ParseCriteriaLines validates criteria a human typed while accepting a spec (one per string).
func ParseCriteriaLines(lines []string) ([]Criterion, []string) {
	return parseCriteria(strings.Join(lines, "\n\n"))
}

// CriteriaStrings renders criteria for tasks.Task.Acceptance, with their ids so tests can reference them.
func CriteriaStrings(cs []Criterion) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID + ": " + c.Text()
	}
	return out
}

package router

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
)

// The LLM tiebreak (docs/model-routing.md s7, decision P112). The router is deterministic: when its ranking leaves the top
// candidates within a small margin of each other (the same tier, penalties and probe result, and prices within
// TiebreakMargin, or no price at all, as for an agent CLI's own models), the order between them is decided by a detail that
// says little (the remaining quota, the name). A Tiebreaker may then choose between them: one call to the configured cheap
// model with a short description that holds no content of the work (the task class, level and weights, and each candidate's
// facts; never the prompt, code, diffs or anything a person wrote). It is off unless a person turns it on, it has its own
// small budget and a timeout, and on any failure the ranking's choice stands; the decision records what happened
// (DecisionFacts.Tiebreak, DecidedBy "llm" when the call chose).

// TiebreakMargin is how close two blended prices must be (relative to the top candidate's) for the candidates to count as
// tied. A design assumption, like the other routing numbers.
const TiebreakMargin = 0.10

// MaxTiebreakOptions is the most candidates one tiebreak chooses between.
const MaxTiebreakOptions = 4

// ErrTiebreakOff is what a Tiebreaker returns when it is switched off: nothing is recorded and the ranking decides.
var ErrTiebreakOff = errors.New("router: tiebreak off")

// ErrTiebreakDryRun is what the Tiebreaker of a dry run (route.preview) returns when the tiebreak is on: the record says a real
// decision would ask (TiebreakRecord.DryRun) and no call is made.
var ErrTiebreakDryRun = errors.New("router: tiebreak not asked in a dry run")

// Tiebreaker chooses between tied candidates. Choose returns the chosen option's Key, or an error (the ranking's choice
// then stands, and the error's text is recorded as why).
type Tiebreaker interface {
	Choose(ctx context.Context, q TiebreakQuery) (TiebreakAnswer, error)
}

// TiebreakQuery is everything a tiebreak is told: the shape of the turn and the candidates' facts. It deliberately has no
// field for text of the work.
type TiebreakQuery struct {
	RunKind       RunKind          `json:"runKind"`
	TaskType      string           `json:"taskType,omitempty"`
	Role          string           `json:"role,omitempty"`
	Level         Level            `json:"level"`
	Complexity    float64          `json:"complexity"`
	Ambiguity     float64          `json:"ambiguity"`
	Risk          bool             `json:"risk"`
	DiffLines     int              `json:"diffLines"`
	Failures      FailureCounts    `json:"failures"`
	ChecksPassing bool             `json:"checksPassing"`
	Options       []TiebreakOption `json:"options"`
	// ProjectID, TaskID and AgentID say whose budgets the call counts toward; they are not sent to the model.
	ProjectID string `json:"-"`
	TaskID    string `json:"-"`
	AgentID   string `json:"-"`
}

// TiebreakOption is one tied candidate as the tiebreak sees it.
type TiebreakOption struct {
	Key               string  `json:"key"` // provider/model
	Provider          string  `json:"provider"`
	Model             string  `json:"model"`
	Tier              string  `json:"tier"`
	PricePerMUSD      float64 `json:"pricePerMUsd"` // blended 3:1; -1 unknown
	Context           int     `json:"context"`
	SupportsTools     bool    `json:"supportsTools"`
	ProbePassed       bool    `json:"probePassed"`
	AgentNative       bool    `json:"agentNative"`
	QuotaRemainingPct float64 `json:"quotaRemainingPct"` // -1 unknown
}

// TiebreakAnswer is a tiebreak's choice.
type TiebreakAnswer struct {
	Key     string  // the chosen option's Key
	Why     string  // one line from the model (clipped and redacted by the Tiebreaker)
	By      string  // provider/model that answered
	CostUSD float64 // what the call cost, when known
}

// TiebreakRecord is what a decision keeps of its tiebreak.
type TiebreakRecord struct {
	Between []string `json:"between"`          // the tied candidates, the ranking's order
	Ranked  string   `json:"ranked"`           // the ranking's choice
	Chose   string   `json:"chose"`            // the candidate that runs
	By      string   `json:"by,omitempty"`     // the cheap model that answered
	Why     string   `json:"why,omitempty"`    // its reason
	Failed  string   `json:"failed,omitempty"` // why the ranking's choice stands
	DryRun  bool     `json:"dryRun,omitempty"` // a preview: a real decision would ask By, nothing was called
}

// tiedWithTop returns the ranked candidates the ranking cannot tell from the top one in any way that matters.
func tiedWithTop(ranked []Scored) []Scored {
	if len(ranked) < 2 {
		return nil
	}
	top := ranked[0]
	out := []Scored{top}
	for _, s := range ranked[1:] {
		if len(out) == MaxTiebreakOptions {
			break
		}
		if s.TierIdx != top.TierIdx || s.Penalty != top.Penalty || s.ProbePassed != top.ProbePassed || s.PriceKnown != top.PriceKnown {
			continue
		}
		if top.PriceKnown {
			base := math.Max(top.BlendedPerM, 1e-9)
			if math.Abs(s.BlendedPerM-top.BlendedPerM)/base > TiebreakMargin {
				continue
			}
		}
		out = append(out, s)
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

func tiebreakQuery(req RouteRequest, level Level, tied []Scored) TiebreakQuery {
	q := TiebreakQuery{RunKind: req.RunKind, TaskType: req.TaskType, Role: string(req.Role), Level: level, Complexity: req.Complexity, Ambiguity: req.Ambiguity,
		Risk: req.Risk, DiffLines: req.DiffLines, Failures: req.Failures, ChecksPassing: req.ChecksPassing, ProjectID: req.ProjectID, TaskID: req.TaskID, AgentID: req.AgentID}
	for _, s := range tied {
		price := -1.0
		if s.PriceKnown {
			price = s.BlendedPerM
		}
		q.Options = append(q.Options, TiebreakOption{Key: string(s.Provider) + "/" + s.Model, Provider: string(s.Provider), Model: s.Model, Tier: string(s.Tier),
			PricePerMUSD: price, Context: s.Context, SupportsTools: s.SupportsTools, ProbePassed: s.ProbePassed, AgentNative: s.AgentNative,
			QuotaRemainingPct: s.QuotaRemainingPct})
	}
	return q
}

// tiebreak asks the Tiebreaker, when there is one and the ranking left a tie, and moves its choice to the front of ranked.
// It returns the record (nil when no tiebreak was asked).
func (r *RuleRouter) tiebreak(ctx context.Context, req RouteRequest, level Level, ranked []Scored) *TiebreakRecord {
	if r.tie == nil || level == LevelCheapest { // the Cheapest level spends nothing on choosing (s7)
		return nil
	}
	tied := tiedWithTop(ranked)
	if tied == nil {
		return nil
	}
	q := tiebreakQuery(req, level, tied)
	rec := &TiebreakRecord{Ranked: q.Options[0].Key, Chose: q.Options[0].Key}
	for _, o := range q.Options {
		rec.Between = append(rec.Between, o.Key)
	}
	ans, err := r.tie.Choose(ctx, q)
	if errors.Is(err, ErrTiebreakOff) {
		return nil
	}
	rec.By, rec.Why = ans.By, ans.Why
	if errors.Is(err, ErrTiebreakDryRun) {
		rec.DryRun = true
		return rec
	}
	if err != nil {
		rec.Failed = clipLine(err.Error(), 200)
		return rec
	}
	idx := -1
	for i, o := range q.Options {
		if o.Key == ans.Key {
			idx = i
		}
	}
	if idx < 0 {
		rec.Failed = fmt.Sprintf("the answer %q is not one of the options", clipLine(ans.Key, 80))
		return rec
	}
	rec.Chose = ans.Key
	if idx > 0 {
		// ranked holds the tied candidates in the same order as q.Options from index 0; find the chosen one and move it up
		for i := range ranked {
			if string(ranked[i].Provider)+"/"+ranked[i].Model == ans.Key {
				c := ranked[i]
				copy(ranked[1:i+1], ranked[0:i])
				ranked[0] = c
				break
			}
		}
	}
	return rec
}

func clipLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "..."
	}
	return s
}

// tiebreakReason is the decision reason's note on its tiebreak.
func tiebreakReason(t *TiebreakRecord) string {
	switch {
	case t == nil:
		return ""
	case t.DryRun:
		return fmt.Sprintf("; a real decision asks %s to break the tie between %s (not asked in a dry run)", orStr(t.By, "the cheap model"), strings.Join(t.Between, ", "))
	case t.Failed != "":
		return fmt.Sprintf("; tiebreak between %s failed (%s), the ranking's choice stands", strings.Join(t.Between, ", "), t.Failed)
	case t.Chose == t.Ranked:
		return fmt.Sprintf("; tiebreak by %s kept the ranking's choice over %s", t.By, strings.Join(t.Between[1:], ", "))
	}
	return fmt.Sprintf("; tiebreak by %s chose it over %s", t.By, t.Ranked)
}

package router

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/providers"
)

// tiebreakDouble is a LABELLED TEST DOUBLE of a Tiebreaker: it answers what the test tells it to and keeps what it was
// asked. The real Tiebreaker (internal/core/tiebreak.go) calls the configured cheap model through the llm.Wrapper.
type tiebreakDouble struct {
	answer string
	err    error
	asked  []TiebreakQuery
}

func (d *tiebreakDouble) Choose(_ context.Context, q TiebreakQuery) (TiebreakAnswer, error) {
	d.asked = append(d.asked, q)
	if d.err != nil {
		return TiebreakAnswer{By: "cheapco/tiny"}, d.err
	}
	return TiebreakAnswer{Key: d.answer, Why: "closer to the work", By: "cheapco/tiny", CostUSD: 0.0001}, nil
}

func tieRouter(w *world, tb Tiebreaker) *RuleRouter {
	return w.router(func(o *RuleOptions) { o.Source = agentCands(); o.Tiebreaker = tb })
}

func TestATieIsBrokenByTheCheapModelFromAContentFreeDescription(t *testing.T) {
	w := newWorld(t)
	tb := &tiebreakDouble{answer: "agent:claude/h3"}
	r := tieRouter(w, tb)
	q := req()
	q.Explicit = nil
	d := mustDecide(t, r, q)
	if d.Model != "h3" || d.DecidedBy != "llm" || !contains(d.PoliciesApplied, "llm_tiebreak") {
		t.Fatalf("the tiebreak chose h3: %s by %s %v", key(d), d.DecidedBy, d.PoliciesApplied)
	}
	tr := d.Facts.Tiebreak
	if tr == nil || tr.Ranked != "agent:claude/h1" || tr.Chose != "agent:claude/h3" || tr.By != "cheapco/tiny" || len(tr.Between) != MaxTiebreakOptions {
		t.Fatalf("record %+v", tr)
	}
	if !strings.Contains(d.Reason, "tiebreak by cheapco/tiny chose it over agent:claude/h1") {
		t.Fatalf("reason %q", d.Reason)
	}
	text := explain(d)
	if !strings.Contains(text, "- Tiebreak: the ranking left agent:claude/h1, agent:claude/h2, agent:claude/h3, agent:claude/h4 within 10% of each other; cheapco/tiny chose agent:claude/h3") {
		t.Fatalf("Why this model? does not show the tiebreak:\n%s", text)
	}
	// what the model was told: the shape of the turn and the candidates' facts, nothing of the work
	if len(tb.asked) != 1 {
		t.Fatalf("asked %d times", len(tb.asked))
	}
	a := tb.asked[0]
	if a.RunKind != RunAgentTask || a.TaskType != "test_fix" || a.Level != LevelBalanced || len(a.Options) != 4 || a.Options[0].PricePerMUSD != -1 || !a.Options[0].AgentNative {
		t.Fatalf("query %+v", a)
	}
	// the premium model and the other provider's model are not tied with the cheap agent models
	for _, o := range a.Options {
		if o.Model == "opus" || o.Model == "elsewhere" {
			t.Fatalf("%s is not tied with the top", o.Model)
		}
	}
}

func TestTheRankingsChoiceStandsWhenTheTiebreakFailsOrIsOff(t *testing.T) {
	w := newWorld(t)
	for name, c := range map[string]struct {
		tb   *tiebreakDouble
		fail string
	}{
		"timeout":        {&tiebreakDouble{err: context.DeadlineExceeded}, "context deadline exceeded"},
		"budget":         {&tiebreakDouble{err: errors.New("the tiebreak's own budget is used up")}, "budget is used up"},
		"not an option":  {&tiebreakDouble{answer: "agent:claude/opus"}, `the answer "agent:claude/opus" is not one of the options`},
		"made-up answer": {&tiebreakDouble{answer: "anything"}, "is not one of the options"},
	} {
		d := mustDecide(t, tieRouter(w, c.tb), req())
		if d.Model != "h1" || d.DecidedBy != "rules" || d.Facts.Tiebreak == nil || !strings.Contains(d.Facts.Tiebreak.Failed, c.fail) {
			t.Errorf("%s: %s by %s, tiebreak %+v", name, key(d), d.DecidedBy, d.Facts.Tiebreak)
			continue
		}
		if !strings.Contains(d.Reason, "failed") || !strings.Contains(d.Reason, "the ranking's choice stands") {
			t.Errorf("%s: reason %q", name, d.Reason)
		}
	}
	// off: nothing is recorded
	d := mustDecide(t, tieRouter(w, &tiebreakDouble{err: ErrTiebreakOff}), req())
	if d.Model != "h1" || d.Facts.Tiebreak != nil || strings.Contains(d.Reason, "tiebreak") {
		t.Fatalf("off: %s %+v %q", key(d), d.Facts.Tiebreak, d.Reason)
	}
}

func TestNoTiebreakWithoutATieAnExplicitPinOrAtTheCheapestLevel(t *testing.T) {
	w := newWorld(t)
	// the registry's world: the probed qwen-cheap is not tied with the unprobed glm-cheaper
	tb := &tiebreakDouble{answer: "x"}
	r := w.router(func(o *RuleOptions) { o.Tiebreaker = tb })
	mustDecide(t, r, req())
	// a pinned model is a person's instruction
	pinned := req()
	pinned.Explicit = &Pin{Model: "h2"}
	mustDecide(t, tieRouter(w, tb), pinned)
	// the Cheapest level spends nothing on choosing
	cheapest := req()
	cheapest.Level = LevelCheapest
	mustDecide(t, tieRouter(w, tb), cheapest)
	if len(tb.asked) != 0 {
		t.Fatalf("asked %d times: %+v", len(tb.asked), tb.asked)
	}
	// prices within 10% tie; further apart they do not
	s := []Scored{
		{Candidate: Candidate{Provider: "p", Model: "a", PriceKnown: true, BlendedPerM: 1.00}},
		{Candidate: Candidate{Provider: "p", Model: "b", PriceKnown: true, BlendedPerM: 1.09}},
		{Candidate: Candidate{Provider: "p", Model: "c", PriceKnown: true, BlendedPerM: 1.20}},
		{Candidate: Candidate{Provider: "p", Model: "d", PriceKnown: false}},
	}
	if got := tiedWithTop(s); len(got) != 2 || got[1].Model != "b" {
		t.Fatalf("tied %+v", got)
	}
}

// A model the provider refused for want of data retention is left out with a reason that says what to do (decision P112).
func TestAModelThatNeedsDataRetentionIsLeftOut(t *testing.T) {
	w := newWorld(t)
	w.src.Retention = w.reg
	if _, err := w.reg.MarkRetentionRequired(w.ctx, w.or.ID, "qwen-cheap", "api", providers.RetentionRefusal); err != nil {
		t.Fatal(err)
	}
	d := mustDecide(t, w.router(), req())
	if d.Model == "qwen-cheap" || !hasAlt(d, "qwen-cheap", FilterRetention) {
		t.Fatalf("chose %s; alternatives %+v", key(d), d.Alternatives)
	}
	for _, a := range d.Alternatives {
		if a.Model == "qwen-cheap" && !strings.Contains(a.Detail, "enable it in the provider's console") {
			t.Fatalf("detail %q", a.Detail)
		}
	}
	// the refusal of an agent turn is told apart from the work's own invalid requests
	for msg, want := range map[string]Cause{
		`API Error: 400 {"type":"error","error":{"type":"invalid_request_error","message":"In order to access this model, your organization or workspace must have data retention enabled."}}`: CauseRetentionRequired,
		`API Error: 400 {"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long"}}`:                                                                             "",
	} {
		if got := ClassifyUnavailable(msg); got != want {
			t.Errorf("%q: %q, want %q", msg, got, want)
		}
	}
	if causeKind(CauseRetentionRequired) != "fallback" || causeKind(CauseWallTime) != "fallback" {
		t.Fatal("both move along the chain")
	}
}

func TestTheLeadsAndRecommendationTurnsHaveTheirOwnKinds(t *testing.T) {
	w := newWorld(t)
	r := w.router()
	for _, k := range []RunKind{RunLead, RunRecommend} {
		d := mustDecide(t, r, turnReq(k))
		if d.Facts.RunKind != k || d.Facts.Tier != providers.TierCheap {
			t.Errorf("%s: %s (%s)", k, key(d), d.Facts.Tier)
		}
		if k.WritesFiles() {
			t.Errorf("%s turns are read-only", k)
		}
		sig, _ := TurnSignals(k)
		if !strings.Contains(explain(d), "- Turn: "+sig.Label+"; ") {
			t.Errorf("%s: no turn line", k)
		}
	}
}

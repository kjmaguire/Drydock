package router

import (
	"context"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Every turn of an agent is routed (decision P63). Each kind of turn has its own run kind, which a rule can match with
// run.kind, and signals (task class and weight) the shipped rules treat sensibly.

func turnReq(k RunKind) RouteRequest {
	q := req()
	q.RunKind, q.Role = k, "Agent"
	sig, ok := TurnSignals(k)
	if !ok {
		panic("no signals for " + k)
	}
	q.TaskType, q.Complexity, q.Ambiguity = sig.TaskType, sig.Complexity, sig.Ambiguity
	return q
}

func TestEachTurnKindIsRoutedByItsSignals(t *testing.T) {
	w := newWorld(t)
	r := w.router()
	for _, c := range []struct {
		kind RunKind
		tier providers.Tier
	}{
		{RunRoomReply, providers.TierCheap},        // light: the ladder's cheap tier
		{RunTests, providers.TierCheap},            // work: cheap first
		{RunSpec, providers.TierPremium},           // ambiguity 0.7: premium-only
		{RunDesign, providers.TierPremium},         // architecture: premium-only
		{RunSecurityReview, providers.TierPremium}, // security_review: premium-only
	} {
		d := mustDecide(t, r, turnReq(c.kind))
		if d.Facts.Tier != c.tier || d.Facts.RunKind != c.kind {
			t.Errorf("%s: %s (%s), want the %s tier", c.kind, key(d), d.Facts.Tier, c.tier)
		}
		sig, _ := TurnSignals(c.kind)
		if text := explain(d); !strings.Contains(text, "- Turn: "+sig.Label+"; ") {
			t.Errorf("%s: Why this model? does not say what kind of turn it was:\n%s", c.kind, text)
		}
	}
	// a turn that writes files needs a model that calls tools, as work does; a read-only reply does not ask for it
	if d := mustDecide(t, r, turnReq(RunTests)); !hasAlt(d, "no-tools", FilterTools) {
		t.Errorf("the tests turn did not refuse a model without tools: %+v", d.Alternatives)
	}
	if d := mustDecide(t, r, turnReq(RunRoomReply)); hasAlt(d, "no-tools", FilterTools) {
		t.Errorf("a room reply refused a model for its tools: %+v", d.Alternatives)
	}
	// rules match the new kinds by name
	eng := newEngine(t)
	only := w.router(func(o *RuleOptions) {
		o.Rules = []Rule{{ID: "replies-premium", Priority: 1, Enabled: true, When: `run.kind == 'room_reply'`, Then: Action{RequireTier: providers.TierPremium}}}
		o.Engine = eng
	})
	if d := mustDecide(t, only, turnReq(RunRoomReply)); d.Facts.Tier != providers.TierPremium {
		t.Errorf("a rule on run.kind == 'room_reply' did not apply: %s", key(d))
	}
	if d := mustDecide(t, only, turnReq(RunTests)); d.Facts.Tier != providers.TierCheap {
		t.Errorf("a rule on room replies applied to a tests turn: %s", key(d))
	}
}

// Like a review, the security review prefers a model other than the worker's (the shipped reviewer-differs rule).
func TestTheSecurityReviewPrefersAModelOtherThanTheWorkers(t *testing.T) {
	w := newWorld(t)
	q := turnReq(RunSecurityReview)
	q.Worker = &WorkerRef{Provider: w.id(w.ant), Model: "prem-1"}
	d := mustDecide(t, w.router(), q)
	if d.Model != "prem-2" || !hasAlt(d, "prem-1", "reviewer-differs") {
		t.Fatalf("security reviewer chose %s, alternatives %+v", key(d), d.Alternatives)
	}
}

// A stored copy of a shipped rule whose condition the person never edited is brought up to date; an edited one is theirs.
func TestUpgradeShippedUpdatesOnlyUneditedConditions(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewRuleStore(db, newEngine(t))
	var shipped Rule
	for _, r := range DefaultRules() {
		if r.ID == "reviewer-differs" {
			shipped = r
		}
	}
	if !strings.Contains(shipped.When, "security_review") {
		t.Fatalf("the shipped rule does not cover the security review: %s", shipped.When)
	}
	old := shipped
	old.When = shippedWhenUpgrades["reviewer-differs"]
	if _, err := s.Put(ctx, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, Rule{ID: "mine", When: shippedWhenUpgrades["reviewer-differs"], Then: Action{Penalty: 1}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	up, err := s.UpgradeShipped(ctx)
	if err != nil || len(up) != 1 || up[0] != "reviewer-differs" {
		t.Fatalf("upgraded %v %v", up, err)
	}
	got := map[string]Rule{}
	rs, _ := s.List(ctx)
	for _, r := range rs {
		got[r.ID] = r
	}
	if got["reviewer-differs"].When != shipped.When || got["reviewer-differs"].Version != 2 || got["mine"].When != shippedWhenUpgrades["reviewer-differs"] {
		t.Fatalf("stored %+v", got)
	}
	// an edited condition stays, and a deleted rule is not brought back
	ed := got["reviewer-differs"]
	ed.When = `run.kind == 'review'`
	if _, err := s.Put(ctx, ed); err != nil {
		t.Fatal(err)
	}
	if up, _ := s.UpgradeShipped(ctx); len(up) != 0 {
		t.Fatalf("an edited rule was upgraded: %v", up)
	}
	if err := s.Delete(ctx, "reviewer-differs"); err != nil {
		t.Fatal(err)
	}
	if up, _ := s.UpgradeShipped(ctx); len(up) != 0 {
		t.Fatalf("a deleted rule came back: %v", up)
	}
}

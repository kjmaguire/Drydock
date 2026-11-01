package providers

import "testing"

// An agent turn on a registered provider holds its estimate against the provider's windows from its start to its end
// (decision P112), so two turns that start together cannot both take a window only one fits in.
func TestHoldsCountAgainstTokenAndCreditWindowsUntilTheTurnEnds(t *testing.T) {
	e := newEnv(t)
	p := e.create(t, NewProvider{Name: "or", BaseURL: "https://api.example.com/v1"})
	l, _ := newLedger(t, e, t0)
	for _, w := range []Window{{ProviderID: p.ID, Kind: WindowTPD, Limit: 10000}, {ProviderID: p.ID, Kind: WindowCredits, Limit: 1}, {ProviderID: p.ID, Kind: WindowRPD, Limit: 10}} {
		if err := l.Configure(e.ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	if ok, _, err := l.Hold(e.ctx, "h1", p.ID, "m", 6000, 0.30); err != nil || !ok {
		t.Fatalf("the first turn fits: %v %v", ok, err)
	}
	// 6000 tokens held: a second turn of 6000 does not fit the 10000-token day, and takes nothing
	if ok, retry, _ := l.Hold(e.ctx, "h2", p.ID, "m", 6000, 0.30); ok || retry.IsZero() {
		t.Fatalf("the second turn must not fit while the first holds its tokens (ok %v, retry %v)", ok, retry)
	}
	ws, _ := l.Snapshot(e.ctx, p.ID)
	for _, w := range ws {
		if w.Kind == WindowRPD && w.Used != 1 {
			t.Fatalf("one request slot is taken, by the first turn only: %+v", w)
		}
	}
	// a small one fits beside it; the router's check sees the window as not yet exhausted
	if ok, _, _ := l.Hold(e.ctx, "h3", p.ID, "m", 3000, 0.30); !ok {
		t.Fatal("6000 + 3000 fits 10000")
	}
	if ok, _ := l.Check(e.ctx, p.ID, "m"); !ok {
		t.Fatal("9000 of 10000 held: still open")
	}
	// h3 never reached the provider: dropped, its request slot comes back
	l.Drop(e.ctx, "h3")
	// h1 ran: 7000 real tokens and $0.40 are added, the hold goes
	if err := l.Settle(e.ctx, "h1", Usage{Input: 5000, Output: 2000}, 0.40); err != nil {
		t.Fatal(err)
	}
	ws, _ = l.Snapshot(e.ctx, p.ID)
	got := map[WindowKind]float64{}
	for _, w := range ws {
		got[w.Kind] = w.Used
	}
	if got[WindowTPD] != 7000 || got[WindowCredits] != 0.40 || got[WindowRPD] != 1 {
		t.Fatalf("after the turns: %v", got)
	}
	// now 7000 are used and nothing is held: 3000 fits, 3001 does not
	if ok, _, _ := l.Hold(e.ctx, "h4", p.ID, "m", 3001, 0); ok {
		t.Fatal("7000 + 3001 passes 10000")
	}
	if ok, _, _ := l.Hold(e.ctx, "h5", p.ID, "m", 3000, 0); !ok {
		t.Fatal("7000 + 3000 fits")
	}
	// the window is now full with h5 held: the router's check refuses a new call until it ends
	if ok, _ := l.Check(e.ctx, p.ID, "m"); ok {
		t.Fatal("10000 of 10000 used or held: the window must refuse")
	}
	l.Drop(e.ctx, "h5")
	if ok, _ := l.Check(e.ctx, p.ID, "m"); !ok {
		t.Fatal("a dropped hold no longer counts")
	}
}

// A model the provider refused for want of data retention is marked once, listed, and cleared by a person (decision P112);
// deleting the provider removes its marks.
func TestRetentionMarksAreKeptUntilClearedAndGoWithTheProvider(t *testing.T) {
	e := newEnv(t)
	p := e.create(t, NewProvider{Name: "anthropic", BaseURL: "https://api.example.com/v1"})
	fresh, err := e.reg.MarkRetentionRequired(e.ctx, p.ID, "fable-5", "api", "In order to access this model, your organization or workspace must have data retention enabled.")
	if err != nil || !fresh {
		t.Fatalf("mark: %v %v", fresh, err)
	}
	if again, _ := e.reg.MarkRetentionRequired(e.ctx, p.ID, "fable-5", "agent", "again"); again {
		t.Fatal("a second mark of the same model is not new")
	}
	if !e.reg.RetentionBlocked(e.ctx, p.ID, "fable-5") || e.reg.RetentionBlocked(e.ctx, p.ID, "other") {
		t.Fatal("only the refused model is marked")
	}
	bs, err := e.reg.RetentionBlocks(e.ctx)
	if err != nil || len(bs) != 1 || bs[0].Source != "api" || bs[0].Message == "" {
		t.Fatalf("list %+v %v", bs, err)
	}
	if ok, _ := e.reg.ClearRetentionBlock(e.ctx, p.ID, "fable-5"); !ok || e.reg.RetentionBlocked(e.ctx, p.ID, "fable-5") {
		t.Fatal("cleared")
	}
	e.reg.MarkRetentionRequired(e.ctx, p.ID, "fable-5", "api", "x")
	if err := e.reg.Delete(e.ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if bs, _ := e.reg.RetentionBlocks(e.ctx); len(bs) != 0 {
		t.Fatalf("the provider's marks go with it: %+v", bs)
	}
}

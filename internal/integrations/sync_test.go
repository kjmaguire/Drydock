package integrations_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/integrations/itfixture"
)

const linearToken = "lin_api_Zk3Pq9TvL2mXw8RaB5nYd7CjHs4UeF6g"

func linearFixture(t *testing.T, n int) (*itfixture.Linear, string) {
	t.Helper()
	fx := itfixture.NewLinear(linearToken)
	ts, url := itfixture.Serve(fx.Server(), &fx.Common)
	t.Cleanup(ts.Close)
	for i := 0; i < n; i++ {
		at := baseTime.Add(-time.Duration(i) * time.Minute)
		fx.Put(itfixture.LinearIssue{ID: fmt.Sprintf("uuid-%04d", i), Identifier: fmt.Sprintf("ENG-%d", 1000-i), Title: fmt.Sprintf("Issue number %d", i),
			Description: fmt.Sprintf("Description of issue %d", i), URL: fmt.Sprintf("https://linear.example/ENG-%d", 1000-i), State: "Todo", Assignee: "Ada", Team: "ENG",
			Priority: 3, Labels: []string{"bug"}, Created: at.Add(-time.Hour), Updated: at})
	}
	return fx, url
}

func TestSyncCursorsDedupeAndWatermark(t *testing.T) {
	h := newHarness(t, nil)
	fx, url := linearFixture(t, 120)
	c := h.connect(integrations.KindLinear, linearToken, url, map[string]string{"team": "ENG"})

	r1 := h.sync(c.ID, integrations.SyncOpts{})
	if r1.Pages != 3 || r1.Seen != 120 || r1.New != 120 || !r1.Complete {
		t.Fatalf("first sync: %+v", r1)
	}
	if got := len(h.items(c.ID)); got != 120 {
		t.Fatalf("stored %d items", got)
	}
	if got := fx.Calls("list_issues"); got != 3 {
		t.Fatalf("list calls %d", got)
	}
	// the team filter from the connection reached the server
	if fx.ListArgs()[0]["team"] != "ENG" {
		t.Fatalf("args %v", fx.ListArgs()[0])
	}

	// second sync: nothing changed. It reads the first page only (everything on it older than the watermark stops the walk)
	// and creates nothing.
	r2 := h.sync(c.ID, integrations.SyncOpts{})
	if r2.New != 0 || r2.Changed != 0 || r2.Pages != 1 || !r2.Complete {
		t.Fatalf("second sync: %+v", r2)
	}
	if got := fx.Calls("list_issues"); got != 4 {
		t.Fatalf("list calls %d, want 4", got)
	}
	if got := h.count(`SELECT COUNT(*) FROM external_items WHERE integration_id=?`, c.ID); got != 120 {
		t.Fatalf("rows %d", got)
	}

	// one issue changes and a new one appears: exactly those two are written, no duplicates
	old := fx.Get("uuid-0003")
	old.Title = "Issue number 3, now with a better title"
	old.Updated = baseTime.Add(time.Minute)
	fx.Put(old)
	fx.Put(itfixture.LinearIssue{ID: "uuid-new", Identifier: "ENG-2000", Title: "Brand new", Description: "fresh", State: "Todo", Team: "ENG", Priority: 1, Created: baseTime, Updated: baseTime.Add(2 * time.Minute)})
	r3 := h.sync(c.ID, integrations.SyncOpts{})
	if r3.New != 1 || r3.Changed != 1 {
		t.Fatalf("third sync: %+v", r3)
	}
	if got := h.count(`SELECT COUNT(*) FROM external_items WHERE integration_id=?`, c.ID); got != 121 {
		t.Fatalf("rows %d", got)
	}
	if n := len(h.events(integrations.EventItemUpserted)); n != 122 { // 120 + 1 new + 1 changed
		t.Fatalf("upsert events %d", n)
	}
	// the sync status the Connections page shows
	v, _ := h.svc.Get(context.Background(), c.ID)
	if v.Sync.State != "ok" || v.Sync.ItemsSeen != 121 || v.Sync.LastSuccessAt == 0 || v.Sync.LastError != "" || v.Status != integrations.StatusOK && v.Status != integrations.StatusUntested {
		t.Fatalf("view %+v", v.Sync)
	}
}

func TestRestartKeepsCursorAndResumesInterruptedSweep(t *testing.T) {
	h := newHarness(t, func(o *integrations.Options) { o.MaxPages = 1 }) // each run reads one page of 50
	fx, url := linearFixture(t, 120)
	c := h.connect(integrations.KindLinear, linearToken, url, nil)

	r1 := h.sync(c.ID, integrations.SyncOpts{})
	if r1.Complete || r1.Seen != 50 {
		t.Fatalf("expected an interrupted sweep: %+v", r1)
	}
	v, _ := h.svc.Get(context.Background(), c.ID)
	if !v.Sync.Resuming {
		t.Fatal("the connection should report an interrupted sweep that will resume")
	}

	// the app restarts: a new Service over the same database continues from the stored cursor and re-reads nothing
	h.restart()
	r2 := h.sync(c.ID, integrations.SyncOpts{})
	if r2.Seen != 50 || r2.New != 50 || r2.Complete {
		t.Fatalf("resume 1: %+v", r2)
	}
	args := fx.ListArgs()
	if args[1]["cursor"] != "c50" {
		t.Fatalf("the restarted sync did not resume from the stored cursor: %v", args[1])
	}
	h.restart()
	r3 := h.sync(c.ID, integrations.SyncOpts{})
	if !r3.Complete || r3.New != 20 {
		t.Fatalf("resume 2: %+v", r3)
	}
	if got := h.count(`SELECT COUNT(*) FROM external_items WHERE integration_id=?`, c.ID); got != 120 {
		t.Fatalf("rows %d", got)
	}
	// after another restart the watermark is kept: a sync reads one page and finds nothing new
	h.restart()
	before := fx.Calls("list_issues")
	r4 := h.sync(c.ID, integrations.SyncOpts{})
	if r4.New != 0 || r4.Changed != 0 || fx.Calls("list_issues") != before+1 || !r4.Complete {
		t.Fatalf("after restart: %+v calls %d->%d", r4, before, fx.Calls("list_issues"))
	}
}

func TestSchedulerRespectsIntervalAndBackoff(t *testing.T) {
	h := newHarness(t, nil)
	_, url := linearFixture(t, 3)
	c := h.connect(integrations.KindLinear, linearToken, url, nil)
	ctx := context.Background()
	if ran := h.svc.Tick(ctx); len(ran) != 1 {
		t.Fatalf("first tick %v", ran)
	}
	if ran := h.svc.Tick(ctx); len(ran) != 0 {
		t.Fatalf("a connection polled a moment ago must not run again: %v", ran)
	}
	h.clk.Advance(3 * time.Minute) // Linear's default interval is 120 s (plus up to 10% jitter)
	if ran := h.svc.Tick(ctx); len(ran) != 1 || ran[0] != c.ID {
		t.Fatalf("tick after the interval %v", ran)
	}
	// a disabled connection is never polled
	cv, _ := h.svc.Get(ctx, c.ID)
	if _, err := h.svc.Connect(ctx, human, integrations.ConnectReq{ID: c.ID, Name: cv.Name, ProjectID: "prj1", BaseURL: url, PollIntervalS: 60}); err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(3 * time.Minute)
	if ran := h.svc.Tick(ctx); len(ran) != 1 {
		t.Fatalf("%v", ran)
	}
}

func TestNotionEtagSkipsBodyFetchAndUnsortedResultsAreFullyWalked(t *testing.T) {
	h := newHarness(t, nil)
	fx := itfixture.NewNotion("ntn_Qw3Er5Ty7Ui9Op1As2Df4Gh6Jk8Lz0Xc")
	ts, url := itfixture.Serve(fx.Server(), &fx.Common)
	t.Cleanup(ts.Close)
	for i := 0; i < 6; i++ {
		fx.Put(itfixture.NotionPage{ID: fmt.Sprintf("page-%d", i), Title: fmt.Sprintf("Spec %d", i), URL: fmt.Sprintf("https://notion.example/page-%d", i),
			Text: fmt.Sprintf("# Spec %d\n\nThe system shall do thing %d.", i, i), Edited: baseTime.Add(-time.Duration(i) * time.Hour)})
	}
	c := h.connect(integrations.KindNotion, "ntn_Qw3Er5Ty7Ui9Op1As2Df4Gh6Jk8Lz0Xc", url, nil)
	r1 := h.sync(c.ID, integrations.SyncOpts{})
	if r1.New != 6 || fx.Calls("notion-fetch") != 6 {
		t.Fatalf("%+v fetches %d", r1, fx.Calls("notion-fetch"))
	}
	// nothing edited: the vendor's last_edited_time is the validator, so no body is fetched again
	r2 := h.sync(c.ID, integrations.SyncOpts{})
	if r2.New != 0 || r2.Changed != 0 || fx.Calls("notion-fetch") != 6 {
		t.Fatalf("%+v fetches %d", r2, fx.Calls("notion-fetch"))
	}
	// one page edited: exactly one more fetch
	p := fx.Get("page-2")
	p.Text += "\n\nAnd also thing X."
	p.Edited = baseTime.Add(time.Minute)
	fx.Put(p)
	r3 := h.sync(c.ID, integrations.SyncOpts{})
	if r3.Changed != 1 || fx.Calls("notion-fetch") != 7 {
		t.Fatalf("%+v fetches %d", r3, fx.Calls("notion-fetch"))
	}
	// the stored body is the sanitized text of the page
	var found bool
	for _, it := range h.items(c.ID) {
		if it.ExternalID == "page-2" {
			d, err := h.svc.ItemDetail(context.Background(), it.ID)
			if err != nil || !contains(d.Body, "And also thing X.") {
				t.Fatalf("detail %v %q", err, d.Body)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("page-2 missing")
	}

	// a server that does not return pages newest first must not make the sync skip pages: with an unsorted listing the walk
	// does not stop at the watermark, so an edit that sits behind older pages is still found
	fx.Unsorted = true
	for i := 6; i < 12; i++ {
		fx.Put(itfixture.NotionPage{ID: fmt.Sprintf("page-%d", i), Title: fmt.Sprintf("Spec %d", i), Text: "x", Edited: baseTime.Add(-time.Duration(i) * time.Hour)})
	}
	old := fx.Get("page-11")
	old.Edited = baseTime.Add(2 * time.Minute)
	old.Text = "edited behind the scrambled order"
	fx.Put(old)
	r4 := h.sync(c.ID, integrations.SyncOpts{})
	if r4.New != 5 || r4.Changed != 1 {
		t.Fatalf("unsorted: %+v", r4)
	}
}

func TestFullSweepMarksMissingItemsGone(t *testing.T) {
	h := newHarness(t, nil)
	fx, url := linearFixture(t, 5)
	c := h.connect(integrations.KindLinear, linearToken, url, nil)
	h.sync(c.ID, integrations.SyncOpts{})
	fx.Delete("uuid-0004")
	r := h.sync(c.ID, integrations.SyncOpts{Full: true})
	if r.Gone != 1 || !r.Complete {
		t.Fatalf("%+v", r)
	}
	if got := h.count(`SELECT COUNT(*) FROM external_items WHERE integration_id=? AND gone=0`, c.ID); got != 4 {
		t.Fatalf("live rows %d", got)
	}
	// a normal sync never marks anything gone: deletion is only detectable by a sweep
	fx.Delete("uuid-0003")
	if r := h.sync(c.ID, integrations.SyncOpts{}); r.Gone != 0 {
		t.Fatalf("%+v", r)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

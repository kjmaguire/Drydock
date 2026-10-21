package integrations_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/integrations/itfixture"
)

// enableRule authors a rule matching every item of a project and takes it out of dry-run.
func enableRule(t *testing.T, h *harness, connID, projectID string) {
	t.Helper()
	ctx := context.Background()
	r, err := h.svc.SetRule(ctx, human, integrations.RuleReq{IntegrationID: connID, ProjectID: projectID, Name: "everything", When: `true`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SetRule(ctx, human, integrations.RuleReq{ID: r.ID, Version: r.Version, Enabled: bptr(true), DryRun: bptr(false)}); err != nil {
		t.Fatal(err)
	}
}

func openItemRefs(t *testing.T, h *harness, connID string) map[string]integrations.Item {
	t.Helper()
	out := map[string]integrations.Item{}
	for _, it := range h.items(connID) {
		out[it.IssueRef] = it
	}
	return out
}

// GitHub: an issue closed after it was synced arrives as an update (state=all with the since watermark), leaves the open set,
// cannot be imported and is not taken by a rule; reopened, it is back.
func TestAGitHubIssueClosedUpstreamLeavesTheOpenSet(t *testing.T) {
	const ghTok = "gho_fixture_token_never_stored_by_drydock"
	fx := itfixture.NewGitHub(ghTok, "acme/web")
	ts, url := itfixture.Serve(fx.Server())
	t.Cleanup(ts.Close)
	h := newHarness(t, nil)
	h.gh = host.HTTPAPI{BaseURL: url, Token: ghTok}
	ctx := context.Background()
	issues := []itfixture.GitHubIssue{
		{Number: 1, Title: "Checkout fails", Updated: baseTime.Add(-time.Hour)},
		{Number: 2, Title: "Login is slow", Updated: baseTime.Add(-2 * time.Hour)},
		{Number: 3, Title: "Closed long ago", Updated: baseTime.Add(-30 * time.Hour), Closed: true},
	}
	fx.Set(issues)
	c, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindGitHub})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "web"}); err != nil {
		t.Fatal(err)
	}
	if r := h.sync(c.ID, integrations.SyncOpts{}); r.New != 2 || fx.ListArgs()[0]["state"] != "open" {
		t.Fatalf("%+v %v", r, fx.ListArgs())
	}
	one := openItemRefs(t, h, c.ID)["#1"]
	if one.ID == "" || one.State != "open" {
		t.Fatalf("%+v", h.items(c.ID))
	}

	// #1 is closed on GitHub (which updates it); the next sync asks for every state since the watermark
	h.clk.Advance(2 * time.Hour)
	issues[0].Closed, issues[0].Updated = true, baseTime.Add(time.Hour)
	fx.Set(issues)
	r := h.sync(c.ID, integrations.SyncOpts{})
	if a := fx.ListArgs()[1]; a["state"] != "all" || a["since"] == "" {
		t.Fatalf("%v", fx.ListArgs())
	}
	if r.Gone != 1 || r.New != 0 {
		t.Fatalf("%+v", r)
	}
	open := openItemRefs(t, h, c.ID)
	if _, ok := open["#1"]; ok || len(open) != 1 {
		t.Fatalf("a closed issue is still offered: %+v", open)
	}
	if st, _ := h.svc.Store().GetItem(ctx, one.ID); !st.Gone || st.State != "closed" {
		t.Fatalf("%+v", st)
	}
	if n := h.count(`SELECT COUNT(*) FROM external_items WHERE external_id='acme/web#3'`); n != 0 {
		t.Fatal("an issue closed before Drydock ever saw it was stored")
	}
	if _, err := h.svc.Import(ctx, human, one.ID, ""); !errors.Is(err, integrations.ErrNotImportable) {
		t.Fatalf("a closed issue was imported: %v", err)
	}
	// a rule over every item takes only the open one
	enableRule(t, h, c.ID, "web")
	h.clk.Advance(time.Hour)
	h.sync(c.ID, integrations.SyncOpts{})
	if tasks := h.taskList(); len(tasks) != 1 || tasks[0].IssueRef != "#2" {
		t.Fatalf("%+v", tasks)
	}

	// reopened: it is open work again
	h.clk.Advance(time.Hour)
	issues[0].Closed, issues[0].Updated = false, baseTime.Add(4*time.Hour)
	fx.Set(issues)
	h.sync(c.ID, integrations.SyncOpts{})
	if it, ok := openItemRefs(t, h, c.ID)["#1"]; !ok || it.State != "open" {
		t.Fatalf("%+v", h.items(c.ID))
	}
}

// Sentry: resolving an issue does not change its lastSeen, so only a sweep of the unresolved set can see it. A resolved issue
// leaves the open set, cannot be imported and is not taken by a rule.
func TestASentryIssueResolvedUpstreamLeavesTheOpenSet(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	fx, url := sentryFixture(t)
	putSentry(fx, "s1", "error", "web", 50, 1)
	putSentry(fx, "s2", "error", "web", 40, 2)
	c := h.connect(integrations.KindSentry, sentryToken, url, map[string]string{"organization": "acme"})
	if r := h.sync(c.ID, integrations.SyncOpts{}); r.New != 2 {
		t.Fatalf("%+v", r)
	}
	var s1 integrations.Item
	for _, it := range h.items(c.ID) {
		if it.ExternalID == "s1" {
			s1 = it
		}
	}

	// resolved in Sentry: its status changes, its lastSeen does not
	is := fx.Get("s1")
	is.Status = "resolved"
	fx.Put(is)
	h.clk.Advance(10 * time.Minute)
	r := h.sync(c.ID, integrations.SyncOpts{})
	if r.Gone != 1 || !r.Complete {
		t.Fatalf("%+v", r)
	}
	if its := h.items(c.ID); len(its) != 1 || its[0].ExternalID != "s2" {
		t.Fatalf("a resolved issue is still offered: %+v", its)
	}
	if _, err := h.svc.Import(ctx, human, s1.ID, ""); !errors.Is(err, integrations.ErrNotImportable) {
		t.Fatalf("a resolved issue was imported: %v", err)
	}
	enableRule(t, h, c.ID, "prj1")
	h.clk.Advance(10 * time.Minute)
	h.sync(c.ID, integrations.SyncOpts{})
	if tasks := h.taskList(); len(tasks) != 1 || tasks[0].IssueRef != "WEB-S2" {
		t.Fatalf("%+v", tasks)
	}

	// a query that lists resolved issues too: a resolved one is still not open work
	is2 := fx.Get("s2")
	is2.Status = "resolved"
	fx.Put(is2)
	putSentry(fx, "s3", "error", "web", 10, 0)
	if _, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "prj1", Config: map[string]string{"organization": "acme", "query": "level:error"}}); err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(10 * time.Minute)
	h.sync(c.ID, integrations.SyncOpts{})
	if its := h.items(c.ID); len(its) != 1 || its[0].ExternalID != "s3" {
		t.Fatalf("%+v", its)
	}
}

package integrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/integrations/itfixture"
)

// assignmentView returns the view of one assignment of a connection.
func assignmentView(t *testing.T, h *harness, connID, asgID string) integrations.AssignmentView {
	t.Helper()
	v, err := h.svc.Get(context.Background(), connID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range v.Assignments {
		if a.ID == asgID {
			return a
		}
	}
	t.Fatalf("assignment %s not found in %+v", asgID, v.Assignments)
	return integrations.AssignmentView{}
}

// A 403 means the credential works but may not read one project's scope. Only that assignment fails; the shared connection
// stays OK and the scheduler keeps syncing the other projects (a 401 is the credential itself and still pauses everything).
func TestAForbiddenScopeFailsOnlyItsAssignment(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	fx, url := linearFixture(t, 2)
	fx.Put(itfixture.LinearIssue{ID: "ops-1", Identifier: "OPS-1", Title: "Rotate the certificates", State: "Todo", Team: "OPS", Created: baseTime, Updated: baseTime})
	c, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindLinear, Token: linearToken, BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}
	a, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "web", Config: map[string]string{"team": "ENG"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "ops", Config: map[string]string{"team": "OPS"}})
	if err != nil {
		t.Fatal(err)
	}
	// the key may not see the OPS team: Linear answers a "forbidden" GraphQL error for that scope only
	fx.FailWhen("team", "OPS", itfixture.LinearForbidden())

	if ran := h.svc.Tick(ctx); len(ran) != 1 {
		t.Fatalf("first tick %v", ran)
	}
	v, _ := h.svc.Get(ctx, c.ID)
	if v.Status != integrations.StatusOK || v.StatusDetail != "" {
		t.Fatalf("one project's missing permission changed the connection: %s %q", v.Status, v.StatusDetail)
	}
	bv := assignmentView(t, h, c.ID, b.ID)
	if bv.Sync.State != "forbidden" || bv.Sync.LastErrorCode != integrations.CodeForbidden || !strings.Contains(bv.Sync.LastError, "lacks a permission") {
		t.Fatalf("B must show the permission error: %+v", bv.Sync)
	}
	av := assignmentView(t, h, c.ID, a.ID)
	if av.Sync.State != "ok" || av.Sync.ItemsSeen != 2 {
		t.Fatalf("A must have synced: %+v", av.Sync)
	}
	if len(h.events(integrations.EventAuthExpired)) != 0 {
		t.Fatal("a 403 was reported as an expired credential")
	}

	// later: A is polled again (the connection is not paused); B is retried after its backoff and still refused
	fx.Put(itfixture.LinearIssue{ID: "eng-new", Identifier: "ENG-2000", Title: "New", State: "Todo", Team: "ENG", Created: baseTime, Updated: baseTime.Add(5 * time.Minute)})
	h.clk.Advance(5 * time.Minute)
	if ran := h.svc.Tick(ctx); len(ran) != 1 {
		t.Fatalf("the connection was not synced again: %v", ran)
	}
	if av := assignmentView(t, h, c.ID, a.ID); av.Sync.ItemsSeen != 3 || av.Sync.State != "ok" {
		t.Fatalf("A stopped syncing: %+v", av.Sync)
	}
	if bv := assignmentView(t, h, c.ID, b.ID); bv.Sync.State != "forbidden" || bv.Sync.ConsecutiveFailures != 2 {
		t.Fatalf("%+v", bv.Sync)
	}

	// the permission is granted: B recovers on its next sync
	fx.ClearFailWhen()
	if _, err := h.svc.SyncAssignment(ctx, b.ID, integrations.SyncOpts{Force: true}); err != nil {
		t.Fatal(err)
	}
	if bv := assignmentView(t, h, c.ID, b.ID); bv.Sync.State != "ok" || bv.Sync.ItemsSeen != 1 {
		t.Fatalf("%+v", bv.Sync)
	}
}

// The same rule for the REST adapters: an HTTP 403 (here Notion's restricted_resource for one data source) is that
// assignment's failure, never a paused connection.
func TestAnHTTP403IsAForbiddenScopeNotARevokedCredential(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	const tok = "ntn_Qw3Er5Ty7Ui9Op1As2Df4Gh6Jk8Lz0Xc"
	fx := itfixture.NewNotion(tok)
	ts, url := itfixture.Serve(fx.Server())
	t.Cleanup(ts.Close)
	fx.Put(itfixture.NotionPage{ID: "page-1", Title: "Spec", Text: "x", Edited: baseTime, DataSource: "ds-1"})
	fx.Put(itfixture.NotionPage{ID: "page-2", Title: "Secret plan", Text: "y", Edited: baseTime, DataSource: "ds-2"})
	c, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindNotion, Token: tok, BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "web", Config: map[string]string{"data_source": "ds-1"}})
	b, _ := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "ops", Config: map[string]string{"data_source": "ds-2"}})
	fx.FailWhen("data_source", "ds-2", itfixture.NotionRestricted())
	_, err = h.svc.Sync(ctx, c.ID, integrations.SyncOpts{})
	var f *integrations.Failure
	if !errors.As(err, &f) || f.Code != integrations.CodeForbidden || !strings.Contains(f.Message, "HTTP 403, restricted_resource") {
		t.Fatalf("%v", err)
	}
	v, _ := h.svc.Get(ctx, c.ID)
	if v.Status == integrations.StatusUnauthenticated || v.StatusDetail != "" {
		t.Fatalf("%s %q", v.Status, v.StatusDetail)
	}
	if av := assignmentView(t, h, c.ID, a.ID); av.Sync.State != "ok" || av.Sync.ItemsSeen != 1 {
		t.Fatalf("%+v", av.Sync)
	}
	if bv := assignmentView(t, h, c.ID, b.ID); bv.Sync.State != "forbidden" {
		t.Fatalf("%+v", bv.Sync)
	}
	// a 401, by contrast, is the credential: the connection pauses
	fx.Revoke(true)
	h.clk.Advance(time.Hour)
	_, err = h.svc.SyncAssignment(ctx, a.ID, integrations.SyncOpts{Force: true})
	if !errors.As(err, &f) || f.Code != integrations.CodeUnauthorized {
		t.Fatalf("%v", err)
	}
	if v, _ := h.svc.Get(ctx, c.ID); v.Status != integrations.StatusUnauthenticated {
		t.Fatalf("%s", v.Status)
	}
}

// GitHub through gh: a 403 that is not a rate limit is a forbidden scope, a 404 is a repository it cannot find; neither is a
// gh sign-out.
func TestGitHubForbiddenAndNotFoundAreAssignmentFailures(t *testing.T) {
	const ghTok = "gho_fixture_token_never_stored_by_drydock"
	fx := itfixture.NewGitHub(ghTok, "acme/web")
	ts, url := itfixture.Serve(fx.Server())
	t.Cleanup(ts.Close)
	fx.Set([]itfixture.GitHubIssue{{Number: 1, Title: "One", Labels: []string{"bug"}, Updated: baseTime}})
	h := newHarness(t, nil)
	h.gh = host.HTTPAPI{BaseURL: url, Token: ghTok}
	ctx := context.Background()
	c, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindGitHub})
	if err != nil {
		t.Fatal(err)
	}
	ok, _ := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "web", Config: map[string]string{"repo": "acme/web", "labels": "bug"}})
	locked, _ := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "locked", Config: map[string]string{"repo": "acme/web", "labels": "internal"}})
	missing, _ := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "gone", Config: map[string]string{"repo": "acme/private"}})
	// GitHub's 403 for a resource the token may not read (no rate-limit headers: it is not a rate limit)
	fx.FailWhen("labels", "internal", itfixture.Fail{Status: 403, Body: `{"message":"Resource not accessible by integration","documentation_url":"https://docs.github.com/rest"}`})
	if _, err := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{}); err == nil {
		t.Fatal("expected the forbidden and missing assignments to fail")
	}
	v, _ := h.svc.Get(ctx, c.ID)
	if v.Status == integrations.StatusUnauthenticated || v.StatusDetail != "" {
		t.Fatalf("%s %q", v.Status, v.StatusDetail)
	}
	if a := assignmentView(t, h, c.ID, ok.ID); a.Sync.State != "ok" || a.Sync.ItemsSeen != 1 {
		t.Fatalf("%+v", a.Sync)
	}
	if a := assignmentView(t, h, c.ID, locked.ID); a.Sync.LastErrorCode != integrations.CodeForbidden || a.Sync.State != "forbidden" {
		t.Fatalf("%+v", a.Sync)
	}
	if a := assignmentView(t, h, c.ID, missing.ID); a.Sync.LastErrorCode != integrations.CodeNotFound || !strings.Contains(a.Sync.LastError, "did not find the repository") {
		t.Fatalf("%+v", a.Sync)
	}
	// text from a tool is classified the same way: 403 is a permission, 401 the credential
	if f := integrations.Classify(integrations.KindGitHub, errors.New("gh: HTTP 403: Resource not accessible by integration"), ""); f.Code != integrations.CodeForbidden {
		t.Errorf("%+v", f)
	}
	if f := integrations.Classify(integrations.KindGitHub, errors.New("HTTP 401: Bad credentials (invalid token)"), ""); f.Code != integrations.CodeUnauthorized {
		t.Errorf("%+v", f)
	}
}

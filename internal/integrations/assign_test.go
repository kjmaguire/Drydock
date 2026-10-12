package integrations_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/integrations/itfixture"
)

// Decision P42: a service is connected once, without a project, and assigned to projects separately; one connection serves
// several projects, each with its own scope, cursor, items and rules.
func TestConnectWithoutAProjectThenAssignToTwoProjects(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	fx, url := linearFixture(t, 3)
	fx.Put(itfixture.LinearIssue{ID: "ops-1", Identifier: "OPS-1", Title: "Rotate the certificates", State: "Todo", Team: "OPS", Created: baseTime, Updated: baseTime})

	// connected at the workspace level: no project, nothing to sync yet, the name is the vendor's
	c, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindLinear, Token: linearToken, BaseURL: url, Name: "a name nobody can set"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Linear" || c.ProjectID != "" || len(c.Assignments) != 0 {
		t.Fatalf("%+v", c)
	}
	if r := h.sync(c.ID, integrations.SyncOpts{}); r.Skipped != "not assigned to a project" || r.Seen != 0 || fx.Calls("issues") != 0 {
		t.Fatalf("%+v", r)
	}
	if rep, _ := h.svc.Test(ctx, c.ID); !rep.OK {
		t.Fatalf("an unassigned connection must still test: %+v", rep)
	}
	// the screen lists the teams to choose from, read from Linear
	teams, err := h.svc.Choices(ctx, c.ID, "team", nil)
	if err != nil || len(teams) != 2 || teams[0].Value != "ENG" || teams[0].Label != "ENG · Engineering" {
		t.Fatalf("%+v %v", teams, err)
	}

	// assigning is a human's action
	if _, err := h.svc.Assign(ctx, "agent:x", integrations.AssignReq{IntegrationID: c.ID, ProjectID: "web"}); !errors.Is(err, integrations.ErrNotHuman) {
		t.Fatalf("%v", err)
	}
	if _, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "web", Config: map[string]string{"repo": "x"}}); !errors.Is(err, integrations.ErrBadRequest) {
		t.Fatalf("a GitHub setting on Linear: %v", err)
	}
	web, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "web", Config: map[string]string{"team": "ENG"}})
	if err != nil {
		t.Fatal(err)
	}
	ops, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "ops", Config: map[string]string{"team": "OPS"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.events(integrations.EventAssigned)) != 2 {
		t.Fatal("no assigned events")
	}
	r := h.sync(c.ID, integrations.SyncOpts{})
	if r.New != 4 || !r.Complete {
		t.Fatalf("%+v", r)
	}
	// each project sees its own team's items
	webItems, _ := h.svc.Items(ctx, integrations.ItemFilter{ProjectID: "web"})
	opsItems, _ := h.svc.Items(ctx, integrations.ItemFilter{AssignmentID: ops.ID})
	if len(webItems) != 3 || len(opsItems) != 1 || opsItems[0].IssueRef != "OPS-1" || opsItems[0].ProjectID != "ops" {
		t.Fatalf("web %d ops %+v", len(webItems), opsItems)
	}
	// an import lands in the item's own project
	if _, err := h.svc.Import(ctx, human, opsItems[0].ID, ""); err != nil {
		t.Fatal(err)
	}
	if tk := h.taskList()[0]; tk.ProjectID != "ops" || tk.IssueRef != "OPS-1" {
		t.Fatalf("%+v", tk)
	}
	v, _ := h.svc.Get(ctx, c.ID)
	seen := map[string]int{}
	for _, a := range v.Assignments {
		seen[a.ProjectID] = a.Sync.ItemsSeen
	}
	if len(v.Assignments) != 2 || seen["web"] != 3 || seen["ops"] != 1 || v.Sync.ItemsSeen != 4 {
		t.Fatalf("%+v", v.Assignments)
	}

	// the same team assigned to a second project: that project gets its own copy, keyed apart
	both, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "docs", Config: map[string]string{"team": "OPS"}})
	if err != nil {
		t.Fatal(err)
	}
	h.sync(c.ID, integrations.SyncOpts{})
	if n := h.count(`SELECT COUNT(*) FROM external_items WHERE external_id='ops-1'`); n != 2 {
		t.Fatalf("ops-1 stored %d times", n)
	}

	// a changed scope restarts that assignment's cursor; a stale version is refused
	if _, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "web", Config: map[string]string{"team": "OPS"}, Version: 99}); !errors.Is(err, integrations.ErrConflict) {
		t.Fatalf("%v", err)
	}
	moved, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "web", Config: map[string]string{"team": "OPS"}, Version: web.Version})
	if err != nil || moved.Version != web.Version+1 || moved.Sync.Watermark != 0 || moved.Sync.ItemsSeen != 0 {
		t.Fatalf("%+v %v", moved, err)
	}

	// unassigning removes the project's scope, cursor, rules and the items that never became tasks; the imported item stays
	if _, err := h.svc.SetRule(ctx, human, integrations.RuleReq{IntegrationID: c.ID, ProjectID: "ops", Name: "ops", When: `true`}); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Unassign(ctx, "agent:x", ops.ID); !errors.Is(err, integrations.ErrNotHuman) {
		t.Fatalf("%v", err)
	}
	if err := h.svc.Unassign(ctx, human, ops.ID); err != nil {
		t.Fatal(err)
	}
	if n := h.count(`SELECT COUNT(*) FROM external_items WHERE assignment_id=? AND gone=1 AND task_id<>''`, ops.ID); n != 1 {
		t.Fatalf("the imported item must stay (gone) with its task: %d", n)
	}
	if n := h.count(`SELECT COUNT(*) FROM integration_rules WHERE project_id='ops'`); n != 0 {
		t.Fatal("the project's rules stayed")
	}
	if n := h.count(`SELECT COUNT(*) FROM integration_cursors WHERE scope=?`, ops.ID); n != 0 {
		t.Fatal("the cursor stayed")
	}
	v, _ = h.svc.Get(ctx, c.ID)
	if len(v.Assignments) != 2 || (v.Assignments[0].ID != both.ID && v.Assignments[1].ID != both.ID) || !v.HasCredential {
		t.Fatalf("unassigning must leave the connection and its other assignments: %+v", v.Assignments)
	}
	// a rule needs its project assigned
	if _, err := h.svc.SetRule(ctx, human, integrations.RuleReq{IntegrationID: c.ID, ProjectID: "ops", Name: "x", When: `true`}); !errors.Is(err, integrations.ErrNotAssigned) {
		t.Fatalf("%v", err)
	}
}

func TestSentryAssignmentNeedsAnOrganizationAndListsProjects(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	fx, url := sentryFixture(t)
	putSentry(fx, "w1", "error", "web", 10, 1)
	putSentry(fx, "a1", "error", "api", 10, 2)
	c, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindSentry, Token: sentryToken, BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "prj1"}); !errors.Is(err, integrations.ErrBadRequest) {
		t.Fatalf("a Sentry assignment without an organization: %v", err)
	}
	orgs, _ := h.svc.Choices(ctx, c.ID, "organization", nil)
	projs, err := h.svc.Choices(ctx, c.ID, "project", map[string]string{"organization": "acme"})
	if len(orgs) != 1 || orgs[0].Value != "acme" || err != nil || len(projs) != 2 {
		t.Fatalf("%+v %+v %v", orgs, projs, err)
	}
	// the issues endpoint filters by project id, so a project choice's value is the id and its label the name
	var api integrations.Choice
	for _, p := range projs {
		if p.Label == "API" {
			api = p
		}
	}
	if api.Value != fx.ProjectID("api") || api.Value == "" {
		t.Fatalf("%+v", projs)
	}
	if _, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "prj1", Config: map[string]string{"organization": "acme", "project": api.Value}}); err != nil {
		t.Fatal(err)
	}
	r := h.sync(c.ID, integrations.SyncOpts{})
	if r.New != 1 || fx.ListArgs()[0]["project"] != api.Value || fx.ListArgs()[0]["query"] != "is:unresolved" || fx.ListArgs()[0]["sort"] != "date" {
		t.Fatalf("%+v %v", r, fx.ListArgs())
	}
	if its := h.items(c.ID); len(its) != 1 || its[0].ExternalID != "a1" || its[0].Meta["project"] != "api" {
		t.Fatalf("only the chosen project's issues: %+v", its)
	}
}

// An assignment made when Drydock sent the project's slug still syncs: the slug is looked up once in the organization's
// projects and the id is stored on the assignment. A slug that is no longer there fails that assignment in plain words.
func TestSentryLegacyProjectSlugIsResolvedToItsIDOnce(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	fx, url := sentryFixture(t)
	putSentry(fx, "w1", "error", "web", 10, 1)
	putSentry(fx, "a1", "error", "api", 10, 2)
	c, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindSentry, Token: sentryToken, BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}
	// the stored scope of an older assignment: a slug, which Sentry's issues endpoint refuses (the fixture answers 400 like it)
	a, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "prj1", Config: map[string]string{"organization": "acme", "project": "web"}})
	if err != nil {
		t.Fatal(err)
	}
	r := h.sync(c.ID, integrations.SyncOpts{})
	webID := fx.ProjectID("web")
	if r.New != 1 || fx.ListArgs()[0]["project"] != webID || fx.Calls("projects") != 1 {
		t.Fatalf("%+v %v projects %d", r, fx.ListArgs(), fx.Calls("projects"))
	}
	if its := h.items(c.ID); len(its) != 1 || its[0].ExternalID != "w1" {
		t.Fatalf("%+v", its)
	}
	av := assignmentView(t, h, c.ID, a.ID)
	if av.Config["project"] != webID || av.Sync.Watermark == 0 {
		t.Fatalf("the id was not stored, or the cursor was reset: %+v", av)
	}
	// the next sync needs no lookup
	h.clk.Advance(time.Hour)
	h.sync(c.ID, integrations.SyncOpts{})
	if fx.Calls("projects") != 1 || fx.ListArgs()[1]["project"] != webID {
		t.Fatalf("projects %d %v", fx.Calls("projects"), fx.ListArgs())
	}

	// a second older assignment names a project that was deleted since
	gone, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "prj2", Config: map[string]string{"organization": "acme", "project": "api"}})
	if err != nil {
		t.Fatal(err)
	}
	fx.RemoveProject("api")
	_, err = h.svc.SyncAssignment(ctx, gone.ID, integrations.SyncOpts{})
	var f *integrations.Failure
	if !errors.As(err, &f) || f.Code != integrations.CodeNotFound || !strings.Contains(f.Message, `The Sentry project "api"`) || !strings.Contains(f.Message, "Choose the Sentry project again") {
		t.Fatalf("%v", err)
	}
	if gv := assignmentView(t, h, c.ID, gone.ID); gv.Config["project"] != "api" || gv.Sync.LastErrorCode != integrations.CodeNotFound {
		t.Fatalf("%+v", gv)
	}
	if v, _ := h.svc.Get(ctx, c.ID); v.Status != integrations.StatusOK {
		t.Fatalf("one assignment's missing project changed the connection: %s", v.Status)
	}
}

// The fixture answers like Sentry: the issues endpoint takes project ids only.
func TestSentryFixtureRefusesAProjectSlug(t *testing.T) {
	fx, url := sentryFixture(t)
	for _, tc := range []struct {
		project string
		status  int
	}{{"web", 400}, {fx.ProjectID("web"), 200}, {"999999", 403}} {
		req, _ := http.NewRequest("GET", url+"/api/0/organizations/acme/issues/?project="+tc.project, nil)
		req.Header.Set("Authorization", "Bearer "+sentryToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Errorf("project=%s: HTTP %d, want %d", tc.project, resp.StatusCode, tc.status)
		}
	}
}

func TestGitHubUsesTheGhSignInAndImportsIssuesAsHashReferences(t *testing.T) {
	ghTok := "gho_fixture_token_never_stored_by_drydock"
	fx := itfixture.NewGitHub(ghTok, "acme/web")
	ts, url := itfixture.Serve(fx.Server())
	t.Cleanup(ts.Close)
	h := newHarness(t, nil)
	ctx := context.Background()
	fx.Set([]itfixture.GitHubIssue{
		{Number: 12, Title: "Checkout fails on Safari", Body: "Steps: ...", Labels: []string{"bug"}, Updated: baseTime},
		{Number: 13, Title: "A pull request, not an issue", Updated: baseTime, PullRequest: true},
		{Number: 9, Title: "Old idea", Updated: baseTime.Add(-48 * time.Hour)},
	})

	// gh is not installed: the connection says so in plain words
	c, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindGitHub})
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthKind != integrations.AuthGH || c.CredentialRef != "" || !c.HasCredential {
		t.Fatalf("%+v", c)
	}
	rep, _ := h.svc.Test(ctx, c.ID)
	if rep.OK || !strings.Contains(rep.Summary, "gh) is not installed") {
		t.Fatalf("%+v", rep)
	}
	// gh signed in (a REST client on the fixture stands in for gh api): the login is the identity
	h.gh = host.HTTPAPI{BaseURL: url, Token: ghTok}
	rep, _ = h.svc.Test(ctx, c.ID)
	if !rep.OK || rep.Identity.Account != "kyle-fixture" || rep.Identity.Method != "GitHub CLI (gh) sign-in" {
		t.Fatalf("%+v", rep)
	}
	// an assignment without a repository takes the project's own (its origin remote)
	a, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "web"})
	if err != nil || a.Config["repo"] != "acme/web" {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "web", Config: map[string]string{"repo": "../../etc"}}); !errors.Is(err, integrations.ErrBadRequest) {
		t.Fatalf("%v", err)
	}
	r := h.sync(c.ID, integrations.SyncOpts{})
	if r.New != 2 || !r.Complete {
		t.Fatalf("%+v", r)
	}
	if fx.ListArgs()[0]["sort"] != "updated" {
		t.Fatalf("%v", fx.ListArgs())
	}
	var issue integrations.Item
	for _, it := range h.items(c.ID) {
		if it.IssueRef == "#12" {
			issue = it
		}
		if strings.Contains(it.Title, "pull request") {
			t.Fatal("a pull request was stored as an issue")
		}
	}
	if issue.ID == "" || issue.Kind != integrations.ItemIssue || issue.ExternalID != "acme/web#12" {
		t.Fatalf("%+v", h.items(c.ID))
	}
	if _, err := h.svc.Import(ctx, human, issue.ID, ""); err != nil {
		t.Fatal(err)
	}
	if tk := h.taskList()[0]; tk.IssueRef != "#12" || tk.ProjectID != "web" || !strings.Contains(tk.Description, "> Title: Checkout fails on Safari") {
		t.Fatalf("%+v", tk)
	}
	// the next sync asks only for what changed since the watermark
	h.clk.Advance(time.Hour)
	h.sync(c.ID, integrations.SyncOpts{})
	if since := fx.ListArgs()[1]["since"]; since == "" {
		t.Fatalf("%v", fx.ListArgs())
	}
	// Drydock stored no GitHub token anywhere
	if strings.Contains(h.diskText(), ghTok) || len(h.cred.Keys()) != 0 {
		t.Fatal("a GitHub token was stored")
	}
	// a second GitHub connection is refused like any other
	if _, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindGitHub}); !errors.Is(err, integrations.ErrAlreadyConnected) {
		t.Fatalf("%v", err)
	}
}

func TestLinearSetStateAndNotionCommentGoThroughTheirAPIs(t *testing.T) {
	h, fx, c := linearWithComment(t)
	ctx := context.Background()
	o, err := h.svc.Propose(ctx, human, integrations.ProposeReq{IntegrationID: c.ID, Op: "set_state", Target: "uuid-0000", Params: map[string]string{"state": "Done"}})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Decide(ctx, human, o.ID, true)
	if out, err := h.svc.Execute(ctx, human, o.ID); err != nil || out.Receipt != "status Done" {
		t.Fatalf("%+v %v", out, err)
	}
	if got := fx.Get("uuid-0000").State; got != "Done" {
		t.Fatalf("state %s", got)
	}
	// a status the team does not have is a plain failure, after the approval was spent
	o2, _ := h.svc.Propose(ctx, human, integrations.ProposeReq{IntegrationID: c.ID, Op: "set_state", Target: "uuid-0000", Params: map[string]string{"state": "Shipped to Mars"}})
	h.svc.Decide(ctx, human, o2.ID, true)
	if out, err := h.svc.Execute(ctx, human, o2.ID); !errors.Is(err, integrations.ErrSendFailed) || out.Status != integrations.OutFailed || !strings.Contains(out.Error, "no status named") {
		t.Fatalf("%+v %v", out, err)
	}

	nfx := itfixture.NewNotion("ntn_Qw3Er5Ty7Ui9Op1As2Df4Gh6Jk8Lz0Xc")
	nts, nurl := itfixture.Serve(nfx.Server())
	t.Cleanup(nts.Close)
	nc := h.connect(integrations.KindNotion, "ntn_Qw3Er5Ty7Ui9Op1As2Df4Gh6Jk8Lz0Xc", nurl, nil)
	n, err := h.svc.Propose(ctx, human, integrations.ProposeReq{IntegrationID: nc.ID, Op: "comment", Target: "page-1", Params: map[string]string{"body": "Implemented in PR 4"}})
	if err != nil || n.Tool != "POST /v1/comments" {
		t.Fatalf("%+v %v", n, err)
	}
	h.svc.Decide(ctx, human, n.ID, true)
	if _, err := h.svc.Execute(ctx, human, n.ID); err != nil || len(nfx.Comments) != 1 {
		t.Fatalf("%v %v", err, nfx.Comments)
	}
	if !strings.Contains(jsonOf(nfx.Comments[0]), `"page_id":"page-1"`) {
		t.Fatalf("%v", nfx.Comments[0])
	}
}

func TestNotionDataSourceQueryAndVersionHeader(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	fx := itfixture.NewNotion("ntn_Qw3Er5Ty7Ui9Op1As2Df4Gh6Jk8Lz0Xc")
	ts, url := itfixture.Serve(fx.Server())
	t.Cleanup(ts.Close)
	fx.Put(itfixture.NotionPage{ID: "p1", Title: "In the database", Text: "row text", Edited: baseTime, DataSource: "ds-1"})
	fx.Put(itfixture.NotionPage{ID: "p2", Title: "Elsewhere", Text: "other", Edited: baseTime})
	c, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindNotion, Token: "ntn_Qw3Er5Ty7Ui9Op1As2Df4Gh6Jk8Lz0Xc", BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}
	rep, _ := h.svc.Test(ctx, c.ID)
	if !rep.OK || rep.Identity.Account != "Drydock fixture" || rep.Identity.Workspace != "Fixture Workspace" || rep.Identity.Method != "Internal integration" {
		t.Fatalf("%+v", rep)
	}
	ds, err := h.svc.Choices(ctx, c.ID, "data_source", nil)
	if err != nil || len(ds) != 1 || ds[0].Value != "ds-1" || ds[0].Label != "Requirements" {
		t.Fatalf("%+v %v", ds, err)
	}
	if _, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "prj1", Config: map[string]string{"data_source": "ds-1"}}); err != nil {
		t.Fatal(err)
	}
	r := h.sync(c.ID, integrations.SyncOpts{})
	if r.New != 1 || fx.Calls("query") != 1 || fx.Calls("search") != 0 {
		t.Fatalf("%+v", r)
	}
	if it := h.items(c.ID)[0]; it.Title != "In the database" || it.ExternalID != "p1" {
		t.Fatalf("%+v", it)
	}
	for _, v := range fx.HeaderSeen("Notion-Version") {
		if v != integrations.NotionVersion {
			t.Fatalf("Notion-Version %q", v)
		}
	}
	// Notion's 429 carries Retry-After in seconds
	fx.FailNext(itfixture.NotionRateLimited(42))
	_, err = h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Full: true})
	var f *integrations.Failure
	if !errors.As(err, &f) || f.Code != integrations.CodeRateLimited || f.RetryAfter != 42*time.Second {
		t.Fatalf("%v %+v", err, f)
	}
}

func TestSentryPagesThroughLinkCursorsAndFollowsItsRateLimitHeaders(t *testing.T) {
	h := newHarness(t, func(o *integrations.Options) { o.PageSize = 2 })
	ctx := context.Background()
	fx, url := sentryFixture(t)
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		putSentry(fx, id, "error", "web", 10, i)
	}
	c := h.connect(integrations.KindSentry, sentryToken, url, map[string]string{"organization": "acme"})
	r := h.sync(c.ID, integrations.SyncOpts{})
	if r.Pages != 3 || r.New != 5 || !r.Complete {
		t.Fatalf("%+v", r)
	}
	args := fx.ListArgs()
	if args[1]["cursor"] != "c2" || args[2]["cursor"] != "c4" {
		t.Fatalf("the Link header's next cursors were not followed: %v", args)
	}
	// the window is spent: X-Sentry-Rate-Limit-Remaining 0 until the reset, so the next request waits for it rather than
	// being refused (here the reset is far, so the sync backs off instead of sleeping)
	fx.SetRemaining(0, h.clk.Now().Add(10*time.Minute))
	calls := fx.Calls("issues")
	_, err := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Full: true}) // the first page's answer says the window is spent
	var f *integrations.Failure
	if !errors.As(err, &f) || f.Code != integrations.CodeRateLimited || fx.Calls("issues") != calls+1 || f.RetryAfter < 9*time.Minute {
		t.Fatalf("%v %+v calls %d -> %d", err, f, calls, fx.Calls("issues"))
	}
	if _, err := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Force: true, Full: true}); !errors.Is(err, integrations.ErrRateLimited) || fx.Calls("issues") != calls+1 {
		t.Fatalf("%v", err)
	}
	// Sentry's 429 with Retry-After
	h.clk.Advance(11 * time.Minute)
	fx.SetRemaining(39, h.clk.Now().Add(time.Second))
	fx.FailNext(itfixture.SentryRateLimited(7))
	_, err = h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Force: true, Full: true})
	if !errors.As(err, &f) || f.Code != integrations.CodeRateLimited {
		t.Fatalf("%v", err)
	}
}

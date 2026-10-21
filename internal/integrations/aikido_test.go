package integrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/integrations/itfixture"
)

const (
	aikidoClientID = "aik_client_Fixture01"
	aikidoSecret   = "AIK_Xv7Bn2Mq9Lc4Zr6Td8Wy1Hs3Gf5Jk0Pa"
)

// aikidoFixture starts the Aikido REST fixture and connects Aikido to it with client credentials, assigned to prj1.
func aikidoFixture(t *testing.T, h *harness, findings []itfixture.AikidoFinding, secret string) (*itfixture.Aikido, integrations.ConnectionView) {
	t.Helper()
	fx := itfixture.NewAikido(aikidoClientID, aikidoSecret)
	fx.Set(findings)
	ts, url := itfixture.Serve(fx.Server())
	t.Cleanup(ts.Close)
	c, err := h.svc.Connect(context.Background(), human, integrations.ConnectReq{Kind: integrations.KindAikido, ClientID: aikidoClientID, ClientSecret: secret, BaseURL: url, ProjectID: "prj1"})
	if err != nil {
		t.Fatal(err)
	}
	return fx, c
}

func finding(id int, sev, typ, rule, pkg string, minutesAgo int) itfixture.AikidoFinding {
	at := baseTime.Add(-time.Duration(minutesAgo) * time.Minute).Unix()
	return itfixture.AikidoFinding{ID: id, GroupID: id * 10, Severity: sev, Type: typ, Rule: rule, AffectedPackage: pkg, AffectedFile: "src/db.go", CodeRepoName: "web", CodeRepoID: 11,
		Status: "open", FirstDetectedAt: at}
}

func TestAikidoClientCredentialsSyncImportAndApprovedIgnore(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	findings := []itfixture.AikidoFinding{
		finding(101, "critical", "open_source", "CVE-2026-0001 in lodash", "lodash", 1),
		finding(102, "high", "sast", "SQL injection", "", 2),
		finding(103, "medium", "leaked_secret", "Hard-coded credential", "", 3),
		finding(104, "low", "iac", "Open security group", "", 4),
	}
	findings[2].AffectedFile = "config.go: " + ghToken // a finding's text can carry a secret
	fx, c := aikidoFixture(t, h, findings, aikidoSecret)

	rep, err := h.svc.Test(ctx, c.ID)
	if err != nil || !rep.OK {
		t.Fatalf("%+v %v", rep, err)
	}
	// the workspace and the token's scopes come from Aikido
	if rep.Identity.Workspace != "Fixture Security" || strings.Join(rep.Identity.Scopes, " ") != "issues:read issues:write repositories:read" || rep.Identity.Method != "REST API client credentials" {
		t.Fatalf("%+v", rep.Identity)
	}
	r := h.sync(c.ID, integrations.SyncOpts{})
	if r.New != 4 || !r.Complete {
		t.Fatalf("%+v", r)
	}
	// one token was bought and reused for every call
	if fx.TokenCalls != 1 {
		t.Fatalf("token calls %d", fx.TokenCalls)
	}
	if a := fx.ListArgs()[0]; a["format"] != "json" || a["filter_status"] != "open" {
		t.Fatalf("%v", a)
	}
	byID := map[string]integrations.Item{}
	for _, it := range h.items(c.ID) {
		byID[it.ExternalID] = it
	}
	if byID["101"].Priority != "critical" || byID["101"].Kind != integrations.ItemFinding || byID["101"].IssueRef != "AIK-101" || byID["104"].Priority != "low" || byID["101"].ProjectID != "prj1" {
		t.Fatalf("%+v", byID["101"])
	}
	// the secret in the finding text was redacted before storage
	d, _ := h.svc.ItemDetail(ctx, byID["103"].ID)
	if strings.Contains(d.Body, ghToken) || !strings.Contains(d.Body, "[REDACTED:") || byID["103"].Redactions == 0 {
		t.Fatalf("%q", d.Body)
	}

	// importing a finding makes a SECURITY task whose priority follows the severity
	res, err := h.svc.Import(ctx, human, byID["101"].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	tk := h.taskList()[0]
	if tk.Priority != 4 || tk.IssueRef != "AIK-101" || tk.ProjectID != "prj1" || !strings.Contains(tk.Description, "severity critical") || !strings.Contains(tk.Description, "> Title: CVE-2026-0001 in lodash") {
		t.Fatalf("%+v %+v", res, tk)
	}
	h.svc.Import(ctx, human, byID["102"].ID, "")
	if got := h.taskList()[1].Priority; got != 3 {
		t.Fatalf("high -> %d", got)
	}

	// every sync lists the findings and diffs by content hash; a finding that disappears upstream is marked gone by the sweep
	h.clk.Advance(time.Minute)
	fx.Set(findings[:3])
	r = h.sync(c.ID, integrations.SyncOpts{})
	if r.Gone != 1 || r.New != 0 {
		t.Fatalf("%+v", r)
	}
	if n := h.count(`SELECT COUNT(*) FROM external_items WHERE integration_id=? AND gone=1 AND external_id='104'`, c.ID); n != 1 {
		t.Fatal("finding 104 was not marked gone")
	}
	// an expired access token is bought again once, on the 401, and the call goes through
	fx.ExpireTokens()
	h.clk.Advance(time.Minute)
	if _, err := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Force: true}); err != nil {
		t.Fatal(err)
	}
	if fx.TokenCalls != 2 {
		t.Fatalf("token calls %d", fx.TokenCalls)
	}
	// an ignore is a suppression of a security signal: reason required, approval required, then it goes to the API
	o, err := h.svc.Propose(ctx, human, integrations.ProposeReq{IntegrationID: c.ID, Op: "ignore", Target: "101", Params: map[string]string{"reason": "false positive: dev-only dependency"}})
	if err != nil {
		t.Fatal(err)
	}
	if o.Tool != "PUT /api/public/v1/issues/{id}/ignore" || o.ProjectID != "prj1" {
		t.Fatalf("%+v", o)
	}
	if _, err := h.svc.Execute(ctx, human, o.ID); !errors.Is(err, integrations.ErrNotApproved) {
		t.Fatalf("%v", err)
	}
	h.svc.Decide(ctx, human, o.ID, true)
	if out, err := h.svc.Execute(ctx, human, o.ID); err != nil || out.Status != integrations.OutSent {
		t.Fatalf("%+v %v", out, err)
	}
	if len(fx.Actions) != 1 || fx.Actions[0] != "ignore 101" {
		t.Fatalf("%v", fx.Actions)
	}
	// the secret is nowhere in the app's storage, events or logs; the database holds no access token either
	for name, text := range map[string]string{"disk": h.diskText(), "events": h.allEventsJSON(), "logs": h.logs.String()} {
		if strings.Contains(text, aikidoSecret) || strings.Contains(text, "aik_access_") {
			t.Errorf("an Aikido credential is in the %s", name)
		}
	}
}

func TestAikidoWrongSecretIsReportedAsRejectedCredentials(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	_, c := aikidoFixture(t, h, []itfixture.AikidoFinding{finding(1, "high", "sast", "x", "", 1)}, "AIK_wrongwrongwrongwrongwrong")
	rep, _ := h.svc.Test(ctx, c.ID)
	if rep.OK || rep.Steps[len(rep.Steps)-1].OK || !strings.Contains(rep.Summary, "did not accept the client ID and secret") {
		t.Fatalf("%+v", rep)
	}
	if v, _ := h.svc.Get(ctx, c.ID); v.Status != integrations.StatusUnauthenticated {
		t.Fatalf("%s", v.Status)
	}
}

func TestAikidoRepositoryScopeAndRegion(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	other := finding(201, "high", "sast", "XSS", "", 1)
	other.CodeRepoID, other.CodeRepoName = 12, "website"
	fx, c := aikidoFixture(t, h, []itfixture.AikidoFinding{finding(101, "high", "sast", "SQLi", "", 1), other}, aikidoSecret)
	ch, err := h.svc.Choices(ctx, c.ID, "repo", nil)
	if err != nil || len(ch) != 2 || ch[1].Value != "12" || ch[1].Label != "website" {
		t.Fatalf("%+v %v", ch, err)
	}
	if _, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "prj1", Config: map[string]string{"repo": "12"}}); err != nil {
		t.Fatal(err)
	}
	r := h.sync(c.ID, integrations.SyncOpts{})
	if r.New != 1 || fx.ListArgs()[0]["filter_code_repo_id"] != "12" {
		t.Fatalf("%+v %v", r, fx.ListArgs())
	}
	// a region is a known host, never a free URL
	h2 := newHarness(t, nil)
	v, err := h2.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindAikido, ClientID: aikidoClientID, ClientSecret: aikidoSecret, Region: "us"})
	if err != nil || v.BaseURL != "https://app.us.aikido.dev" {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestAikidoKeyFromBeforeTheRESTAPIAsksForClientCredentials(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	// a connection migrated from the MCP days holds the MCP server's key as a bare token
	c, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindAikido, ClientID: aikidoClientID, ClientSecret: aikidoSecret, BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.vault.Put(ctx, "integrations/"+c.ID+"/token", "AIK_old_mcp_key_value_12345"); err != nil {
		t.Fatal(err)
	}
	rep, _ := h.svc.Test(ctx, c.ID)
	if rep.OK || !strings.Contains(rep.Summary, "needs a client ID and secret") {
		t.Fatalf("%+v", rep)
	}
}

package integrations_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/integrations/itfixture"
)

// diskText concatenates every file under dir plus the database files, for "the secret is nowhere on disk" checks.
func (h *harness) diskText() string {
	h.t.Helper()
	// a WAL checkpoint makes sure recent writes are in the main file; both are read anyway
	_, _ = h.db.Writer().Exec(`PRAGMA wal_checkpoint(FULL)`)
	var b strings.Builder
	for _, p := range []string{h.db.Path(), h.db.Path() + "-wal", h.db.Path() + "-shm"} {
		if data, err := os.ReadFile(p); err == nil {
			b.Write(data)
		}
	}
	_ = filepath.Walk(h.dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && !strings.HasPrefix(p, h.db.Path()) {
			if data, rerr := os.ReadFile(p); rerr == nil {
				b.Write(data)
			}
		}
		return nil
	})
	return b.String()
}

func TestTokenNeverInSQLiteEventsLogsViewsOrErrors(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	fx, url := linearFixture(t, 3)
	// the user pasted their own API key into an issue description: even that copy is redacted from storage
	li := fx.Get("uuid-0001")
	li.Description = "Auth is broken, my key is " + linearToken + " please check"
	fx.Put(li)

	c := h.connect(integrations.KindLinear, linearToken, url, map[string]string{"team": "ENG"})
	rep, err := h.svc.Test(ctx, c.ID)
	if err != nil || !rep.OK {
		t.Fatalf("%+v %v", rep, err)
	}
	h.sync(c.ID, integrations.SyncOpts{})
	// failure paths put vendor-controlled text into error handling: the vendor echoes the key in its error body
	fx.FailNext(itfixture.Fail{Status: 429, Headers: map[string]string{"Retry-After": "30"}, Body: `{"code":"` + linearToken + `","message":"rate limited for ` + linearToken + `"}`})
	_, serr := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Full: true})
	if serr == nil {
		t.Fatal("expected the injected rate-limit failure")
	}
	h.clk.Advance(time.Minute)
	o, err := h.svc.Propose(ctx, human, integrations.ProposeReq{IntegrationID: c.ID, Op: "comment", Target: "uuid-0000", Params: map[string]string{"body": "status update"}})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Decide(ctx, human, o.ID, true)
	if _, err := h.svc.Execute(ctx, human, o.ID); err != nil {
		t.Fatal(err)
	}
	fx.Revoke(true)
	rep2, _ := h.svc.Test(ctx, c.ID)
	_, serr2 := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Force: true})
	views, _ := h.svc.List(ctx)

	// the key DID reach the server, as Linear wants a personal API key: bare, without "Bearer" (otherwise this test proves
	// nothing) ...
	var reached bool
	for _, a := range fx.AuthSeen() {
		if a == linearToken {
			reached = true
		}
	}
	if !reached {
		t.Fatalf("the fixture never saw the key: %v", fx.AuthSeen())
	}
	// ... it is held only by the Vault ...
	if got, err := h.vault.Get(ctx, "integrations/"+c.ID+"/token"); err != nil || got != linearToken {
		t.Fatalf("vault: %v", err)
	}
	// ... and appears nowhere else
	places := map[string]string{
		"database and body files": h.diskText(),
		"events":                  h.allEventsJSON(),
		"logs":                    h.logs.String(),
		"views":                   jsonOf(views),
		"test report":             jsonOf(rep) + jsonOf(rep2),
		"errors":                  serr.Error() + serr2.Error(),
		"items":                   jsonOf(h.items(c.ID)),
		"outbox":                  jsonOf(mustOutbox(t, h, c.ID)),
	}
	for name, text := range places {
		if strings.Contains(text, linearToken) {
			t.Errorf("the API key is in the %s", name)
		}
		if strings.Contains(text, linearToken[:16]) {
			t.Errorf("a long prefix of the API key is in the %s", name)
		}
	}
	// the credential reference in the row is only a name
	var ref string
	h.db.Reader().QueryRow(`SELECT credential_ref FROM integrations WHERE id=?`, c.ID).Scan(&ref)
	if ref != "integrations/"+c.ID+"/token" {
		t.Fatalf("ref %q", ref)
	}
	found := false
	for _, k := range h.cred.Keys() {
		if strings.Contains(k, ref) {
			found = true
		}
	}
	if !found {
		t.Fatalf("credential store keys %v", h.cred.Keys())
	}
	for _, it := range h.items(c.ID) {
		if it.ExternalID == "uuid-0001" {
			d, _ := h.svc.ItemDetail(ctx, it.ID)
			if strings.Contains(d.Body, linearToken) || !strings.Contains(d.Body, "[REDACTED:") {
				t.Fatalf("item body: %q", d.Body)
			}
		}
	}
}

func mustOutbox(t *testing.T, h *harness, id string) any {
	t.Helper()
	o, err := h.svc.ListOutbox(context.Background(), id, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestConnectValidatesAndDisconnectRemovesTheToken(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	_, url := linearFixture(t, 1)
	bad := []struct {
		name string
		req  integrations.ConnectReq
	}{
		{"unknown kind", integrations.ConnectReq{Kind: "jira", Token: linearToken}},
		{"no token", integrations.ConnectReq{Kind: "linear"}},
		{"token with a newline (header injection)", integrations.ConnectReq{Kind: "linear", Token: "abcdefgh\nX-Evil: 1"}},
		{"token with a space", integrations.ConnectReq{Kind: "linear", Token: "abcdefgh ijkl"}},
		{"short token", integrations.ConnectReq{Kind: "linear", Token: "abc"}},
		{"unknown setting", integrations.ConnectReq{Kind: "linear", Token: linearToken, ProjectID: "prj1", Config: map[string]string{"command": "rm"}}},
		{"plain http to a public host", integrations.ConnectReq{Kind: "linear", Token: linearToken, BaseURL: "http://linear.example"}},
		{"credentials in the url", integrations.ConnectReq{Kind: "linear", Token: linearToken, BaseURL: "https://u:p@linear.example"}},
		{"poll interval too small", integrations.ConnectReq{Kind: "linear", Token: linearToken, PollIntervalS: 5}},
		{"Aikido with a single token", integrations.ConnectReq{Kind: "aikido", Token: linearToken}},
		{"Aikido with an unknown region", integrations.ConnectReq{Kind: "aikido", ClientID: "id", ClientSecret: "secret-secret", Region: "mars"}},
		{"client credentials for Linear", integrations.ConnectReq{Kind: "linear", ClientID: "id", ClientSecret: "secret-secret"}},
		{"a token for GitHub", integrations.ConnectReq{Kind: "github", Token: linearToken}},
		{"a sign-in that never happened", integrations.ConnectReq{Kind: "linear", OAuthSession: "oas_nope"}},
	}
	for _, b := range bad {
		if _, err := h.svc.Connect(ctx, human, b.req); !errors.Is(err, integrations.ErrBadRequest) {
			t.Errorf("%s: %v", b.name, err)
		}
	}
	if _, err := h.svc.Connect(ctx, "agent:x", integrations.ConnectReq{Kind: "linear", Token: linearToken}); !errors.Is(err, integrations.ErrNotHuman) {
		t.Fatalf("an agent connected an integration: %v", err)
	}
	if n := h.count(`SELECT COUNT(*) FROM integrations`); n != 0 {
		t.Fatalf("%d connections were created by invalid requests", n)
	}
	c := h.connect(integrations.KindLinear, linearToken, url, nil)
	if !c.HasCredential || c.Status != integrations.StatusUntested || c.Name != "Linear" {
		t.Fatalf("%+v", c)
	}
	// one Linear per workspace: a second one is refused (Reconnect changes the credential instead)
	if _, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: "linear", Token: linearToken}); !errors.Is(err, integrations.ErrAlreadyConnected) {
		t.Fatalf("a second Linear connection: %v", err)
	}
	h.sync(c.ID, integrations.SyncOpts{})
	if err := h.svc.Disconnect(ctx, "agent:x", c.ID); !errors.Is(err, integrations.ErrNotHuman) {
		t.Fatal(err)
	}
	if err := h.svc.Disconnect(ctx, human, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.vault.Get(ctx, "integrations/"+c.ID+"/token"); err == nil {
		t.Fatal("the token survived the disconnect")
	}
	for _, q := range []string{`SELECT COUNT(*) FROM external_items WHERE integration_id=?`, `SELECT COUNT(*) FROM integration_cursors WHERE integration_id=?`,
		`SELECT COUNT(*) FROM integration_assignments WHERE integration_id=?`} {
		if n := h.count(q, c.ID); n != 0 {
			t.Fatalf("%s: %d left", q, n)
		}
	}
	if len(h.events(integrations.EventDisconnected)) != 1 {
		t.Fatal("no disconnect event")
	}
}

func TestTestConnectionReadsTheIdentityTheCredentialAndOneItem(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	fx, url := linearFixture(t, 2)
	c := h.connect(integrations.KindLinear, linearToken, url, nil)
	rep, err := h.svc.Test(ctx, c.ID)
	if err != nil || !rep.OK || len(rep.Steps) != 3 {
		t.Fatalf("%+v %v", rep, err)
	}
	for _, s := range rep.Steps {
		if !s.OK {
			t.Errorf("step %+v", s)
		}
	}
	// what the connection is, read from Linear's API: the viewer and the organization
	if rep.Identity.Account != "Kyle Fixture" || rep.Identity.Workspace != "Fixture Org" || rep.Identity.WorkspaceURL != "https://linear.app/fixture" || rep.Identity.Method != "Personal API key" {
		t.Fatalf("identity %+v", rep.Identity)
	}
	v, _ := h.svc.Get(ctx, c.ID)
	if v.Status != integrations.StatusOK || v.Identity.Workspace != "Fixture Org" || v.CheckedAt != baseTime.UnixMilli() {
		t.Fatalf("view %s %+v %d", v.Status, v.Identity, v.CheckedAt)
	}

	// key revoked at the vendor: the report names the step and says what to do, in plain language
	fx.Revoke(true)
	rep, _ = h.svc.Test(ctx, c.ID)
	if rep.OK || rep.Steps[1].OK || !strings.Contains(rep.Summary, "rejected the credential") {
		t.Fatalf("%+v", rep)
	}
	if v, _ := h.svc.Get(ctx, c.ID); v.Status != integrations.StatusUnauthenticated || !strings.Contains(v.StatusDetail, "Reconnect Linear") {
		t.Fatalf("%s %q", v.Status, v.StatusDetail)
	}
	// the server went away
	fx.Revoke(false)
	h2 := newHarness(t, nil)
	dead := h2.connect(integrations.KindLinear, linearToken, "http://127.0.0.1:1", nil)
	rep, _ = h2.svc.Test(ctx, dead.ID)
	if rep.OK || !strings.Contains(rep.Summary, "could not reach") {
		t.Fatalf("%+v", rep)
	}
	// no key stored (the credential removed behind the app's back)
	if err := h.vault.Delete(ctx, "integrations/"+c.ID+"/token"); err != nil {
		t.Fatal(err)
	}
	rep, _ = h.svc.Test(ctx, c.ID)
	if rep.OK || rep.Steps[0].OK || len(rep.Steps) != 1 {
		t.Fatalf("%+v", rep)
	}
}

func TestSentryIdentityReportsScopesAndOrganizations(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	fx, url := sentryFixture(t)
	v, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindSentry, Token: sentryToken, BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := h.svc.Test(ctx, v.ID)
	if err != nil || !rep.OK {
		t.Fatalf("%+v %v", rep, err)
	}
	if rep.Identity.Account != "Kyle Fixture" || rep.Identity.Workspace != "Acme Corp" || strings.Join(rep.Identity.Scopes, " ") != "org:read project:read event:read event:write" {
		t.Fatalf("%+v", rep.Identity)
	}
	// not assigned yet: the test says there is nothing to read and still passes
	if !strings.Contains(rep.Steps[2].Detail, "Not assigned to a project yet") || fx.Calls("issues") != 0 {
		t.Fatalf("%+v", rep.Steps)
	}
	// the Bearer scheme reached Sentry
	if a := fx.AuthSeen(); a[0] != "Bearer "+sentryToken {
		t.Fatalf("%v", a)
	}
}

func TestFailuresArePlainLanguageWithBackoffRateLimitAndRecovery(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	fx, url := linearFixture(t, 4)
	c := h.connect(integrations.KindLinear, linearToken, url, nil)
	h.sync(c.ID, integrations.SyncOpts{})
	cur := func() integrations.ConnectionView { v, _ := h.svc.Get(ctx, c.ID); return v }

	// rate limited: Linear answers a "ratelimited" GraphQL error with the reset in its headers; Drydock waits that long and the
	// scheduler stays away
	fx.FailNext(itfixture.LinearRateLimited(h.clk.Now().Add(120 * time.Second)))
	_, err := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Full: true})
	var f *integrations.Failure
	if !errors.As(err, &f) || f.Code != integrations.CodeRateLimited || f.RetryAfter != 120*time.Second {
		t.Fatalf("%v %+v", err, f)
	}
	v := cur()
	if v.Sync.State != "rate_limited" || !strings.Contains(v.Sync.LastError, "slow down") || v.Sync.NextAttemptAt < baseTime.Add(120*time.Second).UnixMilli() {
		t.Fatalf("%+v", v.Sync)
	}
	if _, err := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Force: true}); !errors.Is(err, integrations.ErrRateLimited) {
		t.Fatalf("a manual sync hammered a rate-limited vendor: %v", err)
	}
	calls := fx.Calls("issues")
	h.svc.Tick(ctx)
	if fx.Calls("issues") != calls {
		t.Fatal("the scheduler ignored the rate limit")
	}
	if len(h.events(integrations.EventRateLimited)) != 1 {
		t.Fatal("no rate_limited event")
	}
	h.clk.Advance(3 * time.Minute)
	if _, err := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if v := cur(); v.Sync.ConsecutiveFailures != 0 || v.Sync.LastError != "" || v.Sync.State != "ok" {
		t.Fatalf("%+v", v.Sync)
	}

	// a server error backs off exponentially: 30 s, 60 s, 120 s
	for i, want := range []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second} {
		fx.FailNext(itfixture.Fail{Status: 502, Body: "<html>bad gateway</html>"})
		now := h.clk.Now()
		_, err := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Force: true})
		if err == nil {
			t.Fatal("expected failure")
		}
		got := time.Duration(cur().Sync.NextAttemptAt-now.UnixMilli()) * time.Millisecond
		if got != want {
			t.Fatalf("failure %d: backoff %v, want %v", i+1, got, want)
		}
	}
	if v := cur(); v.Sync.State != "backing_off" || v.Sync.ConsecutiveFailures != 3 || !strings.Contains(v.Sync.LastError, "server error (HTTP 502)") {
		t.Fatalf("%+v", v.Sync)
	}

	// key revoked: the sync pauses (it does not keep failing), the connection says so, and the scheduler stays away
	h.clk.Advance(time.Hour)
	fx.Revoke(true)
	_, err = h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Force: true})
	if !errors.As(err, &f) || f.Code != integrations.CodeUnauthorized {
		t.Fatalf("%v", err)
	}
	v = cur()
	if v.Status != integrations.StatusUnauthenticated || v.Sync.State != "unauthenticated" || !strings.Contains(v.Sync.LastError, "rejected the credential") {
		t.Fatalf("%+v %s", v.Sync, v.Status)
	}
	if len(h.events(integrations.EventAuthExpired)) != 1 {
		t.Fatal("no auth_expired event")
	}
	h.clk.Advance(time.Hour)
	calls = fx.Calls("issues")
	if ran := h.svc.Tick(ctx); len(ran) != 0 || fx.Calls("issues") != calls {
		t.Fatalf("an unauthenticated connection was polled: %v", ran)
	}
	// the user reconnects with a new key: the connection un-pauses and syncs
	fx.Revoke(false)
	if _, err := h.svc.Connect(ctx, human, integrations.ConnectReq{ID: c.ID, BaseURL: url, Token: linearToken}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Test(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if v := cur(); v.Sync.State != "ok" || v.Status != integrations.StatusOK {
		t.Fatalf("%+v %s", v.Sync, v.Status)
	}
}

func TestSchemaDriftDegradesTheConnectionAndKeepsItems(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	fx, url := linearFixture(t, 3)
	c := h.connect(integrations.KindLinear, linearToken, url, nil)
	h.sync(c.ID, integrations.SyncOpts{})
	fx.WrongShape = true
	_, err := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Full: true})
	var f *integrations.Failure
	if !errors.As(err, &f) || f.Code != integrations.CodeSchemaDrift {
		t.Fatalf("%v", err)
	}
	v, _ := h.svc.Get(ctx, c.ID)
	if v.Status != integrations.StatusDegraded || v.Sync.ItemsSeen != 3 || !strings.Contains(v.StatusDetail, "shape Drydock does not recognize") {
		t.Fatalf("%s %+v", v.Status, v.Sync)
	}
	if len(h.events(integrations.EventSchemaDrift)) != 1 {
		t.Fatal("no schema_drift event")
	}
	if n := h.count(`SELECT COUNT(*) FROM external_items WHERE gone=1`); n != 0 {
		t.Fatalf("a broken sync marked %d items gone", n)
	}
}

func TestCredentialsServeTheGatewayCatalogRefs(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	_, url := linearFixture(t, 1)
	c := h.connect(integrations.KindLinear, linearToken, url, nil)
	creds := integrations.Credentials{Store: h.svc.Store(), Vault: h.vault}
	for _, ref := range []string{"integrations/" + c.ID + "/token", "mcp/linear"} {
		got, err := creds.Credential(ctx, "linear", ref)
		if err != nil || got != linearToken {
			t.Fatalf("%s: %v", ref, err)
		}
	}
	if _, err := creds.Credential(ctx, "sentry", "mcp/sentry"); !errors.Is(err, integrations.ErrNoCredential) {
		t.Fatalf("%v", err)
	}
	if _, err := creds.Credential(ctx, "x", "somewhere/else"); err == nil {
		t.Fatal("an arbitrary reference resolved")
	}
	// Aikido's REST client credentials are never handed to an MCP server as if they were its key
	afx := itfixture.NewAikido("aik_client", "aik_secret_Wq3Rt5Yu7Io9")
	ats, aurl := itfixture.Serve(afx.Server())
	t.Cleanup(ats.Close)
	if _, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindAikido, ClientID: "aik_client", ClientSecret: "aik_secret_Wq3Rt5Yu7Io9", BaseURL: aurl}); err != nil {
		t.Fatal(err)
	}
	if _, err := creds.Credential(ctx, "aikido", "mcp/aikido"); !errors.Is(err, integrations.ErrNoCredential) {
		t.Fatalf("%v", err)
	}
}

func TestAProxyRefusalIsNotReportedAsABadToken(t *testing.T) {
	f := integrations.Classify(integrations.KindLinear, errors.New(`Post "https://api.linear.app/graphql": proxyconnect: Proxy refused to open a tunnel: 403 Forbidden`), "")
	if f.Code != integrations.CodeUnreachable || strings.Contains(f.Message, "rejected the credential") || !strings.Contains(f.Message, "proxy") {
		t.Fatalf("%+v", f)
	}
	g := integrations.Classify(integrations.KindLinear, nil, "401 Unauthorized: invalid token")
	if g.Code != integrations.CodeUnauthorized {
		t.Fatalf("a real rejection must still be reported as one: %+v", g)
	}
}

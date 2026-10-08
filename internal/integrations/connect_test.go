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
	"github.com/kjmaguire/drydock/internal/mcp"
)

// readAll concatenates every file under dir plus the database files, for "the secret is nowhere on disk" checks.
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
	// failure paths put vendor-controlled text into error handling
	fx.FailNext("429 rate limit exceeded for token " + linearToken + ". Retry-After: 30")
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

	// the token DID reach the server (otherwise this test proves nothing) ...
	var reached bool
	for _, a := range fx.AuthSeen() {
		if a == "Bearer "+linearToken {
			reached = true
		}
	}
	if !reached {
		t.Fatalf("the fixture never saw the token: %v", fx.AuthSeen())
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
			t.Errorf("the API token is in the %s", name)
		}
		if strings.Contains(text, linearToken[:16]) {
			t.Errorf("a long prefix of the API token is in the %s", name)
		}
	}
	// the credential reference in the row is only a name
	var ref string
	h.db.Reader().QueryRow(`SELECT credential_ref FROM integrations WHERE id=?`, c.ID).Scan(&ref)
	if ref != "integrations/"+c.ID+"/token" {
		t.Fatalf("ref %q", ref)
	}
	// the in-memory credential store holds the value under the app's service name, nowhere in SQLite (Vault.Put, value < 2560 bytes)
	found := false
	for _, k := range h.cred.Keys() {
		if strings.Contains(k, ref) {
			found = true
		}
	}
	if !found {
		t.Fatalf("credential store keys %v", h.cred.Keys())
	}
	// the redacted copy of the user's pasted key in the item body
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
		{"unknown setting", integrations.ConnectReq{Kind: "linear", Token: linearToken, Config: map[string]string{"command": "rm"}}},
		{"plain http to a public host", integrations.ConnectReq{Kind: "linear", Token: linearToken, BaseURL: "http://linear.example/mcp"}},
		{"credentials in the url", integrations.ConnectReq{Kind: "linear", Token: linearToken, BaseURL: "https://u:p@linear.example/mcp"}},
		{"poll interval too small", integrations.ConnectReq{Kind: "linear", Token: linearToken, PollIntervalS: 5}},
		{"bad argv", integrations.ConnectReq{Kind: "aikido", Token: linearToken, Config: map[string]string{"argv": "not json"}}},
		{"a secret in argv", integrations.ConnectReq{Kind: "aikido", Token: linearToken, Config: map[string]string{"argv": `["aikido-mcp","--api_key=ABCDEFGHIJKLMNOP"]`}}},
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
	if !c.HasCredential || c.Status != integrations.StatusUntested {
		t.Fatalf("%+v", c)
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
	if n := h.count(`SELECT COUNT(*) FROM external_items WHERE integration_id=?`, c.ID); n != 0 {
		t.Fatalf("%d items left", n)
	}
	if n := h.count(`SELECT COUNT(*) FROM integration_cursors WHERE integration_id=?`, c.ID); n != 0 {
		t.Fatal("cursor left")
	}
	// the registered secret is no longer redacted (and no longer needed)
	if len(h.events(integrations.EventDisconnected)) != 1 {
		t.Fatal("no disconnect event")
	}
}

func TestTestConnectionReportsTokenServerToolsAndRead(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	fx, url := linearFixture(t, 2)
	c := h.connect(integrations.KindLinear, linearToken, url, nil)
	rep, err := h.svc.Test(ctx, c.ID)
	if err != nil || !rep.OK || len(rep.Steps) != 4 {
		t.Fatalf("%+v %v", rep, err)
	}
	for _, s := range rep.Steps {
		if !s.OK {
			t.Errorf("step %+v", s)
		}
	}
	if len(rep.ToolsFound) != 3 || len(rep.ToolsMissing) != 0 {
		t.Fatalf("%+v", rep)
	}
	if v, _ := h.svc.Get(ctx, c.ID); v.Status != integrations.StatusOK {
		t.Fatalf("status %s", v.Status)
	}

	// token revoked at the vendor: the report names the step and says what to do, in plain language
	fx.Revoke(true)
	rep, _ = h.svc.Test(ctx, c.ID)
	if rep.OK || rep.Steps[1].OK || !strings.Contains(rep.Summary, "rejected the token") {
		t.Fatalf("%+v", rep)
	}
	if v, _ := h.svc.Get(ctx, c.ID); v.Status != integrations.StatusUnauthenticated || !strings.Contains(v.StatusDetail, "Paste a new token") {
		t.Fatalf("%s %q", v.Status, v.StatusDetail)
	}
	// the server went away
	fx.Revoke(false)
	h2 := newHarness(t, nil)
	dead := h2.connect(integrations.KindLinear, linearToken, "http://127.0.0.1:1/mcp", nil)
	rep, _ = h2.svc.Test(ctx, dead.ID)
	if rep.OK || !strings.Contains(rep.Summary, "could not reach") {
		t.Fatalf("%+v", rep)
	}
	// no token stored (credential removed behind the app's back)
	if err := h.vault.Delete(ctx, "integrations/"+c.ID+"/token"); err != nil {
		t.Fatal(err)
	}
	rep, _ = h.svc.Test(ctx, c.ID)
	if rep.OK || rep.Steps[0].OK || len(rep.Steps) != 1 {
		t.Fatalf("%+v", rep)
	}
}

func TestTestConnectionNamesMissingTools(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	// a Sentry server that offers neither list_issues nor search_issues
	fx := itfixture.NewSentry(sentryToken)
	ts, url := itfixture.Serve(fx.Server(), &fx.Common)
	t.Cleanup(ts.Close)
	fx.Server().RemoveTools("list_issues")
	c := h.connect(integrations.KindSentry, sentryToken, url, nil)
	rep, _ := h.svc.Test(ctx, c.ID)
	if rep.OK || len(rep.ToolsMissing) != 1 || rep.ToolsMissing[0] != "list_issues" {
		t.Fatalf("%+v", rep)
	}
	if v, _ := h.svc.Get(ctx, c.ID); v.Status != integrations.StatusDegraded {
		t.Fatalf("%s", v.Status)
	}
	if _, err := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{}); err == nil || !strings.Contains(err.Error(), "does not offer the tool list_issues") {
		t.Fatalf("%v", err)
	}
	// optional write tools missing are reported but do not fail the test
	nfx := itfixture.NewNotion("ntn_Qw3Er5Ty7Ui9Op1As2Df4Gh6Jk8Lz0Xc")
	nts, nurl := itfixture.Serve(nfx.Server(), &nfx.Common)
	t.Cleanup(nts.Close)
	nfx.Server().RemoveTools("notion-create-comment")
	nc := h.connect(integrations.KindNotion, "ntn_Qw3Er5Ty7Ui9Op1As2Df4Gh6Jk8Lz0Xc", nurl, nil)
	rep, _ = h.svc.Test(ctx, nc.ID)
	if !rep.OK || len(rep.WritesMissing) != 1 || rep.WritesMissing[0] != "notion-create-comment" {
		t.Fatalf("%+v", rep)
	}
	if _, err := h.svc.Propose(ctx, human, integrations.ProposeReq{IntegrationID: nc.ID, Op: "comment", Target: "p", Params: map[string]string{"body": "x"}}); !errors.Is(err, integrations.ErrToolMissing) {
		t.Fatalf("%v", err)
	}
}

func TestFailuresArePlainLanguageWithBackoffRateLimitAndRecovery(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	fx, url := linearFixture(t, 4)
	c := h.connect(integrations.KindLinear, linearToken, url, nil)
	h.sync(c.ID, integrations.SyncOpts{})
	cur := func() integrations.ConnectionView { v, _ := h.svc.Get(ctx, c.ID); return v }

	// rate limited: Retry-After is honored and the scheduler stays away
	fx.FailNext("Error 429: rate limit exceeded. Retry-After: 120")
	_, err := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Full: true})
	var f *integrations.Failure
	if !errors.As(err, &f) || f.Code != integrations.CodeRateLimited || f.RetryAfter != 120*time.Second {
		t.Fatalf("%v", err)
	}
	v := cur()
	if v.Sync.State != "rate_limited" || !strings.Contains(v.Sync.LastError, "slow down") || v.Sync.NextAttemptAt < baseTime.Add(120*time.Second).UnixMilli() {
		t.Fatalf("%+v", v.Sync)
	}
	if _, err := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Force: true}); !errors.Is(err, integrations.ErrRateLimited) {
		t.Fatalf("a manual sync hammered a rate-limited vendor: %v", err)
	}
	calls := fx.Calls("list_issues")
	h.svc.Tick(ctx)
	if fx.Calls("list_issues") != calls {
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

	// an unclassified failure backs off exponentially: 30 s, 60 s, 120 s
	var prev int64
	for i, want := range []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second} {
		fx.FailNext("something odd happened")
		now := h.clk.Now()
		_, err := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Force: true})
		if err == nil {
			t.Fatal("expected failure")
		}
		got := time.Duration(cur().Sync.NextAttemptAt-now.UnixMilli()) * time.Millisecond
		if got != want {
			t.Fatalf("failure %d: backoff %v, want %v", i+1, got, want)
		}
		prev = cur().Sync.NextAttemptAt
	}
	_ = prev
	if v := cur(); v.Sync.State != "backing_off" || v.Sync.ConsecutiveFailures != 3 {
		t.Fatalf("%+v", v.Sync)
	}

	// token revoked: the sync pauses (it does not keep failing), the connection says so, and the scheduler stays away
	h.clk.Advance(time.Hour)
	fx.Revoke(true)
	_, err = h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Force: true})
	if !errors.As(err, &f) || f.Code != integrations.CodeUnauthorized {
		t.Fatalf("%v", err)
	}
	v = cur()
	if v.Status != integrations.StatusUnauthenticated || v.Sync.State != "unauthenticated" || !strings.Contains(v.Sync.LastError, "rejected the token") {
		t.Fatalf("%+v %s", v.Sync, v.Status)
	}
	if len(h.events(integrations.EventAuthExpired)) != 1 {
		t.Fatal("no auth_expired event")
	}
	h.clk.Advance(time.Hour)
	calls = fx.Calls("list_issues")
	if ran := h.svc.Tick(ctx); len(ran) != 0 || fx.Calls("list_issues") != calls {
		t.Fatalf("an unauthenticated connection was polled: %v", ran)
	}
	// the user pastes a new token: the connection un-pauses and syncs
	fx.Revoke(false)
	if _, err := h.svc.Connect(ctx, human, integrations.ConnectReq{ID: c.ID, ProjectID: "prj1", BaseURL: url, Token: linearToken}); err != nil {
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

func TestCredentialsServeBothAppSyncAndTheGatewayCatalogRefs(t *testing.T) {
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
}

func TestServerSpecsComeFromTheCatalogAndNeverCarryTheTokenInArgv(t *testing.T) {
	cat, err := mcp.LoadBuiltin()
	if err != nil {
		t.Fatal(err)
	}
	lin := integrations.Connection{Kind: integrations.KindLinear, CredentialRef: "integrations/int_x/token"}
	rd, err := integrations.ServerSpecFor(cat, lin, false)
	if err != nil || rd.URL != "https://mcp.linear.app/mcp/readonly" || rd.Auth.Keyring != "integrations/int_x/token" || rd.Auth.Kind != mcp.AuthBearer {
		t.Fatalf("%+v %v", rd, err)
	}
	wr, _ := integrations.ServerSpecFor(cat, lin, true)
	if wr.URL != "https://mcp.linear.app/mcp" {
		t.Fatalf("writes must use the read-write endpoint: %s", wr.URL)
	}
	sen, _ := integrations.ServerSpecFor(cat, integrations.Connection{Kind: integrations.KindSentry, CredentialRef: "r"}, false)
	if sen.Auth.Scheme != "Sentry-Bearer" || sen.URL != "https://mcp.sentry.dev/mcp" {
		t.Fatalf("%+v", sen)
	}
	aik, _ := integrations.ServerSpecFor(cat, integrations.Connection{Kind: integrations.KindAikido, CredentialRef: "integrations/int_y/token"}, false)
	if aik.Transport != mcp.TransportStdio || aik.Command.SecretEnv["AIKIDO_API_KEY"] != "integrations/int_y/token" || aik.License.Bundled || aik.Command.Pin != "@aikidosec/mcp@1.0.27" {
		t.Fatalf("%+v", aik)
	}
	for _, a := range aik.Command.Argv {
		if strings.Contains(a, "integrations/") {
			t.Fatalf("a credential reference in argv: %v", aik.Command.Argv)
		}
	}
	notion, _ := integrations.ServerSpecFor(cat, integrations.Connection{Kind: integrations.KindNotion, CredentialRef: "r"}, false)
	if notion.URL != "https://mcp.notion.com/mcp" {
		t.Fatalf("%s", notion.URL)
	}
	if _, err := integrations.ServerSpecFor(cat, integrations.Connection{Kind: integrations.KindLinear}, false); !errors.Is(err, integrations.ErrNoCredential) {
		t.Fatalf("%v", err)
	}
	// the template entries in the catalog are the user-install kind and are never bundled (D14)
	for _, id := range []string{"aikido", "sentryx"} {
		s, _ := cat.Get(id)
		if !s.Template || s.License.Bundled {
			t.Fatalf("%s: %+v", id, s)
		}
	}
}

package integrations_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/integrations/itfixture"
)

func oauthMethod(t *testing.T, h *harness, kind string) integrations.AuthMethod {
	t.Helper()
	for _, k := range h.svc.Kinds() {
		if k.Kind != kind {
			continue
		}
		for _, m := range k.Auth {
			if m.ID == integrations.AuthOAuth {
				return m
			}
		}
	}
	t.Fatalf("%s has no OAuth method", kind)
	return integrations.AuthMethod{}
}

// The owner registers an OAuth app with a vendor and pastes its public client id: the sign-in turns on, the id survives a
// restart (the settings table), an empty id turns it off again, and only a human may change it. The page learns where to
// register from the catalog.
func TestPastedOAuthAppTurnsTheSignInOnAndSurvivesARestart(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	for _, k := range []string{integrations.KindLinear, integrations.KindSentry, integrations.KindGitHub} {
		m := oauthMethod(t, h, k)
		if m.Available || m.OAuthApp == nil || m.OAuthApp.ClientID != "" || m.OAuthApp.Source != "" || m.OAuthApp.RegisterURL == "" || !strings.Contains(m.Note, "register one") {
			t.Fatalf("%s before: %+v %+v", k, m, m.OAuthApp)
		}
	}
	if m := oauthMethod(t, h, integrations.KindLinear); m.OAuthApp.RedirectURI != "http://127.0.0.1:41739/callback" || m.OAuthApp.BaseURLSetting {
		t.Fatalf("%+v", m.OAuthApp)
	}
	if m := oauthMethod(t, h, integrations.KindSentry); !m.OAuthApp.BaseURLSetting || m.OAuthApp.RedirectURI != "" {
		t.Fatalf("%+v", m.OAuthApp)
	}
	if _, err := h.svc.SetOAuthApp(ctx, "agent:x", integrations.KindLinear, "lin_client_pasted", ""); !errors.Is(err, integrations.ErrNotHuman) {
		t.Fatalf("%v", err)
	}
	for _, bad := range []struct{ kind, id, base string }{
		{integrations.KindNotion, "x", ""}, {integrations.KindAikido, "x", ""}, {"nope", "x", ""},
		{integrations.KindLinear, "has space", ""}, {integrations.KindLinear, "x", "https://sentry.example"}, {integrations.KindSentry, "x", "not a url"},
	} {
		if _, err := h.svc.SetOAuthApp(ctx, human, bad.kind, bad.id, bad.base); !errors.Is(err, integrations.ErrBadRequest) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	info, err := h.svc.SetOAuthApp(ctx, human, integrations.KindLinear, "  lin_client_pasted ", "")
	if err != nil || info.ClientID != "lin_client_pasted" || info.Source != "setting" || !h.svc.OAuthAvailable(integrations.KindLinear) {
		t.Fatalf("%+v %v", info, err)
	}
	if h.svc.OAuthAvailable(integrations.KindSentry) || h.svc.OAuthAvailable(integrations.KindGitHub) {
		t.Fatal("other kinds turned on")
	}
	if m := oauthMethod(t, h, integrations.KindLinear); !m.Available || m.Note != "" || m.OAuthApp.ClientID != "lin_client_pasted" {
		t.Fatalf("%+v", m)
	}
	// Sentry: the client id belongs to a self-hosted Sentry
	if info, err := h.svc.SetOAuthApp(ctx, human, integrations.KindSentry, "sen_client_pasted", "https://sentry.example.com/"); err != nil || info.BaseURL != "https://sentry.example.com" {
		t.Fatalf("%+v %v", info, err)
	}
	// the change is an event that names the kind, never the id
	ev := h.events("integration.oauth_app_changed")
	if len(ev) != 2 || strings.Contains(h.allEventsJSON(), "lin_client_pasted") {
		t.Fatalf("%d events; %s", len(ev), h.allEventsJSON())
	}
	// a restart reads the settings table
	h.restart()
	if !h.svc.OAuthAvailable(integrations.KindLinear) || !h.svc.OAuthAvailable(integrations.KindSentry) {
		t.Fatal("the pasted apps did not survive the restart")
	}
	if m := oauthMethod(t, h, integrations.KindSentry); m.OAuthApp.ClientID != "sen_client_pasted" || m.OAuthApp.BaseURL != "https://sentry.example.com" {
		t.Fatalf("%+v", m.OAuthApp)
	}
	// an empty id removes the registration
	if info, err := h.svc.SetOAuthApp(ctx, human, integrations.KindLinear, "", ""); err != nil || info.ClientID != "" || info.Source != "" || h.svc.OAuthAvailable(integrations.KindLinear) {
		t.Fatalf("%+v %v", info, err)
	}
	if _, err := h.svc.OAuthStart(ctx, human, integrations.KindLinear, ""); !errors.Is(err, integrations.ErrOAuthUnavailable) || !strings.Contains(err.Error(), "Integrations page") {
		t.Fatalf("%v", err)
	}
	// the build's client id is used when nothing is pasted, and a pasted one wins over it
	h2 := newHarness(t, func(o *integrations.Options) {
		o.OAuth = map[string]integrations.OAuthApp{integrations.KindLinear: {ClientID: "lin_client_build"}}
	})
	if m := oauthMethod(t, h2, integrations.KindLinear); !m.Available || m.OAuthApp.Source != "build" || m.OAuthApp.ClientID != "lin_client_build" {
		t.Fatalf("%+v", m.OAuthApp)
	}
	if info, err := h2.svc.SetOAuthApp(ctx, human, integrations.KindLinear, "lin_client_pasted", ""); err != nil || info.Source != "setting" || info.ClientID != "lin_client_pasted" {
		t.Fatalf("%+v %v", info, err)
	}
}

// A Linear sign-in with the pasted client id: the endpoints are the vendor's (here the fixture's, from the options, which carry
// no client id), and the fixture only accepts the pasted id.
func TestLinearSignsInWithThePastedClientID(t *testing.T) {
	ofx, ourl := oauthFixture(t, "lin_client_pasted")
	lfx := itfixture.NewLinear("")
	lts, lurl := itfixture.Serve(lfx.Server())
	t.Cleanup(lts.Close)
	h := newHarness(t, func(o *integrations.Options) {
		o.OAuth = map[string]integrations.OAuthApp{integrations.KindLinear: {AuthURL: ourl + "/authorize", TokenURL: ourl + "/token"}}
	})
	ctx := context.Background()
	if h.svc.OAuthAvailable(integrations.KindLinear) {
		t.Fatal("endpoints without a client id are not a registration")
	}
	if _, err := h.svc.SetOAuthApp(ctx, human, integrations.KindLinear, "lin_client_pasted", ""); err != nil {
		t.Fatal(err)
	}
	s, err := h.svc.OAuthStart(ctx, human, integrations.KindLinear, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.URL, "client_id=lin_client_pasted") {
		t.Fatalf("%s", s.URL)
	}
	resp, err := http.Get(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if done := waitSignedIn(t, h, s.ID); done.State != "signed_in" {
		t.Fatalf("%+v", done)
	}
	c, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindLinear, OAuthSession: s.ID, BaseURL: lurl})
	if err != nil {
		t.Fatal(err)
	}
	lfx.Token, lfx.Scheme = ofx.Issued[0], "Bearer"
	if rep, _ := h.svc.Test(ctx, c.ID); !rep.OK || rep.Identity.Method != "OAuth sign-in" {
		t.Fatalf("%+v", rep)
	}
}

// GitHub: the OAuth device flow of an app the owner registered is a second way in beside gh. The token goes to the credential
// store, the identity is read from /user, issues are read directly from the REST API (gh is not installed here), and the gh
// way still works beside it.
func TestGitHubSignsInWithTheDeviceFlowBesideGh(t *testing.T) {
	ofx, ourl := oauthFixture(t, "gh_client_pasted")
	gfx := itfixture.NewGitHub("", "acme/web")
	gts, gurl := itfixture.Serve(gfx.Server())
	t.Cleanup(gts.Close)
	now := time.Date(2026, 10, 8, 11, 0, 0, 0, time.UTC)
	gfx.Set([]itfixture.GitHubIssue{{Number: 12, Title: "Crash on start", Body: "plain", Updated: now}, {Number: 13, Title: "Typo", Body: "plain", Updated: now.Add(-time.Minute)}})
	h := newHarness(t, func(o *integrations.Options) {
		o.OAuth = map[string]integrations.OAuthApp{integrations.KindGitHub: {DeviceURL: ourl + "/device/code", TokenURL: ourl + "/token"}}
	})
	ctx := context.Background()
	if m := oauthMethod(t, h, integrations.KindGitHub); m.Available || m.Flow != "device" || m.Label != "Sign in with GitHub (device code)" {
		t.Fatalf("%+v", m)
	}
	// without a registered app GitHub still connects through gh, and refuses a pasted token
	if _, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindGitHub, Token: "ghp_pasted_token_x"}); !errors.Is(err, integrations.ErrBadRequest) {
		t.Fatalf("%v", err)
	}
	if _, err := h.svc.SetOAuthApp(ctx, human, integrations.KindGitHub, "gh_client_pasted", ""); err != nil {
		t.Fatal(err)
	}
	s, err := h.svc.OAuthStart(ctx, human, integrations.KindGitHub, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Method != "device" || s.UserCode != "ABCD-EFGH" || !strings.HasPrefix(s.URL, ourl+"/device") {
		t.Fatalf("%+v", s)
	}
	done := waitSignedIn(t, h, s.ID)
	if done.State != "signed_in" {
		t.Fatalf("%+v", done)
	}
	if b, _ := json.Marshal(done); strings.Contains(string(b), "oat_fixture") {
		t.Fatalf("%s", b)
	}
	// the API URL may only be given with a sign-in (the fixture stands in for api.github.com)
	if _, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindGitHub, BaseURL: gurl}); !errors.Is(err, integrations.ErrBadRequest) {
		t.Fatalf("%v", err)
	}
	gfx.Token = ofx.Issued[0]
	c, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindGitHub, OAuthSession: s.ID, BaseURL: gurl})
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthKind != integrations.AuthOAuth || c.CredentialRef == "" || !c.HasCredential {
		t.Fatalf("%+v", c)
	}
	if stored, _ := h.vault.Get(ctx, c.CredentialRef); !strings.Contains(stored, ofx.Issued[0]) {
		t.Fatal("the GitHub token is not in the credential store")
	}
	// gh is not installed in this harness (h.gh == nil): everything below goes directly to the REST API
	rep, _ := h.svc.Test(ctx, c.ID)
	if !rep.OK || rep.Identity.Account != "kyle-fixture" || rep.Identity.Method != "OAuth sign-in" {
		t.Fatalf("%+v", rep)
	}
	for _, st := range rep.Steps {
		if st.ID == "credential" && (!st.OK || !strings.Contains(st.Detail, "OAuth sign-in")) {
			t.Fatalf("%+v", st)
		}
	}
	a, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "web"})
	if err != nil || a.Config["repo"] != "acme/web" {
		t.Fatalf("%+v %v", a, err)
	}
	if r := h.sync(c.ID, integrations.SyncOpts{}); r.New != 2 || !r.Complete {
		t.Fatalf("%+v", r)
	}
	seen := gfx.AuthSeen()
	if len(seen) == 0 || seen[len(seen)-1] != "Bearer "+ofx.Issued[0] {
		t.Fatalf("%v", seen)
	}
	// Check against your account reads through the sign-in too, and says so
	vr, err := h.svc.Verify(ctx, human, c.ID)
	if err != nil || !vr.OK || strings.Contains(vr.Summary, "through gh") {
		t.Fatalf("%+v %v", vr, err)
	}
	// a revoked sign-in is said plainly, in words about the sign-in, not about gh
	gfx.Token = "other"
	rep, _ = h.svc.Test(ctx, c.ID)
	if rep.OK || !strings.Contains(rep.Summary, "Sign in with GitHub again") {
		t.Fatalf("%+v", rep)
	}
	if v, err := h.svc.Get(ctx, c.ID); err != nil || v.Status != integrations.StatusUnauthenticated {
		t.Fatalf("%+v %v", v, err)
	}
	// tokens are never in the database or events
	if strings.Contains(h.diskText(), "oat_fixture") || strings.Contains(h.allEventsJSON(), "oat_fixture") {
		t.Fatal("the GitHub token reached the database or an event")
	}
	// reconnecting the same connection through gh is still possible (gh now installed, standing in with a REST client)
	h.gh = host.HTTPAPI{BaseURL: gurl, Token: "gh_token_x"}
	gfx.Token = "gh_token_x"
	c2, err := h.svc.Connect(ctx, human, integrations.ConnectReq{ID: c.ID, Kind: integrations.KindGitHub})
	if err != nil || c2.AuthKind != integrations.AuthGH {
		t.Fatalf("%+v %v", c2, err)
	}
	if rep, _ := h.svc.Test(ctx, c.ID); !rep.OK || rep.Identity.Method != "GitHub CLI (gh) sign-in" {
		t.Fatalf("%+v", rep)
	}
}

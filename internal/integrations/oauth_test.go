package integrations_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/integrations/itfixture"
)

func oauthFixture(t *testing.T, clientID string) (*itfixture.OAuth, string) {
	t.Helper()
	fx := itfixture.NewOAuth(clientID)
	ts, url := itfixture.Serve(fx.Server())
	t.Cleanup(ts.Close)
	return fx, url
}

func waitSignedIn(t *testing.T, h *harness, id string) integrations.OAuthSession {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s, err := h.svc.OAuthStatus(id)
		if err != nil {
			t.Fatal(err)
		}
		if s.State != "waiting" {
			return s
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the sign-in did not finish")
	return integrations.OAuthSession{}
}

// Linear: authorization code with PKCE and a loopback redirect, no client secret; the token is refreshed (rotating refresh
// token) when it expires and stored again in the credential store.
func TestLinearSignInWithPKCEAndRefresh(t *testing.T) {
	ofx, ourl := oauthFixture(t, "lin_client_fixture")
	ofx.ExpiresIn = 600
	lfx := itfixture.NewLinear("") // checks the Bearer token below instead
	lts, lurl := itfixture.Serve(lfx.Server())
	t.Cleanup(lts.Close)
	h := newHarness(t, func(o *integrations.Options) {
		o.OAuth = map[string]integrations.OAuthApp{integrations.KindLinear: {ClientID: "lin_client_fixture", AuthURL: ourl + "/authorize", TokenURL: ourl + "/token"}}
	})
	ctx := context.Background()
	if !h.svc.OAuthAvailable(integrations.KindLinear) || h.svc.OAuthAvailable(integrations.KindNotion) {
		t.Fatal("availability")
	}
	if _, err := h.svc.OAuthStart(ctx, "agent:x", integrations.KindLinear, ""); !errors.Is(err, integrations.ErrNotHuman) {
		t.Fatalf("%v", err)
	}
	s, err := h.svc.OAuthStart(ctx, human, integrations.KindLinear, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Method != "pkce" || !strings.Contains(s.URL, "code_challenge_method=S256") || strings.Contains(s.URL, "client_secret") || !strings.Contains(s.URL, "redirect_uri=http%3A%2F%2F127.0.0.1%3A") {
		t.Fatalf("%+v", s)
	}
	// the person approves in the browser: the fixture redirects to Drydock's loopback callback
	resp, err := http.Get(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	done := waitSignedIn(t, h, s.ID)
	if done.State != "signed_in" {
		t.Fatalf("%+v", done)
	}
	// a status never carries the token
	if b, _ := json.Marshal(done); strings.Contains(string(b), "oat_fixture") {
		t.Fatalf("%s", b)
	}
	c, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindLinear, OAuthSession: s.ID, BaseURL: lurl})
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthKind != integrations.AuthOAuth {
		t.Fatalf("%+v", c)
	}
	// the session is spent
	if _, err := h.svc.Connect(ctx, human, integrations.ConnectReq{ID: c.ID, OAuthSession: s.ID}); !errors.Is(err, integrations.ErrBadRequest) {
		t.Fatalf("%v", err)
	}
	lfx.Token, lfx.Scheme = ofx.Issued[0], "Bearer"
	rep, _ := h.svc.Test(ctx, c.ID)
	if !rep.OK || rep.Identity.Method != "OAuth sign-in" || strings.Join(rep.Identity.Scopes, ",") != "read,write" {
		t.Fatalf("%+v", rep)
	}
	// ten minutes later the access token has expired: it is refreshed with the refresh token and stored again
	h.clk.Advance(11 * time.Minute)
	first := ofx.Issued[0]
	lfx.Token = "" // the fixture takes any token now; which one arrived is checked below
	rep, _ = h.svc.Test(ctx, c.ID)
	if !rep.OK || ofx.Refreshes != 1 || len(ofx.Issued) != 2 {
		t.Fatalf("%+v refreshes %d", rep, ofx.Refreshes)
	}
	seen := lfx.AuthSeen()
	if seen[len(seen)-1] != "Bearer "+ofx.Issued[1] {
		t.Fatalf("the refreshed token was not used: %v", seen[len(seen)-1])
	}
	stored, _ := h.vault.Get(ctx, c.CredentialRef)
	if !strings.Contains(stored, ofx.Issued[1]) || strings.Contains(stored, first) {
		t.Fatal("the refreshed token was not stored")
	}
	// still valid a minute later: no second refresh
	h.clk.Advance(time.Minute)
	if rep, _ := h.svc.Test(ctx, c.ID); !rep.OK || ofx.Refreshes != 1 {
		t.Fatalf("%+v %d", rep, ofx.Refreshes)
	}
	// tokens are never in the database or events
	if strings.Contains(h.diskText(), "oat_fixture") || strings.Contains(h.diskText(), "ort_fixture") || strings.Contains(h.allEventsJSON(), "oat_fixture") {
		t.Fatal("an OAuth token reached the database or an event")
	}
}

// Sentry: the device authorization grant, no secret and no redirect.
func TestSentrySignInWithTheDeviceFlow(t *testing.T) {
	_, ourl := oauthFixture(t, "sen_client_fixture")
	h := newHarness(t, func(o *integrations.Options) {
		o.OAuth = map[string]integrations.OAuthApp{integrations.KindSentry: {ClientID: "sen_client_fixture", DeviceURL: ourl + "/device/code", TokenURL: ourl + "/token"}}
	})
	ctx := context.Background()
	s, err := h.svc.OAuthStart(ctx, human, integrations.KindSentry, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Method != "device" || s.UserCode != "ABCD-EFGH" || !strings.Contains(s.URL, "user_code=ABCD-EFGH") {
		t.Fatalf("%+v", s)
	}
	done := waitSignedIn(t, h, s.ID)
	if done.State != "signed_in" {
		t.Fatalf("%+v", done)
	}
	if _, err := h.svc.Connect(ctx, human, integrations.ConnectReq{Kind: integrations.KindSentry, OAuthSession: s.ID}); err != nil {
		t.Fatal(err)
	}
}

func TestSignInIsOffWithoutARegisteredApp(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	for _, k := range []string{integrations.KindLinear, integrations.KindSentry, integrations.KindNotion, integrations.KindAikido, integrations.KindGitHub} {
		if _, err := h.svc.OAuthStart(ctx, human, k, ""); !errors.Is(err, integrations.ErrOAuthUnavailable) {
			t.Errorf("%s: %v", k, err)
		}
	}
	for _, k := range h.svc.Kinds() {
		for _, m := range k.Auth {
			if m.ID == integrations.AuthOAuth && m.Available {
				t.Errorf("%s offers a sign-in it cannot do", k.Kind)
			}
		}
	}
}

// Linear rotates refresh tokens: each is accepted once. Callers that find the access token expired at the same moment (a sync,
// Test and the settings' choices) must refresh it once between them; the others wait and use the token the first one stored,
// instead of spending the same refresh token again and being told invalid_grant (which reads as a revoked sign-in).
func TestConcurrentCallersRefreshAnExpiredSignInOnce(t *testing.T) {
	ofx, ourl := oauthFixture(t, "lin_client_fixture")
	ofx.ExpiresIn = 600
	ofx.RefreshDelay = 50 * time.Millisecond
	lfx := itfixture.NewLinear("")
	lts, lurl := itfixture.Serve(lfx.Server())
	t.Cleanup(lts.Close)
	h := newHarness(t, func(o *integrations.Options) {
		o.OAuth = map[string]integrations.OAuthApp{integrations.KindLinear: {ClientID: "lin_client_fixture", AuthURL: ourl + "/authorize", TokenURL: ourl + "/token"}}
	})
	ctx := context.Background()
	s, err := h.svc.OAuthStart(ctx, human, integrations.KindLinear, "")
	if err != nil {
		t.Fatal(err)
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

	h.clk.Advance(11 * time.Minute) // the access token has expired
	const n = 8
	errs := make(chan error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			<-start
			_, err := h.svc.Choices(ctx, c.ID, "team", nil)
			errs <- err
		}()
	}
	close(start)
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("caller %d: %v", i, err)
		}
	}
	if acc, rej := ofx.RefreshCounts(); acc != 1 || rej != 0 {
		t.Fatalf("refreshes accepted %d, rejected %d; want exactly one", acc, rej)
	}
	// every call went out with the refreshed token, which is the one stored
	stored, _ := h.vault.Get(ctx, c.CredentialRef)
	fresh := ofx.Issued[len(ofx.Issued)-1]
	if !strings.Contains(stored, fresh) {
		t.Fatal("the refreshed token was not stored")
	}
	seen := lfx.AuthSeen()
	for _, a := range seen[len(seen)-n:] {
		if a != "Bearer "+fresh {
			t.Fatalf("a caller used %q", a)
		}
	}
	if v, _ := h.svc.Get(ctx, c.ID); v.Status == integrations.StatusUnauthenticated {
		t.Fatalf("%+v", v.Connection)
	}
}

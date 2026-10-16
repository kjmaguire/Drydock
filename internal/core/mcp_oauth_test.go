package core_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/mcp/oauthfake"
)

func newOAuthFake(t *testing.T) *oauthfake.Server {
	f := oauthfake.New()
	t.Cleanup(f.Close)
	return f
}

type oauthStatusT struct {
	ID       string
	State    string
	URL      string
	Error    string
	SignedIn bool
	At       int64
}

func openOAuthApp(t *testing.T) *app {
	return openMCPApp(t, &claude.Adapter{Exe: "unused"})
}

// signIn plays the browser: it opens the sign-in address the core returned and follows the redirects back to the loopback address.
func signIn(t *testing.T, a *app, id string) oauthStatusT {
	t.Helper()
	var st oauthStatusT
	a.call("mcplib.oauth.begin", map[string]string{"id": id}, &st)
	if st.State != "waiting" || !strings.HasPrefix(st.URL, "http") {
		t.Fatalf("begin: %+v", st)
	}
	resp, err := http.Get(st.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	a.waitFor("the sign-in to finish", func() bool {
		st = oauthStatusT{}
		a.call("mcplib.oauth.status", map[string]string{"id": id}, &st)
		return st.State != "waiting"
	})
	return st
}

func TestOAuthSignInKeepsRefreshesAndForgetsTheTokens(t *testing.T) {
	f := newOAuthFake(t)
	a := openOAuthApp(t)

	var lib core.MCPLibraryServer
	yes := true
	a.call("mcplib.save", core.MCPServerInput{ID: "oauthy", Display: "OAuth server", Transport: "http", URL: f.URL(), OAuth: &yes}, &lib)
	if !lib.OAuth || lib.OAuthAt != 0 || lib.HasToken {
		t.Fatalf("saved %+v", lib)
	}
	// not signed in yet: the server cannot be reached and the page is told why
	if err := a.callErr("mcplib.discover", map[string]string{"id": "oauthy"}); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("discover before sign-in: %v", err)
	}
	// a bearer token and OAuth do not go together
	if err := a.callErr("mcplib.save", core.MCPServerInput{ID: "oauthy", Display: "x", Transport: "http", URL: f.URL(), BearerToken: "abc"}); err == nil {
		t.Fatal("a bearer token was accepted next to OAuth")
	}

	st := signIn(t, a, "oauthy")
	if st.State != "idle" || !st.SignedIn || st.At == 0 || st.Error != "" {
		t.Fatalf("after sign-in: %+v", st)
	}
	if f.Registrations() != 1 {
		t.Fatalf("the client should have registered itself once, not %d times", f.Registrations())
	}
	first := f.Access()
	if first == "" {
		t.Fatal("no access token was issued")
	}
	// the tools were learned with the token
	var libs []core.MCPLibraryServer
	a.call("mcplib.list", nil, &libs)
	if len(libs) != 1 || !libs[0].OAuth || libs[0].OAuthAt == 0 || len(libs[0].Tools) != 1 || libs[0].Tools[0].Name != "whoami" {
		t.Fatalf("after sign-in: %+v", libs)
	}

	// none of the secrets is in anything the app keeps
	for _, secret := range []string{first, "refresh-1", "client-secret-client-1"} {
		if w := leaks(t, a, secret); len(w) > 0 {
			t.Fatalf("%q leaked into %v", secret, w)
		}
	}
	for _, typ := range []string{"mcp.server_oauth_waiting", "mcp.server_oauth_done", "mcp.server_saved"} {
		for _, e := range a.eventsOf(typ) {
			if strings.Contains(fmt.Sprint(e), first) || strings.Contains(fmt.Sprint(e), "refresh-1") {
				t.Fatalf("a token is in %s: %v", typ, e)
			}
		}
	}
	if len(a.eventsOf("mcp.server_oauth_done")) != 1 {
		t.Fatal("no sign-in event")
	}

	// sign out forgets everything and says so
	st = oauthStatusT{}
	a.call("mcplib.oauth.signout", map[string]string{"id": "oauthy"}, &st)
	if st.SignedIn || st.At != 0 {
		t.Fatalf("after sign-out: %+v", st)
	}
	if err := a.callErr("mcplib.discover", map[string]string{"id": "oauthy"}); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("discover after sign-out: %v", err)
	}
}

// With tokens that live a second, the next use after they lapse goes to the token endpoint with the refresh token and carries on;
// when the server refuses the refresh the page is told the sign-in is gone.
func TestOAuthRefreshUsesTheRefreshTokenAndRevocationShowsAsSignedOut(t *testing.T) {
	f := newOAuthFake(t)
	f.SetTTL(1)
	a := openOAuthApp(t)
	yes := true
	var lib core.MCPLibraryServer
	a.call("mcplib.save", core.MCPServerInput{ID: "oauthy", Display: "OAuth server", Transport: "http", URL: f.URL(), OAuth: &yes}, &lib)
	if st := signIn(t, a, "oauthy"); !st.SignedIn {
		t.Fatalf("sign-in: %+v", st)
	}
	first := f.Access()
	time.Sleep(12 * time.Second) // the client treats a token as expired 10 seconds early
	a.call("mcplib.discover", map[string]string{"id": "oauthy"}, &lib)
	if f.Refreshes() < 1 {
		t.Fatalf("the access token was not refreshed (%d refreshes)", f.Refreshes())
	}
	if now := f.Access(); now == first || len(lib.Tools) != 1 {
		t.Fatalf("after the refresh: access %q (was %q), tools %v", now, first, lib.Tools)
	}
	if w := leaks(t, a, f.Access()); len(w) > 0 {
		t.Fatalf("the refreshed token leaked into %v", w)
	}

	// the human revokes the app at the provider: the refresh is refused
	f.Revoke()
	time.Sleep(12 * time.Second)
	err := a.callErr("mcplib.discover", map[string]string{"id": "oauthy"})
	if err == nil || !strings.Contains(err.Error(), "sign in again") {
		t.Fatalf("after revocation: %v", err)
	}
	var st oauthStatusT
	st = oauthStatusT{}
	a.call("mcplib.oauth.status", map[string]string{"id": "oauthy"}, &st)
	if st.SignedIn {
		t.Fatalf("a revoked sign-in still shows as signed in: %+v", st)
	}
	if len(a.eventsOf("mcp.server_oauth_expired")) == 0 {
		t.Fatal("no expiry event")
	}
}

// A browser that comes back with an error, or a sign-in that nobody finishes, ends as a plain message; a second answer is refused.
func TestOAuthSignInFailuresAreReportedAndCanBeCancelled(t *testing.T) {
	f := newOAuthFake(t)
	a := openOAuthApp(t)
	yes := true
	var lib core.MCPLibraryServer
	a.call("mcplib.save", core.MCPServerInput{ID: "oauthy", Display: "OAuth server", Transport: "http", URL: f.URL(), OAuth: &yes}, &lib)

	var st oauthStatusT
	st = oauthStatusT{}
	a.call("mcplib.oauth.begin", map[string]string{"id": "oauthy"}, &st)
	au, _ := url.Parse(st.URL)
	redirect := au.Query().Get("redirect_uri")
	// the human pressed "deny"
	resp, err := http.Get(redirect + "?error=access_denied&error_description=" + url.QueryEscape("the user said no") + "&state=" + url.QueryEscape(au.Query().Get("state")))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	a.waitFor("the failure", func() bool {
		st = oauthStatusT{}
		st = oauthStatusT{}
		a.call("mcplib.oauth.status", map[string]string{"id": "oauthy"}, &st)
		return st.State == "failed"
	})
	if !strings.Contains(st.Error, "access_denied") || st.SignedIn {
		t.Fatalf("denied: %+v", st)
	}
	// the address stops answering once the flow is over
	if r, err := http.Get(redirect + "?code=late&state=x"); err == nil {
		r.Body.Close()
		t.Fatal("the callback address still answers after the flow ended")
	}

	// A forged answer (anything on this computer can reach the callback port) is turned away: it signs nobody in, and it does not
	// use up the flow's one answer either, so the genuine answer that follows still counts (audit 2026-10-16, M9).
	st = oauthStatusT{}
	a.call("mcplib.oauth.begin", map[string]string{"id": "oauthy"}, &st)
	au, _ = url.Parse(st.URL)
	redirect = au.Query().Get("redirect_uri")
	for _, forged := range []string{"?code=forged&state=not-the-state", "?code=forged", "?error=access_denied", "?code=forged&state="} {
		resp, err = http.Get(redirect + forged)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: the forged answer was not turned away: %d", forged, resp.StatusCode)
		}
	}
	st = oauthStatusT{}
	a.call("mcplib.oauth.status", map[string]string{"id": "oauthy"}, &st)
	if st.State != "waiting" || st.SignedIn || f.Access() != "" {
		t.Fatalf("a forged answer ended or answered the sign-in: %+v", st)
	}
	resp, err = http.Get(st.URL) // the genuine answer: the provider redirects the browser back with the code and the state
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	a.waitFor("the genuine answer to sign in", func() bool {
		st = oauthStatusT{}
		a.call("mcplib.oauth.status", map[string]string{"id": "oauthy"}, &st)
		return st.State != "waiting"
	})
	if !st.SignedIn || f.Access() == "" {
		t.Fatalf("the genuine answer after forged ones did not sign in: %+v", st)
	}
	a.call("mcplib.oauth.signout", map[string]string{"id": "oauthy"}, &st)

	// cancel while waiting
	st = oauthStatusT{}
	a.call("mcplib.oauth.begin", map[string]string{"id": "oauthy"}, &st)
	if st.State != "waiting" {
		t.Fatalf("begin: %+v", st)
	}
	st = oauthStatusT{}
	a.call("mcplib.oauth.cancel", map[string]string{"id": "oauthy"}, &st)
	a.waitFor("the cancelled flow to end", func() bool {
		st = oauthStatusT{}
		a.call("mcplib.oauth.status", map[string]string{"id": "oauthy"}, &st)
		return st.State != "waiting"
	})
	if st.SignedIn {
		t.Fatalf("cancel: %+v", st)
	}

	// a server that is not set up for OAuth cannot be signed in to
	no := false
	a.call("mcplib.save", core.MCPServerInput{ID: "oauthy", Display: "OAuth server", Transport: "http", URL: f.URL(), OAuth: &no}, &lib)
	if err := a.callErr("mcplib.oauth.begin", map[string]string{"id": "oauthy"}); err == nil {
		t.Fatal("began a sign-in for a server without OAuth")
	}
}

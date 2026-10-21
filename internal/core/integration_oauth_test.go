package core_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
)

// An integration connects with an OAuth sign-in to the vendor's hosted MCP server instead of a pasted token: the sign-in is a
// library sign-in, the connection refers to it, and signing out there leaves the connection without a credential.
func TestIntegrationConnectsWithAnOAuthSignIn(t *testing.T) {
	f := newOAuthFake(t)
	a := openOAuthApp(t)
	var start core.IntegrationOAuthStart
	a.call("integration.oauthBegin", map[string]string{"kind": "linear", "baseUrl": f.URL()}, &start)
	if start.LibraryID != "linauth" || start.OAuthRef != "oauth/mcplib/linauth" || start.Status.State != "waiting" || !strings.HasPrefix(start.Status.URL, "http") {
		t.Fatalf("begin %+v", start)
	}
	// connecting before the sign-in finished is refused
	if err := a.callErr("integration.connect", map[string]any{"kind": "linear", "oauthRef": start.OAuthRef, "baseUrl": f.URL(), "skipTest": true}); err == nil || !strings.Contains(err.Error(), "sign in first") {
		t.Fatalf("connect before sign-in: %v", err)
	}
	resp, err := http.Get(start.Status.URL) // the browser
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	a.waitFor("the sign-in", func() bool {
		var st oauthStatusT
		a.call("mcplib.oauth.status", map[string]string{"id": start.LibraryID}, &st)
		return st.SignedIn
	})
	var out struct {
		Connection struct {
			ID            string `json:"id"`
			AuthKind      string `json:"authKind"`
			HasCredential bool   `json:"hasCredential"`
		} `json:"connection"`
	}
	a.call("integration.connect", map[string]any{"kind": "linear", "oauthRef": start.OAuthRef, "baseUrl": f.URL(), "skipTest": true}, &out)
	if out.Connection.AuthKind != "oauth" || !out.Connection.HasCredential {
		t.Fatalf("connected %+v", out.Connection)
	}
	// the access token is nowhere the app keeps it in the clear
	if w := leaks(t, a, f.Access()); len(w) > 0 {
		t.Fatalf("the access token leaked into %v", w)
	}
	// a forged reference, a token next to OAuth, and Aikido (no hosted server) are refused
	for _, bad := range []map[string]any{
		{"kind": "linear", "oauthRef": "oauth/mcplib/../../x", "skipTest": true},
		{"kind": "linear", "oauthRef": start.OAuthRef, "token": "lin_api_123456789", "skipTest": true},
	} {
		if err := a.callErr("integration.connect", bad); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
	if err := a.callErr("integration.oauthBegin", map[string]string{"kind": "aikido"}); err == nil {
		t.Error("Aikido has no hosted server to sign in to")
	}
	// signing out in the library leaves the connection without a credential
	a.call("mcplib.oauth.signout", map[string]string{"id": start.LibraryID}, nil)
	var list []struct {
		ID            string `json:"id"`
		HasCredential bool   `json:"hasCredential"`
	}
	a.call("integration.list", nil, &list)
	for _, cn := range list {
		if cn.ID == out.Connection.ID && cn.HasCredential {
			t.Fatal("the connection still claims a credential after the sign-out")
		}
	}
}

package core_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/mcp/oauthfake"
)

// Decision P122: OAuth for catalog servers whose provider has no dynamic client registration (GitHub's hosted MCP server). The
// person registers an OAuth app with the provider and enters its client id and secret; the sign-in uses that client. Tested
// against the fake provider (a test double) started without a registration endpoint.

const (
	preClientID     = "Iv1.drydock0test0client"
	preClientSecret = "app-secret-Zq81xWm4Pt7Lc2Rv9Kd5"
)

func newNoDCRFake(t *testing.T) *oauthfake.Server {
	f := oauthfake.NewWithoutRegistration()
	t.Cleanup(f.Close)
	f.Preregister(preClientID, preClientSecret)
	return f
}

func TestOAuthWithARegisteredClientWhenTheProviderHasNoRegistration(t *testing.T) {
	f := newNoDCRFake(t)
	a := openOAuthApp(t)
	yes := true
	var lib core.MCPLibraryServer
	a.call("mcplib.save", core.MCPServerInput{ID: "ghlike", Display: "No-registration server", Transport: "http", URL: f.URL(), OAuth: &yes}, &lib)

	// without a client the sign-in cannot start, and the page says what to do
	var st oauthStatusT
	a.call("mcplib.oauth.begin", map[string]string{"id": "ghlike"}, &st)
	a.waitFor("the sign-in to fail", func() bool {
		st = oauthStatusT{}
		a.call("mcplib.oauth.status", map[string]string{"id": "ghlike"}, &st)
		return st.State == "failed"
	})
	if !strings.Contains(st.Error, "register an OAuth app") || f.Registrations() != 0 {
		t.Fatalf("%+v", st)
	}

	// a secret without its id, or an id with a space, is refused
	if err := a.callErr("mcplib.oauth.client", core.OAuthClientReq{ID: "ghlike", ClientSecret: preClientSecret}); err == nil {
		t.Fatal("a secret without a client id was accepted")
	}
	if err := a.callErr("mcplib.oauth.client", core.OAuthClientReq{ID: "ghlike", ClientID: "has space"}); err == nil {
		t.Fatal("a client id with a space was accepted")
	}
	a.call("mcplib.oauth.client", core.OAuthClientReq{ID: "ghlike", ClientID: preClientID, ClientSecret: preClientSecret}, &lib)
	if !lib.OAuthClient || lib.OAuthAt != 0 {
		t.Fatalf("%+v", lib)
	}
	st = signIn(t, a, "ghlike")
	if !st.SignedIn || st.Error != "" || f.Registrations() != 0 || f.Access() == "" {
		t.Fatalf("sign-in with the registered client: %+v (registrations %d)", st, f.Registrations())
	}
	a.call("mcplib.discover", map[string]string{"id": "ghlike"}, &lib)
	if len(lib.Tools) != 1 || lib.Tools[0].Name != "whoami" {
		t.Fatalf("tools: %+v", lib.Tools)
	}
	// the client secret is in the vault only, the scanner knows it, the event says only that a client was set
	for _, secret := range []string{preClientSecret, f.Access()} {
		if w := leaks(t, a, secret); len(w) > 0 {
			t.Fatalf("%q leaked into %v", secret, w)
		}
	}
	if got := a.c.Scanner.Redact("echo " + preClientSecret); strings.Contains(got, preClientSecret) {
		t.Fatalf("the scanner does not know the client secret: %q", got)
	}
	ev := a.eventsOf("mcp.server_oauth_client")
	if len(ev) != 1 || strings.Contains(fmt.Sprint(ev), preClientID) {
		t.Fatalf("%v", ev)
	}

	// removing the client signs the server out: the tokens belonged to it
	lib = core.MCPLibraryServer{}
	a.call("mcplib.oauth.client", core.OAuthClientReq{ID: "ghlike"}, &lib)
	if lib.OAuthClient || lib.OAuthAt != 0 {
		t.Fatalf("%+v", lib)
	}
	if err := a.callErr("mcplib.discover", map[string]string{"id": "ghlike"}); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("discover after the client was removed: %v", err)
	}
	// turning OAuth off forgets a client too
	a.call("mcplib.oauth.client", core.OAuthClientReq{ID: "ghlike", ClientID: preClientID}, &lib)
	no := false
	lib = core.MCPLibraryServer{}
	a.call("mcplib.save", core.MCPServerInput{ID: "ghlike", Display: "No-registration server", Transport: "http", URL: f.URL(), OAuth: &no}, &lib)
	if lib.OAuth || lib.OAuthClient {
		t.Fatalf("%+v", lib)
	}
	if err := a.callErr("mcplib.oauth.client", core.OAuthClientReq{ID: "ghlike", ClientID: preClientID}); err == nil {
		t.Fatal("a client was set on a server that does not use OAuth")
	}
}

// A catalog entry with an oauth_app (the shipped gh entry has one) is connected from the gallery with the person's app: the
// library server is set to OAuth with that client and the sign-in starts.
func TestGalleryConnectsAnEntryThroughTheOAuthAppThePersonRegistered(t *testing.T) {
	f := newNoDCRFake(t)
	cat, err := mcp.ParseCatalog([]byte(fmt.Sprintf(`schema: 1
servers:
  - id: ghd
    display: GitHub double
    publisher: Tests
    brand: test
    summary: ghd
    transport: http
    url: %q
    auth:
      kind: bearer
      keyring: "mcp/ghd"
      oauth_app: {register_url: "https://github.com/settings/applications/new", help: "callback http://127.0.0.1/callback"}
    trust: private_data
    read_only_tools: [whoami]
    identity: {tool: whoami}
    license: {server: MIT}
  - {id: plain, display: Plain double, publisher: Tests, brand: test, summary: plain, transport: http, url: "http://127.0.0.1:1/mcp", auth: {kind: bearer, keyring: "mcp/p"}, trust: private_data, license: {server: MIT}}
`, f.URL())))
	if err != nil {
		t.Fatal(err)
	}
	a := openGalleryApp(t, cat)
	if c := a.card("ghd"); c.OAuthApp == nil || c.OAuthApp.RegisterURL != "https://github.com/settings/applications/new" || c.Auth != "bearer" {
		t.Fatalf("%+v", c)
	}
	if err := a.callErr("mcplib.connect", core.MCPConnectReq{CatalogID: "plain", ClientID: preClientID}); err == nil {
		t.Fatal("an entry without an OAuth app took a client")
	}
	if err := a.callErr("mcplib.connect", core.MCPConnectReq{CatalogID: "ghd", ClientID: preClientID, Token: "tok-0123456789"}); err == nil {
		t.Fatal("a token and an app were both accepted")
	}
	var res core.MCPConnectResult
	a.call("mcplib.connect", core.MCPConnectReq{CatalogID: "ghd", ClientID: preClientID, ClientSecret: preClientSecret}, &res)
	if res.OAuth == nil || res.OAuth.State != "waiting" || !strings.Contains(res.OAuth.URL, "client_id="+preClientID) {
		t.Fatalf("%+v", res)
	}
	resp, err := http.Get(res.OAuth.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var c core.MCPGalleryCard
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if c = a.card("ghd"); c.Status == core.GalleryConnected && c.Account == "alice" {
			break
		}
	}
	if c.Status != core.GalleryConnected || c.Account != "alice" {
		var st oauthStatusT
		a.call("mcplib.oauth.status", map[string]string{"id": c.LibraryID}, &st)
		t.Fatalf("card %+v, sign-in %+v", c, st)
	}
	if f.Registrations() != 0 {
		t.Fatal("the client registered itself with a provider that has no registration")
	}
	if w := leaks(t, a, preClientSecret); len(w) > 0 {
		t.Fatalf("the client secret leaked into %v", w)
	}
}

// The shipped gh entry offers the OAuth app route next to the token.
func TestShippedGitHubEntryOffersAnOAuthApp(t *testing.T) {
	cat, err := mcp.LoadBuiltin()
	if err != nil {
		t.Fatal(err)
	}
	gh, ok := cat.Get("gh")
	if !ok || gh.Auth.Kind != mcp.AuthBearer || gh.Auth.OAuthApp == nil || !strings.HasPrefix(gh.Auth.OAuthApp.RegisterURL, "https://github.com/") {
		t.Fatalf("%+v", gh.Auth)
	}
}

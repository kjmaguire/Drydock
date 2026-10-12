package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A server's identity is what it says about itself in its answer to initialize: serverInfo (name, title, version, website) and
// instructions. A custom server is shown by that identity (decision P43), so it must come through, trimmed and as text.
func TestAppUpstreamReportsTheServersOwnIdentity(t *testing.T) {
	srv := sdk.NewServer(&sdk.Implementation{Name: "acme-tracker", Title: "Acme Tracker", Version: "3.2.1", WebsiteURL: "https://acme.example/mcp"},
		&sdk.ServerOptions{Instructions: "Use search before fetch.\n" + strings.Repeat("x", 6000)})
	sdk.AddTool(srv, &sdk.Tool{Name: "whoami", Description: "who"}, func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: `{"user":{"login":"octo","name":"Octo Cat"}}`}}}, nil, nil
	})
	sdk.AddTool(srv, &sdk.Tool{Name: "plain", Description: "plain"}, func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "alice\x1b[31m\nsecond line"}}}, nil, nil
	})
	sdk.AddTool(srv, &sdk.Tool{Name: "broken", Description: "fails"}, func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "no"}}}, nil, nil
	})
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	ts := httptest.NewServer(h)
	defer ts.Close()

	e := newEnv(t, nil)
	up, err := e.g.AppUpstream(context.Background(), httpSpec("acme", ts.URL, nil))
	if err != nil {
		t.Fatal(err)
	}
	id, err := up.Identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.Name != "acme-tracker" || id.Title != "Acme Tracker" || id.Version != "3.2.1" || id.WebsiteURL != "https://acme.example/mcp" || id.ProtocolVersion == "" {
		t.Fatalf("identity: %+v", id)
	}
	if !strings.HasPrefix(id.Instructions, "Use search before fetch.") || len(id.Instructions) > maxIdentityInstructions+3 {
		t.Fatalf("instructions must be kept and trimmed: %d bytes", len(id.Instructions))
	}

	who, err := up.Account(context.Background(), IdentitySpec{Tool: "whoami", Fields: []string{"login", "name"}})
	if err != nil || who != "octo" {
		t.Fatalf("account from a nested JSON answer: %q %v", who, err)
	}
	who, err = up.Account(context.Background(), IdentitySpec{Tool: "plain"})
	if err != nil || who != "alice[31m" {
		t.Fatalf("account from text: one line, control characters dropped: %q %v", who, err)
	}
	if _, err := up.Account(context.Background(), IdentitySpec{Tool: "broken"}); err == nil {
		t.Fatal("an error answer is not an account")
	}
}

func TestAccountFromPrefersTheFieldsInOrder(t *testing.T) {
	cases := []struct {
		structured, text string
		fields           []string
		want             string
	}{
		{`{"id":"user:1","email":"a@b.example"}`, "", []string{"email", "name", "id"}, "a@b.example"},
		{`{"id":"account:9","name":"Acme"}`, "", []string{"email", "name", "id"}, "Acme"},
		{"", `{"login":"octo"}`, []string{"login"}, "octo"},
		{"", "{not json", []string{"login"}, "{not json"},
		{`{"other":1}`, "", []string{"login"}, ""},
		{"", strings.Repeat("y", 500), nil, strings.Repeat("y", 120) + "..."},
	}
	for _, c := range cases {
		var raw json.RawMessage
		if c.structured != "" {
			raw = json.RawMessage(c.structured)
		}
		if got := accountFrom(raw, c.text, c.fields); got != c.want {
			t.Errorf("%q / %q: got %q want %q", c.structured, c.text, got, c.want)
		}
	}
}

// An OAuth access token is a bearer token: a catalog entry whose pasted tokens use another scheme (Sentry's Sentry-Bearer)
// must still send Authorization: Bearer when it is signed in with OAuth.
func TestOAuthAccessTokensAreSentAsBearerWhateverTheScheme(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	ts, _ := startHTTPUpstream(t, "", func(w http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		return true
	})
	e := newEnv(t, func(o *Options) {
		o.Credentials = CredentialFunc(func(context.Context, string, string) (string, error) { return "tok123", nil })
	})
	for _, kind := range []string{AuthDCR, AuthBearer} {
		mu.Lock()
		seen = nil
		mu.Unlock()
		up, err := e.g.AppUpstream(context.Background(), httpSpec("snt"+kind[:1], ts.URL, func(s *ServerSpec) {
			s.Trust, s.DataClass, s.Cache = TrustPrivateData, ContentPrivate, CacheSpec{Scope: "project"}
			s.Auth = AuthSpec{Kind: kind, Header: "Authorization", Scheme: "Sentry-Bearer", Keyring: "k"}
		}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := up.Tools(context.Background()); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{AuthDCR: "Bearer tok123", AuthBearer: "Sentry-Bearer tok123"}[kind]
		mu.Lock()
		got := append([]string(nil), seen...)
		mu.Unlock()
		if len(got) == 0 || got[0] != want {
			t.Fatalf("%s: sent %v, want %q", kind, got, want)
		}
	}
}

// Package oauthfake is a TEST DOUBLE for the far side of OAuth for MCP: an MCP server that wants an access token, together with
// its authorization server (discovery, dynamic client registration, authorization with PKCE verification, token, refresh,
// revocation). It exists so Drydock's side (the official SDK's handler and our wiring) can be run for real in tests and in the
// browser test harness; it is never presented as a real provider.
package oauthfake

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server is an MCP server that wants an OAuth access token, together with its authorization server (discovery, dynamic
// client registration, authorization with PKCE, token and refresh): the protocol a hosted MCP server such as Linear speaks.
// It is a test double for the other side of the protocol; Drydock's side (the official SDK's handler and our wiring) is real.
type Server struct {
	srv *httptest.Server
	mu  sync.Mutex

	access    string // the access token the MCP endpoint accepts right now
	ttl       int    // seconds an issued access token lives
	revoked   bool   // the refresh grant is refused
	seq       int
	clients   map[string]string   // client id -> redirect uri
	codes     map[string][]string // code -> {challenge, client id}
	mcpCalls  int
	refreshes int
	regs      int
	noDCR     bool              // the authorization server offers no dynamic client registration (decision P122)
	secrets   map[string]string // client id -> secret of a pre-registered client ("" for a public one)
}

// loopbackAnyPort is the redirect of a pre-registered client: http://127.0.0.1/callback on any port, as GitHub documents for a
// loopback redirect of an OAuth app.
const loopbackAnyPort = "loopback"

// New starts the server. Close it when done.
func New() *Server { return start(false) }

// NewWithoutRegistration starts a provider that offers no dynamic client registration, as GitHub's: only a client registered
// beforehand (Preregister) can sign in.
func NewWithoutRegistration() *Server { return start(true) }

// Preregister registers a client with the provider the way a person registers an OAuth app: its id, its secret ("" for a
// public client) and the redirect http://127.0.0.1/callback on any port.
func (f *Server) Preregister(id, secret string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clients[id] = loopbackAnyPort
	f.secrets[id] = secret
}

func start(noDCR bool) *Server {
	f := &Server{ttl: 3600, clients: map[string]string{}, codes: map[string][]string{}, noDCR: noDCR, secrets: map[string]string{}}
	mux := http.NewServeMux()
	mcpSrv := sdk.NewServer(&sdk.Implementation{Name: "oauthy", Title: "OAuth test double", Version: "1"}, nil)
	type in struct{}
	sdk.AddTool(mcpSrv, &sdk.Tool{Name: "whoami", Description: "who is signed in"}, func(ctx context.Context, r *sdk.CallToolRequest, _ in) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "alice"}}}, nil, nil
	})
	mcpHandler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return mcpSrv }, nil)
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.mcpCalls++
		cur := f.access
		f.mu.Unlock()
		if cur == "" || r.Header.Get("Authorization") != "Bearer "+cur {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp", scope="read"`, f.srv.URL))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"resource": f.srv.URL + "/mcp", "authorization_servers": []string{f.srv.URL}, "scopes_supported": []string{"read"}, "bearer_methods_supported": []string{"header"}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		m := map[string]any{
			"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize", "token_endpoint": f.srv.URL + "/token", "registration_endpoint": f.srv.URL + "/register",
			"response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
			"token_endpoint_auth_methods_supported": []string{"client_secret_post", "client_secret_basic", "none"}, "scopes_supported": []string{"read", "offline_access"},
			"authorization_response_iss_parameter_supported": true,
		}
		if f.noDCR {
			delete(m, "registration_endpoint")
		}
		writeJSON(w, m)
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		if f.noDCR {
			http.NotFound(w, r)
			return
		}
		var m struct {
			RedirectURIs []string `json:"redirect_uris"`
		}
		_ = json.NewDecoder(r.Body).Decode(&m)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.regs++
		id := fmt.Sprintf("client-%d", f.regs)
		f.clients[id] = m.RedirectURIs[0]
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"client_id": id, "client_secret": "client-secret-" + id, "redirect_uris": m.RedirectURIs, "token_endpoint_auth_method": "client_secret_basic"})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f.mu.Lock()
		redirect, ok := f.clients[q.Get("client_id")]
		f.mu.Unlock()
		if ok && redirect == loopbackAnyPort {
			// a registered app's loopback redirect: the port may be any (the path and host may not)
			if u, err := url.Parse(q.Get("redirect_uri")); err == nil && u.Scheme == "http" && u.Hostname() == "127.0.0.1" && u.Path == "/callback" {
				redirect = q.Get("redirect_uri")
			} else {
				redirect = ""
			}
		}
		if !ok || redirect == "" || redirect != q.Get("redirect_uri") || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("response_type") != "code" {
			http.Error(w, "bad authorization request", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.seq++
		code := fmt.Sprintf("code-%d", f.seq)
		f.codes[code] = []string{q.Get("code_challenge"), q.Get("client_id")}
		f.mu.Unlock()
		// the human approved: go back to the client with the code
		http.Redirect(w, r, redirect+"?code="+code+"&state="+url.QueryEscape(q.Get("state"))+"&iss="+url.QueryEscape(f.srv.URL), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id, secret, hasBasic := r.BasicAuth()
		if !hasBasic {
			id, secret = r.Form.Get("client_id"), r.Form.Get("client_secret")
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		want := "client-secret-" + id
		if s, pre := f.secrets[id]; pre {
			want = s
		}
		if _, ok := f.clients[id]; !ok || secret != want {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			c, ok := f.codes[r.Form.Get("code")]
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if !ok || c[1] != id || base64.RawURLEncoding.EncodeToString(sum[:]) != c[0] {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			delete(f.codes, r.Form.Get("code"))
		case "refresh_token":
			if f.revoked || r.Form.Get("refresh_token") != "refresh-1" {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			f.refreshes++
		default:
			http.Error(w, `{"error":"unsupported_grant_type"}`, http.StatusBadRequest)
			return
		}
		f.seq++
		f.access = fmt.Sprintf("access-token-%d-zq81", f.seq)
		writeJSON(w, map[string]any{"access_token": f.access, "token_type": "Bearer", "expires_in": f.ttl, "refresh_token": "refresh-1", "scope": "read"})
	})
	f.srv = httptest.NewServer(mux)
	return f
}

// URL is the address of the MCP endpoint to give to Drydock.
func (f *Server) URL() string { return f.srv.URL + "/mcp" }

// Close stops the server.
func (f *Server) Close() { f.srv.Close() }

// Access is the access token the MCP endpoint accepts right now ("" before anyone signed in).
func (f *Server) Access() string { f.mu.Lock(); defer f.mu.Unlock(); return f.access }

// SetTTL sets how many seconds an access token issued from now on lives.
func (f *Server) SetTTL(seconds int) { f.mu.Lock(); f.ttl = seconds; f.mu.Unlock() }

// Revoke makes the refresh grant fail from now on, as when the user removes the app at the provider.
func (f *Server) Revoke() { f.mu.Lock(); f.revoked = true; f.mu.Unlock() }

// Registrations is how many clients registered themselves; Refreshes how many refresh grants were served.
func (f *Server) Registrations() int { f.mu.Lock(); defer f.mu.Unlock(); return f.regs }
func (f *Server) Refreshes() int     { f.mu.Lock(); defer f.mu.Unlock(); return f.refreshes }

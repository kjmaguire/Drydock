package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"

	"github.com/kjmaguire/drydock/internal/netguard"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/storage"
)

// OAuth for http MCP servers (docs/mcp.md "OAuth"). The human clicks "Sign in" on a library server; Drydock runs the OAuth 2.1
// authorization-code flow with PKCE using the official MCP SDK's auth.AuthorizationCodeHandler (server discovery, dynamic
// client registration, the token exchange), opens nothing itself and only hands the sign-in address to the page. The browser
// comes back to a loopback address on this machine that accepts one request. The tokens and the registered client are kept in
// the credential vault (mcp:<id>:oauth) and nowhere else; the gateway asks for the access token at call time and Drydock
// refreshes it with the stored refresh token. Agents never see any of it.

const oauthFlowTimeout = 10 * time.Minute

// OAuthStatus is the state of a server's sign-in, for the page.
type OAuthStatus struct {
	ID    string `json:"id"`
	State string `json:"state"` // idle | waiting | done | failed
	// URL is the sign-in page the human opens in a browser, while State is waiting.
	URL      string `json:"url,omitempty"`
	Error    string `json:"error,omitempty"`
	SignedIn bool   `json:"signedIn"`
	At       int64  `json:"at,omitempty"`
	Scope    string `json:"scope,omitempty"`
}

// oauthSaved is what the vault holds under mcp:<id>:oauth.
type oauthSaved struct {
	ClientID     string        `json:"clientId"`
	ClientSecret string        `json:"clientSecret,omitempty"`
	TokenURL     string        `json:"tokenUrl"`
	AuthStyle    int           `json:"authStyle"`
	Scopes       []string      `json:"scopes,omitempty"`
	Token        *oauth2.Token `json:"token"`
}

type oauthFlow struct {
	mu     sync.Mutex
	state  string
	url    string
	err    string
	cancel context.CancelFunc
	ready  chan struct{} // closed when the address is known or the flow ended
	once   sync.Once
}

func (f *oauthFlow) signal() { f.once.Do(func() { close(f.ready) }) }

type oauthRuntime struct {
	mu      sync.Mutex
	flows   map[string]*oauthFlow
	sources map[string]*oauthSource
}

var oauthRuntimes sync.Map // *mcpServers -> *oauthRuntime

func (s *mcpServers) orun() *oauthRuntime {
	v, _ := oauthRuntimes.LoadOrStore(s, &oauthRuntime{flows: map[string]*oauthFlow{}, sources: map[string]*oauthSource{}})
	return v.(*oauthRuntime)
}

// ---- network guard ----

// oauthPolicy is what the sign-in of one MCP server may contact (internal/netguard, audit 2026-10-16, M9). The addresses it
// names (resource metadata, authorization server, token endpoint) come from that server, so an untrusted one must not be able to
// point Drydock at an address inside the person's network or at a cloud metadata endpoint. The server the person configured is
// itself allowed, whatever it resolves to; a server on this computer may also name other loopback addresses and ports. A server
// on the local network is NOT a reason to open the whole network.
func oauthPolicy(serverURL string) netguard.Policy {
	var p netguard.Policy
	if u, err := url.Parse(serverURL); err == nil {
		p.AllowHost = u.Hostname()
		p.AllowLoopback = netguard.IsLoopbackHost(u.Hostname())
	}
	return p
}

func oauthHTTPClient(serverURL string) *http.Client {
	pol := oauthPolicy(serverURL)
	return &http.Client{
		// every request, and every hop of a redirect, resolves the name itself, refuses non-public addresses and dials the
		// address it checked
		Transport: netguard.Guard{Base: pol.Transport(), Policy: pol},
		Timeout:   60 * time.Second,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

// ---- status ----

func (s *mcpServers) oauthStatus(ctx context.Context, id string) (OAuthStatus, error) {
	r, err := s.load(ctx, id)
	if err != nil {
		return OAuthStatus{}, err
	}
	st := OAuthStatus{ID: id, State: "idle", SignedIn: r.OAuth && r.OAuthAt > 0, At: r.OAuthAt, Scope: r.OAuthScope}
	rt := s.orun()
	rt.mu.Lock()
	f := rt.flows[id]
	rt.mu.Unlock()
	if f != nil {
		f.mu.Lock()
		st.State, st.URL, st.Error = f.state, f.url, f.err
		f.mu.Unlock()
		if st.State != "waiting" {
			st.URL = ""
		}
		if st.State == "done" {
			st.State = "idle"
		}
	}
	return st, nil
}

// ---- sign in ----

func (s *mcpServers) oauthBegin(ctx context.Context, id string) (OAuthStatus, error) {
	if err := s.c.RequireLicensed(ctx, "add_connection"); err != nil {
		return OAuthStatus{}, err
	}
	r, err := s.load(ctx, id)
	if err != nil {
		return OAuthStatus{}, err
	}
	if r.Transport != "http" || !r.OAuth {
		return OAuthStatus{}, errors.New("this server is not set up to sign in with OAuth")
	}
	if s.c.secrets == nil {
		return OAuthStatus{}, errNoVault
	}
	rt := s.orun()
	rt.mu.Lock()
	if f := rt.flows[id]; f != nil {
		f.mu.Lock()
		waiting := f.state == "waiting"
		f.mu.Unlock()
		if waiting {
			rt.mu.Unlock()
			<-f.ready
			return s.oauthStatus(ctx, id)
		}
	}
	fctx, cancel := context.WithTimeout(context.Background(), oauthFlowTimeout)
	f := &oauthFlow{state: "waiting", cancel: cancel, ready: make(chan struct{})}
	rt.flows[id] = f
	rt.mu.Unlock()
	by := s.c.Human()
	safe.Go("core.mcpServers.oauthBegin", func() { s.runOAuth(fctx, f, r, by) })
	select {
	case <-f.ready:
	case <-time.After(30 * time.Second):
	case <-ctx.Done():
	}
	return s.oauthStatus(ctx, id)
}

func (s *mcpServers) runOAuth(ctx context.Context, f *oauthFlow, r mcpRow, by string) {
	defer f.cancel()
	fail := func(err error) {
		f.mu.Lock()
		f.state, f.err, f.url = "failed", err.Error(), ""
		f.mu.Unlock()
		f.signal()
		s.c.publish(context.Background(), "mcp.server_oauth_failed", "", map[string]any{"id": r.ID, "by": by})
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fail(fmt.Errorf("could not listen for the sign-in to come back: %w", err))
		return
	}
	defer func() { _ = ln.Close() }()
	redirect := "http://" + ln.Addr().String() + "/callback"
	type cb struct{ code, state, iss, err string }
	got := make(chan cb, 1)
	// The answer the human's browser brings back must carry the state this flow's authorization address carries. Without it,
	// anything on this computer that reaches the callback port first (it is on loopback, and a port can be found) would use up
	// the flow's one answer, with an error, and the real one would be turned away.
	var expectState atomic.Pointer[string]
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, req *http.Request) {
		// only the browser the human opened, on this computer, to this address
		if req.Method != http.MethodGet || req.Host != ln.Addr().String() {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		q := req.URL.Query()
		if want := expectState.Load(); want == nil || *want == "" || q.Get("state") != *want {
			http.Error(w, "this is not the answer this sign-in is waiting for", http.StatusBadRequest)
			return
		}
		c := cb{code: q.Get("code"), state: q.Get("state"), iss: q.Get("iss")}
		if e := q.Get("error"); e != "" {
			c.err = e
			if d := q.Get("error_description"); d != "" {
				c.err += ": " + d
			}
		}
		select {
		case got <- c:
		default: // only the first answer counts
			http.Error(w, "this sign-in was already answered", http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		msg := "You are signed in. You can close this tab and go back to Drydock."
		if c.err != "" {
			msg = "The sign-in did not finish: " + html.EscapeString(c.err) + ". Go back to Drydock and try again."
		}
		fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>Drydock</title><p style=\"font:16px system-ui;margin:3rem auto;max-width:32rem\">%s</p>", msg)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	safe.Go("core.mcpServers.runOAuth", func() { _ = srv.Serve(ln) })
	defer func() {
		sctx, c := context.WithTimeout(context.Background(), 2*time.Second)
		defer c()
		_ = srv.Shutdown(sctx)
	}()

	hc := oauthHTTPClient(r.URL)
	allowLocal := false // a sign-in page over plain http: only a server on this computer
	if u, err := url.Parse(r.URL); err == nil {
		allowLocal = netguard.IsLoopbackHost(u.Hostname())
	}
	var cfg *oauth2.Config
	var tok *oauth2.Token
	var attempts int  // the SDK asks again after a failed attempt; a flow gets exactly one answer from the human
	var reason string // why the first attempt failed, kept for the message
	h, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
			RedirectURIs: []string{redirect}, ClientName: "Drydock", GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"},
		}},
		RedirectURL:         redirect,
		RequestRefreshToken: true,
		Client:              hc,
		NewTokenSource: func(ctx context.Context, c *oauth2.Config, t *oauth2.Token) (oauth2.TokenSource, error) {
			cfg, tok = c, t
			return c.TokenSource(ctx, t), nil
		},
		AuthorizationCodeFetcher: func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			u, err := url.Parse(args.URL)
			if err != nil || (u.Scheme != "https" && !(allowLocal && u.Scheme == "http")) {
				return nil, errors.New("the server gave a sign-in address that is not https")
			}
			st := u.Query().Get("state")
			expectState.Store(&st)
			attempts++
			if attempts > 1 {
				if reason != "" {
					return nil, errors.New(reason)
				}
				return nil, errors.New("the sign-in did not complete; start it again")
			}
			f.mu.Lock()
			f.url = args.URL
			f.mu.Unlock()
			f.signal()
			s.c.publish(context.Background(), "mcp.server_oauth_waiting", "", map[string]any{"id": r.ID, "by": by})
			select {
			case c := <-got:
				if c.err != "" {
					reason = "the provider refused the sign-in: " + c.err
					return nil, errors.New(reason)
				}
				return &auth.AuthorizationResult{Code: c.code, State: c.state, Iss: c.iss}, nil
			case <-ctx.Done():
				return nil, errors.New("the sign-in was not completed in time")
			}
		},
	})
	if err != nil {
		fail(err)
		return
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "drydock", Version: "1"}, nil)
	cs, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: r.URL, OAuthHandler: h, HTTPClient: hc, DisableStandaloneSSE: true}, nil)
	if err != nil {
		fail(fmt.Errorf("sign-in failed: %w", err))
		return
	}
	_ = cs.Close()
	if cfg == nil || tok == nil {
		// the server needed no sign-in after all (it answered without a token)
		fail(errors.New("the server did not ask for a sign-in"))
		return
	}
	saved := oauthSaved{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, TokenURL: cfg.Endpoint.TokenURL, AuthStyle: int(cfg.Endpoint.AuthStyle), Scopes: cfg.Scopes, Token: tok}
	if err := s.oauthSave(ctx, r.ID, saved); err != nil {
		fail(err)
		return
	}
	now := storage.Now()
	scope := strings.Join(cfg.Scopes, " ")
	if err := s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE mcp_library SET oauth_at=?, oauth_scope=?, updated_at=? WHERE id=?`, now, scope, now, r.ID)
		return err
	}); err != nil {
		fail(err)
		return
	}
	if gw := s.c.MCPGateway(); gw != nil {
		gw.StopUpstream(r.ID)
	}
	rt := s.orun()
	rt.mu.Lock()
	delete(rt.sources, r.ID)
	rt.mu.Unlock()
	// learn the tools now that the server will talk to us
	if _, err := s.discover(context.Background(), r.ID); err != nil {
		s.c.logf("mcp %s: signed in, but listing its tools failed: %v", r.ID, err)
	}
	f.mu.Lock()
	f.state, f.url = "done", ""
	f.mu.Unlock()
	f.signal()
	s.c.publish(context.Background(), "mcp.server_oauth_done", "", map[string]any{"id": r.ID, "by": by})
}

func (s *mcpServers) oauthSave(ctx context.Context, id string, st oauthSaved) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := s.c.secrets.Put(ctx, mcpVaultRef(id, "oauth"), string(b)); err != nil {
		return fmt.Errorf("could not keep the sign-in: %w", err)
	}
	s.teachOAuthSecrets(id, st)
	return nil
}

// teachOAuthSecrets makes the secret scanner know the tokens of a signed-in server, so a tool result can never echo them back
// into a conversation.
func (s *mcpServers) teachOAuthSecrets(id string, st oauthSaved) {
	if s.c.Scanner == nil || st.Token == nil {
		return
	}
	_ = s.c.Scanner.RegisterSecret("mcp-"+id+"-oauth-access", st.Token.AccessToken)
	if st.Token.RefreshToken != "" {
		_ = s.c.Scanner.RegisterSecret("mcp-"+id+"-oauth-refresh", st.Token.RefreshToken)
	}
	if st.ClientSecret != "" {
		_ = s.c.Scanner.RegisterSecret("mcp-"+id+"-oauth-client", st.ClientSecret)
	}
}

func (s *mcpServers) oauthCancel(id string) {
	rt := s.orun()
	rt.mu.Lock()
	f := rt.flows[id]
	rt.mu.Unlock()
	if f != nil {
		f.cancel()
	}
}

// oauthSignOut forgets the tokens. The server stays set up for OAuth, signed out.
func (s *mcpServers) oauthSignOut(ctx context.Context, id string) (OAuthStatus, error) {
	r, err := s.load(ctx, id)
	if err != nil {
		return OAuthStatus{}, err
	}
	s.oauthCancel(id)
	if s.c.secrets != nil {
		_ = s.c.secrets.Delete(ctx, mcpVaultRef(id, "oauth"))
	}
	now := storage.Now()
	if err := s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE mcp_library SET oauth_at=0, oauth_scope='', updated_at=? WHERE id=?`, now, id)
		return err
	}); err != nil {
		return OAuthStatus{}, err
	}
	rt := s.orun()
	rt.mu.Lock()
	delete(rt.sources, id)
	delete(rt.flows, id)
	rt.mu.Unlock()
	if gw := s.c.MCPGateway(); gw != nil {
		gw.StopUpstream(id)
	}
	s.c.publish(ctx, "mcp.server_oauth_signed_out", "", map[string]any{"id": id, "by": s.c.Human(), "wasSignedIn": r.OAuthAt > 0})
	return s.oauthStatus(ctx, id)
}

// ---- at call time ----

type oauthSource struct {
	mu   sync.Mutex
	ts   oauth2.TokenSource
	last string
	st   oauthSaved
}

func (s *mcpServers) oauthSourceFor(ctx context.Context, id string) (*oauthSource, error) {
	rt := s.orun()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if src := rt.sources[id]; src != nil {
		return src, nil
	}
	if s.c.secrets == nil {
		return nil, errNoVault
	}
	raw, err := s.c.secrets.Get(ctx, mcpVaultRef(id, "oauth"))
	if err != nil || raw == "" {
		return nil, fmt.Errorf("%s is not signed in: sign in from the Library page", id)
	}
	var st oauthSaved
	if err := json.Unmarshal([]byte(raw), &st); err != nil || st.Token == nil {
		return nil, fmt.Errorf("the stored sign-in for %s is unreadable: sign in again from the Library page", id)
	}
	r, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	cfg := &oauth2.Config{ClientID: st.ClientID, ClientSecret: st.ClientSecret, Scopes: st.Scopes,
		Endpoint: oauth2.Endpoint{TokenURL: st.TokenURL, AuthStyle: oauth2.AuthStyle(st.AuthStyle)}}
	hctx := context.WithValue(context.Background(), oauth2.HTTPClient, oauthHTTPClient(r.URL))
	src := &oauthSource{ts: cfg.TokenSource(hctx, st.Token), last: st.Token.AccessToken, st: st}
	rt.sources[id] = src
	return src, nil
}

// oauthAccessToken is the gateway's credential for a signed-in server: the current access token, refreshed when it is about to
// expire, with the new tokens kept in the vault. A refresh that the server refuses means the human has to sign in again.
func (s *mcpServers) oauthAccessToken(ctx context.Context, id string) (string, error) {
	src, err := s.oauthSourceFor(ctx, id)
	if err != nil {
		return "", err
	}
	src.mu.Lock()
	defer src.mu.Unlock()
	tok, err := src.ts.Token()
	if err != nil {
		rt := s.orun()
		rt.mu.Lock()
		delete(rt.sources, id)
		rt.mu.Unlock()
		_ = s.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
			_, e := tx.ExecContext(ctx, `UPDATE mcp_library SET oauth_at=0 WHERE id=?`, id)
			return e
		})
		s.c.publish(context.Background(), "mcp.server_oauth_expired", "", map[string]any{"id": id})
		return "", fmt.Errorf("the sign-in to %s has expired or was revoked: sign in again from the Library page", id)
	}
	if tok.AccessToken != src.last {
		src.last = tok.AccessToken
		st := src.st
		st.Token = tok
		src.st = st
		if err := s.oauthSave(ctx, id, st); err != nil {
			s.c.logf("mcp %s: could not keep the refreshed sign-in: %v", id, err)
		}
	}
	return tok.AccessToken, nil
}

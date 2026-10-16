package integrations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/ids"
	"golang.org/x/oauth2"
)

// OAuthApp is Drydock's registration with a vendor's OAuth server. A kind signs in only when its ClientID is set: the core
// reads it from the build or the environment, or the owner registers an app with the vendor and pastes its client id on the
// Integrations page (oauthapps.go); without one, the person pastes an API key or token.
//
//   - Linear: authorization code with PKCE (S256) and a loopback redirect. Linear accepts a token exchange without the client
//     secret for a PKCE sign-in, so a desktop app holds no secret. Access tokens expire and are refreshed with the rotating
//     refresh token (https://linear.app/developers/oauth-2-0-authentication).
//   - Sentry: the device authorization grant (RFC 8628): Drydock shows a code, the person approves it on sentry.io, Drydock
//     polls the token endpoint; no secret and no redirect. Access tokens last 30 days and are refreshed
//     (docs/api/auth.mdx, "Device Authorization Flow").
//   - GitHub: the device authorization grant of an OAuth app with "Device flow" enabled (a public client id, no secret), scopes
//     repo and read:org; the token does not expire unless the app opts in, and goes to the credential store like the others.
//     Without one GitHub uses the person's gh sign-in.
//   - Notion: its public OAuth needs a client secret at the token exchange, which a desktop app cannot keep, so Notion connects
//     with an internal integration secret only. Aikido has client credentials only.
type OAuthApp struct {
	ClientID     string   `json:"-"`
	AuthURL      string   `json:"-"` // authorization endpoint (code flow)
	TokenURL     string   `json:"-"`
	DeviceURL    string   `json:"-"` // device authorization endpoint (device flow)
	Scopes       []string `json:"-"`
	RedirectPort int      `json:"-"` // the loopback port of the code flow's redirect (0 = any free port)
}

// Method is "pkce" or "device".
func (a OAuthApp) Method() string {
	if a.DeviceURL != "" {
		return "device"
	}
	return "pkce"
}

func (a OAuthApp) config(redirect string) *oauth2.Config {
	return &oauth2.Config{ClientID: a.ClientID, Scopes: a.Scopes, RedirectURL: redirect,
		Endpoint: oauth2.Endpoint{AuthURL: a.AuthURL, TokenURL: a.TokenURL, DeviceAuthURL: a.DeviceURL, AuthStyle: oauth2.AuthStyleInParams}}
}

// DefaultOAuthApp returns a kind's OAuth endpoints and scopes (without a client id). baseURL is a self-hosted Sentry.
func DefaultOAuthApp(kind, baseURL string) (OAuthApp, bool) {
	switch kind {
	case KindLinear:
		// Linear's scopes are comma-separated in one parameter; the redirect port is fixed so the registered app can allow it
		return OAuthApp{AuthURL: "https://linear.app/oauth/authorize", TokenURL: "https://api.linear.app/oauth/token", Scopes: []string{"read,write"}, RedirectPort: linearRedirectPort}, true
	case KindGitHub:
		// GitHub answers the device endpoints as JSON when asked to (golang.org/x/oauth2 asks); scopes are space-separated
		return OAuthApp{DeviceURL: "https://github.com/login/device/code", TokenURL: "https://github.com/login/oauth/access_token", Scopes: []string{"repo", "read:org"}}, true
	case KindSentry:
		base := "https://sentry.io"
		if baseURL != "" {
			base = strings.TrimRight(baseURL, "/")
		}
		return OAuthApp{DeviceURL: base + "/oauth/device/code/", TokenURL: base + "/oauth/token/", Scopes: []string{"org:read", "project:read", "event:read", "event:write"}}, true
	}
	return OAuthApp{}, false
}

// oauthApp is the registration a kind signs in with (Options.OAuth over the defaults; the client id the owner pasted over the
// build's, and for Sentry its self-hosted URL when the caller names none), ok only with a client id.
func (s *Service) oauthApp(kind, baseURL string) (OAuthApp, bool) {
	set, pasted := s.oauthAppSetting(kind)
	if baseURL == "" && pasted {
		baseURL = set.BaseURL
	}
	def, ok := DefaultOAuthApp(kind, baseURL)
	if !ok {
		return OAuthApp{}, false
	}
	o := s.o.OAuth[kind]
	def.ClientID = o.ClientID
	if pasted {
		def.ClientID = set.ClientID
	}
	if def.ClientID == "" {
		return OAuthApp{}, false
	}
	if o.AuthURL != "" {
		def.AuthURL = o.AuthURL
	}
	if o.TokenURL != "" {
		def.TokenURL = o.TokenURL
	}
	if o.DeviceURL != "" {
		def.DeviceURL = o.DeviceURL
	}
	if len(o.Scopes) > 0 {
		def.Scopes = o.Scopes
	}
	if o.RedirectPort != 0 {
		def.RedirectPort = o.RedirectPort
	}
	return def, true
}

// OAuthAvailable reports whether a kind can sign in: an OAuth app is registered for it (the build's, or one the owner pasted).
func (s *Service) OAuthAvailable(kind string) bool { _, ok := s.oauthApp(kind, ""); return ok }

// OAuthSession is a sign-in in progress. Its token stays in memory until Connect stores it in the credential store.
type OAuthSession struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Method    string `json:"method"` // pkce | device
	State     string `json:"state"`  // waiting | signed_in | failed
	URL       string `json:"url"`    // the page the person opens to sign in
	UserCode  string `json:"userCode,omitempty"`
	Error     string `json:"error,omitempty"`
	ExpiresAt int64  `json:"expiresAt"`

	baseURL string
	token   *oauth2.Token
	cancel  context.CancelFunc
}

type oauthSessions struct {
	mu sync.Mutex
	m  map[string]*OAuthSession
}

const oauthWindow = 10 * time.Minute

// OAuthStart begins a sign-in. Only a human can start one.
func (s *Service) OAuthStart(ctx context.Context, by, kind, baseURL string) (OAuthSession, error) {
	if err := requireHuman(by); err != nil {
		return OAuthSession{}, err
	}
	if baseURL != "" {
		b, err := CheckAPIBase(baseURL)
		if err != nil {
			return OAuthSession{}, err
		}
		baseURL = b
	}
	app, ok := s.oauthApp(kind, baseURL)
	if !ok {
		if _, has := DefaultOAuthApp(kind, ""); has {
			return OAuthSession{}, errf(ErrOAuthUnavailable, "no %s OAuth app is registered: register one with %s and paste its client id on the Integrations page, or paste a %s instead", vendor(kind), vendor(kind), tokenNoun(kind))
		}
		return OAuthSession{}, errf(ErrOAuthUnavailable, "%s has no sign-in Drydock can use; paste a %s instead", vendor(kind), tokenNoun(kind))
	}
	sess := &OAuthSession{ID: ids.New("oas"), Kind: kind, Method: app.Method(), State: "waiting", baseURL: baseURL, ExpiresAt: s.o.Now().Add(oauthWindow).UnixMilli()}
	bg, cancel := context.WithTimeout(context.WithValue(context.Background(), oauth2.HTTPClient, s.hc), oauthWindow)
	sess.cancel = cancel
	fail := func(msg string) {
		s.oauth.mu.Lock()
		if sess.State == "waiting" {
			sess.State, sess.Error = "failed", s.scrub(msg)
		}
		s.oauth.mu.Unlock()
	}
	done := func(tok *oauth2.Token) {
		s.oauth.mu.Lock()
		sess.State, sess.token = "signed_in", tok
		s.oauth.mu.Unlock()
		if s.o.RegisterSecret != nil {
			s.o.RegisterSecret("oauth:"+sess.ID, tok.AccessToken)
		}
	}
	switch app.Method() {
	case "device":
		conf := app.config("")
		dctx, dcancel := context.WithTimeout(ctx, s.o.CallTimeout)
		resp, err := conf.DeviceAuth(context.WithValue(dctx, oauth2.HTTPClient, s.hc))
		dcancel()
		if err != nil {
			cancel()
			return OAuthSession{}, fmt.Errorf("%w: %s did not start the sign-in: %s", ErrBadRequest, vendor(kind), s.scrub(err.Error()))
		}
		sess.URL = firstNonEmpty(resp.VerificationURIComplete, resp.VerificationURI)
		sess.UserCode = resp.UserCode
		go func() {
			defer cancel()
			tok, err := conf.DeviceAccessToken(bg, resp)
			if err != nil {
				fail(vendor(kind) + " sign-in did not finish: " + err.Error())
				return
			}
			done(tok)
		}()
	default:
		// the fixed port first (the redirect URI the registered app allows); when it is taken, any free port, and the vendor
		// may then refuse the redirect: the person closes the program holding the port and signs in again
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", app.RedirectPort))
		if err != nil && app.RedirectPort != 0 {
			ln, err = net.Listen("tcp", "127.0.0.1:0")
		}
		if err != nil {
			cancel()
			return OAuthSession{}, fmt.Errorf("integrations: could not open the sign-in callback on this computer: %w", err)
		}
		redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
		conf := app.config(redirect)
		verifier := oauth2.GenerateVerifier()
		state := randomHex(16)
		sess.URL = conf.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
		srv := &http.Server{ReadHeaderTimeout: 10 * time.Second}
		srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}
			q := r.URL.Query()
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy", "default-src 'none'")
			switch {
			case q.Get("state") != state:
				http.Error(w, "This sign-in link does not belong to the sign-in Drydock started.", http.StatusBadRequest)
				return
			case q.Get("error") != "":
				fail(vendor(kind) + " sign-in was refused (" + clampText(q.Get("error"), 60) + ")")
				fmt.Fprintf(w, "<p>%s sign-in was refused. You can close this tab.</p>", html.EscapeString(vendor(kind)))
			default:
				tok, err := conf.Exchange(bg, q.Get("code"), oauth2.VerifierOption(verifier))
				if err != nil {
					fail(vendor(kind) + " did not issue a token: " + err.Error())
					fmt.Fprintf(w, "<p>%s did not finish the sign-in. Go back to Drydock.</p>", html.EscapeString(vendor(kind)))
				} else {
					done(tok)
					fmt.Fprintf(w, "<p>Signed in to %s. You can close this tab and return to Drydock.</p>", html.EscapeString(vendor(kind)))
				}
			}
			go func() { _ = srv.Close(); cancel() }()
		})
		go func() { _ = srv.Serve(ln) }()
		go func() {
			<-bg.Done()
			_ = srv.Close()
			fail(vendor(kind) + " sign-in was not finished within 10 minutes")
		}()
	}
	s.oauth.mu.Lock()
	for id, old := range s.oauth.m { // drop finished and stale sessions
		if old.State != "waiting" && s.o.Now().UnixMilli() > old.ExpiresAt {
			delete(s.oauth.m, id)
		}
	}
	s.oauth.m[sess.ID] = sess
	out := *sess
	s.oauth.mu.Unlock()
	return out, nil
}

// OAuthStatus returns a sign-in's state (never its token).
func (s *Service) OAuthStatus(id string) (OAuthSession, error) {
	s.oauth.mu.Lock()
	defer s.oauth.mu.Unlock()
	sess, ok := s.oauth.m[id]
	if !ok {
		return OAuthSession{}, fmt.Errorf("%w: sign-in %s", ErrNotFound, id)
	}
	out := *sess
	out.token, out.cancel = nil, nil
	return out, nil
}

// takeOAuth hands a finished sign-in's token to Connect (once).
func (s *Service) takeOAuth(id, kind string) (*oauth2.Token, string, error) {
	s.oauth.mu.Lock()
	defer s.oauth.mu.Unlock()
	sess, ok := s.oauth.m[id]
	if !ok || sess.Kind != kind {
		return nil, "", errf(ErrBadRequest, "no %s sign-in %s; sign in again", vendor(kind), id)
	}
	if sess.State != "signed_in" || sess.token == nil {
		return nil, "", errf(ErrBadRequest, "the %s sign-in is not finished", vendor(kind))
	}
	delete(s.oauth.m, id)
	return sess.token, sess.baseURL, nil
}

// closeOAuth stops every sign-in in progress (Service.Close).
func (s *Service) closeOAuth() {
	s.oauth.mu.Lock()
	defer s.oauth.mu.Unlock()
	for id, sess := range s.oauth.m {
		if sess.cancel != nil {
			sess.cancel()
		}
		delete(s.oauth.m, id)
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// tokenNoun is what a person pastes for a kind.
func tokenNoun(kind string) string {
	switch kind {
	case KindLinear:
		return "personal API key"
	case KindNotion:
		return "internal integration secret"
	case KindSentry:
		return "auth token"
	case KindAikido:
		return "REST API client ID and secret"
	case KindGitHub:
		return "gh sign-in"
	}
	return "token"
}

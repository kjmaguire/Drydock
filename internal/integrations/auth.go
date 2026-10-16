package integrations

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
	"golang.org/x/oauth2"
)

// How a credential is kept in the credential store (providers.SecretStore) under the connection's reference:
//
//   - a pasted API key or token: the bare token;
//   - an OAuth sign-in: JSON {"type":"oauth", access_token, refresh_token, token_type, expiry, scope}, refreshed (and stored
//     again) when it expires;
//   - Aikido's client credentials: JSON {"type":"client_credentials", client_id, client_secret}; the short-lived access token
//     they buy is kept in memory only.
//
// None of it is ever in a database row, an event, a log line or an error.
type storedCred struct {
	Type         string    `json:"type"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ClientID     string    `json:"client_id,omitempty"`
	ClientSecret string    `json:"client_secret,omitempty"`
}

// parseStored reads a structured credential; ok=false means the value is a bare token.
func parseStored(v string) (storedCred, bool) {
	if !strings.HasPrefix(strings.TrimSpace(v), `{"type":`) {
		return storedCred{}, false
	}
	var c storedCred
	if json.Unmarshal([]byte(v), &c) != nil || c.Type == "" {
		return storedCred{}, false
	}
	return c, true
}

// secretParts lists the values of a stored credential the secret scanner must learn (so they are redacted anywhere).
func secretParts(v string) []string {
	c, ok := parseStored(v)
	if !ok {
		return []string{v}
	}
	var out []string
	for _, s := range []string{c.AccessToken, c.RefreshToken, c.ClientSecret} {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// defaultAPIBase is each vendor's API host.
func defaultAPIBase(kind string) string {
	switch kind {
	case KindLinear:
		return "https://api.linear.app"
	case KindNotion:
		return "https://api.notion.com"
	case KindSentry:
		return "https://sentry.io"
	case KindAikido:
		return AikidoRegions["eu"]
	case KindGitHub:
		return "https://api.github.com" // only a Sign in with GitHub calls it; gh knows its own host
	}
	return ""
}

// apiBase is the connection's API base URL.
func apiBase(c Connection) string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return defaultAPIBase(c.Kind)
}

type cachedToken struct {
	token  string
	expiry time.Time
	scope  string
	// oauth is the whole OAuth sign-in when this entry holds one (an Aikido entry holds only the bought access token)
	oauth *storedCred
}

// authState holds the per-connection runtime state: the rate limiter, the access token in use (Aikido's bought token, or an
// OAuth sign-in as last read from or written to the credential store) and the lock a credential change takes.
type authState struct {
	mu       sync.Mutex
	limiters map[string]*limiter
	tokens   map[string]cachedToken
	// gen counts each connection's credential changes (forgetAuth): a token read before a change is not cached after it
	gen map[string]uint64
	// credLocks serialize the OAuth refresh of a connection and a change of its credential (Connect, Disconnect)
	credLocks map[string]chan struct{}
	// spent holds, per connection, a hash of the refresh token in the credential store that a refresh has already spent
	// although the renewed sign-in could not be written there
	spent map[string]string
}

// lockCredential takes the per-connection credential lock, or gives up when ctx ends.
func (s *Service) lockCredential(ctx context.Context, id string) (release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.auth.mu.Lock()
	ch := s.auth.credLocks[id]
	if ch == nil {
		ch = make(chan struct{}, 1)
		s.auth.credLocks[id] = ch
	}
	s.auth.mu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// authGen returns the connection's credential generation.
func (s *Service) authGen(id string) uint64 {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	return s.auth.gen[id]
}

// cachedOAuth returns the OAuth sign-in kept in memory for a connection.
func (s *Service) cachedOAuth(id string) (storedCred, bool) {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	ct, ok := s.auth.tokens[id]
	if !ok || ct.oauth == nil {
		return storedCred{}, false
	}
	return *ct.oauth, true
}

// cacheOAuth keeps an OAuth sign-in in memory, unless the connection's credential changed since gen was read.
func (s *Service) cacheOAuth(id string, gen uint64, sc storedCred) {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	if s.auth.gen[id] != gen {
		return
	}
	s.auth.tokens[id] = cachedToken{token: sc.AccessToken, expiry: sc.Expiry, scope: sc.Scope, oauth: &sc}
}

func refreshTokenHash(rt string) string {
	sum := sha256.Sum256([]byte(rt))
	return hex.EncodeToString(sum[:])
}

func (s *Service) limiterFor(c Connection) *limiter {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	key := c.ID + "/" + c.AuthKind
	l := s.auth.limiters[key]
	if l == nil {
		rl := RateLimitFor(c.Kind, c.AuthKind)
		if s.o.RateLimits != nil {
			if o, ok := s.o.RateLimits[c.Kind]; ok {
				rl = o
			}
		}
		l = newLimiter(rl)
		l.now = s.o.Now
		s.auth.limiters[key] = l
	}
	return l
}

// forgetAuth drops what is kept in memory for a connection whose credential changed or was removed. Connect and Disconnect
// call it while holding the connection's credential lock, after the credential store was changed.
func (s *Service) forgetAuth(id string) {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	s.auth.gen[id]++
	delete(s.auth.tokens, id)
	for k := range s.auth.limiters {
		if strings.HasPrefix(k, id+"/") {
			delete(s.auth.limiters, k)
		}
	}
}

// api builds the authenticated client of a connection.
func (s *Service) api(ctx context.Context, c Connection) (*API, *Failure) {
	if c.Kind == KindGitHub && !usesGH(c) {
		// a Sign in with GitHub: the same REST API gh api speaks, called directly with the sign-in's token
		auth, err := s.authorization(ctx, c, false)
		if err != nil {
			var f *Failure
			if errors.As(err, &f) {
				return nil, f
			}
			return nil, &Failure{Code: CodeUnauthorized, Cause: err, Message: "The GitHub sign-in cannot be read. Sign in with GitHub again on the Integrations page."}
		}
		return &API{Conn: c, GH: host.HTTPAPI{BaseURL: apiBase(c), Token: strings.TrimPrefix(auth, "Bearer "), Client: s.hc}}, nil
	}
	if c.Kind == KindGitHub {
		if s.o.GitHub == nil {
			return nil, &Failure{Code: CodeUnreachable, Message: "The GitHub CLI (gh) is not installed. Install it from https://cli.github.com and run `gh auth login`."}
		}
		gh, err := s.o.GitHub(ctx)
		if err != nil || gh == nil {
			return nil, &Failure{Code: CodeUnreachable, Cause: err, Message: "The GitHub CLI (gh) is not installed. Install it from https://cli.github.com and run `gh auth login`."}
		}
		return &API{Conn: c, GH: gh}, nil
	}
	if c.CredentialRef == "" {
		return nil, &Failure{Code: CodeUnauthorized, Cause: ErrNoCredential, Message: "No credential is stored for " + vendor(c.Kind) + ". Reconnect it on the Integrations page."}
	}
	base := apiBase(c)
	if base == "" {
		return nil, &Failure{Code: CodeOther, Message: "No API address is known for " + vendor(c.Kind) + "."}
	}
	hdr := http.Header{}
	if c.Kind == KindNotion {
		hdr.Set("Notion-Version", NotionVersion)
	}
	cl := &apiClient{kind: c.Kind, base: base, hc: s.hc, headers: hdr, lim: s.limiterFor(c), now: s.o.Now, maxBody: 16 << 20}
	cl.renew = c.Kind == KindAikido
	cl.auth = func(ctx context.Context, renew bool) (string, error) { return s.authorization(ctx, c, renew) }
	return &API{Conn: c, HTTP: cl}, nil
}

// authorization returns the Authorization header value for a connection.
func (s *Service) authorization(ctx context.Context, c Connection, renew bool) (string, error) {
	noCred := &Failure{Code: CodeUnauthorized, Cause: ErrNoCredential, Message: "No credential is stored for " + vendor(c.Kind) + ". Reconnect it on the Integrations page."}
	if strings.HasPrefix(c.CredentialRef, OAuthRefPrefix) {
		// a sign-in made before P42 through the vendor's hosted MCP server: its token is sent as is; whether the vendor's API
		// accepts it is up to the vendor (Test says so when it does not)
		if s.o.OAuthToken == nil {
			return "", noCred
		}
		tok, err := s.o.OAuthToken(ctx, c.CredentialRef)
		if err != nil || tok == "" {
			return "", &Failure{Code: CodeUnauthorized, Cause: err, Message: "The " + vendor(c.Kind) + " sign-in made through its MCP server has no usable token. Reconnect " + vendor(c.Kind) + "."}
		}
		return "Bearer " + tok, nil
	}
	if s.o.Vault == nil {
		return "", &Failure{Code: CodeUnauthorized, Cause: ErrCredentialsLocked, Message: "The credential store is not available, so the " + vendor(c.Kind) + " credential cannot be read."}
	}
	// an OAuth sign-in read or renewed earlier is kept in memory; the credential store holds the durable copy
	if sc, ok := s.cachedOAuth(c.ID); ok && s.oauthUsable(sc) {
		return "Bearer " + sc.AccessToken, nil
	}
	gen := s.authGen(c.ID)
	v, err := s.o.Vault.Get(ctx, c.CredentialRef)
	if err != nil || v == "" {
		return "", noCred
	}
	sc, structured := parseStored(v)
	switch {
	case structured && sc.Type == "oauth":
		tok, err := s.freshOAuth(ctx, c, sc, gen)
		if err != nil {
			return "", err
		}
		return "Bearer " + tok, nil
	case structured && sc.Type == "client_credentials":
		tok, err := s.clientCredentialsToken(ctx, c, sc, renew)
		if err != nil {
			return "", err
		}
		return "Bearer " + tok, nil
	case c.Kind == KindAikido:
		return "", &Failure{Code: CodeUnauthorized, Message: "This Aikido connection holds the MCP key of an earlier Drydock. Aikido's REST API needs a client ID and secret (Aikido: Integrations > Public REST API). Reconnect Aikido with them."}
	case c.Kind == KindLinear && c.AuthKind != AuthOAuth:
		return v, nil // Linear personal API keys go without "Bearer"
	}
	return "Bearer " + v, nil
}

// oauthUsable reports that a stored sign-in needs no refresh now.
func (s *Service) oauthUsable(sc storedCred) bool {
	return sc.Expiry.IsZero() || sc.Expiry.After(s.o.Now().Add(time.Minute)) || sc.RefreshToken == ""
}

// freshOAuth returns a usable access token, refreshing (and storing) it when it has expired. sc was read from the credential
// store at generation gen. Precondition: the credential store is available (authorization returns ErrCredentialsLocked
// before calling this when it is not).
//
// Refreshes of one connection are serialized with each other and with a change of its credential (Connect, Disconnect): a
// vendor that rotates refresh tokens (Linear) accepts each one once, so a second caller that spent the same refresh token
// would get invalid_grant and the connection would look signed out. A caller that waited uses the sign-in the first one kept
// in memory, or re-reads the credential store; if the credential there is gone or is no longer an OAuth sign-in (the person
// disconnected, or reconnected with an API key), it stops: nothing is refreshed or written back.
//
// A renewed sign-in is kept in memory and written to the credential store. When that write fails, the old refresh token is
// spent all the same: the callers of this process go on with the copy in memory, and a later caller that finds only the
// spent token in the store asks for a new sign-in instead of sending it. Only a refusal of the grant itself (invalid_grant,
// or the token endpoint answering 400 or 401) means the sign-in is gone; a network error, a timeout, a 5xx or a cancelled
// call is retried later like any other call.
func (s *Service) freshOAuth(ctx context.Context, c Connection, sc storedCred, gen uint64) (string, error) {
	if s.oauthUsable(sc) {
		s.cacheOAuth(c.ID, gen, sc)
		return sc.AccessToken, nil
	}
	app, ok := s.oauthApp(c.Kind, c.BaseURL)
	if !ok {
		return sc.AccessToken, nil // no registration to refresh with: try the token; a 401 asks for a new sign-in
	}
	release, err := s.lockCredential(ctx, c.ID)
	if err != nil {
		return "", Classify(c.Kind, err, "")
	}
	defer release()
	noCred := &Failure{Code: CodeUnauthorized, Cause: ErrNoCredential, Message: "No credential is stored for " + vendor(c.Kind) + ". Reconnect it on the Integrations page."}
	gen = s.authGen(c.ID) // the credential cannot change while the lock is held
	if cur, ok := s.cachedOAuth(c.ID); ok {
		sc = cur // renewed by the caller this one waited for (and perhaps not stored)
	} else {
		v, err := s.o.Vault.Get(ctx, c.CredentialRef)
		if err != nil || v == "" {
			return "", noCred
		}
		cur, ok := parseStored(v)
		if !ok || cur.Type != "oauth" {
			return "", noCred
		}
		sc = cur
	}
	if s.oauthUsable(sc) {
		s.cacheOAuth(c.ID, gen, sc)
		return sc.AccessToken, nil
	}
	s.auth.mu.Lock()
	spent := s.auth.spent[c.ID] != "" && s.auth.spent[c.ID] == refreshTokenHash(sc.RefreshToken)
	s.auth.mu.Unlock()
	if spent {
		return "", &Failure{Code: CodeUnauthorized, Cause: ErrNoCredential, Message: "Drydock renewed the " + vendor(c.Kind) + " sign-in but could not save it in the credential store, and the saved one can no longer be renewed. Reconnect " + vendor(c.Kind) + "."}
	}
	conf := app.config("")
	// expired by Drydock's clock: hand the library a token it also sees as expired, so it refreshes
	old := &oauth2.Token{AccessToken: sc.AccessToken, RefreshToken: sc.RefreshToken, TokenType: sc.TokenType, Expiry: time.Now().Add(-time.Minute)}
	tok, err := conf.TokenSource(context.WithValue(ctx, oauth2.HTTPClient, s.hc), old).Token()
	if err != nil {
		return "", s.refreshFailure(c.Kind, err)
	}
	nsc := oauthStored(tok, sc.Scope)
	nsc.Expiry = s.onOurClock(tok.Expiry)
	s.cacheOAuth(c.ID, gen, nsc)
	b, err := json.Marshal(nsc)
	if err != nil {
		return tok.AccessToken, nil
	}
	s.registerCredential(c.ID, string(b))
	// stored after every refresh: a vendor may rotate only the refresh token or the expiry
	if err := s.o.Vault.Put(ctx, c.CredentialRef, string(b)); err != nil {
		s.o.Logf("integrations: the renewed %s sign-in of connection %s could not be saved in the credential store; Drydock keeps using it until it quits: %s", vendor(c.Kind), c.ID, s.scrub(err.Error()))
		if nsc.RefreshToken != sc.RefreshToken {
			s.auth.mu.Lock()
			s.auth.spent[c.ID] = refreshTokenHash(sc.RefreshToken)
			s.auth.mu.Unlock()
		}
	} else {
		s.auth.mu.Lock()
		delete(s.auth.spent, c.ID)
		s.auth.mu.Unlock()
	}
	return tok.AccessToken, nil
}

// refreshFailure classifies a failed refresh grant. Only the token endpoint refusing the grant (invalid_grant, or HTTP 400 or
// 401) means the sign-in must be made again; anything else is retried with backoff.
func (s *Service) refreshFailure(kind string, err error) *Failure {
	v := vendor(kind)
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		status := 0
		if re.Response != nil {
			status = re.Response.StatusCode
		}
		switch {
		case re.ErrorCode == "invalid_grant" || status == http.StatusBadRequest || status == http.StatusUnauthorized:
			return &Failure{Code: CodeUnauthorized, Cause: err, Message: v + " did not renew the sign-in. Reconnect " + v + "."}
		case status == http.StatusTooManyRequests:
			return &Failure{Code: CodeRateLimited, Cause: ErrRateLimited, RetryAfter: retryAfterHeader(re.Response.Header.Get("Retry-After"), s.o.Now()), Message: v + " asked Drydock to slow down (rate limit) while renewing the sign-in. Drydock will wait and try again by itself."}
		}
		return &Failure{Code: CodeUnreachable, Cause: err, Message: fmt.Sprintf("%s's sign-in server answered HTTP %d when Drydock renewed the sign-in. Drydock will retry.", v, status)}
	}
	f := Classify(kind, err, "")
	if f.Code == CodeUnauthorized || f.Code == CodeForbidden {
		// the words of a transport error, not the vendor refusing the grant
		f = &Failure{Code: CodeUnreachable, Cause: err, Message: "Drydock could not reach " + v + " to renew the sign-in. Drydock will retry."}
	}
	return f
}

// onOurClock moves a token expiry the OAuth library computed on the wall clock to Drydock's clock (the same instant in the
// product; tests run Drydock on a fixed clock).
func (s *Service) onOurClock(exp time.Time) time.Time {
	if exp.IsZero() {
		return exp
	}
	return s.o.Now().Add(time.Until(exp))
}

func oauthStored(tok *oauth2.Token, fallbackScope string) storedCred {
	scope := fallbackScope
	if v, ok := tok.Extra("scope").(string); ok && v != "" {
		scope = v
	}
	return storedCred{Type: "oauth", AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, TokenType: tok.TokenType, Expiry: tok.Expiry, Scope: scope}
}

// clientCredentialsToken buys (or reuses) an Aikido access token: POST <host>/api/oauth/token, grant_type=client_credentials,
// HTTP Basic client id and secret.
func (s *Service) clientCredentialsToken(ctx context.Context, c Connection, sc storedCred, renew bool) (string, error) {
	s.auth.mu.Lock()
	ct, ok := s.auth.tokens[c.ID]
	s.auth.mu.Unlock()
	if ok && !renew && ct.expiry.After(s.o.Now().Add(30*time.Second)) {
		return ct.token, nil
	}
	req, err := http.NewRequestWithContext(ctx, "POST", apiBase(c)+"/api/oauth/token", strings.NewReader(url.Values{"grant_type": {"client_credentials"}}.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(sc.ClientID+":"+sc.ClientSecret)))
	resp, err := s.hc.Do(req)
	if err != nil {
		traceCall(ctx, "POST", "/api/oauth/token", 0)
		return "", Classify(c.Kind, err, "")
	}
	defer resp.Body.Close()
	traceCall(ctx, "POST", "/api/oauth/token", resp.StatusCode)
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusForbidden {
		return "", &Failure{Code: CodeUnauthorized, Message: fmt.Sprintf("Aikido did not accept the client ID and secret (HTTP %d%s). Check them, or create new ones under Integrations > Public REST API, and reconnect.", resp.StatusCode, codeSuffix(vendorCode(b)))}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", &Failure{Code: CodeRateLimited, Cause: ErrRateLimited, RetryAfter: retryAfterHeader(resp.Header.Get("Retry-After"), s.o.Now()), Message: "Aikido asked Drydock to slow down (rate limit). Drydock will wait and try again by itself."}
	}
	if resp.StatusCode/100 != 2 {
		return "", &Failure{Code: CodeUnreachable, Message: fmt.Sprintf("Aikido's token endpoint answered HTTP %d. Drydock will retry.", resp.StatusCode)}
	}
	var tr struct {
		AccessToken string          `json:"access_token"`
		ExpiresIn   json.Number     `json:"expires_in"`
		Scope       json.RawMessage `json:"scope"`
	}
	if err := json.Unmarshal(b, &tr); err != nil || tr.AccessToken == "" {
		return "", schemaDrift(KindAikido, errors.New("the token answer has no access_token"))
	}
	exp := time.Hour
	if n, err := tr.ExpiresIn.Int64(); err == nil && n > 0 {
		exp = time.Duration(n) * time.Second
	}
	scope := ""
	var ss string
	var sl []string
	if json.Unmarshal(tr.Scope, &ss) == nil {
		scope = ss
	} else if json.Unmarshal(tr.Scope, &sl) == nil {
		scope = strings.Join(sl, " ")
	}
	if s.o.RegisterSecret != nil {
		s.o.RegisterSecret("integration:"+c.ID+":access", tr.AccessToken)
	}
	s.auth.mu.Lock()
	s.auth.tokens[c.ID] = cachedToken{token: tr.AccessToken, expiry: s.o.Now().Add(exp), scope: scope}
	s.auth.mu.Unlock()
	return tr.AccessToken, nil
}

func codeSuffix(code string) string {
	if code == "" {
		return ""
	}
	return ", " + code
}

// credentialScopes returns the scopes a credential reports outside the identity call: an OAuth sign-in's granted scope and
// Aikido's token scope.
func (s *Service) credentialScopes(ctx context.Context, c Connection) []string {
	var scope string
	if c.Kind == KindAikido {
		s.auth.mu.Lock()
		scope = s.auth.tokens[c.ID].scope
		s.auth.mu.Unlock()
	} else if s.o.Vault != nil && c.CredentialRef != "" && !strings.HasPrefix(c.CredentialRef, OAuthRefPrefix) {
		if v, err := s.o.Vault.Get(ctx, c.CredentialRef); err == nil {
			if sc, ok := parseStored(v); ok && sc.Type == "oauth" {
				scope = sc.Scope
			}
		}
	}
	return strings.FieldsFunc(scope, func(r rune) bool { return r == ' ' || r == ',' })
}

// authMethod describes how a connection's credential was made, in words.
func authMethod(c Connection) string {
	switch {
	case usesGH(c):
		return "GitHub CLI (gh) sign-in"
	case strings.HasPrefix(c.CredentialRef, OAuthRefPrefix):
		return "OAuth sign-in through the vendor's MCP server (made before Drydock called the API directly)"
	case c.AuthKind == AuthOAuth:
		return "OAuth sign-in"
	case c.AuthKind == AuthClientCredentials:
		return "REST API client credentials"
	case c.Kind == KindLinear:
		return "Personal API key"
	case c.Kind == KindNotion:
		return "Internal integration secret"
	case c.Kind == KindSentry:
		return "Auth token"
	}
	return "API key"
}

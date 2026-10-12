package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
)

// TaskInput is what an importer hands to the task creator. Tasks always start in BACKLOG.
type TaskInput struct {
	ProjectID   string
	Title       string
	Description string
	Priority    int
	IssueRef    string
	Template    string
}

// TaskCreator creates a BACKLOG task and returns its id (core wires tasks.Service behind it).
type TaskCreator func(ctx context.Context, in TaskInput) (taskID string, err error)

// Options wires the Service.
type Options struct {
	DB      *storage.DB
	Bus     events.Bus
	Vault   providers.SecretStore
	Scanner security.SecretScanner
	// HTTPClient calls the vendors' APIs (default: proxy from the environment, CallTimeout per request).
	HTTPClient *http.Client
	// GitHub returns the user's gh-backed API (nil, or an error: gh is not installed). Drydock never holds a GitHub token.
	GitHub func(ctx context.Context) (host.API, error)
	// ProjectRepo returns a project's GitHub repository (owner/name, from its origin remote), the default of a GitHub
	// assignment.
	ProjectRepo func(ctx context.Context, projectID string) (string, error)
	// OAuth registers Drydock's OAuth apps per kind (a client id; endpoints default to the vendor's). Without a client id a
	// kind connects with a pasted key only.
	OAuth map[string]OAuthApp
	// RateLimits overrides the documented limits per kind (tests use generous ones; the product uses RateLimitFor).
	RateLimits map[string]RateLimit
	// Approvals gates every outbound write. Required for Propose/Decide/Execute.
	Approvals *security.Approvals
	// CreateTask creates BACKLOG tasks for imports and rules. Required for Import and rules.
	CreateTask TaskCreator
	// ProjectExists validates a Drydock project id (nil = accept any).
	ProjectExists func(ctx context.Context, projectID string) error
	// BodyDir is <app data>/integrations.
	BodyDir string
	// RegisterSecret/UnregisterSecret teach the app's scanner the exact credential values so they are redacted everywhere.
	RegisterSecret   func(name, value string)
	UnregisterSecret func(name string)
	// OAuthToken resolves a pre-P42 OAuth sign-in reference ("oauth/mcplib/<id>", a sign-in to the vendor's hosted MCP server)
	// to its current access token.
	OAuthToken func(ctx context.Context, ref string) (string, error)
	// OnCredentialChange tells whoever caches a vendor's credential (the MCP gateway) that it changed.
	OnCredentialChange func(kind string)
	Logf               func(format string, a ...any)
	Now                func() time.Time

	PageSize     int           // items per page (default 50)
	MaxPages     int           // pages per sync run before the sweep pauses and resumes next time (default 20)
	MaxBodyFetch int           // Notion page bodies fetched per sync run (default 25)
	Overlap      time.Duration // watermark overlap for clock skew (default 2 min)
	BaseBackoff  time.Duration // first failure backoff (default 30 s)
	MaxBackoff   time.Duration // backoff ceiling (default 1 h)
	CallTimeout  time.Duration // per API call (default 45 s)
	// Jitter returns a factor in [0.8, 1.2] applied to backoff (tests pin it to 1).
	Jitter func() float64
	// ThreatThreshold overrides RiskThreshold (tests only).
	ThreatThreshold int
}

// Service is the integrations facade.
type Service struct {
	o     Options
	st    *Store
	san   Sanitizer
	hc    *http.Client
	mu    sync.Mutex
	locks map[string]*sync.Mutex // per-connection sync locks and per-item import locks
	auth  authState
	oauth oauthSessions
}

// New builds a Service.
func New(o Options) (*Service, error) {
	if o.DB == nil {
		return nil, errors.New("integrations: Options.DB is required")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.PageSize <= 0 {
		o.PageSize = 50
	}
	if o.MaxPages <= 0 {
		o.MaxPages = 20
	}
	if o.MaxBodyFetch <= 0 {
		o.MaxBodyFetch = 25
	}
	if o.Overlap <= 0 {
		o.Overlap = 2 * time.Minute
	}
	if o.BaseBackoff <= 0 {
		o.BaseBackoff = 30 * time.Second
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = time.Hour
	}
	if o.CallTimeout <= 0 {
		o.CallTimeout = 45 * time.Second
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.Jitter == nil {
		o.Jitter = func() float64 { return 0.8 + 0.4*float64(time.Now().UnixNano()%1000)/1000 }
	}
	if o.ThreatThreshold <= 0 {
		o.ThreatThreshold = RiskThreshold
	}
	hc := o.HTTPClient
	if hc == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = http.ProxyFromEnvironment
		hc = &http.Client{Transport: tr, Timeout: o.CallTimeout,
			// a redirect may not carry the credential to another host
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 || req.URL.Host != via[0].URL.Host {
					return http.ErrUseLastResponse
				}
				return nil
			}}
	}
	s := &Service{o: o, hc: hc, locks: map[string]*sync.Mutex{}}
	s.auth.limiters, s.auth.tokens = map[string]*limiter{}, map[string]cachedToken{}
	s.auth.gen, s.auth.credLocks, s.auth.spent = map[string]uint64{}, map[string]chan struct{}{}, map[string]string{}
	s.oauth.m = map[string]*OAuthSession{}
	s.st = &Store{DB: o.DB, BodyDir: o.BodyDir, Now: func() int64 { return o.Now().UTC().UnixMilli() }}
	s.san = Sanitizer{Scanner: o.Scanner}
	return s, nil
}

// Close stops sign-ins in progress.
func (s *Service) Close() { s.closeOAuth() }

// Store exposes the persistence layer (tests and the core extension).
func (s *Service) Store() *Store { return s.st }

func (s *Service) nowMS() int64 { return s.o.Now().UTC().UnixMilli() }

func (s *Service) lock(key string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.locks[key]
	if m == nil {
		m = &sync.Mutex{}
		s.locks[key] = m
	}
	return m
}

// scrub removes secrets from text that is about to be stored or published.
func (s *Service) scrub(text string) string {
	if s.o.Scanner == nil {
		return text
	}
	return s.o.Scanner.Redact(text)
}

// emit publishes an event. Payloads carry ids, counts and hashes, never item text or secrets. A failure to publish never
// fails the operation: the database rows are the record.
func (s *Service) emit(ctx context.Context, typ, projectID, taskID string, data map[string]any) {
	if s.o.Bus == nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := s.o.Bus.Publish(pctx, events.Event{Type: typ, ProjectID: projectID, TaskID: taskID, Data: b}); err != nil {
		s.o.Logf("integrations: publish %s: %v", typ, err)
	}
}

func requireHuman(by string) error {
	if !security.IsHumanPrincipal(by) {
		return ErrNotHuman
	}
	return nil
}

// ---- connect / disconnect / list ----

// ConnectReq connects a service at the workspace level, or reconnects one (ID set: a new credential for the same connection,
// its assignments and items untouched). No project is needed: assigning the connection to projects is a separate step
// (Assign). The credential goes to the credential store and is never returned, stored in a row, logged or published.
type ConnectReq struct {
	ID   string `json:"id"` // reconnect this connection
	Kind string `json:"kind"`
	// Token is a pasted API key, integration secret or auth token (Linear, Notion, Sentry).
	Token string `json:"token"`
	// ClientID and ClientSecret are Aikido's REST API client credentials; Region picks Aikido's host (eu, us, me, au).
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Region       string `json:"region"`
	// BaseURL replaces the vendor's API host (a self-hosted Sentry, a local test server).
	BaseURL string `json:"baseUrl"`
	// OAuthSession connects with a finished sign-in (OAuthStart).
	OAuthSession string `json:"oauthSession"`
	// OAuthRef keeps a pre-P42 sign-in made through the vendor's hosted MCP server ("oauth/mcplib/<id>").
	OAuthRef      string `json:"oauthRef"`
	PollIntervalS int    `json:"pollIntervalS"`
	// ProjectID and Config assign the new connection to a project in the same call (a shortcut for Connect then Assign).
	ProjectID string            `json:"projectId"`
	Config    map[string]string `json:"config"`
	// Name is ignored: a connection is named after its vendor.
	Name string `json:"name"`
}

// OAuthRefPrefix marks a credential reference that resolves through an MCP OAuth sign-in (Options.OAuthToken).
const OAuthRefPrefix = "oauth/"

var oauthRefRe = regexp.MustCompile(`^oauth/mcplib/[A-Za-z0-9_.-]{1,80}$`)

var (
	tokenRe    = regexp.MustCompile(`^[\x21-\x7E]{8,}$`) // printable ASCII, no spaces or newlines: a token cannot inject a header
	clientIDRe = regexp.MustCompile(`^[\x21-\x7E]{1,200}$`)
	cfgKeys    = map[string][]string{
		KindLinear: {"team", "project"}, KindNotion: {"data_source", "query"}, KindSentry: {"organization", "project", "query"}, KindAikido: {"repo"}, KindGitHub: {"repo", "labels"},
	}
	// settings of connections made before P42 that no longer mean anything: dropped silently
	legacyCfgKeys = map[string]bool{"argv": true, "workspace": true}
	requiredCfg   = map[string][]string{KindSentry: {"organization"}}
)

func validateConfig(kind string, cfg map[string]string) (map[string]string, error) {
	out := map[string]string{}
	allowed := map[string]bool{}
	for _, k := range cfgKeys[kind] {
		allowed[k] = true
	}
	for k, v := range cfg {
		if legacyCfgKeys[k] && !allowed[k] {
			continue
		}
		if !allowed[k] {
			return nil, errf(ErrBadRequest, "%q is not a setting of %s (allowed: %s)", k, vendor(kind), strings.Join(cfgKeys[kind], ", "))
		}
		v = strings.TrimSpace(v)
		if len(v) > 200 {
			return nil, errf(ErrBadRequest, "setting %q is too long", k)
		}
		if cl, _ := stripUnsafe(v); cl != v {
			return nil, errf(ErrBadRequest, "setting %q contains control or invisible characters", k)
		}
		if kind == KindGitHub && k == "repo" && v != "" && !host.ValidFullName(v) {
			return nil, errf(ErrBadRequest, "the GitHub repository must be owner/name")
		}
		if v != "" {
			out[k] = v
		}
	}
	return out, nil
}

func checkRequired(kind string, cfg map[string]string) error {
	for _, k := range requiredCfg[kind] {
		if cfg[k] == "" {
			return errf(ErrBadRequest, "%s needs the %q setting for a project", vendor(kind), k)
		}
	}
	return nil
}

// Connect stores the credential in the credential store and the connection row. It does not call the vendor: the core runs
// Test right after, which reads the account the credential reaches.
func (s *Service) Connect(ctx context.Context, by string, r ConnectReq) (ConnectionView, error) {
	if err := requireHuman(by); err != nil {
		return ConnectionView{}, err
	}
	var existing *Connection
	if r.ID != "" {
		c, err := s.st.GetConnection(ctx, r.ID)
		if err != nil {
			return ConnectionView{}, err
		}
		existing = &c
		r.Kind = c.Kind
	}
	if _, ok := AdapterFor(r.Kind); !ok {
		return ConnectionView{}, errf(ErrBadRequest, "unknown integration %q (use linear, notion, sentry, aikido or github)", r.Kind)
	}
	if existing == nil {
		all, err := s.st.ListConnections(ctx)
		if err != nil {
			return ConnectionView{}, err
		}
		for _, c := range all {
			if c.Kind == r.Kind {
				return ConnectionView{}, fmt.Errorf("%w (%s)", ErrAlreadyConnected, vendor(r.Kind))
			}
		}
	}
	cfg, err := validateConfig(r.Kind, r.Config)
	if err != nil {
		return ConnectionView{}, err
	}
	if r.PollIntervalS < 0 || (r.PollIntervalS > 0 && r.PollIntervalS < 30) || r.PollIntervalS > 86400 {
		return ConnectionView{}, errf(ErrBadRequest, "the poll interval must be between 30 seconds and 24 hours")
	}
	// the API host
	base := ""
	if r.Region != "" {
		if r.Kind != KindAikido {
			return ConnectionView{}, errf(ErrBadRequest, "only Aikido has a region")
		}
		b, ok := AikidoRegions[strings.ToLower(r.Region)]
		if !ok {
			return ConnectionView{}, errf(ErrBadRequest, "Aikido's regions are eu, us, me and au")
		}
		base = b
	}
	if strings.TrimSpace(r.BaseURL) != "" {
		if r.Kind == KindGitHub {
			return ConnectionView{}, errf(ErrBadRequest, "GitHub goes through gh; it has no API URL here")
		}
		b, err := CheckAPIBase(r.BaseURL)
		if err != nil {
			return ConnectionView{}, err
		}
		base = b
	}
	if base == "" && existing != nil && r.Region == "" && r.BaseURL == "" {
		base = existing.BaseURL
	}

	// the credential
	var secret, authKind, oauthRef string
	switch {
	case r.Kind == KindGitHub:
		if r.Token != "" || r.ClientSecret != "" || r.OAuthSession != "" || r.OAuthRef != "" {
			return ConnectionView{}, errf(ErrBadRequest, "GitHub uses your gh sign-in; Drydock takes no GitHub token")
		}
		authKind = AuthGH
	case r.OAuthSession != "":
		if r.Token != "" || r.OAuthRef != "" {
			return ConnectionView{}, errf(ErrBadRequest, "connect with a token or with a sign-in, not both")
		}
		tok, sbase, err := s.takeOAuth(r.OAuthSession, r.Kind)
		if err != nil {
			return ConnectionView{}, err
		}
		sc := oauthStored(tok, "")
		sc.Expiry = s.onOurClock(tok.Expiry)
		b, _ := json.Marshal(sc)
		secret, authKind = string(b), AuthOAuth
		if sbase != "" {
			base = sbase
		}
	case r.OAuthRef != "":
		if !oauthRefRe.MatchString(r.OAuthRef) || r.Kind == KindAikido {
			return ConnectionView{}, errf(ErrBadRequest, "not an OAuth sign-in reference")
		}
		if r.Token != "" {
			return ConnectionView{}, errf(ErrBadRequest, "connect with a token or with a sign-in, not both")
		}
		if s.o.OAuthToken == nil {
			return ConnectionView{}, errf(ErrBadRequest, "this sign-in is not available in this build")
		}
		if _, err := s.o.OAuthToken(ctx, r.OAuthRef); err != nil {
			return ConnectionView{}, errf(ErrBadRequest, "sign in first: %v", err)
		}
		authKind, oauthRef = AuthOAuth, r.OAuthRef
	case r.Kind == KindAikido:
		if r.Token != "" {
			return ConnectionView{}, errf(ErrBadRequest, "Aikido's API takes a client ID and secret (Integrations > Public REST API), not a single token")
		}
		if r.ClientID != "" || r.ClientSecret != "" {
			if !clientIDRe.MatchString(r.ClientID) || len(r.ClientSecret) > 4096 || !tokenRe.MatchString(r.ClientSecret) {
				return ConnectionView{}, errf(ErrBadRequest, "the client ID and secret must be printable characters without spaces or line breaks")
			}
			b, _ := json.Marshal(storedCred{Type: "client_credentials", ClientID: r.ClientID, ClientSecret: r.ClientSecret})
			secret, authKind = string(b), AuthClientCredentials
		}
	default:
		if r.ClientID != "" || r.ClientSecret != "" {
			return ConnectionView{}, errf(ErrBadRequest, "%s takes a %s, not a client ID and secret", vendor(r.Kind), tokenNoun(r.Kind))
		}
		if r.Token != "" {
			if len(r.Token) > 4096 || !tokenRe.MatchString(r.Token) {
				return ConnectionView{}, errf(ErrBadRequest, "the token must be 8 to 4096 printable characters without spaces or line breaks")
			}
			secret, authKind = r.Token, AuthAPIKey
		}
	}
	if existing == nil && authKind == "" {
		return ConnectionView{}, errf(ErrBadRequest, "paste the %s %s", vendor(r.Kind), tokenNoun(r.Kind))
	}
	if r.ProjectID != "" && s.o.ProjectExists != nil {
		if err := s.o.ProjectExists(ctx, r.ProjectID); err != nil {
			return ConnectionView{}, err
		}
	}

	var saved Connection
	if existing == nil {
		c := Connection{Kind: r.Kind, AuthKind: authKind, BaseURL: base, PollIntervalS: r.PollIntervalS, Enabled: true, Status: StatusUntested, Config: map[string]string{}}
		if oauthRef != "" {
			c.CredentialRef = oauthRef
		}
		if saved, err = s.st.InsertConnection(ctx, c); err != nil {
			return ConnectionView{}, err
		}
		if saved.CredentialRef == "" && authKind != AuthGH {
			saved.CredentialRef = "integrations/" + saved.ID + "/token"
			if saved, err = s.st.UpdateConnection(ctx, saved, saved.Version); err != nil {
				return ConnectionView{}, err
			}
		}
	} else {
		c := *existing
		c.BaseURL, c.Status, c.StatusDetail, c.Enabled = base, StatusUntested, "", true
		if r.PollIntervalS != 0 {
			c.PollIntervalS = r.PollIntervalS
		}
		if authKind != "" {
			c.AuthKind = authKind
		}
		switch {
		case oauthRef != "":
			c.CredentialRef = oauthRef
		case secret != "" && (c.CredentialRef == "" || strings.HasPrefix(c.CredentialRef, OAuthRefPrefix)):
			c.CredentialRef = "integrations/" + c.ID + "/token"
		}
		if saved, err = s.st.UpdateConnection(ctx, c, existing.Version); err != nil {
			return ConnectionView{}, err
		}
	}
	if secret != "" && s.o.Vault == nil {
		if existing == nil {
			_ = s.st.DeleteConnection(ctx, saved.ID)
		}
		return ConnectionView{}, ErrCredentialsLocked
	}
	if secret != "" || oauthRef != "" || base != "" {
		// a refresh of the old OAuth sign-in that is in flight finishes first, so it cannot write the renewed sign-in over the
		// new credential, or keep it in memory after the change
		release, err := s.lockCredential(ctx, saved.ID)
		if err != nil {
			if existing == nil {
				_ = s.st.DeleteConnection(ctx, saved.ID)
			}
			return ConnectionView{}, err
		}
		if secret != "" {
			if err := s.o.Vault.Put(ctx, saved.CredentialRef, secret); err != nil {
				release()
				if existing == nil {
					_ = s.st.DeleteConnection(ctx, saved.ID)
				}
				return ConnectionView{}, fmt.Errorf("integrations: could not store the credential in the credential store: %w", err)
			}
			s.registerCredential(saved.ID, secret)
		}
		s.forgetAuth(saved.ID)
		release()
		if s.o.OnCredentialChange != nil {
			s.o.OnCredentialChange(saved.Kind)
		}
	}
	// a new credential restarts the failure bookkeeping and un-pauses the sync of every assignment
	if asgs, err := s.st.ListAssignments(ctx, saved.ID); err == nil {
		for _, a := range asgs {
			if cur, err := s.st.GetCursor(ctx, saved.ID, a.ID); err == nil && (cur.ConsecutiveFailures > 0 || cur.NextAttemptAt > 0) {
				cur.ConsecutiveFailures, cur.NextAttemptAt, cur.LastError, cur.LastErrorCode = 0, 0, "", ""
				_ = s.st.PutCursor(ctx, cur)
			}
		}
	}
	s.emit(ctx, EventConnected, "", "", map[string]any{"integrationId": saved.ID, "kind": saved.Kind, "by": by, "authKind": saved.AuthKind, "reconnect": existing != nil})
	if r.ProjectID != "" {
		if _, err := s.Assign(ctx, by, AssignReq{IntegrationID: saved.ID, ProjectID: r.ProjectID, Config: cfg}); err != nil && !errors.Is(err, ErrConflict) {
			return ConnectionView{}, err
		}
	}
	return s.Get(ctx, saved.ID)
}

// registerCredential teaches the scanner every secret part of a stored credential.
func (s *Service) registerCredential(id, secret string) {
	if s.o.RegisterSecret == nil {
		return
	}
	for i, p := range secretParts(secret) {
		name := "integration:" + id
		if i > 0 {
			name = fmt.Sprintf("integration:%s:%d", id, i)
		}
		s.o.RegisterSecret(name, p)
	}
}

// RegisterStoredSecrets teaches the scanner the credentials already stored (at startup).
func (s *Service) RegisterStoredSecrets(ctx context.Context) {
	if s.o.Vault == nil {
		return
	}
	conns, err := s.st.ListConnections(ctx)
	if err != nil {
		return
	}
	for _, c := range conns {
		if c.CredentialRef == "" || strings.HasPrefix(c.CredentialRef, OAuthRefPrefix) {
			continue
		}
		if v, err := s.o.Vault.Get(ctx, c.CredentialRef); err == nil && v != "" {
			s.registerCredential(c.ID, v)
		}
	}
}

// removeConnection deletes the connection's credential and row while holding its credential lock: a refresh of an OAuth
// sign-in that is in flight finishes first, so it cannot write the deleted credential back or keep it in memory.
func (s *Service) removeConnection(ctx context.Context, c Connection) error {
	id := c.ID
	release, err := s.lockCredential(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	if s.o.Vault != nil && c.CredentialRef != "" && !strings.HasPrefix(c.CredentialRef, OAuthRefPrefix) {
		if err := s.o.Vault.Delete(ctx, c.CredentialRef); err != nil {
			return fmt.Errorf("integrations: could not remove the credential from the credential store: %w", err)
		}
	}
	if s.o.UnregisterSecret != nil {
		s.o.UnregisterSecret("integration:" + id)
		for i := 1; i < 4; i++ {
			s.o.UnregisterSecret(fmt.Sprintf("integration:%s:%d", id, i))
		}
		s.o.UnregisterSecret("integration:" + id + ":access")
	}
	if err := s.st.DeleteConnection(ctx, id); err != nil {
		return err
	}
	s.forgetAuth(id)
	return nil
}

// Disconnect removes the connection, its credential, its assignments and everything not tied to a task.
func (s *Service) Disconnect(ctx context.Context, by, id string) error {
	if err := requireHuman(by); err != nil {
		return err
	}
	c, err := s.st.GetConnection(ctx, id)
	if err != nil {
		return err
	}
	if err := s.removeConnection(ctx, c); err != nil {
		return err
	}
	if s.o.OnCredentialChange != nil {
		s.o.OnCredentialChange(c.Kind)
	}
	s.emit(ctx, EventDisconnected, "", "", map[string]any{"integrationId": id, "kind": c.Kind, "by": by})
	return nil
}

// List returns every connection with its assignments and sync status.
func (s *Service) List(ctx context.Context) ([]ConnectionView, error) {
	cs, err := s.st.ListConnections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ConnectionView, 0, len(cs))
	for _, c := range cs {
		v, err := s.view(ctx, c)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Get returns one connection view.
func (s *Service) Get(ctx context.Context, id string) (ConnectionView, error) {
	c, err := s.st.GetConnection(ctx, id)
	if err != nil {
		return ConnectionView{}, err
	}
	return s.view(ctx, c)
}

// syncStatus derives the shown sync state of one cursor.
func (s *Service) syncStatus(c Connection, cur Cursor, items int) SyncStatus {
	st := SyncStatus{LastSuccessAt: cur.LastSuccessAt, LastAttemptAt: cur.LastAttemptAt, LastError: cur.LastError, LastErrorCode: cur.LastErrorCode, ItemsSeen: items,
		ItemsLastSync: cur.ItemsLastSync, ConsecutiveFailures: cur.ConsecutiveFailures, NextAttemptAt: cur.NextAttemptAt, Watermark: cur.Watermark, Resuming: cur.After != ""}
	switch {
	case !c.Enabled:
		st.State = "paused"
	case cur.LastAttemptAt == 0:
		st.State = "never_synced"
	case cur.LastErrorCode == CodeUnauthorized:
		st.State = "unauthenticated"
	case cur.LastErrorCode == CodeForbidden:
		st.State = "forbidden" // this assignment's scope is refused; the connection itself works
	case cur.LastErrorCode == CodeRateLimited && cur.NextAttemptAt > s.nowMS():
		st.State = "rate_limited"
	case cur.ConsecutiveFailures > 0 && cur.NextAttemptAt > s.nowMS():
		st.State = "backing_off"
	case cur.ConsecutiveFailures > 0:
		st.State = "error"
	default:
		st.State = "ok"
	}
	return st
}

var syncRank = map[string]int{"ok": 0, "never_synced": 1, "paused": 2, "backing_off": 3, "rate_limited": 4, "error": 5, "forbidden": 6, "unauthenticated": 7}

func (s *Service) view(ctx context.Context, c Connection) (ConnectionView, error) {
	v := ConnectionView{Connection: c, Assignments: []AssignmentView{}}
	v.HasCredential = s.hasCredential(ctx, c)
	asgs, err := s.st.ListAssignments(ctx, c.ID)
	if err != nil {
		return v, err
	}
	total, err := s.st.CountItems(ctx, c.ID)
	if err != nil {
		return v, err
	}
	v.Sync = SyncStatus{State: "never_synced", ItemsSeen: total}
	if len(asgs) == 0 && !c.LegacyAdopted {
		v.LegacyItems = total
	}
	for i, a := range asgs {
		cur, err := s.st.GetCursor(ctx, c.ID, a.ID)
		if err != nil {
			return v, err
		}
		n, err := s.st.CountAssignmentItems(ctx, a.ID)
		if err != nil {
			return v, err
		}
		st := s.syncStatus(c, cur, n)
		if !a.Enabled {
			st.State = "paused"
		}
		v.Assignments = append(v.Assignments, AssignmentView{Assignment: a, Sync: st})
		// the connection shows its worst assignment, with the newest success and attempt
		agg := v.Sync
		if i == 0 || syncRank[st.State] > syncRank[agg.State] {
			agg = st
			agg.ItemsSeen = total
		}
		if st.LastSuccessAt > agg.LastSuccessAt {
			agg.LastSuccessAt = st.LastSuccessAt
		}
		if st.LastAttemptAt > agg.LastAttemptAt {
			agg.LastAttemptAt = st.LastAttemptAt
		}
		v.Sync = agg
	}
	if !c.Enabled {
		v.Sync.State = "paused"
	}
	if ad, ok := AdapterFor(c.Kind); ok {
		v.Tools.SyncTools = ad.Endpoints()
		v.Tools.Ops = ad.Ops()
		if v.Tools.Ops == nil {
			v.Tools.Ops = []OpInfo{}
		}
	}
	return v, nil
}

// pollInterval is the effective interval of a connection.
func pollInterval(c Connection) time.Duration {
	if c.PollIntervalS > 0 {
		return time.Duration(c.PollIntervalS) * time.Second
	}
	switch c.Kind {
	case KindLinear:
		return 120 * time.Second
	case KindAikido:
		return time.Hour
	case KindGitHub:
		return 600 * time.Second
	}
	return 300 * time.Second
}

// validHTTPURL accepts only absolute http(s) URLs of reasonable length without control characters.
func validHTTPURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 500 {
		return ""
	}
	if cl, _ := stripUnsafe(raw); cl != raw || strings.ContainsAny(raw, " \t\n<>\"") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return raw
}

// hasCredential reports whether the connection's credential can be read now (GitHub: always, its sign-in is gh's).
func (s *Service) hasCredential(ctx context.Context, c Connection) bool {
	if c.Kind == KindGitHub {
		return true
	}
	if c.CredentialRef == "" {
		return false
	}
	if strings.HasPrefix(c.CredentialRef, OAuthRefPrefix) {
		if s.o.OAuthToken == nil {
			return false
		}
		_, err := s.o.OAuthToken(ctx, c.CredentialRef)
		return err == nil
	}
	if s.o.Vault == nil {
		return false
	}
	_, err := s.o.Vault.Get(ctx, c.CredentialRef)
	return err == nil
}

package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/mcp"
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
	Callers CallerFactory
	Scanner security.SecretScanner
	// Approvals gates every outbound write. Required for Propose/Decide/Execute.
	Approvals *security.Approvals
	// CreateTask creates BACKLOG tasks for imports and rules. Required for Import and rules.
	CreateTask TaskCreator
	// ProjectExists validates a Drydock project id (nil = accept any).
	ProjectExists func(ctx context.Context, projectID string) error
	// BodyDir is <app data>/integrations.
	BodyDir string
	// RegisterSecret/UnregisterSecret teach the app's scanner the exact token values so they are redacted everywhere.
	RegisterSecret func(name, value string)
	// OAuthToken resolves an OAuthRefPrefix credential reference to a current access token (core: the MCP library sign-in).
	OAuthToken       func(ctx context.Context, ref string) (string, error)
	UnregisterSecret func(name string)
	// OnCredentialChange tells the MCP gateway to drop a pooled upstream so the next call uses the new credential.
	OnCredentialChange func(serverID string)
	Logf               func(format string, a ...any)
	Now                func() time.Time

	PageSize     int           // items per page (default 50)
	MaxPages     int           // pages per sync run before the sweep pauses and resumes next time (default 20)
	MaxBodyFetch int           // Notion page bodies fetched per sync run (default 25)
	Overlap      time.Duration // watermark overlap for clock skew (default 2 min)
	BaseBackoff  time.Duration // first failure backoff (default 30 s)
	MaxBackoff   time.Duration // backoff ceiling (default 1 h)
	CallTimeout  time.Duration // per MCP call (default 45 s)
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
	mu    sync.Mutex
	locks map[string]*sync.Mutex // per-connection sync locks and per-item import locks
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
	s := &Service{o: o, locks: map[string]*sync.Mutex{}}
	s.st = &Store{DB: o.DB, BodyDir: o.BodyDir, Now: func() int64 { return o.Now().UTC().UnixMilli() }}
	s.san = Sanitizer{Scanner: o.Scanner}
	return s, nil
}

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

// ConnectReq creates or updates a connection. Token is the API key or access token the user pasted; it goes to the Vault and is
// never returned, stored in a row, logged or published.
type ConnectReq struct {
	ID            string            `json:"id"` // set to update an existing connection (new token, new scope)
	Kind          string            `json:"kind"`
	Name          string            `json:"name"`
	ProjectID     string            `json:"projectId"`
	Token         string            `json:"token"`
	BaseURL       string            `json:"baseUrl"`
	Config        map[string]string `json:"config"`
	PollIntervalS int               `json:"pollIntervalS"`
	// OAuthRef connects with an OAuth sign-in instead of a pasted token: "oauth/mcplib/<library server id>", the MCP library
	// server the human signed in to (the vendor's hosted MCP server). Drydock keeps and refreshes those tokens there.
	OAuthRef string `json:"oauthRef"`
}

// OAuthRefPrefix marks a credential reference that resolves through an OAuth sign-in (Options.OAuthToken).
const OAuthRefPrefix = "oauth/"

var oauthRefRe = regexp.MustCompile(`^oauth/mcplib/[A-Za-z0-9_.-]{1,80}$`)

var (
	tokenRe = regexp.MustCompile(`^[\x21-\x7E]{8,}$`) // printable ASCII, no spaces or newlines: a token cannot inject a header
	cfgKeys = map[string][]string{
		KindLinear: {"team"}, KindNotion: {"query"}, KindSentry: {"organization", "project", "query"}, KindAikido: {"workspace", "repo", "argv"},
	}
)

func validateConfig(kind string, cfg map[string]string) (map[string]string, error) {
	out := map[string]string{}
	allowed := map[string]bool{}
	for _, k := range cfgKeys[kind] {
		allowed[k] = true
	}
	for k, v := range cfg {
		if !allowed[k] {
			return nil, errf(ErrBadRequest, "%q is not a setting of %s (allowed: %s)", k, vendor(kind), strings.Join(cfgKeys[kind], ", "))
		}
		limit := 200
		if k == "argv" {
			limit = 1000
		}
		if len(v) > limit {
			return nil, errf(ErrBadRequest, "setting %q is too long", k)
		}
		if cl, _ := stripUnsafe(v); cl != v {
			return nil, errf(ErrBadRequest, "setting %q contains control or invisible characters", k)
		}
		if v != "" {
			out[k] = v
		}
	}
	return out, nil
}

// Connect stores the token in the Vault and the connection row. It does not call the vendor: run Test afterwards.
func (s *Service) Connect(ctx context.Context, by string, r ConnectReq) (ConnectionView, error) {
	if err := requireHuman(by); err != nil {
		return ConnectionView{}, err
	}
	if s.o.Vault == nil {
		return ConnectionView{}, ErrCredentialsLocked
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
		return ConnectionView{}, errf(ErrBadRequest, "unknown integration %q (use linear, notion, sentry or aikido)", r.Kind)
	}
	cfg, err := validateConfig(r.Kind, r.Config)
	if err != nil {
		return ConnectionView{}, err
	}
	if r.Token != "" && (len(r.Token) > 4096 || !tokenRe.MatchString(r.Token)) {
		return ConnectionView{}, errf(ErrBadRequest, "the token must be 8 to 4096 printable characters without spaces or line breaks")
	}
	if r.OAuthRef != "" {
		if !oauthRefRe.MatchString(r.OAuthRef) {
			return ConnectionView{}, errf(ErrBadRequest, "not an OAuth sign-in reference")
		}
		if r.Token != "" {
			return ConnectionView{}, errf(ErrBadRequest, "connect with a token or with an OAuth sign-in, not both")
		}
		if r.Kind == KindAikido {
			return ConnectionView{}, errf(ErrBadRequest, "Aikido has no hosted server to sign in to; paste its API key")
		}
		if s.o.OAuthToken == nil {
			return ConnectionView{}, errf(ErrBadRequest, "OAuth sign-in is not available in this build")
		}
		if _, err := s.o.OAuthToken(ctx, r.OAuthRef); err != nil {
			return ConnectionView{}, errf(ErrBadRequest, "sign in first: %v", err)
		}
	}
	if existing == nil && r.Token == "" && r.OAuthRef == "" {
		return ConnectionView{}, errf(ErrBadRequest, "paste the %s API key or access token, or sign in with OAuth", vendor(r.Kind))
	}
	if r.ProjectID != "" && s.o.ProjectExists != nil {
		if err := s.o.ProjectExists(ctx, r.ProjectID); err != nil {
			return ConnectionView{}, err
		}
	}
	if r.PollIntervalS < 0 || (r.PollIntervalS > 0 && r.PollIntervalS < 30) || r.PollIntervalS > 86400 {
		return ConnectionView{}, errf(ErrBadRequest, "the poll interval must be between 30 seconds and 24 hours")
	}
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = vendor(r.Kind)
	}
	if len(name) > 80 {
		return ConnectionView{}, errf(ErrBadRequest, "the name is too long")
	}
	c := Connection{Kind: r.Kind, Name: name, ProjectID: r.ProjectID, AuthKind: "api_key", BaseURL: r.BaseURL, Config: cfg, PollIntervalS: r.PollIntervalS, Enabled: true, Status: StatusUntested}
	if r.OAuthRef != "" {
		c.AuthKind, c.CredentialRef = "oauth", r.OAuthRef
	}
	if existing != nil {
		c = *existing
		c.Name, c.ProjectID, c.BaseURL, c.Config, c.PollIntervalS = name, r.ProjectID, r.BaseURL, cfg, r.PollIntervalS
		c.Status, c.StatusDetail, c.Enabled = StatusUntested, "", true
		switch {
		case r.OAuthRef != "":
			c.AuthKind, c.CredentialRef = "oauth", r.OAuthRef
		case r.Token != "" && strings.HasPrefix(c.CredentialRef, OAuthRefPrefix):
			c.AuthKind, c.CredentialRef = "api_key", "integrations/"+c.ID+"/token" // back from OAuth to a pasted token
		}
	}
	// validate the server spec (URL scheme, argv, command) before anything is stored
	if cat, err := mcp.LoadBuiltin(); err == nil {
		probe := c
		probe.CredentialRef = "integrations/validate"
		spec, serr := ServerSpecFor(cat, probe, false)
		if serr == nil {
			serr = spec.Validate(false)
		}
		if serr != nil {
			return ConnectionView{}, errf(ErrBadRequest, "%v", serr)
		}
	}
	var saved Connection
	if existing == nil {
		saved, err = s.st.InsertConnection(ctx, c)
		if err != nil {
			return ConnectionView{}, err
		}
		if saved.CredentialRef == "" {
			saved.CredentialRef = "integrations/" + saved.ID + "/token"
			saved, err = s.st.UpdateConnection(ctx, saved, saved.Version)
			if err != nil {
				return ConnectionView{}, err
			}
		}
	} else {
		if saved, err = s.st.UpdateConnection(ctx, c, existing.Version); err != nil {
			return ConnectionView{}, err
		}
	}
	if r.Token != "" {
		if err := s.o.Vault.Put(ctx, saved.CredentialRef, r.Token); err != nil {
			if existing == nil {
				_ = s.st.DeleteConnection(ctx, saved.ID)
			}
			return ConnectionView{}, fmt.Errorf("integrations: could not store the token in the credential store: %w", err)
		}
		if s.o.RegisterSecret != nil {
			s.o.RegisterSecret("integration:"+saved.ID, r.Token)
		}
		if s.o.OnCredentialChange != nil {
			s.o.OnCredentialChange(saved.Kind)
		}
	}
	// a changed token or scope restarts the failure bookkeeping and un-pauses the sync
	if cur, err := s.st.GetCursor(ctx, saved.ID, defaultScope); err == nil && (cur.ConsecutiveFailures > 0 || cur.NextAttemptAt > 0) {
		cur.ConsecutiveFailures, cur.NextAttemptAt, cur.LastError, cur.LastErrorCode = 0, 0, "", ""
		_ = s.st.PutCursor(ctx, cur)
	}
	s.emit(ctx, EventConnected, saved.ProjectID, "", map[string]any{"integrationId": saved.ID, "kind": saved.Kind, "by": by, "tokenReplaced": r.Token != ""})
	return s.view(ctx, saved)
}

// Disconnect removes the connection, its token and everything not tied to a task.
func (s *Service) Disconnect(ctx context.Context, by, id string) error {
	if err := requireHuman(by); err != nil {
		return err
	}
	c, err := s.st.GetConnection(ctx, id)
	if err != nil {
		return err
	}
	if s.o.Vault != nil && c.CredentialRef != "" && !strings.HasPrefix(c.CredentialRef, OAuthRefPrefix) {
		if err := s.o.Vault.Delete(ctx, c.CredentialRef); err != nil {
			return fmt.Errorf("integrations: could not remove the token from the credential store: %w", err)
		}
	}
	if s.o.UnregisterSecret != nil {
		s.o.UnregisterSecret("integration:" + id)
	}
	if err := s.st.DeleteConnection(ctx, id); err != nil {
		return err
	}
	if s.o.OnCredentialChange != nil {
		s.o.OnCredentialChange(c.Kind)
	}
	s.emit(ctx, EventDisconnected, c.ProjectID, "", map[string]any{"integrationId": id, "kind": c.Kind, "by": by})
	return nil
}

// List returns every connection with its sync status.
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

func (s *Service) view(ctx context.Context, c Connection) (ConnectionView, error) {
	v := ConnectionView{Connection: c}
	v.HasCredential = s.hasCredential(ctx, c)
	cur, err := s.st.GetCursor(ctx, c.ID, defaultScope)
	if err != nil {
		return v, err
	}
	n, err := s.st.CountItems(ctx, c.ID)
	if err != nil {
		return v, err
	}
	v.Sync = SyncStatus{LastSuccessAt: cur.LastSuccessAt, LastAttemptAt: cur.LastAttemptAt, LastError: cur.LastError, LastErrorCode: cur.LastErrorCode, ItemsSeen: n,
		ItemsLastSync: cur.ItemsLastSync, ConsecutiveFailures: cur.ConsecutiveFailures, NextAttemptAt: cur.NextAttemptAt, Watermark: cur.Watermark, Resuming: cur.After != ""}
	switch {
	case !c.Enabled:
		v.Sync.State = "paused"
	case cur.LastAttemptAt == 0:
		v.Sync.State = "never_synced"
	case cur.LastErrorCode == CodeUnauthorized:
		v.Sync.State = "unauthenticated"
	case cur.LastErrorCode == CodeRateLimited && cur.NextAttemptAt > s.nowMS():
		v.Sync.State = "rate_limited"
	case cur.ConsecutiveFailures > 0 && cur.NextAttemptAt > s.nowMS():
		v.Sync.State = "backing_off"
	case cur.ConsecutiveFailures > 0:
		v.Sync.State = "error"
	default:
		v.Sync.State = "ok"
	}
	if ad, ok := AdapterFor(c.Kind); ok {
		for _, n := range ad.Needs() {
			if !n.Write {
				v.Tools.SyncTools = append(v.Tools.SyncTools, n.Candidates[0])
			}
		}
		v.Tools.Ops = ad.Ops()
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

// hasCredential reports whether the connection's token can be read now: from the vault, or from its OAuth sign-in.
func (s *Service) hasCredential(ctx context.Context, c Connection) bool {
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

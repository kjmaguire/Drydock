package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Options configures a Gateway. Only DataDir is required; everything else has a documented default or disables the
// feature that needs it (no DB: no result cache and no usage rows; no Bus: no events; no Proc: no stdio upstreams).
type Options struct {
	// DataDir is the app data directory: runs/<run_id>/ holds mcp.json and the raw results (mcp/<call_id>.raw),
	// mcp-cache/ holds cached results.
	DataDir string
	DB      *storage.DB
	Bus     events.Bus
	// Proc starts stdio upstreams. They are never started any other way (Job Objects must be able to kill the tree).
	Proc        proc.Manager
	Scanner     Scanner // default BasicScanner
	Credentials CredentialSource
	Approver    Approver
	// Permissions answers the permission_prompt tool. Nil: every permission request is refused.
	Permissions PermissionPrompter
	Workspace   Workspace
	// ShimCommand is the argv of the stdio shim a CLI spawns (docs/mcp.md section 2, transport A). Default:
	// {os.Executable(), "mcp-shim"}.
	ShimCommand []string
	// Addr is the listen address; it must be a loopback address. Default 127.0.0.1:0 (ephemeral port).
	Addr string

	DefaultInlineBytes int           // shaping budget when neither the catalog nor the tool sets one (default 8192)
	FetchRangeMax      int           // largest fetch_range length (default 65536)
	FetchRangeBurst    int           // fetch_range rate limit: burst (default 30) ...
	FetchRangePerSec   float64       // ... and refill per second (default 5)
	PostBurst          int           // room.post rate limit (default 10)
	PostPerSec         float64       // default 0.5
	IdleTimeout        time.Duration // idle stdio/HTTP upstream shutdown (default 10 min)
	CallTimeout        time.Duration // per upstream call (default 60 s)
	InitTimeout        time.Duration // upstream initialize budget (default 10 s)
	StopGrace          time.Duration // wait for a stdio upstream to exit after its stdin closed (default 2 s)
	BreakerThreshold   int           // failures before the breaker opens (default 3)
	BreakerOpen        time.Duration // how long it stays open (default 60 s)
	// OnCritical is called when an upstream marked critical failed during a run; the owner pauses the run.
	OnCritical func(run RunInfo, server string, err error)
	Now        func() time.Time
	Logger     *slog.Logger
}

func (o *Options) defaults() {
	if o.Scanner == nil {
		o.Scanner = BasicScanner{}
	}
	if o.Addr == "" {
		o.Addr = "127.0.0.1:0"
	}
	if o.DefaultInlineBytes <= 0 {
		o.DefaultInlineBytes = 8192
	}
	if o.FetchRangeMax <= 0 {
		o.FetchRangeMax = 65536
	}
	if o.FetchRangeBurst <= 0 {
		o.FetchRangeBurst = 30
	}
	if o.FetchRangePerSec <= 0 {
		o.FetchRangePerSec = 5
	}
	if o.PostBurst <= 0 {
		o.PostBurst = 10
	}
	if o.PostPerSec <= 0 {
		o.PostPerSec = 0.5
	}
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = 10 * time.Minute
	}
	if o.CallTimeout <= 0 {
		o.CallTimeout = 60 * time.Second
	}
	if o.InitTimeout <= 0 {
		o.InitTimeout = 10 * time.Second
	}
	if o.StopGrace <= 0 {
		o.StopGrace = 2 * time.Second
	}
	if o.BreakerThreshold <= 0 {
		o.BreakerThreshold = 3
	}
	if o.BreakerOpen <= 0 {
		o.BreakerOpen = 60 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
}

// Gateway serves one MCP endpoint per agent run on a loopback listener.
type Gateway struct {
	opts     Options
	ctx      context.Context
	cancel   context.CancelFunc
	httpBase http.RoundTripper
	cache    *cacheStore

	mu   sync.RWMutex
	runs map[string]*Run
	pool map[string]*upstream
	ln   net.Listener
	srv  *http.Server
	wg   sync.WaitGroup

	authMu   sync.Mutex
	authSeen map[string]*bucket
	authAll  *bucket // every failure together, whatever the run id says

	fmu     sync.Mutex
	flights map[string]*flight
}

// New creates a gateway. Call Start to listen.
func New(opts Options) (*Gateway, error) {
	opts.defaults()
	if opts.DataDir == "" {
		return nil, errors.New("mcp: Options.DataDir is required")
	}
	host, _, err := net.SplitHostPort(opts.Addr)
	if err != nil {
		return nil, fmt.Errorf("mcp: bad listen address %q: %w", opts.Addr, err)
	}
	if !isLoopbackHost(host) {
		return nil, fmt.Errorf("mcp: listen address %q is not loopback: the gateway never listens on a routable interface (brief section 16)", opts.Addr)
	}
	if len(opts.ShimCommand) == 0 {
		exe, _ := os.Executable()
		opts.ShimCommand = []string{exe, "mcp-shim"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	g := &Gateway{
		opts: opts, ctx: ctx, cancel: cancel, httpBase: upstreamTransport(),
		runs: map[string]*Run{}, pool: map[string]*upstream{}, authSeen: map[string]*bucket{}, flights: map[string]*flight{},
	}
	g.authAll = newBucket(30, 30.0/60, g.now)
	if opts.DB != nil {
		g.cache = &cacheStore{db: opts.DB, dir: filepath.Join(opts.DataDir, "mcp-cache"), now: opts.Now}
	}
	return g, nil
}

func (g *Gateway) now() time.Time { return g.opts.Now() }

// Start binds the listener and serves in the background.
func (g *Gateway) Start() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ln != nil {
		return errors.New("mcp: gateway already started")
	}
	ln, err := net.Listen("tcp", g.opts.Addr)
	if err != nil {
		return err
	}
	g.ln = ln
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp/", g.serveMCP)
	// the address is loopback and the callers are the agents' CLIs and the shim: small requests, and none that sit idle for long
	// without being a held-open stream, so the header size is limited and an idle connection is closed (audit 2026-10-16, L24)
	g.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 64 << 10, IdleTimeout: 5 * time.Minute}
	g.wg.Add(2)
	safe.Go("mcp.Gateway.Start", func() { defer g.wg.Done(); _ = g.srv.Serve(ln) })
	safe.Go("mcp.Gateway.Start#2", func() { defer g.wg.Done(); g.reaper() })
	return nil
}

// Addr returns the listen address ("127.0.0.1:port") once started.
func (g *Gateway) Addr() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.ln == nil {
		return ""
	}
	return g.ln.Addr().String()
}

// Close ends every run, stops every upstream (killing stdio process trees) and stops listening.
func (g *Gateway) Close() error {
	g.mu.RLock()
	srv := g.srv
	g.mu.RUnlock()
	g.cancelRuns()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
		_ = srv.Close()
	}
	g.stopAllUpstreams("gateway closed")
	g.cancel()
	g.wg.Wait()
	return nil
}

func (g *Gateway) cancelRuns() {
	g.mu.RLock()
	ids := make([]string, 0, len(g.runs))
	for id := range g.runs {
		ids = append(ids, id)
	}
	g.mu.RUnlock()
	for _, id := range ids {
		g.EndRun(id)
	}
}

func (g *Gateway) stopAllUpstreams(reason string) {
	g.mu.Lock()
	ups := make([]*upstream, 0, len(g.pool))
	for _, u := range g.pool {
		ups = append(ups, u)
	}
	g.mu.Unlock()
	var wg sync.WaitGroup
	for _, u := range ups {
		wg.Add(1)
		safe.Go("mcp.Gateway.stopAllUpstreams", func() { defer wg.Done(); u.stop(reason) })
	}
	wg.Wait()
}

// reaper stops upstreams idle for longer than IdleTimeout.
func (g *Gateway) reaper() {
	iv := g.opts.IdleTimeout / 4
	if iv < 50*time.Millisecond {
		iv = 50 * time.Millisecond
	}
	if iv > time.Minute {
		iv = time.Minute
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-t.C:
			g.ReapIdle()
		}
	}
}

// ReapIdle stops every connected upstream that has been idle for IdleTimeout. It returns the ids it stopped.
func (g *Gateway) ReapIdle() []string {
	g.mu.RLock()
	var idle []*upstream
	for _, u := range g.pool {
		if u.isConnected() && u.inflight.Load() == 0 && g.now().UnixNano()-u.lastUsed.Load() > int64(g.opts.IdleTimeout) {
			idle = append(idle, u)
		}
	}
	g.mu.RUnlock()
	var ids []string
	for _, u := range idle {
		u.stop("idle")
		ids = append(ids, u.spec.ID)
	}
	return ids
}

// PurgeCache removes expired cache entries and emits mcp.cache_evict for the count.
func (g *Gateway) PurgeCache(ctx context.Context) (int, error) {
	n, err := g.cache.Purge(ctx)
	if n > 0 {
		g.publish(ctx, EventCacheEvict, RunInfo{}, "", map[string]any{"entries": n, "reason": "expired"})
	}
	return n, err
}

// InvalidateCache removes every cached result of a server (for example after pack.refreshed).
func (g *Gateway) InvalidateCache(ctx context.Context, serverID string) (int, error) {
	n, err := g.cache.InvalidateServer(ctx, serverID)
	if n > 0 {
		g.publish(ctx, EventCacheEvict, RunInfo{}, "", map[string]any{"server": serverID, "entries": n, "reason": "invalidated"})
	}
	return n, err
}

// StopUpstream stops the shared connection(s) of a server id (for example after its configuration changed).
func (g *Gateway) StopUpstream(serverID string) {
	g.mu.RLock()
	var ups []*upstream
	for _, u := range g.pool {
		if u.spec.ID == serverID {
			ups = append(ups, u)
		}
	}
	g.mu.RUnlock()
	for _, u := range ups {
		u.stop("stopped")
	}
}

// UpstreamPIDs returns the pids of running stdio upstreams by server id (diagnostics and tests).
func (g *Gateway) UpstreamPIDs() map[string]int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := map[string]int{}
	for _, u := range g.pool {
		u.mu.Lock()
		if u.proc != nil && u.sess != nil {
			out[u.spec.ID] = u.proc.PID()
		}
		u.mu.Unlock()
	}
	return out
}

// ---- events ----

// publish emits an event; failures to publish never fail a tool call.
func (g *Gateway) publish(ctx context.Context, typ string, run RunInfo, callID string, data map[string]any) {
	if g.opts.Bus == nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	if run.RunID != "" {
		data["run_id"] = run.RunID
	}
	if callID != "" {
		data["call_id"] = callID
	}
	b, err := json.Marshal(data)
	if err != nil {
		g.opts.Logger.Error("mcp: event payload", "type", typ, "err", err)
		return
	}
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := g.opts.Bus.Publish(pctx, events.Event{Type: typ, ProjectID: run.ProjectID, RunID: run.RunID, TaskID: run.TaskID, AgentID: run.AgentID, Data: b}); err != nil {
		g.opts.Logger.Warn("mcp: publish failed", "type", typ, "err", err)
	}
}

// scrub redacts secrets from text that is about to be logged or published.
func (g *Gateway) scrub(s string) string {
	out, _ := redact(s, g.opts.Scanner.Scan(s))
	return out
}

// ---- runs ----

// RunConfig describes one agent run's endpoint.
type RunConfig struct {
	Info RunInfo
	// DataClass is the run's provider data class. A "trainable" run gets no Workspace tools and no private_data
	// upstreams (security.md section 9, E7).
	DataClass string
	Policy    RunPolicy
	// Servers are the upstreams enabled for the project (Catalog.Resolve); they are started lazily on first use.
	Servers []ServerSpec
	// RunDir overrides <DataDir>/runs/<run_id>.
	RunDir string
}

// Endpoint is what the Agent Runtime needs to point a CLI at the gateway. Token is a secret: it goes only into the run's
// config file (user-ACL only) or the shim's environment, never into logs, events, prompts or SQLite.
type Endpoint struct {
	RunID string
	// URL is the streamable HTTP endpoint, http://127.0.0.1:<port>/mcp/<run_id>.
	URL   string
	Token string
	// ShimCommand is the argv of the stdio shim (transport A).
	ShimCommand []string
	// ConfigPath is where WriteConfig puts the CLI config file by default: <run dir>/mcp.json.
	ConfigPath string
}

var runIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// StartRun registers a run and returns its endpoint. The gateway listens before the CLI is spawned and answers
// initialize and tools/list without contacting any upstream that declares its tools.
func (g *Gateway) StartRun(cfg RunConfig) (*Endpoint, error) {
	id := cfg.Info.RunID
	if !runIDRe.MatchString(id) {
		return nil, fmt.Errorf("mcp: invalid run id %q", id)
	}
	if cfg.DataClass == "" {
		cfg.DataClass = DataClassPrivate
	}
	if cfg.DataClass != DataClassPrivate && cfg.DataClass != DataClassTrainable {
		return nil, fmt.Errorf("mcp: invalid data class %q", cfg.DataClass)
	}
	specs := map[string]*ServerSpec{}
	for i := range cfg.Servers {
		s := cfg.Servers[i]
		s.applyDefaults()
		if err := s.Validate(false); err != nil {
			return nil, fmt.Errorf("mcp: server %q: %w", s.ID, err)
		}
		if specs[s.ID] != nil {
			return nil, fmt.Errorf("mcp: duplicate server %q", s.ID)
		}
		specs[s.ID] = &s
	}
	for sid := range cfg.Policy.Allow {
		if sid != WorkspaceServerID && specs[sid] == nil {
			// An allowlist entry for a server that is not enabled grants nothing; flag it instead of silently ignoring it.
			g.opts.Logger.Warn("mcp: policy allows a server that is not enabled for the run", "run", id, "server", sid)
		}
	}
	g.mu.Lock()
	if g.ln == nil {
		g.mu.Unlock()
		return nil, errors.New("mcp: gateway is not started")
	}
	if _, dup := g.runs[id]; dup {
		g.mu.Unlock()
		return nil, fmt.Errorf("mcp: run %q already has an endpoint", id)
	}
	port := g.ln.Addr().(*net.TCPAddr).Port
	g.mu.Unlock()

	var tok [32]byte
	if _, err := rand.Read(tok[:]); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(tok[:])
	dir := cfg.RunDir
	if dir == "" {
		dir = filepath.Join(g.opts.DataDir, "runs", id)
	}
	if err := os.MkdirAll(filepath.Join(dir, "mcp"), 0o700); err != nil {
		return nil, err
	}
	r := &Run{
		g: g, cfg: cfg, dir: dir, specs: specs, tokenHash: sha256.Sum256([]byte(token)),
		handles: map[string]string{}, creds: map[string]credInfo{}, done: make(chan struct{}),
		fetchLim: newBucket(g.opts.FetchRangeBurst, g.opts.FetchRangePerSec, g.now),
		postLim:  newBucket(g.opts.PostBurst, g.opts.PostPerSec, g.now),
	}
	r.server = sdk.NewServer(&sdk.Implementation{Name: "drydock-gateway", Version: "1"}, &sdk.ServerOptions{
		Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{}},
		Logger:       g.opts.Logger,
	})
	r.server.AddReceivingMiddleware(r.middleware)
	r.handler = sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return r.server }, &sdk.StreamableHTTPOptions{Logger: g.opts.Logger})

	g.mu.Lock()
	if _, dup := g.runs[id]; dup {
		g.mu.Unlock()
		return nil, fmt.Errorf("mcp: run %q already has an endpoint", id)
	}
	g.runs[id] = r
	g.mu.Unlock()
	return &Endpoint{
		RunID: id, URL: fmt.Sprintf("http://127.0.0.1:%d/mcp/%s", port, id), Token: token,
		ShimCommand: append([]string(nil), g.opts.ShimCommand...), ConfigPath: filepath.Join(dir, "mcp.json"),
	}, nil
}

// EndRun invalidates the run's token, closes its sessions and deletes its config file. The raw results stay on disk
// (retention is brief section 18). Upstreams are shared and keep running until idle.
func (g *Gateway) EndRun(runID string) {
	g.mu.Lock()
	r := g.runs[runID]
	delete(g.runs, runID)
	g.mu.Unlock()
	if r == nil {
		return
	}
	r.closed.Store(true)
	r.closeOnce.Do(func() { close(r.done) })
	for s := range r.server.Sessions() {
		_ = s.Close()
	}
	_ = os.Remove(filepath.Join(r.dir, "mcp.json"))
}

func (g *Gateway) run(id string) *Run {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.runs[id]
}

// ---- HTTP ----

func (g *Gateway) serveMCP(w http.ResponseWriter, req *http.Request) {
	runID := strings.TrimPrefix(req.URL.Path, "/mcp/")
	if runID == "" || strings.Contains(runID, "/") {
		http.NotFound(w, req)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	// DNS-rebinding defence: only loopback Host names on our own port, and no browser Origin at all.
	if !g.hostOK(req.Host) {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	if req.Header.Get("Origin") != "" {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	run := g.run(runID)
	if run == nil || !run.tokenOK(req.Header.Get("Authorization")) {
		g.authFailed(req, runID, run != nil)
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	run.handler.ServeHTTP(w, req)
}

func (g *Gateway) hostOK(host string) bool {
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		return false
	}
	if !isLoopbackHost(h) {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.ln == nil {
		return false
	}
	return p == strconv.Itoa(g.ln.Addr().(*net.TCPAddr).Port)
}

// authFailed emits mcp.auth_failed, at most a handful per run id per minute and a few dozen in all, so that a flood cannot fill
// the event log. The run id is part of the address the caller chose: only an id of the shape Drydock makes gets a bucket of its
// own (at most 1024 of them, each a few dozen bytes); every other path shares one, so a caller cannot make the gateway hold
// memory in proportion to what it sends (audit 2026-10-16, L24).
func (g *Gateway) authFailed(req *http.Request, runID string, known bool) {
	safeID := runID
	if !runIDRe.MatchString(safeID) {
		safeID = "(invalid)"
	}
	g.authMu.Lock()
	b := g.authSeen[safeID]
	if b == nil {
		if len(g.authSeen) >= 1024 {
			g.authSeen = map[string]*bucket{}
		}
		b = newBucket(5, 5.0/60, g.now)
		g.authSeen[safeID] = b
	}
	g.authMu.Unlock()
	if !b.allow() || !g.authAll.allow() {
		return
	}
	reason := "missing or invalid bearer token"
	if !known {
		reason = "unknown or ended run"
	}
	g.publish(req.Context(), EventAuthFailed, RunInfo{RunID: safeID}, "", map[string]any{"reason": reason, "remote": req.RemoteAddr})
}

// ---- upstream pool ----

type credInfo struct {
	token string
	id    string
}

func specPoolHash(s ServerSpec) string {
	v := struct {
		T, URL, Header, Scheme string
		Params                 map[string]any
		Cmd                    *CommandSpec
		Auth                   string
	}{s.Transport, s.URL, s.Auth.Header, s.Auth.Scheme, s.Shape.URLParams, s.Command, s.Auth.Kind}
	b, _ := yaml.Marshal(v)
	return sha256Hex(b)[:24]
}

// upstreamFor returns the shared upstream for a spec and the run's credential for it.
func (r *Run) upstreamFor(ctx context.Context, spec *ServerSpec) (*upstream, error) {
	ci, err := r.credential(ctx, spec)
	if err != nil {
		return nil, &unavailableError{spec.ID, err}
	}
	key := spec.ID + "|" + specPoolHash(*spec) + "|" + ci.id
	g := r.g
	g.mu.Lock()
	defer g.mu.Unlock()
	if u := g.pool[key]; u != nil {
		return u, nil
	}
	u := &upstream{g: g, spec: *spec, key: key, credID: ci.id, token: ci.token}
	u.version = g.loadVersion(key)
	u.touch()
	g.pool[key] = u
	return u, nil
}

func (r *Run) credential(ctx context.Context, spec *ServerSpec) (credInfo, error) {
	r.credMu.Lock()
	defer r.credMu.Unlock()
	if ci, ok := r.creds[spec.ID]; ok {
		return ci, nil
	}
	ci := credInfo{id: "anon"}
	if spec.Transport == TransportHTTP && spec.Auth.Kind != AuthNone {
		var tok string
		var err error
		if r.g.opts.Credentials != nil {
			tok, err = r.g.opts.Credentials.Credential(ctx, spec.ID, spec.Auth.Keyring)
		} else {
			err = errors.New("no credential source configured")
		}
		if err != nil || tok == "" {
			if !spec.Auth.Optional {
				if err == nil {
					err = errors.New("credential is empty")
				}
				hint := ""
				if spec.Auth.Kind == AuthPKCE || spec.Auth.Kind == AuthDCR {
					hint = " (sign in to the server first: the OAuth flow is run by the application, not by this package)"
				}
				return credInfo{}, fmt.Errorf("credentials for %s are not available%s: %v", spec.ID, hint, err)
			}
			tok = ""
		}
		if tok != "" {
			sum := sha256.Sum256([]byte(tok))
			ci = credInfo{token: tok, id: hex.EncodeToString(sum[:8])}
		}
	}
	r.creds[spec.ID] = ci
	return ci, nil
}

// ---- token buckets ----

type bucket struct {
	mu     sync.Mutex
	cap    float64
	rate   float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

func newBucket(burst int, perSec float64, now func() time.Time) *bucket {
	return &bucket{cap: float64(burst), rate: perSec, tokens: float64(burst), last: now(), now: now}
}

func (b *bucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := b.now()
	b.tokens += t.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.cap {
		b.tokens = b.cap
	}
	b.last = t
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (b *bucket) retryAfter() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rate <= 0 {
		return time.Minute
	}
	need := 1 - b.tokens
	if need <= 0 {
		return 0
	}
	return time.Duration(need / b.rate * float64(time.Second))
}

// tokenOK is a constant-time bearer check.
func (r *Run) tokenOK(authz string) bool {
	if r.closed.Load() {
		return false
	}
	scheme, tok, ok := strings.Cut(strings.TrimSpace(authz), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(tok)))
	return subtle.ConstantTimeCompare(sum[:], r.tokenHash[:]) == 1
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

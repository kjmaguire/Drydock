package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kjmaguire/drydock/internal/proc"
)

// upstream is one shared connection to a catalog or custom server (docs/mcp.md section 11): started lazily on the first
// use, shared by every run that enabled it (the gateway multiplexes with per-run allowlists), stopped after an idle
// timeout, guarded by a circuit breaker.
type upstream struct {
	g      *Gateway
	spec   ServerSpec
	key    string // pool key: spec hash + credential identity
	credID string // non-secret identity of the credential (hash prefix)
	token  string // resolved credential; memory only, never logged

	mu       sync.Mutex // serializes connect and teardown
	sess     *sdk.ClientSession
	proc     proc.Proc
	exited   chan struct{} // closed when the stdio process exited
	exitInfo proc.ExitInfo
	stopping bool
	version  string
	tools    []*sdk.Tool
	restarts int
	stderr   *tailBuf

	bmu       sync.Mutex // breaker
	failures  int
	openUntil time.Time

	lastUsed atomic.Int64
	inflight atomic.Int32
}

var errCircuitOpen = errors.New("circuit breaker open")

// unavailable wraps a cause as the structured upstream_unavailable error.
type unavailableError struct {
	server string
	cause  error
}

func (e *unavailableError) Error() string {
	return fmt.Sprintf("%s: server %s: %v", ErrUpstreamUnavailable, e.server, e.cause)
}
func (e *unavailableError) Unwrap() []error { return []error{ErrUpstreamUnavailable, e.cause} }

func (u *upstream) touch() { u.lastUsed.Store(u.g.now().UnixNano()) }

func (u *upstream) breakerOpen() (bool, time.Time) {
	u.bmu.Lock()
	defer u.bmu.Unlock()
	if u.failures >= u.g.opts.BreakerThreshold && u.g.now().Before(u.openUntil) {
		return true, u.openUntil
	}
	return false, time.Time{}
}

func (u *upstream) noteFailure() {
	u.bmu.Lock()
	defer u.bmu.Unlock()
	u.failures++
	if u.failures >= u.g.opts.BreakerThreshold {
		u.openUntil = u.g.now().Add(u.g.opts.BreakerOpen)
	}
}

func (u *upstream) noteSuccess() {
	u.bmu.Lock()
	u.failures = 0
	u.openUntil = time.Time{}
	u.bmu.Unlock()
}

// ensure returns a live session, connecting (or reconnecting after a crash) when needed.
func (u *upstream) ensure(ctx context.Context) (*sdk.ClientSession, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.sess != nil {
		return u.sess, nil
	}
	if open, until := u.breakerOpen(); open {
		return nil, &unavailableError{u.spec.ID, fmt.Errorf("%w until %s", errCircuitOpen, until.Format(time.RFC3339))}
	}
	if err := u.connectLocked(ctx); err != nil {
		u.noteFailure()
		return nil, &unavailableError{u.spec.ID, err}
	}
	return u.sess, nil
}

func (u *upstream) connectLocked(ctx context.Context) error {
	g := u.g
	if u.spec.Transport == TransportStdio && g.opts.Proc == nil {
		return errors.New("no process manager configured for stdio servers")
	}
	ictx, cancel := context.WithTimeout(ctx, g.opts.InitTimeout)
	defer cancel()
	client := sdk.NewClient(&sdk.Implementation{Name: "drydock-gateway", Version: "1"}, nil)
	var (
		tr sdk.Transport
		p  proc.Proc
	)
	switch u.spec.Transport {
	case TransportHTTP:
		ep, err := upstreamEndpoint(u.spec)
		if err != nil {
			return err
		}
		at := &authTransport{base: g.httpBase, header: u.spec.Auth.Header, value: u.authValue()}
		if (u.spec.Auth.Kind == AuthPKCE || u.spec.Auth.Kind == AuthDCR) && g.opts.Credentials != nil {
			// OAuth access tokens expire while an upstream connection lives on: ask the credential source (which refreshes) for each request
			spec := u.spec
			at.dynamic = func(ctx context.Context) (string, error) {
				tok, err := g.opts.Credentials.Credential(ctx, spec.ID, spec.Auth.Keyring)
				if err != nil || tok == "" {
					if err == nil {
						err = errors.New("credential is empty")
					}
					return "", err
				}
				return spec.Auth.Scheme + " " + tok, nil
			}
		}
		hc := &http.Client{
			Transport: at,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("upstream redirect refused (credentials are never forwarded to another URL)")
			},
		}
		tr = &sdk.StreamableClientTransport{Endpoint: ep, HTTPClient: hc, DisableStandaloneSSE: true, MaxRetries: 2}
	case TransportStdio:
		argv := u.spec.Command.Argv
		env := baseEnv()
		for k, v := range u.spec.Command.Env {
			env = append(env, k+"="+v)
		}
		for name, ref := range u.spec.Command.SecretEnv {
			if g.opts.Credentials == nil {
				return fmt.Errorf("credential %q for %s is needed but no credential source is configured", ref, name)
			}
			v, err := g.opts.Credentials.Credential(ictx, u.spec.ID, ref)
			if err != nil {
				return fmt.Errorf("credential %q: %w", ref, err)
			}
			env = append(env, name+"="+v)
		}
		var err error
		p, err = g.opts.Proc.Start(g.ctx, proc.Spec{Path: argv[0], Args: argv[1:], Dir: u.spec.Command.Cwd, Env: env, Label: "mcp:" + u.spec.ID})
		if err != nil {
			return fmt.Errorf("start %s: %w", argv[0], err)
		}
		se := &tailBuf{max: 8192}
		u.stderr = se
		go func() { _, _ = io.Copy(se, p.Stderr()) }()
		tr = &sdk.IOTransport{Reader: io.NopCloser(p.Stdout()), Writer: p.Stdin()}
	default:
		return fmt.Errorf("unknown transport %q", u.spec.Transport)
	}
	sess, err := client.Connect(ictx, tr, nil)
	if err != nil {
		if p != nil {
			_ = p.Kill(true)
			<-p.Wait()
		}
		return fmt.Errorf("connect: %w", err)
	}
	u.sess = sess
	u.proc = p
	u.stopping = false
	if ir := sess.InitializeResult(); ir != nil && ir.ServerInfo != nil {
		u.setVersion(ir.ServerInfo.Name + "@" + ir.ServerInfo.Version)
	}
	pid := 0
	restarts := u.restarts
	if p != nil {
		pid = p.PID()
		exited := make(chan struct{})
		u.exited = exited
		go u.watchProc(p, exited, sess, restarts)
	}
	go func() {
		_ = sess.Wait()
		u.sessionClosed(sess)
	}()
	// Learn the real tool list (and with it the servers' read-only hints) once per connection; the last known list is kept
	// while the server is down so that tools/list keeps answering.
	if tools, err := listAll(ictx, sess); err == nil {
		u.tools = tools
	}
	u.touch()
	g.publish(context.Background(), EventServerStarted, RunInfo{}, "", map[string]any{
		"server": u.spec.ID, "transport": u.spec.Transport, "pid": pid, "restart_count": restarts, "version": u.version,
	})
	u.restarts++
	return nil
}

// watchProc records a stdio process exit; an exit that nobody asked for is an mcp.server.failed event.
func (u *upstream) watchProc(p proc.Proc, exited chan struct{}, sess *sdk.ClientSession, restarts int) {
	ei := <-p.Wait()
	u.mu.Lock()
	u.exitInfo = ei
	stopping := u.stopping
	if u.sess == sess {
		u.sess = nil
	}
	u.mu.Unlock()
	close(exited)
	if !stopping {
		tail := ""
		if u.stderr != nil {
			tail = u.g.scrub(u.stderr.String())
		}
		u.g.publish(context.Background(), EventServerFailed, RunInfo{}, "", map[string]any{
			"server": u.spec.ID, "transport": u.spec.Transport, "pid": p.PID(), "exit_code": ei.Code, "killed": ei.Killed,
			"restart_count": restarts, "stderr_tail": tail,
		})
	}
}

// sessionClosed drops a session that ended on its own (HTTP session expired, server hung up).
func (u *upstream) sessionClosed(s *sdk.ClientSession) {
	u.mu.Lock()
	if u.sess == s {
		u.sess = nil
	}
	u.mu.Unlock()
}

// stop closes the session and kills the whole process tree. It is safe to call repeatedly.
func (u *upstream) stop(reason string) {
	u.mu.Lock()
	sess, p, exited := u.sess, u.proc, u.exited
	if sess == nil && p == nil {
		u.mu.Unlock()
		return
	}
	u.stopping = true
	u.sess = nil
	u.proc, u.exited = nil, nil
	u.mu.Unlock()
	if sess != nil {
		_ = sess.Close() // closes stdin of a stdio server: a well-behaved server exits by itself
	}
	if p != nil {
		select {
		case <-exited:
		case <-time.After(u.g.opts.StopGrace):
		}
		_ = p.Kill(true) // always: grandchildren (npx -> node) outlive the direct child, the job kills them
		<-exited
	}
	u.g.publish(context.Background(), EventServerStopped, RunInfo{}, "", map[string]any{"server": u.spec.ID, "transport": u.spec.Transport, "reason": reason})
}

func (u *upstream) authValue() string {
	if u.token == "" {
		return ""
	}
	if u.spec.Auth.Scheme == "" {
		return u.token
	}
	return u.spec.Auth.Scheme + " " + u.token
}

// setVersion remembers the server version and persists it so that cache keys computed before the next connect (a restart
// of the app) still carry the version the entries were made against.
func (u *upstream) setVersion(v string) {
	if u.spec.Command != nil && u.spec.Command.Pin != "" {
		v += "|" + u.spec.Command.Pin
	}
	if v == u.version {
		return
	}
	u.version = v
	u.g.saveVersion(u.key, v)
}

func (u *upstream) currentVersion() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.version
}

// listTools returns the real tool list, connecting if needed.
func (u *upstream) listTools(ctx context.Context) ([]*sdk.Tool, error) {
	sess, err := u.ensure(ctx)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	if u.tools != nil && u.sess == sess {
		t := u.tools
		u.mu.Unlock()
		return t, nil
	}
	u.mu.Unlock()
	all, err := listAll(ctx, sess)
	if err != nil {
		u.failedCall(ctx, err)
		return nil, &unavailableError{u.spec.ID, err}
	}
	u.noteSuccess()
	u.mu.Lock()
	u.tools = all
	u.mu.Unlock()
	return all, nil
}

// listAll pages through tools/list.
func listAll(ctx context.Context, sess *sdk.ClientSession) ([]*sdk.Tool, error) {
	all := []*sdk.Tool{}
	cursor := ""
	for i := 0; i < 50; i++ {
		res, err := sess.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		all = append(all, res.Tools...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	return all, nil
}

// cachedTools returns the last known real list, without connecting (nil when never listed).
func (u *upstream) cachedTools() []*sdk.Tool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.tools
}

// call forwards a tool call. A returned error means the upstream could not be reached or died; a tool-level error comes
// back as a result with IsError.
func (u *upstream) call(ctx context.Context, tool string, args json.RawMessage) (*sdk.CallToolResult, error) {
	sess, err := u.ensure(ctx)
	if err != nil {
		return nil, err
	}
	u.inflight.Add(1)
	defer u.inflight.Add(-1)
	u.touch()
	cctx, cancel := context.WithTimeout(ctx, u.g.opts.CallTimeout)
	defer cancel()
	var a any
	if len(args) > 0 {
		a = args
	}
	res, err := sess.CallTool(cctx, &sdk.CallToolParams{Name: tool, Arguments: a})
	if err != nil {
		var je *jsonrpc.Error
		if !isTransportErr(err) && errors.As(err, &je) { // the server answered: it is alive, the request was refused
			u.noteSuccess()
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: je.Message}}}, nil
		}
		u.failedCall(ctx, err)
		return nil, &unavailableError{u.spec.ID, err}
	}
	u.noteSuccess()
	u.touch()
	return res, nil
}

// isTransportErr reports whether a call error means the connection (not the request) failed. The SDK reports a request that
// never reached the server as a JSON-RPC error with code -32005 ("rejected by transport"), so the code is checked first.
func isTransportErr(err error) bool {
	var je *jsonrpc.Error
	if errors.As(err, &je) && je.Code == -32005 {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, sdk.ErrConnectionClosed) || errors.Is(err, context.DeadlineExceeded)
}

// failedCall counts a transport-level failure unless the caller itself gave up. An HTTP session that failed is dropped so
// that the next call reconnects; a stdio server is handled by its process watcher.
func (u *upstream) failedCall(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return
	}
	u.noteFailure()
	if u.spec.Transport == TransportHTTP {
		u.mu.Lock()
		sess := u.sess
		u.mu.Unlock()
		if sess != nil {
			u.sessionClosed(sess)
			go func() { _ = sess.Close() }()
		}
	}
}

func (u *upstream) isConnected() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.sess != nil
}

// ---- helpers ----

func upstreamEndpoint(s ServerSpec) (string, error) {
	u, err := url.Parse(s.URL)
	if err != nil {
		return "", err
	}
	if len(s.Shape.URLParams) > 0 {
		q := u.Query()
		for k, v := range s.Shape.URLParams {
			q.Set(k, fmt.Sprint(v))
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// authTransport adds the upstream credential to every request. The credential is held by Drydock, never by the agent.
type authTransport struct {
	base   http.RoundTripper
	header string
	value  string
	// dynamic, when set, supplies the header value for every request (OAuth tokens that refresh).
	dynamic func(ctx context.Context) (string, error)
}

func (t *authTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.dynamic != nil {
		v, err := t.dynamic(r.Context())
		if err != nil {
			return nil, fmt.Errorf("upstream credential: %w", err)
		}
		r = r.Clone(r.Context())
		r.Header.Set(t.header, v)
		return t.base.RoundTrip(r)
	}
	if t.value != "" {
		r = r.Clone(r.Context())
		r.Header.Set(t.header, t.value)
	}
	return t.base.RoundTrip(r)
}

// baseEnv is the environment given to stdio upstreams: an allowlist, never the whole environment (security.md section 8).
func baseEnv() []string {
	allow := map[string]bool{
		"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "WINDIR": true, "COMSPEC": true, "TEMP": true, "TMP": true,
		"TMPDIR": true, "HOME": true, "USERPROFILE": true, "APPDATA": true, "LOCALAPPDATA": true, "PROGRAMFILES": true,
		"PROGRAMFILES(X86)": true, "PROGRAMDATA": true, "LANG": true, "LC_ALL": true, "SYSTEMDRIVE": true, "NUMBER_OF_PROCESSORS": true,
		"HOMEDRIVE": true, "HOMEPATH": true,
	}
	var out []string
	for _, kv := range os.Environ() {
		k, _, ok := strings.Cut(kv, "=")
		if ok && allow[strings.ToUpper(k)] {
			out = append(out, kv)
		}
	}
	return out
}

// tailBuf keeps the last max bytes written to it (stderr of a stdio server).
type tailBuf struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuf) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuf) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

// ---- version persistence (settings table) ----

func (g *Gateway) saveVersion(poolKey, v string) {
	if g.opts.DB == nil {
		return
	}
	k := "mcp.version." + poolKey
	_ = g.opts.DB.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v)
		return err
	})
}

func (g *Gateway) loadVersion(poolKey string) string {
	if g.opts.DB == nil {
		return ""
	}
	var v string
	err := g.opts.DB.Reader().QueryRow(`SELECT value FROM settings WHERE key = ?`, "mcp.version."+poolKey).Scan(&v)
	if err != nil {
		return ""
	}
	return v
}

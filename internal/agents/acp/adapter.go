// Package acp is the one generic Agent Client Protocol adapter. It drives any CLI that speaks ACP over stdio (gemini --acp,
// opencode acp, goose acp, and the pinned npx bridges) through github.com/coder/acp-go-sdk. Capabilities come from the
// agent's own `initialize` response at runtime and never from the provider name (decision D10). Processes start through
// proc.Manager, so on Windows the whole tree (npx.cmd, node, the CLI) lives in one Job Object.
package acp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/proc"
)

// DefaultStartTimeout bounds spawn + initialize + session/new. A first `npx -y` run downloads the bridge, so it is generous.
const DefaultStartTimeout = 3 * time.Minute

// Config describes one ACP agent. Everything here comes from a manifest (internal/agents/manifests); nothing in this package
// switches on a provider name.
type Config struct {
	ID       string   // adapter id ("gemini", "opencode", ...)
	Command  string   // executable, resolved by proc through exec.LookPath (npx -> npx.cmd on Windows)
	Args     []string // argv after the executable
	Env      []string // KEY=VALUE added to the environment (after inheritance)
	Declared agents.Capabilities
	// InstallHint is the vendor's install command, shown by Detect and never executed.
	InstallHint string
	// Detector implements Detect (internal/connections). When nil a minimal PATH lookup is used.
	Detector func(ctx context.Context) (agents.Detection, error)
	// Permission answers session/request_permission. Nil means RejectOnce: nothing is ever approved by default.
	Permission PermissionHandler
	// Terminal advertises and serves the client-side terminal methods. Off by default: the agent then uses its own shell
	// tool (subject to its own approval flow). Terminals run argv without a shell and only in a directory inside the cwd.
	Terminal bool
	// NoFS stops advertising fs/read_text_file and fs/write_text_file. By default both are served and confined to the cwd.
	NoFS bool
	// ClientMeta is sent as `clientCapabilities._meta` in initialize: optional agent behaviour a manifest opts in to
	// (transports.acp.client_meta), never declared for every agent. OpenCode's "opencode/child-session-updates" moves its
	// sub-agents' updates off the session's own stream onto an extension notification (ext.go).
	ClientMeta map[string]any
	// ScrubEnv starts the child from a small allowlist of the process environment instead of inheriting all of it.
	ScrubEnv bool
	// PassEnv returns extra variable names to keep when ScrubEnv is on (read at every start).
	PassEnv      func() []string
	StartTimeout time.Duration
	// Logger receives connection diagnostics; nil discards them (they can contain protocol payloads).
	Logger *slog.Logger
}

// Adapter implements agents.Adapter for one ACP agent.
type Adapter struct {
	cfg Config

	mu       sync.Mutex
	observed Observed
	have     bool
}

var _ agents.Adapter = (*Adapter)(nil)

// New returns an adapter for cfg.
func New(cfg Config) *Adapter {
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = DefaultStartTimeout
	}
	return &Adapter{cfg: cfg}
}

// ID implements agents.Adapter.
func (a *Adapter) ID() string { return a.cfg.ID }

// Config returns the adapter's configuration.
func (a *Adapter) Config() Config { return a.cfg }

// Capabilities returns the declared capabilities merged with everything observed from the agent so far. Before any
// session has started that is the manifest's declaration plus the ACP protocol baselines.
func (a *Adapter) Capabilities() agents.Capabilities {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Capabilities(a.cfg.Declared, a.observed)
}

// Observed returns what the last session or handshake saw, and whether anything was seen.
func (a *Adapter) Observed() (Observed, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.observed, a.have
}

func (a *Adapter) record(o Observed) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.have { // usage and cost evidence survives a later session that has not produced any yet
		o.Usage = o.Usage || a.observed.Usage
		o.Cost = o.Cost || a.observed.Cost
	}
	a.observed, a.have = o, true
}

// Detect implements agents.Adapter.
func (a *Adapter) Detect(ctx context.Context) (agents.Detection, error) {
	if a.cfg.Detector != nil {
		return a.cfg.Detector(ctx)
	}
	d := agents.Detection{Adapter: a.cfg.ID, Status: agents.StatusNotInstalled, InstallHint: a.cfg.InstallHint}
	p, err := exec.LookPath(a.cfg.Command)
	if err != nil {
		d.Problems = append(d.Problems, a.cfg.Command+" was not found on PATH")
		return d, nil
	}
	d.Installed, d.Path, d.Status = true, p, agents.StatusReady
	return d, nil
}

// Start implements agents.Adapter: spawn, initialize, then session/new (or resume/load when spec.ResumeID is set).
// The context bounds the process lifetime exactly as proc.Manager.Start documents: cancelling it kills the tree.
func (a *Adapter) Start(ctx context.Context, pm proc.Manager, spec agents.RunSpec) (agents.Session, error) {
	s, err := a.launch(ctx, pm, spec)
	if err != nil {
		return nil, err
	}
	octx, cancel := context.WithTimeout(ctx, a.cfg.StartTimeout)
	defer cancel()
	if err := s.openSession(octx); err != nil {
		_ = s.Stop(context.Background())
		return nil, s.wrap(err)
	}
	a.record(s.observedNow())
	return s, nil
}

// Handshake is the protocol part of "Test connection": spawn, initialize, try session/new, report, stop the tree. A failing
// session/new (typically "authentication required") is reported, not returned as an error, because the initialize result is
// still real evidence.
type Handshake struct {
	Info        AgentInfo   `json:"info"`
	Session     SessionInfo `json:"session"`
	SessionOK   bool        `json:"sessionOk"`
	SessionErr  string      `json:"sessionErr,omitempty"`
	InitElapsed time.Duration
	PID         int
	Stderr      string
}

// Handshake runs the protocol handshake against the real agent and stops it.
func (a *Adapter) Handshake(ctx context.Context, pm proc.Manager, spec agents.RunSpec) (Handshake, error) {
	var h Handshake
	began := time.Now()
	s, err := a.launch(ctx, pm, spec)
	if err != nil {
		return h, err
	}
	defer func() { _ = s.Stop(context.Background()) }()
	h.Info, h.PID, h.InitElapsed = s.info, s.PID(), time.Since(began)
	octx, cancel := context.WithTimeout(ctx, a.cfg.StartTimeout)
	defer cancel()
	if err := s.openSession(octx); err != nil {
		h.SessionErr = s.wrap(err).Error()
	} else {
		h.SessionOK = true
		h.Session = s.sinfo
	}
	h.Stderr = s.Stderr()
	a.record(s.observedNow())
	return h, nil
}

// launch spawns the process and completes `initialize`.
func (a *Adapter) launch(ctx context.Context, pm proc.Manager, spec agents.RunSpec) (*Session, error) {
	if a.cfg.Command == "" {
		return nil, errors.New("acp: adapter has no command")
	}
	if spec.Cwd == "" {
		return nil, errors.New("acp: RunSpec.Cwd is required")
	}
	g, err := newGuard(spec.Cwd)
	if err != nil {
		return nil, err
	}
	var pass []string
	if a.cfg.ScrubEnv && a.cfg.PassEnv != nil {
		pass = proc.ScrubEnv(os.Environ(), a.cfg.PassEnv(), nil)
	}
	env := buildEnv(os.Environ(), a.cfg.ScrubEnv, pass, a.cfg.Env, spec.Env)
	p, err := pm.Start(ctx, proc.Spec{Path: a.cfg.Command, Args: a.cfg.Args, Dir: spec.Cwd, Env: env, Label: "agent:" + spec.AgentID})
	if err != nil {
		g.close()
		return nil, fmt.Errorf("acp %s: start %s: %w", a.cfg.ID, a.cfg.Command, err)
	}
	s := newSession(a, pm, p, g, spec)
	ictx, cancel := context.WithTimeout(ctx, a.cfg.StartTimeout)
	defer cancel()
	caps := acp.ClientCapabilities{
		Fs:       acp.FileSystemCapabilities{ReadTextFile: !a.cfg.NoFS, WriteTextFile: !a.cfg.NoFS},
		Terminal: a.cfg.Terminal,
	}
	if len(a.cfg.ClientMeta) > 0 {
		caps.Meta = maps.Clone(a.cfg.ClientMeta)
	}
	resp, err := s.conn.Initialize(ictx, acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersionNumber,
		ClientInfo:         &acp.Implementation{Name: "drydock", Title: acp.Ptr("Drydock"), Version: "0"},
		ClientCapabilities: caps,
	})
	if err != nil {
		_ = s.Stop(context.Background())
		return nil, s.wrap(fmt.Errorf("initialize: %w", err))
	}
	s.info = parseInit(resp)
	return s, nil
}

// baseEnvAllow is the environment a scrubbed child keeps: enough to run a CLI, nothing that carries credentials.
var baseEnvAllow = []string{
	"PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "COMSPEC", "TEMP", "TMP", "TMPDIR", "HOME", "USERPROFILE", "APPDATA",
	"LOCALAPPDATA", "PROGRAMDATA", "PROGRAMFILES", "PROGRAMFILES(X86)", "HOMEDRIVE", "HOMEPATH", "USERNAME", "LANG", "LC_ALL",
	"TERM", "TZ", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "NO_COLOR",
}

// buildEnv composes the child environment: inherited (or allowlisted) first, then manifest extras, then per-run extras.
// Later entries win. Values are never logged.
func buildEnv(parent []string, scrub bool, extra ...[]string) []string {
	var env []string
	if scrub {
		allow := map[string]bool{}
		for _, k := range baseEnvAllow {
			allow[strings.ToUpper(k)] = true
		}
		for _, kv := range parent {
			if k, _, ok := strings.Cut(kv, "="); ok && allow[strings.ToUpper(k)] {
				env = append(env, kv)
			}
		}
	} else {
		env = append(env, parent...)
	}
	for _, e := range extra {
		env = append(env, e...)
	}
	return env
}

// wrap adds the stderr tail and, for authentication failures, the agent's own sign-in methods to an error.
func (s *Session) wrap(err error) error {
	msg := err.Error()
	var re *acp.RequestError
	if errors.As(err, &re) && re.Code == -32000 {
		var ids []string
		for _, m := range s.info.AuthMethods {
			ids = append(ids, m.ID)
		}
		msg = "the agent requires authentication; sign in with the CLI's own login"
		if len(ids) > 0 {
			msg += " (agent offers: " + strings.Join(ids, ", ") + ")"
		}
		msg += ": " + re.Message
	}
	if tail := strings.TrimSpace(s.Stderr()); tail != "" {
		if len(tail) > 600 {
			tail = tail[len(tail)-600:]
		}
		msg += "; stderr: " + tail
	}
	return fmt.Errorf("acp %s: %s", s.adapter.cfg.ID, msg)
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

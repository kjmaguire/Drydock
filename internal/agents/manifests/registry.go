package manifests

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"slices"
	"sort"
	"strings"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/headless"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/wsl"
)

// ErrNotImplemented marks adapters that exist as a manifest (detection, install guidance, gates) but cannot start sessions yet.
var ErrNotImplemented = errors.New("not implemented")

// Parser names a manifest may reference (the Go side of the manifest/Go boundary, docs/connections.md section 13).
const (
	ParserClaude      = "claude_sdkmessage"
	ParserAntigravity = "agy_stream_json"
)

// DetectFunc computes a Detection for a manifest. internal/connections provides the implementation; the registry takes it as a
// function so that package can depend on this one.
type DetectFunc func(ctx context.Context, m *Manifest) (agents.Detection, error)

// Options configure a Registry.
type Options struct {
	Set    *Set    // default: the embedded manifests
	Compat *Compat // default: the embedded compat.yaml
	Detect DetectFunc
	// Permission answers ACP permission requests for every ACP adapter; nil rejects.
	Permission acp.PermissionHandler
	// Terminal enables the client-side terminal for ACP adapters (off by default).
	Terminal bool
	// ScrubEnv starts agents (ACP agents and Claude Code) from an allowlisted environment.
	ScrubEnv bool
	// PassEnv returns extra variable names the human chose to pass to agents on top of the allowlist (read at every start).
	PassEnv func() []string
	// ClaudeExe overrides claude discovery (the "custom path" Connection setting; tests).
	ClaudeExe string
	// WSL reaches the WSL distros for adapters with the wsl run mode (Claude Code, decision P41). Nil: native only.
	WSL *wsl.Host
	// Exe overrides the executable of a headless adapter by id (Antigravity, a custom CLI): tests point them at a stand-in.
	Exe map[string]string
}

// Registry builds agents.Adapter instances from manifests.
type Registry struct {
	opts   Options
	Set    *Set
	Compat *Compat
}

// NewRegistry loads the embedded manifests and compat.yaml unless the options supply others.
func NewRegistry(o Options) (*Registry, error) {
	r := &Registry{opts: o, Set: o.Set, Compat: o.Compat}
	var err error
	if r.Set == nil {
		if r.Set, err = Embedded(); err != nil {
			return nil, err
		}
	}
	if r.Compat == nil {
		if r.Compat, err = EmbeddedCompat(); err != nil {
			return nil, err
		}
	}
	for _, id := range r.Compat.adapterIDs() {
		if _, ok := r.Set.Get(id); !ok {
			return nil, fmt.Errorf("compat.yaml lists adapter %q which has no manifest", id)
		}
	}
	return r, nil
}

func (c *Compat) adapterIDs() []string {
	ids := make([]string, 0, len(c.Adapters))
	for id := range c.Adapters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// DeclaredCapabilities maps capabilities_declared onto the normalized matrix. "unknown" and absent are false ("false means not
// verified, not impossible"); "estimate" (a client-side cost estimate) counts as reported.
func DeclaredCapabilities(m *Manifest) agents.Capabilities {
	get := func(k string) bool {
		switch v := m.Capabilities[k].(type) {
		case bool:
			return v
		case string:
			return v == "estimate"
		}
		return false
	}
	return agents.Capabilities{
		Streaming: get("streaming"), StructuredOutput: get("structured_output"), ModelSelection: get("model_selection"),
		EffortControl: get("effort_control"), SessionResume: get("session_resume"), Interrupt: get("interrupt"), MCP: get("mcp"),
		TokenUsage: get("token_usage"), CostReporting: get("cost_reporting"), Headless: get("headless"), ImageInput: get("image_input"),
		ToolCallVisibility: get("tool_call_visibility"), RunModes: append([]string(nil), m.Meta.RunModes...),
	}
}

// WSLLaunchable reports whether the adapter built for m can start its CLI inside a WSL distro. Only the Claude headless
// adapter has that path (decision P41); a manifest may declare the wsl run mode as a fact about the CLI before Drydock can use it.
func WSLLaunchable(m *Manifest) bool {
	return m.Transports.Primary == TransportHeadless && m.Transports.Headless != nil && m.Transports.Headless.Parser == ParserClaude
}

// nativeOnly is caps without the wsl run mode: for adapters that cannot start a CLI inside a distro.
func nativeOnly(caps agents.Capabilities) agents.Capabilities {
	caps.RunModes = []string{"native"}
	return caps
}

// InstallHint is the first Windows install command, shown to the user and never executed.
func (m *Manifest) InstallHint() string {
	for _, in := range m.Install {
		if in.RunMode == "" {
			return in.Shell + ": " + in.Command
		}
	}
	return ""
}

// WSLInstallHint is the first install command to run inside a WSL distro ("" when the manifest has none). Never executed.
func (m *Manifest) WSLInstallHint() string {
	for _, in := range m.Install {
		if in.RunMode == "wsl" {
			return "inside the distro: " + in.Command
		}
	}
	return ""
}

// Build returns the adapter for the manifest's primary transport.
func (r *Registry) Build(id string) (agents.Adapter, error) {
	m, ok := r.Set.Get(id)
	if !ok {
		return nil, fmt.Errorf("no manifest for adapter %q", id)
	}
	detect := r.detector(m)
	switch m.Transports.Primary {
	case TransportACP:
		return r.buildACP(m, detect)
	case TransportHeadless:
		return r.buildHeadless(m, detect)
	}
	return nil, fmt.Errorf("adapter %q: unsupported primary transport %q", id, m.Transports.Primary)
}

// BuildAll builds every adapter it can. The error map holds the ones it cannot (for example a disabled transport).
func (r *Registry) BuildAll() (map[string]agents.Adapter, map[string]error) {
	out := map[string]agents.Adapter{}
	errs := map[string]error{}
	for _, id := range r.Set.IDs() {
		a, err := r.Build(id)
		if err != nil {
			errs[id] = err
			continue
		}
		out[id] = a
	}
	return out, errs
}

func (r *Registry) detector(m *Manifest) func(ctx context.Context) (agents.Detection, error) {
	if r.opts.Detect == nil {
		return nil
	}
	return func(ctx context.Context) (agents.Detection, error) { return r.opts.Detect(ctx, m) }
}

func (r *Registry) buildACP(m *Manifest, detect func(context.Context) (agents.Detection, error)) (agents.Adapter, error) {
	a := m.Transports.ACP
	if a == nil || !m.ACPEnabled() {
		why := "disabled"
		if a != nil && a.EnableWhen != "" {
			why += " until " + a.EnableWhen
		}
		return nil, fmt.Errorf("adapter %q: the acp transport is %s", m.Meta.ID, why)
	}
	var env []string
	keys := make([]string, 0, len(a.Env))
	for k := range a.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.Contains(a.Env[k], "{{") {
			return nil, fmt.Errorf("adapter %q: env %s has an unresolved placeholder %q", m.Meta.ID, k, a.Env[k])
		}
		env = append(env, k+"="+a.Env[k])
	}
	var sb *acp.SandboxSupport
	if a.Sandbox != nil {
		sb = &acp.SandboxSupport{Mode: a.Sandbox.Mode}
	}
	return acp.New(acp.Config{
		ID: m.Meta.ID, Command: a.Argv[0], Args: append([]string(nil), a.Argv[1:]...), Env: env, ClientMeta: maps.Clone(a.ClientMeta),
		Declared: nativeOnly(DeclaredCapabilities(m)), InstallHint: m.InstallHint(), Detector: detect,
		Permission: r.opts.Permission, Terminal: r.opts.Terminal, ScrubEnv: r.opts.ScrubEnv, PassEnv: r.opts.PassEnv,
		RepoConfig: slices.Clone(m.RepoConfig), Sandbox: sb,
	}), nil
}

func (r *Registry) buildHeadless(m *Manifest, detect func(context.Context) (agents.Detection, error)) (agents.Adapter, error) {
	h := m.Transports.Headless
	switch {
	case h.Parser == ParserClaude:
		ad := claude.New()
		ad.Exe = r.opts.ClaudeExe
		ad.ScrubEnv, ad.PassEnv = r.opts.ScrubEnv, r.opts.PassEnv
		ad.UserSettingsOnly = r.opts.ScrubEnv // the same "start agents from a trimmed, trusted setup" switch
		if slices.Contains(m.Meta.RunModes, "wsl") {
			ad.WSL = r.opts.WSL
		}
		return &detectOverride{Adapter: ad, detect: detect}, nil
	case h.Parser == ParserAntigravity:
		// the documented headless mode (decisions D2, D11), unverified against a real agy; the acknowledgement gate is enforced
		// where agents are created (core.CreateAgent). Interrupt is Drydock's own: it ends agy's process tree, as the documented
		// Ctrl+C ends print mode (decision P117), so it does not wait for the manifest to declare a CLI feature.
		caps := nativeOnly(DeclaredCapabilities(m))
		caps.Interrupt = true
		return headless.NewAntigravity(headless.AntigravityConfig{
			ID: m.Meta.ID, Binaries: m.Binaries.Native, Exe: r.opts.Exe[m.Meta.ID], Args: m.HeadlessArgs,
			Caps: caps, InstallHint: m.InstallHint(), Detect: detect, ScrubEnv: r.opts.ScrubEnv, PassEnv: r.opts.PassEnv,
		}), nil
	case h.Parser == "" && h.Dialect != nil && m.Meta.ID == TemplateID:
		return &unimplemented{id: m.Meta.ID, caps: nativeOnly(DeclaredCapabilities(m)), hint: m.InstallHint(), detect: detect, m: m,
			why: "this is the template for your own CLI: add a copy with its own id and program on the Agents page (it is kept in <app data>/adapters/custom/<id>.yaml)"}, nil
	case h.Parser == "" && h.Dialect != nil:
		return r.buildCustom(m, detect)
	}
	return nil, fmt.Errorf("adapter %q: no Go parser is registered for %q", m.Meta.ID, h.Parser)
}

// buildCustom drives a person's own CLI with the generic runner (decision P115). What the runner can do follows from the
// manifest: the runner ends a turn's process to interrupt it, reads tool calls, usage and a session id only when the dialect
// maps them, and passes a model, an effort or a session id only when the argv has a place for it.
func (r *Registry) buildCustom(m *Manifest, detect func(context.Context) (agents.Detection, error)) (agents.Adapter, error) {
	h := m.Transports.Headless
	via, _ := promptVia(h.Prompt)
	vars := map[string]bool{}
	for _, v := range collectVars(h.Argv, h.Optional) {
		vars[v] = true
	}
	d := headless.Dialect{Format: h.Dialect.Format, TypeField: h.Dialect.TypeField, Map: h.Dialect.Map}
	caps := agents.Capabilities{
		Streaming: true, Headless: true, Interrupt: true, RunModes: []string{agents.RunModeNative},
		ToolCallVisibility: d.Map["tool_call"] != nil, TokenUsage: d.Map["usage"] != nil,
		SessionResume: vars["session_id"] && d.Map["session"] != nil, ModelSelection: vars["model"], EffortControl: vars["effort"],
	}
	codes := map[int]string{}
	for k, v := range h.ExitCodes {
		codes[k] = v
	}
	// the manifest's mcp section (decision P117): a flag naming the file goes on every turn's argv; a file in the working folder
	// is written by the core when it attaches the gateway
	mc, err := m.CustomMCP()
	if err != nil {
		return nil, err
	}
	var mcpArgs func(string) []string
	if mc != nil {
		caps.MCP = true
		if mc.Via == CustomMCPViaArg {
			mcpArgs = mc.ArgsFor
		}
	}
	return headless.NewCustom(headless.CustomConfig{
		ID: m.Meta.ID, Binaries: m.Binaries.Native, Exe: r.opts.Exe[m.Meta.ID], Args: m.HeadlessArgs, Prompt: via, Dialect: d,
		ExitCodes: codes, Caps: caps, InstallHint: m.InstallHint(), Detect: detect, ScrubEnv: r.opts.ScrubEnv, PassEnv: r.opts.PassEnv,
		MCPArgs: mcpArgs,
	})
}

// detectOverride routes Detect through the registry's manifest-driven detector and leaves everything else to the wrapped adapter.
type detectOverride struct {
	agents.Adapter
	detect func(context.Context) (agents.Detection, error)
}

func (d *detectOverride) Detect(ctx context.Context) (agents.Detection, error) {
	if d.detect == nil {
		return d.Adapter.Detect(ctx)
	}
	return d.detect(ctx)
}

// NativeInstalled forwards to the wrapped adapter (agents.NativeLocator): an embedded interface does not carry it.
func (d *detectOverride) NativeInstalled() bool { return agents.NativeInstalled(d.Adapter) }

// unimplemented is an adapter that can be detected and described but not started.
type unimplemented struct {
	id     string
	caps   agents.Capabilities
	hint   string
	why    string
	m      *Manifest
	detect func(context.Context) (agents.Detection, error)
}

func (u *unimplemented) ID() string                        { return u.id }
func (u *unimplemented) Capabilities() agents.Capabilities { return u.caps }

func (u *unimplemented) Detect(ctx context.Context) (agents.Detection, error) {
	if u.detect != nil {
		return u.detect(ctx)
	}
	d := agents.Detection{Adapter: u.id, Status: agents.StatusNotInstalled, InstallHint: u.hint}
	for _, b := range u.m.Binaries.Native {
		if p, err := exec.LookPath(b); err == nil {
			d.Installed, d.Path, d.Status = true, p, agents.StatusDegraded
			d.Problems = append(d.Problems, "sessions are not implemented for this adapter")
			return d, nil
		}
	}
	d.Problems = append(d.Problems, strings.Join(u.m.Binaries.Native, " or ")+" was not found on PATH")
	return d, nil
}

func (u *unimplemented) Start(context.Context, proc.Manager, agents.RunSpec) (agents.Session, error) {
	return nil, fmt.Errorf("%s: %w: %s", u.id, ErrNotImplemented, u.why)
}

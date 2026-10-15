// Package connections implements the Connections feature for agent CLIs: detection against the manifests, the status model,
// version and prerequisite checks, auth probes that never touch credentials, and "Test connection" (docs/connections.md).
// Install guidance is shown and never executed; nothing here downloads or installs anything.
package connections

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/wsl"
)

// Reason codes (docs/connections.md section 1).
const (
	ReasonPrereqMissing   = "prereq_missing"
	ReasonVersionBelowMin = "version_below_min"
	ReasonVersionBlocked  = "version_blocked"
	ReasonVersionUntested = "version_untested"
	ReasonVersionProbe    = "version_probe_failed"
	ReasonAuthProbeFalse  = "auth_probe_false"
	ReasonTransport       = "transport_fallback"
	ReasonTestFailed      = "test_failed"
	ReasonGateUnacked     = "gate_unacknowledged"
	// ReasonDrift is "drift:<kind>": an agent sent output the parser does not read (kind schema), until a Test passes.
	ReasonDrift = "drift"
	// ReasonWSL: the distro lacks something the run mode needs (interop for Drydock's tools, a known mount root).
	ReasonWSL           = "wsl_setup"
	defaultProbeTimeout = 10 * time.Second
)

// Prereq is the result of checking one prerequisite tool.
type Prereq struct {
	Tool    string `json:"tool"`
	Min     string `json:"min,omitempty"`
	Found   bool   `json:"found"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	OK      bool   `json:"ok"`
	Guide   string `json:"guide,omitempty"` // plain-language guidance, never a command
}

// GateStatus is an acknowledgement the user owes before the Connection is usable.
type GateStatus struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Acked bool   `json:"acked"`
}

// Connection is the computed, user-visible state of one agent CLI.
type Connection struct {
	agents.Detection
	Reason      string       `json:"reason,omitempty"` // machine code, optionally "code:detail"
	Warnings    []string     `json:"warnings,omitempty"`
	Gates       []GateStatus `json:"gates,omitempty"`
	Usable      bool         `json:"usable"` // status ready (or a usable degraded) and every gate acknowledged
	Prereqs     []Prereq     `json:"prereqs,omitempty"`
	Transport   string       `json:"transport,omitempty"`
	CompatClass string       `json:"compatClass,omitempty"` // best evidence class for this exact version
	Platform    string       `json:"platform"`
	// Key names this Connection: the adapter id for the native install, "<adapter>@wsl:<distro>" for an install inside a WSL
	// distro, which is a Connection of its own with its own sign-in and Test (docs/connections.md section 4b).
	Key     string    `json:"key"`
	RunMode string    `json:"runMode"`          // native | wsl
	Distro  string    `json:"distro,omitempty"` // the WSL distro (run mode wsl)
	WSL     *WSLFacts `json:"wsl,omitempty"`    // what the probe found in the distro (run mode wsl)
	// SignIn is the CLI's own sign-in command as a person types it ("claude auth login", from the manifest's auth.login), which
	// the card's Sign in button runs in a terminal (decision P50); "" when the CLI names none Drydock can start.
	SignIn string `json:"signIn,omitempty"`

	fallback string // the transport fallback checkPrereqs reported, for rate
}

// TestOutcome is the last "Test connection" result, which can degrade a Connection.
// DriftRecord is drift the core observed in an agent's live output (docs/connections.md s6): kind schema means unknown event
// types. A passing Test connection clears it.
type DriftRecord struct {
	Kind  string   `json:"kind"`
	Types []string `json:"types,omitempty"`
	At    int64    `json:"at"`
}

type TestOutcome struct {
	OK      bool
	Reason  string
	Version string // the CLI version the Test ran against; a pass clears version_untested for exactly that version
}

// Options configure detection.
type Options struct {
	PM      proc.Manager
	Env     []string // environment for probes; default os.Environ()
	Compat  *manifests.Compat
	Acked   map[string]bool        // acknowledged gate ids
	Last    map[string]TestOutcome // last Test result per adapter id
	Drift   map[string]DriftRecord // drift seen in live output since the last passing Test, per adapter id
	Timeout time.Duration          // per probe; default 10s
	// BinaryOverride replaces binary discovery per adapter id (the "custom path" Connection setting; tests).
	BinaryOverride map[string]string
	// WSL reaches the WSL distros for the WSL Connections (nil: none are listed).
	WSL *wsl.Host
	// Home is the Windows user's home, where .wslconfig says the WSL networking mode (default: the user's home folder).
	Home string

	// probeCached lets CheckWSL take a recent probe of the distro from the probe cache instead of probing again (ReadyWSL on
	// an agent's start path); the Connections page always probes.
	probeCached bool
}

// compileVersion is the manifest's version regex.
func compileVersion(m *manifests.Manifest) (*regexp.Regexp, error) {
	return regexp.Compile(m.Detect.Version.Regex)
}

func (o *Options) defaults() {
	if o.Home == "" {
		o.Home = userHome()
	}
	if o.PM == nil {
		o.PM = proc.New()
	}
	if o.Env == nil {
		o.Env = os.Environ()
	}
	if o.Timeout <= 0 {
		o.Timeout = defaultProbeTimeout
	}
}

// envBilling says, for the credential variables whose effect is known, who pays when a CLI Drydock starts finds one (warn_env
// in the manifests). Agents run headless, and a headless Claude Code uses ANTHROPIC_API_KEY when it is set, also when the
// person is signed in with a subscription (the CLI reports it as apiKeySource; measured with 2.1.296).
var envBilling = map[string]string{
	"ANTHROPIC_API_KEY":       "every agent turn is billed per token to that API key's account, not to the Claude subscription you are signed in with. Remove it from the environment to use the subscription",
	"CLAUDE_CODE_OAUTH_TOKEN": "agent turns use the subscription that token belongs to, which can be another account than the one shown as signed in",
}

// Detect computes the Detection for a manifest. It never returns an error for a missing or broken CLI: that is a status.
func Detect(ctx context.Context, m *manifests.Manifest, o Options) (agents.Detection, error) {
	return Check(ctx, m, o).Detection, nil
}

// Check computes the full Connection for a manifest.
func Check(ctx context.Context, m *manifests.Manifest, o Options) Connection {
	o.defaults()
	c := Connection{Platform: runtime.GOOS, Transport: m.Transports.Primary, Key: m.Meta.ID, RunMode: agents.RunModeNative}
	c.Adapter, c.InstallHint, c.Status = m.Meta.ID, m.InstallHint(), agents.StatusNotInstalled
	c.SignIn = signInText(m, m.Binaries.Native)

	for _, k := range m.Auth.WarnEnv {
		if envHas(o.Env, k) {
			what := fmt.Sprintf("which changes how %s authenticates and bills", m.Meta.DisplayName)
			if b, ok := envBilling[strings.ToUpper(k)]; ok {
				what = b
			}
			c.Warnings = append(c.Warnings, fmt.Sprintf("%s is set in the environment Drydock starts agents in: %s (the value is never read by Drydock)", k, what))
		}
	}
	if m.Terms != nil && m.Terms.Warning != "" {
		c.Warnings = append(c.Warnings, strings.TrimSpace(m.Terms.Warning))
	}
	if g := m.Gate; g != nil && g.Required {
		acked := o.Acked[g.ID]
		c.Gates = append(c.Gates, GateStatus{ID: g.ID, Text: strings.TrimSpace(g.Text), Acked: acked})
	}
	finish := func() Connection {
		unacked := false
		for _, g := range c.Gates {
			unacked = unacked || !g.Acked
		}
		// Degraded is usable when the agent can still start: an unverified version or a failed last Test. A missing fallback
		// transport or an unreadable version probe is not.
		usable := c.Status == agents.StatusReady ||
			c.Status == agents.StatusDegraded && (strings.HasPrefix(c.Reason, ReasonVersionUntested) || strings.HasPrefix(c.Reason, ReasonTestFailed) ||
				strings.HasPrefix(c.Reason, ReasonDrift))
		c.Usable = usable && !unacked
		if unacked && c.Reason == "" {
			c.Reason = ReasonGateUnacked
		}
		return c
	}

	// 1. resolve the binary
	path := resolveBinary(m, o)
	if path == "" {
		c.Problems = append(c.Problems, strings.Join(m.Binaries.Native, " or ")+" was not found on PATH or in the usual install locations")
		c.Reason = "not_found"
		return finish()
	}
	c.Installed, c.Path = true, path

	// 2. prerequisites
	blocked, fallback := checkPrereqs(ctx, m, o, &c)
	if blocked {
		c.Status = agents.StatusNotInstalled
		return finish()
	}
	c.fallback = fallback

	// 3. version
	ver := probeVersion(ctx, m, path, o)
	if ver.problem != "" {
		c.Problems = append(c.Problems, ver.problem)
		c.Status, c.Reason = agents.StatusDegraded, ReasonVersionProbe
		return finish()
	}
	rate(m, o, &c, ver.text, func() authState { return probeAuth(ctx, m, path, o, &c) }, nil)
	return finish()
}

// rate finishes a Connection whose binary answered with version text: the supported range and compat.yaml, then (only when
// those pass) the sign-in state auth reports, then what degrades it (drift, the last Test, a fallback transport, an untested
// version). extra lists further degrading problems found on the way (a WSL distro without interop). c.Key names the
// Connection's kept Test result.
func rate(m *manifests.Manifest, o Options, c *Connection, version string, auth func() authState, extra []string) {
	c.Version = version
	v, _ := manifests.ParseVersion(version)
	if min, ok := manifests.ParseVersion(m.Versions.Min); ok && m.Versions.Min != "" && v.Less(min) {
		c.Status, c.Reason = agents.StatusUnsupportedVersion, ReasonVersionBelowMin
		c.Problems = append(c.Problems, fmt.Sprintf("%s %s is older than the minimum supported %s (recommended %s)", m.Meta.DisplayName, version, m.Versions.Min, m.Versions.Recommended))
		return
	}
	standing := manifests.StandingUntested
	if o.Compat != nil {
		var entry *manifests.CompatEntry
		standing, entry = o.Compat.Classify(m.Meta.ID, version)
		if best, ok := o.Compat.Best(m.Meta.ID, version); ok {
			c.CompatClass = string(best.Class)
		} else if entry != nil {
			c.CompatClass = string(entry.Class)
		}
	}
	if standing == manifests.StandingBlocked {
		c.Status, c.Reason = agents.StatusUnsupportedVersion, ReasonVersionBlocked
		c.Problems = append(c.Problems, fmt.Sprintf("%s %s is on the blocked list in compat.yaml", m.Meta.DisplayName, version))
		return
	}

	// 4. authentication (only after the cheap checks pass)
	if st := auth(); st == authNo {
		c.Status, c.Reason = agents.StatusUnauthenticated, ReasonAuthProbeFalse
		if c.RunMode == agents.RunModeWSL {
			msg := "not signed in inside the distro: open a terminal in it (wsl -d " + c.Distro + ") and run `claude auth login`; this sign-in is the distro's own, not the Windows one"
			if c.AuthDetail != "" {
				msg += ". What Drydock saw: " + c.AuthDetail
			}
			c.Problems = append(c.Problems, msg)
		} else {
			c.Problems = append(c.Problems, "not signed in: run the CLI's own login in the embedded terminal")
		}
		return
	}

	// 5. degraded conditions
	key := c.Key
	if key == "" {
		key = m.Meta.ID
	}
	var degraded []string
	fallback := c.fallback
	last, haveLast := o.Last[key]
	drift, haveDrift := o.Drift[m.Meta.ID]
	switch {
	case haveDrift:
		c.Reason = ReasonDrift + ":" + drift.Kind
		degraded = append(degraded, fmt.Sprintf("%s sent output Drydock does not read (event types: %s); turns may lose results. Check the contract suite, then run Test connection",
			m.Meta.DisplayName, strings.Join(drift.Types, ", ")))
	case haveLast && !last.OK:
		c.Reason = ReasonTestFailed + ":" + last.Reason
		degraded = append(degraded, "the last Test connection failed: "+last.Reason)
	case fallback != "":
		c.Reason = ReasonTransport + ":" + fallback
		degraded = append(degraded, fallback)
	case len(extra) > 0:
		c.Reason = ReasonWSL
	case standing == manifests.StandingUntested && !(haveLast && last.OK && last.Version == version):
		c.Reason = ReasonVersionUntested
		degraded = append(degraded, fmt.Sprintf("%s %s has not been verified by Drydock yet; run Test connection", m.Meta.DisplayName, version))
	}
	degraded = append(degraded, extra...)
	if len(degraded) > 0 {
		c.Status = agents.StatusDegraded
		c.Problems = append(c.Problems, degraded...)
		return
	}
	c.Status = agents.StatusReady
}

func envHas(env []string, key string) bool {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) && v != "" {
			return true
		}
	}
	return false
}

// resolveBinary finds the executable: the override, then each manifest name through PATH (and PATHEXT on Windows, which
// resolves .cmd shims such as npx.cmd and gemini.cmd), then the secondary hint directories.
func resolveBinary(m *manifests.Manifest, o Options) string {
	if p := o.BinaryOverride[m.Meta.ID]; p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
		return ""
	}
	names := candidateNames(m.Binaries.Native)
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	home, _ := os.UserHomeDir()
	for _, h := range m.Binaries.Hints {
		dir := h
		if strings.HasPrefix(dir, "~") && home != "" {
			dir = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(dir, "~"), "/"))
		}
		for _, n := range names {
			p := filepath.Join(dir, n)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	return ""
}

// candidateNames lists the names to try: as written, then without a Windows extension, so a manifest written for Windows
// (claude.exe, gemini.cmd) also finds `claude` and `gemini` on a development host.
func candidateNames(native []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, n := range native {
		add(n)
	}
	for _, n := range native {
		switch strings.ToLower(filepath.Ext(n)) {
		case ".exe", ".cmd", ".bat", ".com":
			add(strings.TrimSuffix(n, filepath.Ext(n)))
		}
	}
	return out
}

type versionResult struct {
	text    string
	problem string
}

func probeVersion(ctx context.Context, m *manifests.Manifest, path string, o Options) versionResult {
	res := runProbe(ctx, o.PM, path, m.Detect.Version.Argv, o.Env, o.Timeout)
	if res.TimedOut {
		return versionResult{problem: fmt.Sprintf("%s %s did not answer within %s", filepath.Base(path), strings.Join(m.Detect.Version.Argv, " "), o.Timeout)}
	}
	if res.Err != nil && res.Exit == -1 {
		return versionResult{problem: fmt.Sprintf("could not run %s: %v", path, res.Err)}
	}
	re := regexp.MustCompile(m.Detect.Version.Regex)
	for _, out := range []string{res.Stdout, res.Stderr} {
		for _, line := range strings.Split(stripANSI(out), "\n") {
			if mm := re.FindStringSubmatch(strings.TrimSpace(line)); mm != nil {
				return versionResult{text: mm[1]}
			}
		}
	}
	return versionResult{problem: fmt.Sprintf("%s %s printed no version matching the manifest (exit %d)", filepath.Base(path), strings.Join(m.Detect.Version.Argv, " "), res.Exit)}
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

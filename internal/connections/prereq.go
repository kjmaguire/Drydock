package connections

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/kjmaguire/drydock/internal/agents/manifests"
)

// toolGuide is plain-language guidance for a missing prerequisite. It never contains an installer command (no verified one
// exists in the evidence, docs/connections.md section 10), and Drydock never installs anything.
var toolGuide = map[string]string{
	"node": "Node %s or newer is required: install it from nodejs.org or your package manager, then re-check.",
	"npx":  "npx is part of Node: install Node from nodejs.org or your package manager, then re-check.",
	"npm":  "npm is part of Node: install Node from nodejs.org or your package manager, then re-check.",
	"git":  "Git is required: install Git for Windows from git-scm.com or your package manager, then re-check.",
	"gh":   "GitHub CLI is required: install it from cli.github.com or your package manager, then re-check.",
}

// checkTool looks a tool up on PATH (PATHEXT resolves npx.cmd on Windows) and, for versioned tools, runs `--version`.
func checkTool(ctx context.Context, tool, min string, o Options) Prereq {
	pr := Prereq{Tool: tool, Min: min}
	guide := toolGuide[tool]
	if strings.Contains(guide, "%s") {
		guide = fmt.Sprintf(guide, min)
	}
	p, err := exec.LookPath(tool)
	if err != nil {
		pr.Guide = guide
		return pr
	}
	pr.Found, pr.Path = true, p
	if tool == "npx" {
		pr.OK = true // presence is the check; npx follows Node's version
		return pr
	}
	res := runProbe(ctx, o.PM, p, []string{"--version"}, o.Env, o.Timeout)
	if res.TimedOut || res.Exit != 0 {
		pr.Guide = guide
		return pr
	}
	if v, ok := manifests.ParseVersion(res.Stdout); ok {
		pr.Version = v.String()
		if mv, ok := manifests.ParseVersion(min); min != "" && ok && v.Less(mv) {
			pr.Guide = guide
			return pr
		}
		pr.OK = true
		return pr
	}
	pr.Guide = guide
	return pr
}

var pinnedBridgeCmd = regexp.MustCompile(`(?i)^npx(\.cmd)?$`)

// transportNeeds lists the tools a transport requires, including npx when it launches a bridge through it.
func transportNeeds(argv []string, reqs []manifests.Requirement) []manifests.Requirement {
	out := append([]manifests.Requirement(nil), reqs...)
	if len(argv) > 0 && pinnedBridgeCmd.MatchString(argv[0]) {
		out = append(out, manifests.Requirement{Tool: "npx"})
	}
	return out
}

// checkPrereqs evaluates prerequisites (docs/connections.md section 3 rule 2 and section 10). It returns blocked when a
// prerequisite needed by every usable transport is missing, and a fallback description when only the primary transport is
// blocked and another transport exists.
func checkPrereqs(ctx context.Context, m *manifests.Manifest, o Options, c *Connection) (blocked bool, fallback string) {
	cache := map[string]Prereq{}
	check := func(r manifests.Requirement) Prereq {
		key := r.Tool + "@" + r.Min
		if p, ok := cache[key]; ok {
			return p
		}
		p := checkTool(ctx, r.Tool, r.Min, o)
		cache[key] = p
		c.Prereqs = append(c.Prereqs, p)
		return p
	}
	addMissing := func(p Prereq) {
		msg := p.Guide
		if msg == "" {
			msg = fmt.Sprintf("%s is required", p.Tool)
		}
		if p.Found && p.Version != "" {
			msg = fmt.Sprintf("%s (found %s)", msg, p.Version)
		}
		c.Problems = append(c.Problems, msg)
	}
	// requirements of every transport
	for _, r := range m.Requires {
		if p := check(r); !p.OK {
			c.Reason = ReasonPrereqMissing + ":" + r.Tool
			addMissing(p)
			blocked = true
		}
	}
	if blocked {
		return true, ""
	}
	// requirements of the primary transport only
	var needs []manifests.Requirement
	var alt bool
	switch m.Transports.Primary {
	case manifests.TransportACP:
		if a := m.Transports.ACP; a != nil {
			needs = transportNeeds(a.Argv, a.Requires)
		}
		alt = m.Transports.Headless != nil
	case manifests.TransportHeadless:
		alt = m.Transports.ACP != nil && m.ACPEnabled() // a headless primary has no tool prerequisites of its own
	}
	var missing []Prereq
	for _, r := range needs {
		if p := check(r); !p.OK {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return false, ""
	}
	for _, p := range missing {
		addMissing(p)
	}
	if alt {
		fallback = fmt.Sprintf("the primary %s transport needs %s; only the %s fallback is available", m.Transports.Primary, missing[0].Tool, otherTransport(m.Transports.Primary))
		if h := m.Transports.Headless; h != nil && h.Unverified && m.Transports.Primary == manifests.TransportACP {
			c.Problems = append(c.Problems, "the headless fallback is declared but not implemented yet, so sessions cannot start until the prerequisite is installed")
		}
		return false, fallback
	}
	c.Reason = ReasonPrereqMissing + ":" + missing[0].Tool
	return true, ""
}

func otherTransport(t string) string {
	if t == manifests.TransportACP {
		return manifests.TransportHeadless
	}
	return manifests.TransportACP
}

type authState int

const (
	authUnknown authState = iota
	authYes
	authNo
)

// probeAuth runs the manifest's non-interactive auth probe, if there is one, and records the answer on c. It reads only the
// CLI's own answer (its exit code or JSON); it never opens a credential file. A probe that cannot answer leaves Authenticated
// nil ("unknown") rather than guessing.
func probeAuth(ctx context.Context, m *manifests.Manifest, path string, o Options, c *Connection) authState {
	pr := m.Auth.Probe
	if pr == nil {
		c.AuthDetail = "this CLI has no non-interactive auth probe; sign-in is confirmed by Test connection"
		return authUnknown
	}
	if pr.Unverified {
		c.AuthDetail = "probe output is unverified: "
	}
	argv := pr.Argv
	bin := path
	res := runProbe(ctx, o.PM, bin, argv, o.Env, o.Timeout*2)
	if res.TimedOut || (res.Err != nil && res.Exit == -1) {
		c.AuthDetail += "the auth probe did not complete"
		return authUnknown
	}
	set := func(v bool, detail string) authState {
		c.Authenticated = &v
		c.AuthDetail += detail
		if v {
			return authYes
		}
		return authNo
	}
	switch pr.Parse {
	case "json":
		cond, err := manifests.ParseCondition(pr.ReadyWhen)
		if err != nil {
			c.AuthDetail += "bad probe condition"
			return authUnknown
		}
		if ok, known := cond.EvalJSON([]byte(strings.TrimSpace(res.Stdout))); known {
			detail := jsonField(res.Stdout, "authMethod")
			if ok {
				c.Account = jsonField(res.Stdout, "email")
				return set(true, detail)
			}
			return set(false, "the CLI reports it is not signed in")
		}
		c.AuthDetail += fmt.Sprintf("the probe exited %d without usable JSON; sign-in is confirmed by Test connection", res.Exit)
		return authUnknown
	case "exit_code":
		if cond, err := manifests.ParseCondition(pr.ReadyWhen); err == nil {
			if ok, _ := cond.EvalExit(res.Exit); ok {
				return set(true, "")
			}
		}
		if pr.NotReadyWhen != "" {
			if cond, err := manifests.ParseCondition(pr.NotReadyWhen); err == nil {
				if ok, _ := cond.EvalExit(res.Exit); ok {
					return set(false, strings.TrimSpace(firstLine(res.Stdout+res.Stderr)))
				}
			}
		}
		c.AuthDetail += fmt.Sprintf("the probe exited %d, which the manifest does not classify", res.Exit)
		return authUnknown
	case "text_providers":
		return parseProviders(res, c)
	}
	return authUnknown
}

var credCountRe = regexp.MustCompile(`(\d+)\s+credentials?`)

// parseProviders reads `opencode providers list`: "N credentials" after the header. A positive count means a provider is
// signed in; zero is unknown, not unauthenticated, because OpenCode ships free models that work without any credential.
func parseProviders(res runResult, c *Connection) authState {
	if res.Exit != 0 {
		c.AuthDetail += fmt.Sprintf("providers list exited %d", res.Exit)
		return authUnknown
	}
	m := credCountRe.FindStringSubmatch(stripANSI(res.Stdout))
	if m == nil {
		c.AuthDetail += "providers list printed no credential count"
		return authUnknown
	}
	if m[1] == "0" {
		c.AuthDetail += "no provider credentials configured (free models may still work)"
		return authUnknown
	}
	t := true
	c.Authenticated = &t
	c.AuthDetail += m[1] + " provider credential(s) configured"
	return authYes
}

func jsonField(doc, key string) string {
	re := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `"\s*:\s*"([^"]*)"`)
	if m := re.FindStringSubmatch(doc); m != nil {
		return m[1]
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(stripANSI(s))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

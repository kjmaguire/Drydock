package connections

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/wsl"
)

// WSL Connections (decision P41, docs/connections.md section 4b): a CLI installed inside a WSL distro is a Connection of its
// own, one per distro, with its own sign-in (the distro's, never the Windows one) and its own Test. Drydock never reads, copies
// or moves a credential between the two (D1): the sign-in state is what the CLI's own status command prints inside the distro.

// WSLFacts is what a WSL Connection shows about its distro, in plain words where a person acts on them.
type WSLFacts struct {
	Distro    string   `json:"distro"`
	Default   bool     `json:"default,omitempty"`
	WSLVer    int      `json:"wslVersion,omitempty"` // 1 or 2 as wsl.exe -l -v reports it (0 unknown)
	WSL2      bool     `json:"wsl2"`                 // the probe found a WSL 2 kernel
	Kernel    string   `json:"kernel,omitempty"`
	Interop   bool     `json:"interop"`
	NetMode   string   `json:"netMode,omitempty"` // nat, mirrored, ... ("" not reported)
	MountRoot string   `json:"mountRoot,omitempty"`
	Home      string   `json:"home,omitempty"`
	Setsid    bool     `json:"setsid"`
	Git       string   `json:"git,omitempty"`
	Notes     []string `json:"notes,omitempty"`
}

// WSLKey is the Connection key of adapter id's install inside distro.
func WSLKey(id, distro string) string { return id + "@wsl:" + distro }

// ParseWSLKey splits a WSL Connection key; ok is false for a native key.
func ParseWSLKey(key string) (id, distro string, ok bool) {
	id, distro, ok = strings.Cut(key, "@wsl:")
	return id, distro, ok && distro != ""
}

// hasWSL reports whether Drydock can start the manifest's CLI inside a distro: it declares the wsl run mode with a binary
// for it, and its adapter has the launch path (manifests.WSLLaunchable). OpenCode declares WSL too, but its ACP adapter has no
// WSL start yet, so it gets no WSL Connection.
func hasWSL(m *manifests.Manifest) bool {
	return slices.Contains(m.Meta.RunModes, agents.RunModeWSL) && len(m.Binaries.WSL) > 0 && manifests.WSLLaunchable(m)
}

// CheckWSL computes the Connection of m's install inside distro d, probing it through the person's login shell.
func CheckWSL(ctx context.Context, m *manifests.Manifest, d wsl.Distro, o Options) Connection {
	o.defaults()
	c := Connection{Transport: m.Transports.Primary, Key: WSLKey(m.Meta.ID, d.Name), RunMode: agents.RunModeWSL, Distro: d.Name, Platform: "linux (WSL)"}
	c.Adapter, c.InstallHint, c.Status = m.Meta.ID, m.WSLInstallHint(), agents.StatusNotInstalled
	c.SignIn = signInText(m, m.Binaries.WSL)
	for _, k := range m.Auth.WarnEnv {
		if envHas(o.Env, k) {
			c.Warnings = append(c.Warnings, fmt.Sprintf("%s is set in the Windows environment Drydock starts agents in, and it is passed into the distro too: %s (the value is never read by Drydock)", k, envBillingText(k, m)))
		}
	}
	c.Gates = append(c.Gates, gates(m, o)...)
	finish := func() Connection {
		unacked := false
		for _, g := range c.Gates {
			unacked = unacked || !g.Acked
		}
		usable := c.Status == agents.StatusReady || c.Status == agents.StatusDegraded &&
			(strings.HasPrefix(c.Reason, ReasonVersionUntested) || strings.HasPrefix(c.Reason, ReasonTestFailed) || strings.HasPrefix(c.Reason, ReasonDrift))
		c.Usable = usable && !unacked
		if unacked && c.Reason == "" {
			c.Reason = ReasonGateUnacked
		}
		return c
	}
	if o.WSL == nil {
		c.Problems = append(c.Problems, "WSL is not available in this build")
		c.Reason = "wsl_unavailable"
		return finish()
	}
	var authArgs []string
	if pr := m.Auth.Probe; pr != nil {
		authArgs = pr.Argv
	}
	in, err := o.WSL.Probe(ctx, d.Name, m.Binaries.WSL[0], m.Detect.Version.Argv, authArgs, true)
	f := &WSLFacts{Distro: d.Name, Default: d.Default, WSLVer: d.Version, WSL2: in.WSL2, Kernel: in.Kernel, Interop: in.Interop, NetMode: in.NetMode,
		MountRoot: in.Paths.MountRoot, Home: in.Home, Setsid: in.Setsid, Git: in.Git}
	if f.NetMode == "" && o.Home != "" {
		f.NetMode = wsl.NetworkingMode(o.Home)
	}
	c.WSL = f
	if in.WSL2 || d.Version == 2 {
		c.Platform = "linux (WSL 2)"
	} else if d.Version == 1 {
		c.Platform = "linux (WSL 1)"
	}
	if err != nil {
		c.Problems = append(c.Problems, err.Error())
		c.Reason = "wsl_probe_failed"
		return finish()
	}
	f.Notes = wslNotes(in, f.NetMode)
	if in.Tool == "" {
		c.Problems = append(c.Problems, fmt.Sprintf("%s was not found in the distro %s (a login shell's PATH, ~/.local/bin and ~/.claude/local were searched)", strings.Join(m.Binaries.WSL, " or "), d.Name))
		c.Reason = "not_found"
		return finish()
	}
	c.Installed, c.Path = true, in.Tool
	ver := ""
	if re, rerr := compileVersion(m); rerr == nil {
		if mm := re.FindStringSubmatch(strings.TrimSpace(stripANSI(in.Version))); mm != nil {
			ver = mm[1]
		}
	}
	if ver == "" {
		c.Problems = append(c.Problems, fmt.Sprintf("%s %s inside %s printed no version matching the manifest", in.Tool, strings.Join(m.Detect.Version.Argv, " "), d.Name))
		c.Status, c.Reason = agents.StatusDegraded, ReasonVersionProbe
		return finish()
	}
	var extra []string
	if !in.Interop {
		extra = append(extra, "Windows interop is off in this distro, so Drydock's tools (its MCP gateway, reached through a Windows program the distro starts) cannot reach an agent here: turn it on with [interop] enabled=true in /etc/wsl.conf, then wsl --terminate "+d.Name)
	}
	if in.Paths.MountRoot == "" {
		extra = append(extra, "the distro does not say where it mounts Windows drives (wslpath failed and /etc/wsl.conf names no [automount] root), so no Windows folder can be handed to the agent")
	}
	rate(m, o, &c, ver, func() authState { return wslAuth(m, in, &c) }, extra)
	return finish()
}

// wslNotes are the plain facts a person needs about running agents in this distro.
func wslNotes(in wsl.Info, netMode string) []string {
	var n []string
	switch {
	case netMode == "mirrored":
		n = append(n, "Networking is mirrored: the distro shares Windows' localhost. Drydock's tools do not depend on it (they go through a Windows program the distro starts).")
	case netMode != "":
		n = append(n, "Networking is "+netMode+": the distro cannot reach Windows' localhost directly. Drydock's tools still work: they go through a Windows program the distro starts (interop), not through the network.")
	}
	if !in.Setsid {
		n = append(n, "setsid is missing in the distro: stopping an agent still kills every process it started (they carry a marker), but its process group is not its own.")
	}
	n = append(n, "A repository on a Windows drive is read across the WSL file share (/mnt/...), which is slower; a repository inside the distro is fast for the agent, and its worktrees are made inside the distro too.")
	return n
}

// wslAuth reads the sign-in state from the probe's run of the manifest's auth probe inside the distro.
func wslAuth(m *manifests.Manifest, in wsl.Info, c *Connection) authState {
	pr := m.Auth.Probe
	if pr == nil {
		c.AuthDetail = "this CLI has no non-interactive auth probe; sign-in is confirmed by Test connection"
		return authUnknown
	}
	if in.AuthExit < 0 {
		c.AuthDetail = "the auth probe did not run inside the distro"
		return authUnknown
	}
	res := runResult{Stdout: in.Auth, Exit: in.AuthExit}
	switch pr.Parse {
	case "json":
		cond, err := manifests.ParseCondition(pr.ReadyWhen)
		if err != nil {
			c.AuthDetail = "bad probe condition"
			return authUnknown
		}
		if ok, known := cond.EvalJSON([]byte(strings.TrimSpace(res.Stdout))); known {
			v := ok
			c.Authenticated = &v
			if ok {
				c.AuthDetail, c.Account = jsonField(res.Stdout, "authMethod"), jsonField(res.Stdout, "email")
				return authYes
			}
			c.AuthDetail = "`" + authCmd(in, pr.Argv) + "` answered " + firstNonBlank(wsl.Excerpt(res.Stdout), "that it is not signed in") + checkedAs(in)
			return authNo
		}
		if res.Exit > 0 {
			f := false
			c.Authenticated = &f
			c.AuthDetail = fmt.Sprintf("`%s` exited %d without its JSON answer", authCmd(in, pr.Argv), res.Exit)
			if in.AuthErr != "" {
				c.AuthDetail += ": " + in.AuthErr
			}
			c.AuthDetail += checkedAs(in)
			return authNo
		}
		c.AuthDetail = "the auth probe printed no usable JSON; sign-in is confirmed by Test connection"
		return authUnknown
	case "exit_code":
		if cond, err := manifests.ParseCondition(pr.ReadyWhen); err == nil {
			if ok, _ := cond.EvalExit(res.Exit); ok {
				t := true
				c.Authenticated = &t
				return authYes
			}
		}
	}
	return authUnknown
}

// authCmd is the sign-in check as it ran inside the distro.
func authCmd(in wsl.Info, argv []string) string {
	return strings.Join(append([]string{in.Tool}, argv...), " ")
}

// checkedAs names the distro user and HOME the sign-in check ran as: a sign-in made as another user (root, say) is not this
// one's.
func checkedAs(in wsl.Info) string {
	if in.User == "" && in.Home == "" {
		return ""
	}
	return fmt.Sprintf(" (checked as the distro's default user %s, HOME %s)", firstNonBlank(in.User, "?"), firstNonBlank(in.Home, "?"))
}

func firstNonBlank(s ...string) string {
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// gates are the manifest's acknowledgement gates with their state.
func gates(m *manifests.Manifest, o Options) []GateStatus {
	if g := m.Gate; g != nil && g.Required {
		return []GateStatus{{ID: g.ID, Text: strings.TrimSpace(g.Text), Acked: o.Acked[g.ID]}}
	}
	return nil
}

func envBillingText(k string, m *manifests.Manifest) string {
	if b, ok := envBilling[strings.ToUpper(k)]; ok {
		return b
	}
	return fmt.Sprintf("which changes how %s authenticates and bills", m.Meta.DisplayName)
}

// DetectWSL lists the WSL Connections: one per user distro (not a container engine's own) for every manifest with the wsl
// run mode. Without wsl.exe, or with no distro, there are none. Distros are probed in parallel.
func (s *Service) DetectWSL(ctx context.Context) []Connection {
	o := s.options()
	if o.WSL == nil {
		return nil
	}
	if _, err := o.WSL.Path(); err != nil {
		return nil
	}
	var ms []*manifests.Manifest
	for _, id := range s.Registry.Set.IDs() {
		if m, ok := s.Registry.Set.Get(id); ok && hasWSL(m) {
			ms = append(ms, m)
		}
	}
	if len(ms) == 0 {
		return nil
	}
	ds, _, err := o.WSL.Distros(ctx)
	if err != nil {
		return nil
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var out []Connection
	for _, m := range ms {
		for _, d := range ds {
			if !wsl.UserDistro(d.Name) {
				continue
			}
			wg.Add(1)
			go func(m *manifests.Manifest, d wsl.Distro) {
				defer wg.Done()
				c := CheckWSL(ctx, m, d, o)
				mu.Lock()
				out = append(out, c)
				mu.Unlock()
			}(m, d)
		}
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// CheckKey computes one Connection by its key (native or WSL).
func (s *Service) CheckKey(ctx context.Context, key string) (Connection, error) {
	id, distro, isWSL := ParseWSLKey(key)
	if !isWSL {
		return s.Check(ctx, key)
	}
	return s.CheckWSL(ctx, id, distro)
}

// CheckWSL computes the Connection of adapter id's install inside distro.
func (s *Service) CheckWSL(ctx context.Context, id, distro string) (Connection, error) {
	m, ok := s.Registry.Set.Get(id)
	if !ok {
		return Connection{}, fmt.Errorf("no manifest for adapter %q", id)
	}
	if !hasWSL(m) {
		return Connection{}, fmt.Errorf("%s has no WSL run mode", m.Meta.DisplayName)
	}
	o := s.options()
	d := wsl.Distro{Name: distro}
	if o.WSL != nil {
		if ds, _, err := o.WSL.Distros(ctx); err == nil {
			found := false
			for _, x := range ds {
				if strings.EqualFold(x.Name, distro) {
					d, found = x, true
				}
			}
			if !found {
				return Connection{}, fmt.Errorf("there is no WSL distro named %s on this machine", distro)
			}
		}
	}
	return CheckWSL(ctx, m, d, o), nil
}

func userHome() string {
	h, _ := os.UserHomeDir()
	return h
}

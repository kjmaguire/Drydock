package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/chats"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/wsl"
)

// The WSL run mode (decision P41): where an agent's CLI runs. An agent says "native", "wsl" with a distro, or nothing, which
// follows its project's default (native unless a person changed it). The sandbox (P30) and WSL are refused together: the
// sandbox's container runs the commands of an agent on Windows, and a CLI inside a distro would run its own on Linux.

func init() {
	RegisterExtension(Extension{Name: "wsl", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"wsl.distros": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.WSLDistros(ctx), nil }),
			"project.runMode": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
			}) (any, error) {
				return c.projectRunMode(ctx, p.ProjectID), nil
			}),
			"project.runModes": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.ProjectRunModes(ctx) }),
			"project.setRunMode": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
				RunMode   string `json:"runMode"`
				WSLDistro string `json:"wslDistro"`
			}) (any, error) {
				return c.SetProjectRunMode(ctx, p.ProjectID, p.RunMode, p.WSLDistro, c.Human())
			}),
		}, nil, nil
	}})
}

// settingRunMode + project id holds a project's default run mode.
const settingRunMode = "runmode:"

// ProjectRunMode is a project's default run mode for its agents, and who set it.
type ProjectRunMode struct {
	RunMode   string `json:"runMode"` // native | wsl
	WSLDistro string `json:"wslDistro,omitempty"`
	By        string `json:"by,omitempty"` // the person who set it, or "drydock" for a new project's default (decision P58)
	At        int64  `json:"at,omitempty"` // unix milliseconds
	// Why says why Drydock chose it (By "drydock"): the CLI is not usable on Windows and is ready in the distro.
	Why string `json:"why,omitempty"`
}

func (c *Core) projectRunMode(ctx context.Context, projectID string) ProjectRunMode {
	var raw string
	def := ProjectRunMode{RunMode: agents.RunModeNative}
	if projectID == "" || c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingRunMode+projectID).Scan(&raw) != nil {
		return def
	}
	var p ProjectRunMode
	if json.Unmarshal([]byte(raw), &p) != nil || (p.RunMode != agents.RunModeNative && p.RunMode != agents.RunModeWSL) {
		return def
	}
	return p
}

// ProjectRunModes is every project's default run mode, by project id. The Agents page's "Used by" column asks for all of
// them in one call instead of one call per project on every load. A workspace without a project answers an empty map, never
// a null.
func (c *Core) ProjectRunModes(ctx context.Context) (map[string]ProjectRunMode, error) {
	ps, err := c.Projects(ctx)
	if err != nil {
		return nil, err
	}
	modes := make(map[string]ProjectRunMode, len(ps))
	for _, p := range ps {
		modes[p.ID] = c.projectRunMode(ctx, p.ID)
	}
	return modes, nil
}

// SetProjectRunMode sets the run mode of the project's agents that do not choose their own. Only a human identity may.
func (c *Core) SetProjectRunMode(ctx context.Context, projectID, mode, distro, by string) (ProjectRunMode, error) {
	if !strings.HasPrefix(by, "human:") || len(by) == len("human:") {
		return ProjectRunMode{}, errors.New("only a person may change where a project's agents run")
	}
	if _, err := c.GetProject(ctx, projectID); err != nil {
		return ProjectRunMode{}, err
	}
	if mode == "" {
		mode = agents.RunModeNative
	}
	m, d, err := c.checkRunMode(ctx, projectID, "", mode, distro)
	if err != nil {
		return ProjectRunMode{}, err
	}
	p := ProjectRunMode{RunMode: m, WSLDistro: d, By: by, At: time.Now().UnixMilli()}
	if err := c.storeRunMode(ctx, projectID, p); err != nil {
		return ProjectRunMode{}, err
	}
	c.publish(ctx, "project.run_mode", projectID, map[string]any{"runMode": p.RunMode, "wslDistro": p.WSLDistro, "by": by})
	return p, nil
}

// checkRunMode validates a run mode for an agent of adapter (adapter "" checks a project default, which only Claude Code
// agents use in WSL) and returns it normalized: "" and native carry no distro; wsl needs an adapter that can start there, a
// distro this machine has, and a project without the sandbox.
func (c *Core) checkRunMode(ctx context.Context, projectID, adapter, mode, distro string) (string, string, error) {
	mode, distro = strings.TrimSpace(mode), strings.TrimSpace(distro)
	switch mode {
	case "", agents.RunModeNative:
		return mode, "", nil
	case agents.RunModeWSL:
	default:
		return "", "", fmt.Errorf("an agent runs natively or in WSL, not %q", mode)
	}
	if adapter != "" {
		c.mu.Lock()
		ad, ok := c.adapters[adapter]
		c.mu.Unlock()
		if !ok || !slices.Contains(ad.Capabilities().RunModes, agents.RunModeWSL) {
			return "", "", fmt.Errorf("%s cannot run in WSL on this machine (only Claude Code can, where WSL is installed)", adapter)
		}
	} else if c.wsl == nil {
		return "", "", errors.New("WSL is not available on this machine")
	}
	if distro == "" {
		return "", "", errors.New("choose the WSL distro the agent runs in")
	}
	if c.wsl == nil {
		return "", "", errors.New("WSL is not available on this machine")
	}
	ds, msg, err := c.wsl.Distros(ctx)
	if err != nil {
		return "", "", fmt.Errorf("listing the WSL distros: %w", err)
	}
	found := ""
	for _, d := range ds {
		if strings.EqualFold(d.Name, distro) && wsl.UserDistro(d.Name) {
			found = d.Name
		}
	}
	if found == "" {
		if msg != "" {
			return "", "", fmt.Errorf("there is no WSL distro named %s (%s)", distro, msg)
		}
		return "", "", fmt.Errorf("there is no WSL distro named %s on this machine", distro)
	}
	if projectID != "" && c.sandboxSwitch(ctx, projectID).Enabled {
		return "", "", errors.New(sandboxWSLRefusal)
	}
	return mode, found, nil
}

// sandboxWSLRefusal is why the sandbox and the WSL run mode are not combined (decision P41).
const sandboxWSLRefusal = "this project runs its agents' commands in a sandbox (a container per task), which needs the agent to run on Windows: an agent inside WSL would run its commands there instead. Run this project's agents natively, or turn the sandbox off in the project settings"

// effectiveRunMode is where an agent's CLI runs: its own choice, else its project's default (native or wsl).
func (c *Core) effectiveRunMode(ctx context.Context, a Agent) (string, string) {
	switch a.RunMode {
	case agents.RunModeNative:
		return agents.RunModeNative, ""
	case agents.RunModeWSL:
		return agents.RunModeWSL, a.WSLDistro
	}
	p := c.projectRunMode(ctx, a.ProjectID)
	if p.RunMode == agents.RunModeWSL {
		c.mu.Lock()
		ad, ok := c.adapters[a.Adapter]
		c.mu.Unlock()
		if ok && slices.Contains(ad.Capabilities().RunModes, agents.RunModeWSL) {
			return agents.RunModeWSL, p.WSLDistro
		}
	}
	return agents.RunModeNative, ""
}

// agentRunMode is Runtime.RunMode: an unknown agent runs natively. A chat's turn says its own run mode in its spec (a chat
// chosen to run in a WSL distro), so this is not asked for it. why is set when Drydock chose the place (runPlacement).
func (c *Core) agentRunMode(ctx context.Context, agentID string) (string, string, string) {
	a, err := c.agent(ctx, agentID)
	if err != nil {
		return agents.RunModeNative, "", ""
	}
	return c.runPlacement(ctx, a)
}

// projectHasWSLAgents names the project's agents that run in WSL (their own choice or the project's default).
func (c *Core) projectWSLAgents(ctx context.Context, projectID string) []string {
	ags, err := c.Agents(ctx, projectID)
	if err != nil {
		return nil
	}
	var names []string
	for _, a := range ags {
		if m, _ := c.effectiveRunMode(ctx, a); m == agents.RunModeWSL {
			names = append(names, a.Name)
		}
	}
	return names
}

// wslPaths is the path mapping of distro, from the last probe of it (probing it now when there is none).
func (c *Core) wslPaths(ctx context.Context, distro string) (wsl.Paths, bool) {
	in, err := c.wslInfo(ctx, distro)
	if err != nil || in.Paths.MountRoot == "" {
		return wsl.Paths{}, false
	}
	return in.Paths, true
}

// wslInfo is the last probe of distro (probing it now when there is none).
func (c *Core) wslInfo(ctx context.Context, distro string) (wsl.Info, error) {
	if c.wsl == nil || distro == "" {
		return wsl.Info{}, errors.New("WSL is not available")
	}
	return c.wsl.Probe(ctx, distro, "claude", claude.WSLVersionArgs, nil, false)
}

// openRepo opens a project's repository. One inside a WSL distro (\\wsl.localhost\<distro>\..., \\wsl$\...) runs every git
// call with the distro's own git (wsl.GitRunner), so its worktrees, made next to it inside the distro, link with Linux paths
// that git inside the distro can follow (decision P41).
func (c *Core) openRepo(ctx context.Context, path string) (*git.Repo, error) {
	run, err := c.repoRunner(ctx, path)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return git.Open(ctx, path)
	}
	return git.OpenIn(ctx, path, run)
}

// repoRunner is the git a repository at path is driven by: nil (this machine's git) for a Windows folder, the distro's own
// git for one inside a WSL distro (decision P41).
func (c *Core) repoRunner(ctx context.Context, path string) (git.Runner, error) {
	distro, _, ok := wsl.ParseUNC(path)
	if !ok {
		return nil, nil
	}
	if c.wsl == nil {
		return nil, fmt.Errorf("the repository is inside the WSL distro %s, and WSL is not available here", distro)
	}
	exe, err := c.wsl.Path()
	if err != nil {
		return nil, fmt.Errorf("the repository is inside the WSL distro %s: %w", distro, err)
	}
	in, err := c.wslInfo(ctx, distro)
	if err != nil {
		return nil, fmt.Errorf("the repository is inside the WSL distro %s: %w", distro, err)
	}
	if in.Git == "" {
		return nil, fmt.Errorf("the repository is inside the WSL distro %s, which has no git: install git there (for example sudo apt install git)", distro)
	}
	if in.Paths.MountRoot == "" {
		return nil, fmt.Errorf("the WSL distro %s did not say where it mounts Windows drives", distro)
	}
	return wsl.GitRunner{Exe: exe, Paths: in.Paths}, nil
}

// WSLDistro is one distro as the agent forms list it.
type WSLDistro struct {
	wsl.Distro
	// Claude is what the last probe of the distro found ("" when it was not probed yet: the Connections page probes).
	Claude    string `json:"claude,omitempty"`
	Probed    bool   `json:"probed"`
	SignedIn  *bool  `json:"signedIn,omitempty"`
	Interop   bool   `json:"interop"`
	MountRoot string `json:"mountRoot,omitempty"`
}

// WSLStatus is the wsl.distros answer: whether WSL is here, its distros, and why there are none.
type WSLStatus struct {
	Available bool        `json:"available"`
	Distros   []WSLDistro `json:"distros"`
	Message   string      `json:"message,omitempty"`
}

// WSLDistros lists this machine's user distros with what the last probes found, without probing (wsl.exe -l only).
func (c *Core) WSLDistros(ctx context.Context) WSLStatus {
	st := WSLStatus{Distros: []WSLDistro{}}
	if c.wsl == nil {
		st.Message = "WSL is not available in this build"
		return st
	}
	if _, err := c.wsl.Path(); err != nil {
		st.Message = "WSL is not installed on this machine"
		return st
	}
	st.Available = true
	ds, msg, err := c.wsl.Distros(ctx)
	if err != nil {
		st.Message = err.Error()
		return st
	}
	st.Message = msg
	for _, d := range ds {
		if !wsl.UserDistro(d.Name) {
			continue
		}
		row := WSLDistro{Distro: d}
		if in, ok := c.wsl.Cached(d.Name, "claude"); ok {
			row.Probed, row.Claude, row.Interop, row.MountRoot = true, in.Tool, in.Interop, in.Paths.MountRoot
			row.SignedIn = wslSignedIn(in)
		}
		st.Distros = append(st.Distros, row)
	}
	return st
}

// wslSignedIn is the sign-in state the probe's run of `claude auth status` printed inside the distro (nil when it did not run).
// It is the CLI's own answer: Drydock never reads a credential (D1).
func wslSignedIn(in wsl.Info) *bool {
	if in.AuthExit < 0 {
		return nil
	}
	v := strings.Contains(strings.ReplaceAll(in.Auth, " ", ""), `"loggedIn":true`)
	return &v
}

// checkChatWSL is chats.Deps.CheckWSL: a chat runs adapter inside distro only where an agent could (checkRunMode: Claude Code,
// a distro this machine has, no sandbox in the chat's project), with the CLI installed and not known to be signed out there.
// A "signed out" from the probe cache is checked again before it refuses, so signing in on the Connections page counts at
// once. Every refusal names the distro.
func (c *Core) checkChatWSL(ctx context.Context, projectID, adapter, distro string) (string, error) {
	_, d, err := c.checkRunMode(ctx, projectID, adapter, agents.RunModeWSL, distro)
	if err != nil {
		return "", err
	}
	probe := func(fresh bool) (wsl.Info, error) {
		return c.wsl.Probe(ctx, d, "claude", claude.WSLVersionArgs, claude.WSLAuthArgs, fresh)
	}
	in, err := probe(false)
	if err == nil && in.Tool != "" {
		if s := wslSignedIn(in); s != nil && !*s {
			in, err = probe(true)
		}
	}
	if err != nil {
		return "", fmt.Errorf("the WSL distro %s cannot be reached: %w", d, err)
	}
	if in.Tool == "" {
		return "", fmt.Errorf("Claude Code was not found in the WSL distro %s: install it inside the distro, then sign in on its card on the Connections page", d)
	}
	if s := wslSignedIn(in); s != nil && !*s {
		return "", fmt.Errorf("Claude Code in the WSL distro %s is not signed in: sign in on its card on the Connections page (Sign in runs `claude auth login` inside the distro)", d)
	}
	return d, nil
}

// wslChatsDir is where the private folders of WSL chats live, under the distro user's HOME.
const wslChatsDir = ".drydock/chats"

// isWSLChatFolder reports whether a Linux path is a chat's private folder (<HOME>/.drydock/chats/<chat id>; a fork keeps its
// source's).
func isWSLChatFolder(linux string) bool {
	dir := path.Dir(path.Clean(linux))
	return path.Base(dir) == path.Base(wslChatsDir) && path.Base(path.Dir(dir)) == path.Dir(wslChatsDir)
}

// chatWSLFolder is chats.Deps.WSLFolder. A chat without a project and without a folder the person picked runs in a private
// folder in the distro user's home (<HOME>/.drydock/chats/<chat id>, made by the distro's user through wsl.exe), so its files
// are on the distro's own disk; the folder is named the Windows way (\\wsl.localhost\<distro>\...) and made again when it is
// gone. A folder inside the distro (a Linux path, or \\wsl.localhost\<distro>\...) is checked inside the distro; a Windows
// folder is checked on Windows (the CLI reaches it through the distro's mount root).
func (c *Core) chatWSLFolder(ctx context.Context, distro, chatID, cwd string) (string, error) {
	if c.wsl == nil {
		return "", fmt.Errorf("the chat runs in the WSL distro %s, and WSL is not available here", distro)
	}
	if strings.ContainsAny(cwd, "\x00\r\n") {
		return "", errors.New("the folder path contains a control character")
	}
	var linux string
	switch d, lin, isUNC := wsl.ParseUNC(cwd); {
	case cwd == "":
		in, err := c.wslInfo(ctx, distro)
		if err != nil {
			return "", fmt.Errorf("the WSL distro %s cannot be reached: %w", distro, err)
		}
		if !strings.HasPrefix(in.Home, "/") {
			return "", fmt.Errorf("the WSL distro %s did not say where its user's home is", distro)
		}
		linux = path.Join(in.Home, wslChatsDir, chatID)
	case isUNC && !strings.EqualFold(d, distro):
		return "", fmt.Errorf("the folder %s is inside the WSL distro %s, and this chat runs in %s", cwd, d, distro)
	case isUNC:
		linux = lin
	case strings.HasPrefix(cwd, "/") && !strings.HasPrefix(cwd, "//"):
		linux = path.Clean(cwd)
	default:
		return chats.ValidateFolder(cwd)
	}
	if err := c.wsl.Dir(ctx, distro, linux, isWSLChatFolder(linux)); err != nil {
		return "", err
	}
	return wsl.UNC(distro, linux), nil
}

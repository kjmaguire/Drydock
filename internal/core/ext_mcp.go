package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kjmaguire/drydock/internal/agents/manifests"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/chats"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/packs"
	"github.com/kjmaguire/drydock/internal/peer"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/repomap"
	"github.com/kjmaguire/drydock/internal/security/secretadapt"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The MCP wiring (docs/mcp.md): one shared Gateway owned by the Core, and the per-session hook that gives an agent CLI its single
// "drydock" MCP entry. The gateway is created here, after the knowledge extension, because its Workspace providers come from
// Knowledge().Workspace(). The hooks themselves (Runtime.MCP, Deps.MCPStatus) are installed in core.Open and read the gateway
// through c.mcpw, so a session that starts before this extension finished simply runs without MCP and says why.

func init() {
	RegisterExtension(Extension{Name: "mcp", After: []string{"knowledge"}, Start: startMCP})
}

// mcpWiring is the running gateway of one Core.
type mcpWiring struct {
	gw *mcp.Gateway
}

// MCPGateway returns the Core's shared MCP gateway (nil when it did not start).
func (c *Core) MCPGateway() *mcp.Gateway {
	if w := c.mcpw.Load(); w != nil {
		return w.gw
	}
	return nil
}

// settingActTools is the per-project setting that gives agents the Workspace tools that change state (task, room, handoff).
const settingActTools = "mcp.act_tools:"

func startMCP(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	ws := mcp.Workspace{}
	k := c.Knowledge()
	if k != nil {
		ws = k.Workspace()
	}
	ws.Tasks, ws.Room, ws.Handoffs = mcpTasks{c}, mcpRoom{c}, mcpHandoffs{c}
	ws.Chats = mcpChats{c} // ext_chats.go; allowed only to chat sessions that read history (planChatMCP)
	// a chat session's skills tool serves the library skills chosen for the chat (ext_chats.go, decision P60); every other run
	// keeps the project's accepted skills
	ws.Skills = chatSkillsTool{c: c, inner: ws.Skills}
	ws.Sandbox = mcpSandbox{c} // ext_sandbox.go; allowed only to sandboxed worker turns (mcpAttach)
	if repomap.Available() {
		ws.RepoMap = mcpRepoMap{c} // ext_repomap.go; without CGO the tool answers mcp.RepoMapGatedMessage
	}
	gw, err := mcp.New(mcp.Options{DataDir: c.cfg.DataDir, DB: c.DB, Bus: c.Bus, Proc: c.cfg.Procs, Scanner: secretadapt.MCP(c.Scanner), Workspace: ws, Credentials: lateMCP{c}, Approver: lateMCP{c}, Permissions: c.permissions(),
		PeerOwner: peerOwner, PeerMember: proc.InTree})
	if err != nil {
		return nil, nil, err
	}
	// a config file left by a crash holds a token that died with the process; do not leave it lying around
	stale, _ := filepath.Glob(filepath.Join(c.cfg.DataDir, "runs", "*", "mcp.json"))
	for _, f := range stale {
		_ = os.Remove(f)
	}
	if err := gw.Start(); err != nil {
		return nil, nil, err
	}
	if k != nil {
		k.UseGateway(gw) // live packs query the docs servers through the shared gateway, not a private one
	}
	w := &mcpWiring{gw: gw}
	c.mcpw.Store(w)
	closeFn := func() {
		c.mcpw.Store(nil)
		_ = gw.Close()
	}
	m := map[string]Method{
		"mcp.status": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.mcpStatusReport(ctx), nil }),
		// The human allows the gateway for a CLI whose per-run MCP mechanism no real run has proven yet (Codex, Gemini, OpenCode,
		// Goose). Off by default. It reaches the CLI through ACP session/new.mcpServers only, as the stdio shim.
		"mcp.try.set": bind(func(ctx context.Context, p struct {
			Adapter string `json:"adapter"`
			Enabled bool   `json:"enabled"`
		}) (any, error) {
			c.mu.Lock()
			_, known := c.adapters[p.Adapter]
			c.mu.Unlock()
			if !known {
				return nil, fmt.Errorf("unknown adapter %q", p.Adapter)
			}
			if _, canTry, why := c.adapterMCPKind(p.Adapter); !canTry {
				return nil, fmt.Errorf("%s", why)
			}
			v := "0"
			if p.Enabled {
				v = "1"
			}
			if err := c.setSetting(ctx, settingMCPTry+p.Adapter, v); err != nil {
				return nil, err
			}
			c.publish(ctx, "mcp.try_changed", "", map[string]any{"adapter": p.Adapter, "enabled": p.Enabled, "by": c.Human()})
			return c.mcpStatusReport(ctx), nil
		}),
		"mcp.actTools.get": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			return map[string]any{"projectId": p.ProjectID, "enabled": c.actToolsEnabled(ctx, p.ProjectID)}, nil
		}),
		"mcp.actTools.set": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
			Enabled   bool   `json:"enabled"`
		}) (any, error) {
			if _, err := c.GetProject(ctx, p.ProjectID); err != nil {
				return nil, err
			}
			v := "0"
			if p.Enabled {
				v = "1"
			}
			if err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingActTools+p.ProjectID, v)
				return err
			}); err != nil {
				return nil, err
			}
			if k != nil {
				k.audit(ctx, "mcp.act_tools", p.ProjectID, map[string]any{"enabled": p.Enabled})
			}
			return map[string]any{"projectId": p.ProjectID, "enabled": p.Enabled}, nil
		}),
	}
	return m, closeFn, nil
}

func (c *Core) actToolsEnabled(ctx context.Context, projectID string) bool {
	var v string
	if err := c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingActTools+projectID).Scan(&v); err != nil {
		return false
	}
	return v == "1" || v == "true"
}

// mcpPlan is the decision for one session: attach or not, and with which policy.
type mcpPlan struct {
	Attach     bool
	Unverified bool // attached because the human allowed an unproven mechanism for this CLI (mcp.try.set)
	Reason     string
	DataClass  string
	Info       mcp.RunInfo
	Policy     mcp.RunPolicy
	Tools      []string         // exposed tool names, including fetch_range
	Servers    []mcp.ServerSpec // the project's own MCP servers this agent was granted (ext_mcpservers.go)
}

// customMCP says whether adapter is a person's own CLI (decision P115) and, when its manifest has an mcp section, how its CLI
// takes the MCP configuration (decision P117).
func (c *Core) customMCP(adapter string) (custom bool, how *manifests.CustomMCP) {
	if c.Connections == nil || c.Connections.Registry == nil {
		return false, nil
	}
	m, ok := c.Connections.Registry.Set.Get(adapter)
	if !ok || !m.Meta.Custom {
		return false, nil
	}
	how, _ = m.CustomMCP() // a loaded manifest validated, so an error cannot happen here; nil is "declares none" either way
	return true, how
}

// mcpProvider is the provider mcp.ConfigFor knows an adapter by: a person's own CLI is "custom".
func (c *Core) mcpProvider(adapter string) string {
	if custom, _ := c.customMCP(adapter); custom {
		return "custom"
	}
	return adapter
}

// adapterMCPKind reports whether the generated configuration of an adapter is verified (and why not), plus canTry: the
// configuration can be generated but no real run has proven it, so the human may choose to attach the gateway anyway
// (mcp.try.set). A CLI with no known mechanism (Antigravity, a person's own CLI whose manifest has no mcp section) can never be
// tried.
func (c *Core) adapterMCPKind(adapter string) (verified, canTry bool, why string) {
	if custom, how := c.customMCP(adapter); custom && how == nil {
		return false, false, adapter + ": its manifest has no mcp section, so Drydock does not know how this CLI takes an MCP configuration"
	}
	return adapterMCPKind(c.mcpProvider(adapter), adapter)
}

// adapterMCPKind is Core.adapterMCPKind for a provider ConfigFor knows; adapter is the id the reason names.
func adapterMCPKind(provider, adapter string) (verified, canTry bool, why string) {
	probe := mcp.Endpoint{RunID: "probe", URL: "http://127.0.0.1:1/mcp/probe", Token: "probe", ShimCommand: []string{"drydock", "mcp-shim"}}
	cfg, err := mcp.ConfigFor(provider, probe)
	switch {
	case errors.Is(err, mcp.ErrMCPUnknown):
		return false, false, adapter + ": per-run MCP configuration is unproven for this CLI (docs/mcp.md section 10)"
	case err != nil:
		return false, false, adapter + ": " + err.Error()
	case !cfg.Verified:
		return false, true, adapter + ": the generated MCP configuration is unverified (no live run has used it)"
	}
	return true, false, ""
}

const settingMCPTry = "mcp.try."

// mcpTryAllowed reports whether the human allowed the gateway for a CLI whose mechanism is not proven yet.
func (c *Core) mcpTryAllowed(ctx context.Context, adapter string) bool {
	var v string
	if err := c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingMCPTry+adapter).Scan(&v); err != nil {
		return false
	}
	return v == "1"
}

// planMCP decides what a session of an agent gets. role is the role the policy table is keyed by ("reviewer" for a review turn).
func (c *Core) planMCP(ctx context.Context, agentID, projectID, taskID, adapter, role, model, cwd string) mcpPlan {
	pl := mcpPlan{DataClass: mcp.DataClassPrivate}
	no := func(format string, a ...any) mcpPlan { pl.Reason = fmt.Sprintf(format, a...); return pl }
	if c.MCPGateway() == nil {
		return no("the MCP gateway is not running")
	}
	if ok, canTry, why := c.adapterMCPKind(adapter); !ok {
		if !canTry || !c.mcpTryAllowed(ctx, adapter) {
			return no("%s", why)
		}
		pl.Unverified = true
	}
	ag, err := c.agent(ctx, agentID)
	if err != nil {
		return no("unknown agent %q", agentID)
	}
	if role == "" {
		role = ag.Role
	}
	if ag.ProviderID != "" && c.Providers != nil {
		if p, err := c.Providers.Get(ctx, ag.ProviderID); err == nil && c.Providers.EffectiveDataClass(ctx, p, firstNonEmpty(model, ag.Model)) == providers.DataTrainable {
			return no("the agent runs on a trainable provider route: no Workspace tools or private data for it (security.md section 9)")
		}
	}
	pl.Info = mcp.RunInfo{ProjectID: projectID, TaskID: taskID, AgentID: agentID, Role: role, Worktree: cwd}
	// the role's row of the project's table (docs/mcp.md section 3, decision P115); the act column only while the project lets
	// agents act (mcp.act_tools)
	row := policyRow(role)
	tab := c.roleTable(ctx, projectID)
	var packAllow packs.Allowance
	if k := c.Knowledge(); k != nil {
		packAllow, _ = k.Packs.AllowFor(ctx, projectID, agentID, role)
	}
	var allow []string
	for _, t := range tab.WorkspaceTools(row, c.actToolsEnabled(ctx, projectID), repomap.Available()) {
		if t == "search_pack" && !packAllow.SearchPack {
			continue // search_pack reads only attached packs: none attached, nothing to search
		}
		allow = append(allow, t)
	}
	sort.Strings(allow)
	pl.Policy = mcp.RunPolicy{Allow: map[string][]string{mcp.WorkspaceServerID: allow}}
	pl.Tools = append(append([]string(nil), allow...), mcp.ToolFetchRange)
	// servers the human added to this project and granted to this agent (deny by default: no grant, no server), as far as the
	// role's row lets it reach that kind of server
	columns := map[string]string{}
	if specs, grants, _ := c.mcpServersForRun(ctx, projectID, agentID); len(specs) > 0 {
		cols := c.serverColumns(ctx, specs)
		for _, sp := range specs {
			if !tab.Allowed(row, cols[sp.ID]) {
				continue
			}
			columns[sp.ID] = cols[sp.ID]
			pl.Servers = append(pl.Servers, sp)
			pl.Policy.Allow[sp.ID] = grants[sp.ID]
		}
	}
	// the docs servers of the live packs attached for this agent (docs/expert-packs.md): the pack's tool of the catalog server,
	// through the same gateway pipeline (catalog trust and cache, egress limits, untrusted marking), under the role's row
	c.addLivePackServers(ctx, &pl, tab, row, packAllow, columns)
	// the project's CEL rules and the task's narrowing only take tools away, on every list and call (RunPolicy.Refuse)
	ruleSpecs, err := c.policyRules(ctx, projectID)
	if err != nil {
		return no("the project's tool rules could not be read: %v", err)
	}
	var rules *mcp.RuleSet
	if len(ruleSpecs) > 0 {
		if rules, err = mcp.CompileRules(ruleSpecs); err != nil {
			return no("the project's tool rules do not compile, so no tools are given until a person fixes them: %v", err)
		}
	}
	narrow, narrowed := c.taskNarrowing(ctx, taskID)
	pl.Policy.Refuse = toolRefuser(rules, narrow, narrowed, mcp.ToolFacts{Role: row, Adapter: adapter, Task: taskID != ""}, columns)
	pl.Tools = append(pl.Tools, exposedTools(pl.Servers, pl.Policy.Allow)...)
	pl.Tools = keepAllowed(pl.Policy, pl.Tools)
	pl.Attach = true
	return pl
}

// mcpAttach is Runtime.MCP: it starts the session's gateway run, writes the single-entry config file and points the CLI at it.
func (c *Core) mcpAttach(ctx context.Context, cfg *rt.Config, runID string) (rt.MCPAttachment, error) {
	var pl mcpPlan
	if strings.HasPrefix(cfg.AgentID, chats.AgentPrefix) {
		pl = c.planChatMCP(ctx, cfg) // a chat session: only the chats tool (ext_chats.go)
	} else {
		pl = c.planMCP(ctx, cfg.AgentID, cfg.ProjectID, cfg.TaskID, cfg.Adapter, cfg.Spec.Role, cfg.Spec.Model, cfg.Spec.Cwd)
	}
	if !pl.Attach {
		return rt.MCPAttachment{Reason: pl.Reason}, nil
	}
	if cfg.Spec.Sandboxed {
		// a sandboxed worker turn (pipeline prepareWorker) runs its commands in the task's container through these tools
		pl.Policy.Allow[mcp.WorkspaceServerID] = append(pl.Policy.Allow[mcp.WorkspaceServerID], SandboxTools...)
		pl.Tools = append(pl.Tools, SandboxTools...)
	}
	// a worker turn outside a sandbox asks Drydock about tool uses its rules do not cover (ext_permprompt.go)
	offerPermissionPrompts(cfg, &pl)
	gw := c.MCPGateway()
	if gw == nil {
		return rt.MCPAttachment{Reason: "the MCP gateway is not running"}, nil
	}
	pl.Info.RunID = runID
	if cfg.Spec.IsWSL() {
		// the agent runs inside a WSL distro: its permission requests name Linux paths (decision P41)
		p, ok := c.wslPaths(ctx, cfg.Spec.WSLDistro)
		if !ok {
			return rt.MCPAttachment{Reason: "the WSL distro " + cfg.Spec.WSLDistro + " could not be probed, so its paths cannot be checked"}, nil
		}
		pl.Info.WSL = &p
	}
	// the peer check (decision P122) follows the agent's processes on this computer; an agent in a WSL distro connects from
	// inside the distro, where neither the TCP table nor the job can follow it
	ep, err := gw.StartRun(mcp.RunConfig{Info: pl.Info, DataClass: pl.DataClass, Policy: pl.Policy, Servers: pl.Servers, PeerCheck: !cfg.Spec.IsWSL()})
	if err != nil {
		return rt.MCPAttachment{}, err
	}
	release := func() {
		gw.EndRun(runID)
		_ = os.Remove(ep.ConfigPath)
	}
	cc, err := mcp.ConfigFor(c.mcpProvider(cfg.Adapter), *ep, mcp.ConfigOptions{Tools: pl.Tools})
	if err == nil {
		err = mcp.WriteConfig(ep.ConfigPath, cc)
	}
	if err != nil {
		release()
		return rt.MCPAttachment{}, errors.New("could not write the MCP config: " + scrubPath(err))
	}
	if _, how := c.customMCP(cfg.Adapter); how != nil && how.Via == manifests.CustomMCPViaFile {
		// a person's own CLI that reads its MCP configuration from its working folder (decision P117) gets the same file there
		removeFile, ferr := placeCustomMCPFile(ctx, cfg.Spec.Cwd, how.File, cc.JSON)
		if ferr != nil {
			release()
			return rt.MCPAttachment{}, ferr
		}
		endRun := release
		release = func() { removeFile(); endRun() }
	}
	cfg.Spec.MCPConfigPath = ep.ConfigPath
	cfg.Spec.AllowedTools = append(append([]string(nil), cfg.Spec.AllowedTools...), cc.AllowedTools...)
	return rt.MCPAttachment{Attached: true, Unverified: pl.Unverified, Transport: cc.Transport, Tools: pl.Tools, Release: release,
		BindProcess: func(pid int) { gw.BindPeer(runID, pid) }}, nil
}

// scrubPath keeps an OS error short: it names the path and the failure, never file content.
func scrubPath(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Op + " " + pe.Path + ": " + pe.Err.Error()
	}
	return err.Error()
}

// mcpStatus is Deps.MCPStatus: the manifest's view of what a turn gets.
func (c *Core) mcpStatus(ctx context.Context, t tasks.Task, agentID, phase string) pipeline.MCPStatus {
	ag, err := c.agent(ctx, agentID)
	if err != nil {
		return pipeline.MCPStatus{Reason: fmt.Sprintf("unknown agent %q", agentID)}
	}
	role := ag.Role
	if phase == "review" {
		role = "reviewer"
	}
	pl := c.planMCP(ctx, agentID, t.ProjectID, t.ID, ag.Adapter, role, ag.Model, t.Worktree)
	if !pl.Attach {
		return pipeline.MCPStatus{Reason: pl.Reason}
	}
	if phase == "work" && ag.Adapter == "claude" && c.sandboxSwitch(ctx, t.ProjectID).Enabled {
		pl.Tools = append(pl.Tools, SandboxTools...) // what mcpAttach adds for a sandboxed worker turn
	} else if phase == "work" && ag.Adapter == "claude" {
		pl.Tools = append(pl.Tools, mcp.ToolPermissionPrompt) // what mcpAttach adds for a worker turn outside a sandbox
	}
	return pipeline.MCPStatus{Attached: true, Tools: pl.Tools}
}

// mcpStatusReport is the mcp.status method: whether the gateway runs and which adapters can be given MCP.
func (c *Core) mcpStatusReport(ctx context.Context) map[string]any {
	type adapterRow struct {
		Adapter  string `json:"adapter"`
		Attached bool   `json:"attached"`
		Reason   string `json:"reason,omitempty"`
		// CanTry: the mechanism is unproven but the human may allow it; Trying: they did.
		CanTry bool `json:"canTry"`
		Trying bool `json:"trying"`
	}
	c.mu.Lock()
	names := make([]string, 0, len(c.adapters))
	for id := range c.adapters {
		names = append(names, id)
	}
	c.mu.Unlock()
	sort.Strings(names)
	rows := make([]adapterRow, 0, len(names))
	for _, id := range names {
		ok, canTry, why := c.adapterMCPKind(id)
		trying := canTry && c.mcpTryAllowed(ctx, id)
		rows = append(rows, adapterRow{Adapter: id, Attached: ok || trying, Reason: why, CanTry: canTry, Trying: trying})
	}
	gw := c.MCPGateway()
	addr := ""
	if gw != nil {
		addr = gw.Addr()
	}
	return map[string]any{"running": gw != nil, "address": addr, "adapters": rows}
}

// peerOwner is the gateway's PeerOwner (decision P122): the process of a loopback connection's client end, or
// mcp.ErrPeerUnsupported where this platform cannot find it.
func peerOwner(client, server netip.AddrPort) (int, error) {
	pid, err := peer.Owner(client, server)
	if errors.Is(err, peer.ErrUnsupported) {
		return 0, mcp.ErrPeerUnsupported
	}
	return pid, err
}

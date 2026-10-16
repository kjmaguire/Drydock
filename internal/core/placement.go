package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/connections"
)

// Where an agent runs when nobody chose (decision P58). A project's default "Where agents run" starts from the Connections:
// on Windows when the agent CLI is usable there, else in the first WSL distro where it is. At run time, an agent that follows
// a default nobody chose (the project's native default, not set by a person) and whose CLI is not installed on Windows runs
// in the one WSL distro where the CLI is ready (or the WSL default distro among several), and the run's events say so. An
// agent a person set to run on Windows, a project a person set to Windows, and a sandboxed project are never moved: they keep
// the clear "not installed on Windows" error.

// byDrydock is ProjectRunMode.By when Drydock set the default itself (a new project, from the Connections).
const byDrydock = "drydock"

// adapterDisplay is the CLI's name as people know it ("Claude Code"), else its id.
func (c *Core) adapterDisplay(id string) string {
	if m, ok := c.Connections.Registry.Set.Get(id); ok && m.Meta.DisplayName != "" {
		return m.Meta.DisplayName
	}
	return id
}

// canRunInWSL reports whether adapter id can start its CLI inside a WSL distro.
func (c *Core) canRunInWSL(id string) (agents.Adapter, bool) {
	c.mu.Lock()
	ad, ok := c.adapters[id]
	c.mu.Unlock()
	return ad, ok && slices.Contains(ad.Capabilities().RunModes, agents.RunModeWSL)
}

// wslReady lists the WSL distros where adapter id's CLI is ready (connections.Service.ReadyWSL: usable, the WSL default
// distro first). fresh false reuses a recent probe.
func (c *Core) wslReady(ctx context.Context, id string, fresh bool) []connections.Connection {
	if _, ok := c.canRunInWSL(id); !ok || c.wsl == nil {
		return nil
	}
	return c.Connections.ReadyWSL(ctx, id, fresh)
}

// runPlacement is where agent a's CLI runs, and why when Drydock chose it: the agent's own choice, else its project's
// default, else (decision P58) the WSL distro where the CLI is ready when it is not installed on Windows.
func (c *Core) runPlacement(ctx context.Context, a Agent) (mode, distro, why string) {
	mode, distro = c.effectiveRunMode(ctx, a)
	if mode != agents.RunModeNative || a.RunMode == agents.RunModeNative {
		return mode, distro, "" // in WSL already, or a person chose Windows for this agent: then the clear error stays
	}
	if p := c.projectRunMode(ctx, a.ProjectID); strings.HasPrefix(p.By, "human:") {
		return mode, distro, "" // a person chose Windows for the project's agents
	}
	ad, ok := c.canRunInWSL(a.Adapter)
	if !ok || agents.NativeInstalled(ad) || c.sandboxSwitch(ctx, a.ProjectID).Enabled {
		return mode, distro, ""
	}
	ready := c.wslReady(ctx, a.Adapter, false)
	pick := ""
	switch {
	case len(ready) == 1:
		pick = ready[0].Distro
	case len(ready) > 1 && ready[0].WSL != nil && ready[0].WSL.Default:
		pick = ready[0].Distro // several: the WSL default distro, when it is one of them
	}
	if pick == "" {
		return mode, distro, ""
	}
	name := c.adapterDisplay(a.Adapter)
	return agents.RunModeWSL, pick, fmt.Sprintf("%s is not installed on Windows and %s follows its project's default, so it runs in the WSL distro %s, where %s is ready (Project settings, \"Where agents run\", chooses)",
		name, a.Name, pick, name)
}

// placeNewProject sets a new project's default run mode from the Connections (decision P58): nothing (Windows) when the agent
// CLI that can run in WSL (Claude Code) is usable on Windows, else the first WSL distro where it is usable. It probes only on
// a machine with WSL; when no distro has it ready, the default stays on Windows, as before.
func (c *Core) placeNewProject(ctx context.Context, projectID string) {
	if c.wsl == nil {
		return
	}
	if _, err := c.wsl.Path(); err != nil {
		return
	}
	c.mu.Lock()
	ids := make([]string, 0, len(c.adapters))
	for id, ad := range c.adapters {
		if slices.Contains(ad.Capabilities().RunModes, agents.RunModeWSL) {
			ids = append(ids, id)
		}
	}
	c.mu.Unlock()
	sort.Strings(ids)
	if len(ids) == 0 {
		return
	}
	id := ids[0] // Claude Code: the only CLI Drydock starts inside a distro
	if i := slices.Index(ids, "claude"); i >= 0 {
		id = ids[i]
	}
	ad, _ := c.canRunInWSL(id)
	if agents.NativeInstalled(ad) {
		// installed on Windows: it stays the default unless its Connection found it there and not usable (signed out, a
		// blocked version); a program the detection does not see (a configured one) counts as usable
		if conn, err := c.Connections.Check(ctx, id); err != nil || conn.Usable || !conn.Installed {
			return
		}
	}
	ready := c.wslReady(ctx, id, true)
	if len(ready) == 0 {
		return
	}
	d, name := ready[0].Distro, c.adapterDisplay(id)
	why := fmt.Sprintf("%s is not usable on Windows; it is ready in the WSL distro %s", name, d)
	pr := ProjectRunMode{RunMode: agents.RunModeWSL, WSLDistro: d, By: byDrydock, At: time.Now().UnixMilli(), Why: why}
	if err := c.storeRunMode(ctx, projectID, pr); err != nil {
		c.logf("project %s: setting where its agents run: %v", projectID, err)
		return
	}
	c.publish(ctx, "project.run_mode", projectID, map[string]any{"runMode": pr.RunMode, "wslDistro": pr.WSLDistro, "by": pr.By, "why": why})
}

// storeRunMode writes a project's default run mode.
func (c *Core) storeRunMode(ctx context.Context, projectID string, p ProjectRunMode) error {
	b, _ := json.Marshal(p)
	return c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingRunMode+projectID, string(b))
		return err
	})
}

package core

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/connections"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/safe"
)

// Drydock checks the agent CLIs itself (A2): a little after the app starts, every half hour, and when something says a CLI's
// state may have changed (a sign-in ended, an agent could not start), it checks the CLIs in use, records each answer as the
// newest (connections.Service.Last, which connection.list {cached:true}, the Inbox and the Agents page read at once) and
// publishes connection.checked. A check starts each CLI for its version and its own sign-in status, as the Agents page does:
// no model is called and nothing leaves the machine (P36). Credentials are never read (D1).

// cliCheckFirst is how long after start the first check waits (the app's own start comes first); cliCheckEvery is the period.
var (
	cliCheckFirst = 30 * time.Second
	cliCheckEvery = 30 * time.Minute
)

// cliCheckTimeout bounds one round of checks.
const cliCheckTimeout = 2 * time.Minute

func init() {
	RegisterExtension(Extension{Name: "clicheck", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		bg, cancel := context.WithCancel(context.WithoutCancel(ctx))
		kick := make(chan string, 1)
		var wg sync.WaitGroup
		wg.Add(2)
		safe.Go("core.clicheck.events", func() {
			defer wg.Done()
			events.Consume(bg, c.Bus, events.Filter{Types: []string{"connection.sign_in_finished", "agent.failed"}}, func(e events.Event) {
				if why, ok := cliCheckReason(e); ok {
					select {
					case kick <- why:
					default: // a check is already asked for: it covers this one too
					}
				}
			})
		})
		safe.Go("core.clicheck", func() {
			defer wg.Done()
			c.cliCheckLoop(bg, kick)
		})
		return nil, func() { cancel(); wg.Wait() }, nil
	}})
}

// cliCheckReason says whether an event is a reason to check the CLIs again: a sign-in that ended, or an agent that failed to
// start (its CLI may have been removed or signed out). A failure during a run is the run's, not the CLI's.
func cliCheckReason(e events.Event) (string, bool) {
	switch e.Type {
	case "connection.sign_in_finished":
		return "sign_in", true
	case "agent.failed":
		var d struct {
			Phase string `json:"phase"`
		}
		if json.Unmarshal(e.Data, &d) == nil && d.Phase == "start" {
			return "agent_failed", true
		}
	}
	return "", false
}

func (c *Core) cliCheckLoop(ctx context.Context, kick <-chan string) {
	first := time.NewTimer(cliCheckFirst)
	defer first.Stop()
	every := time.NewTicker(cliCheckEvery)
	defer every.Stop()
	for {
		why := ""
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			why = "start"
		case <-every.C:
			why = "period"
		case why = <-kick:
		}
		c.CheckCLIs(ctx, why)
	}
}

// CheckCLIs checks the CLIs in use (the installs the crews' agents and the chats run: on Windows, or inside the WSL distro an
// agent or a chat runs in, decision P58) or, before anything uses one (a first run, where Home needs to know whether the
// builder's CLI is ready), every CLI Drydock knows on Windows. Every answer becomes the newest check of its Connection, and
// connection.checked says what was checked (keys and counts only).
func (c *Core) CheckCLIs(ctx context.Context, why string) []connections.Connection {
	ids := c.connectionsInUse(ctx)
	if len(ids) == 0 {
		ids = c.Connections.Registry.Set.IDs()
	}
	sort.Strings(ids)
	cctx, cancel := context.WithTimeout(ctx, cliCheckTimeout)
	defer cancel()
	out := make([]connections.Connection, len(ids))
	ok := make([]bool, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		safe.Go("core.CheckCLIs", func() {
			defer wg.Done()
			conn, err := c.Connections.CheckKey(cctx, id)
			out[i], ok[i] = conn, err == nil
		})
	}
	wg.Wait()
	var checked []connections.Connection
	keys := []string{}
	usable := 0
	for i := range out {
		if !ok[i] {
			continue
		}
		checked = append(checked, out[i])
		keys = append(keys, out[i].Key)
		if out[i].Usable {
			usable++
		}
	}
	if ctx.Err() == nil {
		c.publish(ctx, "connection.checked", "", map[string]any{"why": why, "keys": keys, "usable": usable})
	}
	return checked
}

// connectionsInUse are the Connection keys of the agents not archived and of the chats not archived that Drydock has a manifest
// for: the adapter id for a Windows install, connections.WSLKey for an install inside a distro.
func (c *Core) connectionsInUse(ctx context.Context) []string {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT adapter, run_mode, wsl_distro FROM agents WHERE archived_at=0
		UNION SELECT adapter, CASE WHEN wsl_distro<>'' THEN 'wsl' ELSE '' END, wsl_distro FROM chats WHERE archived_at=0`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var keys []string
	seen := map[string]bool{}
	for rows.Next() {
		var id, mode, distro string
		if rows.Scan(&id, &mode, &distro) != nil {
			continue
		}
		if _, known := c.Connections.Registry.Set.Get(id); !known {
			continue
		}
		key := id
		if mode == agents.RunModeWSL && distro != "" {
			key = connections.WSLKey(id, distro)
		}
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys
}

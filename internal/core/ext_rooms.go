package core

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/connections"
	"github.com/kjmaguire/drydock/internal/room"
)

// Rooms and the Inbox, decision P130: muting a room (room.setMuted), the stored join rules a person makes (room.joinRules,
// room.joinRule.add, room.joinRule.remove), a person's answer to an agent's request to join a channel (room.answerJoin), and
// the Inbox items the core holds in memory: an agent that failed or needs attention (the presence tracker) and an agent CLI an
// agent uses that is not ready (the Connections' latest checks). room.stop is with the responder (ext_agents.go).

func init() {
	RegisterExtension(Extension{Name: "rooms", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"room.setMuted": bind(func(ctx context.Context, p struct {
				RoomID string `json:"roomId"`
				Muted  bool   `json:"muted"`
			}) (any, error) {
				return c.SetRoomMuted(ctx, p.RoomID, p.Muted)
			}),
			"room.joinRules": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
			}) (any, error) {
				return c.Room.JoinRules(ctx, p.ProjectID)
			}),
			"room.joinRule.add": bind(func(ctx context.Context, p room.JoinRule) (any, error) {
				return c.Room.AddJoinRule(ctx, room.JoinRule{ProjectID: p.ProjectID, AgentID: p.AgentID, PathPrefix: p.PathPrefix, State: p.State}, room.Human(c.cfg.User))
			}),
			"room.joinRule.remove": bind(func(ctx context.Context, p idParam) (any, error) {
				if err := c.Room.RemoveJoinRule(ctx, p.ID, room.Human(c.cfg.User)); err != nil {
					return nil, err
				}
				return map[string]bool{"removed": true}, nil
			}),
			"room.answerJoin": bind(func(ctx context.Context, p struct {
				ID    string `json:"id"`
				Allow bool   `json:"allow"`
			}) (any, error) {
				return c.Room.AnswerJoin(ctx, p.ID, p.Allow, room.Human(c.cfg.User))
			}),
		}, nil, nil
	}})
}

// SetRoomMuted mutes or unmutes a room for the person: a muted room's messages raise neither its unread count nor an Inbox
// item, except a message that mentions the person by name. It returns the room's unread row as it is now.
func (c *Core) SetRoomMuted(ctx context.Context, roomID string, muted bool) (room.Unread, error) {
	rm, err := c.Room.GetRoom(ctx, roomID)
	if err != nil {
		return room.Unread{}, err
	}
	me := room.Human(c.cfg.User)
	if ok, err := c.Room.IsMember(ctx, roomID, me); err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("you are not a member of %s", rm.Name)
		}
		return room.Unread{}, err
	}
	st := room.StateActive
	if muted {
		st = room.StateMuted
	}
	if err := c.Room.SetMemberState(ctx, roomID, me, st, me); err != nil {
		return room.Unread{}, err
	}
	us, err := c.Room.UnreadCounts(ctx, me, rm.ProjectID)
	if err != nil {
		return room.Unread{}, err
	}
	for _, u := range us {
		if u.RoomID == roomID {
			return u, nil
		}
	}
	return room.Unread{RoomID: roomID, RoomName: rm.Name, Kind: rm.Kind, Muted: muted}, nil
}

// stateItems are the Inbox items of what the core holds in memory (decision P130): an agent whose presence is Failed or Needs
// Attention, and an agent CLI that an agent of the workspace (of projectID, when given) uses whose latest check says it is not
// ready. An agent put there by a watchdog pause is left out: the watchdog item is that cause. A Connection nothing checked yet
// since the app started has no item (nothing here starts a CLI); one item stands for every agent that uses the CLI.
func (c *Core) stateItems(ctx context.Context, projectID string) []room.NeedsYouItem {
	type ag struct{ id, project, name, adapter, runMode, distro string }
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT id, project_id, name, adapter, run_mode, wsl_distro FROM agents WHERE archived_at=0 AND (?='' OR project_id=?)`, projectID, projectID)
	if err != nil {
		return nil
	}
	live := map[string]ag{}
	for rows.Next() {
		var a ag
		if rows.Scan(&a.id, &a.project, &a.name, &a.adapter, &a.runMode, &a.distro) == nil {
			live[a.id] = a
		}
	}
	rows.Close()
	var out []room.NeedsYouItem
	if c.Presence != nil {
		for _, ps := range c.Presence.Snapshot() {
			a, ok := live[ps.AgentID]
			if !ok || ps.Label == "watchdog" || (ps.Presence != room.PresenceFailed && ps.Presence != room.PresenceNeedsAttention) {
				continue
			}
			what, word := "Failed", "failed"
			if ps.Presence == room.PresenceNeedsAttention {
				what, word = "Needs attention", "attention"
			}
			title := what
			if act := strings.TrimSpace(ps.Activity); act != "" && act != "needs attention" {
				title += ": " + act
			}
			out = append(out, room.NeedsYouItem{Key: fmt.Sprintf("agent:%s:%s:%d", a.id, word, ps.Since.UnixMilli()), Kind: room.NeedsAgent, Severity: 1,
				Title: inboxLine(title, 160), Detail: "Open the agent to see what happened. This goes when it next starts.", ProjectID: a.project, AgentID: a.id,
				AgentName: a.name, TaskID: ps.TaskID, CreatedAt: ps.Since})
		}
		// one the person marked done stays out; a later failure has another key (its time)
		keys := make([]string, len(out))
		for i, it := range out {
			keys[i] = it.Key
		}
		if acked, err := c.Room.AckedKeys(ctx, keys); err == nil && len(acked) > 0 {
			kept := out[:0]
			for _, it := range out {
				if !acked[it.Key] {
					kept = append(kept, it)
				}
			}
			out = kept
		}
	}
	if c.Connections == nil {
		return out
	}
	last := c.Connections.Last()
	seen := map[string]bool{}
	for _, a := range live {
		key := a.adapter
		if a.runMode == agents.RunModeWSL && a.distro != "" {
			key = connections.WSLKey(a.adapter, a.distro)
		}
		ck, ok := last[key]
		if !ok || ck.Usable || seen[key] || c.given[a.adapter] {
			continue
		}
		seen[key] = true
		name := a.adapter
		if m, ok := c.Connections.Registry.Set.Get(a.adapter); ok && m.Meta.DisplayName != "" {
			name = m.Meta.DisplayName
		}
		if ck.Distro != "" {
			name += " in " + ck.Distro
		}
		detail := "Agents that use it cannot start. Open it on the Agents page to fix it, then check again."
		if len(ck.Problems) > 0 {
			detail = inboxLine(ck.Problems[0], 160)
		}
		out = append(out, room.NeedsYouItem{Key: "connection:" + key, Kind: room.NeedsConnection, Severity: 1, Title: name + " is " + connectionWords(ck.Connection),
			Detail: detail, Adapter: a.adapter, ConnectionKey: key, CreatedAt: ck.At.UTC()})
	}
	return out
}

// connectionWords says in plain words why a Connection is not ready.
func connectionWords(c connections.Connection) string {
	switch {
	case c.Status == agents.StatusNotInstalled:
		return "not installed"
	case c.Status == agents.StatusUnauthenticated:
		return "not signed in"
	case c.Status == agents.StatusUnsupportedVersion:
		return "a version Drydock does not support"
	case c.Reason == connections.ReasonGateUnacked:
		return "waiting for your acknowledgement"
	}
	return "not ready"
}

// inboxLine is s on one line, cut to n bytes with "..." at a rune boundary.
func inboxLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

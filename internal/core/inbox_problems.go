package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/crashreport"
	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/room"
)

// What the core already knows went wrong in the background reaches the Inbox with its fix (lane B batch B6, items F14 and H7):
// a connected service whose key was refused, whose answers changed shape or whose sync failed three times in a row; a model
// provider that is down; an MCP server whose sign-in expired; an agent CLI sending output Drydock does not read; a scan tool
// whose install failed; a crash report of an earlier run. Each item is worked out from the current state on every read, like
// stateItems, so it goes by itself once the state is fine again (a sync that succeeds, a provider that answers, a new sign-in, a
// Test connection that passes, an install that works, the report deleted). One the person marks done stays out: a service's or a
// provider's key carries the day, so a problem that lasts comes back once a day, not on every failure.

// syncFailuresBeforeInbox is how many syncs in a row must fail before a connected service's sync reaches the Inbox.
const syncFailuresBeforeInbox = 3

// problemItems lists those items. The words are the core's own: the vendor's name, the state in plain words and the reason the
// core recorded (already redacted where it was recorded); never a prompt, code or a secret.
func (c *Core) problemItems(ctx context.Context) []room.NeedsYouItem {
	now := time.Now().UTC()
	day := now.Format("2006-01-02")
	var out []room.NeedsYouItem
	out = append(out, c.integrationProblems(ctx, day)...)
	out = append(out, c.providerProblems(ctx, day)...)
	out = append(out, c.mcpSignInProblems(ctx)...)
	out = append(out, c.driftProblems(ctx)...)
	out = append(out, c.toolInstallProblems()...)
	out = append(out, c.crashProblems()...)
	return c.ackedOut(ctx, out)
}

// ackedOut leaves out the items the person marked done.
func (c *Core) ackedOut(ctx context.Context, out []room.NeedsYouItem) []room.NeedsYouItem {
	if len(out) == 0 {
		return nil
	}
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
	return out
}

// integrationProblems: one item per enabled connection whose key was refused, whose answers changed shape, or whose sync (its
// own or one of its projects') failed syncFailuresBeforeInbox times in a row.
func (c *Core) integrationProblems(ctx context.Context, day string) []room.NeedsYouItem {
	svc := c.Integrations()
	if svc == nil {
		return nil
	}
	conns, err := svc.List(ctx)
	if err != nil {
		return nil
	}
	var out []room.NeedsYouItem
	for _, v := range conns {
		if !v.Enabled || v.Status == integrations.StatusDisconnected {
			continue
		}
		name := firstNonEmpty(v.Name, v.Kind)
		var title, detail, state string
		switch v.Status {
		case integrations.StatusUnauthenticated:
			state, title, detail = "key", name+" refused its key", v.StatusDetail
		case integrations.StatusDegraded:
			state, title, detail = "degraded", name+" answers in a way Drydock does not read", v.StatusDetail
		default:
			// a sync held back by the service's rate limit is not a problem the person can fix: it waits and tries again by
			// itself, so it never counts here (Reconnect would not help)
			var worst integrations.SyncStatus
			for _, st := range append([]integrations.SyncStatus{v.Sync}, assignmentSyncs(v.Assignments)...) {
				if st.LastErrorCode != integrations.CodeRateLimited && st.ConsecutiveFailures > worst.ConsecutiveFailures {
					worst = st
				}
			}
			if worst.ConsecutiveFailures < syncFailuresBeforeInbox {
				continue
			}
			state = "sync"
			title = fmt.Sprintf("%s sync failed %d times in a row", name, worst.ConsecutiveFailures)
			detail = worst.LastError
		}
		if strings.TrimSpace(detail) == "" {
			detail = "Open it to reconnect. This goes when it works again."
		}
		out = append(out, room.NeedsYouItem{Key: "integration:" + v.ID + ":" + state + ":" + day, Kind: room.NeedsIntegration, Severity: 1,
			Title: inboxLine(title, 160), Detail: inboxLine(detail, 200), Subject: v.Kind, CreatedAt: time.UnixMilli(v.Updated).UTC()})
	}
	return out
}

func assignmentSyncs(as []integrations.AssignmentView) []integrations.SyncStatus {
	out := make([]integrations.SyncStatus, 0, len(as))
	for _, a := range as {
		out = append(out, a.Sync)
	}
	return out
}

// providerProblems: one item per enabled model provider that is down (the health loop's last checks failed), except the server
// of Models on this PC, which the person starts and stops on its own page.
func (c *Core) providerProblems(ctx context.Context, day string) []room.NeedsYouItem {
	if c.Providers == nil {
		return nil
	}
	ps, err := c.Providers.List(ctx)
	if err != nil {
		return nil
	}
	var out []room.NeedsYouItem
	for _, p := range ps {
		if !p.Enabled || p.Status != string(providers.HealthDown) || p.Name == localProviderName || p.Name == oldLocalProviderName {
			continue
		}
		detail := "Agents and chats that use it fall back to another model or wait. Check again once it is back."
		if c.Health != nil {
			if st, err := c.Health.Status(ctx, p.ID); err == nil && strings.TrimSpace(st.LastError) != "" {
				detail = "Its last check: " + st.LastError
			}
		}
		out = append(out, room.NeedsYouItem{Key: "provider:" + p.ID + ":down:" + day, Kind: room.NeedsProvider, Severity: 1,
			Title: inboxLine(p.Name+" is down", 160), Detail: inboxLine(detail, 200), Subject: p.ID, CreatedAt: p.UpdatedAt.UTC()})
	}
	return out
}

// mcpSignInProblems: one item per MCP server whose sign-in expired or was revoked and that is not signed in again.
func (c *Core) mcpSignInProblems(ctx context.Context) []room.NeedsYouItem {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT m.id, m.display, s.value FROM settings s JOIN mcp_library m ON m.id = substr(s.key, ?)
		WHERE s.key LIKE ? AND m.oauth=1 AND m.oauth_at=0`, len(mcpOAuthExpiredKey)+1, mcpOAuthExpiredKey+"%")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []room.NeedsYouItem
	for rows.Next() {
		var id, display, at string
		if rows.Scan(&id, &display, &at) != nil {
			continue
		}
		ms, _ := strconv.ParseInt(at, 10, 64)
		name := firstNonEmpty(display, id)
		out = append(out, room.NeedsYouItem{Key: "mcp-sign-in:" + id + ":" + at, Kind: room.NeedsMCPSignIn, Severity: 1,
			Title:   inboxLine(name+" needs you to sign in again", 160),
			Detail:  "Its sign-in expired or was revoked, so agents' calls to its tools fail. Sign in again; your browser opens the server's own page.",
			Subject: id, CreatedAt: time.UnixMilli(ms).UTC()})
	}
	return out
}

// driftProblems: one item per agent CLI that sent output Drydock does not read, until a Test connection passes.
func (c *Core) driftProblems(ctx context.Context) []room.NeedsYouItem {
	var out []room.NeedsYouItem
	for adapter, rec := range c.loadDrift(ctx) {
		name := adapter
		if c.Connections != nil {
			if m, ok := c.Connections.Registry.Set.Get(adapter); ok && m.Meta.DisplayName != "" {
				name = m.Meta.DisplayName
			}
		}
		detail := "Drydock may miss part of what its agents do. Update the CLI or Drydock, then Test connection on its page."
		if len(rec.Types) > 0 {
			detail = "New kinds of output: " + inboxLine(strings.Join(rec.Types, ", "), 120) + ". " + detail
		}
		out = append(out, room.NeedsYouItem{Key: fmt.Sprintf("adapter-drift:%s:%d", adapter, rec.At), Kind: room.NeedsAdapterDrift, Severity: 1,
			Title: inboxLine(name+" sends output Drydock does not read", 160), Detail: detail, Adapter: adapter, Subject: adapter,
			CreatedAt: time.UnixMilli(rec.At).UTC()})
	}
	return out
}

// toolInstallProblems: one item per scan tool whose last install failed (a later install, Drydock's own or the person's, that
// works or is cancelled replaces it).
func (c *Core) toolInstallProblems() []room.NeedsYouItem {
	s := c.security()
	if s == nil {
		return nil
	}
	var out []room.NeedsYouItem
	for tool, j := range s.installJobs() {
		if j.State != "failed" {
			continue
		}
		detail := "Retry, or install it from the Security page."
		if e := strings.TrimSpace(j.Error); e != "" {
			detail = inboxLine(e, 160) + ". " + detail
		}
		out = append(out, room.NeedsYouItem{Key: fmt.Sprintf("tool-install:%s:%d", tool, j.Ended.UnixMilli()), Kind: room.NeedsToolInstall, Severity: 1,
			Title: "Installing " + tool + " failed", Detail: detail, Subject: tool, CreatedAt: j.Ended.UTC()})
	}
	return out
}

// crashProblems: one item per crash report kept on the Support page; deleting the report or marking the item done clears it.
func (c *Core) crashProblems() []room.NeedsYouItem {
	if c.cfg.DataDir == "" {
		return nil
	}
	rs, err := crashreport.List(c.cfg.DataDir)
	if err != nil {
		return nil
	}
	var out []room.NeedsYouItem
	for _, r := range rs {
		detail := "The crash report is on the Support page, with its secrets taken out. It never leaves this PC by itself."
		if s := strings.TrimSpace(r.Summary); s != "" {
			detail = inboxLine(s, 160) + ". " + detail
		}
		out = append(out, room.NeedsYouItem{Key: "crash:" + r.Name, Kind: room.NeedsCrash, Severity: 2,
			Title: "Drydock stopped unexpectedly on " + r.Time.UTC().Format("2 Jan 15:04 UTC"), Detail: detail, Subject: r.Name, CreatedAt: r.Time.UTC()})
	}
	return out
}

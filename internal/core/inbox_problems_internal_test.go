package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/crashreport"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/storage"
)

func problemKinds(t *testing.T, c *Core) map[room.NeedsKind]room.NeedsYouItem {
	t.Helper()
	items, err := c.NeedsYou(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	out := map[room.NeedsKind]room.NeedsYouItem{}
	for _, it := range items {
		out[it.Kind] = it
	}
	return out
}

// What the core already knows went wrong reaches the Inbox with what it is about, and each item goes by itself once the state is
// fine again (lane B batch B6, F14 and H7).
func TestInboxProblemItemsAppearAndClearThemselves(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	c, err := Open(ctx, Config{DataDir: filepath.Join(dir, "d"), User: "kyle", LeaseTTL: 3 * time.Second, Credentials: providers.NewMemoryCredentialStoreForTests()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := c.DB.Writer().ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	if got := problemKinds(t, c); len(got) != 0 {
		for k := range got {
			if k != room.NeedsConnection && k != room.NeedsAgent {
				t.Fatalf("a fresh workspace has a problem item %s", k)
			}
		}
	}

	// a connected service whose key was refused
	now := storage.Now()
	exec(`INSERT INTO integrations(id,kind,name,status,status_detail,created_at,updated_at) VALUES('int_1','linear','Linear','unauthenticated','HTTP 401 from Linear',?,?)`, now, now)
	// a provider that is down, and the server of Models on this PC, which never gets an item
	p, err := c.Providers.Create(ctx, providers.NewProvider{Name: "Fixture API", Kind: providers.KindDirect, BaseURL: "https://api.example.com/v1", DataClass: providers.DataPrivate, Tier: providers.TierCheap})
	if err != nil {
		t.Fatal(err)
	}
	local, err := c.Providers.Create(ctx, providers.NewProvider{Name: localProviderName, Kind: providers.KindHost, BaseURL: "http://127.0.0.1:9/v1", DataClass: providers.DataPrivate, Tier: providers.TierFree})
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE providers SET status='down' WHERE id IN (?,?)`, p.ID, local.ID)
	// an MCP server whose sign-in expired
	exec(`INSERT INTO mcp_library(id,display,transport,url,created_at,updated_at,oauth,oauth_at) VALUES('srv_1','Linear MCP','http','https://mcp.linear.app/mcp',?,?,1,0)`, now, now)
	exec(`INSERT INTO settings(key,value) VALUES(?,?)`, mcpOAuthExpiredKey+"srv_1", "1700000000000")
	// an agent CLI sending output Drydock does not read
	c.observeDrift(ctx, "claude", "brand_new_event")
	// a scan tool whose install failed
	s := c.security()
	if s == nil {
		t.Fatal("the security extension did not start")
	}
	s.mu.Lock()
	s.jobs["osv-scanner"] = &toolJob{job: scan.ToolJob{State: "failed", Error: "download failed", Ended: time.Now().UTC()}}
	s.mu.Unlock()
	// a crash report of an earlier run
	if err := os.MkdirAll(crashreport.Dir(c.cfg.DataDir), 0o700); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(crashreport.Dir(c.cfg.DataDir), "crash-20261103T101500Z-1.txt")
	if err := os.WriteFile(report, []byte("panic: boom\n\ngoroutine 1 [running]:\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := problemKinds(t, c)
	want := map[room.NeedsKind]string{
		room.NeedsIntegration:  "Linear refused its key",
		room.NeedsProvider:     "Fixture API is down",
		room.NeedsMCPSignIn:    "Linear MCP needs you to sign in again",
		room.NeedsAdapterDrift: "sends output Drydock does not read",
		room.NeedsToolInstall:  "Installing osv-scanner failed",
		room.NeedsCrash:        "Drydock stopped unexpectedly",
	}
	for k, title := range want {
		if !strings.Contains(got[k].Title, title) {
			t.Errorf("%s item: %+v", k, got[k])
		}
	}
	if got[room.NeedsIntegration].Subject != "linear" || got[room.NeedsProvider].Subject != p.ID || got[room.NeedsMCPSignIn].Subject != "srv_1" ||
		got[room.NeedsToolInstall].Subject != "osv-scanner" || got[room.NeedsAdapterDrift].Adapter != "claude" {
		t.Fatalf("subjects %+v", got)
	}
	items, _ := c.NeedsYou(ctx, "")
	for _, it := range items {
		if it.Kind == room.NeedsProvider && it.Subject == local.ID {
			t.Fatal("the server of Models on this PC got a down item")
		}
	}

	// a service that failed three syncs in a row, not two
	exec(`UPDATE integrations SET status='ok' WHERE id='int_1'`)
	exec(`INSERT INTO integration_assignments(id,integration_id,project_id,created_at,updated_at) VALUES('asg_1','int_1','prj_x',?,?)`, now, now)
	exec(`INSERT INTO integration_cursors(integration_id,scope,consecutive_failures,last_error,last_attempt_at) VALUES('int_1','asg_1',2,'timeout',?)`, now)
	if _, ok := problemKinds(t, c)[room.NeedsIntegration]; ok {
		t.Fatal("two failed syncs already reached the Inbox")
	}
	exec(`UPDATE integration_cursors SET consecutive_failures=3 WHERE integration_id='int_1' AND scope='asg_1'`)
	if it := problemKinds(t, c)[room.NeedsIntegration]; !strings.Contains(it.Title, "sync failed 3 times") || it.Detail != "timeout" {
		t.Fatalf("sync item %+v", it)
	}

	// marking an item done keeps it out (the day's key)
	if err := c.Room.AckNeedsYou(ctx, problemKinds(t, c)[room.NeedsIntegration].Key, room.Human("kyle")); err != nil {
		t.Fatal(err)
	}
	if _, ok := problemKinds(t, c)[room.NeedsIntegration]; ok {
		t.Fatal("an item marked done came back")
	}

	// each goes by itself once the state is fine again
	exec(`UPDATE providers SET status='up' WHERE id=?`, p.ID)
	exec(`UPDATE mcp_library SET oauth_at=? WHERE id='srv_1'`, now)
	exec(`DELETE FROM settings WHERE key=?`, driftKey+"claude")
	s.mu.Lock()
	s.jobs["osv-scanner"].job.State = "done"
	s.mu.Unlock()
	if err := os.Remove(report); err != nil {
		t.Fatal(err)
	}
	for k := range want {
		if it, ok := problemKinds(t, c)[k]; ok {
			t.Errorf("%s item stayed after its state was fine: %+v", k, it)
		}
	}
}

package core_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/backup"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/room"
)

type bkClock struct{ t time.Time }

func (c *bkClock) now() time.Time { return c.t }

func (a *app) backups() (dir string, list []backup.Info) {
	a.t.Helper()
	var out struct {
		Dir     string        `json:"dir"`
		Backups []backup.Info `json:"backups"`
	}
	a.call("backup.list", nil, &out)
	return out.Dir, out.Backups
}

func (a *app) projectNames() []string {
	var ps []core.Project
	a.call("project.list", nil, &ps)
	var out []string
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func TestBackupSettingsWarnAboutSyncFoldersAndDefaultToDataDir(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	a := open(t, dataDir)
	var s core.BackupSettings
	a.call("backup.settings.get", nil, &s)
	if !s.Enabled || s.Dir != filepath.Join(dataDir, "backups") || s.Daily != 7 || s.Weekly != 4 || s.Warning != "" {
		t.Fatalf("defaults %+v", s)
	}
	sync := filepath.Join(t.TempDir(), "OneDrive", "Drydock backups")
	a.call("backup.settings.set", map[string]any{"dir": sync, "daily": 3, "weekly": 1}, &s)
	if s.Dir != sync || s.Daily != 3 || s.Weekly != 1 || !strings.Contains(s.Warning, "cloud-sync") {
		t.Fatalf("sync folder: %+v", s)
	}
	if err := a.callErr("backup.settings.set", map[string]any{"dir": "relative/path"}); err == nil {
		t.Error("a relative backup folder was accepted")
	}
	if err := a.callErr("backup.settings.set", map[string]any{"daily": 0}); err == nil {
		t.Error("0 daily backups was accepted")
	}
	s = core.BackupSettings{}
	a.call("backup.settings.set", map[string]any{"dir": ""}, &s)
	if s.Dir != filepath.Join(dataDir, "backups") || s.Warning != "" {
		t.Fatalf("reset to default: %+v", s)
	}
}

func TestBackupNowListVerifyAndTamperDetection(t *testing.T) {
	a := open(t, "")
	gitProject(t, a, "gocalc")
	var res core.BackupResult
	a.call("backup.now", nil, &res)
	if res.Backup.Name == "" || res.Backup.Size == 0 || res.Backup.SHA256 == "" {
		t.Fatalf("backup %+v", res)
	}
	dir, list := a.backups()
	if len(list) != 1 || list[0].Name != res.Backup.Name || !strings.HasPrefix(dir, a.dataDir()) {
		t.Fatalf("list %v in %s", list, dir)
	}
	var v struct {
		OK             bool  `json:"ok"`
		SchemaVersion  int64 `json:"schemaVersion"`
		RestorableHere bool  `json:"restorableHere"`
	}
	a.call("backup.verify", map[string]string{"name": res.Backup.Name}, &v)
	if !v.OK || v.SchemaVersion < 80 || !v.RestorableHere {
		t.Fatalf("verify %+v", v)
	}
	if err := a.callErr("backup.verify", map[string]string{"name": "../../etc/passwd"}); err == nil {
		t.Fatal("a path was accepted as a backup name")
	}
	// flip a byte: verify reports it, restore refuses to stage it
	f, _ := os.OpenFile(res.Backup.Path, os.O_RDWR, 0)
	f.WriteAt([]byte{0xAA, 0xBB, 0xCC, 0xDD}, 100)
	f.Close()
	var bad struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	a.call("backup.verify", map[string]string{"name": res.Backup.Name}, &bad)
	if bad.OK || !strings.Contains(bad.Error, "SHA-256") {
		t.Fatalf("tampered backup verified: %+v", bad)
	}
	if err := a.callErr("backup.restore", map[string]any{"name": res.Backup.Name, "confirm": true}); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("a damaged backup was staged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.dataDir(), "restore.pending")); err == nil {
		t.Fatal("a marker was written for a damaged backup")
	}
}

// The scheduler: daily when idle, 7 daily and 4 weekly kept, the backup folder is the configured one.
func TestBackupSchedulerRunsDailyAndPrunes(t *testing.T) {
	a := open(t, "")
	gitProject(t, a, "gocalc")
	clk := &bkClock{t: time.Date(2026, 3, 1, 3, 0, 0, 0, time.UTC)}
	a.c.TestSetBackupClock(clk.now)
	custom := filepath.Join(t.TempDir(), "my backups")
	a.call("backup.settings.set", map[string]any{"dir": custom, "daily": 3, "weekly": 1}, nil)

	tick := func() { a.c.TestBackupTick(context.Background()) }
	tick()
	if _, l := a.backups(); len(l) != 1 || l[0].Label != "scheduled" || !strings.HasPrefix(l[0].Path, custom) {
		t.Fatalf("first tick: %v", l)
	}
	tick() // the same day: nothing new
	if _, l := a.backups(); len(l) != 1 {
		t.Fatalf("a second backup the same day: %d", len(l))
	}
	clk.t = clk.t.Add(2 * time.Hour)
	tick()
	if _, l := a.backups(); len(l) != 1 {
		t.Fatalf("two hours later: %d", len(l))
	}
	for day := 1; day <= 12; day++ { // twelve more days
		clk.t = clk.t.Add(24 * time.Hour)
		tick()
	}
	_, l := a.backups()
	// 3 newest days + the newest backup of 1 older ISO week
	if len(l) != 4 {
		t.Fatalf("after 13 days with daily=3 weekly=1 expected 4 backups, have %d: %v", len(l), names(l))
	}
	var s core.BackupSettings
	a.call("backup.settings.get", nil, &s)
	if s.LastSuccess == 0 || s.LastError != "" {
		t.Fatalf("settings %+v", s)
	}

	// disabled: nothing happens
	a.call("backup.settings.set", map[string]any{"enabled": false}, nil)
	clk.t = clk.t.Add(48 * time.Hour)
	tick()
	if _, l2 := a.backups(); len(l2) != len(l) {
		t.Fatal("a disabled scheduler took a backup")
	}
	a.call("backup.settings.set", map[string]any{"enabled": true}, nil)
}

func names(l []backup.Info) []string {
	var out []string
	for _, b := range l {
		out = append(out, b.Name)
	}
	return out
}

// A failed backup is never silent: setting, event, SYSTEM message and a Needs-you item; and it is retried later.
func TestBackupFailureIsReportedEverywhere(t *testing.T) {
	a := open(t, "")
	p := gitProject(t, a, "gocalc")
	clk := &bkClock{t: time.Date(2026, 3, 1, 3, 0, 0, 0, time.UTC)}
	a.c.TestSetBackupClock(clk.now)
	dir := filepath.Join(t.TempDir(), "bk")
	a.call("backup.settings.set", map[string]any{"dir": dir}, nil)
	// the folder disappears and a file takes its place (a full disk, a revoked share and an unplugged drive look alike)
	os.RemoveAll(dir)
	os.WriteFile(dir, []byte("not a directory"), 0o644)

	a.c.TestBackupTick(context.Background())
	var s core.BackupSettings
	a.call("backup.settings.get", nil, &s)
	if s.LastError == "" || s.LastSuccess != 0 {
		t.Fatalf("the failure was not recorded: %+v", s)
	}
	a.waitFor("backup.failed event", func() bool {
		evs, _ := a.c.Replay(context.Background(), 0, events.Filter{Types: []string{"backup.failed"}}, 10)
		return len(evs) == 1
	})
	alerts := a.alertsRoom(p.ID)
	var msgs []room.Message
	a.call("room.messages", map[string]any{"roomId": alerts, "limit": 50}, &msgs)
	var system, question bool
	for _, m := range msgs {
		if m.Type == room.TypeSystem && strings.Contains(m.Body, "scheduled backup failed") {
			system = true
		}
		if m.Type == room.TypeQuestion && strings.Contains(m.Body, "scheduled backup failed") {
			question = true
		}
	}
	if !system || !question {
		t.Fatalf("system message %v, needs-you question %v in %d messages", system, question, len(msgs))
	}
	var needs []room.NeedsYouItem
	a.call("needsyou.list", map[string]string{"projectId": p.ID}, &needs)
	found := false
	for _, n := range needs {
		if strings.Contains(n.Title, "backup failed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no Needs-you item: %+v", needs)
	}
	// not retried within the retry window, and no second notice that day
	a.c.TestBackupTick(context.Background())
	evs, _ := a.c.Replay(context.Background(), 0, events.Filter{Types: []string{"backup.failed"}}, 10)
	if len(evs) != 1 {
		t.Fatalf("retried immediately: %d failures", len(evs))
	}
	// the folder is fixed; after the retry delay the backup succeeds and the error clears
	os.Remove(dir)
	a.c.TestSetBackupRetry(0)
	a.c.TestBackupTick(context.Background())
	s = core.BackupSettings{}
	a.call("backup.settings.get", nil, &s)
	if s.LastError != "" || s.LastSuccess == 0 {
		t.Fatalf("not recovered: %+v", s)
	}
	if _, l := a.backups(); len(l) != 1 {
		t.Fatalf("backups %v", names(l))
	}
	// a manual backup that fails also reports (and returns the error to the caller)
	os.RemoveAll(dir)
	os.WriteFile(dir, []byte("x"), 0o644)
	if err := a.callErr("backup.now", nil); err == nil {
		t.Fatal("a failed manual backup returned no error")
	}
}

// Restore end to end: stage, restart, and the database is the backup's; the old one is kept; running it twice is harmless.
func TestRestoreIsStagedAndAppliedBeforeTheDatabaseOpens(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	a := open(t, dataDir)
	p := gitProject(t, a, "Alpha")
	var res core.BackupResult
	a.call("backup.now", nil, &res)
	gitProject(t, a, "Beta (made after the backup)")
	if got := a.projectNames(); len(got) != 2 {
		t.Fatalf("projects before: %v", got)
	}

	if err := a.callErr("backup.restore", map[string]any{"name": res.Backup.Name}); err == nil || !strings.Contains(err.Error(), "Confirm") {
		t.Fatalf("restore without confirmation: %v", err)
	}
	if err := a.callErr("backup.restore", map[string]any{"name": "drydock-nope.db", "confirm": true}); err == nil {
		t.Fatal("restoring a backup that does not exist")
	}
	var staged map[string]any
	a.call("backup.restore", map[string]any{"name": res.Backup.Name, "confirm": true}, &staged)
	if staged["staged"] != true || staged["restartRequired"] != true {
		t.Fatalf("staged %v", staged)
	}
	var st map[string]any
	a.call("backup.restore.status", nil, &st)
	if st["pending"] != true {
		t.Fatalf("status %v", st)
	}
	// nothing has changed yet: the running app keeps its database until it restarts
	if got := a.projectNames(); len(got) != 2 {
		t.Fatalf("the restore was applied while the app was running: %v", got)
	}
	a.c.Close()

	b := openAt(t, dataDir)
	got := b.projectNames()
	if len(got) != 1 || got[0] != "Alpha" {
		t.Fatalf("after restart the projects are %v, want only Alpha", got)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "restore.pending")); err == nil {
		t.Fatal("the marker was not consumed")
	}
	dir, _ := b.backups()
	var kept []string
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "replaced-") && strings.HasSuffix(e.Name(), ".db") {
			kept = append(kept, e.Name())
		}
	}
	if len(kept) != 1 {
		t.Fatalf("the replaced database was not kept: %v", ents)
	}
	// the human is told
	b.waitFor("restore notice", func() bool {
		var msgs []room.Message
		b.call("room.messages", map[string]any{"roomId": b.alertsRoom(p.ID), "limit": 50}, &msgs)
		for _, m := range msgs {
			if strings.Contains(m.Body, "Restored from backup") && strings.Contains(m.Body, "replaced-") {
				return true
			}
		}
		return false
	})
	// and the replaced database is a real, openable copy that still has Beta
	info := backup.Info{Path: filepath.Join(dir, kept[0]), Name: kept[0]}
	if err := backup.Verify(context.Background(), info); err != nil {
		t.Fatalf("replaced database: %v", err)
	}
	// restarting again does not re-apply anything
	b.c.Close()
	c := openAt(t, dataDir)
	if got := c.projectNames(); len(got) != 1 {
		t.Fatalf("second restart: %v", got)
	}
}

// A staged restore that cannot be applied leaves the current data untouched and says so.
func TestFailedRestoreKeepsTheCurrentDatabase(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	a := open(t, dataDir)
	p := gitProject(t, a, "Alpha")
	a.c.Close()
	// a marker pointing at a file that is not a backup (it is missing)
	marker := `{"backupPath": "` + filepath.ToSlash(filepath.Join(dataDir, "backups", "drydock-20260101T000000Z-manual.db")) + `", "name": "drydock-20260101T000000Z-manual.db", "backupsDir": "` + filepath.ToSlash(filepath.Join(dataDir, "backups")) + `"}`
	if err := os.WriteFile(filepath.Join(dataDir, "restore.pending"), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	b := openAt(t, dataDir)
	if got := b.projectNames(); len(got) != 1 || got[0] != "Alpha" {
		t.Fatalf("a failed restore changed the data: %v", got)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "restore.pending")); err == nil {
		t.Fatal("the failed marker would be retried forever")
	}
	b.waitFor("failure notice", func() bool {
		var msgs []room.Message
		b.call("room.messages", map[string]any{"roomId": b.alertsRoom(p.ID), "limit": 50}, &msgs)
		for _, m := range msgs {
			if strings.Contains(m.Body, "restore was not applied") && strings.Contains(m.Body, "unchanged") {
				return true
			}
		}
		return false
	})
}

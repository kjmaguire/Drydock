package updatepolicy_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/backup"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/updatepolicy"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.4.0", "1.3.9", 1}, {"1.4.0", "1.4.0", 0}, {"1.4.0", "2.0.0", -1}, {"v1.4.0", "1.4.0", 0},
		{"1.5.0-beta.2", "1.5.0", -1}, {"1.5.0-beta.2", "1.5.0-beta.10", -1}, {"1.5.0-beta.2", "1.5.0-alpha.9", 1},
		{"1.5.0-rc.1", "1.4.9", 1}, {"1.0.0+build5", "1.0.0", 0},
	}
	for _, c := range cases {
		got, err := updatepolicy.CompareVersions(c.a, c.b)
		if err != nil || got != c.want {
			t.Errorf("%s vs %s = %d (%v), want %d", c.a, c.b, got, err, c.want)
		}
	}
	if _, err := updatepolicy.CompareVersions("1.4", "1.4.0"); err == nil {
		t.Error("a two-part version must be rejected")
	}
}

func TestChecklistBlocksUnsafeUpdates(t *testing.T) {
	stable := func(ch string) bool { return ch == "stable" }
	ok := updatepolicy.State{Current: "1.4.0", Candidate: "1.5.0", Channel: "stable", AllowedChannels: stable}
	if b := updatepolicy.Check(ok); len(b) != 0 {
		t.Fatalf("a clean upgrade was blocked: %v", b)
	}
	cases := map[string]updatepolicy.State{
		"channel":   {Current: "1.4.0", Candidate: "1.5.0-beta.1", Channel: "beta", AllowedChannels: stable},
		"downgrade": {Current: "1.4.0", Candidate: "1.3.0", Channel: "stable", AllowedChannels: stable},
		"same":      {Current: "1.4.0", Candidate: "1.4.0", Channel: "stable", AllowedChannels: stable},
		"runs":      {Current: "1.4.0", Candidate: "1.5.0", Channel: "stable", AllowedChannels: stable, ActiveRuns: 2},
		"agents":    {Current: "1.4.0", Candidate: "1.5.0", Channel: "stable", AllowedChannels: stable, LiveAgents: 1},
	}
	for name, s := range cases {
		if b := updatepolicy.Check(s); len(b) == 0 {
			t.Errorf("%s was not blocked", name)
		}
	}
	// an explicit rollback is the only way to a lower version
	rb := updatepolicy.State{Current: "1.4.0", Candidate: "1.3.0", Channel: "stable", AllowedChannels: stable, Rollback: true}
	if b := updatepolicy.Check(rb); len(b) != 0 {
		t.Fatalf("rollback blocked: %v", b)
	}
}

func TestPrepareKeepsBackupAndPreviousExecutableAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "drydock.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exe := filepath.Join(dir, "Drydock.exe")
	os.WriteFile(exe, []byte("MZ-old-version"), 0o755)

	p, err := updatepolicy.Prepare(ctx, db, dir, exe, "1.4.0", "1.5.0", time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p.PreviousExe); string(b) != "MZ-old-version" || !strings.Contains(p.PreviousExe, filepath.Join("previous", "1.4.0")) {
		t.Fatalf("previous exe %q", p.PreviousExe)
	}
	if !p.Backup.Keep || !strings.Contains(p.Backup.Name, "pre-update-1.4.0-to-1.5.0") {
		t.Fatalf("backup %+v", p.Backup)
	}
	// the pre-update backup survives pruning until the new version is confirmed healthy
	if _, err := backup.Prune(filepath.Join(dir, "backups"), 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Backup.Path); err != nil {
		t.Fatal("pre-update backup was pruned before confirmation")
	}
	if err := updatepolicy.Confirm(p); err != nil {
		t.Fatal(err)
	}

	// a missing executable stops the update before anything is applied
	if _, err := updatepolicy.Prepare(ctx, db, dir, filepath.Join(dir, "nope.exe"), "1.4.0", "1.5.0", time.Now()); err == nil {
		t.Fatal("Prepare must fail when the previous version cannot be kept")
	}
}

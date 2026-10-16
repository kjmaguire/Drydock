package updatepolicy_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/updatepolicy"
)

func prepared(t *testing.T, db *storage.DB, dir, from, to string, now time.Time) updatepolicy.Prepared {
	t.Helper()
	exe := filepath.Join(dir, "drydock.exe")
	if err := os.WriteFile(exe, []byte("the running executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := updatepolicy.Prepare(context.Background(), db, dir, exe, from, to, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Backup.Path + ".keep"); err != nil {
		t.Fatalf("the pre-update backup is not exempt from pruning: %v", err)
	}
	return p
}

func keepMarker(p updatepolicy.Prepared) bool {
	_, err := os.Stat(p.Backup.Path + ".keep")
	return err == nil
}

// The pre-update backup stays out of pruning until the new version ran healthy; nothing ever said so, and each update left a
// database-sized backup behind for good (audit 2026-10-16, M13).
func TestAPreUpdateBackupIsReleasedWhenTheNewVersionHasRunHealthy(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.OpenTemp(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	p := prepared(t, db, dir, "1.0.0", "1.1.0", now)
	if err := updatepolicy.Record(ctx, db, p, "1.0.0", "1.1.0", now); err != nil {
		t.Fatal(err)
	}
	// the old version is still running (the update did not take): the backup stays for the rollback
	if released, err := updatepolicy.ConfirmHealthy(ctx, db, "1.0.0", now.Add(time.Hour)); err != nil || released || !keepMarker(p) {
		t.Fatalf("released %v err %v keep %v", released, err, keepMarker(p))
	}
	// the new version runs: released, once
	if released, err := updatepolicy.ConfirmHealthy(ctx, db, "1.1.0", now.Add(time.Hour)); err != nil || !released || keepMarker(p) {
		t.Fatalf("released %v err %v keep %v", released, err, keepMarker(p))
	}
	if released, err := updatepolicy.ConfirmHealthy(ctx, db, "1.1.0", now.Add(2*time.Hour)); err != nil || released {
		t.Fatalf("the same update was confirmed twice: %v %v", released, err)
	}
	// nothing pending: nothing happens
	if released, err := updatepolicy.ConfirmHealthy(ctx, db, "9.9.9", now); err != nil || released {
		t.Fatalf("%v %v", released, err)
	}
}

func TestAnUpdateThatNeverTakesDoesNotKeepItsBackupForEver(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.OpenTemp(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	p := prepared(t, db, dir, "1.0.0", "1.1.0", now)
	if err := updatepolicy.Record(ctx, db, p, "1.0.0", "1.1.0", now); err != nil {
		t.Fatal(err)
	}
	if released, _ := updatepolicy.ConfirmHealthy(ctx, db, "1.0.0", now.Add(updatepolicy.MaxPending-time.Hour)); released {
		t.Fatal("released too early")
	}
	if released, err := updatepolicy.ConfirmHealthy(ctx, db, "1.0.0", now.Add(updatepolicy.MaxPending+time.Hour)); err != nil || !released || keepMarker(p) {
		t.Fatalf("an update that never took kept its backup past %v: released %v err %v", updatepolicy.MaxPending, released, err)
	}
}

func TestANewerPendingUpdateReleasesTheBackupOfTheOlderOne(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.OpenTemp(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	first := prepared(t, db, dir, "1.0.0", "1.1.0", now)
	if err := updatepolicy.Record(ctx, db, first, "1.0.0", "1.1.0", now); err != nil {
		t.Fatal(err)
	}
	second := prepared(t, db, dir, "1.0.0", "1.2.0", now.Add(time.Minute))
	if err := updatepolicy.Record(ctx, db, second, "1.0.0", "1.2.0", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if keepMarker(first) || !keepMarker(second) {
		t.Fatalf("first kept %v, second kept %v", keepMarker(first), keepMarker(second))
	}
}

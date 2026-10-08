package backup_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/backup"
	"github.com/kjmaguire/drydock/internal/storage"
)

func openDB(t *testing.T) (*storage.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "drydock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, dir
}

func count(t *testing.T, path string) int {
	t.Helper()
	// open a copy: opening a database (WAL mode, migrations) writes to it, and the backup itself must stay untouched
	cp := filepath.Join(t.TempDir(), "copy.db")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(cp, b, 0o644)
	db, err := storage.Open(context.Background(), cp)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.Reader().QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

var t0 = time.Date(2026, 10, 8, 3, 0, 0, 0, time.UTC)

func TestBackupIsConsistentVerifiedAndRestorable(t *testing.T) {
	ctx := context.Background()
	db, dir := openDB(t)
	db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p1','one','/r',1)`)
	bdir := filepath.Join(dir, "backups")
	in, err := backup.Create(ctx, db, bdir, "scheduled", t0)
	if err != nil {
		t.Fatal(err)
	}
	if in.SHA256 == "" || in.Size == 0 {
		t.Fatalf("%+v", in)
	}
	// data written after the snapshot is not in it
	db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p2','two','/r',2)`)
	if got := count(t, in.Path); got != 1 {
		t.Fatalf("snapshot has %d projects, want the 1 that existed", got)
	}
	if err := backup.Verify(ctx, in); err != nil {
		t.Fatal(err)
	}
	// corruption is detected by the recorded hash
	b, _ := os.ReadFile(in.Path)
	bad := in
	bad.Path = filepath.Join(bdir, "drydock-20260101T000000Z-copy.db")
	b[len(b)-10] ^= 0xFF
	os.WriteFile(bad.Path, b, 0o644)
	if err := backup.Verify(ctx, bad); err == nil {
		t.Fatal("a modified backup verified")
	}

	// restore: close the database first, as the app does
	dbPath := db.Path()
	db.Close()
	replaced, err := backup.Restore(ctx, in, dbPath, bdir, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got := count(t, replaced); got != 2 {
		t.Fatalf("the replaced database must be kept intact (2 projects), has %d", got)
	}
	if got := count(t, dbPath); got != 1 {
		t.Fatalf("restored database has %d projects, want 1", got)
	}
}

func TestRestoreRefusesACorruptBackupAndChangesNothing(t *testing.T) {
	ctx := context.Background()
	db, dir := openDB(t)
	db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p1','one','/r',1)`)
	in, err := backup.Create(ctx, db, filepath.Join(dir, "b"), "manual", t0)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(in.Path)
	b[len(b)/2] ^= 0xFF
	os.WriteFile(in.Path, b, 0o644)
	dbPath := db.Path()
	db.Close()
	if _, err := backup.Restore(ctx, in, dbPath, filepath.Join(dir, "b"), t0); err == nil {
		t.Fatal("restored a corrupt backup")
	}
	if got := count(t, dbPath); got != 1 {
		t.Fatalf("the live database was touched: %d", got)
	}
}

func TestPrunePolicyKeepsDailyWeeklyAndPreUpdate(t *testing.T) {
	ctx := context.Background()
	db, dir := openDB(t)
	bdir := filepath.Join(dir, "b")
	// 20 daily backups, one per day, plus a pre-update backup 15 days back
	for i := 0; i < 20; i++ {
		if _, err := backup.Create(ctx, db, bdir, "scheduled", t0.AddDate(0, 0, -i)); err != nil {
			t.Fatal(err)
		}
	}
	pre, err := backup.Create(ctx, db, bdir, "pre-update-1.0.0-to-1.1.0", t0.AddDate(0, 0, -15).Add(time.Hour))
	if err != nil || !pre.Keep {
		t.Fatalf("%+v %v", pre, err)
	}
	removed, err := backup.Prune(bdir, 7, 4)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := backup.List(bdir)
	var daily, other int
	havePre := false
	for _, b := range left {
		if b.Name == pre.Name {
			havePre = true
		}
		if b.Created.After(t0.AddDate(0, 0, -7)) {
			daily++
		} else {
			other++
		}
	}
	if !havePre {
		t.Fatal("the pre-update backup must be exempt from pruning")
	}
	if daily != 7 || other < 1 || other > 5 || len(removed) == 0 {
		t.Fatalf("kept %d recent and %d older (removed %d): %v", daily, other, len(removed), names(left))
	}
	// once the update is confirmed healthy the exemption ends
	if err := backup.Release(pre.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Prune(bdir, 7, 4); err != nil {
		t.Fatal(err)
	}
	left, _ = backup.List(bdir)
	for _, b := range left {
		if b.Name == pre.Name {
			t.Fatal("a released pre-update backup should be prunable")
		}
	}
}

func names(l []backup.Info) []string {
	var s []string
	for _, b := range l {
		s = append(s, b.Name)
	}
	return s
}

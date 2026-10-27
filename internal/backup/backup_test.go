package backup_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	// measured at t0, the time the backups were made: with the wall clock, the exemption ran out on 2026-10-23
	removed, err := backup.Prune(bdir, 7, 4, t0)
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
	if _, err := backup.Prune(bdir, 7, 4, t0); err != nil {
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

// A power cut can leave the hash file next to a backup empty or cut. Listing the backups runs 5 seconds after every start; it
// used to take the first word of whatever the file held and panicked on an empty one, ending the app at every start (audit
// 2026-10-16, H9). The backup is listed without a hash instead, and is still checked for integrity.
func TestListSurvivesAnEmptyOrCutHashFile(t *testing.T) {
	ctx := context.Background()
	db, dir := openDB(t)
	bdir := filepath.Join(dir, "backups")
	var made []backup.Info
	for i := 0; i < 4; i++ {
		in, err := backup.Create(ctx, db, bdir, "scheduled", t0.Add(time.Duration(i)*24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		made = append(made, in)
	}
	for i, content := range []string{"", "\n  \n", "deadbeef", "not a hash at all  drydock.db\n"} {
		if err := os.WriteFile(made[i].Path+".sha256", []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	list, err := backup.List(bdir)
	if err != nil || len(list) != 4 {
		t.Fatalf("%v %d", err, len(list))
	}
	for _, in := range list {
		if in.SHA256 != "" {
			t.Fatalf("%s was listed with the hash %q read from a damaged file", in.Name, in.SHA256)
		}
		if err := backup.Verify(ctx, in); err != nil {
			t.Fatalf("a backup with a damaged hash file is still a good backup: %v", err)
		}
	}
	// and a good hash file is still read
	if err := os.WriteFile(made[0].Path+".sha256", []byte(made[0].SHA256+"  "+made[0].Name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, _ = backup.List(bdir)
	var got string
	for _, in := range list {
		if in.Name == made[0].Name {
			got = in.SHA256
		}
	}
	if got != made[0].SHA256 {
		t.Fatalf("hash %q, want %q", got, made[0].SHA256)
	}
}

// A snapshot that fails leaves nothing behind, and the leftovers of earlier failures are swept (audit 2026-10-16, M13): the
// temporary file is as large as the database and a failure that repeats every hour left one each time.
func TestAFailedSnapshotLeavesNoTemporaryFileAndStaleOnesAreSwept(t *testing.T) {
	ctx := context.Background()
	db, dir := openDB(t)
	bdir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	// the snapshot cannot be put in place: its name is taken by a folder
	name := "drydock-20261008T030000Z-scheduled.db"
	if err := os.MkdirAll(filepath.Join(bdir, name, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Create(ctx, db, bdir, "scheduled", t0); err == nil {
		t.Fatal("the snapshot was put over a folder")
	}
	// older leftovers of failed snapshots and unfinished hash files, and one that is recent (another run may still write it)
	old := t0.Add(-3 * time.Hour)
	stale := []string{"drydock-20261001T030000Z-scheduled.db.partial", ".drydock-20261001T030000Z-scheduled.db.sha256.123.tmp"}
	for _, n := range stale {
		p := filepath.Join(bdir, n)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	fresh := filepath.Join(bdir, "drydock-20261008T025900Z-other.db.partial")
	if err := os.WriteFile(fresh, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fresh, t0.Add(time.Hour-time.Minute), t0.Add(time.Hour-time.Minute)); err != nil { // a minute before the run below
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(bdir, name)); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Create(ctx, db, bdir, "scheduled", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(bdir)
	var left []string
	for _, e := range ents {
		left = append(left, e.Name())
	}
	for _, n := range stale {
		if _, err := os.Stat(filepath.Join(bdir, n)); err == nil {
			t.Errorf("the stale %s was not swept (%v)", n, left)
		}
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a recent temporary file was removed: %v", err)
	}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".partial") && filepath.Join(bdir, e.Name()) != fresh {
			t.Errorf("a failed snapshot left %s", e.Name())
		}
	}
}

// A marker that nothing released (every update before the release was wired) does not keep a backup for ever.
func TestAPreUpdateMarkerExpires(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.OpenTemp(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bdir := filepath.Join(dir, "backups")
	old, err := backup.Create(ctx, db, bdir, "pre-update-1.0.0-to-1.1.0", t0.Add(-backup.MaxKeepAge-48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := backup.Create(ctx, db, bdir, "pre-update-1.1.0-to-1.2.0", t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	removed, err := backup.Prune(bdir, 0, 0, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != old.Name {
		t.Fatalf("removed %v, want only the expired %s", removed, old.Name)
	}
	if _, err := os.Stat(fresh.Path); err != nil {
		t.Fatalf("a fresh pre-update backup was pruned: %v", err)
	}
	for _, sfx := range []string{"", ".sha256", ".keep"} {
		if _, err := os.Stat(old.Path + sfx); err == nil {
			t.Errorf("%s survived", old.Path+sfx)
		}
	}
}

// The exemption is measured against the time Prune is given, not the wall clock: an unreleased marker made at t0 holds just
// before t0+MaxKeepAge and lapses at it; and a zero time is refused rather than exempting every marker for ever.
func TestPruneMeasuresTheExemptionAtTheGivenTime(t *testing.T) {
	ctx := context.Background()
	db, dir := openDB(t)
	bdir := filepath.Join(dir, "b")
	pre, err := backup.Create(ctx, db, bdir, "pre-update-1.0.0-to-1.1.0", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Prune(bdir, 0, 0, time.Time{}); err == nil {
		t.Fatal("a zero time was accepted")
	}
	if removed, err := backup.Prune(bdir, 0, 0, t0.Add(backup.MaxKeepAge-time.Second)); err != nil || len(removed) != 0 {
		t.Fatalf("inside the exemption: removed %v, %v", removed, err)
	}
	removed, err := backup.Prune(bdir, 0, 0, t0.Add(backup.MaxKeepAge))
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != pre.Name {
		t.Fatalf("at MaxKeepAge: removed %v, want %s", removed, pre.Name)
	}
}

// A backup the person took (Back up now, before a risky change) used to share its day's one place with the scheduled backup, so
// the next backup that day deleted it (review 2026-10-27). It now stays for the daily window, and then goes like any other.
func TestPruneKeepsABackupThePersonTookThatDay(t *testing.T) {
	ctx := context.Background()
	db, dir := openDB(t)
	bdir := filepath.Join(dir, "b")
	mine, err := backup.Create(ctx, db, bdir, "manual", t0.Add(-3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	sched, err := backup.Create(ctx, db, bdir, backup.ScheduledLabel, t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	removed, err := backup.Prune(bdir, 7, 4, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed %v the same day", removed)
	}
	left, _ := backup.List(bdir)
	if got := names(left); len(got) != 2 {
		t.Fatalf("left %v, want %s and %s", got, mine.Name, sched.Name)
	}
	// eight days later, with a newer scheduled backup each day, it has had its time
	for i := 1; i <= 8; i++ {
		if _, err := backup.Create(ctx, db, bdir, backup.ScheduledLabel, t0.AddDate(0, 0, i)); err != nil {
			t.Fatal(err)
		}
	}
	removed, err = backup.Prune(bdir, 7, 0, t0.AddDate(0, 0, 8).Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	gone := false
	for _, n := range removed {
		gone = gone || n == mine.Name
	}
	if !gone {
		t.Fatalf("the person's backup outlived the daily window: removed %v", removed)
	}
}

package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSchemaVersions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := OpenTemp(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	latest := LatestVersion()
	if latest < 80 {
		t.Fatalf("latest embedded migration %d", latest)
	}
	v, err := db.SchemaVersion(ctx)
	if err != nil || v != latest {
		t.Fatalf("database at %d (%v), want %d", v, err, latest)
	}
	// a file can be inspected without opening it as a Drydock database (read-only, no migration)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	fv, err := FileSchemaVersion(ctx, filepath.Join(dir, "drydock.db"))
	if err != nil || fv != latest {
		t.Fatalf("file at %d (%v)", fv, err)
	}
	junk := filepath.Join(dir, "junk.db")
	os.WriteFile(junk, []byte("this is not sqlite at all, just text, long enough to be read as a header......"), 0o644)
	if _, err := FileSchemaVersion(ctx, junk); err == nil {
		t.Fatal("a non-database file reported a version")
	}
}

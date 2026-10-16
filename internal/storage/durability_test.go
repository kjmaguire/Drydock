package storage

import (
	"context"
	"testing"
)

// The database's durability is what the docs say (audit 2026-10-16, L21): WAL mode with synchronous=NORMAL on the writer, so a
// commit survives the app crashing and the file survives a power cut. A change to either must change the documents with it
// (docs/packaging.md "How durable a commit is", events.Bus.Publish).
func TestTheDatabaseRunsInWALModeWithSynchronousNormal(t *testing.T) {
	db, err := OpenTemp(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mode string
	var sync int
	if err := db.Writer().QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal mode %q %v", mode, err)
	}
	if err := db.Writer().QueryRow(`PRAGMA synchronous`).Scan(&sync); err != nil || sync != 1 { // 1 is NORMAL, 2 is FULL
		t.Fatalf("synchronous %d %v", sync, err)
	}
}

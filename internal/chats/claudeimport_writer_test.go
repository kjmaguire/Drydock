package chats

import (
	"context"
	"testing"
	"time"
)

// An imported session is scanned for secrets before it is written: the scan of every line of a long session inside the write
// transaction held the app's one writer connection, and every other write (a message sent, an event published) waited for it.
func TestImportScansBeforeHoldingTheWriter(t *testing.T) {
	ctx := context.Background()
	f := newImportFixture(t)
	s, db := newImportService(t, f)
	scan := s.d.Redact
	var scans int
	var blocked error
	s.d.Redact = func(text string) string {
		scans++
		wctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := db.Writer().ExecContext(wctx, `UPDATE chats SET title=title WHERE id=''`); err != nil && blocked == nil {
			blocked = err
		}
		return scan(text)
	}
	res, err := s.ImportClaudeCode(ctx, ImportReq{Sessions: []ImportRef{{SessionID: sessA}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Imported) != 1 || res.Imported[0].Messages == 0 {
		t.Fatalf("the import: %+v", res)
	}
	if scans == 0 {
		t.Fatal("the session was not scanned")
	}
	if blocked != nil {
		t.Fatalf("a write waited for the import's transaction while it scanned (%d scans): %v", scans, blocked)
	}
}

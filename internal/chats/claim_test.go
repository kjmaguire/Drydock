package chats

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/storage"
)

// A change committed between Send reading the chat and claiming it (the person moved the chat to another folder while the
// message was on its way) is kept: the turn claims the chat as it is now, not as it was read, so the folder the person chose
// is not written back to the old one (audit 2026-10-27).
func TestATurnClaimsTheChatAsItIsNotAsItWasRead(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	before, after := t.TempDir(), t.TempDir()
	// no adapter is registered in the runtime: the turn fails to start once claimed, which is all this test needs
	s := New(Deps{DB: db, Runtime: &rt.Runtime{}, Capabilities: func(string) (agents.Capabilities, bool) { return agents.Capabilities{}, true }}, Options{})
	defer s.Close()
	c, err := s.Create(ctx, CreateReq{Cwd: before})
	if err != nil {
		t.Fatal(err)
	}
	moved := 0
	s.beforeClaim = func(id string) {
		if moved > 0 {
			return
		}
		moved++
		// what chat.update commits when the person moves the chat: the folder, the session dropped, the version raised
		if _, err := db.Writer().ExecContext(ctx, `UPDATE chats SET cwd=?, session_id='', version=version+1 WHERE id=?`, after, id); err != nil {
			t.Error(err)
		}
	}
	if _, err := s.Send(ctx, SendReq{ID: c.ID, Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	s.Close() // the turn has recorded its end
	got, err := s.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cwd != after {
		t.Fatalf("the chat runs in %s again; the person moved it to %s", got.Cwd, after)
	}
	var turns int
	_ = db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM chat_turns WHERE chat_id=?`, c.ID).Scan(&turns)
	if turns != 1 {
		t.Fatalf("%d turns, want the one", turns)
	}
}

// A fork that fails after copying the files of its messages leaves none of them on disk: they were copied into the new chat's
// own folder inside the fork's transaction, which no row names once it rolled back (audit 2026-10-27).
func TestAFailedForkLeavesNoCopiedFiles(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	s := New(Deps{DB: db, Runtime: &rt.Runtime{}, Capabilities: func(string) (agents.Capabilities, bool) { return agents.Capabilities{}, true },
		AttachDir: func(id string) string { return filepath.Join(root, id) }}, Options{})
	defer s.Close()
	c, err := s.Create(ctx, CreateReq{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	files := []pendingFile{{att: Attachment{ID: "catt_one", Name: "notes.txt", MIME: "text/plain", Size: 5, Kind: KindText}, data: []byte("notes")}}
	if err := s.storeFiles(c.ID, files); err != nil {
		t.Fatal(err)
	}
	if _, err := s.insertMessage(ctx, c, "", RoleUser, "see the notes", "", nil, msgExtra{files: files}); err != nil {
		t.Fatal(err)
	}
	// the fork's copy of the message is refused after its file was copied
	if _, err := db.Writer().ExecContext(ctx, `CREATE TRIGGER no_fork BEFORE INSERT ON chat_messages WHEN new.chat_id <> '`+c.ID+`' BEGIN SELECT RAISE(ABORT, 'refused by the test'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fork(ctx, c.ID, 0); err == nil {
		t.Fatal("the fork went through")
	}
	ents, _ := os.ReadDir(root)
	for _, e := range ents {
		if e.Name() != c.ID {
			left, _ := os.ReadDir(filepath.Join(root, e.Name()))
			t.Fatalf("the failed fork left %s with %d files", e.Name(), len(left))
		}
	}
}

// A chat deleted while a message to it is on its way answers the send with "chat not found", not the database's own words
// (review 2026-10-27: "sql: no rows in result set").
func TestASendToAChatDeletedMeanwhileSaysTheChatIsGone(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(Deps{DB: db, Runtime: &rt.Runtime{}, Capabilities: func(string) (agents.Capabilities, bool) { return agents.Capabilities{}, true }}, Options{})
	defer s.Close()
	c, err := s.Create(ctx, CreateReq{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s.beforeClaim = func(id string) {
		if _, err := db.Writer().ExecContext(ctx, `DELETE FROM chats WHERE id=?`, id); err != nil {
			t.Error(err)
		}
	}
	if _, err := s.Send(ctx, SendReq{ID: c.ID, Text: "hello"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("send: %v", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.running) != 0 {
		t.Fatalf("the chat is still held: %v", s.running)
	}
}

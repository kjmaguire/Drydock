package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/kjmaguire/drydock/internal/storage"
)

func newStore(t *testing.T) Store {
	t.Helper()
	db, err := storage.OpenTemp(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, p := range []string{"p1", "p2"} {
		if _, err := db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES(?,?,'/x',1)`, p, p); err != nil {
			t.Fatal(err)
		}
	}
	return Store{DB: db}
}

func TestGetDeleteFindAndWorkspaceSearch(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a, _ := s.Add(ctx, Entry{ProjectID: "p1", Kind: Decision, Title: "Use D1", Body: "All data lives in D1."}, true)
	b, _ := s.Add(ctx, Entry{ProjectID: "p1", Kind: Convention, Title: "D1 proposal", Body: "Proposed D1 convention."}, false)
	c, _ := s.Add(ctx, Entry{ProjectID: "p2", Kind: Decision, Title: "Other project D1", Body: "D1 elsewhere."}, true)

	got, err := s.Get(ctx, a.ID)
	if err != nil || got.Title != "Use D1" {
		t.Fatalf("%v %+v", err, got)
	}
	if _, err := s.Get(ctx, "mem_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	// Search (agents) sees accepted entries of one project; Find (the memory page) can see every status and every project
	if r, _ := s.Search(ctx, "p1", "D1", 10); len(r) != 1 || r[0].ID != a.ID {
		t.Fatalf("%+v", r)
	}
	if r, _ := s.Find(ctx, "p1", "D1", "", 10); len(r) != 2 {
		t.Fatalf("%+v", r)
	}
	if r, _ := s.Find(ctx, "p1", "D1", Proposed, 10); len(r) != 1 || r[0].ID != b.ID {
		t.Fatalf("%+v", r)
	}
	if r, _ := s.Find(ctx, "", "D1", "", 10); len(r) != 3 {
		t.Fatalf("%+v", r)
	}
	if r, _ := s.SearchWorkspace(ctx, "D1", 10); len(r) != 2 {
		t.Fatalf("workspace search is accepted entries of every project: %+v", r)
	}
	if r, _ := s.Find(ctx, "p1", "  ", "", 10); r != nil {
		t.Fatal("empty query")
	}
	// delete removes the row and its FTS entry
	if err := s.Delete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if r, _ := s.SearchWorkspace(ctx, "D1", 10); len(r) != 1 {
		t.Fatalf("%+v", r)
	}
	if !ValidKind(Preference) || ValidKind("nope") {
		t.Fatal("ValidKind")
	}
}

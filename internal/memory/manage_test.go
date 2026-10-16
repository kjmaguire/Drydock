package memory

import (
	"context"
	"errors"
	"strings"
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

// Search drops common words (a question does not match every entry through "the" and "how") and ranks a title match above
// a body mention, the same weighting the memory page and the expert packs use.
func TestSearchIgnoresCommonWordsAndWeighsTitles(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	body, _ := s.Add(ctx, Entry{ProjectID: "p1", Kind: Decision, Title: "Logging", Body: "Logs go to the shared collector; the deploy script ships them."}, true)
	title, _ := s.Add(ctx, Entry{ProjectID: "p1", Kind: Decision, Title: "How we deploy", Body: "Through the pipeline, never by hand."}, true)
	_, _ = s.Add(ctx, Entry{ProjectID: "p1", Kind: Convention, Title: "Naming", Body: "The packages are named for what they do."}, true)
	r, err := s.Search(ctx, "p1", "how do I deploy the worker", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 2 {
		t.Fatalf("common words matched unrelated entries: %+v", r)
	}
	if r[0].ID != title.ID || r[1].ID != body.ID {
		t.Fatalf("a title match must rank first: %q then %q", r[0].Title, r[1].Title)
	}
}

// A proposal is short enough to be read whole before it is accepted (audit 2026-10-16, M26).
func TestCheckSizeRefusesWhatCannotBeReadWhole(t *testing.T) {
	ok := Entry{Title: "t", Body: strings.Repeat("é", MaxBodyChars)}
	if err := CheckSize(ok); err != nil {
		t.Fatalf("exactly the limit in characters (not bytes): %v", err)
	}
	if err := CheckSize(Entry{Title: "t", Body: strings.Repeat("x", MaxBodyChars+1)}); err == nil || !strings.Contains(err.Error(), "split it") {
		t.Fatalf("a body over the limit: %v", err)
	}
	if err := CheckSize(Entry{Title: strings.Repeat("x", MaxTitleChars+1), Body: "b"}); err == nil {
		t.Fatal("a title over the limit")
	}
}

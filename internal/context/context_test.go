package context

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/memory"
	"github.com/kjmaguire/drydock/internal/storage"
)

func newEngine(t *testing.T) (*Engine, memory.Store) {
	t.Helper()
	db, err := storage.OpenTemp(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p1','demo','/x',1)`)
	ms := memory.Store{DB: db}
	return &Engine{DB: db, Memory: ms, Standing: func(context.Context, string) string { return "Prefer small PRs." }}, ms
}

func seedMemory(t *testing.T, ms memory.Store) {
	t.Helper()
	ctx := context.Background()
	ms.Add(ctx, memory.Entry{ProjectID: "p1", Kind: memory.Decision, Title: "Use D1 for storage", Body: "All persistent data lives in Cloudflare D1.\nNo other DB."}, true)
	ms.Add(ctx, memory.Entry{ProjectID: "p1", Kind: memory.Rejected, Title: "Durable Objects for items", Body: "Tried; too chatty for this workload."}, true)
	ms.Add(ctx, memory.Entry{ProjectID: "p1", Kind: memory.Convention, Title: "Proposed only", Body: "never shown"}, false)
}

func TestGenerateUsesOnlyAcceptedMemoryAndRecordsManifest(t *testing.T) {
	e, ms := newEngine(t)
	seedMemory(t, ms)
	files, m, err := e.Generate(context.Background(), "p1", nil)
	if err != nil {
		t.Fatal(err)
	}
	agents := files["AGENTS.md"]
	for _, want := range []string{"drydock:generated", "Prefer small PRs.", "Use D1 for storage", "Rejected approaches", "Durable Objects for items", "Deterministic tools answer before any model"} {
		if !strings.Contains(agents, want) {
			t.Errorf("AGENTS.md missing %q", want)
		}
	}
	if strings.Contains(agents, "never shown") {
		t.Fatal("proposed (unaccepted) memory must not reach agents")
	}
	if !strings.Contains(files["CLAUDE.md"], "@AGENTS.md") || !strings.Contains(files["GEMINI.md"], "@AGENTS.md") {
		t.Fatal("import files must point at AGENTS.md")
	}
	if strings.Contains(files["CLAUDE.md"], "Use D1") {
		t.Fatal("import files must not duplicate content")
	}
	if len(files) != 3 {
		t.Fatalf("files %v", files.Names())
	}
	srcs := map[string]bool{}
	for _, it := range m.Items {
		srcs[it.Source] = it.Included
	}
	if !srcs["standing instructions"] || !srcs["file:AGENTS.md"] {
		t.Fatalf("manifest %+v", m.Items)
	}
	// determinism: same inputs produce byte-identical files (cache-stable prefix)
	files2, _, _ := e.Generate(context.Background(), "p1", nil)
	if files["AGENTS.md"] != files2["AGENTS.md"] {
		t.Fatal("generation must be deterministic")
	}
}

func TestWriteImportsUserAuthoredFilesAndKeepsThemOutOfGit(t *testing.T) {
	ctx := context.Background()
	e, ms := newEngine(t)
	seedMemory(t, ms)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Repo rules\nAlways run `task test`.\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("x\n"), 0o644)
	repo, err := git.Init(ctx, dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	w, err := repo.CreateWorktree(ctx, git.WorktreeReq{TaskID: "task_1", Title: "t", Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := e.Write(ctx, "p1", w.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Imported) != 1 || res.Imported[0] != "AGENTS.md" {
		t.Fatalf("result %+v", res)
	}
	got, _ := os.ReadFile(filepath.Join(w.Path, "AGENTS.md"))
	if !strings.Contains(string(got), "Always run `task test`.") || !strings.Contains(string(got), "From the repository's own AGENTS.md") {
		t.Fatalf("the repo's own instructions must survive as an imported section:\n%s", got)
	}
	// the generated files must never be committed or shown as agent changes
	if err := repo.LocalIgnore(ctx, w, res.Written); err != nil {
		t.Fatal(err)
	}
	st, _ := repo.Status(ctx, w)
	if st.Dirty() {
		t.Fatalf("generated files leaked into git status: %+v", st)
	}
	os.WriteFile(filepath.Join(w.Path, "feature.go"), []byte("package x\n"), 0o644)
	if _, err := repo.Commit(ctx, w, "work"); err != nil {
		t.Fatal(err)
	}
	diff, _ := repo.Diff(ctx, w, "")
	if strings.Contains(diff, "AGENTS.md") || strings.Contains(diff, "CLAUDE.md") || !strings.Contains(diff, "feature.go") {
		t.Fatalf("diff must contain the agent's work and not Drydock's files:\n%s", diff)
	}
	// and the (clean) worktree can still be removed
	if err := repo.DeleteWorktree(ctx, w, false); err != nil {
		t.Fatalf("worktree with skip-worktree/excluded generated files should delete as clean: %v", err)
	}
}

func TestHumanEditsAreOfferedBackNotOverwritten(t *testing.T) {
	ctx := context.Background()
	e, ms := newEngine(t)
	dir := t.TempDir()
	if _, _, err := e.Write(ctx, "p1", dir); err != nil {
		t.Fatal(err)
	}
	// unchanged: regeneration just rewrites
	res, _, _ := e.Write(ctx, "p1", dir)
	if len(res.Edited) != 0 {
		t.Fatalf("no edit yet: %+v", res)
	}
	// a human edits the generated file
	p := filepath.Join(dir, "AGENTS.md")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, append(b, []byte("\nAlways write tests first.\n")...), 0o644)
	res, _, _ = e.Write(ctx, "p1", dir)
	if len(res.Edited) != 1 || res.Edited[0] != "AGENTS.md" {
		t.Fatalf("edit not detected: %+v", res)
	}
	after, _ := os.ReadFile(p)
	if !strings.Contains(string(after), "Always write tests first.") {
		t.Fatal("a human edit must not be overwritten")
	}
	props, _ := ms.List(ctx, "p1", memory.Proposed, "")
	if len(props) != 1 || !strings.Contains(props[0].Body, "Always write tests first.") {
		t.Fatalf("edit must be offered back as a memory proposal: %+v", props)
	}
}

func TestManifestRoundTripAndMemorySearch(t *testing.T) {
	ctx := context.Background()
	e, ms := newEngine(t)
	seedMemory(t, ms)
	var m Manifest
	m.Add("task", true, 400, "task spec", 1)
	m.Add("repo map", false, 0, "gated (decision D5)", 9)
	if err := e.SaveManifest(ctx, "run_1", "work", 1, "task_1", "a1", m); err != nil {
		t.Fatal(err)
	}
	got, err := e.LoadManifest(ctx, "run_1", "work", 1)
	if err != nil || len(got.Items) != 2 || got.Items[1].Included || got.IncludedBytes() != 400 {
		t.Fatalf("%+v %v", got, err)
	}
	hits, _ := ms.Search(ctx, "p1", "which database storage do we use?", 3)
	if len(hits) != 1 || hits[0].Title != "Use D1 for storage" {
		t.Fatalf("search %+v", hits)
	}
	if hits, _ := ms.Search(ctx, "p1", "proposed only", 3); len(hits) != 0 {
		t.Fatal("search covers accepted memory only")
	}
}

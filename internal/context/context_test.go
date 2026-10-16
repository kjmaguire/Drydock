package context

import (
	"context"
	"fmt"
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

// Accepted memory goes into AGENTS.md newest first up to MemoryBudget, each entry cut to MemoryEntryMax characters; the rest
// is named in the manifest as left out and AGENTS.md points to search_memory.
func TestAcceptedMemoryIsCappedNewestFirst(t *testing.T) {
	e, ms := newEngine(t)
	ctx := context.Background()
	long := strings.Repeat("word ", 400) // 2,000 characters
	if _, err := ms.Add(ctx, memory.Entry{ProjectID: "p1", Kind: memory.Decision, Title: "Oldest decision", Body: "OLDEST " + long}, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		ms.Add(ctx, memory.Entry{ProjectID: "p1", Kind: memory.Convention, Title: fmt.Sprintf("Convention %02d", i), Body: long}, true)
	}
	ms.Add(ctx, memory.Entry{ProjectID: "p1", Kind: memory.Rejected, Title: "Newest rejected approach", Body: "Do not retry the cache."}, true)
	files, m, err := e.Generate(ctx, "p1", nil)
	if err != nil {
		t.Fatal(err)
	}
	agents := files["AGENTS.md"]
	if !strings.Contains(agents, "**Newest rejected approach**: Do not retry the cache.") || strings.Contains(agents, "OLDEST") {
		t.Fatalf("the newest entries go in, the oldest is left out:\n%s", agents)
	}
	if !strings.Contains(agents, "... (cut; search_memory has the whole entry)") || strings.Contains(agents, long) {
		t.Fatal("a long entry is cut")
	}
	var memBytes, in, out int
	for _, l := range strings.Split(agents, "\n") {
		if strings.HasPrefix(l, "- **") {
			memBytes += len(l) + 1
		}
	}
	for _, it := range m.Items {
		if !strings.HasPrefix(it.Source, "memory:") {
			continue
		}
		if it.Included {
			in++
		} else {
			out++
			if !strings.Contains(it.Reason, "over the memory budget") {
				t.Errorf("%+v", it)
			}
		}
	}
	if memBytes > MemoryBudget || in+out != 22 || out == 0 || in < 10 {
		t.Fatalf("memory lines %d bytes (budget %d), %d in, %d out", memBytes, MemoryBudget, in, out)
	}
	if !strings.Contains(agents, fmt.Sprintf("%d older memory entries are not listed here: search_memory finds them.", out)) {
		t.Fatalf("AGENTS.md does not say where the rest is:\n%s", agents)
	}
}

func TestAWorktreeLinkOutOfTheFolderIsNeitherReadNorWrittenThrough(t *testing.T) {
	ctx := context.Background()
	e, _ := newEngine(t)
	outside := t.TempDir()
	victim := filepath.Join(outside, "database")
	os.WriteFile(victim, []byte("USER-DATA-NOT-INSTRUCTIONS"), 0o644)
	dir := t.TempDir()
	if err := os.Symlink(victim, filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "GEMINI.md")); err != nil {
		t.Fatal(err)
	}
	res, _, err := e.Write(ctx, "p1", dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "USER-DATA-NOT-INSTRUCTIONS" {
		t.Fatalf("the file behind the link was overwritten: %q", b)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if strings.Contains(string(got), "USER-DATA") || !strings.HasPrefix(string(got), headerPrefix) {
		t.Fatalf("the generated file must be a plain file with only generated text:\n%s", got)
	}
	if len(res.Imported) != 0 {
		t.Fatalf("a file behind a link out of the folder was imported: %+v", res)
	}
}

func TestImportedInstructionTextIsCappedAndSaidOnce(t *testing.T) {
	ctx := context.Background()
	e, _ := newEngine(t)
	dir := t.TempDir()
	big := strings.Repeat("Rule line that repeats.\n", 10000) // ~240 KB
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(big), 0o644)
	if err := os.Symlink("AGENTS.md", filepath.Join(dir, "GEMINI.md")); err != nil {
		os.WriteFile(filepath.Join(dir, "GEMINI.md"), []byte(big), 0o644) // no links here: the same text twice
	}
	files, m, err := e.Generate(ctx, "p1", map[string]string{"AGENTS.md": big, "GEMINI.md": big})
	if err != nil {
		t.Fatal(err)
	}
	body := files["AGENTS.md"]
	if len(body) > maxImportedBytes+8<<10 {
		t.Fatalf("an imported file of %d bytes is not capped: AGENTS.md is %d bytes", len(big), len(body))
	}
	if !strings.Contains(body, "[cut: the file is longer than 64 KB]") || strings.Count(body, "From the repository's own") != 1 {
		t.Fatalf("one capped import expected:\n%.400s", body)
	}
	var said bool
	for _, it := range m.Items {
		said = said || (it.Source == "repo:GEMINI.md" && !it.Included && strings.Contains(it.Reason, "already imported"))
	}
	if !said {
		t.Fatalf("the duplicate is not in the manifest: %+v", m.Items)
	}
}

func TestAHumanEditProposalHasItsSecretsRedacted(t *testing.T) {
	ctx := context.Background()
	e, ms := newEngine(t)
	e.Redact = func(s string) string { return strings.ReplaceAll(s, "sk-LIVE-SECRET", "[redacted]") }
	dir := t.TempDir()
	if _, _, err := e.Write(ctx, "p1", dir); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "AGENTS.md")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, append(b, []byte("\nUse key sk-LIVE-SECRET for the staging API.\n")...), 0o644)
	if _, _, err := e.Write(ctx, "p1", dir); err != nil {
		t.Fatal(err)
	}
	props, _ := ms.List(ctx, "p1", memory.Proposed, "")
	if len(props) != 1 || strings.Contains(props[0].Body, "sk-LIVE-SECRET") || !strings.Contains(props[0].Body, "[redacted]") {
		t.Fatalf("the proposal kept the secret: %+v", props)
	}
}

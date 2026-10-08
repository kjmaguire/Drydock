package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests drive the real git CLI against real temporary repositories.

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newRepo(t *testing.T) (*Repo, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	write(t, dir, "README.md", "# demo\n")
	write(t, dir, "src/main.go", "package main\n\nfunc main() {}\n")
	r, err := Init(context.Background(), dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	return r, t.TempDir() // second dir: worktree parent
}

func TestCreateWorktreeIsIdempotentAndIsolated(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	w, err := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_1", Title: "Add Subtract!", Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	if w.Branch != "drydock/task_1-add-subtract" || w.Base != "main" {
		t.Fatalf("worktree %+v", w)
	}
	write(t, w.Path, "src/new.go", "package main\n")
	again, err := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_1", Title: "Add Subtract!", Parent: parent})
	if err != nil || again.Path != w.Path {
		t.Fatalf("second create: %+v %v", again, err)
	}
	if _, err := os.Stat(filepath.Join(w.Path, "src", "new.go")); err != nil {
		t.Fatal("an idempotent create must not disturb the worktree's files")
	}
	// the main tree is untouched
	if _, err := os.Stat(filepath.Join(r.Root, "src", "new.go")); err == nil {
		t.Fatal("changes leaked into the main working tree")
	}
}

func TestStatusDiffAndDiffHash(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	w, _ := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_2", Title: "x", Parent: parent})
	h0, err := r.DiffHash(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	write(t, w.Path, "src/main.go", "package main\n\nfunc main() { println(1) }\n")
	write(t, w.Path, "src/util.go", "package main\n")
	st, _ := r.Status(ctx, w)
	if !st.Dirty() || len(st.Modified) != 1 || len(st.Untracked) != 1 || st.Branch != w.Branch {
		t.Fatalf("status %+v", st)
	}
	d, err := r.Diff(ctx, w, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "println(1)") || !strings.Contains(d, "src/util.go") {
		t.Fatalf("diff must include modifications and untracked files:\n%s", d)
	}
	ds, _ := r.DiffStat(ctx, w, "")
	if ds.Files != 2 || ds.Insertions < 2 {
		t.Fatalf("stat %+v", ds)
	}
	h1, _ := r.DiffHash(ctx, w)
	h1b, _ := r.DiffHash(ctx, w)
	if h1 == h0 || h1 != h1b {
		t.Fatalf("diff hash must change with content and be stable otherwise: %s %s %s", h0, h1, h1b)
	}
	// the agent's index must be untouched by diffing
	if st2, _ := r.Status(ctx, w); len(st2.Added) != 0 {
		t.Fatalf("diff staged files in the real index: %+v", st2)
	}
}

func TestCommitAndMergeStrategies(t *testing.T) {
	for _, strat := range []MergeStrategy{MergeCommit, MergeSquash, MergeRebase} {
		t.Run(string(strat), func(t *testing.T) {
			ctx := context.Background()
			r, parent := newRepo(t)
			w, _ := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_3", Title: "feature", Parent: parent})
			write(t, w.Path, "src/feature.go", "package main\n// feature\n")
			id, err := r.Commit(ctx, w, "add feature")
			if err != nil || id == "" {
				t.Fatalf("commit: %v", err)
			}
			if id2, _ := r.Commit(ctx, w, "nothing"); id2 != id {
				t.Fatal("commit with no changes must return the current HEAD")
			}
			if strat == MergeRebase {
				// move main forward so a real rebase is needed
				write(t, r.Root, "README.md", "# demo v2\n")
				r.git(ctx, r.Root, nil, "commit", "-aqm", "docs")
			}
			if _, err := r.Merge(ctx, w, "main", strat, "merge feature"); err != nil {
				t.Fatalf("merge: %v", err)
			}
			if _, err := os.Stat(filepath.Join(r.Root, "src", "feature.go")); err != nil {
				t.Fatal("merged file missing from main")
			}
			if st, _ := r.Status(ctx, Worktree{Path: r.Root}); st.Dirty() {
				t.Fatalf("main left dirty: %+v", st)
			}
		})
	}
}

func TestMergeConflictAbortsCleanly(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	w, _ := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_4", Title: "c", Parent: parent})
	write(t, w.Path, "README.md", "# from task\n")
	r.Commit(ctx, w, "task change")
	write(t, r.Root, "README.md", "# from main\n")
	r.git(ctx, r.Root, nil, "commit", "-aqm", "main change")
	_, err := r.Merge(ctx, w, "main", MergeCommit, "")
	var ce *ConflictError
	if !errors.As(err, &ce) || len(ce.Files) != 1 || ce.Files[0] != "README.md" {
		t.Fatalf("want ConflictError on README.md, got %v", err)
	}
	if st, _ := r.Status(ctx, Worktree{Path: r.Root}); st.Dirty() {
		t.Fatalf("a failed merge must leave no half-merged state: %+v", st)
	}
	if b, _ := os.ReadFile(filepath.Join(r.Root, "README.md")); string(b) != "# from main\n" {
		t.Fatalf("main content changed: %q", b)
	}
}

func TestDeleteWorktreeRefusesDirtyUnlessForced(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	w, _ := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_5", Title: "d", Parent: parent})
	write(t, w.Path, "unsaved.txt", "precious\n")
	err := r.DeleteWorktree(ctx, w, false)
	if !errors.Is(err, ErrDirtyWorktree) {
		t.Fatalf("want ErrDirtyWorktree, got %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(w.Path, "unsaved.txt")); string(b) != "precious\n" {
		t.Fatal("refusal must not touch the files")
	}
	r.Commit(ctx, w, "save work")
	if err := r.DeleteWorktree(ctx, w, false); err != nil {
		t.Fatalf("clean worktree should delete: %v", err)
	}
	if _, err := os.Stat(w.Path); err == nil {
		t.Fatal("worktree directory still exists")
	}
	if err := r.DeleteWorktree(ctx, w, false); err != nil { // idempotent
		t.Fatalf("second delete: %v", err)
	}
}

func TestSafePointAndRestoreAreNonDestructive(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	w, _ := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_6", Title: "sp", Parent: parent})
	write(t, w.Path, "src/main.go", "package main\n// attempt 1\n")
	write(t, w.Path, "notes.txt", "attempt 1 notes\n")
	ref1, err := r.SafePoint(ctx, w, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ref1 != "refs/drydock/safe/task_6/1" {
		t.Fatalf("ref %s", ref1)
	}
	// taking a safe point leaves the worktree and branch alone
	st, _ := r.Status(ctx, w)
	if len(st.Added) != 0 || len(st.Modified) != 1 || len(st.Untracked) != 1 {
		t.Fatalf("safe point disturbed the worktree: %+v", st)
	}
	head1, _ := r.Head(ctx, w)

	// attempt 2 goes off the rails: edits, deletes, adds
	write(t, w.Path, "src/main.go", "package main\n// attempt 2 broke it\n")
	os.Remove(filepath.Join(w.Path, "notes.txt"))
	write(t, w.Path, "junk.txt", "junk\n")
	undo, err := r.RestoreSafePoint(ctx, w, ref1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(w.Path, "src", "main.go")); string(b) != "package main\n// attempt 1\n" {
		t.Fatalf("main.go not restored: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(w.Path, "notes.txt")); string(b) != "attempt 1 notes\n" {
		t.Fatalf("deleted file not restored: %q", b)
	}
	if _, err := os.Stat(filepath.Join(w.Path, "junk.txt")); err == nil {
		t.Fatal("file created after the safe point should be removed")
	}
	if head2, _ := r.Head(ctx, w); head2 != head1 {
		t.Fatal("restore must not move the branch")
	}
	// the rollback itself is undoable
	if _, err := r.RestoreSafePoint(ctx, w, undo, 3); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(w.Path, "src", "main.go")); string(b) != "package main\n// attempt 2 broke it\n" {
		t.Fatalf("undo failed: %q", b)
	}
	if _, err := os.Stat(filepath.Join(w.Path, "junk.txt")); err != nil {
		t.Fatal("undo should bring junk.txt back")
	}
}

func TestOpenNotARepo(t *testing.T) {
	if _, err := Open(context.Background(), t.TempDir()); !errors.Is(err, ErrNotARepo) {
		t.Fatalf("want ErrNotARepo, got %v", err)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"Add Subtract!": "add-subtract", "  ": "task", "Fix: the BIG bug (#12)": "fix-the-big-bug-12"} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q)=%q want %q", in, got, want)
		}
	}
}

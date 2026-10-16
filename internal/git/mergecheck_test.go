package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Two tasks approved in the same second both passed a stale-base check made before the merge lock; the second would land on a
// base the first had moved (audit 2026-10-16, M14). Under the lock exactly one of them merges.
func TestOnlyOneOfTwoSimultaneousApprovalsMergesOnTheSameBase(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	var ws [2]Worktree
	var heads [2]string
	for i := range ws {
		w, err := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_m" + string(rune('a'+i)), Title: "m", Parent: parent})
		if err != nil {
			t.Fatal(err)
		}
		write(t, w.Path, "src/m"+string(rune('a'+i))+".go", "package main\n")
		if heads[i], err = r.Commit(ctx, w, "m"); err != nil {
			t.Fatal(err)
		}
		ws[i] = w
	}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range ws {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = r.MergeChecked(ctx, ws[i], "main", MergeCommit, "", MergeCheck{Head: heads[i], UpToDate: true})
		}()
	}
	wg.Wait()
	var merged, stale int
	for _, err := range errs {
		var se *StaleBaseError
		switch {
		case err == nil:
			merged++
		case errors.As(err, &se) && se.Target == "main" && se.Behind > 0:
			stale++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if merged != 1 || stale != 1 {
		t.Fatalf("merged %d, stale %d; want one of each (errors %v)", merged, stale, errs)
	}
	if st, _ := r.Status(ctx, Worktree{Path: r.Root}); st.Dirty() {
		t.Fatalf("the refused merge left the main tree dirty: %+v", st)
	}
}

// Once the stale branch has the target brought in, the same check lets it through, and a plain Merge never asks.
func TestMergeCheckedLetsABranchThroughOnceItIsUpToDate(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	first, _ := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_n1", Title: "n", Parent: parent})
	second, _ := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_n2", Title: "n", Parent: parent})
	write(t, first.Path, "src/one.go", "package main\n")
	r.Commit(ctx, first, "one")
	write(t, second.Path, "src/two.go", "package main\n")
	head, _ := r.Commit(ctx, second, "two")
	if _, err := r.Merge(ctx, first, "main", MergeCommit, ""); err != nil {
		t.Fatal(err)
	}
	var se *StaleBaseError
	if _, err := r.MergeChecked(ctx, second, "main", MergeCommit, "", MergeCheck{Head: head, UpToDate: true}); !errors.As(err, &se) {
		t.Fatalf("a branch behind the base was merged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "src", "two.go")); err == nil {
		t.Fatal("the refused merge changed the main tree")
	}
	if _, conflicts, err := r.MergeIn(ctx, second, "main"); err != nil || len(conflicts) != 0 {
		t.Fatalf("bring the base in: %v %v", conflicts, err)
	}
	head, _ = r.Head(ctx, second)
	if _, err := r.MergeChecked(ctx, second, "main", MergeCommit, "", MergeCheck{Head: head, UpToDate: true}); err != nil {
		t.Fatalf("an up to date branch was refused: %v", err)
	}
}

// What a human approved is a commit; a branch that moved on since is not that change.
func TestMergeCheckedRefusesABranchThatMovedSinceItWasReviewed(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	w, _ := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_r1", Title: "r", Parent: parent})
	write(t, w.Path, "src/r.go", "package main\n")
	reviewed, _ := r.Commit(ctx, w, "reviewed")
	write(t, w.Path, "src/r.go", "package main\n// added after the review\n")
	if _, err := r.Commit(ctx, w, "after"); err != nil {
		t.Fatal(err)
	}
	var ce *ChangedSinceReviewError
	_, err := r.MergeChecked(ctx, w, "main", MergeCommit, "", MergeCheck{Head: reviewed})
	if !errors.As(err, &ce) || ce.Reviewed != reviewed || ce.Now == reviewed {
		t.Fatalf("want ChangedSinceReviewError, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "src", "r.go")); err == nil {
		t.Fatal("the refused merge changed the main tree")
	}
}

// A listing that failed is not an empty listing: a merge that cannot say which files are in conflict must not pass for one that
// has none, and a resolved check must not pass for a repository it could not read (audit 2026-10-16, L10).
func TestAConflictListThatCannotBeReadIsAnErrorNotNone(t *testing.T) {
	ctx := context.Background()
	r, _ := newRepo(t)
	if files, err := r.conflictFiles(ctx, t.TempDir()); err == nil {
		t.Fatalf("a folder that is not a repository listed conflicts: %v", files)
	}
	if left, err := r.UnresolvedConflicts(ctx, Worktree{Path: t.TempDir()}); err == nil {
		t.Fatalf("unresolved conflicts of a folder that is not a repository: %v", left)
	}
}

// A link in the worktree must not make the marker scan read a file outside it.
func TestTheMarkerScanDoesNotFollowALinkOutOfTheWorktree(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	w, _ := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_l1", Title: "l", Parent: parent})
	write(t, w.Path, "README.md", "# from task\n")
	r.Commit(ctx, w, "task change")
	write(t, r.Root, "README.md", "# from main\n")
	r.git(ctx, r.Root, nil, "commit", "-aqm", "main change")
	if _, conf, err := r.MergeIn(ctx, w, "main"); err != nil || len(conf) != 1 {
		t.Fatalf("set up the conflict: %v %v", conf, err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("<<<<<<< ours\nsecret\n=======\nx\n>>>>>>> theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(w.Path, "README.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(w.Path, "README.md")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	left, err := r.UnresolvedConflicts(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	// the file cannot be read through the link, so it cannot be shown to be resolved: it is reported, and what was behind the link was not read
	if len(left) != 1 || left[0] != "README.md" {
		t.Fatalf("the file behind the link must be reported as unresolved, got %v", left)
	}
}

// A change of whitespace alone is a different change; --stable's patch id ignores it (audit 2026-10-16, L15).
func TestPatchIDSeesAChangeOfWhitespaceOnly(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	write(t, r.Root, "app.py", "def f(x):\n    if x:\n        pass\n")
	r.git(ctx, r.Root, nil, "add", "-A")
	r.git(ctx, r.Root, nil, "commit", "-qm", "py")
	a, _ := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_w1", Title: "w", Parent: parent})
	b, _ := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_w2", Title: "w", Parent: parent})
	write(t, a.Path, "app.py", "def f(x):\n    if x:\n        pass\n    return x\n")
	write(t, b.Path, "app.py", "def f(x):\n    if x:\n        pass\n        return x\n")
	r.Commit(ctx, a, "a")
	r.Commit(ctx, b, "b")
	ia, err := r.PatchID(ctx, a, "main")
	if err != nil {
		t.Fatal(err)
	}
	ib, err := r.PatchID(ctx, b, "main")
	if err != nil {
		t.Fatal(err)
	}
	if ia == ib {
		t.Fatalf("an indent that moves a statement into the if block is the same patch id %s", ia)
	}
	// and the same change keeps its id when the base moves under it
	write(t, r.Root, "other.txt", "x\n")
	r.git(ctx, r.Root, nil, "add", "-A")
	r.git(ctx, r.Root, nil, "commit", "-qm", "other")
	if _, _, err := r.MergeIn(ctx, a, "main"); err != nil {
		t.Fatal(err)
	}
	if ia2, _ := r.PatchID(ctx, a, "main"); ia2 != ia {
		t.Fatalf("bringing the base in changed the patch id: %s then %s", ia, ia2)
	}
}

// With a git that has no --verbatim the fallback adds a digest of the changed lines, so whitespace still counts.
func TestPatchIDFallbackAlsoSeesWhitespace(t *testing.T) {
	ctx := context.Background()
	r, _ := newRepo(t)
	one := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1,2 @@\n a\n+    b\n"
	two := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1,2 @@\n a\n+b\n"
	i1, err := r.patchID(ctx, r.Root, one, false)
	if err != nil {
		t.Fatal(err)
	}
	i2, err := r.patchID(ctx, r.Root, two, false)
	if err != nil {
		t.Fatal(err)
	}
	if i1 == i2 {
		t.Fatalf("the fallback id ignores whitespace: %s", i1)
	}
}

// Restoring a safe point removes the files made since. A folder the agent swapped for a link to somewhere else must not make that
// removal reach a file outside the worktree (audit 2026-10-16, L10).
func TestRestoringASafePointDoesNotDeleteThroughALinkedFolder(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	w, _ := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_sp1", Title: "sp", Parent: parent})
	// a staged file in a folder, so git keeps listing "later/extra.txt" whatever happens to the folder
	write(t, w.Path, "later/extra.txt", "made in the worktree\n")
	if _, err := r.git(ctx, w.Path, nil, "add", "later/extra.txt"); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	victim := filepath.Join(outside, "extra.txt")
	if err := os.WriteFile(victim, []byte("not the worktree's\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// the folder becomes a link, and the safe point is taken with the link in it, so the restore keeps the link and
	// still finds "later/extra.txt" in the index, which the safe point does not have
	if err := os.RemoveAll(filepath.Join(w.Path, "later")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(w.Path, "later")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	ref, err := r.SafePoint(ctx, w, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RestoreSafePoint(ctx, w, ref, 2); err == nil {
		t.Fatal("a removal that cannot be confined to the worktree was reported as done")
	}
	if b, err := os.ReadFile(victim); err != nil || string(b) != "not the worktree's\n" {
		t.Fatalf("a file outside the worktree was reached through the link: %q %v", b, err)
	}
}

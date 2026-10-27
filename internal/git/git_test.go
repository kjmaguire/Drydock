package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// A plain folder, a folder that is gone and a repository git cannot read (a broken .git/config) are all ErrNotARepo, told
// apart by the words: the path alone for the first two, git's own reason after the path for the last.
func TestOpenNotARepo(t *testing.T) {
	ctx := context.Background()
	plain := t.TempDir()
	if _, err := Open(ctx, plain); !errors.Is(err, ErrNotARepo) || err.Error() != "git: not a git repository: "+plain {
		t.Fatalf("a plain folder: %v", err)
	}
	gone := filepath.Join(t.TempDir(), "gone")
	if _, err := Open(ctx, gone); !errors.Is(err, ErrNotARepo) || err.Error() != "git: not a git repository: "+gone {
		t.Fatalf("a folder that is gone says so, not what git could not do: %v", err)
	}
	broken := t.TempDir()
	if _, err := Init(ctx, broken, "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, ".git", "config"), []byte("this is not a git configuration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, broken); !errors.Is(err, ErrNotARepo) || !strings.Contains(err.Error(), broken+": ") || !strings.Contains(err.Error(), "bad config") {
		t.Fatalf("a broken .git/config carries git's words after the path: %v", err)
	}
}

// git's words follow the command line and the reason when it said any; a git that said nothing (it could not start, or could
// not enter the folder) leaves nothing dangling after the reason.
func TestGitErrorWordsEndWithWhatGitSaid(t *testing.T) {
	said := &gitError{args: []string{"remote", "get-url", "origin"}, err: errors.New("exit status 128"), stderr: "fatal: bad config line 1 in file .git/config"}
	if got := said.Error(); got != "git remote get-url origin: exit status 128: fatal: bad config line 1 in file .git/config" {
		t.Fatalf("with git's words: %q", got)
	}
	silent := &gitError{args: []string{"remote", "get-url", "origin"}, err: errors.New("chdir /src/moved: no such file or directory")}
	if got := silent.Error(); got != "git remote get-url origin: chdir /src/moved: no such file or directory" {
		t.Fatalf("without: %q", got)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"Add Subtract!": "add-subtract", "  ": "task", "Fix: the BIG bug (#12)": "fix-the-big-bug-12"} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q)=%q want %q", in, got, want)
		}
	}
	if got := BranchName("tsk_1", "Add Subtract!"); got != "drydock/tsk_1-add-subtract" {
		t.Errorf("BranchName = %q", got)
	}
}

// A task's diff is its own work. Once another task has merged into the base branch, a diff taken against the branch's current
// tip would show that other task's changes as deletions in this one.
func TestDiffIsAgainstTheMergeBaseNotTheMovingTip(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	w, _ := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_a", Title: "a", Parent: parent})
	write(t, w.Path, "src/a.go", "package main\n// a\n")
	if _, err := r.Commit(ctx, w, "task a"); err != nil {
		t.Fatal(err)
	}
	// another task lands on main in the meantime
	write(t, r.Root, "src/other.go", "package main\n// other\n")
	r.git(ctx, r.Root, nil, "add", "-A")
	r.git(ctx, r.Root, nil, "commit", "-qm", "other task")

	d, err := r.Diff(ctx, w, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "src/a.go") || strings.Contains(d, "other.go") {
		t.Fatalf("the diff must hold only this task's change:\n%s", d)
	}
	ds, _ := r.DiffStat(ctx, w, "")
	if ds.Files != 1 || ds.Deletions != 0 {
		t.Fatalf("stat %+v", ds)
	}
	paths, err := r.ChangedPaths(ctx, w, "")
	if err != nil || strings.Join(paths, ",") != "src/a.go" {
		t.Fatalf("footprint %v %v", paths, err)
	}
	if n, _ := r.Behind(ctx, w, "main"); n != 1 {
		t.Fatalf("behind = %d, want 1", n)
	}
	// uncommitted and untracked work is part of the footprint too
	write(t, w.Path, "src/b.go", "package main\n")
	write(t, w.Path, "README.md", "# changed\n")
	paths, _ = r.ChangedPaths(ctx, w, "")
	if strings.Join(paths, ",") != "README.md,src/a.go,src/b.go" {
		t.Fatalf("footprint %v", paths)
	}
}

func TestMergeInBringsTheBaseIntoTheBranchAndLeavesConflictsForTheAgent(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	w, _ := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_m", Title: "m", Parent: parent})
	write(t, w.Path, "src/feature.go", "package main\n// feature\n")
	r.Commit(ctx, w, "feature")

	// nothing to bring in
	if ch, conf, err := r.MergeIn(ctx, w, "main"); err != nil || ch || len(conf) != 0 {
		t.Fatalf("up to date: %v %v %v", ch, conf, err)
	}
	// main moves without touching our files: a clean merge, history kept
	write(t, r.Root, "src/other.go", "package main\n")
	r.git(ctx, r.Root, nil, "add", "-A")
	r.git(ctx, r.Root, nil, "commit", "-qm", "other")
	ch, conf, err := r.MergeIn(ctx, w, "main")
	if err != nil || !ch || len(conf) != 0 {
		t.Fatalf("clean merge: %v %v %v", ch, conf, err)
	}
	if _, err := os.Stat(filepath.Join(w.Path, "src", "other.go")); err != nil {
		t.Fatal("the base's file did not arrive in the worktree")
	}
	if n, _ := r.Behind(ctx, w, "main"); n != 0 {
		t.Fatalf("still behind by %d", n)
	}
	if d, _ := r.Diff(ctx, w, ""); strings.Contains(d, "other.go") {
		t.Fatalf("after bringing the base in, the diff is still only this task's work:\n%s", d)
	}

	// a conflict: the merge stays in progress with markers in the file
	write(t, w.Path, "README.md", "# from the task\n")
	r.Commit(ctx, w, "readme")
	write(t, r.Root, "README.md", "# from main\n")
	r.git(ctx, r.Root, nil, "commit", "-aqm", "main readme")
	ch, conf, err = r.MergeIn(ctx, w, "main")
	if err != nil || !ch || len(conf) != 1 || conf[0] != "README.md" {
		t.Fatalf("conflicting merge: %v %v %v", ch, conf, err)
	}
	if !r.MergeInProgress(ctx, w) {
		t.Fatal("the merge should be left in progress for the agent")
	}
	if left, err := r.UnresolvedConflicts(ctx, w); err != nil || len(left) != 1 {
		t.Fatalf("markers should be reported: %v %v", left, err)
	}
	// the agent resolves it by editing the file; committing completes the merge
	write(t, w.Path, "README.md", "# from the task and main\n")
	if left, err := r.UnresolvedConflicts(ctx, w); err != nil || len(left) != 0 {
		t.Fatalf("resolved file still reported: %v %v", left, err)
	}
	if _, err := r.Commit(ctx, w, "resolve"); err != nil {
		t.Fatal(err)
	}
	if r.MergeInProgress(ctx, w) {
		t.Fatal("the merge should be complete")
	}
	if n, _ := r.Behind(ctx, w, "main"); n != 0 {
		t.Fatalf("behind %d after resolving", n)
	}
	// a dirty worktree is refused: the work must be committed first
	write(t, w.Path, "src/dirty.go", "package main\n")
	write(t, r.Root, "src/more.go", "package main\n")
	r.git(ctx, r.Root, nil, "add", "-A")
	r.git(ctx, r.Root, nil, "commit", "-qm", "more")
	if _, _, err := r.MergeIn(ctx, w, "main"); !errors.Is(err, ErrDirtyWorktree) {
		t.Fatalf("dirty worktree: %v", err)
	}
}

// Two approvals at once must not race on git's index lock: merges into the base branch are one at a time.
func TestMergesIntoTheBaseBranchAreSerialized(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	const n = 6
	var ws []Worktree
	for i := 0; i < n; i++ {
		w, err := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_p" + string(rune('a'+i)), Title: "p", Parent: parent})
		if err != nil {
			t.Fatal(err)
		}
		write(t, w.Path, "src/p"+string(rune('a'+i))+".go", "package main\n")
		if _, err := r.Commit(ctx, w, "p"); err != nil {
			t.Fatal(err)
		}
		ws = append(ws, w)
	}
	errs := make(chan error, n)
	for _, w := range ws {
		go func(w Worktree) { _, err := r.Merge(ctx, w, "main", MergeCommit, ""); errs <- err }(w)
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent merge failed: %v", err)
		}
	}
	for i := 0; i < n; i++ {
		if _, err := os.Stat(filepath.Join(r.Root, "src", "p"+string(rune('a'+i))+".go")); err != nil {
			t.Fatalf("merge %d is missing", i)
		}
	}
}

// With core.autocrlf (the Git for Windows default) a worktree is checked out with CRLF. A file written back with LF and the
// same text differs only in size, which git status reports as modified without reading it; Status must not.
func TestStatusIgnoresALineEndingOnlyRewriteUnderAutoCRLF(t *testing.T) {
	ctx := context.Background()
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, []byte("[core]\n\tautocrlf = true\n[user]\n\tname = Test\n\temail = test@drydock.invalid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	r, parent := newRepo(t)
	w, err := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_crlf", Title: "eol", Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(w.Path, "README.md")); string(b) != "# demo\r\n" {
		t.Fatalf("the checkout should have CRLF under autocrlf: %q", b)
	}
	write(t, w.Path, "README.md", "# demo\n")
	write(t, w.Path, "src/main.go", "package main\n\nfunc main() { println() }\n")
	st, err := r.Status(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.Modified, []string{"src/main.go"}) {
		t.Fatalf("only the real change is modified: %+v", st)
	}
}

func TestListFilesHonoursGitignoreAndListsUntrackedFiles(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	write(t, r.Root, ".gitignore", "build/\n*.log\n")
	write(t, r.Root, "build/out.go", "package out\n")
	write(t, r.Root, "debug.log", "noise\n")
	write(t, r.Root, "src/new.go", "package main\n") // untracked, not ignored
	files, err := r.ListFiles(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	want := []string{".gitignore", "README.md", "src/main.go", "src/new.go"}
	if !slices.Equal(files, want) {
		t.Fatalf("ListFiles = %v, want %v", files, want)
	}
}

// RemoteURL tells a repository without that remote (ErrNoRemote) from git failing to read the repository at all.
func TestRemoteURLSaysWhenThereIsNoSuchRemote(t *testing.T) {
	ctx := context.Background()
	r, _ := newRepo(t)
	if _, err := r.RemoteURL(ctx, "origin"); !errors.Is(err, ErrNoRemote) {
		t.Fatalf("no origin: %v", err)
	}
	if out, err := exec.Command("git", "-C", r.Root, "remote", "add", "origin", "https://github.com/acme/demo.git").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if url, err := r.RemoteURL(ctx, "origin"); err != nil || url != "https://github.com/acme/demo.git" {
		t.Fatalf("origin: %q %v", url, err)
	}
	if err := os.RemoveAll(filepath.Join(r.Root, ".git")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RemoteURL(ctx, "origin"); err == nil || errors.Is(err, ErrNoRemote) {
		t.Fatalf("a folder that is no longer a repository is a failure, not a missing remote: %v", err)
	}
}

// Deleting a task worktree whose folder is already gone used to run `git worktree prune`, which also forgot the person's own
// linked worktrees whose folder could not be seen then (on a drive that is not plugged in), losing their index and HEAD
// (review 2026-10-27). Now only the task's own entry goes.
func TestDeletingAMissingWorktreeForgetsOnlyThatOne(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	// the person's own worktree, made with git, whose drive is unplugged: its folder is out of sight for now
	own := filepath.Join(t.TempDir(), "mine")
	if out, err := exec.Command("git", "-C", r.Root, "worktree", "add", "-q", "-b", "mine", own).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v %s", err, out)
	}
	away := own + "-unplugged"
	if err := os.Rename(own, away); err != nil {
		t.Fatal(err)
	}
	w, err := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_9", Title: "gone", Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(w.Path); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteWorktree(ctx, w, false); err != nil {
		t.Fatal(err)
	}
	list, err := exec.Command("git", "-C", r.Root, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(list), w.Path) {
		t.Fatalf("the task's worktree is still registered:\n%s", list)
	}
	if !strings.Contains(string(list), own) {
		t.Fatalf("the person's own worktree was forgotten:\n%s", list)
	}
	// plugged in again, it works as before
	if err := os.Rename(away, own); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", own, "status", "--short").CombinedOutput(); err != nil {
		t.Fatalf("the person's worktree no longer works: %v %s", err, out)
	}
}

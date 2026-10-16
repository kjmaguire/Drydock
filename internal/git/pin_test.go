package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests plant what an agent can write from inside its own worktree (audit 2026-10-16, C1) and prove Drydock's git calls
// do not run it. Every plant is first shown live: the same repository, queried by a plain git call the way a person or an agent
// would, runs the planted program and writes the marker, so a missing marker after Drydock's calls means something.

// plantRedirect makes the worktree's .git file point at a repository the "agent" wrote inside the worktree, whose configuration
// carries a clean filter, and attributes that apply the filter to every file.
func plantRedirect(t *testing.T, wt, marker string) {
	t.Helper()
	x := filepath.Join(wt, ".x")
	for _, d := range []string{"objects", filepath.Join("refs", "heads")} {
		if err := os.MkdirAll(filepath.Join(x, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, x, "HEAD", "ref: refs/heads/main\n")
	write(t, x, "config", "[core]\n\trepositoryformatversion = 0\n")
	rawGit(t, wt, "config", "--file", filepath.Join(x, "config"), "filter.x.clean", markerScript(marker))
	write(t, wt, ".git", "gitdir: ./.x\n")
	write(t, wt, ".gitattributes", "* filter=x\n")
	write(t, wt, "src/planted.go", "package main\n")
}

// plantEmbedded creates a repository inside the worktree whose own configuration has a clean filter, and returns a function that
// makes one of its files look modified to git (same size, newer time), which is what makes git hash it and run the filter.
func plantEmbedded(t *testing.T, wt, marker string) (touch func()) {
	t.Helper()
	sub := filepath.Join(wt, "vendor", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	rawGit(t, sub, "init", "-q", "-b", "main")
	write(t, sub, ".gitattributes", "* filter=x\n")
	write(t, sub, "f.txt", "aaaa\n")
	rawGit(t, sub, "add", "-A")
	rawGit(t, sub, "commit", "-q", "-m", "c1")
	rawGit(t, sub, "config", "filter.x.clean", markerScript(marker))
	// a .gitmodules entry that asks for the strictest dirty check, which beats a diff.ignoreSubmodules setting
	write(t, wt, ".gitmodules", "[submodule \"vendor/sub\"]\n\tpath = vendor/sub\n\turl = ./vendor/sub\n\tignore = none\n")
	n := 0
	return func() {
		t.Helper()
		n++
		p := filepath.Join(sub, "f.txt")
		body := "bbbb\n"
		if n%2 == 0 {
			body = "cccc\n"
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		later := time.Now().Add(time.Duration(n+1) * time.Minute)
		if err := os.Chtimes(p, later, later); err != nil {
			t.Fatal(err)
		}
	}
}

// drydockCalls runs every operation Drydock performs on a task's worktree.
func drydockCalls(t *testing.T, r *Repo, w Worktree) {
	t.Helper()
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"Status":             func() error { _, err := r.Status(ctx, w); return err },
		"Diff":               func() error { _, err := r.Diff(ctx, w, "main"); return err },
		"DiffStat":           func() error { _, err := r.DiffStat(ctx, w, "main"); return err },
		"DiffHash":           func() error { _, err := r.DiffHash(ctx, w); return err },
		"ChangedPaths":       func() error { _, err := r.ChangedPaths(ctx, w, "main"); return err },
		"SafePoint":          func() error { _, err := r.SafePoint(ctx, w, 1); return err },
		"Head":               func() error { _, err := r.Head(ctx, w); return err },
		"Behind":             func() error { _, err := r.Behind(ctx, w, "main"); return err },
		"PatchID":            func() error { _, err := r.PatchID(ctx, w, "main"); return err },
		"ListFiles":          func() error { _, err := r.ListFiles(ctx, w.Path); return err },
		"LocalIgnore":        func() error { return r.LocalIgnore(ctx, w, []string{"AGENTS.md"}) },
		"UnresolvedConflict": func() error { _, err := r.UnresolvedConflicts(ctx, w); return err },
		"MergeInProgress":    func() error { r.MergeInProgress(ctx, w); return nil },
	} {
		if err := call(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := r.Commit(ctx, w, "task work"); err != nil {
		t.Errorf("Commit: %v", err)
	}
	if _, _, err := r.MergeIn(ctx, w, "main"); err != nil {
		t.Errorf("MergeIn: %v", err)
	}
	if err := r.Rebase(ctx, w, "main"); err != nil {
		t.Errorf("Rebase: %v", err)
	}
	if _, err := r.RestoreSafePoint(ctx, w, SafePointRef(w.TaskID, 1), 2); err != nil {
		t.Errorf("RestoreSafePoint: %v", err)
	}
}

func TestARedirectedGitFileInAWorktreeRunsNothingInDrydocksCalls(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	w, err := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_redirect", Title: "redirect", Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "filter-ran")
	plantRedirect(t, w.Path, marker)

	// the plant is live: git started the way a person would, in the same folder, follows the .git file and runs the filter
	rawGit(t, w.Path, "add", "-A")
	if !exists(marker) {
		t.Fatal("the planted filter did not run for a plain git call either, so this test proves nothing")
	}
	_ = os.Remove(marker)

	drydockCalls(t, r, w)
	if exists(marker) {
		t.Fatal("a filter from a configuration the agent wrote ran in a Drydock git call")
	}
	// the work Drydock committed went to the task's branch in the real repository, not into the planted one
	if out := rawGit(t, r.Root, "log", "--format=%s", w.Branch); !strings.Contains(out, "task work") {
		t.Fatalf("the commit did not land on %s: %q", w.Branch, out)
	}
	if err := r.DeleteWorktree(ctx, w, false); err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}
	if exists(marker) {
		t.Fatal("removing the worktree ran the planted filter")
	}
}

func TestAnEmbeddedRepositoryIsNeverAskedWhetherItIsModified(t *testing.T) {
	for _, tc := range []struct {
		name string
		// recorded: an earlier `git add` left the embedded repository in the branch as a gitlink
		recorded bool
	}{{"untracked", false}, {"recorded as a gitlink", true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			r, parent := newRepo(t)
			w, err := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_embedded", Title: "embedded", Parent: parent})
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "filter-ran")
			touch := plantEmbedded(t, w.Path, marker)
			if tc.recorded {
				rawGit(t, w.Path, "add", "vendor/sub")
				rawGit(t, w.Path, "commit", "-q", "-m", "the agent records the repository")
				if out := rawGit(t, w.Path, "ls-files", "-s", "vendor/sub"); !strings.HasPrefix(out, "160000") {
					t.Fatalf("the repository is not recorded as a gitlink: %q", out)
				}
				_ = os.Remove(marker)
			}
			write(t, w.Path, "src/feature.go", "package main\n")
			touch()

			drydockCalls(t, r, w)
			if exists(marker) {
				t.Fatal("the embedded repository's clean filter ran in a Drydock git call")
			}
			// the work outside the nested repository was committed, the nested repository's content was not
			if out := rawGit(t, r.Root, "ls-tree", "-r", "--name-only", w.Branch); !strings.Contains(out, "src/feature.go") || strings.Contains(out, "vendor/sub/") {
				t.Fatalf("committed tree:\n%s", out)
			}
			st, err := r.Status(ctx, w)
			if err != nil || st.Dirty() {
				t.Fatalf("an embedded repository counts as work Drydock can commit: %+v %v", st, err)
			}
			// git would not remove a worktree that holds a nested repository without force, and neither does Drydock
			if err := r.DeleteWorktree(ctx, w, false); !errors.Is(err, ErrDirtyWorktree) {
				t.Fatalf("DeleteWorktree of a worktree with a nested repository: %v", err)
			}
			touch()
			if exists(marker) {
				t.Fatal("the filter ran before the plant was exercised")
			}

			// the plant is live: once the embedded repository is recorded in the index, a plain git status in the worktree
			// enters it and runs its filter
			if !tc.recorded {
				rawGit(t, w.Path, "add", "vendor/sub")
			}
			rawGit(t, w.Path, "status", "--porcelain")
			if !exists(marker) {
				t.Fatal("a plain git status did not run the embedded repository's filter either, so this test proves nothing")
			}
			_ = os.Remove(marker)
			touch()
			if err := r.DeleteWorktree(ctx, w, true); err != nil {
				t.Fatalf("forced DeleteWorktree: %v", err)
			}
			if exists(marker) {
				t.Fatal("the forced removal ran the filter")
			}
		})
	}
}

func TestAFolderGitDoesNotKnowAsAWorktreeIsRefused(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	w, err := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_forget", Title: "forget", Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "filter-ran")
	plantRedirect(t, w.Path, marker)
	// the folder was never registered
	stranger := t.TempDir()
	write(t, stranger, ".git", "gitdir: ./.x\n")
	if _, err := r.Status(ctx, Worktree{Path: stranger}); !errors.Is(err, ErrNotAWorktree) {
		t.Fatalf("Status of an unregistered folder: %v", err)
	}
	// and a worktree whose registration is gone (the agent removed the private directory) is refused too, not rediscovered
	admin, err := filepath.Glob(filepath.Join(r.Root, ".git", "worktrees", "*"))
	if err != nil || len(admin) != 1 {
		t.Fatalf("registered worktrees: %v %v", admin, err)
	}
	if err := os.RemoveAll(admin[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Status(ctx, w); !errors.Is(err, ErrNotAWorktree) {
		t.Fatalf("Status of a worktree git forgot: %v", err)
	}
	if exists(marker) {
		t.Fatal("a refused call still ran the planted filter")
	}
}

func TestACallInAWorktreeIsPinnedToItsRegisteredGitDirectory(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	w, err := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_pin", Title: "pin", Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	pin, err := r.pinFor(w.Path)
	if err != nil || len(pin) != 2 || !strings.HasPrefix(pin[0], "GIT_DIR=") || !strings.HasPrefix(pin[1], "GIT_WORK_TREE=") {
		t.Fatalf("pin of a worktree: %v %v", pin, err)
	}
	if got := strings.TrimPrefix(pin[0], "GIT_DIR="); filepath.Dir(got) != filepath.Join(r.Root, ".git", "worktrees") {
		t.Fatalf("GIT_DIR %q is not a private directory of the repository", got)
	}
	// a folder inside the worktree belongs to the same worktree
	write(t, w.Path, "pkg/a.go", "package pkg\n")
	if inner, err := r.pinFor(filepath.Join(w.Path, "pkg")); err != nil || strings.Join(inner, " ") != strings.Join(pin, " ") {
		t.Fatalf("pin of a folder inside the worktree: %v %v", inner, err)
	}
	if got, err := r.ListFiles(ctx, filepath.Join(w.Path, "pkg")); err != nil || len(got) != 1 || got[0] != "a.go" {
		t.Fatalf("ListFiles from inside the worktree: %v %v", got, err)
	}
	// the main working tree is the person's own and is not pinned
	if p, err := r.pinFor(r.Root); err != nil || len(p) != 0 {
		t.Fatalf("pin of the main working tree: %v %v", p, err)
	}
	if p, err := r.pinFor(filepath.Join(r.Root, "src")); err != nil || len(p) != 0 {
		t.Fatalf("pin of a folder in the main working tree: %v %v", p, err)
	}
}

func TestHardeningNeverAsksNestedRepositories(t *testing.T) {
	for _, sub := range []string{"status", "diff"} {
		got := strings.Join(hardening([]string{sub, "--stat"}), " ")
		if !strings.Contains(got, sub+" ") || !strings.Contains(got, "--ignore-submodules=all") {
			t.Errorf("%s: %s", sub, got)
		}
	}
	got := strings.Join(hardening([]string{"commit", "-m", "x"}), " ")
	for _, want := range []string{"diff.ignoreSubmodules=all", "status.submoduleSummary=false", "submodule.recurse=false", "core.hooksPath="} {
		if !strings.Contains(got, want) {
			t.Errorf("commit is missing %s: %s", want, got)
		}
	}
}

func TestPathStateAsksThroughTheGuardedChokepoint(t *testing.T) {
	ctx := context.Background()
	r, _ := newRepo(t)
	write(t, r.Root, "skills/a/SKILL.md", "---\nname: a\n---\n")
	rawGit(t, r.Root, "add", "-A")
	rawGit(t, r.Root, "commit", "-q", "-m", "add skill")
	r, err := Open(ctx, r.Root)
	if err != nil {
		t.Fatal(err)
	}
	commit, clean := r.PathState(ctx, r.Root, "skills/a")
	if len(commit) != 40 || !clean {
		t.Fatalf("a committed, unmodified path: %q %v", commit, clean)
	}
	write(t, r.Root, "skills/a/SKILL.md", "---\nname: a\n---\nedited\n")
	if c, clean := r.PathState(ctx, r.Root, "skills/a"); c != commit || clean {
		t.Fatalf("a modified path: %q %v", c, clean)
	}
	if c, clean := r.PathState(ctx, r.Root, "skills/none"); c != "" || clean {
		t.Fatalf("an unknown path: %q %v", c, clean)
	}

	// a configuration that changed since a person trusted it is refused, so the filter it names never runs
	marker := filepath.Join(t.TempDir(), "filter-ran")
	rawGit(t, r.Root, "config", "filter.evil.clean", markerScript(marker))
	write(t, r.Root, ".gitattributes", "* filter=evil\n")
	write(t, r.Root, "skills/a/SKILL.md", "---\nname: a\n---\nedited again, longer\n")
	if c, clean := r.PathState(ctx, r.Root, "skills/a"); clean || c != "" && c != commit {
		t.Fatalf("under a changed configuration: %q %v", c, clean)
	}
	if exists(marker) {
		t.Fatal("PathState ran a filter the guard had not approved")
	}
}

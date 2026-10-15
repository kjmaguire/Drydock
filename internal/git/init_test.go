package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/wsl"
	"github.com/kjmaguire/drydock/internal/wsl/wsltest"
)

// WorkTreeOf tells a repository's own folder, a folder inside its work tree and a folder in none apart (decision P58: a new
// project's repository is never made inside another one).
func TestWorkTreeOfTellsARepositoryAFolderInsideOneAndAPlainFolderApart(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	root, _ := filepath.EvalSymlinks(r.Root)
	if top, err := WorkTreeOf(ctx, r.Root, nil); err != nil || !samePathForTest(top, root) {
		t.Fatalf("the repository's own folder: %q %v", top, err)
	}
	sub := filepath.Join(r.Root, "src")
	if top, err := WorkTreeOf(ctx, sub, nil); err != nil || !samePathForTest(top, root) {
		t.Fatalf("a folder inside it: %q %v", top, err)
	}
	plain := t.TempDir()
	if top, err := WorkTreeOf(ctx, plain, nil); err != nil || top != "" {
		t.Fatalf("a folder in no repository: %q %v", top, err)
	}
	if _, err := WorkTreeOf(ctx, filepath.Join(r.Root, ".git"), nil); err == nil {
		t.Fatal("a folder inside .git is not a work tree and must not pass as a plain folder")
	}
}

func samePathForTest(a, b string) bool {
	x, _ := filepath.EvalSymlinks(a)
	return filepath.Clean(x) == filepath.Clean(b)
}

// InitIn on an empty folder makes the branch with an empty first commit, so a task worktree can be made from it at once; the
// files already in a folder become its first commit, and what its .gitignore leaves out stays out.
func TestInitMakesABranchWorktreesCanStartFromWithWhatIsInTheFolder(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	empty := t.TempDir()
	r, err := InitIn(ctx, empty, "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := r.CurrentBranch(ctx); err != nil || b != "main" {
		t.Fatalf("branch %q %v", b, err)
	}
	if n, err := r.TrackedFiles(ctx); err != nil || n != 0 {
		t.Fatalf("an empty folder's first commit holds %d files (%v)", n, err)
	}
	w, err := r.CreateWorktree(ctx, WorktreeReq{TaskID: "T1", Title: "first", Base: "main", Parent: t.TempDir()})
	if err != nil {
		t.Fatalf("a worktree from the first commit: %v", err)
	}
	if _, err := os.Stat(w.Path); err != nil {
		t.Fatal(err)
	}

	full := t.TempDir()
	write(t, full, "README.md", "# app\n")
	write(t, full, ".gitignore", "secret.env\n")
	write(t, full, "secret.env", "TOKEN=not-for-git\n")
	r2, err := InitIn(ctx, full, "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r2.TrackedFiles(ctx); err != nil || n != 2 {
		t.Fatalf("the first commit holds %d files, want README.md and .gitignore (%v)", n, err)
	}
	out, err := r2.git(ctx, full, nil, "ls-files")
	if err != nil || strings.Contains(out, "secret.env") {
		t.Fatalf("an ignored file was committed: %q %v", out, err)
	}
}

// A repository made inside a WSL distro is made by the distro's git, with Drydock's hardening on every call (decision P41).
func TestInitInThroughTheDistroRunsTheDistrosGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in wsl.exe runs on a Linux development host")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	f := wsltest.NewFake(t, false, "Ubuntu")
	run := hostRunner{wsl.GitRunner{Exe: f.Exe, Paths: wsl.Paths{Distro: "Ubuntu", MountRoot: "/mnt/"}}}
	ctx := context.Background()
	dir := t.TempDir()
	if top, err := WorkTreeOf(ctx, dir, run); err != nil || top != "" {
		t.Fatalf("a plain folder through the distro: %q %v", top, err)
	}
	r, err := InitIn(ctx, dir, "main", run)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := r.CurrentBranch(ctx); err != nil || b != "main" {
		t.Fatalf("branch %q %v", b, err)
	}
	n := 0
	for _, c := range f.Calls() {
		if slices.Contains(c, "git") && slices.Contains(c, "core.fsmonitor=false") {
			n++
		}
	}
	if n < 3 {
		t.Fatalf("only %d calls ran the distro's git with the hardening: %q", n, f.Calls())
	}
}

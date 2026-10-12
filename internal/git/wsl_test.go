package git

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/wsl"
	"github.com/kjmaguire/drydock/internal/wsl/wsltest"
)

func TestMain(m *testing.M) {
	if wsltest.IsStandIn() { // the LABELLED stand-in wsl.exe (internal/wsl/wsltest)
		os.Exit(wsltest.StandIn(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// A Windows git reaching a repository inside a WSL distro through \\wsl.localhost needs safe.directory for exactly that path:
// Drydock passes it on the call's own command line, never in the person's global configuration.
func TestSafeDirectoryNamesOnlyDistroPathsOnTheCallItself(t *testing.T) {
	got := safeDirectory(`\\wsl.localhost\Ubuntu\home\k\app\.drydock-worktrees\p1\T1`, `\\wsl$\Ubuntu\home\k\app`, `D:\src\app`, "")
	want := []string{"-c", "safe.directory=//wsl.localhost/Ubuntu/home/k/app/.drydock-worktrees/p1/T1", "-c", "safe.directory=//wsl$/Ubuntu/home/k/app"}
	if !slices.Equal(got, want) {
		t.Fatalf("%q", got)
	}
	if len(safeDirectory(`C:\x`, "/home/k")) != 0 {
		t.Fatal("only distro paths need it")
	}
	r := &Repo{Root: `\\wsl.localhost\Ubuntu\home\k\app`}
	cmd, err := r.command(context.Background(), r.Root, nil, []string{"status"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cmd.Args, "safe.directory=//wsl.localhost/Ubuntu/home/k/app") || !slices.Contains(cmd.Args, "core.fsmonitor=false") {
		t.Fatalf("%q", cmd.Args)
	}
}

func TestWorktreesOfADistroRepositoryLiveNextToItInsideTheDistro(t *testing.T) {
	r := &Repo{Root: `\\wsl.localhost\Ubuntu\home\k\code\app`}
	if p := r.WorktreeParent(`C:\Users\k\AppData\Roaming\Drydock\worktrees`, "p1"); p != filepath.Join(filepath.Dir(r.Root), ".drydock-worktrees", "p1") {
		t.Fatalf("%s", p)
	}
	w := &Repo{Root: `D:\src\app`}
	if p := w.WorktreeParent("data/worktrees", "p1"); p != filepath.Join("data/worktrees", "p1") {
		t.Fatalf("a repository on Windows keeps its worktrees in the data folder: %s", p)
	}
}

func TestGitRunnerTranslatesPathsAndRunsTheDistrosGit(t *testing.T) {
	g := wsl.GitRunner{Exe: "wsl.exe", Paths: wsl.Paths{Distro: "Ubuntu", MountRoot: "/mnt/"}}
	path, argv, _, err := g.Command(`\\wsl.localhost\Ubuntu\home\k\app`, []string{"GIT_INDEX_FILE=C:\\Users\\k\\AppData\\Local\\Temp\\drydock-index-1", "GIT_AUTHOR_NAME=Drydock"},
		[]string{"-c", "core.hooksPath=C:/Users/k/AppData/Local/Temp/drydock-no-hooks-1", "worktree", "add", "-b", "drydock/T1-x", `\\wsl.localhost\Ubuntu\home\k\.drydock-worktrees\p1\T1`, "main",
			"-m", "message=C:\\keep"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-d", "Ubuntu", "--cd", "/home/k/app", "--exec", "/usr/bin/env", "GIT_INDEX_FILE=/mnt/c/Users/k/AppData/Local/Temp/drydock-index-1", "GIT_AUTHOR_NAME=Drydock",
		"git", "-c", "core.hooksPath=/mnt/c/Users/k/AppData/Local/Temp/drydock-no-hooks-1", "worktree", "add", "-b", "drydock/T1-x", "/home/k/.drydock-worktrees/p1/T1", "main",
		"-m", "message=C:\\keep"} // a message is never rewritten
	if path != "wsl.exe" || !slices.Equal(argv, want) {
		t.Fatalf("\n got %q\nwant %q", argv, want)
	}
	if _, _, _, err := g.Command(`\\wsl.localhost\Debian\x`, nil, []string{"status"}); err == nil {
		t.Fatal("another distro's folder")
	}
	if h := g.HostPath("/home/k/app/.git"); h != `\\wsl.localhost\Ubuntu\home\k\app\.git` {
		t.Fatal(h)
	}
}

// hostRunner is the GitRunner of the stand-in distro on a development host, whose paths are this host's own.
type hostRunner struct{ wsl.GitRunner }

func (hostRunner) HostPath(p string) string { return p }

func TestARepositoryOpenedThroughTheDistroRunsEveryGitCallThereAndLinksWorktreesWithItsPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in wsl.exe runs on a Linux development host; Windows CI drives a real distro (internal/wsl TestRealWSLRepository)")
	}
	r0, _ := newRepo(t)
	f := wsltest.NewFake(t, false, "Ubuntu")
	run := hostRunner{wsl.GitRunner{Exe: f.Exe, Paths: wsl.Paths{Distro: "Ubuntu", MountRoot: "/mnt/"}}}
	ctx := context.Background()
	r, err := OpenIn(ctx, r0.Root, run)
	if err != nil {
		t.Fatal(err)
	}
	parent := r.WorktreeParent("unused", "p1")
	if parent != filepath.Join(filepath.Dir(r0.Root), ".drydock-worktrees", "p1") {
		t.Fatalf("parent %s", parent)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(filepath.Dir(r0.Root), ".drydock-worktrees")) })
	w, err := r.CreateWorktree(ctx, WorktreeReq{TaskID: "T1", Title: "wsl", Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	write(t, w.Path, "src/new.go", "package main\n")
	if _, err := r.SafePoint(ctx, w, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Commit(ctx, w, "add new"); err != nil {
		t.Fatal(err)
	}
	if st, err := r.Status(ctx, w); err != nil || st.Dirty() {
		t.Fatalf("%+v %v", st, err)
	}
	if id, err := r.PatchID(ctx, w, "main"); err != nil || id == "" {
		t.Fatalf("patch-id: %q %v", id, err)
	}
	b, _ := os.ReadFile(filepath.Join(w.Path, ".git"))
	if !strings.HasPrefix(string(b), "gitdir: /") {
		t.Fatalf("the worktree links with the distro's absolute path: %q", b)
	}
	calls := f.Calls()
	n := 0
	for _, c := range calls {
		if slices.Contains(c, "git") && slices.Contains(c, "core.fsmonitor=false") {
			n++
		}
	}
	if n < 5 {
		t.Fatalf("only %d of %d calls ran the distro's git with the hardening", n, len(calls))
	}
}

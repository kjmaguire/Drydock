package git

import (
	"context"
	"errors"
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

// TestRealWSLRepository proves a repository inside a real distro (CI's wsl job sets DRYDOCK_WSL_TEST_DISTRO): opened through
// \\wsl.localhost with the distro's git, its task worktree is made next to it inside the distro, Drydock's commit and status
// work, and the distro's own git can use the worktree (its links are Linux paths). It also records what a plain Windows git
// does with that path when it gets safe.directory on its command line (the fallback of the chokepoint).
func TestRealWSLRepository(t *testing.T) {
	distro := os.Getenv("DRYDOCK_WSL_TEST_DISTRO")
	if runtime.GOOS != "windows" || distro == "" {
		t.Skip("needs Windows with a WSL distro named in DRYDOCK_WSL_TEST_DISTRO (CI's wsl job)")
	}
	ctx := context.Background()
	h := &wsl.Host{}
	exe, err := h.Path()
	if err != nil {
		t.Fatal(err)
	}
	in, err := h.Probe(ctx, distro, "git", []string{"--version"}, nil, true)
	if err != nil || in.Tool == "" {
		t.Fatalf("the distro has no git: %+v %v", in, err)
	}
	base := "/tmp/drydock-wsl-repo-" + wsl.NewMarker()
	inDistro := func(dir string, args ...string) string {
		t.Helper()
		full := append([]string{"-d", distro, "--cd", dir, "--exec"}, args...)
		out, err := exec.Command(exe, full...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
		return string(out)
	}
	inDistro("/", "mkdir", "-p", base+"/app")
	t.Cleanup(func() { _ = exec.Command(exe, "-d", distro, "--exec", "rm", "-rf", base).Run() })
	inDistro(base+"/app", "/bin/sh", "-c", "git init -q -b main && printf 'hello\\n' > README.md && git add -A && git -c user.name=t -c user.email=t@t commit -q -m init")
	unc := wsl.UNC(distro, base+"/app")
	r, err := OpenIn(ctx, unc, wsl.GitRunner{Exe: exe, Paths: in.Paths})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(r.Root, unc) {
		t.Fatalf("root %s, want %s", r.Root, unc)
	}
	parent := r.WorktreeParent(t.TempDir(), "p1")
	if parent != wsl.UNC(distro, base+"/.drydock-worktrees/p1") {
		t.Fatalf("the worktree parent is not next to the repository inside the distro: %s", parent)
	}
	w, err := r.CreateWorktree(ctx, WorktreeReq{TaskID: "T1", Title: "wsl", Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Path, "new.txt"), []byte("written from Windows through \\\\wsl.localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Commit(ctx, w, "add new"); err != nil {
		t.Fatal(err)
	}
	if st, err := r.Status(ctx, w); err != nil || st.Dirty() {
		t.Fatalf("%+v %v", st, err)
	}
	b, _ := os.ReadFile(filepath.Join(w.Path, ".git"))
	if !strings.HasPrefix(string(b), "gitdir: /") {
		t.Fatalf("the worktree's link is not a Linux path: %q", b)
	}
	// the agent's (and the person's) git inside the distro uses the worktree as it is
	log := inDistro(base+"/.drydock-worktrees/p1/T1", "git", "log", "--oneline", "-1")
	if !strings.Contains(log, "add new") {
		t.Fatalf("the distro's git does not see Drydock's commit: %s", log)
	}
	// the fallback: a plain Windows git reaching the repository through \\wsl.localhost, with safe.directory on its own
	// command line. Recorded, not asserted: Drydock opens a distro repository with the distro's git.
	plain := &Repo{Root: unc}
	if out, err := plain.gitUnguarded(ctx, unc, nil, "rev-parse", "--show-toplevel"); err != nil {
		t.Logf("Windows git with safe.directory on %s: %v", unc, err)
	} else {
		t.Logf("Windows git with safe.directory on %s answered %s", unc, strings.TrimSpace(out))
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

// A repository the distro's git cannot read (a broken .git/config) is ErrNotARepo with git's own words after the path, as on
// this machine; a runner whose git cannot run at all says why in brackets, so the person is never told only "not a repository".
func TestOpenInTellsABrokenRepositoryFromAGitThatCannotRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in wsl.exe runs on a Linux development host")
	}
	r0, _ := newRepo(t)
	f := wsltest.NewFake(t, false, "Ubuntu")
	run := hostRunner{wsl.GitRunner{Exe: f.Exe, Paths: wsl.Paths{Distro: "Ubuntu", MountRoot: "/mnt/"}}}
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(r0.Root, ".git", "config"), []byte("this is not a git configuration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := OpenIn(ctx, r0.Root, run)
	if !errors.Is(err, ErrNotARepo) || !strings.Contains(err.Error(), r0.Root+": ") || !strings.Contains(err.Error(), "bad config") {
		t.Fatalf("a broken .git/config through the distro's git: %v", err)
	}
	noGit := hostRunner{wsl.GitRunner{Exe: filepath.Join(t.TempDir(), "no-wsl.exe"), Paths: wsl.Paths{Distro: "Ubuntu", MountRoot: "/mnt/"}}}
	_, err = OpenIn(ctx, r0.Root, noGit)
	if !errors.Is(err, ErrNotARepo) || !strings.Contains(err.Error(), r0.Root+" (") {
		t.Fatalf("a git that cannot run says why, in brackets after the path: %v", err)
	}
}

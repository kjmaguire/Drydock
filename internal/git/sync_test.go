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

// The sync tests (decision P143) use local repositories only: an upstream working repository, a bare "remote" cloned from it,
// and the project's folder cloned from the bare one, so the fetch and the fast-forward run exactly as they would against
// GitHub, minus the network.

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

type syncFixture struct {
	upstream string // a working repository that pushes to the remote
	remote   string // the bare repository standing in for GitHub
	local    *Repo  // the project's folder
}

func newSyncFixture(t *testing.T) syncFixture {
	t.Helper()
	up, _ := newRepo(t)
	bare := filepath.Join(t.TempDir(), "remote.git")
	run(t, up.Root, "clone", "--quiet", "--bare", up.Root, bare)
	run(t, up.Root, "remote", "add", "origin", bare)
	dst := filepath.Join(t.TempDir(), "project")
	run(t, t.TempDir(), "clone", "--quiet", bare, dst)
	local, err := Open(context.Background(), dst)
	if err != nil {
		t.Fatal(err)
	}
	return syncFixture{upstream: up.Root, remote: bare, local: local}
}

// push makes a commit upstream and pushes it to the remote.
func (f syncFixture) push(t *testing.T, file, content string) {
	t.Helper()
	write(t, f.upstream, file, content)
	run(t, f.upstream, "add", "-A")
	run(t, f.upstream, "commit", "-q", "-m", "upstream "+file)
	run(t, f.upstream, "push", "-q", "origin", "main")
}

func head(t *testing.T, dir, ref string) string { t.Helper(); return run(t, dir, "rev-parse", ref) }

func TestFetchThenFastForwardACleanBranch(t *testing.T) {
	ctx := context.Background()
	f := newSyncFixture(t)
	st, err := f.local.RemoteState(ctx, "origin", "main")
	if err != nil || !st.Tracked || st.Ahead != 0 || st.Behind != 0 || st.Current != "main" {
		t.Fatalf("fresh clone: %+v %v", st, err)
	}
	f.push(t, "NEW.md", "new\n")
	// before a fetch the folder knows nothing of it
	if st, _ := f.local.RemoteState(ctx, "origin", "main"); st.Behind != 0 {
		t.Fatalf("no fetch yet, behind %d", st.Behind)
	}
	before := head(t, f.local.Root, "HEAD")
	if err := f.local.Fetch(ctx, "origin"); err != nil {
		t.Fatal(err)
	}
	st, _ = f.local.RemoteState(ctx, "origin", "main")
	if st.Behind != 1 || st.Ahead != 0 {
		t.Fatalf("after fetch: %+v", st)
	}
	// a fetch changes no file and no local branch
	if head(t, f.local.Root, "HEAD") != before {
		t.Fatal("fetch moved the branch")
	}
	if _, err := os.Stat(filepath.Join(f.local.Root, "NEW.md")); err == nil {
		t.Fatal("fetch wrote a file")
	}
	out, st, err := f.local.FastForward(ctx, "origin", "main")
	if err != nil || out != SyncUpdated || st.Behind != 0 || st.Ahead != 0 {
		t.Fatalf("fast-forward: %s %+v %v", out, st, err)
	}
	if head(t, f.local.Root, "HEAD") != head(t, f.upstream, "HEAD") {
		t.Fatal("the branch is not at the remote's commit")
	}
	if _, err := os.Stat(filepath.Join(f.local.Root, "NEW.md")); err != nil {
		t.Fatal("the new file is not in the folder")
	}
	if out, _, _ := f.local.FastForward(ctx, "origin", "main"); out != SyncUpToDate {
		t.Fatalf("again: %s", out)
	}
}

func TestFastForwardRefusesLocalChanges(t *testing.T) {
	ctx := context.Background()
	f := newSyncFixture(t)
	f.push(t, "NEW.md", "new\n")
	if err := f.local.Fetch(ctx, "origin"); err != nil {
		t.Fatal(err)
	}
	write(t, f.local.Root, "README.md", "# edited by the person\n")
	before := head(t, f.local.Root, "HEAD")
	out, st, err := f.local.FastForward(ctx, "origin", "main")
	if err != nil || out != SyncDirty || st.Behind != 1 {
		t.Fatalf("dirty: %s %+v %v", out, st, err)
	}
	if head(t, f.local.Root, "HEAD") != before {
		t.Fatal("a dirty tree's branch moved")
	}
	if b, _ := os.ReadFile(filepath.Join(f.local.Root, "README.md")); string(b) != "# edited by the person\n" {
		t.Fatalf("the person's edit was touched: %q", b)
	}
	// an untracked file counts too: the tree is left as it is
	run(t, f.local.Root, "checkout", "-q", "--", "README.md")
	write(t, f.local.Root, "scratch.txt", "notes\n")
	if out, _, _ := f.local.FastForward(ctx, "origin", "main"); out != SyncDirty {
		t.Fatalf("untracked: %s", out)
	}
}

func TestFastForwardRefusesADivergedBranch(t *testing.T) {
	ctx := context.Background()
	f := newSyncFixture(t)
	f.push(t, "NEW.md", "new\n")
	write(t, f.local.Root, "LOCAL.md", "mine\n")
	run(t, f.local.Root, "add", "-A")
	run(t, f.local.Root, "commit", "-q", "-m", "local")
	mine := head(t, f.local.Root, "HEAD")
	if err := f.local.Fetch(ctx, "origin"); err != nil {
		t.Fatal(err)
	}
	out, st, err := f.local.FastForward(ctx, "origin", "main")
	if err != nil || out != SyncDiverged || st.Ahead != 1 || st.Behind != 1 {
		t.Fatalf("diverged: %s %+v %v", out, st, err)
	}
	if head(t, f.local.Root, "HEAD") != mine {
		t.Fatal("a diverged branch was changed")
	}
	// ahead only: nothing to bring in, and nothing is pushed
	g := newSyncFixture(t)
	write(t, g.local.Root, "LOCAL.md", "mine\n")
	run(t, g.local.Root, "add", "-A")
	run(t, g.local.Root, "commit", "-q", "-m", "local")
	if out, st, _ := g.local.FastForward(ctx, "origin", "main"); out != SyncAhead || st.Ahead != 1 {
		t.Fatalf("ahead: %s %+v", out, st)
	}
	if head(t, g.remote, "main") == head(t, g.local.Root, "HEAD") {
		t.Fatal("the local commit reached the remote")
	}
}

func TestFastForwardRefusesAnotherBranch(t *testing.T) {
	ctx := context.Background()
	f := newSyncFixture(t)
	f.push(t, "NEW.md", "new\n")
	if err := f.local.Fetch(ctx, "origin"); err != nil {
		t.Fatal(err)
	}
	run(t, f.local.Root, "checkout", "-q", "-b", "experiment")
	mainBefore := head(t, f.local.Root, "refs/heads/main")
	out, st, err := f.local.FastForward(ctx, "origin", "main")
	if err != nil || out != SyncOtherBranch || st.Current != "experiment" || st.Behind != 1 {
		t.Fatalf("other branch: %s %+v %v", out, st, err)
	}
	if head(t, f.local.Root, "refs/heads/main") != mainBefore {
		t.Fatal("main moved while another branch was checked out")
	}
}

func TestFetchAndFastForwardLeaveTaskBranchesAlone(t *testing.T) {
	ctx := context.Background()
	f := newSyncFixture(t)
	w, err := f.local.CreateWorktree(ctx, WorktreeReq{TaskID: "task_1", Title: "Work", Parent: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	write(t, w.Path, "TASK.md", "task\n")
	if _, err := f.local.Commit(ctx, w, "task work"); err != nil {
		t.Fatal(err)
	}
	taskHead := head(t, f.local.Root, "refs/heads/"+w.Branch)
	f.push(t, "NEW.md", "new\n")
	if err := f.local.Fetch(ctx, "origin"); err != nil {
		t.Fatal(err)
	}
	if out, _, err := f.local.FastForward(ctx, "origin", "main"); err != nil || out != SyncUpdated {
		t.Fatalf("%s %v", out, err)
	}
	if head(t, f.local.Root, "refs/heads/"+w.Branch) != taskHead {
		t.Fatal("the task branch moved")
	}
	if _, err := os.Stat(filepath.Join(w.Path, "NEW.md")); err == nil {
		t.Fatal("the task worktree was changed")
	}
}

func TestSyncWithoutARemote(t *testing.T) {
	ctx := context.Background()
	r, _ := newRepo(t)
	if err := r.Fetch(ctx, "origin"); !errors.Is(err, ErrNoRemote) {
		t.Fatalf("fetch without a remote: %v", err)
	}
	st, err := r.RemoteState(ctx, "origin", "main")
	if err != nil || st.Tracked {
		t.Fatalf("state without a remote: %+v %v", st, err)
	}
	if out, _, err := r.FastForward(ctx, "origin", "main"); err != nil || out != SyncNoRemoteBranch {
		t.Fatalf("fast-forward without a remote: %s %v", out, err)
	}
	// names are names, never options
	if err := r.Fetch(ctx, "--upload-pack=x"); err == nil {
		t.Fatal("an option was taken as a remote")
	}
	if _, err := r.RemoteState(ctx, "origin", "-x"); err == nil {
		t.Fatal("an option was taken as a branch")
	}
}

// A single-branch clone fetches its one branch; a configured refspec that would write anywhere but the remote's own
// tracking branches is not used.
func TestFetchKeepsTheConfiguredRefspec(t *testing.T) {
	ctx := context.Background()
	f := newSyncFixture(t)
	run(t, f.upstream, "branch", "side")
	run(t, f.upstream, "push", "-q", "origin", "side")
	run(t, f.local.Root, "config", "--replace-all", "remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/main")
	local, err := Open(ctx, f.local.Root) // the person's own change of the configuration, trusted as it opens
	if err != nil {
		t.Fatal(err)
	}
	if got := local.fetchSpecs(ctx, "origin"); len(got) != 1 || got[0] != "+refs/heads/main:refs/remotes/origin/main" {
		t.Fatalf("specs %v", got)
	}
	if err := local.Fetch(ctx, "origin"); err != nil {
		t.Fatal(err)
	}
	if out, _ := local.git(ctx, local.Root, nil, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/side"); strings.TrimSpace(out) != "" {
		t.Fatal("a single-branch clone fetched another branch")
	}
	run(t, f.local.Root, "config", "--replace-all", "remote.origin.fetch", "+refs/heads/*:refs/heads/*")
	local, err = Open(ctx, f.local.Root)
	if err != nil {
		t.Fatal(err)
	}
	if got := local.fetchSpecs(ctx, "origin"); len(got) != 1 || got[0] != "+refs/heads/*:refs/remotes/origin/*" {
		t.Fatalf("a refspec writing local branches was used: %v", got)
	}
}

package core_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
)

// Sync now and the background check (decision P143) against local repositories: a bare "remote" and an upstream that pushes to
// it; the project's folder is a clone of the remote. No network.

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// syncWorld is an upstream repository, the bare remote it pushes to, and a clone of the remote for the project.
func syncWorld(t *testing.T) (upstream, remote, folder string) {
	t.Helper()
	upstream = newRepo(t)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	remote = filepath.Join(base, "remote.git")
	gitRun(t, upstream, "clone", "--quiet", "--bare", upstream, remote)
	gitRun(t, upstream, "remote", "add", "origin", remote)
	folder = filepath.Join(base, "project")
	gitRun(t, base, "clone", "--quiet", remote, folder)
	return upstream, remote, folder
}

func pushUpstream(t *testing.T, upstream, file string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(upstream, file), []byte(file+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, upstream, "add", "-A")
	gitRun(t, upstream, "commit", "-q", "-m", "upstream "+file)
	gitRun(t, upstream, "push", "-q", "origin", "main")
}

func TestRepoSyncNowFastForwardsAndRefusesLocalChanges(t *testing.T) {
	a := open(t, "")
	upstream, _, folder := syncWorld(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "synced", "repoPath": folder}, &p)

	// turn the background check off first, so this test decides every fetch
	var set core.RepoSyncSettings
	a.call("repo.syncSettings.set", map[string]any{"auto": false}, &set)
	if set.Auto {
		t.Fatal("the background check is still on")
	}
	var st core.RepoSync
	a.call("repo.syncState", map[string]any{"projectId": p.ID}, &st)
	if st.Remote != "origin" || st.GitHub || st.Branch != "main" || !st.Tracked || st.Behind != 0 || st.CheckedAt != 0 || st.Checking {
		t.Fatalf("fresh state: %+v", st)
	}

	pushUpstream(t, upstream, "ONE.md")
	// a person's edit in the folder: Sync now fetches but leaves the files alone
	if err := os.WriteFile(filepath.Join(folder, "go.mod"), []byte("module edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.call("repo.sync", map[string]any{"projectId": p.ID}, &st)
	if st.Outcome != "local_changes" || st.Behind != 1 || st.CheckedAt == 0 || st.Error != "" {
		t.Fatalf("dirty sync: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(folder, "ONE.md")); err == nil {
		t.Fatal("a dirty folder was changed")
	}
	gitRun(t, folder, "checkout", "-q", "--", "go.mod")

	// now clean: Sync now fast-forwards, and announces it
	sub, stop := a.c.Bus.Subscribe(events.Filter{Types: []string{"repo.synced"}})
	defer stop()
	a.call("repo.sync", map[string]any{"projectId": p.ID}, &st)
	if st.Outcome != "updated" || st.Behind != 0 || st.Ahead != 0 {
		t.Fatalf("clean sync: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(folder, "ONE.md")); err != nil {
		t.Fatal("the remote's commit is not in the folder")
	}
	select {
	case ev := <-sub:
		if ev.ProjectID != p.ID || strings.Contains(string(ev.Data), folder) {
			t.Fatalf("event %s %s", ev.ProjectID, ev.Data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no repo.synced event")
	}
	a.call("repo.sync", map[string]any{"projectId": p.ID}, &st)
	if st.Outcome != "up_to_date" {
		t.Fatalf("again: %+v", st)
	}
}

func TestRepoSyncOpeningAProjectFetchesOnlyWhenOn(t *testing.T) {
	a := open(t, "")
	upstream, _, folder := syncWorld(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "auto", "repoPath": folder}, &p)
	before := gitRun(t, folder, "rev-parse", "HEAD")
	pushUpstream(t, upstream, "TWO.md")

	var set core.RepoSyncSettings
	a.call("repo.syncSettings.get", nil, &set)
	if !set.Auto {
		t.Fatal("the background check is on by default")
	}
	var st core.RepoSync
	a.call("repo.syncState", map[string]any{"projectId": p.ID}, &st)
	if !st.Checking {
		t.Fatalf("opening the project starts a check: %+v", st)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		a.call("repo.syncState", map[string]any{"projectId": p.ID}, &st)
		if st.CheckedAt != 0 && !st.Checking {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the check never finished: %+v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st.Behind != 1 || st.Outcome != "" {
		t.Fatalf("after the check: %+v", st)
	}
	// a background check changes no file and no branch
	if gitRun(t, folder, "rev-parse", "HEAD") != before {
		t.Fatal("the background check moved the branch")
	}
	// read again right away: no second fetch
	a.call("repo.syncState", map[string]any{"projectId": p.ID}, &st)
	if st.Checking {
		t.Fatal("a fresh check was repeated")
	}
}

func TestRepoSyncWithoutARemoteOrRepository(t *testing.T) {
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "local-only", "repoPath": newRepo(t)}, &p)
	var st core.RepoSync
	a.call("repo.syncState", map[string]any{"projectId": p.ID}, &st)
	if st.Remote != "" || st.Checking {
		t.Fatalf("no remote: %+v", st)
	}
	if _, err := a.c.Call(context.Background(), "repo.sync", mustJSON(map[string]any{"projectId": p.ID})); err == nil || !strings.Contains(err.Error(), "no 'origin' remote") {
		t.Fatalf("sync without a remote: %v", err)
	}
	var none core.Project
	a.call("project.create", map[string]any{"name": "no-repo"}, &none)
	a.call("repo.syncState", map[string]any{"projectId": none.ID}, &st)
	if st.Remote != "" {
		t.Fatalf("no repository: %+v", st)
	}
}

// A failed fetch says why without the remote's login: a token in the address never reaches the page.
func TestRepoSyncFailureHidesTheLogin(t *testing.T) {
	a := open(t, "")
	folder := newRepo(t)
	gitRun(t, folder, "remote", "add", "origin", "https://someone:ghp_abcdefghijklmnopqrstuvwxyz0123456789@127.0.0.1:1/acme/x.git")
	var p core.Project
	a.call("project.create", map[string]any{"name": "bad-remote", "repoPath": folder}, &p)
	var set core.RepoSyncSettings
	a.call("repo.syncSettings.set", map[string]any{"auto": false}, &set)
	var st core.RepoSync
	a.call("repo.sync", map[string]any{"projectId": p.ID}, &st)
	if st.Error == "" || st.CheckedAt != 0 {
		t.Fatalf("a failed fetch: %+v", st)
	}
	b, _ := json.Marshal(st)
	if strings.Contains(string(b), "ghp_abcdefghijklmnopqrstuvwxyz0123456789") || strings.Contains(string(b), "someone:") {
		t.Fatalf("the login leaked: %s", b)
	}
}

// The folder last cloned into is where the next clone starts.
func TestGitHubCloneRemembersTheFolder(t *testing.T) {
	src := newRepo(t)
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		GitHubCloneURL: func(string) string { return src }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	var dir string
	a.call("github.cloneDir", nil, &dir)
	if dir != core.DefaultCloneDir() {
		t.Fatalf("before any clone: %q", dir)
	}
	parent := filepath.Join(t.TempDir(), "my code")
	var cl core.CloneResult
	a.call("github.clone", map[string]any{"repo": "acme/gocalc", "dir": parent}, &cl)
	a.call("github.cloneDir", nil, &dir)
	if dir != parent {
		t.Fatalf("after a clone into %q: %q", parent, dir)
	}
}

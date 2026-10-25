package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/git"
)

// originRepo is a repository made in a temp folder for remoteRepo, with git on PATH (the test is skipped without it).
func originRepo(t *testing.T) *git.Repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo, err := git.Init(context.Background(), t.TempDir(), "main")
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// gitAt runs git in dir for the test's own scaffolding (adding a remote).
func gitAt(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// The owner and repository come from the origin remote on github.com, and each way that fails is told apart for the person: no
// origin at all, an origin elsewhere, and a repository git itself cannot read (a broken .git/config), which is never mistaken
// for "no origin" and carries git's own words.
func TestRemoteRepoTellsNoOriginFromAnOriginElsewhereFromARepositoryGitCannotRead(t *testing.T) {
	ctx := context.Background()
	repo := originRepo(t)
	if _, _, err := remoteRepo(ctx, repo); err == nil || !strings.Contains(err.Error(), "has no 'origin' remote") {
		t.Fatalf("no origin: %v", err)
	}
	gitAt(t, repo.Root, "remote", "add", "origin", "https://gitlab.com/acme/elsewhere.git")
	if _, _, err := remoteRepo(ctx, repo); err == nil || !strings.Contains(err.Error(), "is not a github.com remote") || !strings.Contains(err.Error(), "gitlab.com/acme/elsewhere") {
		t.Fatalf("an origin elsewhere: %v", err)
	}
	gitAt(t, repo.Root, "remote", "set-url", "origin", "git@github.com:acme/app.git")
	if owner, name, err := remoteRepo(ctx, repo); err != nil || owner != "acme" || name != "app" {
		t.Fatalf("a github.com origin: %s/%s %v", owner, name, err)
	}
	if err := os.WriteFile(filepath.Join(repo.Root, ".git", "config"), []byte("this is not a git configuration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := remoteRepo(ctx, repo)
	if err == nil || !strings.Contains(err.Error(), "could not read the repository's 'origin' remote") || !strings.Contains(err.Error(), "bad config") {
		t.Fatalf("a repository git cannot read carries git's words: %v", err)
	}
	if strings.Contains(err.Error(), "has no 'origin' remote") {
		t.Fatalf("a repository git cannot read must not pass for one without an origin: %v", err)
	}
}

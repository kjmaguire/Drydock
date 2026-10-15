package core_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/runners"
)

// headOf is the commit HEAD names in dir ("" when there is none), read with the test's own git.
func headOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "-q", "--verify", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func realTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// Decision P58: starting from scratch, Drydock makes the project's repository. A folder that does not exist is made, git
// init names the branch main and a first commit lets worktrees start; the files already in a plain folder are that commit.
// A folder that is already a repository is never touched, and one inside another repository's work tree is refused.
func TestRepoInitMakesARepositoryAndNeverTouchesOne(t *testing.T) {
	a := open(t, "")
	base := realTemp(t)

	var f core.RepoFolder
	fresh := filepath.Join(base, "projects", "DryDock - Test Project")
	a.call("repo.check", map[string]string{"path": fresh}, &f)
	if f.State != core.FolderMissing || !f.CanInit {
		t.Fatalf("a folder that does not exist: %+v", f)
	}
	var made core.RepoInit
	a.call("repo.init", map[string]string{"path": `"` + fresh + `"`}, &made) // pasted with "Copy as path" quotes
	if made.Path != fresh || made.Branch != "main" || !made.CreatedFolder || made.Files != 0 || headOf(t, fresh) == "" {
		t.Fatalf("%+v head %q", made, headOf(t, fresh))
	}
	var p core.Project
	a.call("project.create", map[string]any{"name": "fresh", "repoPath": fresh}, &p)
	if !p.HasRepo || p.DefaultBranch != "main" {
		t.Fatalf("the new repository is not usable for a project: %+v", p)
	}

	// a plain folder with files: they are the first commit, what .gitignore leaves out stays out
	plain := filepath.Join(base, "plain")
	for name, body := range map[string]string{"main.go": "package main\n", ".gitignore": "secret.env\n", "secret.env": "TOKEN=x\n"} {
		if err := os.MkdirAll(plain, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(plain, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a.call("repo.check", map[string]string{"path": plain}, &f)
	if f.State != core.FolderPlain || f.Entries != 3 || !strings.Contains(f.Message, "3 items") {
		t.Fatalf("a plain folder: %+v", f)
	}
	a.call("repo.init", map[string]string{"path": plain}, &made)
	if made.CreatedFolder || made.Files != 2 {
		t.Fatalf("%+v", made)
	}

	// an existing repository: reported, never touched
	repo := newRepo(t)
	before := headOf(t, repo)
	a.call("repo.check", map[string]string{"path": repo}, &f)
	if f.State != core.FolderRepository || f.Branch != "main" || f.CanInit {
		t.Fatalf("an existing repository: %+v", f)
	}
	if err := a.callErr("repo.init", map[string]string{"path": repo}); err == nil || !strings.Contains(err.Error(), "already a Git repository") {
		t.Fatalf("an existing repository was initialised again: %v", err)
	}
	if headOf(t, repo) != before {
		t.Fatal("the existing repository was changed")
	}
	unborn := filepath.Join(base, "unborn")
	if out, err := exec.Command("git", "init", "-q", unborn).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	a.call("repo.check", map[string]string{"path": unborn}, &f)
	if f.State != core.FolderNoBranch || f.CanInit {
		t.Fatalf("a repository without a commit: %+v", f)
	}
	if err := a.callErr("repo.init", map[string]string{"path": unborn}); err == nil || headOf(t, unborn) != "" {
		t.Fatalf("a repository without a commit was given one: %v", err)
	}

	// inside another repository's work tree, existing or not: refused, nothing made
	for _, inside := range []string{filepath.Join(repo, "sub"), filepath.Join(repo, "sub", "deeper")} {
		if inside == filepath.Join(repo, "sub") {
			if err := os.MkdirAll(inside, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		a.call("repo.check", map[string]string{"path": inside}, &f)
		if f.State != core.FolderInside || f.Root != repo || f.CanInit || !strings.Contains(f.Message, "inside the Git repository at "+repo) {
			t.Fatalf("%s: %+v", inside, f)
		}
		if err := a.callErr("repo.init", map[string]string{"path": inside}); err == nil || !strings.Contains(err.Error(), "inside the Git repository") {
			t.Fatalf("a repository was made inside another: %v", err)
		}
		if _, err := os.Stat(filepath.Join(inside, ".git")); err == nil {
			t.Fatal("a .git was made inside another repository")
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "sub", "deeper")); err == nil {
		t.Fatal("a folder was made inside another repository")
	}

	// a relative path and a file are refused with the reason
	a.call("repo.check", map[string]string{"path": "code/app"}, &f)
	if f.State != core.FolderInvalid || !strings.Contains(f.Message, "full path") {
		t.Fatalf("%+v", f)
	}
	a.call("repo.check", map[string]string{"path": filepath.Join(plain, "main.go")}, &f)
	if f.State != core.FolderNotFolder {
		t.Fatalf("%+v", f)
	}

	// only a person may make a repository
	if _, err := a.c.InitRepository(context.Background(), filepath.Join(base, "agent"), "agent:agt_1"); err == nil || !strings.Contains(err.Error(), "only a person") {
		t.Fatalf("an agent made a repository: %v", err)
	}
}

// The wizard can make the repository as part of creating a project from scratch; an existing repository is used as it is.
func TestSetupMakesTheRepositoryFirstWhenAsked(t *testing.T) {
	a := open(t, "")
	fresh := filepath.Join(realTemp(t), "from-scratch")
	var res core.SetupResult
	a.call("project.setup", core.SetupRequest{Name: "Scratch", Repo: core.SetupRepo{Kind: "local", Path: fresh, Init: true}}, &res)
	if !res.Project.HasRepo || res.Project.DefaultBranch != "main" || res.Project.RepoPath != fresh {
		t.Fatalf("%+v", res.Project)
	}
	repo := newRepo(t)
	before := headOf(t, repo)
	a.call("project.setup", core.SetupRequest{Repo: core.SetupRepo{Kind: "local", Path: repo, Init: true}}, &res)
	if !res.Project.HasRepo || headOf(t, repo) != before {
		t.Fatalf("an existing repository was changed: %+v", res.Project)
	}
	// without the box ticked, a plain folder is refused as before
	plain := filepath.Join(realTemp(t), "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := a.callErr("project.setup", core.SetupRequest{Repo: core.SetupRepo{Kind: "local", Path: plain}}); err == nil || !strings.Contains(err.Error(), "not a usable Git repository") {
		t.Fatalf("%v", err)
	}
}

// "Choose folder..." opens the window's own folder dialog (Config.PickFolder); without one it says to type the path. The
// folder hints name the Dev Drives found.
func TestPickFolderUsesTheWindowsDialogAndSaysWhenThereIsNone(t *testing.T) {
	want := filepath.Join(t.TempDir(), "chosen")
	var asked core.FolderPick
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		// TEST DOUBLES: the folder dialog and the Dev Drive list
		PickFolder: func(_ context.Context, p core.FolderPick) (string, error) { asked = p; return want, nil },
		DevDrives:  func(context.Context) []runners.Drive { return []runners.Drive{{Letter: "D:", Trusted: true}} },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	var got map[string]string
	a.call("fs.pickFolder", map[string]string{"title": "Choose the project's folder"}, &got)
	if got["path"] != want || asked.Title != "Choose the project's folder" {
		t.Fatalf("%v %+v", got, asked)
	}
	var h core.FolderHints
	a.call("fs.folderHints", struct{}{}, &h)
	if !h.CanPick || len(h.DevDrives) != 1 || h.DevDrives[0].Letter != "D:" {
		t.Fatalf("%+v", h)
	}
	if _, err := c.PickFolder(context.Background(), "", "agent:agt_1"); err == nil {
		t.Fatal("an agent opened the folder dialog")
	}

	b := open(t, "")
	if err := b.callErr("fs.pickFolder", map[string]string{}); err == nil || !strings.Contains(err.Error(), "type the folder's full path") {
		t.Fatalf("without a dialog: %v", err)
	}
	b.call("fs.folderHints", struct{}{}, &h)
	if h.CanPick || h.DevDrives == nil {
		t.Fatalf("%+v", h)
	}
}

//go:build !windows

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
)

// B12: New project's Clone makes the project at once, with no repository, and attaches the clone when it lands.
func TestSetupLaterMakesTheProjectFirstAndAttachesTheClone(t *testing.T) {
	src := newRepo(t)
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		GitHubCloneURL: func(string) string { return "file://" + src }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	parent := t.TempDir()
	var res core.SetupResult
	a.call("project.setup", map[string]any{"repo": map[string]any{"kind": "github", "github": "acme/gocalc", "cloneDir": parent, "later": true}}, &res)
	if res.Project.ID == "" || res.Project.Name != "gocalc" {
		t.Fatalf("the project is made at once, named after the repository: %+v", res.Project)
	}
	if res.Clone != nil || res.Landing == nil || !res.Landing.Running || res.Landing.Repo != "acme/gocalc" {
		t.Fatalf("the answer says the clone is landing: clone %+v, landing %+v", res.Clone, res.Landing)
	}
	a.waitFor("the clone to land", func() bool { return len(a.eventsOf("project.clone_landed")) == 1 })
	var p core.Project
	a.call("project.get", map[string]string{"id": res.Project.ID}, &p)
	if !p.HasRepo || filepath.Clean(p.RepoPath) != filepath.Join(parent, "gocalc") {
		t.Fatalf("the clone is the project's repository: %+v", p)
	}
	if _, err := os.Stat(filepath.Join(p.RepoPath, "go.mod")); err != nil {
		t.Fatalf("cloned files: %v", err)
	}
	var state *core.LandingClone
	a.call("project.cloneState", map[string]string{"id": res.Project.ID}, &state)
	if state != nil {
		t.Fatalf("a landed clone leaves no state: %+v", state)
	}
}

// B12: a clone that could not start is refused before the project is made; one that is cancelled while it runs leaves the
// project without a repository and says why.
func TestSetupLaterRefusedUpFrontOrCancelledKeepsTheProject(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep")
	}
	bin := t.TempDir()
	// TEST DOUBLE of git for the clone: it makes the folder, prints a progress line, then waits as a slow network would
	script := "#!/bin/sh\nif [ \"$1\" != clone ]; then exec '" + realGit + "' \"$@\"; fi\nmkdir -p \"$4\"\necho made > \"$4/half\"\nprintf 'Receiving objects:  42%% (21/50)\\r' >&2\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		GitHubCloneURL: func(string) string { return "https://example.invalid/acme/slow.git" }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}

	// a target folder that is not empty is refused before anything is made
	full := t.TempDir()
	if err := os.MkdirAll(filepath.Join(full, "slow"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "slow", "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call(context.Background(), "project.setup", mustJSON(map[string]any{"repo": map[string]any{"kind": "github", "github": "acme/slow", "cloneDir": full, "later": true}})); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("a full target folder answered %v", err)
	}
	var projects []core.Project
	a.call("project.list", struct{}{}, &projects)
	if len(projects) != 0 {
		t.Fatalf("a refused clone made a project: %+v", projects)
	}

	parent := t.TempDir()
	var res core.SetupResult
	a.call("project.setup", map[string]any{"name": "Slow", "repo": map[string]any{"kind": "github", "github": "acme/slow", "cloneDir": parent, "later": true}}, &res)
	a.waitFor("the clone's progress", func() bool { return len(a.eventsOf("github.clone_progress")) > 0 })
	var got map[string]bool
	a.call("github.cloneCancel", map[string]string{"repo": "acme/slow"}, &got)
	if !got["cancelled"] {
		t.Fatal("the landing clone was not cancelled")
	}
	a.waitFor("the clone to end", func() bool { return len(a.eventsOf("project.clone_failed")) == 1 })
	var state *core.LandingClone
	a.call("project.cloneState", map[string]string{"id": res.Project.ID}, &state)
	if state == nil || state.Running || !strings.Contains(state.Error, "was cancelled") || state.Repo != "acme/slow" {
		t.Fatalf("the project says why it has no repository: %+v", state)
	}
	var p core.Project
	a.call("project.get", map[string]string{"id": res.Project.ID}, &p)
	if p.ID == "" || p.HasRepo {
		t.Fatalf("the project stays, without a repository: %+v", p)
	}
	if _, err := os.Stat(filepath.Join(parent, "slow")); !os.IsNotExist(err) {
		t.Fatalf("the half-made folder is still there: %v", err)
	}
}

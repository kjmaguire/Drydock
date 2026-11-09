//go:build !windows

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
	"github.com/kjmaguire/drydock/internal/terminal"
)

// B12: a clone shows its progress (github.clone_progress: the repository, the phase and the percent, nothing else) and the
// clone ends with the project's repository as before.
func TestGitHubClonePublishesItsProgress(t *testing.T) {
	src := newRepo(t)
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		// file:// makes git use its pack protocol, which reports progress, as a clone from GitHub does
		GitHubCloneURL: func(string) string { return "file://" + src }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	var cl core.CloneResult
	a.call("github.clone", map[string]any{"repo": "acme/gocalc", "dir": t.TempDir()}, &cl)
	if _, err := os.Stat(filepath.Join(cl.Path, "go.mod")); err != nil {
		t.Fatalf("cloned files: %v", err)
	}
	ev := a.eventsOf("github.clone_progress")
	if len(ev) == 0 {
		t.Fatal("no progress was published")
	}
	sawFull := false
	for _, e := range ev {
		delete(e, "_run")
		delete(e, "_task")
		delete(e, "_agent")
		if e["repo"] != "acme/gocalc" || e["phase"] == "" || len(e) != 3 {
			t.Fatalf("a progress event carries the repository, the phase and the percent only: %v", e)
		}
		if e["percent"] == float64(100) {
			sawFull = true
		}
	}
	if !sawFull {
		t.Fatalf("no phase reached 100%%: %v", ev)
	}
}

// B12: Cancel ends a running clone at once: the program is killed, the half-made folder is removed, the clone answers that it
// was cancelled, and asking to cancel when nothing runs says so.
func TestGitHubCloneCancel(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	bin := t.TempDir()
	// TEST DOUBLE of git for the clone: it makes the folder, prints a progress line, then waits as a slow network would; any
	// other git command runs the real git
	script := "#!/bin/sh\nif [ \"$1\" != clone ]; then exec '" + realGit + "' \"$@\"; fi\nmkdir -p \"$4\"\necho made > \"$4/half\"\nprintf 'Receiving objects:  42%% (21/50)\\r' >&2\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep")
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		GitHubCloneURL: func(string) string { return "https://example.invalid/acme/slow.git" }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	var none map[string]bool
	a.call("github.cloneCancel", map[string]string{"repo": "acme/slow"}, &none)
	if none["cancelled"] {
		t.Fatal("cancelled a clone that was not running")
	}
	parent := t.TempDir()
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := c.Call(context.Background(), "github.clone", mustJSON(map[string]any{"repo": "acme/slow", "dir": parent}))
		done <- err
	}()
	a.waitFor("the clone's progress", func() bool {
		for _, e := range a.eventsOf("github.clone_progress") {
			if e["repo"] == "acme/slow" && e["percent"] == float64(42) && e["phase"] == "Receiving objects" {
				return true
			}
		}
		return false
	})
	var got map[string]bool
	a.call("github.cloneCancel", map[string]string{"repo": "https://github.com/acme/slow"}, &got)
	if !got["cancelled"] {
		t.Fatal("the running clone was not cancelled")
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "was cancelled") {
			t.Fatalf("the clone answered %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the clone did not end after Cancel")
	}
	if time.Since(start) > 30*time.Second {
		t.Fatal("the clone ran to its end instead of being cancelled")
	}
	if _, err := os.Stat(filepath.Join(parent, "slow")); !os.IsNotExist(err) {
		t.Fatalf("the half-made folder is still there: %v", err)
	}
	if len(a.eventsOf("github.clone_cancelled")) != 1 {
		t.Fatal("no github.clone_cancelled event")
	}
}

// B12: a second clone of the same repository (or of another into the same folder) while one runs is refused before anything
// is touched: the folder the first clone is writing stays, and the answer is the refusal alone, not "cloning failed".
func TestGitHubCloneRefusedSecondCloneLeavesTheFirstsFolder(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep")
	}
	bin := t.TempDir()
	// TEST DOUBLE of git for the clone: it makes the (still empty) folder, prints a progress line, then waits as a slow
	// network would; any other git command runs the real git
	script := "#!/bin/sh\nif [ \"$1\" != clone ]; then exec '" + realGit + "' \"$@\"; fi\nmkdir -p \"$4\"\nprintf 'Receiving objects:  3%% (1/50)\\r' >&2\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		GitHubCloneURL: func(string) string { return "https://example.invalid/slow.git" }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	parent := t.TempDir()
	done := make(chan error, 1)
	go func() {
		_, err := c.Call(context.Background(), "github.clone", mustJSON(map[string]any{"repo": "acme/slow", "dir": parent}))
		done <- err
	}()
	a.waitFor("the first clone's progress", func() bool { return len(a.eventsOf("github.clone_progress")) > 0 })
	dest := filepath.Join(parent, "slow")
	for _, repo := range []string{"acme/slow", "otherorg/slow"} {
		err := a.callErr("github.clone", map[string]any{"repo": repo, "dir": parent})
		if err == nil || !strings.Contains(err.Error(), "being cloned already") || strings.Contains(err.Error(), "failed") {
			t.Fatalf("a second clone (%s) answered %v", repo, err)
		}
		if st, err := os.Stat(dest); err != nil || !st.IsDir() {
			t.Fatalf("the second clone (%s) removed the folder the first is writing: %v", repo, err)
		}
	}
	var got map[string]bool
	a.call("github.cloneCancel", map[string]string{"repo": "acme/slow"}, &got)
	if !got["cancelled"] {
		t.Fatal("the first clone was not running any more")
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "was cancelled") {
			t.Fatalf("the first clone answered %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the first clone did not end after Cancel")
	}
}

// F4: Sign in runs the person's own `gh auth login` in a terminal; when it ends the core asks gh again (past the kept
// github.status answer) and github.sign_in_finished says the new state. A gh that is not there is refused with what to do.
func TestGitHubSignInRunsGhAuthLogin(t *testing.T) {
	ghOnPath(t, ghFixture{Login: "", AuthLogin: "kyle"})
	a := open(t, "")
	var st core.GitHubStatus
	a.call("github.status", nil, &st)
	if !st.Installed || st.LoggedIn {
		t.Fatalf("before: %+v", st)
	}
	var in terminal.Info
	a.call("github.signIn", nil, &in)
	if in.ID == "" || !strings.Contains(in.Title, "GitHub") {
		t.Fatalf("terminal %+v", in)
	}
	a.waitFor("github.sign_in_finished", func() bool { return len(a.eventsOf("github.sign_in_finished")) == 1 })
	e := a.eventsOf("github.sign_in_finished")[0]
	if e["exitCode"] != float64(0) || e["signedIn"] != true {
		t.Fatalf("finished %v", e)
	}
	a.call("github.status", nil, &st)
	if !st.LoggedIn || st.Login != "kyle" {
		t.Fatalf("after: %+v", st)
	}
	if len(a.eventsOf("github.sign_in_started")) != 1 {
		t.Fatal("no github.sign_in_started")
	}

	pathFor(t) // git alone: no gh
	if err := a.callErr("github.signIn", nil); err == nil || !strings.Contains(err.Error(), "cli.github.com") {
		t.Fatalf("without gh: %v", err)
	}
}

// F4: github.status is kept a few seconds (the picker and the pages ask on every open); refresh asks gh again.
func TestGitHubStatusIsKeptBriefly(t *testing.T) {
	ghOnPath(t, ghFixture{Login: "kyle"})
	a := open(t, "")
	var st core.GitHubStatus
	a.call("github.status", nil, &st)
	if st.Login != "kyle" {
		t.Fatalf("%+v", st)
	}
	// gh is signed out behind Drydock's back
	b, _ := json.Marshal(ghFixture{})
	if err := os.WriteFile(os.Getenv(ghStandInFixture), b, 0o644); err != nil {
		t.Fatal(err)
	}
	a.call("github.status", nil, &st)
	if !st.LoggedIn {
		t.Fatalf("the kept answer was not used: %+v", st)
	}
	a.call("github.status", map[string]bool{"refresh": true}, &st)
	if st.LoggedIn {
		t.Fatalf("refresh did not ask gh again: %+v", st)
	}
}

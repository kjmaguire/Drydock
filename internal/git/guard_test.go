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

	"github.com/kjmaguire/drydock/internal/proc"
)

// These tests plant what an agent in a task's worktree could plant in the shared .git directory (docs/security.md s3, threat
// T3) and prove that Drydock's own git calls do not run it. Each planted program writes a marker file; a plain git call made
// the way a person would make it shows the plant is live, so the absence of the marker after Drydock's calls means something.

// rawGit runs git the way a person (or an agent) would: no hardening, the repository's configuration as it is.
func rawGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitEnv(os.Environ()), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// markerScript is a POSIX shell command that writes path (git runs hooks, filters and fsmonitor through sh, bundled with Git
// for Windows too); it copies stdin to stdout so it also works as a clean filter.
func markerScript(path string) string {
	return "echo ran > '" + filepath.ToSlash(path) + "'; cat"
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func hook(t *testing.T, dir, name, marker string) {
	t.Helper()
	write(t, dir, name, "#!/bin/sh\necho ran > '"+filepath.ToSlash(marker)+"'\n")
	if err := os.Chmod(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestDrydockGitCallsNeverRunHooksOrFsmonitor(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	marks := t.TempDir()
	hooks := filepath.Join(r.Root, ".git", "hooks")
	for _, h := range []string{"pre-commit", "post-commit", "post-checkout", "post-merge", "reference-transaction", "pre-auto-gc"} {
		hook(t, hooks, h, filepath.Join(marks, h))
	}
	// the repository's own setting points hooks elsewhere too, with the same hooks there
	custom := t.TempDir()
	hook(t, custom, "post-commit", filepath.Join(marks, "custom-post-commit"))
	rawGit(t, r.Root, "config", "core.hooksPath", filepath.ToSlash(custom))
	rawGit(t, r.Root, "config", "core.fsmonitor", markerScript(filepath.Join(marks, "fsmonitor")))
	r, err := Open(ctx, r.Root) // the user's own configuration, trusted as it is when the project is opened
	if err != nil {
		t.Fatal(err)
	}

	w, err := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_h", Title: "hooks", Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	write(t, w.Path, "src/new.go", "package main\n")
	if _, err := r.SafePoint(ctx, w, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Status(ctx, w); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Diff(ctx, w, "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Commit(ctx, w, "task"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Merge(ctx, w, "main", MergeCommit, "merge"); err != nil {
		t.Fatal(err)
	}
	ran, _ := os.ReadDir(marks)
	if len(ran) > 0 {
		var names []string
		for _, e := range ran {
			names = append(names, e.Name())
		}
		t.Fatalf("Drydock's git calls ran the repository's programs: %v", names)
	}

	// The plants are live: a person's own commit in the main tree runs the configured hook.
	write(t, r.Root, "x.txt", "x\n")
	rawGit(t, r.Root, "add", "x.txt")
	rawGit(t, r.Root, "commit", "-q", "-m", "by hand")
	if !exists(filepath.Join(marks, "custom-post-commit")) {
		t.Fatal("the planted hook did not run for a plain git commit either, so this test proves nothing")
	}
}

func TestAConfigChangeUnderATaskIsRefusedUntilAPersonTrustsIt(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	w, err := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_f", Title: "filter", Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	marks := t.TempDir()
	marker := filepath.Join(marks, "filter")
	// What an agent can do from its worktree: write the shared config and an attributes file in its own tree.
	rawGit(t, w.Path, "config", "filter.evil.clean", markerScript(marker))
	write(t, w.Path, ".gitattributes", "* filter=evil\n")
	write(t, w.Path, "src/new.go", "package main\n")

	head, err := r.Head(ctx, w) // reading refs runs nothing, so it still works
	if err != nil || head == "" {
		t.Fatalf("Head under an untrusted configuration: %q %v", head, err)
	}
	var ce *ConfigChangedError
	for name, call := range map[string]func() error{
		"Commit":    func() error { _, err := r.Commit(ctx, w, "x"); return err },
		"SafePoint": func() error { _, err := r.SafePoint(ctx, w, 1); return err },
		"Diff":      func() error { _, err := r.Diff(ctx, w, "main"); return err },
		"Status":    func() error { _, err := r.Status(ctx, w); return err },
	} {
		if err := call(); !errors.As(err, &ce) {
			t.Fatalf("%s with a planted filter driver: %v, want ConfigChangedError", name, err)
		}
	}
	if !slices.Contains(ce.Changed, "filter.evil.clean") {
		t.Fatalf("the refusal names %v, want filter.evil.clean", ce.Changed)
	}
	if strings.Contains(ce.Error(), "echo") {
		t.Fatalf("the refusal shows a configured value: %s", ce.Error())
	}
	if exists(marker) {
		t.Fatal("the planted filter ran in a Drydock git call")
	}
	gate, _, payload := ce.Gate()
	if gate != GateRepoConfigChanged || payload["fingerprint"] != ce.Fingerprint {
		t.Fatalf("gate %q payload %v", gate, payload)
	}

	// A person approves exactly the state they were shown; a further change made since needs its own review.
	rawGit(t, w.Path, "config", "core.sshCommand", "evil")
	var again *ConfigChangedError
	if err := r.Trust(ctx, ce.Fingerprint); !errors.As(err, &again) || !slices.Contains(again.Changed, "core.sshcommand") {
		t.Fatalf("trusting a stale fingerprint: %v", err)
	}
	if err := r.Trust(ctx, again.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Commit(ctx, w, "x"); err != nil {
		t.Fatalf("Commit after the person trusted the change: %v", err)
	}
	if changed, _, err := r.ConfigStatus(ctx); err != nil || len(changed) != 0 {
		t.Fatalf("status after trust: %v %v", changed, err)
	}
	// and the plant was live all along: trusted, git runs it (that is the person's call)
	if !exists(marker) {
		t.Fatal("the planted filter never ran even when trusted, so this test proves nothing")
	}
}

func TestAPlantedHookIsNamedAndARestartKeepsTheTrustedState(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	trusted := r.TrustedState()
	w, err := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_k", Title: "hook", Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	hook(t, filepath.Join(r.Root, ".git", "hooks"), "post-commit", filepath.Join(t.TempDir(), "m"))

	// a restart: the repository is opened again (and would trust the planted hook), then given the state recorded before
	r2, err := Open(ctx, r.Root)
	if err != nil {
		t.Fatal(err)
	}
	r2.SetTrustedState(trusted)
	var ce *ConfigChangedError
	if _, err := r2.Commit(ctx, w, "x"); !errors.As(err, &ce) || !slices.Contains(ce.Changed, "hooks/post-commit") {
		t.Fatalf("Commit after a restart with a hook planted before it: %v", err)
	}
}

func TestDrydocksOwnBranchBookkeepingIsNotAChange(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	remote := t.TempDir()
	rawGit(t, remote, "init", "-q", "--bare")
	rawGit(t, r.Root, "remote", "add", "origin", filepath.ToSlash(remote))
	r, err := Open(ctx, r.Root)
	if err != nil {
		t.Fatal(err)
	}
	w, err := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_p", Title: "push", Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	write(t, w.Path, "a.txt", "a\n")
	if _, err := r.Commit(ctx, w, "a"); err != nil {
		t.Fatal(err)
	}
	if err := r.Push(ctx, w, "origin"); err != nil { // writes branch.<task>.remote and .merge
		t.Fatal(err)
	}
	if out := rawGit(t, r.Root, "config", "--get", "branch."+w.Branch+".remote"); strings.TrimSpace(out) != "origin" {
		t.Fatalf("push did not record the upstream: %q", out)
	}
	write(t, w.Path, "b.txt", "b\n")
	if _, err := r.Commit(ctx, w, "b"); err != nil {
		t.Fatalf("a commit after Drydock's own push: %v", err)
	}
	if err := r.DeleteWorktree(ctx, w, true); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteBranch(ctx, w.Branch, true); err != nil {
		t.Fatal(err)
	}
	if changed, _, err := r.ConfigStatus(ctx); err != nil || len(changed) != 0 {
		t.Fatalf("Drydock's own bookkeeping counted as a change: %v %v", changed, err)
	}
	// but a merge option on a branch changes how a merge runs, so it is a change
	rawGit(t, r.Root, "config", "branch.main.mergeOptions", "--no-ff")
	if changed, _, _ := r.ConfigStatus(ctx); !slices.Contains(changed, "branch.main.mergeoptions") {
		t.Fatalf("branch mergeOptions not seen: %v", changed)
	}
}

func TestInheritedGitVariablesDoNotRedirectDrydocksGit(t *testing.T) {
	ctx := context.Background()
	r, _ := newRepo(t)
	other, _ := newRepo(t)
	marks := t.TempDir()
	ext := filepath.Join(marks, "ext")
	t.Setenv("GIT_DIR", filepath.Join(other.Root, ".git"))
	t.Setenv("GIT_WORK_TREE", other.Root)
	t.Setenv("GIT_EXTERNAL_DIFF", "sh -c '"+markerScript(ext)+"'")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
	t.Setenv("GIT_CONFIG_VALUE_0", markerScript(filepath.Join(marks, "fsm")))
	write(t, r.Root, "README.md", "# changed\n")
	branch, err := r.CurrentBranch(ctx)
	if err != nil || branch != "main" {
		t.Fatalf("CurrentBranch: %q %v", branch, err)
	}
	st, err := r.Status(ctx, Worktree{Path: r.Root})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Modified) == 0 {
		t.Fatal("Status read another repository (the inherited GIT_DIR)")
	}
	if ents, _ := os.ReadDir(marks); len(ents) > 0 {
		t.Fatalf("an inherited variable ran a program: %v", ents)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if got := gitEnv([]string{"GIT_DIR=x", "git_index_file=y", "GIT_SSH_COMMAND=ssh -i k", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_GLOBAL=/home/u/.gitconfig-ci", "PATH=/bin"}); !slices.Equal(got, []string{"GIT_SSH_COMMAND=ssh -i k", "GIT_CONFIG_GLOBAL=/home/u/.gitconfig-ci", "PATH=/bin"}) {
		t.Fatalf("gitEnv kept %v", got)
	}
}

// A git call that stays on this machine does not get the tokens the app was started with; push, the one call that reaches a
// remote, keeps the environment because the person's login is read from it (audit 2026-10-16, L8).
func TestLocalGitCallsDoNotInheritTheAppsTokens(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_not_for_git_status")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws_not_for_git_status")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-not-for-git-status")
	t.Setenv("GIT_SSH_COMMAND", "ssh -i /home/u/key")
	t.Setenv("GIT_CONFIG_GLOBAL", "/home/u/.gitconfig-ci")
	t.Setenv("GIT_DIR", "/elsewhere/.git")
	t.Setenv("PATH", os.Getenv("PATH"))
	r := &Repo{AuthorName: "t", AuthorEmail: "t@t"}
	has := func(env []string, name string) bool {
		for _, kv := range env {
			// names are compared as Windows does: the system spells the search path Path, not PATH
			if k, _, _ := strings.Cut(kv, "="); strings.EqualFold(k, name) {
				return true
			}
		}
		return false
	}
	local := r.env(true)
	for _, bad := range []string{"GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY", "ANTHROPIC_API_KEY", "GIT_DIR"} {
		if has(local, bad) {
			t.Errorf("%s reached a local git call", bad)
		}
	}
	for _, keep := range []string{"PATH", "GIT_CONFIG_GLOBAL", "GIT_SSH_COMMAND", "GIT_TERMINAL_PROMPT", "GIT_AUTHOR_NAME", proc.NoCwdSearchVar} {
		if !has(local, keep) {
			t.Errorf("%s was removed from a local git call", keep)
		}
	}
	remote := r.env(false)
	for _, keep := range []string{"GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY", "GIT_SSH_COMMAND", "PATH"} {
		if !has(remote, keep) {
			t.Errorf("%s was removed from a call that talks to a remote", keep)
		}
	}
	if has(remote, "GIT_DIR") {
		t.Error("GIT_DIR must never reach git")
	}
	for args, want := range map[string]bool{"push --set-upstream origin x": true, "fetch origin": true, "pull": true, "clone u d": true, "ls-remote origin": true,
		"-c a=b -C /x push origin": true, "remote update": true, "remote get-url origin": false, "status --porcelain": false, "-c core.hooksPath=/x diff": false,
		"worktree add x": false, "": false, "merge --no-ff b": false} {
		if got := talksToARemote(strings.Fields(args)); got != want {
			t.Errorf("talksToARemote(%q) = %v, want %v", args, got, want)
		}
	}
}

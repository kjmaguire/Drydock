package host

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRealGitHubMergeThroughGh runs the merge gate against real GitHub through the real gh and git. It is skipped unless
// DRYDOCK_REAL_GITHUB_REPO=owner/name names a THROWAWAY repository (the test merges a pull request into it);
// DRYDOCK_REAL_GITHUB_BASE picks the base branch (default: the repository's default branch). It clones the repository,
// pushes a branch (named like a task branch, drydock/...) with a one-line change, opens a pull request with Drydock's
// client, posts a commit status (context drydock-proof, state success) through gh api so that CI exists, and then makes the
// calls the pull-request flow makes: the required checks of the base, the CI wait, the merge state, the merge guarded by
// the pushed (reviewed) commit, and the branch deletion. A second pull request with no status shows that the CI wait does
// not pass a commit nothing reported on (the CI gate's default, "required", then asks the human). Pull requests left open
// are closed and branches deleted on the way out, also when the test fails.
func TestRealGitHubMergeThroughGh(t *testing.T) {
	target := os.Getenv("DRYDOCK_REAL_GITHUB_REPO")
	if target == "" {
		t.Skip("set DRYDOCK_REAL_GITHUB_REPO=owner/name (a throwaway repository: a pull request is merged into it) to run the merge gate against real GitHub")
	}
	owner, name, ok := strings.Cut(target, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		t.Fatalf("DRYDOCK_REAL_GITHUB_REPO must be owner/name, not %q", target)
	}
	for _, bin := range []string{"gh", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s is not installed: %v", bin, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	g := GitHub{API: GhCLI{}, Owner: owner, Repo: name}
	base := os.Getenv("DRYDOCK_REAL_GITHUB_BASE")
	if base == "" {
		var repo struct {
			DefaultBranch string `json:"default_branch"`
		}
		if _, err := g.API.Do(ctx, "GET", g.p(""), nil, &repo, ""); err != nil || repo.DefaultBranch == "" {
			t.Fatalf("read %s through gh: %v", target, err)
		}
		base = repo.DefaultBranch
	}
	run := func(t *testing.T, dir string, bin string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GH_PROMPT_DISABLED=1", "NO_COLOR=1",
			"GIT_AUTHOR_NAME=Drydock Test", "GIT_AUTHOR_EMAIL=drydock-test@localhost", "GIT_COMMITTER_NAME=Drydock Test", "GIT_COMMITTER_EMAIL=drydock-test@localhost")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", bin, args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	clone := filepath.Join(t.TempDir(), "clone")
	run(t, "", "gh", "repo", "clone", target, clone, "--", "--depth=1", "--branch", base)
	suffix := fmt.Sprint(time.Now().UnixNano())

	// openPR pushes a branch with a one-line change and opens a pull request for it; cleanup closes it if it is still open
	// and deletes the branch
	openPR := func(t *testing.T, label string) (PR, string, string) {
		t.Helper()
		branch := "drydock/proof-" + label + "-" + suffix
		run(t, clone, "git", "checkout", "-q", "-B", branch, "origin/"+base)
		file := filepath.Join(clone, "drydock-proof", label+"-"+suffix+".txt")
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("Drydock merge gate proof "+label+" "+suffix+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(t, clone, "git", "add", "-A")
		run(t, clone, "git", "commit", "-q", "-m", "Drydock merge gate proof ("+label+")")
		sha := run(t, clone, "git", "rev-parse", "HEAD")
		run(t, clone, "git", "push", "-q", "origin", branch)
		t.Cleanup(func() { _ = g.DeleteBranch(context.Background(), branch) }) // already gone after a merge: ignored
		pr, err := g.OpenPR(ctx, PRRequest{Title: "Drydock merge gate proof (" + label + ")", Body: "Opened by Drydock's TestRealGitHubMergeThroughGh. Safe to close.", Head: branch, Base: base})
		if err != nil {
			t.Fatalf("open the pull request: %v", err)
		}
		t.Cleanup(func() {
			if p, _, err := g.GetPR(context.Background(), pr.Number, ""); err == nil && p.State == "open" {
				_ = g.ClosePR(context.Background(), pr.Number)
			}
		})
		if pr.Head.SHA != sha || pr.Base.Ref != base {
			t.Fatalf("pull request %+v, pushed %s onto %s", pr, sha, base)
		}
		t.Logf("pull request #%d %s (%s)", pr.Number, pr.URL, branch)
		return pr, branch, sha
	}

	t.Run("a commit status counts as CI and the pull request merges through the gate", func(t *testing.T) {
		pr, branch, sha := openPR(t, "merge")
		if _, err := g.API.Do(ctx, "POST", g.p("/statuses/%s", sha), map[string]string{"state": "success", "context": "drydock-proof",
			"description": "Posted by Drydock's real GitHub test"}, nil, ""); err != nil {
			t.Fatalf("post the commit status: %v", err)
		}
		required, perr := g.RequiredChecks(ctx, base)
		t.Logf("required checks of %s: %v (could not read: %v)", base, required, perr)
		st, err := g.WaitCI(ctx, sha, CIWait{Poll: 3 * time.Second, Grace: 45 * time.Second, MaxWait: 5 * time.Minute, Required: required})
		if err != nil {
			t.Fatalf("CI wait: %v (%+v)", err, st)
		}
		found := false
		for _, r := range st.Runs {
			found = found || r.Kind == KindStatus && r.Name == "drydock-proof" && r.Conclusion == "success"
		}
		if !st.Passed || len(st.Missing) > 0 || !found {
			t.Fatalf("the CI wait must pass on the posted status: %+v", st)
		}
		got, err := g.MergeState(ctx, pr.Number, MergeWait{Tries: 10, Delay: 3 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("merge state: %s (mergeable_state %q)", got.MergeStatus(), got.MergeableState)
		switch got.MergeStatus() {
		case "clean", "unstable", "has_hooks":
		default:
			t.Fatalf("GitHub reports the merge state %q: the test repository must let this pull request merge without a review", got.MergeStatus())
		}
		// the reviewed-head guard as the merge step applies it: the head is still the pushed commit, and GitHub is told so
		if got.Head.SHA != sha {
			t.Fatalf("the head moved to %s after the push of %s", got.Head.SHA, sha)
		}
		merged, err := g.Merge(ctx, pr.Number, MergeSquash, sha, "")
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if err := g.DeleteBranch(ctx, branch); err != nil {
			t.Fatalf("delete the branch: %v", err)
		}
		after, _, err := g.GetPR(ctx, pr.Number, "")
		if err != nil || !after.Merged {
			t.Fatalf("the pull request must be merged: %+v %v", after, err)
		}
		if left := run(t, clone, "git", "ls-remote", "--heads", "origin", branch); left != "" {
			t.Fatalf("the branch is still on GitHub after the merge: %s", left)
		}
		t.Logf("merged as %s and the branch is deleted", merged)
	})

	t.Run("without CI the wait does not pass", func(t *testing.T) {
		pr, _, sha := openPR(t, "noci")
		st, err := g.WaitCI(ctx, sha, CIWait{Poll: 3 * time.Second, Grace: 20 * time.Second, MaxWait: 2 * time.Minute})
		if err != nil {
			t.Fatalf("CI wait: %v", err)
		}
		if len(st.Runs) > 0 {
			t.Skipf("the repository's own CI reported %d result(s) on the commit; this part needs a repository without CI", len(st.Runs))
		}
		if st.Passed || len(st.Failed) > 0 || st.Pending {
			t.Fatalf("nothing reported, so the wait must not pass: %+v", st)
		}
		t.Logf("pull request #%d: nothing reported within the grace period; the CI gate (required) asks the human instead of passing", pr.Number)
	})
}

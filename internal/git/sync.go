package git

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Keeping a project's folder in step with its remote (decision P143): Fetch only updates the remote-tracking branches
// (refs/remotes/<remote>/*), never a local branch, a worktree or a file; FastForward moves the checked-out default branch of the
// main working tree forward to the remote's, and only when that changes nothing but the branch (the tree is clean, the branch is
// the one checked out, and it has no commits of its own). Nothing here merges, rebases, resets, forces or pushes, and the task
// worktrees and their drydock/* branches are never touched.

// SyncOutcome is what FastForward did, or why it did nothing.
type SyncOutcome string

const (
	// SyncUpdated: the branch was fast-forwarded to the remote's.
	SyncUpdated SyncOutcome = "updated"
	// SyncUpToDate: the remote has nothing the branch lacks.
	SyncUpToDate SyncOutcome = "up_to_date"
	// SyncAhead: the branch has commits the remote does not (and nothing new came): nothing to bring in, nothing is pushed.
	SyncAhead SyncOutcome = "ahead"
	// SyncDirty: the main working tree has local changes, so its files were left alone.
	SyncDirty SyncOutcome = "local_changes"
	// SyncDiverged: both sides have commits of their own; only a merge or a rebase could join them, and Drydock makes neither.
	SyncDiverged SyncOutcome = "diverged"
	// SyncOtherBranch: another branch is checked out in the main working tree.
	SyncOtherBranch SyncOutcome = "other_branch"
	// SyncNoRemoteBranch: the remote has no branch of that name.
	SyncNoRemoteBranch SyncOutcome = "no_remote_branch"
)

// RemoteState compares a branch of the main working tree with its copy on a remote, as of the last fetch.
type RemoteState struct {
	Remote string `json:"remote"`
	Branch string `json:"branch"`
	// Current is the branch checked out in the main working tree ("HEAD" when detached).
	Current string `json:"current"`
	// Tracked is false when refs/remotes/<remote>/<branch> does not exist (never fetched, or the remote has no such branch).
	Tracked bool `json:"tracked"`
	Ahead   int  `json:"ahead"`
	Behind  int  `json:"behind"`
}

// remoteNameRe is the remote names Drydock passes to git: a name, never an option or a URL.
var remoteNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func checkRemote(remote string) error {
	if !remoteNameRe.MatchString(remote) {
		return fmt.Errorf("git: %q is not a remote name", remote)
	}
	return nil
}

func checkSyncArgs(remote, branch string) error {
	if err := checkRemote(remote); err != nil {
		return err
	}
	if branch == "" || strings.HasPrefix(branch, "-") || strings.ContainsAny(branch, " ~^:?*[\\") || strings.Contains(branch, "..") {
		return fmt.Errorf("git: %q is not a branch name", branch)
	}
	return nil
}

// Fetch brings the remote's branches into refs/remotes/<remote>/* (the person's own login reaches the remote: a call that
// talks to a remote keeps the environment that says how). Local branches, worktrees and files are not changed: the refspec is
// given on the command line, so a configured one that writes local branches is not used.
func (r *Repo) Fetch(ctx context.Context, remote string) error {
	if err := checkRemote(remote); err != nil {
		return err
	}
	if _, err := r.RemoteURL(ctx, remote); err != nil {
		return err
	}
	_, err := r.git(ctx, r.Root, nil, "fetch", "--quiet", "--no-recurse-submodules", "--no-write-fetch-head", remote, "+refs/heads/*:refs/remotes/"+remote+"/*")
	return err
}

// RemoteState reads where branch stands against remote's copy of it, from the refs the last fetch left (no network).
func (r *Repo) RemoteState(ctx context.Context, remote, branch string) (RemoteState, error) {
	st := RemoteState{Remote: remote, Branch: branch}
	if err := checkSyncArgs(remote, branch); err != nil {
		return st, err
	}
	cur, err := r.CurrentBranch(ctx)
	if err != nil {
		return st, err
	}
	st.Current = cur
	theirs := "refs/remotes/" + remote + "/" + branch
	if _, err := r.git(ctx, r.Root, nil, "rev-parse", "--verify", "--quiet", theirs+"^{commit}"); err != nil {
		return st, nil // not tracked: nothing to compare yet
	}
	st.Tracked = true
	out, err := r.git(ctx, r.Root, nil, "rev-list", "--left-right", "--count", "refs/heads/"+branch+"..."+theirs)
	if err != nil {
		return st, err
	}
	if _, err := fmt.Sscan(strings.TrimSpace(out), &st.Ahead, &st.Behind); err != nil {
		return st, fmt.Errorf("git: reading %q: %w", strings.TrimSpace(out), err)
	}
	return st, nil
}

// FastForward moves branch, checked out in the main working tree, to remote's copy of it as the last fetch left it, when that
// is a fast-forward and the tree has no local changes; otherwise it changes nothing and says why. It holds the lock merges into
// the main working tree take, so a task's merge never runs at the same moment.
func (r *Repo) FastForward(ctx context.Context, remote, branch string) (SyncOutcome, RemoteState, error) {
	defer r.lockMain()()
	st, err := r.RemoteState(ctx, remote, branch)
	if err != nil {
		return "", st, err
	}
	switch {
	case !st.Tracked:
		return SyncNoRemoteBranch, st, nil
	case st.Behind == 0 && st.Ahead > 0:
		return SyncAhead, st, nil
	case st.Behind == 0:
		return SyncUpToDate, st, nil
	case st.Current != branch:
		return SyncOtherBranch, st, nil
	case st.Ahead > 0:
		return SyncDiverged, st, nil
	}
	ws, err := r.Status(ctx, Worktree{Path: r.Root})
	if err != nil {
		return "", st, err
	}
	if ws.Dirty() {
		return SyncDirty, st, nil
	}
	if _, err := r.git(ctx, r.Root, nil, "merge", "--ff-only", "--quiet", "refs/remotes/"+remote+"/"+branch); err != nil {
		// --ff-only changes nothing when it cannot fast-forward (an untracked file in the way, for one)
		var ge *gitError
		if errors.As(err, &ge) && strings.Contains(ge.stderr, "untracked working tree files would be overwritten") {
			return SyncDirty, st, nil
		}
		return "", st, err
	}
	after, err := r.RemoteState(ctx, remote, branch)
	return SyncUpdated, after, err
}

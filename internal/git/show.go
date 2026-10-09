package git

import (
	"context"
	"strings"
)

// Reading file versions for the diff viewer. Every call runs in the main working tree: worktrees share its object database,
// so a task's commits stay readable after its worktree is gone (until git collects an unreachable commit).

// HasCommit reports whether rev names a commit that is in the object database.
func (r *Repo) HasCommit(ctx context.Context, rev string) bool {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return false
	}
	out, err := r.git(ctx, r.Root, nil, "cat-file", "-t", rev+"^{commit}")
	return err == nil && strings.TrimSpace(out) == "commit"
}

// ShowFile returns path's content at commit rev. ok is false when the path does not exist in that commit.
func (r *Repo) ShowFile(ctx context.Context, rev, path string) (content string, ok bool, err error) {
	if !r.HasCommit(ctx, rev) {
		return "", false, ErrUnknownRevision
	}
	spec := rev + ":" + strings.TrimPrefix(path, "./")
	if _, err := r.git(ctx, r.Root, nil, "cat-file", "-e", spec); err != nil {
		return "", false, nil
	}
	out, err := r.git(ctx, r.Root, nil, "cat-file", "blob", spec)
	if err != nil {
		return "", false, err
	}
	return out, true, nil
}

// MergeBaseOf returns the merge base of two revisions.
func (r *Repo) MergeBaseOf(ctx context.Context, a, b string) (string, error) {
	out, err := r.git(ctx, r.Root, nil, "merge-base", a, b)
	return strings.TrimSpace(out), err
}

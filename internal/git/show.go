package git

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrBlobTooLarge is returned for a file in a commit that is larger than the caller allows; the file is not read.
var ErrBlobTooLarge = errors.New("git: the file is larger than the limit")

// DefaultShowMax is the largest file ShowFile reads. The files read through it are a repository's configuration and the
// versions the diff viewer shows; a file of hundreds of megabytes is not one of those, and used to be read into memory whole.
const DefaultShowMax = 32 << 20

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

// ShowFile returns path's content at commit rev (a file of at most DefaultShowMax bytes). ok is false when the path does not
// exist in that commit.
func (r *Repo) ShowFile(ctx context.Context, rev, path string) (content string, ok bool, err error) {
	return r.ShowFileMax(ctx, rev, path, DefaultShowMax)
}

// BlobSize returns the size in bytes of path at commit rev without reading it: git is asked for the size of the object
// (audit 2026-10-16, L22). ok is false when the path does not exist in that commit.
func (r *Repo) BlobSize(ctx context.Context, rev, path string) (size int64, ok bool, err error) {
	if !r.HasCommit(ctx, rev) {
		return 0, false, ErrUnknownRevision
	}
	spec := rev + ":" + strings.TrimPrefix(path, "./")
	if _, err := r.git(ctx, r.Root, nil, "cat-file", "-e", spec); err != nil {
		return 0, false, nil
	}
	out, err := r.git(ctx, r.Root, nil, "cat-file", "-s", spec)
	if err != nil {
		return 0, false, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil || n < 0 {
		return 0, false, fmt.Errorf("git: the size of %s is %q", spec, strings.TrimSpace(out))
	}
	return n, true, nil
}

// ShowFileMax is ShowFile for a file of at most limit bytes: the size is asked first, and a larger file is ErrBlobTooLarge, never
// read into memory.
func (r *Repo) ShowFileMax(ctx context.Context, rev, path string, limit int64) (content string, ok bool, err error) {
	size, ok, err := r.BlobSize(ctx, rev, path)
	if err != nil || !ok {
		return "", false, err
	}
	if size > limit {
		return "", true, fmt.Errorf("%w (%d bytes, at most %d)", ErrBlobTooLarge, size, limit)
	}
	spec := rev + ":" + strings.TrimPrefix(path, "./")
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

// ExistingPaths returns which of paths are in commit rev (files, or folders named as such), with one git call (git ls-tree): a caller about to read a few
// files that may not be there asks once instead of four calls per missing file (each git start is slow on Windows).
func (r *Repo) ExistingPaths(ctx context.Context, rev string, paths ...string) (map[string]bool, error) {
	have := map[string]bool{}
	if len(paths) == 0 {
		return have, nil
	}
	args := append([]string{"ls-tree", "-z", "--full-tree", "--name-only", rev, "--"}, paths...)
	out, err := r.git(ctx, r.Root, nil, args...)
	if err != nil {
		return nil, err
	}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			have[p] = true
		}
	}
	return have, nil
}

package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Pinning a call to its repository (audit 2026-10-16, C1).
//
// Git decides which repository a call belongs to from the folder it starts in: a `.git` file or folder there names the
// configuration, index and refs the call uses, and a repository nested inside a tree is a repository of its own, with its own
// configuration. In a task's worktree both are the agent's to write. A `.git` file that says `gitdir: ./.x` points git at a
// configuration the agent wrote, whose `filter.x.clean` git then runs on the host, outside any sandbox and approval; a
// repository the agent planted inside its tree does the same when git asks whether it is modified. The guard (guard.go) watches
// the common git directory, which neither of these touches. Two rules close that:
//
//   - A call in a task's worktree names the repository itself: GIT_DIR is the private directory the repository registered for
//     that worktree (<common>/worktrees/<name>, written by git and out of the worktree's reach) and GIT_WORK_TREE is the
//     worktree. The `.git` file in the worktree is never read. A folder that is neither the main working tree nor a registered
//     worktree is refused.
//   - A nested repository (a submodule or an embedded repository) is opaque: Drydock does not stage what is inside it, does
//     not ask whether it is modified, and removes no worktree that holds one without a person's force. (hardening and addAll.)

// ErrNotAWorktree is returned for a folder that is neither the repository's main working tree nor a worktree it registered.
var ErrNotAWorktree = errors.New("git: not a registered worktree of this repository")

// worktreeEntry is a linked worktree the repository registered: its private git directory and its folder.
type worktreeEntry struct {
	admin string // <common>/worktrees/<name>
	path  string // the worktree's folder, as git recorded it
}

// lexical is p in the form two paths are compared in when nothing needs resolving: cleaned, and lower-cased on Windows (the
// file system there ignores case).
func lexical(p string) string {
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

// canonical is lexical after symlinks and junctions are resolved (git records the real path of a worktree; Drydock's own may
// reach it through a link).
func canonical(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return lexical(p)
}

// within reports whether child is parent or a folder inside it (both normalised the same way).
func within(child, parent string) bool {
	if child == parent {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(parent, sep) {
		parent += sep
	}
	return strings.HasPrefix(child, parent)
}

// registeredWorktrees reads the repository's own record of its linked worktrees: <common>/worktrees/<name>/gitdir names the
// `.git` file of each worktree (absolute, or relative to that directory in newer git).
func (r *Repo) registeredWorktrees() []worktreeEntry {
	dir := filepath.Join(r.guard.commonDir, "worktrees")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []worktreeEntry
	for _, e := range ents {
		admin := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(filepath.Join(admin, "gitdir"))
		if err != nil {
			continue
		}
		target := strings.TrimSpace(string(b))
		if target == "" {
			continue
		}
		if r.runner != nil {
			target = r.runner.HostPath(target)
		}
		target = filepath.FromSlash(target)
		if !filepath.IsAbs(target) && !strings.HasPrefix(target, `\\`) {
			target = filepath.Join(admin, target)
		}
		out = append(out, worktreeEntry{admin: admin, path: filepath.Dir(filepath.Clean(target))})
	}
	return out
}

// worktreeFor returns the registered linked worktree that holds dir (dir itself, or a folder inside it; the deepest one when
// worktrees nest). The main working tree is not a linked worktree here, even when the repository itself is a linked worktree
// of another one.
func (r *Repo) worktreeFor(dir string) (worktreeEntry, bool) {
	if r.guard == nil {
		return worktreeEntry{}, false
	}
	entries := r.registeredWorktrees()
	if len(entries) == 0 {
		return worktreeEntry{}, false
	}
	// compare the paths as written first; only when none matches, resolve links (a resolve costs a system call per folder)
	if e, ok := matchWorktree(entries, r.Root, dir, lexical); ok {
		return e, true
	}
	return matchWorktree(entries, r.Root, dir, canonical)
}

func matchWorktree(entries []worktreeEntry, root, dir string, norm func(string) string) (worktreeEntry, bool) {
	want, main := norm(dir), norm(root)
	var best worktreeEntry
	bestLen := -1
	for _, e := range entries {
		p := norm(e.path)
		if p == main || !within(want, p) {
			continue
		}
		if len(p) > bestLen {
			best, bestLen = e, len(p)
		}
	}
	return best, bestLen >= 0
}

// pinFor is the environment that fixes a git call in dir to its repository: GIT_DIR and GIT_WORK_TREE of the linked worktree
// that holds dir, nothing for the main working tree (the person's own folder, which has its own .git), and ErrNotAWorktree for
// any other folder (an unregistered one could be a `.git` the agent wrote, or a worktree git was told to forget).
func (r *Repo) pinFor(dir string) ([]string, error) {
	if r.guard == nil {
		return nil, nil
	}
	if e, ok := r.worktreeFor(dir); ok {
		return []string{"GIT_DIR=" + e.admin, "GIT_WORK_TREE=" + e.path}, nil
	}
	if within(lexical(dir), lexical(r.Root)) || within(canonical(dir), canonical(r.Root)) {
		return nil, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrNotAWorktree, dir)
}

// nestedRepos lists the repositories nested in the checkout at dir, relative to it with forward slashes: the gitlinks in the
// index (submodules, and embedded repositories an earlier `git add` recorded) and the embedded repositories no ignore rule
// hides, which git lists as one directory. It asks git, so ignore rules and sparse checkouts apply as they do to `git add`.
func (r *Repo) nestedRepos(ctx context.Context, dir string, env []string) ([]string, error) {
	// -t puts a tag first (H/S/M for an index entry, ? for an untracked path), so no file name can pass for the other kind
	out, err := r.git(ctx, dir, env, "ls-files", "-z", "-t", "-s", "-o", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var nested []string
	for _, rec := range strings.Split(out, "\x00") {
		if len(rec) < 3 {
			continue
		}
		tag, body := rec[0], rec[2:]
		if tag == '?' {
			if strings.HasSuffix(body, "/") { // an untracked file never ends in a slash: this is a nested repository
				nested = append(nested, strings.TrimSuffix(body, "/"))
			}
			continue
		}
		meta, path, ok := strings.Cut(body, "\t")
		if ok && strings.HasPrefix(meta, "160000 ") {
			nested = append(nested, path)
		}
	}
	return nested, nil
}

// addAll stages everything in the checkout at dir (git add -A) except the nested repositories. Staging a path asks git whether
// the nested repository is modified, and that runs the repository's own configuration (a clean filter, for one), which is the
// agent's to write; a pathspec that leaves the repository out keeps git from entering it.
func (r *Repo) addAll(ctx context.Context, dir string, env []string) error {
	nested, err := r.nestedRepos(ctx, dir, env)
	if err != nil {
		return err
	}
	args := []string{"add", "-A", "--", "."}
	for _, n := range nested {
		args = append(args, ":(exclude,literal)"+n)
	}
	_, err = r.git(ctx, dir, env, args...)
	return err
}

// linuxPather is implemented by a Runner that can name a folder of this machine the way its git sees it (internal/wsl.GitRunner).
type linuxPather interface {
	LinuxPath(p string) (string, error)
}

// restoreGitFile makes the `.git` file of a registered worktree say what git wrote when it made the worktree: the path of the
// worktree's private git directory. Git checks that file when it removes a worktree and runs a status from it, and an agent can
// have rewritten it (or replaced it with a folder or a link) to send git elsewhere. Whatever stands in its place is removed.
func (r *Repo) restoreGitFile(w Worktree) error {
	e, ok := r.worktreeFor(w.Path)
	if !ok || lexical(e.path) != lexical(w.Path) && canonical(e.path) != canonical(w.Path) {
		return fmt.Errorf("%w: %s", ErrNotAWorktree, w.Path)
	}
	admin := filepath.ToSlash(e.admin)
	if r.runner != nil {
		lp, ok := r.runner.(linuxPather)
		if !ok {
			return nil // git in the distro reports what it finds
		}
		var err error
		if admin, err = lp.LinuxPath(e.admin); err != nil {
			return err
		}
	}
	want := "gitdir: " + admin + "\n"
	file := filepath.Join(e.path, ".git")
	if st, err := os.Lstat(file); err == nil && st.Mode().IsRegular() {
		if b, err := os.ReadFile(file); err == nil && string(b) == want {
			return nil
		}
	}
	if err := os.RemoveAll(file); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(want), 0o644)
}

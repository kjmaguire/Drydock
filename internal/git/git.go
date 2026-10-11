// Package git drives the `git` CLI for local repository operations: worktrees (one per autonomous task), status, diff,
// commit, merge, rebase, and iteration safe points. Git hosting (GitHub) lives in internal/git/host.
//
// Rules (brief section 13): never delete a dirty worktree without explicit approval; never destroy uncommitted work
// silently; the engine never resets a worktree on its own.
package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Errors.
var (
	ErrDirtyWorktree = errors.New("git: worktree has uncommitted changes")
	ErrNotARepo      = errors.New("git: not a git repository")
	// ErrUnknownRevision means a commit is not in the object database (never fetched, or collected after its branch went).
	ErrUnknownRevision = errors.New("git: unknown revision")
)

// ConflictError reports a merge/rebase that stopped on conflicts. The merge is aborted before this is returned, so
// no half-merged state is left behind.
type ConflictError struct {
	Files  []string
	Output string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("git: merge conflict in %d file(s): %s", len(e.Files), strings.Join(e.Files, ", "))
}

// Repo is a git repository (its main working tree) used as the parent of task worktrees.
type Repo struct {
	Root string // absolute path of the main working tree
	// Identity is used for commits made on behalf of agents when the user has not configured git.
	AuthorName  string
	AuthorEmail string

	guard *guard // the repository's trusted git configuration (guard.go)
}

// Worktree is a task's isolated checkout.
type Worktree struct {
	Path   string
	Branch string
	Base   string // branch the worktree was created from
	TaskID string
}

// Open verifies path is a git repository and returns it.
func Open(ctx context.Context, path string) (*Repo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	r := &Repo{Root: abs, AuthorName: "Drydock", AuthorEmail: "drydock@localhost"}
	out, err := r.git(ctx, abs, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotARepo, abs)
	}
	r.Root = filepath.Clean(strings.TrimSpace(out))
	if err := r.startGuard(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

// Init creates a repository with an initial commit of whatever is in dir (used by tests and "new project").
func Init(ctx context.Context, dir, branch string) (*Repo, error) {
	r := &Repo{Root: dir, AuthorName: "Drydock", AuthorEmail: "drydock@localhost"}
	if _, err := r.gitUnguarded(ctx, dir, nil, "init", "-q", "-b", branch); err != nil {
		return nil, err
	}
	if err := r.startGuard(ctx); err != nil {
		return nil, err
	}
	if _, err := r.git(ctx, dir, nil, "add", "-A"); err != nil {
		return nil, err
	}
	if _, err := r.gitCommit(ctx, dir, "Initial commit"); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Repo) env() []string {
	return append(gitEnv(os.Environ()),
		"GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true", "GIT_MERGE_AUTOEDIT=no", "GIT_PAGER=cat",
		"GIT_AUTHOR_NAME="+r.AuthorName, "GIT_AUTHOR_EMAIL="+r.AuthorEmail,
		"GIT_COMMITTER_NAME="+r.AuthorName, "GIT_COMMITTER_EMAIL="+r.AuthorEmail,
	)
}

// keptGitEnv are the GIT_* variables of the app's own environment that git calls keep: how the user reaches their remotes,
// and which of the user's configuration files git reads (GIT_CONFIG_GLOBAL, GIT_CONFIG_SYSTEM, GIT_CONFIG_NOSYSTEM choose
// files that stand in for ~/.gitconfig and the system file; Drydock's -c settings override every file). Every other GIT_*
// variable is dropped: inherited ones such as GIT_DIR, GIT_INDEX_FILE, GIT_EXTERNAL_DIFF, GIT_CONFIG_COUNT/KEY/VALUE or
// GIT_CONFIG_PARAMETERS would point Drydock's git at another repository, run a program in it, or override the -c settings.
var keptGitEnv = map[string]bool{"GIT_SSH": true, "GIT_SSH_COMMAND": true, "GIT_SSH_VARIANT": true, "GIT_ASKPASS": true,
	"GIT_SSL_CAINFO": true, "GIT_SSL_CAPATH": true, "GIT_SSL_NO_VERIFY": true, "GIT_PROXY_COMMAND": true, "GIT_HTTP_PROXY_AUTHMETHOD": true, "GIT_EXEC_PATH": true,
	"GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_SYSTEM": true, "GIT_CONFIG_NOSYSTEM": true}

// gitEnv is env without the GIT_* variables that are not in keptGitEnv (names compared as Windows does, ignoring case).
func gitEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if u := strings.ToUpper(k); strings.HasPrefix(u, "GIT_") && !keptGitEnv[u] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

var (
	noHooksOnce sync.Once
	noHooks     string
)

// noHooksDir is an empty directory of Drydock's own that every git call uses as core.hooksPath, so no hook of the repository
// (planted or the user's own) runs inside Drydock's git calls (docs/security.md s3; safe points and commits never run user
// hooks, brief s13). If it cannot be created, a path that does not exist serves the same purpose.
func noHooksDir() string {
	noHooksOnce.Do(func() {
		d, err := os.MkdirTemp("", "drydock-no-hooks-")
		if err != nil {
			d = filepath.Join(os.TempDir(), fmt.Sprintf("drydock-no-hooks-%d-%d", os.Getpid(), time.Now().UnixNano()))
		}
		noHooks = filepath.ToSlash(d)
	})
	return noHooks
}

// hardening is the configuration every git call Drydock makes runs with: no hooks, no fsmonitor program, and (for diff) no
// external diff or textconv driver. core.longpaths avoids Windows MAX_PATH failures in deep node_modules-style trees;
// quotepath off keeps names readable.
func hardening(args []string) []string {
	full := []string{"-c", "core.hooksPath=" + noHooksDir(), "-c", "core.fsmonitor=false", "-c", "core.longpaths=true", "-c", "core.quotepath=false"}
	if len(args) > 0 && (args[0] == "diff" || args[0] == "show" || args[0] == "log") {
		return append(append(full, args[0], "--no-ext-diff", "--no-textconv"), args[1:]...)
	}
	return append(full, args...)
}

// git runs git in dir with extra env and returns stdout. A non-zero exit is an error carrying stderr. A command that could run
// a configured program first checks the repository's configuration against the trusted state (guard.go).
func (r *Repo) git(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	if len(args) > 0 && !inertCommands[args[0]] && r.guard != nil {
		if err := r.guard.check(ctx, r.Root); err != nil {
			return "", err
		}
	}
	return r.gitUnguarded(ctx, dir, extraEnv, args...)
}

func (r *Repo) gitUnguarded(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", hardening(args)...)
	cmd.Dir = dir
	cmd.Env = append(r.env(), extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), &gitError{args: args, stderr: strings.TrimSpace(stderr.String()), stdout: stdout.String(), err: err}
	}
	return stdout.String(), nil
}

type gitError struct {
	args   []string
	stderr string
	stdout string
	err    error
}

func (e *gitError) Error() string {
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.args, " "), e.err, e.stderr)
}
func (e *gitError) Unwrap() error { return e.err }

func (r *Repo) gitCommit(ctx context.Context, dir, msg string) (string, error) {
	if _, err := r.git(ctx, dir, nil, "commit", "-q", "--allow-empty", "-m", msg); err != nil {
		return "", err
	}
	out, err := r.git(ctx, dir, nil, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// Version returns the git version string ("git version 2.43.0").
func Version(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "--version").Output()
	return strings.TrimSpace(string(out)), err
}

// CurrentBranch returns the checked-out branch of the main working tree.
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	out, err := r.git(ctx, r.Root, nil, "rev-parse", "--abbrev-ref", "HEAD")
	return strings.TrimSpace(out), err
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slug makes a branch-safe fragment from free text.
func Slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		s = "task"
	}
	return s
}

// WorktreeReq describes a new worktree.
type WorktreeReq struct {
	TaskID string
	Title  string
	Base   string // branch to start from; default current branch
	Parent string // directory that holds worktrees (e.g. <app data>/worktrees/<project>)
}

// CreateWorktree creates (idempotently) the task's worktree and branch `drydock/<task>-<slug>`. If the worktree already
// exists for this task it is returned unchanged, so a retried node execution reuses it.
func (r *Repo) CreateWorktree(ctx context.Context, req WorktreeReq) (Worktree, error) {
	base := req.Base
	if base == "" {
		b, err := r.CurrentBranch(ctx)
		if err != nil {
			return Worktree{}, err
		}
		base = b
	}
	branch := "drydock/" + req.TaskID + "-" + Slug(req.Title)
	path := filepath.Join(req.Parent, req.TaskID)
	w := Worktree{Path: path, Branch: branch, Base: base, TaskID: req.TaskID}
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return w, nil // already created
	}
	if err := os.MkdirAll(req.Parent, 0o755); err != nil {
		return w, err
	}
	if _, err := r.git(ctx, r.Root, nil, "worktree", "add", "-b", branch, path, base); err != nil {
		// the branch may exist from an earlier, interrupted attempt
		if _, err2 := r.git(ctx, r.Root, nil, "worktree", "add", path, branch); err2 != nil {
			return w, err
		}
	}
	return w, nil
}

// Status is a parsed `git status --porcelain=v1`.
type Status struct {
	Branch    string
	Modified  []string
	Added     []string
	Deleted   []string
	Untracked []string
	Conflicts []string
}

// Dirty reports whether anything is uncommitted.
func (s Status) Dirty() bool {
	return len(s.Modified)+len(s.Added)+len(s.Deleted)+len(s.Untracked)+len(s.Conflicts) > 0
}

// Files lists every changed path.
func (s Status) Files() []string {
	var f []string
	for _, l := range [][]string{s.Modified, s.Added, s.Deleted, s.Untracked, s.Conflicts} {
		f = append(f, l...)
	}
	sort.Strings(f)
	return f
}

// Status reports the state of a worktree.
func (r *Repo) Status(ctx context.Context, w Worktree) (Status, error) {
	out, err := r.git(ctx, w.Path, nil, "status", "--porcelain=v1", "-b", "--untracked-files=all")
	if err != nil {
		return Status{}, err
	}
	var s Status
	unstaged := map[string]bool{} // modified in the worktree only: confirmed against the content below
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			s.Branch = strings.SplitN(strings.TrimPrefix(line, "## "), "...", 2)[0]
			continue
		}
		if len(line) < 4 {
			continue
		}
		x, y, p := line[0], line[1], strings.TrimSpace(line[3:])
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		switch {
		case x == '?' && y == '?':
			s.Untracked = append(s.Untracked, p)
		case x == 'U' || y == 'U' || (x == 'A' && y == 'A') || (x == 'D' && y == 'D'):
			s.Conflicts = append(s.Conflicts, p)
		case x == 'A':
			s.Added = append(s.Added, p)
		case x == 'D' || y == 'D':
			s.Deleted = append(s.Deleted, p)
		case x == ' ' && y == 'M':
			unstaged[p] = true
			s.Modified = append(s.Modified, p)
		default:
			s.Modified = append(s.Modified, p)
		}
	}
	if len(unstaged) > 0 {
		// status calls a file modified when its size differs from the checkout, without reading it. With core.autocrlf
		// (the Git for Windows default) a file checked out with CRLF and written back with LF and the same text differs in
		// size only, so status reports a change that git diff, which compares the content, does not. Only diff's answer is
		// kept; both print paths quoted the same way.
		diff, err := r.git(ctx, w.Path, nil, "diff", "--name-only", "--no-renames")
		if err != nil {
			return Status{}, err
		}
		changed := map[string]bool{}
		for _, l := range strings.Split(diff, "\n") {
			changed[strings.TrimSpace(l)] = true
		}
		s.Modified = slices.DeleteFunc(s.Modified, func(p string) bool { return unstaged[p] && !changed[p] })
	}
	return s, nil
}

// diffBase turns a branch name into the commit the worktree's branch shares with it. Diffing against the branch's CURRENT tip would
// show every change that landed on it since the worktree was made (for example another task's merge) as if this task had
// removed it; the merge base shows only this task's own work. If the name is not a ref the worktree shares history with (a
// commit id, an unrelated ref) it is used as given.
func (r *Repo) diffBase(ctx context.Context, w Worktree, base string) string {
	if base == "" {
		base = w.Base
	}
	if mb, err := r.git(ctx, w.Path, nil, "merge-base", "HEAD", base); err == nil {
		if mb = strings.TrimSpace(mb); mb != "" {
			return mb
		}
	}
	return base
}

// Diff returns the unified diff of the worktree (committed and uncommitted changes, including untracked files) against base. A
// branch name is resolved to its merge base with the worktree, so only the worktree's own changes appear.
func (r *Repo) Diff(ctx context.Context, w Worktree, base string) (string, error) {
	base = r.diffBase(ctx, w, base)
	// make untracked files visible to `git diff` without staging them for real: intent-to-add in a throwaway index
	idx, cleanup, err := r.tempIndex(ctx, w)
	if err != nil {
		return "", err
	}
	defer cleanup()
	env := []string{"GIT_INDEX_FILE=" + idx}
	if _, err := r.git(ctx, w.Path, env, "add", "-A"); err != nil {
		return "", err
	}
	return r.git(ctx, w.Path, env, "diff", "--cached", "--no-color", "--find-renames", base)
}

// DiffStat returns `git diff --stat` style numbers for the worktree against base.
type DiffStat struct {
	Files      int
	Insertions int
	Deletions  int
}

func (r *Repo) DiffStat(ctx context.Context, w Worktree, base string) (DiffStat, error) {
	base = r.diffBase(ctx, w, base)
	idx, cleanup, err := r.tempIndex(ctx, w)
	if err != nil {
		return DiffStat{}, err
	}
	defer cleanup()
	env := []string{"GIT_INDEX_FILE=" + idx}
	if _, err := r.git(ctx, w.Path, env, "add", "-A"); err != nil {
		return DiffStat{}, err
	}
	out, err := r.git(ctx, w.Path, env, "diff", "--cached", "--numstat", base)
	if err != nil {
		return DiffStat{}, err
	}
	var ds DiffStat
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 3 {
			continue
		}
		ds.Files++
		// a binary file's counts are "-": they count as 0 lines
		a, _ := strconv.Atoi(f[0])
		d, _ := strconv.Atoi(f[1])
		ds.Insertions += a
		ds.Deletions += d
	}
	return ds, nil
}

// DiffHash is the watchdog's progress fingerprint: a hash of the diff against HEAD plus the sorted untracked files
// (brief section 7). It changes whenever the worktree content changes.
func (r *Repo) DiffHash(ctx context.Context, w Worktree) (string, error) {
	idx, cleanup, err := r.tempIndex(ctx, w)
	if err != nil {
		return "", err
	}
	defer cleanup()
	env := []string{"GIT_INDEX_FILE=" + idx}
	if _, err := r.git(ctx, w.Path, env, "add", "-A"); err != nil {
		return "", err
	}
	tree, err := r.git(ctx, w.Path, env, "write-tree")
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(strings.TrimSpace(tree)))
	return hex.EncodeToString(h[:8]), nil
}

// tempIndex copies the worktree's index to a temp file so we can `add -A` without touching what the agent sees.
func (r *Repo) tempIndex(ctx context.Context, w Worktree) (string, func(), error) {
	gitDir, err := r.git(ctx, w.Path, nil, "rev-parse", "--git-dir")
	if err != nil {
		return "", func() {}, err
	}
	gd := strings.TrimSpace(gitDir)
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(w.Path, gd)
	}
	f, err := os.CreateTemp("", "drydock-index-*")
	if err != nil {
		return "", func() {}, err
	}
	f.Close()
	if src, err := os.ReadFile(filepath.Join(gd, "index")); err == nil {
		if err := os.WriteFile(f.Name(), src, 0o600); err != nil {
			os.Remove(f.Name())
			return "", func() {}, err
		}
	} else {
		os.Remove(f.Name()) // no index yet: git creates one
	}
	return f.Name(), func() { os.Remove(f.Name()); os.Remove(f.Name() + ".lock") }, nil
}

// Commit stages everything and commits. It returns the commit id (or the current HEAD if there was nothing to commit).
func (r *Repo) Commit(ctx context.Context, w Worktree, msg string) (string, error) {
	if _, err := r.git(ctx, w.Path, nil, "add", "-A"); err != nil {
		return "", err
	}
	st, err := r.Status(ctx, w)
	if err != nil {
		return "", err
	}
	if !st.Dirty() {
		out, err := r.git(ctx, w.Path, nil, "rev-parse", "HEAD")
		return strings.TrimSpace(out), err
	}
	return r.gitCommit(ctx, w.Path, msg)
}

// Head returns the commit id at HEAD of the worktree.
func (r *Repo) Head(ctx context.Context, w Worktree) (string, error) {
	out, err := r.git(ctx, w.Path, nil, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// MergeStrategy for bringing a task branch into the target.
type MergeStrategy string

const (
	MergeCommit MergeStrategy = "merge"
	MergeSquash MergeStrategy = "squash"
	MergeRebase MergeStrategy = "rebase"
)

// Merge brings the task branch into target in the main working tree (which must be clean and is switched to target).
// On conflict the merge is aborted and a *ConflictError is returned. A human action.
func (r *Repo) Merge(ctx context.Context, w Worktree, target string, strategy MergeStrategy, msg string) (string, error) {
	defer r.lockMain()()
	st, err := r.Status(ctx, Worktree{Path: r.Root})
	if err != nil {
		return "", err
	}
	if st.Dirty() {
		return "", fmt.Errorf("%w: the main working tree has local changes; commit or stash them before merging", ErrDirtyWorktree)
	}
	if cur, _ := r.CurrentBranch(ctx); cur != target {
		if _, err := r.git(ctx, r.Root, nil, "checkout", "-q", target); err != nil {
			return "", err
		}
	}
	if msg == "" {
		msg = "Merge " + w.Branch
	}
	var mergeErr error
	switch strategy {
	case MergeSquash:
		_, mergeErr = r.git(ctx, r.Root, nil, "merge", "--squash", w.Branch)
		if mergeErr == nil {
			_, mergeErr = r.gitCommit(ctx, r.Root, msg)
		}
	case MergeRebase:
		// rebase the task branch onto target in its worktree, then fast-forward target
		if err := r.Rebase(ctx, w, target); err != nil {
			return "", err
		}
		_, mergeErr = r.git(ctx, r.Root, nil, "merge", "--ff-only", w.Branch)
	default:
		_, mergeErr = r.git(ctx, r.Root, nil, "merge", "--no-ff", "-m", msg, w.Branch)
	}
	if mergeErr != nil {
		files := r.conflictFiles(ctx, r.Root)
		if len(files) > 0 {
			_, _ = r.git(ctx, r.Root, nil, "merge", "--abort")
			_, _ = r.git(ctx, r.Root, nil, "reset", "-q", "--merge")
			return "", &ConflictError{Files: files, Output: mergeErr.Error()}
		}
		return "", mergeErr
	}
	out, err := r.git(ctx, r.Root, nil, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

func (r *Repo) conflictFiles(ctx context.Context, dir string) []string {
	out, _ := r.git(ctx, dir, nil, "diff", "--name-only", "--diff-filter=U")
	var f []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			f = append(f, l)
		}
	}
	return f
}

// Rebase rebases the worktree's branch onto onto. On conflict it aborts and returns *ConflictError.
func (r *Repo) Rebase(ctx context.Context, w Worktree, onto string) error {
	_, err := r.git(ctx, w.Path, nil, "rebase", onto)
	if err != nil {
		files := r.conflictFiles(ctx, w.Path)
		_, _ = r.git(ctx, w.Path, nil, "rebase", "--abort")
		if len(files) > 0 {
			return &ConflictError{Files: files, Output: err.Error()}
		}
		return err
	}
	return nil
}

// DeleteWorktree removes a worktree. It refuses a dirty worktree unless force is true; force requires the caller to
// hold an approval (the caller's responsibility; this package only enforces the default).
func (r *Repo) DeleteWorktree(ctx context.Context, w Worktree, force bool) error {
	if _, err := os.Stat(w.Path); os.IsNotExist(err) {
		_, _ = r.git(ctx, r.Root, nil, "worktree", "prune")
		return nil
	}
	st, err := r.Status(ctx, w)
	if err != nil {
		return err
	}
	if st.Dirty() && !force {
		return fmt.Errorf("%w: %d changed file(s)", ErrDirtyWorktree, len(st.Files()))
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, w.Path)
	_, err = r.git(ctx, r.Root, nil, args...)
	return err
}

// DeleteBranch deletes a local branch (merged only unless force).
func (r *Repo) DeleteBranch(ctx context.Context, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := r.git(ctx, r.Root, nil, "branch", flag, branch)
	return err
}

// --- safe points ---

// SafePointRef returns the hidden ref name for an attempt.
func SafePointRef(taskID string, attempt int) string {
	return fmt.Sprintf("refs/drydock/safe/%s/%d", taskID, attempt)
}

// SafePoint records the exact worktree state (tracked and untracked files) as a hidden commit without touching the
// working branch or the index the agent sees: temporary index, add -A, write-tree, commit-tree, update-ref.
func (r *Repo) SafePoint(ctx context.Context, w Worktree, attempt int) (string, error) {
	idx, cleanup, err := r.tempIndex(ctx, w)
	if err != nil {
		return "", err
	}
	defer cleanup()
	env := []string{"GIT_INDEX_FILE=" + idx}
	if _, err := r.git(ctx, w.Path, env, "add", "-A"); err != nil {
		return "", err
	}
	tree, err := r.git(ctx, w.Path, env, "write-tree")
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", strings.TrimSpace(tree), "-m", fmt.Sprintf("drydock safe point: task %s attempt %d", w.TaskID, attempt)}
	if head, err := r.Head(ctx, w); err == nil {
		args = append(args, "-p", head)
	}
	commit, err := r.git(ctx, w.Path, env, args...)
	if err != nil {
		return "", err
	}
	ref := SafePointRef(w.TaskID, attempt)
	if _, err := r.git(ctx, w.Path, nil, "update-ref", ref, strings.TrimSpace(commit)); err != nil {
		return "", err
	}
	return ref, nil
}

// RestoreSafePoint puts the worktree's files back to the state recorded at ref. It is a human action: it first records
// the current state as a new safe point (so the rollback itself is undoable) and returns that ref.
func (r *Repo) RestoreSafePoint(ctx context.Context, w Worktree, ref string, undoAttempt int) (string, error) {
	undo, err := r.SafePoint(ctx, w, undoAttempt)
	if err != nil {
		return "", fmt.Errorf("could not record the current state before restoring: %w", err)
	}
	// files in the safe point
	listOut, err := r.git(ctx, w.Path, nil, "ls-tree", "-r", "--name-only", ref)
	if err != nil {
		return "", err
	}
	want := map[string]bool{}
	for _, f := range strings.Split(listOut, "\n") {
		if f = strings.TrimSpace(f); f != "" {
			want[f] = true
		}
	}
	// remove tracked/untracked files that are not part of the safe point
	curOut, _ := r.git(ctx, w.Path, nil, "ls-files", "-c", "-o", "--exclude-standard")
	for _, f := range strings.Split(curOut, "\n") {
		if f = strings.TrimSpace(f); f != "" && !want[f] {
			_ = os.Remove(filepath.Join(w.Path, filepath.FromSlash(f)))
		}
	}
	if _, err := r.git(ctx, w.Path, nil, "restore", "--source="+ref, "--staged", "--worktree", ":/"); err != nil {
		return undo, err
	}
	// leave the index matching HEAD so the agent sees the restored state as ordinary changes
	if _, err := r.git(ctx, w.Path, nil, "reset", "-q"); err != nil {
		return undo, err
	}
	return undo, nil
}

// LocalIgnore keeps paths out of git status/commits in this checkout without touching tracked history: tracked files get
// the skip-worktree flag (local modifications are invisible to add/commit), untracked ones are added to the repository's
// local info/exclude. Used for Drydock-generated instruction files.
func (r *Repo) LocalIgnore(ctx context.Context, w Worktree, paths []string) error {
	var untracked []string
	for _, p := range paths {
		if _, err := r.git(ctx, w.Path, nil, "ls-files", "--error-unmatch", "--", p); err == nil {
			if _, err := r.git(ctx, w.Path, nil, "update-index", "--skip-worktree", "--", p); err != nil {
				return err
			}
		} else {
			untracked = append(untracked, p)
		}
	}
	if len(untracked) == 0 {
		return nil
	}
	out, err := r.git(ctx, w.Path, nil, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	ex := strings.TrimSpace(out)
	if !filepath.IsAbs(ex) {
		ex = filepath.Join(w.Path, ex)
	}
	if err := os.MkdirAll(filepath.Dir(ex), 0o755); err != nil {
		return err
	}
	cur, _ := os.ReadFile(ex)
	have := map[string]bool{}
	for _, l := range strings.Split(string(cur), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	var add strings.Builder
	for _, p := range untracked {
		if !have["/"+p] {
			add.WriteString("/" + p + "\n")
		}
	}
	if add.Len() == 0 {
		return nil
	}
	f, err := os.OpenFile(ex, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(cur) > 0 && !strings.HasSuffix(string(cur), "\n") {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err = f.WriteString(add.String())
	return err
}

// Push publishes the task branch to remote. It never forces: a rework adds commits on top of what was pushed, and a
// rejected (non-fast-forward) push is an error for a human to resolve.
func (r *Repo) Push(ctx context.Context, w Worktree, remote string) error {
	if remote == "" {
		remote = "origin"
	}
	_, err := r.git(ctx, w.Path, nil, "push", "--set-upstream", remote, "refs/heads/"+w.Branch+":refs/heads/"+w.Branch)
	return err
}

// MergeBase returns the commit the worktree's branch shares with ref.
func (r *Repo) MergeBase(ctx context.Context, w Worktree, ref string) (string, error) {
	out, err := r.git(ctx, w.Path, nil, "merge-base", "HEAD", ref)
	return strings.TrimSpace(out), err
}

// Behind counts the commits on ref that the worktree's branch does not have yet.
func (r *Repo) Behind(ctx context.Context, w Worktree, ref string) (int, error) {
	out, err := r.git(ctx, w.Path, nil, "rev-list", "--count", "HEAD.."+ref)
	if err != nil {
		return 0, err
	}
	var n int
	_, err = fmt.Sscan(strings.TrimSpace(out), &n)
	return n, err
}

// ChangedPaths lists the paths the worktree has changed against base's merge base: committed, uncommitted and untracked, with a
// rename counted as the old and the new path. It is the task's footprint, used to see whether two tasks touch the same files.
func (r *Repo) ChangedPaths(ctx context.Context, w Worktree, base string) ([]string, error) {
	base = r.diffBase(ctx, w, base)
	idx, cleanup, err := r.tempIndex(ctx, w)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	env := []string{"GIT_INDEX_FILE=" + idx}
	if _, err := r.git(ctx, w.Path, env, "add", "-A"); err != nil {
		return nil, err
	}
	out, err := r.git(ctx, w.Path, env, "diff", "--cached", "--name-only", "--no-renames", base)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			paths = append(paths, l)
		}
	}
	return paths, nil
}

// MergeIn brings ref into the worktree's branch with a merge commit (history is not rewritten). The worktree must be clean.
// changed is false when the branch already contained ref. On conflicts the merge is LEFT IN PROGRESS with the markers in the
// files and conflicts lists them, so the agent that owns the worktree can resolve them; ResolveCheck says when they are gone.
func (r *Repo) MergeIn(ctx context.Context, w Worktree, ref string) (changed bool, conflicts []string, err error) {
	st, err := r.Status(ctx, w)
	if err != nil {
		return false, nil, err
	}
	if st.Dirty() {
		return false, nil, fmt.Errorf("%w: commit the work before bringing %s in", ErrDirtyWorktree, ref)
	}
	behind, err := r.Behind(ctx, w, ref)
	if err != nil || behind == 0 {
		return false, nil, err
	}
	_, mergeErr := r.git(ctx, w.Path, nil, "merge", "--no-edit", "-m", "Bring "+ref+" into "+w.Branch, ref)
	if mergeErr != nil {
		if files := r.conflictFiles(ctx, w.Path); len(files) > 0 {
			return true, files, nil
		}
		return false, nil, mergeErr
	}
	return true, nil, nil
}

// MergeInProgress reports whether the worktree is in the middle of a merge (a conflict that is not resolved and committed yet).
func (r *Repo) MergeInProgress(ctx context.Context, w Worktree) bool {
	out, err := r.git(ctx, w.Path, nil, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	return err == nil && strings.TrimSpace(out) != ""
}

// AbortMerge ends a merge that is in progress and restores the branch to where it was.
func (r *Repo) AbortMerge(ctx context.Context, w Worktree) error {
	_, err := r.git(ctx, w.Path, nil, "merge", "--abort")
	return err
}

// UnresolvedConflicts lists the files of an in-progress merge that still contain conflict markers.
func (r *Repo) UnresolvedConflicts(ctx context.Context, w Worktree) []string {
	var left []string
	for _, f := range r.conflictFiles(ctx, w.Path) {
		b, err := os.ReadFile(filepath.Join(w.Path, filepath.FromSlash(f)))
		if err != nil {
			continue // a file the agent deleted counts as resolved
		}
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "<<<<<<< ") || strings.HasPrefix(l, ">>>>>>> ") || l == "=======" {
				left = append(left, f)
				break
			}
		}
	}
	return left
}

var mergeLocks sync.Map // repository root -> *sync.Mutex

// lockMain serializes the operations that change the main working tree of one repository (merging a task into the base branch).
// Two approvals at the same moment would otherwise race on git's index lock and one would fail half-way.
func (r *Repo) lockMain() func() {
	v, _ := mergeLocks.LoadOrStore(r.Root, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// PatchID identifies the worktree's own committed change independent of where it sits in history (git patch-id --stable of the
// diff against the merge base with ref). Bringing the base branch into the branch cleanly moves the merge base but not this value;
// resolving a conflict, or any edit, changes it. It is how "the same change that was approved" is recognized.
func (r *Repo) PatchID(ctx context.Context, w Worktree, ref string) (string, error) {
	mb, err := r.MergeBase(ctx, w, ref)
	if err != nil {
		return "", err
	}
	diff, err := r.git(ctx, w.Path, nil, "diff", "--no-color", "--no-renames", mb, "HEAD")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(diff) == "" {
		return "empty", nil
	}
	ctx2, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx2, "git", hardening([]string{"patch-id", "--stable"})...)
	cmd.Dir = w.Path
	cmd.Env = r.env()
	cmd.Stdin = strings.NewReader(diff)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git patch-id: %w", err)
	}
	f := strings.Fields(out.String())
	if len(f) == 0 {
		return "", errors.New("git patch-id printed nothing")
	}
	return f[0], nil
}

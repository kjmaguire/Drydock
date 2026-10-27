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
	"io/fs"
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

	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/safefs"
)

// Errors.
var (
	ErrDirtyWorktree = errors.New("git: worktree has uncommitted changes")
	ErrNotARepo      = errors.New("git: not a git repository")
	// ErrUnknownRevision means a commit is not in the object database (never fetched, or collected after its branch went).
	ErrUnknownRevision = errors.New("git: unknown revision")
	// ErrOutputTooLarge means a git call printed more than Drydock keeps in memory; the call was ended.
	ErrOutputTooLarge = errors.New("git: the output is larger than the limit")
	// ErrNoRemote is RemoteURL's answer when the repository has no remote of that name.
	ErrNoRemote = errors.New("git: no such remote")
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

// StaleBaseError reports a merge that was not made because the target branch moved after the branch was last brought up to date
// with it: what was checked and approved is not what would land (docs/parallel-work.md). Nothing was changed.
type StaleBaseError struct {
	Target string
	Behind int
}

func (e *StaleBaseError) Error() string {
	return fmt.Sprintf("git: %s has %d commit(s) the branch does not have yet", e.Target, e.Behind)
}

// ChangedSinceReviewError reports a merge that was not made because the branch is no longer at the commit that was reviewed.
// Nothing was changed.
type ChangedSinceReviewError struct{ Reviewed, Now string }

func (e *ChangedSinceReviewError) Error() string {
	return fmt.Sprintf("git: the branch is at %s, not at the reviewed commit %s", shortSHA(e.Now), shortSHA(e.Reviewed))
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// Repo is a git repository (its main working tree) used as the parent of task worktrees.
type Repo struct {
	Root string // absolute path of the main working tree
	// Identity is used for commits made on behalf of agents when the user has not configured git.
	AuthorName  string
	AuthorEmail string

	guard *guard // the repository's trusted git configuration (guard.go)
	// runner, when set, runs every git call of this repository somewhere else: inside the WSL distro the repository lives in
	// (decision P41). Nil: this machine's git.
	runner Runner
}

// Runner runs git somewhere other than this machine's own git: for a repository inside a WSL distro, the distro's git, so
// the worktrees it makes link to each other with the distro's paths, which the agent's git (and the person's) inside the
// distro can read. Windows git through \\wsl.localhost would write \\wsl.localhost links that git inside the distro cannot
// follow (and would prune). internal/wsl.GitRunner implements it.
type Runner interface {
	// Command returns the program and its arguments that run git with args in dir, with env set (KEY=VALUE, never secrets:
	// they may be on a command line). Paths in dir, env and args are this machine's (Windows) paths.
	Command(dir string, env, args []string) (path string, argv []string, procEnv []string, err error)
	// HostPath maps an absolute path git printed back to this machine's form.
	HostPath(p string) string
}

// OpenIn is Open for a repository whose git calls all run through runner (nil: as Open).
func OpenIn(ctx context.Context, path string, runner Runner) (*Repo, error) {
	abs := path
	if runner == nil {
		return Open(ctx, path)
	}
	r := &Repo{Root: abs, AuthorName: "Drydock", AuthorEmail: "drydock@localhost", runner: runner}
	out, err := r.git(ctx, abs, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, notARepo(abs, err)
	}
	r.Root = strings.TrimSpace(out)
	if err := r.startGuard(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

// RemoteURL is the URL of the named remote, read through Drydock's git chokepoint (so a repository inside a WSL distro asks
// the distro's git). Reading a remote runs no configured program.
func (r *Repo) RemoteURL(ctx context.Context, name string) (string, error) {
	out, err := r.gitUnguarded(ctx, r.Root, nil, "remote", "get-url", name)
	if ge := (*gitError)(nil); errors.As(err, &ge) && strings.Contains(ge.stderr, "No such remote") {
		return "", fmt.Errorf("%w: %q", ErrNoRemote, name) // git speaks English here: Drydock sets LC_ALL=C for every call
	}
	return strings.TrimSpace(out), err
}

// WorktreeParent is the folder that holds this repository's task worktrees for project: defaultRoot/<project> (the app's
// data folder) for a repository on Windows; for a repository inside a WSL distro, a folder next to it inside the distro
// (<parent>/.drydock-worktrees/<project>), because a worktree is only as fast for an agent in the distro as the disk it is on
// and git inside the distro must be able to follow its links (decision P41).
func (r *Repo) WorktreeParent(defaultRoot, project string) string {
	if r.runner != nil || isDistroPath(r.Root) {
		return filepath.Join(filepath.Dir(r.Root), ".drydock-worktrees", project)
	}
	return filepath.Join(defaultRoot, project)
}

// drydockEnv is what Drydock sets for every git call (on top of the environment): no prompt, no locks for reads, a fixed
// language, no editor or pager, and the commit identity.
func (r *Repo) drydockEnv() []string {
	return []string{"GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true", "GIT_MERGE_AUTOEDIT=no", "GIT_PAGER=cat",
		"GIT_AUTHOR_NAME=" + r.AuthorName, "GIT_AUTHOR_EMAIL=" + r.AuthorEmail,
		"GIT_COMMITTER_NAME=" + r.AuthorName, "GIT_COMMITTER_EMAIL=" + r.AuthorEmail}
}

// command builds the git process for args in dir: this machine's git with the hardening (and, for a distro path reached
// from Windows, the safe.directory that path needs), or the runner's.
func (r *Repo) command(ctx context.Context, dir string, extraEnv []string, args []string) (*exec.Cmd, error) {
	// a call in a task's worktree names its repository instead of letting git discover it from files the agent can write
	// (pin.go); a pinned call's own variables come last so nothing in extraEnv can redirect it
	pin, err := r.pinFor(dir)
	if err != nil {
		return nil, err
	}
	extraEnv = append(slices.Clone(extraEnv), pin...)
	if r.runner != nil {
		path, argv, penv, err := r.runner.Command(dir, append(r.drydockEnv(), extraEnv...), hardening(args))
		if err != nil {
			return nil, err
		}
		cmd := exec.CommandContext(ctx, path, argv...)
		cmd.Env = penv
		proc.Harden(cmd)
		return cmd, nil
	}
	cmd := exec.CommandContext(ctx, "git", append(safeDirectory(dir, r.Root), hardening(args)...)...)
	cmd.Dir = dir
	cmd.Env = append(r.env(!talksToARemote(args)), extraEnv...)
	proc.Harden(cmd)
	return cmd, nil
}

// hostOutput maps the absolute paths a runner's git printed (rev-parse --show-toplevel, --git-dir, --git-path) to this
// machine's form.
func (r *Repo) hostOutput(args []string, out string) string {
	if r.runner == nil || len(args) == 0 || args[0] != "rev-parse" {
		return out
	}
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "/") {
			lines[i] = r.runner.HostPath(t)
		}
	}
	return strings.Join(lines, "\n")
}

// safeDirectory is the -c safe.directory a Windows git needs to work in a repository inside a WSL distro reached through
// \\wsl.localhost (its files belong to the distro's user, so git reports "dubious ownership"). It names exactly the paths
// of this call, on the command line of this call only: the person's global configuration is never changed. Empty for any
// other path. A repository Drydock opened inside a distro runs its git there (Runner); this covers a Windows git that still
// reaches one.
func safeDirectory(paths ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range paths {
		if !isDistroPath(p) {
			continue
		}
		s := strings.ReplaceAll(p, `\`, "/")
		if !seen[s] {
			seen[s] = true
			out = append(out, "-c", "safe.directory="+s)
		}
	}
	return out
}

// isDistroPath reports whether p is a WSL distro's file as Windows names it (\\wsl.localhost\..., \\wsl$\...).
func isDistroPath(p string) bool {
	s := strings.ToLower(strings.ReplaceAll(p, "/", `\`))
	return strings.HasPrefix(s, `\\wsl.localhost\`) || strings.HasPrefix(s, `\\wsl$\`)
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
		if _, statErr := os.Stat(abs); errors.Is(statErr, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotARepo, abs) // the folder is gone: git's words would only say it could not enter it
		}
		return nil, notARepo(abs, err)
	}
	r.Root = filepath.Clean(strings.TrimSpace(out))
	if err := r.startGuard(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

// notARepo is why git does not take path as a repository, for the person reading it: the path alone when git says no more than
// that (a plain folder), git's own words when they say more (a broken .git/config: "fatal: bad config line 1 in file
// .git/config"), and why git could not run at all otherwise (a distro's git). ErrNotARepo is wrapped in each, so callers tell
// the case from other failures the same way. git speaks English here: LC_ALL=C is set for every call.
func notARepo(path string, err error) error {
	var ge *gitError
	if errors.As(err, &ge) && ge.stderr != "" {
		if strings.Contains(ge.stderr, "not a git repository") {
			return fmt.Errorf("%w: %s", ErrNotARepo, path)
		}
		return fmt.Errorf("%w: %s: %s", ErrNotARepo, path, ge.stderr)
	}
	return fmt.Errorf("%w: %s (%v)", ErrNotARepo, path, err)
}

// Init creates a repository with an initial commit of whatever is in dir (used by tests and "new project").
func Init(ctx context.Context, dir, branch string) (*Repo, error) {
	return InitIn(ctx, dir, branch, nil)
}

// InitIn is Init with every git call run through runner (nil: this machine's git): a repository made inside a WSL distro is
// made by the distro's git (decision P41). The first commit holds what is in dir (what a .gitignore there leaves out stays
// out); an empty dir gets an empty commit, so the branch exists and worktrees can be made from it.
func InitIn(ctx context.Context, dir, branch string, runner Runner) (*Repo, error) {
	r := &Repo{Root: dir, AuthorName: "Drydock", AuthorEmail: "drydock@localhost", runner: runner}
	if _, err := r.gitUnguarded(ctx, dir, nil, "init", "-q", "-b", branch); err != nil {
		return nil, err
	}
	if err := r.startGuard(ctx); err != nil {
		return nil, err
	}
	if err := r.addAll(ctx, dir, nil); err != nil {
		return nil, err
	}
	if _, err := r.gitCommit(ctx, dir, "Initial commit"); err != nil {
		return nil, err
	}
	return r, nil
}

// WorkTreeOf is the top folder of the Git work tree that holds dir ("" when dir is in none), asked through Drydock's git
// chokepoint (runner nil: this machine's git). dir must exist. Any other failure (a folder inside a .git folder, a repository
// git refuses as another user's) is an error.
func WorkTreeOf(ctx context.Context, dir string, runner Runner) (string, error) {
	r := &Repo{Root: dir, AuthorName: "Drydock", AuthorEmail: "drydock@localhost", runner: runner}
	out, err := r.gitUnguarded(ctx, dir, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		var ge *gitError
		if errors.As(err, &ge) && strings.Contains(ge.stderr, "not a git repository") {
			return "", nil
		}
		return "", err
	}
	top := strings.TrimSpace(out)
	if runner == nil && top != "" {
		top = filepath.Clean(top)
	}
	return top, nil
}

// TrackedFiles counts the files in the index of the main working tree (after Init: the files of the first commit).
func (r *Repo) TrackedFiles(ctx context.Context) (int, error) {
	out, err := r.git(ctx, r.Root, nil, "ls-files", "-z")
	if err != nil {
		return 0, err
	}
	return strings.Count(out, "\x00"), nil
}

// ListFiles lists the files of the checkout at dir (the repository's own folder or one of its worktrees; empty: Root) as git
// sees them: tracked files and untracked files that .gitignore, .git/info/exclude and the global excludes do not ignore.
// Paths are relative to dir, with forward slashes. Files deleted from the working tree but still in the index are listed
// too; callers that read the files skip the missing ones.
func (r *Repo) ListFiles(ctx context.Context, dir string) ([]string, error) {
	if dir == "" {
		dir = r.Root
	}
	out, err := r.git(ctx, dir, nil, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" && !seen[f] { // a file with merge conflicts is listed once per stage
			seen[f] = true
			files = append(files, f)
		}
	}
	return files, nil
}

// env is the environment of a git call on this machine. A call that talks to a remote (push) keeps the app's own environment:
// the credential helper, ssh and askpass programs it starts read the person's login from it. Every other call gets the
// allowlist that project commands get, plus what git's configuration reads (localEnvNames) and the GIT_* variables of keptGitEnv,
// so whatever a configuration could still start in a call that stays on this machine (a filter, a diff driver, a pager) finds
// none of the tokens the app was started with (audit 2026-10-16, L8).
func (r *Repo) env(local bool) []string {
	parent := gitEnv(os.Environ())
	if local {
		names := append(append(append([]string(nil), proc.BaseEnvNames...), proc.ToolEnvNames...), localEnvNames...)
		parent = proc.ScrubEnv(parent, names, []string{"GIT_"})
	}
	// git for Windows starts sh, ssh and the like by name from the folder it runs in, which for a task's worktree is the agent's
	return proc.WithSafeExeSearch(append(parent, r.drydockEnv()...))
}

// localEnvNames are variables a local git call may read beyond proc.BaseEnvNames and proc.ToolEnvNames: the Windows account
// facts git for Windows and the MSYS runtime under it ask for. None of them carries a credential.
var localEnvNames = []string{"ALLUSERSPROFILE", "USERDOMAIN", "USERDOMAIN_ROAMINGPROFILE", "LOGONSERVER", "SESSIONNAME", "MSYSTEM", "MSYS", "CYGWIN",
	"PROCESSOR_ARCHITEW6432", "PROCESSOR_LEVEL", "PROCESSOR_REVISION"}

// talksToARemote reports whether a git call (args as Drydock passes them, with its own -c and -C options in front of the
// subcommand) contacts a remote and so needs the person's login.
func talksToARemote(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-c" || a == "-C":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			switch a {
			case "push", "fetch", "pull", "clone", "ls-remote", "submodule", "lfs":
				return true
			case "remote": // asking for a remote's address is local; updating, pruning or showing one contacts it
				return !slices.Contains(args[i+1:], "get-url")
			}
			return false
		}
	}
	return false
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

// noHooksDir is the empty directory every git call uses as core.hooksPath (proc.NoHooksDir).
func noHooksDir() string { return proc.NoHooksDir() }

// hardening is the configuration every git call Drydock makes runs with: no hooks, no fsmonitor program, and (for diff) no
// external diff or textconv driver. Submodules and embedded repositories are never asked whether they are modified: that is a
// git run inside the nested repository, with the configuration (clean filters, for one) that repository carries, which a task's
// agent can write (pin.go). The command-line flag beats a `.gitmodules` setting; the -c settings cover the commands that have
// no flag (commit, merge). core.longpaths avoids Windows MAX_PATH failures in deep node_modules-style trees; quotepath off keeps
// names readable.
func hardening(args []string) []string {
	full := []string{"-c", "core.hooksPath=" + noHooksDir(), "-c", "core.fsmonitor=false", "-c", "core.longpaths=true", "-c", "core.quotepath=false",
		"-c", "diff.ignoreSubmodules=all", "-c", "status.submoduleSummary=false", "-c", "submodule.recurse=false"}
	if len(args) == 0 {
		return full
	}
	switch args[0] {
	case "diff":
		return append(append(full, "diff", "--no-ext-diff", "--no-textconv", "--ignore-submodules=all"), args[1:]...)
	case "show", "log":
		return append(append(full, args[0], "--no-ext-diff", "--no-textconv"), args[1:]...)
	case "status":
		return append(append(full, "status", "--ignore-submodules=all"), args[1:]...)
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
	cmd, err := r.command(ctx, dir, extraEnv, args)
	if err != nil {
		return "", &gitError{args: args, err: err}
	}
	// What git prints is kept in memory, so it is bounded: a diff of a file an agent made huge, a listing of a tree it filled,
	// must end the call and not the app (audit 2026-10-16, M6). Going over the limit cancels the call.
	stdout := &proc.CapWriter{Max: maxStdout, Over: cancel}
	stderr := &proc.CapWriter{Max: maxStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if stdout.Exceeded() {
			err = ErrOutputTooLarge
		}
		return stdout.String(), &gitError{args: args, stderr: strings.TrimSpace(stderr.String()), stdout: stdout.String(), err: err}
	}
	if stdout.Exceeded() { // a command that finished before the cancel could end it
		return "", &gitError{args: args, stderr: strings.TrimSpace(stderr.String()), err: ErrOutputTooLarge}
	}
	return r.hostOutput(args, stdout.String()), nil
}

// The most a git call may print that is kept: standard output, and the error text. A var so a test can lower it.
var (
	maxStdout = 256 << 20
	maxStderr = 1 << 20
)

type gitError struct {
	args   []string
	stderr string
	stdout string
	err    error
}

// Error is git's command line, what went wrong and git's own words when it said any: a git that could not start, or a folder
// it could not enter, leaves none, and nothing dangles after the reason then.
func (e *gitError) Error() string {
	s := fmt.Sprintf("git %s: %v", strings.Join(e.args, " "), e.err)
	if e.stderr != "" {
		s += ": " + e.stderr
	}
	return s
}
func (e *gitError) Unwrap() error { return e.err }

func (r *Repo) gitCommit(ctx context.Context, dir, msg string) (string, error) {
	if _, err := r.git(ctx, dir, nil, "commit", "-q", "--allow-empty", "-m", msg); err != nil {
		return "", err
	}
	out, err := r.git(ctx, dir, nil, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// PathState says, for rel (a path relative to dir, a folder of the main working tree), the commit that last changed it on the
// current branch ("" when none did) and whether it is tracked with no change, staged or not. Both come back empty/false when git
// cannot say. It runs through the same hardened, guarded calls as the rest of the package.
func (r *Repo) PathState(ctx context.Context, dir, rel string) (commit string, clean bool) {
	out, err := r.git(ctx, dir, nil, "log", "-1", "--format=%H", "--", rel)
	if c := strings.TrimSpace(out); err != nil || c == "" {
		return "", false
	} else {
		commit = c
	}
	st, err := r.git(ctx, dir, nil, "status", "--porcelain", "--", rel)
	if err != nil {
		return commit, false
	}
	return commit, strings.TrimSpace(st) == ""
}

// Version returns the git version string ("git version 2.43.0").
func Version(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "--version")
	proc.Harden(cmd)
	out, err := cmd.Output()
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

// BranchName is the branch a task's worktree is made on: drydock/<task>-<slug of its title>.
func BranchName(taskID, title string) string { return "drydock/" + taskID + "-" + Slug(title) }

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
	branch := BranchName(req.TaskID, req.Title)
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
			if strings.HasSuffix(p, "/") {
				continue // an embedded repository: git lists it as one directory; Drydock neither stages nor inspects it (pin.go)
			}
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
	if err := r.addAll(ctx, w.Path, env); err != nil {
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
	if err := r.addAll(ctx, w.Path, env); err != nil {
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
	if err := r.addAll(ctx, w.Path, env); err != nil {
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
	if err := r.addAll(ctx, w.Path, nil); err != nil {
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
	return r.MergeChecked(ctx, w, target, strategy, msg, MergeCheck{})
}

// MergeCheck is what must still hold once the merge lock is taken: two tasks approved in the same second both passed the checks
// that were made before the lock, and the second would otherwise land on a base the first had moved (audit 2026-10-16, M14).
type MergeCheck struct {
	// Head, when set, is the commit the branch must be at: the one that was reviewed and approved.
	Head string
	// UpToDate requires the branch to contain every commit of the target.
	UpToDate bool
}

// MergeChecked is Merge that first checks, under the merge lock, that the branch is still what was reviewed and that the target
// has not moved past it. A failed check returns *ChangedSinceReviewError or *StaleBaseError and changes nothing.
func (r *Repo) MergeChecked(ctx context.Context, w Worktree, target string, strategy MergeStrategy, msg string, chk MergeCheck) (string, error) {
	defer r.lockMain()()
	if chk.Head != "" {
		now, err := r.Head(ctx, w)
		if err != nil {
			return "", err
		}
		if now != chk.Head {
			return "", &ChangedSinceReviewError{Reviewed: chk.Head, Now: now}
		}
	}
	if chk.UpToDate {
		behind, err := r.Behind(ctx, w, target)
		if err != nil {
			return "", fmt.Errorf("compare with %s: %w", target, err)
		}
		if behind > 0 {
			return "", &StaleBaseError{Target: target, Behind: behind}
		}
	}
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
		// The main working tree was clean when the merge started, so whatever the merge left is put back, whether it stopped on
		// a conflict or for another reason: a half-finished merge there would refuse every merge after it (audit 2026-10-16, L10).
		files, ferr := r.conflictFiles(ctx, r.Root)
		_, _ = r.git(ctx, r.Root, nil, "merge", "--abort")
		_, _ = r.git(ctx, r.Root, nil, "reset", "-q", "--merge")
		if ferr != nil {
			return "", fmt.Errorf("%w (and the files in conflict could not be listed: %v)", mergeErr, ferr)
		}
		if len(files) > 0 {
			return "", &ConflictError{Files: files, Output: mergeErr.Error()}
		}
		return "", mergeErr
	}
	out, err := r.git(ctx, r.Root, nil, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// conflictFiles lists the files in conflict in dir; an error means the list is unknown, not that it is empty.
func (r *Repo) conflictFiles(ctx context.Context, dir string) ([]string, error) {
	out, err := r.git(ctx, dir, nil, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	var f []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			f = append(f, l)
		}
	}
	return f, nil
}

// Rebase rebases the worktree's branch onto onto. On conflict it aborts and returns *ConflictError.
func (r *Repo) Rebase(ctx context.Context, w Worktree, onto string) error {
	_, err := r.git(ctx, w.Path, nil, "rebase", onto)
	if err != nil {
		files, ferr := r.conflictFiles(ctx, w.Path)
		_, _ = r.git(ctx, w.Path, nil, "rebase", "--abort")
		if ferr != nil {
			return fmt.Errorf("%w (and the files in conflict could not be listed: %v)", err, ferr)
		}
		if len(files) > 0 {
			return &ConflictError{Files: files, Output: err.Error()}
		}
		return err
	}
	return nil
}

// DeleteWorktree removes a worktree. It refuses a dirty worktree unless force is true, and a worktree that holds a nested
// repository (its history would go with the folder), as git does; force requires the caller to hold an approval (the caller's
// responsibility; this package only enforces the default).
func (r *Repo) DeleteWorktree(ctx context.Context, w Worktree, force bool) error {
	if _, err := os.Stat(w.Path); os.IsNotExist(err) {
		_, _ = r.git(ctx, r.Root, nil, "worktree", "prune")
		return nil
	}
	if !force {
		st, err := r.Status(ctx, w)
		if err != nil {
			return err
		}
		if st.Dirty() {
			return fmt.Errorf("%w: %d changed file(s)", ErrDirtyWorktree, len(st.Files()))
		}
		nested, err := r.nestedRepos(ctx, w.Path, nil)
		if err != nil {
			return err
		}
		if len(nested) > 0 {
			return fmt.Errorf("%w: it holds a nested repository (%s)", ErrDirtyWorktree, nested[0])
		}
	}
	// Always --force: git's own check for an unclean worktree runs a status that starts from the worktree's .git file, which the
	// agent can rewrite (pin.go). The checks above ask the same through a call that is pinned to the worktree's registered git
	// directory; what git would add (a worktree with submodules) is covered by the nested-repository check. Git still validates
	// the .git file, so it is put back first.
	if err := r.restoreGitFile(w); err != nil {
		return err
	}
	_, err := r.git(ctx, r.Root, nil, "worktree", "remove", "--force", w.Path)
	return err
}

// PruneWorktrees makes git forget the worktrees whose folder is gone (git worktree prune). It changes nothing else.
func (r *Repo) PruneWorktrees(ctx context.Context) error {
	_, err := r.git(ctx, r.Root, nil, "worktree", "prune")
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
	if err := r.addAll(ctx, w.Path, env); err != nil {
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
	curOut, err := r.git(ctx, w.Path, nil, "ls-files", "-c", "-o", "--exclude-standard")
	if err != nil {
		return undo, fmt.Errorf("list the files of the worktree: %w", err)
	}
	for _, f := range strings.Split(curOut, "\n") {
		if f = strings.TrimSpace(f); f != "" && !want[f] {
			if strings.HasSuffix(f, "/") {
				continue // an untracked repository inside the worktree: git lists the folder, and its history is not Drydock's to delete
			}
			// The worktree is the agent's: a folder in it may have been swapped for a link to somewhere else since git listed the
			// file, and a plain remove would then delete that other file. Removal is confined to the worktree (audit 2026-10-16, L10).
			if err := safefs.Remove(w.Path, f); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return undo, fmt.Errorf("remove %s: %w", f, err)
			}
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
	if err := r.addAll(ctx, w.Path, env); err != nil {
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
		files, ferr := r.conflictFiles(ctx, w.Path)
		if ferr != nil {
			return false, nil, fmt.Errorf("%w (and the files in conflict could not be listed: %v)", mergeErr, ferr)
		}
		if len(files) > 0 {
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

// UnresolvedConflicts lists the files of an in-progress merge that still contain conflict markers. An error means the list of
// files in conflict could not be had: that is not the same as none being left.
func (r *Repo) UnresolvedConflicts(ctx context.Context, w Worktree) ([]string, error) {
	files, err := r.conflictFiles(ctx, w.Path)
	if err != nil {
		return nil, err
	}
	var left []string
	for _, f := range files {
		// the worktree is the agent's: a link in it must not make this read a file outside it
		b, err := safefs.ReadFile(w.Path, f, maxConflictFileBytes)
		if errors.Is(err, fs.ErrNotExist) {
			continue // a file the agent deleted counts as resolved
		}
		if err != nil {
			left = append(left, f) // a file that cannot be read cannot be shown to be resolved
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "<<<<<<< ") || strings.HasPrefix(l, ">>>>>>> ") || l == "=======" {
				left = append(left, f)
				break
			}
		}
	}
	return left, nil
}

// maxConflictFileBytes bounds how much of a file in conflict is read to look for markers.
const maxConflictFileBytes = 32 << 20

var mergeLocks sync.Map // repository root -> *sync.Mutex

// lockMain serializes the operations that change the main working tree of one repository (merging a task into the base branch).
// Two approvals at the same moment would otherwise race on git's index lock and one would fail half-way.
func (r *Repo) lockMain() func() {
	v, _ := mergeLocks.LoadOrStore(r.Root, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// PatchID identifies the worktree's own committed change independent of where it sits in history (git patch-id --verbatim of the
// diff against the merge base with ref). Bringing the base branch into the branch cleanly moves the merge base but not this value;
// resolving a conflict, or any edit, changes it, and so does a change of whitespace alone: --stable would ignore it, and in Python,
// YAML or a Makefile moving a line by an indent is a different program (audit 2026-10-16, L15). It is how "the same change that
// was approved" is recognized.
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
	id, err := r.patchID(ctx, w.Path, diff, true)
	if err != nil {
		// a git older than 2.38 has no --verbatim: its stable id plus a digest of the changed lines as written
		return r.patchID(ctx, w.Path, diff, false)
	}
	return id, nil
}

// patchID runs git patch-id over diff. Without verbatim it uses --stable, which strips whitespace, and adds a digest of the
// changed lines so that a whitespace-only difference still changes the id.
func (r *Repo) patchID(ctx context.Context, dir, diff string, verbatim bool) (string, error) {
	ctx2, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	mode := "--stable"
	if verbatim {
		mode = "--verbatim"
	}
	cmd, err := r.command(ctx2, dir, nil, []string{"patch-id", mode})
	if err != nil {
		return "", fmt.Errorf("git patch-id: %w", err)
	}
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
	if verbatim {
		return f[0], nil
	}
	h := sha256.New()
	for _, l := range strings.Split(diff, "\n") {
		if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") {
			h.Write([]byte(l))
			h.Write([]byte{'\n'})
		}
	}
	return f[0] + "+" + hex.EncodeToString(h.Sum(nil))[:16], nil
}

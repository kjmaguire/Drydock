package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/runners"
)

// GitHubStatus says whether the GitHub integration (the user's own `gh`, signed in) can be used.
type GitHubStatus struct {
	Installed bool `json:"installed"`
	LoggedIn  bool `json:"loggedIn"`
	// Unreachable is true when GitHub (or the network) did not answer in time: gh may well be signed in.
	Unreachable bool   `json:"unreachable,omitempty"`
	Login       string `json:"login"`
	Error       string `json:"error,omitempty"`
	// Version is the GitHub CLI's version as `gh --version` prints it ("2.83.1"); empty when it could not be read, or when
	// an API stands in for gh (as tests give the core). The answer is kept for up to a minute (ghVersion), so an updated gh
	// shows on the first check after that.
	Version string `json:"version,omitempty"`
}

// CloneResult is a repository cloned for a new project.
type CloneResult struct {
	Path          string `json:"path"`
	DefaultBranch string `json:"defaultBranch"`
}

// githubCLI is the GitHub API the core talks through, the path of the user's gh it resolved on PATH, and whether GitHub can
// be used at all. The path is "" when a configured API stands in for gh (as tests give the core): there is no gh to ask then.
func (c *Core) githubCLI() (host.API, string, bool) {
	if c.cfg.GitHubAPI != nil {
		return c.cfg.GitHubAPI, "", true
	}
	path, err := exec.LookPath("gh")
	if err != nil {
		return nil, "", false
	}
	return host.GhCLI{}, path, true
}

// githubAPI is githubCLI for the callers that only need the API.
func (c *Core) githubAPI() (host.API, bool) {
	api, _, ok := c.githubCLI()
	return api, ok
}

// githubStatusTimeout is how long the account check waits for GitHub before saying it did not answer.
var githubStatusTimeout = 20 * time.Second

// GitHubAccountStatus reports whether `gh` is installed and signed in, and its version. It never reads or stores a token.
func (c *Core) GitHubAccountStatus(ctx context.Context) GitHubStatus {
	api, ghPath, ok := c.githubCLI()
	if !ok {
		return GitHubStatus{Error: "the GitHub CLI (gh) is not installed: install it from https://cli.github.com and run `gh auth login`"}
	}
	vctx, cancel := context.WithTimeout(ctx, githubStatusTimeout)
	defer cancel()
	who, err := host.Viewer(vctx, api)
	// the version is read after the sign-in check, in turn, within the same budget: the answer is kept, so this costs one short
	// process now and then, and a check whose budget GitHub used up still reports the kept version without waiting on gh
	version := ""
	if ghPath != "" {
		version = ghVersion(vctx, ghPath)
	}
	if err != nil {
		if errors.Is(err, host.ErrAuth) {
			return GitHubStatus{Installed: true, Version: version, Error: "gh is not signed in: run `gh auth login`"}
		}
		if vctx.Err() != nil {
			return GitHubStatus{Installed: true, Version: version, Unreachable: true, Error: "GitHub did not answer within 20 seconds (the network or GitHub may be down); try again later"}
		}
		return GitHubStatus{Installed: true, Version: version, Error: err.Error()}
	}
	return GitHubStatus{Installed: true, LoggedIn: true, Login: who.Login, Version: version}
}

// ghVersionTTL is how long a kept version is trusted while the file at the gh path looks the same. A launcher or shim (scoop's
// shims\gh.exe, chocolatey's bin\gh.exe, a wrapper .cmd) does not change when the gh behind it does, so the version is asked
// again after a minute even when the file looks the same.
const ghVersionTTL = time.Minute

// ghVersionNow is the clock a kept version's age is measured by (a test moves it).
var ghVersionNow = time.Now

// ghVersion is the version of the GitHub CLI at path (`gh --version` only prints it, without the network), or "" when it does
// not say. The answer is kept and reused while both hold: the file at path is unchanged (same path, size and modification
// time) and the answer is younger than ghVersionTTL. A changed file or an old answer asks gh again.
func ghVersion(ctx context.Context, path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	key := ghBinary{path: path, size: fi.Size(), mod: fi.ModTime().UnixNano()}
	ghVersions.Lock()
	defer ghVersions.Unlock()
	now := ghVersionNow()
	if ghVersions.key == key && ghVersions.v != "" && now.Sub(ghVersions.at) < ghVersionTTL {
		return ghVersions.v
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1")
	proc.Harden(cmd)
	out := &proc.CapWriter{Max: 4096}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return "" // not kept: the next check asks again
	}
	v := parseGhVersion(out.String())
	ghVersions.key, ghVersions.v, ghVersions.at = key, v, now
	return v
}

// ghBinary is which gh a kept version belongs to.
type ghBinary struct {
	path string
	size int64
	mod  int64 // modification time, Unix nanoseconds
}

// ghVersions is the last version read, the binary it was read from and when (the lock also keeps two checks from running gh
// twice).
var ghVersions struct {
	sync.Mutex
	key ghBinary
	v   string
	at  time.Time
}

// ghVersionRe is the first line of `gh --version`: "gh version 2.83.1 (2025-11-13)".
var ghVersionRe = regexp.MustCompile(`^gh version (\d+\.\d+\.\d+[0-9A-Za-z.+-]*)`)

func parseGhVersion(s string) string {
	if m := ghVersionRe.FindStringSubmatch(strings.TrimSpace(s)); m != nil {
		return m[1]
	}
	return ""
}

// GitHubOwners lists the signed-in account followed by its organizations.
func (c *Core) GitHubOwners(ctx context.Context) ([]string, error) {
	api, ok := c.githubAPI()
	if !ok {
		return nil, errors.New("the GitHub CLI (gh) is not installed")
	}
	who, err := host.Viewer(ctx, api)
	if err != nil {
		return nil, err
	}
	orgs, err := host.Orgs(ctx, api)
	if err != nil {
		return nil, err
	}
	return append([]string{who.Login}, orgs...), nil
}

// GitHubRepos lists the repositories the signed-in user can reach. Names and descriptions are outside text: shown, never
// given to an agent as instructions.
func (c *Core) GitHubRepos(ctx context.Context, owner, query string) ([]host.RepoSummary, error) {
	api, ok := c.githubAPI()
	if !ok {
		return nil, errors.New("the GitHub CLI (gh) is not installed")
	}
	return host.ListRepos(ctx, api, owner, query)
}

// DefaultCloneDir is where repositories are cloned unless the human picks another folder (Visual Studio's default location).
func DefaultCloneDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "source", "repos")
}

// settingCodeDir is the folder the person's last new local project was made in (the parent of its folder): New folder starts
// there next time.
const settingCodeDir = "project.codeDir"

// CodeDir is where a new project's folder goes unless the person types another: the parent of the last one made in Drydock,
// while it is a full path, else (on Windows) the first Dev Drive's src folder ("D:\\src", a trusted one first), else
// DefaultCloneDir.
func (c *Core) CodeDir(ctx context.Context) string {
	var v string
	_ = c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingCodeDir).Scan(&v)
	return codeDirFrom(v, c.devDrives(ctx).Drives, runtime.GOOS == "windows", DefaultCloneDir())
}

// codeDirFrom is CodeDir's rule: the saved folder while it is a full path, else on Windows a Dev Drive's src folder (a trusted
// drive first), else fallback. A drive letter is no folder anywhere else, so off Windows the drives are not used.
func codeDirFrom(saved string, drives []runners.Drive, windows bool, fallback string) string {
	if saved != "" && filepath.IsAbs(saved) {
		return saved
	}
	if windows {
		for _, trusted := range []bool{true, false} {
			for _, d := range drives {
				if d.Trusted == trusted && d.Letter != "" {
					return strings.TrimRight(d.Letter, `\/`) + `\src`
				}
			}
		}
	}
	return fallback
}

// rememberCodeDir keeps the parent of a new project's folder as the next New folder's starting place.
func (c *Core) rememberCodeDir(ctx context.Context, folder string) {
	folder = cleanFolderArg(folder)
	if folder == "" || !filepath.IsAbs(folder) {
		return
	}
	parent := filepath.Dir(filepath.Clean(folder))
	if parent == folder {
		return
	}
	if err := c.setSetting(ctx, settingCodeDir, parent); err != nil {
		c.logf("remembering the code folder: %v", err)
	}
}

// settingCloneDir is the folder the person last cloned a repository into (decision P143): the next clone starts there.
const settingCloneDir = "github.lastCloneDir"

// GitHubCloneDir is the folder a clone starts in: the one the person last cloned into, while it is a full path, else
// DefaultCloneDir.
func (c *Core) GitHubCloneDir(ctx context.Context) string {
	var v string
	if err := c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingCloneDir).Scan(&v); err == nil && filepath.IsAbs(v) {
		return v
	}
	return DefaultCloneDir()
}

// cloneTarget is the repository (owner/name), the folder to clone into (DefaultCloneDir when empty) and the clone's own
// folder inside it, or why they are not usable. Nothing is made.
func cloneTarget(fullName, parentDir string) (repo, parent, dest string, err error) {
	repo = host.FullNameOf(fullName)
	if !host.ValidFullName(repo) {
		return "", "", "", fmt.Errorf("%q is not a GitHub repository name (owner/name)", repo)
	}
	if strings.TrimSpace(parentDir) == "" {
		parentDir = DefaultCloneDir()
	}
	parent = filepath.Clean(strings.TrimSpace(parentDir))
	if !filepath.IsAbs(parent) {
		return "", "", "", fmt.Errorf("choose a full folder path to clone into, not %q", parent)
	}
	return repo, parent, filepath.Join(parent, repo[strings.IndexByte(repo, '/')+1:]), nil
}

// GitHubClone clones owner/name into parentDir/name with the user's own `gh` (their sign-in, nothing stored by Drydock) and
// returns the new repository's path, ready for project.create or project.attachRepo.
func (c *Core) GitHubClone(ctx context.Context, fullName, parentDir string) (CloneResult, error) {
	if err := c.RequireLicensed(ctx, "create_project"); err != nil {
		return CloneResult{}, err
	}
	fullName, parentDir, dest, err := cloneTarget(fullName, parentDir)
	if err != nil {
		return CloneResult{}, err
	}
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return CloneResult{}, fmt.Errorf("cannot create %s: %w", parentDir, err)
	}
	// the run is registered before the folder is looked at: a second clone of the same repository, or into the same folder,
	// is refused here and never reaches the clean-up below, which would remove the folder the first clone is writing
	ctx, cancelRun := context.WithCancelCause(ctx)
	defer cancelRun(nil)
	remove, busy := c.clones.add(normalizeRepo(fullName), cleanCloneDest(dest), cancelRun)
	if busy != nil {
		return CloneResult{}, busy
	}
	defer remove()
	if ents, err := os.ReadDir(dest); err == nil && len(ents) > 0 {
		return CloneResult{}, fmt.Errorf("%s already exists and is not empty: attach it as a local repository instead, or choose another folder", dest)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	var exe string
	var args []string
	if c.cfg.GitHubCloneURL != nil {
		exe, args = "git", []string{"clone", "--progress", c.cfg.GitHubCloneURL(fullName), dest}
	} else {
		if _, err := exec.LookPath("gh"); err != nil {
			return CloneResult{}, errors.New("the GitHub CLI (gh) is not installed: install it from https://cli.github.com and run `gh auth login`")
		}
		exe, args = "gh", []string{"repo", "clone", fullName, dest, "--", "--progress"}
	}
	msg, err := c.runClone(ctx, fullName, exe, args)
	if err != nil {
		_ = os.RemoveAll(dest) // a half-made clone would block the next try
		if errors.Is(err, errCloneCancelled) {
			c.publish(context.WithoutCancel(ctx), "github.clone_cancelled", "", map[string]any{"repo": fullName})
			return CloneResult{}, fmt.Errorf("the clone of %s was cancelled: nothing was kept", fullName)
		}
		msg = strings.TrimSpace(c.Scanner.Redact(msg))
		if len(msg) > 600 {
			msg = msg[:600] + "..."
		}
		return CloneResult{}, fmt.Errorf("cloning %s failed: %v: %s", fullName, err, msg)
	}
	root, branch, err := c.openProjectRepo(ctx, dest)
	if err != nil {
		return CloneResult{}, err
	}
	if err := c.setSetting(ctx, settingCloneDir, parentDir); err != nil {
		c.logf("remembering the clone folder: %v", err)
	}
	c.publish(ctx, "github.cloned", "", map[string]any{"repo": fullName})
	return CloneResult{Path: root, DefaultBranch: branch}, nil
}

// ghStatusTTL is how long a github.status answer is reused (F4): the clone picker, the GitHub page and the pull-request page
// each ask on open, and gh answers in about a second. The sign-in's end (github.sign_in_finished) drops it, and Check again
// asks with refresh.
const ghStatusTTL = 10 * time.Second

// ghStatusCache is the last github.status answer and when it was read. drop() moves gen on: a read that started before it
// (a page's read while gh auth login was finishing) may answer its own caller, but its answer is not kept over the newer one.
type ghStatusCache struct {
	mu   sync.Mutex
	st   GitHubStatus
	at   time.Time // when the kept answer was stored; zero: none
	from time.Time // when the read that gave the kept answer started
	gen  uint64
}

func (k *ghStatusCache) drop() {
	k.mu.Lock()
	k.at = time.Time{}
	k.gen++
	k.mu.Unlock()
}

// get answers from the kept answer while it is fresh (unless refresh), else from read, keeping read's answer when keep says
// so, no drop() happened since the read started, and no read that started later has been kept.
func (k *ghStatusCache) get(refresh bool, read func() (st GitHubStatus, keep bool)) GitHubStatus {
	k.mu.Lock()
	if !refresh && !k.at.IsZero() && time.Since(k.at) < ghStatusTTL {
		st := k.st
		k.mu.Unlock()
		return st
	}
	gen, start := k.gen, time.Now()
	k.mu.Unlock()
	st, keep := read()
	if keep {
		k.mu.Lock()
		if k.gen == gen && (k.at.IsZero() || !start.Before(k.from)) {
			k.st, k.at, k.from = st, time.Now(), start
		}
		k.mu.Unlock()
	}
	return st
}

// CachedGitHubStatus is GitHubAccountStatus, reused for ghStatusTTL unless refresh asks gh again. An answer GitHub did not give
// in time (unreachable) is not kept.
func (c *Core) CachedGitHubStatus(ctx context.Context, refresh bool) GitHubStatus {
	return c.ghStatus.get(refresh, func() (GitHubStatus, bool) {
		st := c.GitHubAccountStatus(ctx)
		return st, !st.Unreachable && ctx.Err() == nil
	})
}

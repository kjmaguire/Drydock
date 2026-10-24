package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/proc"
)

// GitHubStatus says whether the GitHub integration (the user's own `gh`, signed in) can be used.
type GitHubStatus struct {
	Installed bool `json:"installed"`
	LoggedIn  bool `json:"loggedIn"`
	// Unreachable is true when GitHub (or the network) did not answer in time: gh may well be signed in.
	Unreachable bool   `json:"unreachable,omitempty"`
	Login       string `json:"login"`
	Error       string `json:"error,omitempty"`
	// Version is the GitHub CLI's version as `gh --version` prints it ("2.83.1"); empty when it could not be read.
	Version string `json:"version,omitempty"`
}

// CloneResult is a repository cloned for a new project.
type CloneResult struct {
	Path          string `json:"path"`
	DefaultBranch string `json:"defaultBranch"`
}

func (c *Core) githubAPI() (host.API, bool) {
	if c.cfg.GitHubAPI != nil {
		return c.cfg.GitHubAPI, true
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, false
	}
	return host.GhCLI{}, true
}

// githubStatusTimeout is how long the account check waits for GitHub before saying it did not answer.
var githubStatusTimeout = 20 * time.Second

// GitHubAccountStatus reports whether `gh` is installed and signed in, and its version. It never reads or stores a token.
func (c *Core) GitHubAccountStatus(ctx context.Context) GitHubStatus {
	api, ok := c.githubAPI()
	if !ok {
		return GitHubStatus{Error: "the GitHub CLI (gh) is not installed: install it from https://cli.github.com and run `gh auth login`"}
	}
	// the version is read beside the sign-in check, not before it (and only once per gh binary: ghVersion keeps it)
	versionCh := make(chan string, 1)
	if c.cfg.GitHubAPI == nil { // the user's own gh (not an API a test gave the core)
		go func() { versionCh <- ghVersion(ctx) }()
	} else {
		versionCh <- ""
	}
	vctx, cancel := context.WithTimeout(ctx, githubStatusTimeout)
	defer cancel()
	who, err := host.Viewer(vctx, api)
	version := <-versionCh
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

// ghVersion is the version of the GitHub CLI on PATH (`gh --version` only prints it, without the network), or "" when it does
// not say. It is read once per binary: the answer is kept until gh at that path changes (another path, size or time).
func ghVersion(ctx context.Context) string {
	path, err := exec.LookPath("gh")
	if err != nil {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	key := ghBinary{path: path, size: fi.Size(), mod: fi.ModTime().UnixNano()}
	ghVersions.Lock()
	defer ghVersions.Unlock()
	if ghVersions.key == key && ghVersions.v != "" {
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
	ghVersions.key, ghVersions.v = key, v
	return v
}

// ghBinary is which gh a kept version belongs to.
type ghBinary struct {
	path string
	size int64
	mod  int64 // modification time, Unix nanoseconds
}

// ghVersions is the last version read, and the binary it was read from (the lock also keeps two checks from running gh twice).
var ghVersions struct {
	sync.Mutex
	key ghBinary
	v   string
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

// GitHubClone clones owner/name into parentDir/name with the user's own `gh` (their sign-in, nothing stored by Drydock) and
// returns the new repository's path, ready for project.create or project.attachRepo.
func (c *Core) GitHubClone(ctx context.Context, fullName, parentDir string) (CloneResult, error) {
	if err := c.RequireLicensed(ctx, "create_project"); err != nil {
		return CloneResult{}, err
	}
	fullName = strings.TrimSpace(fullName)
	if !host.ValidFullName(fullName) {
		return CloneResult{}, fmt.Errorf("%q is not a GitHub repository name (owner/name)", fullName)
	}
	if strings.TrimSpace(parentDir) == "" {
		parentDir = DefaultCloneDir()
	}
	parentDir = filepath.Clean(strings.TrimSpace(parentDir))
	if !filepath.IsAbs(parentDir) {
		return CloneResult{}, fmt.Errorf("choose a full folder path to clone into, not %q", parentDir)
	}
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return CloneResult{}, fmt.Errorf("cannot create %s: %w", parentDir, err)
	}
	dest := filepath.Join(parentDir, fullName[strings.IndexByte(fullName, '/')+1:])
	if ents, err := os.ReadDir(dest); err == nil && len(ents) > 0 {
		return CloneResult{}, fmt.Errorf("%s already exists and is not empty: attach it as a local repository instead, or choose another folder", dest)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	var cmd *exec.Cmd
	if c.cfg.GitHubCloneURL != nil {
		cmd = exec.CommandContext(ctx, "git", "clone", "--quiet", c.cfg.GitHubCloneURL(fullName), dest)
	} else {
		if _, err := exec.LookPath("gh"); err != nil {
			return CloneResult{}, errors.New("the GitHub CLI (gh) is not installed: install it from https://cli.github.com and run `gh auth login`")
		}
		cmd = exec.CommandContext(ctx, "gh", "repo", "clone", fullName, dest, "--", "--quiet")
	}
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0", "NO_COLOR=1")
	proc.Harden(cmd)
	stderr := &proc.CapWriter{Max: 1 << 20}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(dest) // a half-made clone would block the next try
		msg := strings.TrimSpace(c.Scanner.Redact(stderr.String()))
		if len(msg) > 600 {
			msg = msg[:600] + "..."
		}
		return CloneResult{}, fmt.Errorf("cloning %s failed: %v: %s", fullName, err, msg)
	}
	root, branch, err := c.openProjectRepo(ctx, dest)
	if err != nil {
		return CloneResult{}, err
	}
	c.publish(ctx, "github.cloned", "", map[string]any{"repo": fullName})
	return CloneResult{Path: root, DefaultBranch: branch}, nil
}

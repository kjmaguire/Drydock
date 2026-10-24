package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/git/host"
)

// The Repositories table of the GitHub page (decision P98): one row per project with its GitHub repository, the CI state of
// the default branch's latest commit and how many pull requests are open. GitHub is read through the user's own gh
// (githubAPI: GhCLI, hardened, no prompts, a timeout per call; Drydock reads no token), one GraphQL request per repository per
// refresh, kept for repoOverviewTTL. A failure is that row's, never the whole answer's. Without gh every project with a GitHub
// remote is still listed, marked as needing gh. Nothing is written and no event is published.

const (
	// repoOverviewTTL is how long a repository's answer is reused before GitHub is asked again (refresh skips it).
	repoOverviewTTL = 60 * time.Second
	// repoOverviewTimeout bounds one repository's read; the others go on.
	repoOverviewTimeout = 20 * time.Second
	// repoOverviewParallel is how many repositories are read at once.
	repoOverviewParallel = 4
	// repoRemoteParallel is how many projects' origin remotes are read at once (a git process each).
	repoRemoteParallel = 8
)

// The states of a ProjectRepoRow.
const (
	RepoStateGitHub  = "github"   // read from GitHub: CI and open pull requests are set
	RepoStateLocal   = "local"    // a repository whose origin is not on github.com (or that has no origin)
	RepoStateNoRepo  = "no_repo"  // the project has no repository attached
	RepoStateNeedsGh = "needs_gh" // a GitHub remote, but the GitHub CLI is not installed
	RepoStateError   = "error"    // GitHub, or the repository folder, could not be read: Error says why (Repo is empty for the folder)
)

// ProjectRepoRow is one project in the GitHub page's Repositories table.
type ProjectRepoRow struct {
	ProjectID string `json:"projectId"`
	Project   string `json:"project"`
	State     string `json:"state"`
	// Repo is owner/name of the origin remote, when it is on github.com.
	Repo string `json:"repo,omitempty"`
	// URL is the repository on github.com (as GitHub reported it, else made from Repo).
	URL           string `json:"url,omitempty"`
	Private       bool   `json:"private,omitempty"`
	DefaultBranch string `json:"defaultBranch,omitempty"`
	// CI is passing, failing, running or none (host.CI*), for the default branch's latest commit; set when State is github.
	CI      string `json:"ci,omitempty"`
	OpenPRs int    `json:"openPullRequests"`
	Error   string `json:"error,omitempty"`
	// CheckedAt is when GitHub answered for this repository (Unix milliseconds); 0 when it was not asked.
	CheckedAt int64 `json:"checkedAt,omitempty"`
}

// ProjectRepos is the whole table.
type ProjectRepos struct {
	GhInstalled bool             `json:"ghInstalled"`
	Rows        []ProjectRepoRow `json:"rows"`
}

type repoOverviewEntry struct {
	at time.Time
	ov host.RepoOverview
}

type repoOverviews struct {
	c     *Core
	mu    sync.Mutex
	cache map[string]repoOverviewEntry // owner/name -> the last answer
	// remoteCache is each project's origin remote as last read (project id and repository folder -> it); a failed read is
	// not kept, and a project given another folder is read afresh
	remoteCache map[string]projectRemote
	now         func() time.Time
}

func init() {
	RegisterExtension(Extension{Name: "github-repos", Start: func(_ context.Context, c *Core) (map[string]Method, func(), error) {
		r := &repoOverviews{c: c, cache: map[string]repoOverviewEntry{}, remoteCache: map[string]projectRemote{}, now: time.Now}
		return map[string]Method{
			// github.projectRepos: the Repositories table (refresh: ask GitHub again even when an answer is fresh)
			"github.projectRepos": bind(func(ctx context.Context, p struct {
				Refresh bool `json:"refresh"`
			}) (any, error) {
				return r.list(ctx, p.Refresh)
			}),
		}, nil, nil
	}})
}

func (r *repoOverviews) list(ctx context.Context, refresh bool) (ProjectRepos, error) {
	c := r.c
	projects, err := c.Projects(ctx)
	if err != nil {
		return ProjectRepos{}, err
	}
	api, ghOK := c.githubAPI()
	out := ProjectRepos{GhInstalled: ghOK, Rows: make([]ProjectRepoRow, 0, len(projects))}
	want := map[string][]int{} // owner/name -> the rows that show it (one read for a repository two projects share)
	var order []string
	for i, rem := range r.remotes(ctx, projects, refresh) {
		p := projects[i]
		row := ProjectRepoRow{ProjectID: p.ID, Project: p.Name, DefaultBranch: p.DefaultBranch}
		switch rem.state {
		case RepoStateGitHub:
			row.Repo = rem.full
			row.URL = "https://github.com/" + row.Repo
			if !ghOK {
				row.State = RepoStateNeedsGh
				break
			}
			if _, seen := want[row.Repo]; !seen {
				order = append(order, row.Repo)
			}
			want[row.Repo] = append(want[row.Repo], len(out.Rows))
		default:
			row.State, row.Error = rem.state, rem.msg
		}
		out.Rows = append(out.Rows, row)
	}
	if len(order) == 0 {
		return out, nil
	}
	type answer struct {
		ov  host.RepoOverview
		at  time.Time
		err error
	}
	answers := make([]answer, len(order))
	sem := make(chan struct{}, repoOverviewParallel)
	var wg sync.WaitGroup
	for i, full := range order {
		if e, ok := r.fresh(full, refresh); ok {
			answers[i] = answer{ov: e.ov, at: e.at}
			continue
		}
		wg.Add(1)
		go func(i int, full string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			owner, name, _ := strings.Cut(full, "/")
			rctx, cancel := context.WithTimeout(ctx, repoOverviewTimeout)
			defer cancel()
			ov, err := host.Overview(rctx, api, owner, name)
			if err != nil && rctx.Err() != nil && ctx.Err() == nil {
				err = fmt.Errorf("GitHub did not answer within %d seconds", int(repoOverviewTimeout/time.Second))
			}
			at := r.now()
			answers[i] = answer{ov: ov, at: at, err: err}
			if err == nil {
				r.mu.Lock()
				r.cache[full] = repoOverviewEntry{at: at, ov: ov}
				r.mu.Unlock()
			}
		}(i, full)
	}
	wg.Wait()
	for i, full := range order {
		a := answers[i]
		for _, idx := range want[full] {
			row := &out.Rows[idx]
			if a.err != nil {
				row.State, row.Error = RepoStateError, r.say(full, a.err)
				continue
			}
			row.State, row.CI, row.OpenPRs, row.Private, row.CheckedAt = RepoStateGitHub, a.ov.CI, a.ov.OpenPRs, a.ov.Private, a.at.UnixMilli()
			if a.ov.DefaultBranch != "" {
				row.DefaultBranch = a.ov.DefaultBranch
			}
			if strings.HasPrefix(a.ov.URL, "https://github.com/") {
				row.URL = a.ov.URL
			}
		}
	}
	return out, nil
}

// fresh is the cached answer for a repository, unless it is older than repoOverviewTTL or a refresh was asked for.
func (r *repoOverviews) fresh(full string, refresh bool) (repoOverviewEntry, bool) {
	if refresh {
		return repoOverviewEntry{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.cache[full]
	if !ok || r.now().Sub(e.at) > repoOverviewTTL {
		return repoOverviewEntry{}, false
	}
	return e, true
}

// projectRemote is where a project's repository lives: owner/name on github.com, or the state that says why there is none.
type projectRemote struct {
	at    time.Time
	state string
	full  string
	msg   string
}

// remotes reads every project's origin remote (git, on this PC; GitHub is not asked), repoRemoteParallel at once, each kept for
// repoOverviewTTL like GitHub's answers (refresh reads them again), so a table whose answers are all fresh runs no process.
func (r *repoOverviews) remotes(ctx context.Context, projects []Project, refresh bool) []projectRemote {
	out := make([]projectRemote, len(projects))
	sem := make(chan struct{}, repoRemoteParallel)
	var wg sync.WaitGroup
	for i, p := range projects {
		if !p.HasRepo {
			out[i] = projectRemote{state: RepoStateNoRepo}
			continue
		}
		r.mu.Lock()
		e, ok := r.remoteCache[p.ID+"\x00"+p.RepoPath]
		r.mu.Unlock()
		if ok && !refresh && r.now().Sub(e.at) <= repoOverviewTTL {
			out[i] = e
			continue
		}
		wg.Add(1)
		go func(i int, p Project) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rem := r.remote(ctx, p)
			out[i] = rem
			if rem.state != RepoStateError {
				r.mu.Lock()
				r.remoteCache[p.ID+"\x00"+p.RepoPath] = rem
				r.mu.Unlock()
			}
		}(i, p)
	}
	wg.Wait()
	return out
}

// remote reads one project's origin remote. No origin, or one not on github.com, is a local repository; a folder git cannot
// open or read is an error, never "local".
func (r *repoOverviews) remote(ctx context.Context, p Project) projectRemote {
	at := r.now()
	repo, err := r.c.repo(ctx, p.ID)
	if err != nil {
		return projectRemote{at: at, state: RepoStateError, msg: "The repository folder could not be opened: " + r.clip(err.Error())}
	}
	url, err := repo.RemoteURL(ctx, "origin")
	switch {
	case errors.Is(err, git.ErrNoRemote):
		return projectRemote{at: at, state: RepoStateLocal}
	case err != nil:
		return projectRemote{at: at, state: RepoStateError, msg: "git could not read the repository: " + r.clip(err.Error())}
	}
	owner, name, ok := parseGitHubRemote(url)
	if !ok {
		return projectRemote{at: at, state: RepoStateLocal}
	}
	return projectRemote{at: at, state: RepoStateGitHub, full: owner + "/" + name}
}

// say words a failed read for the table: what to do about it when Drydock knows, else gh's or GitHub's own words (redacted).
func (r *repoOverviews) say(full string, err error) string {
	switch {
	case errors.Is(err, host.ErrAuth):
		return "gh is not signed in: run `gh auth login`"
	case errors.Is(err, host.ErrNotFound):
		return fmt.Sprintf("GitHub shows no repository %s to your gh sign-in", full)
	case errors.Is(err, host.ErrRateLimited):
		return "GitHub's rate limit was reached: try again later"
	}
	return r.clip(err.Error())
}

func (r *repoOverviews) clip(s string) string {
	if r.c.Scanner != nil {
		s = r.c.Scanner.Redact(s)
	}
	s = strings.TrimSpace(s)
	if rs := []rune(s); len(rs) > 300 {
		s = string(rs[:300]) + "..."
	}
	return s
}

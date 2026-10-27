package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/safe"
)

// The Repositories table of the GitHub page (decision P98): one row per project with its GitHub repository, the CI state of
// the default branch's latest commit and how many pull requests are open. GitHub is read through the user's own gh
// (githubAPI: GhCLI, hardened, no prompts, a timeout per call; Drydock reads no token), one GraphQL request per repository per
// refresh, kept for repoOverviewTTL. A failure is that row's, never the whole answer's, and the row says which kind it is: a
// repository GitHub could not be read for (error, in gh's or GitHub's words), a folder git could not read (unreadable, in git's
// words), or a repository GitHub answered only in part (github, with what it refused in Incomplete). Without gh every project
// with a GitHub remote is still listed, marked as needing gh. Nothing is written and no event is published.

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

// The states of a ProjectRepoRow. The page draws every cell from the state and never infers it from which fields are set.
const (
	RepoStateGitHub  = "github"   // read from GitHub: CI and open pull requests are set (Incomplete names what GitHub refused, if anything)
	RepoStateLocal   = "local"    // a repository whose origin is not on github.com (or that has no origin)
	RepoStateNoRepo  = "no_repo"  // the project has no repository attached
	RepoStateNeedsGh = "needs_gh" // a GitHub remote, but the GitHub CLI is not installed
	RepoStateError   = "error"    // GitHub could not be read for this repository: Repo is set and Error says why, in gh's or GitHub's words
	// RepoStateUnreadable is a repository folder, or git in it, that could not be read: Repo and URL are empty and Error carries
	// git's or the OS's own words, which tell whether the folder is gone ("not a git repository: C:\src\moved") or its
	// configuration is broken ("bad config line 1 in file .git/config").
	RepoStateUnreadable = "unreadable"
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
	// Error is why the row could not be read (error and unreadable rows), shown under the page's state word ("Could not read",
	// "Not readable"): the other program's own words for what Drydock cannot explain ("gh: HTTP 502: Bad Gateway", git's "not
	// a git repository: C:\src\moved"), and Drydock's own line for the cases it knows (say: not signed in, a repository GitHub
	// does not show, the rate limit, no answer in time; a repository inside a WSL distro that is not available here).
	Error string `json:"error,omitempty"`
	// Incomplete is what GitHub refused in an otherwise answered repository (host.RepoOverview.Incomplete, cached answers
	// included): "statusCheckRollup: Resource not accessible by personal access token", several joined by "; ". Only a github row
	// has it, and only when something was refused; the CI or pull-request count it names is then not an answer.
	Incomplete string `json:"incomplete,omitempty"`
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

func (e repoOverviewEntry) when() time.Time { return e.at }

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
		if e, ok := fresh(r, r.cache, full, refresh); ok {
			answers[i] = answer{ov: e.ov, at: e.at}
			continue
		}
		wg.Add(1)
		// what a reading that panicked leaves (safe.Go keeps the panic from ending the app)
		answers[i] = answer{err: errors.New("Drydock could not read this repository's overview")}
		safe.Go("core.repoOverviews", func() {
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
		})
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
			// GitHub's words go through the one redact-and-clip rule, as on the error path
			row.State, row.CI, row.OpenPRs, row.Private, row.Incomplete, row.CheckedAt = RepoStateGitHub, a.ov.CI, a.ov.OpenPRs, a.ov.Private, r.clip(a.ov.Incomplete), a.at.UnixMilli()
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

// aged is a kept answer that knows when it was read: GitHub's answer for a repository, or a project's origin remote.
type aged interface{ when() time.Time }

// fresh is the answer kept under key in m, unless a refresh was asked for or it is older than repoOverviewTTL: the one rule
// for GitHub's answers and the origin remotes alike.
func fresh[T aged](r *repoOverviews, m map[string]T, key string, refresh bool) (T, bool) {
	var none T
	if refresh {
		return none, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := m[key]
	if !ok || r.now().Sub(e.when()) > repoOverviewTTL {
		return none, false
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

func (p projectRemote) when() time.Time { return p.at }

// remotes reads every project's origin remote (git, on this PC; GitHub is not asked), repoRemoteParallel at once, each kept for
// repoOverviewTTL like GitHub's answers (refresh reads them again), so a table whose answers are all fresh runs no process. A
// folder that could not be read is not kept, so it is read again next time, like a failed GitHub read.
func (r *repoOverviews) remotes(ctx context.Context, projects []Project, refresh bool) []projectRemote {
	out := make([]projectRemote, len(projects))
	sem := make(chan struct{}, repoRemoteParallel)
	var wg sync.WaitGroup
	for i, p := range projects {
		if !p.HasRepo {
			out[i] = projectRemote{state: RepoStateNoRepo}
			continue
		}
		key := p.ID + "\x00" + p.RepoPath
		if e, ok := fresh(r, r.remoteCache, key, refresh); ok {
			out[i] = e
			continue
		}
		wg.Add(1)
		out[i] = projectRemote{state: RepoStateUnreadable} // what a reading that panicked leaves
		safe.Go("core.projectRemotes", func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rem := r.remote(ctx, p)
			out[i] = rem
			if rem.state != RepoStateUnreadable {
				r.mu.Lock()
				r.remoteCache[key] = rem
				r.mu.Unlock()
			}
		})
	}
	wg.Wait()
	return out
}

// remote reads one project's origin remote. No origin, or one not on github.com, is a local repository; a folder git cannot
// open or read is unreadable, never "local", and the row carries git's own words (redacted and clipped, nothing of Drydock's
// in front: the page puts "Not readable" before them), which say whether the folder is gone or its configuration is broken.
func (r *repoOverviews) remote(ctx context.Context, p Project) projectRemote {
	at := r.now()
	repo, err := r.c.repo(ctx, p.ID)
	if err != nil {
		return projectRemote{at: at, state: RepoStateUnreadable, msg: r.clip(err.Error())}
	}
	url, err := repo.RemoteURL(ctx, "origin")
	switch {
	case errors.Is(err, git.ErrNoRemote):
		return projectRemote{at: at, state: RepoStateLocal}
	case err != nil:
		// a folder gone since the repository was opened (moved, deleted): git cannot even start there, and its words then blame
		// git itself (chdir, fork/exec) rather than the folder, so say what happened in the words git.Open uses for it
		if _, statErr := os.Stat(p.RepoPath); errors.Is(statErr, fs.ErrNotExist) {
			return projectRemote{at: at, state: RepoStateUnreadable, msg: r.clip(fmt.Errorf("%w: %s", git.ErrNotARepo, p.RepoPath).Error())}
		}
		return projectRemote{at: at, state: RepoStateUnreadable, msg: r.clip(err.Error())}
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

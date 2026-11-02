package core

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/safe"
)

// Keeping a project's folder in step with its remote (decision P143). Sync now (repo.sync) fetches the project's origin remote
// and fast-forwards the checked-out default branch of the project's folder when that changes nothing but the branch (internal/git
// sync.go says when); the background check (on when a project is opened and every 15 minutes while the app runs, unless the person
// turned it off) only fetches, so the behind count stays fresh and no file ever changes by itself. Nothing merges, rebases,
// resets, forces or pushes, and no task worktree or branch is touched. The person's own git login reaches the remote (a call that
// talks to a remote keeps the environment that says how); an error is scrubbed before it is kept or shown; the remote's address is
// never sent to the page, logged or stored (it can carry a token).

// syncRemote is the remote a project syncs with.
const syncRemote = "origin"

// settingSyncAuto is the setting that turns the background fetch off ("0"); on when unset.
const settingSyncAuto = "reposync.auto"

// The background check's period, and how old a project's last check may be when it is opened before it is checked again.
var (
	repoSyncEvery    = 15 * time.Minute
	repoSyncOnOpen   = 2 * time.Minute
	repoSyncFirstRun = 90 * time.Second
)

var urlUserinfo = regexp.MustCompile(`://[^/@\s]+@`)

// RepoSync is where a project's folder stands against its remote.
type RepoSync struct {
	ProjectID string `json:"projectId"`
	// Remote is "origin" when the repository has one; "" when there is nothing to sync with (no repository, no origin).
	Remote string `json:"remote"`
	// GitHub is true when the remote is on github.com.
	GitHub bool `json:"github"`
	// Branch is the project's default branch; Current the branch checked out in its folder.
	Branch  string `json:"branch"`
	Current string `json:"current"`
	// Tracked is false while the remote's copy of Branch is not known (never fetched, or the remote has no such branch).
	Tracked bool `json:"tracked"`
	Ahead   int  `json:"ahead"`
	Behind  int  `json:"behind"`
	// CheckedAt is when the remote was last fetched since the app started (ms); 0: not checked yet.
	CheckedAt int64 `json:"checkedAt"`
	Checking  bool  `json:"checking"`
	// Outcome is what the last Sync now did (git.SyncOutcome: updated, up_to_date, ahead, local_changes, diverged,
	// other_branch, no_remote_branch); "" after a background check alone.
	Outcome string `json:"outcome,omitempty"`
	// Error is why the last fetch or Sync now failed, scrubbed; "" when it worked.
	Error string `json:"error,omitempty"`
	// ErrorKind says which step Error is from: "fetch" (reaching the remote), "update" (the fast-forward of the folder) or
	// "config" (the repository's git configuration changed since a person trusted it: the project's banner reviews it).
	ErrorKind string `json:"errorKind,omitempty"`
}

// RepoSyncSettings is repo.syncSettings.get / set.
type RepoSyncSettings struct {
	// Auto is the background fetch (on opening a project and every 15 minutes).
	Auto bool `json:"auto"`
}

type repoSyncMem struct {
	checkedAt int64
	checking  bool
	outcome   string
	err       string
	errKind   string
}

// syncAgentBusy is Sync now's outcome while a chat of the project is mid-turn: the folder is left alone so the files do not change
// under an agent working in it.
const syncAgentBusy = "agent_busy"

type repoSyncer struct {
	c   *Core
	ctx context.Context
	mu  sync.Mutex
	mem map[string]*repoSyncMem
	// one fetch at a time per project
	locks sync.Map // projectID -> *sync.Mutex
}

func init() {
	RegisterExtension(Extension{Name: "reposync", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		ctx, cancel := context.WithCancel(ctx)
		y := &repoSyncer{c: c, ctx: ctx, mem: map[string]*repoSyncMem{}}
		safe.Go("reposync.loop", func() { y.loop(ctx) })
		m := map[string]Method{
			"repo.syncState": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
			}) (any, error) {
				st, err := y.state(ctx, p.ProjectID)
				if err == nil && st.Remote != "" && y.auto(ctx) && y.due(p.ProjectID, repoSyncOnOpen) {
					y.fetchLater(p.ProjectID) // opening a project checks it when its last check is old
					st.Checking = true
				}
				return st, err
			}),
			"repo.sync": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
			}) (any, error) {
				return y.syncNow(ctx, p.ProjectID)
			}),
			"repo.syncSettings.get": bind(func(ctx context.Context, _ struct{}) (any, error) {
				return RepoSyncSettings{Auto: y.auto(ctx)}, nil
			}),
			"repo.syncSettings.set": bind(func(ctx context.Context, p struct {
				Auto *bool `json:"auto"`
			}) (any, error) {
				if p.Auto != nil {
					v := "1"
					if !*p.Auto {
						v = "0"
					}
					if err := c.setSetting(ctx, settingSyncAuto, v); err != nil {
						return nil, err
					}
					c.publish(ctx, "repo.syncSettings", "", map[string]any{"auto": *p.Auto})
				}
				return RepoSyncSettings{Auto: y.auto(ctx)}, nil
			}),
		}
		return m, cancel, nil
	}})
}

func (y *repoSyncer) auto(ctx context.Context) bool {
	var v string
	if err := y.c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingSyncAuto).Scan(&v); err != nil {
		return true
	}
	return v != "0"
}

func (y *repoSyncer) memOf(projectID string) *repoSyncMem {
	m := y.mem[projectID]
	if m == nil {
		m = &repoSyncMem{}
		y.mem[projectID] = m
	}
	return m
}

// due reports whether the project's last check is older than age and none runs now.
func (y *repoSyncer) due(projectID string, age time.Duration) bool {
	y.mu.Lock()
	defer y.mu.Unlock()
	m := y.memOf(projectID)
	return !m.checking && time.Since(time.UnixMilli(m.checkedAt)) >= age
}

// state reads where the project's folder stands, from the refs the last fetch left (no network).
func (y *repoSyncer) state(ctx context.Context, projectID string) (RepoSync, error) {
	st := RepoSync{ProjectID: projectID}
	p, err := y.c.GetProject(ctx, projectID)
	if err != nil {
		return st, err
	}
	if !p.HasRepo {
		return st, nil
	}
	repo, err := y.c.repo(ctx, projectID)
	if err != nil {
		return st, err
	}
	url, err := repo.RemoteURL(ctx, syncRemote)
	if errors.Is(err, git.ErrNoRemote) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	_, _, st.GitHub = parseGitHubRemote(url)
	st.Remote = syncRemote
	rs, err := repo.RemoteState(ctx, syncRemote, p.DefaultBranch)
	if err != nil {
		return st, err
	}
	st.Branch, st.Current, st.Tracked, st.Ahead, st.Behind = rs.Branch, rs.Current, rs.Tracked, rs.Ahead, rs.Behind
	y.mu.Lock()
	m := y.memOf(projectID)
	kind := m.errKind
	y.mu.Unlock()
	if kind == "config" { // a refusal by the configuration guard lasts only until a person trusts the configuration
		if changed, _, err := repo.ConfigStatus(ctx); err == nil && len(changed) == 0 {
			y.mu.Lock()
			if m.errKind == "config" {
				m.err, m.errKind = "", ""
			}
			y.mu.Unlock()
		}
	}
	y.mu.Lock()
	st.CheckedAt, st.Checking, st.Outcome, st.Error, st.ErrorKind = m.checkedAt, m.checking, m.outcome, m.err, m.errKind
	y.mu.Unlock()
	return st, nil
}

func (y *repoSyncer) lock(projectID string) func() {
	v, _ := y.locks.LoadOrStore(projectID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// fetch fetches the project's remote and records when (or why it failed). A fetch that works clears the last Sync now's outcome:
// its reason was about the remote as it stood then.
func (y *repoSyncer) fetch(ctx context.Context, projectID string) error {
	y.mu.Lock()
	y.memOf(projectID).checking = true
	y.mu.Unlock()
	var ferr error
	repo, err := y.c.repo(ctx, projectID)
	if err == nil {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		ferr = repo.Fetch(ctx, syncRemote)
		cancel()
	} else {
		ferr = err
	}
	y.mu.Lock()
	m := y.memOf(projectID)
	m.checking = false
	if ferr != nil {
		m.err, m.errKind = y.scrub(ferr), "fetch"
		var cc *git.ConfigChangedError
		if errors.As(ferr, &cc) {
			m.errKind = "config"
		}
	} else {
		m.err, m.errKind, m.outcome = "", "", ""
		m.checkedAt = time.Now().UnixMilli()
	}
	y.mu.Unlock()
	return ferr
}

// scrub is an error as the page may show it: secrets redacted, one line, bounded.
func (y *repoSyncer) scrub(err error) string {
	msg := err.Error()
	if y.c.Scanner != nil {
		msg = y.c.Scanner.Redact(msg)
	}
	msg = urlUserinfo.ReplaceAllString(msg, "://") // https://user:token@host: the address itself is never shown with a login
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > 400 {
		msg = msg[:400] + "..."
	}
	return msg
}

// fetchLater runs a background fetch of the project and announces the result.
func (y *repoSyncer) fetchLater(projectID string) {
	y.mu.Lock()
	m := y.memOf(projectID)
	if m.checking {
		y.mu.Unlock()
		return
	}
	m.checking = true // shown at once; fetch sets it again
	y.mu.Unlock()
	safe.Go("reposync.fetch", func() {
		defer y.lock(projectID)()
		_ = y.fetch(y.ctx, projectID)
		y.announce(y.ctx, projectID)
	})
}

// announce publishes the project's state (no address, no file names: counts and words only).
func (y *repoSyncer) announce(ctx context.Context, projectID string) {
	if ctx.Err() != nil {
		return
	}
	st, err := y.state(ctx, projectID)
	if err != nil {
		return
	}
	y.c.publish(ctx, "repo.synced", projectID, map[string]any{"behind": st.Behind, "ahead": st.Ahead, "outcome": st.Outcome, "failed": st.Error != ""})
}

// syncNow is Sync now: fetch, then fast-forward when it changes nothing but the branch.
func (y *repoSyncer) syncNow(ctx context.Context, projectID string) (RepoSync, error) {
	st, err := y.state(ctx, projectID)
	if err != nil {
		return st, err
	}
	if st.Remote == "" {
		return st, errors.New("this project's repository has no 'origin' remote to sync with")
	}
	unlock := y.lock(projectID)
	ferr := y.fetch(ctx, projectID)
	outcome := ""
	if ferr == nil {
		repo, err := y.c.repo(ctx, projectID)
		if err != nil {
			unlock()
			return st, err
		}
		if busy, err := y.chatBusy(ctx, projectID); err != nil || busy {
			unlock()
			if err != nil {
				return st, err
			}
			y.mu.Lock()
			y.memOf(projectID).outcome = syncAgentBusy
			y.mu.Unlock()
			y.announce(ctx, projectID)
			return y.state(ctx, projectID)
		}
		out, _, err := repo.FastForward(ctx, syncRemote, st.Branch)
		if err != nil {
			unlock()
			y.mu.Lock()
			m := y.memOf(projectID)
			m.err, m.errKind = y.scrub(err), "update"
			var cc *git.ConfigChangedError
			if errors.As(err, &cc) {
				m.errKind = "config"
			}
			y.mu.Unlock()
			y.announce(ctx, projectID)
			return y.state(ctx, projectID)
		}
		outcome = string(out)
	}
	y.mu.Lock()
	y.memOf(projectID).outcome = outcome
	y.mu.Unlock()
	unlock()
	y.announce(ctx, projectID)
	return y.state(ctx, projectID)
}

// chatBusy reports whether a chat of the project is mid-turn: a project chat works in the project's folder.
func (y *repoSyncer) chatBusy(ctx context.Context, projectID string) (bool, error) {
	var n int
	err := y.c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM chats WHERE project_id=? AND status='running'`, projectID).Scan(&n)
	return n > 0, err
}

// loop is the background check: every project with a remote is fetched (never changed) each period while it is on.
func (y *repoSyncer) loop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(repoSyncFirstRun):
	}
	t := time.NewTicker(repoSyncEvery)
	defer t.Stop()
	for {
		if y.auto(ctx) {
			y.checkAll(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (y *repoSyncer) checkAll(ctx context.Context) {
	ps, err := y.c.Projects(ctx)
	if err != nil {
		return
	}
	for _, p := range ps {
		if ctx.Err() != nil {
			return
		}
		if !p.HasRepo || !y.due(p.ID, repoSyncEvery-time.Minute) {
			continue
		}
		repo, err := y.c.repo(ctx, p.ID)
		if err != nil {
			continue
		}
		if _, err := repo.RemoteURL(ctx, syncRemote); err != nil { // no origin: nothing to check
			continue
		}
		func() {
			defer y.lock(p.ID)()
			_ = y.fetch(ctx, p.ID)
		}()
		y.announce(ctx, p.ID)
	}
}

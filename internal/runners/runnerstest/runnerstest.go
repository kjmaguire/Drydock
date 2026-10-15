// Package runnerstest holds labelled TEST DOUBLES for the CI runners (decision P56): an in-memory GitHub that answers the
// runner endpoints, and a stand-in launcher whose "runner" is a script of output lines, so the pool, the core methods and the
// app's browser tests run without GitHub, without the runner program and without a container engine. Nothing here is used
// by the shipped app.
package runnerstest

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/runners"
)

// Repo is how the fake GitHub sees a repository.
type Repo struct {
	Private    bool
	Visibility string // "private" unless set
	Admin      bool
	Archived   bool
}

// Registration is a just-in-time registration the fake handed out.
type Registration struct {
	Repo   string
	ID     int64
	Name   string
	Labels []string
	Config string
}

// GitHub is a TEST DOUBLE of the GitHub REST endpoints the runners use.
type GitHub struct {
	mu      sync.Mutex
	Repos   map[string]Repo
	Regs    []Registration
	Removed []int64
	Listed  map[string][]runners.Registered // extra runners GitHub lists per repository
	Version string                          // release version; "2.330.0" unless set
	next    int64
	FailJIT error
}

// NewGitHub is a fake GitHub with the given repositories.
func NewGitHub(repos map[string]Repo) *GitHub {
	return &GitHub{Repos: repos, Listed: map[string][]runners.Registered{}}
}

func (g *GitHub) Raw(context.Context, string) (string, error) {
	return "", fmt.Errorf("not in the fake")
}

// Registrations is a copy of the registrations so far.
func (g *GitHub) Registrations() []Registration {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Registration(nil), g.Regs...)
}

// RemovedIDs is a copy of the removed runner ids.
func (g *GitHub) RemovedIDs() []int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]int64(nil), g.Removed...)
}

// SetRepo changes a repository (for example, making it public).
func (g *GitHub) SetRepo(name string, r Repo) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Repos[name] = r
}

func reply(out any, v any) error {
	if out == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func (g *GitHub) Do(_ context.Context, method, path string, body any, out any, _ string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if path == runners.ReleasePath && method == "GET" {
		v := g.Version
		if v == "" {
			v = "2.330.0"
		}
		var assets []map[string]any
		var notes strings.Builder
		for i, p := range []string{"win-x64", "linux-x64"} {
			ext := ".zip"
			if strings.HasPrefix(p, "linux") {
				ext = ".tar.gz"
			}
			name := "actions-runner-" + p + "-" + v + ext
			assets = append(assets, map[string]any{"name": name, "browser_download_url": runners.DownloadPrefix + "v" + v + "/" + name, "size": 1})
			fmt.Fprintf(&notes, "<!-- BEGIN SHA %s -->%s<!-- END SHA %s -->\n", p, strings.Repeat(strconv.Itoa(i+1), 64), p)
		}
		return "", reply(out, map[string]any{"tag_name": "v" + v, "body": notes.String(), "assets": assets})
	}
	rest, ok := strings.CutPrefix(path, "/repos/")
	if !ok {
		return "", &host.APIError{Status: 404, Message: "Not Found", Path: path}
	}
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 {
		return "", &host.APIError{Status: 404, Message: "Not Found", Path: path}
	}
	full := parts[0] + "/" + parts[1]
	repo, known := g.Repos[full]
	if !known {
		return "", &host.APIError{Status: 404, Message: "Not Found", Path: path}
	}
	sub := ""
	if len(parts) == 3 {
		sub = parts[2]
	}
	switch {
	case sub == "" && method == "GET":
		vis := repo.Visibility
		if vis == "" {
			vis = "public"
			if repo.Private {
				vis = "private"
			}
		}
		return "", reply(out, map[string]any{"full_name": full, "private": repo.Private, "visibility": vis, "archived": repo.Archived,
			"permissions": map[string]bool{"admin": repo.Admin}})
	case sub == "actions/runners/generate-jitconfig" && method == "POST":
		if g.FailJIT != nil {
			return "", g.FailJIT
		}
		var req struct {
			Name   string   `json:"name"`
			Labels []string `json:"labels"`
		}
		if err := reply(&req, body); err != nil {
			return "", err
		}
		g.next++
		id := 1000 + g.next
		cfg := fmt.Sprintf("jit-secret-%d", id)
		g.Regs = append(g.Regs, Registration{Repo: full, ID: id, Name: req.Name, Labels: req.Labels, Config: cfg})
		return "", reply(out, map[string]any{"runner": map[string]any{"id": id, "name": req.Name}, "encoded_jit_config": cfg})
	case strings.HasPrefix(sub, "actions/runners/") && method == "DELETE":
		id, err := strconv.ParseInt(strings.TrimPrefix(sub, "actions/runners/"), 10, 64)
		if err != nil {
			return "", &host.APIError{Status: 404, Message: "Not Found", Path: path}
		}
		g.Removed = append(g.Removed, id)
		return "", nil
	case strings.HasPrefix(sub, "actions/runners") && method == "GET":
		return "", reply(out, map[string]any{"total_count": len(g.Listed[full]), "runners": g.Listed[full]})
	}
	return "", &host.APIError{Status: 404, Message: "Not Found", Path: path}
}

// Launcher is a TEST DOUBLE launcher: Start "runs" a runner that prints "Listening for Jobs", then runs the jobs queued with
// Queue (printing the runner's own job lines) and exits, or waits until it is stopped.
type Launcher struct {
	K       runners.Kind
	FreeB   uint64 // 0: free space unknown
	Unavail error  // what Available says
	JobTime time.Duration

	mu      sync.Mutex
	queue   []runners.JobResult
	Started []runners.Job
	Cleaned []string
	Swept   int
	Prepped []string
}

// Queue adds a job the next runner takes.
func (l *Launcher) Queue(name, result string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.queue = append(l.queue, runners.JobResult{Name: name, Result: result})
}

// StartedJobs is a copy of the started jobs.
func (l *Launcher) StartedJobs() []runners.Job {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]runners.Job(nil), l.Started...)
}

// CleanedNames is a copy of the cleaned-up job names.
func (l *Launcher) CleanedNames() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.Cleaned...)
}

func (l *Launcher) Kind() runners.Kind              { return l.K }
func (l *Launcher) Available(context.Context) error { return l.Unavail }
func (l *Launcher) Arch() string                    { return "X64" }
func (l *Launcher) Free() (uint64, bool)            { return l.FreeB, l.FreeB > 0 }
func (l *Launcher) Sweep(context.Context)           { l.mu.Lock(); l.Swept++; l.mu.Unlock() }
func (l *Launcher) Cleanup(job runners.Job) error {
	l.mu.Lock()
	l.Cleaned = append(l.Cleaned, job.Name)
	l.mu.Unlock()
	return nil
}
func (l *Launcher) Prepare(_ context.Context, rel runners.Release) error {
	l.mu.Lock()
	l.Prepped = append(l.Prepped, rel.Version)
	l.mu.Unlock()
	return nil
}

type running struct {
	lines chan string
	exit  chan int
	stop  chan struct{}
	once  sync.Once
}

func (r *running) Lines() <-chan string { return r.lines }
func (r *running) Wait() <-chan int     { return r.exit }
func (r *running) Stop()                { r.once.Do(func() { close(r.stop) }) }

func (l *Launcher) take() *runners.JobResult {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.queue) == 0 {
		return nil
	}
	j := l.queue[0]
	l.queue = l.queue[1:]
	return &j
}

func (l *Launcher) Start(_ context.Context, job runners.Job) (runners.Running, error) {
	l.mu.Lock()
	l.Started = append(l.Started, job)
	d := l.JobTime
	l.mu.Unlock()
	r := &running{lines: make(chan string, 16), exit: make(chan int, 1), stop: make(chan struct{})}
	go func() {
		defer close(r.lines)
		r.lines <- "√ Connected to GitHub"
		r.lines <- "2026-10-15 02:00:00Z: Listening for Jobs"
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			next := l.take()
			if next == nil {
				select {
				case <-r.stop:
					r.exit <- 1
					return
				case <-tick.C:
					continue
				}
			}
			r.lines <- "2026-10-15 02:00:01Z: Running job: " + next.Name
			select {
			case <-time.After(d):
			case <-r.stop:
				r.exit <- 1
				return
			}
			r.lines <- "2026-10-15 02:00:02Z: Job " + next.Name + " completed with result: " + next.Result
			r.exit <- 0
			return
		}
	}()
	return r, nil
}

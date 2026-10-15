package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/runners"
	"github.com/kjmaguire/drydock/internal/sandbox"
)

// CI runners (decision P56, docs/ci-runners.md): GitHub Actions self-hosted runners for a person's private repositories,
// kept by internal/runners. They are machine settings, so they belong to the workspace (Connections page), not to a project.
// Every change is a person's (human identity only), a Windows runner needs the person to accept that its jobs run as them,
// and a repository that is not private is refused. Runners only run CI and report to GitHub; Drydock's own pipeline reads
// the results and does the fixing and the pushing (docs/github.md section 5). Events: runners.changed.

const (
	settingRunnerSlots = "runners:slots"
	settingRunnerRoot  = "runners:root"
	maxRunnerSlots     = 8
)

func init() {
	RegisterExtension(Extension{Name: "runners", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		r := &runnersExt{c: c}
		r.start(ctx)
		return map[string]Method{
			"runners.get": bind(func(ctx context.Context, _ struct{}) (any, error) {
				return r.view(ctx), nil
			}),
			"runners.add": bind(func(ctx context.Context, p struct {
				Repo            string       `json:"repo"`
				Kind            runners.Kind `json:"kind"`
				AcceptRunsAsYou bool         `json:"acceptRunsAsYou"`
			}) (any, error) {
				return r.add(ctx, p.Repo, p.Kind, p.AcceptRunsAsYou, c.Human())
			}),
			"runners.remove": bind(func(ctx context.Context, p struct {
				Repo string       `json:"repo"`
				Kind runners.Kind `json:"kind"`
			}) (any, error) {
				return r.change(ctx, p.Repo, p.Kind, c.Human(), func(slots []runners.SlotConfig, i int) []runners.SlotConfig {
					return append(slots[:i], slots[i+1:]...)
				})
			}),
			"runners.enable": bind(func(ctx context.Context, p struct {
				Repo    string       `json:"repo"`
				Kind    runners.Kind `json:"kind"`
				Enabled bool         `json:"enabled"`
			}) (any, error) {
				by := c.Human()
				return r.change(ctx, p.Repo, p.Kind, by, func(slots []runners.SlotConfig, i int) []runners.SlotConfig {
					slots[i].Enabled, slots[i].By, slots[i].At = p.Enabled, by, time.Now().UTC()
					return slots
				})
			}),
			"runners.root.set": bind(func(ctx context.Context, p struct {
				Path string `json:"path"`
			}) (any, error) {
				return r.setRoot(ctx, p.Path, c.Human())
			}),
			"runners.log": bind(func(_ context.Context, p struct {
				Repo string       `json:"repo"`
				Kind runners.Kind `json:"kind"`
			}) (any, error) {
				return map[string]any{"lines": r.pool.Log(p.Repo + ":" + string(p.Kind))}, nil
			}),
		}, r.pool.Close, nil
	}})
}

type runnersExt struct {
	c    *Core
	pool *runners.Pool
	host *runners.HostLauncher
	mu   sync.Mutex // one change at a time

	root       string
	rootSource string // chosen | devdrive | default
	devDrives  []runners.Drive
}

func (r *runnersExt) start(ctx context.Context) {
	c := r.c
	cache := filepath.Join(c.cfg.DataDir, "runners", "cache")
	launchers := map[runners.Kind]runners.Launcher{
		runners.Linux: &runners.ContainerLauncher{Engine: sandbox.NewEngine(), Cache: cache},
	}
	if runtime.GOOS == "windows" {
		r.devDrives = runners.DevDrives(ctx)
		r.resolveRoot(ctx)
		r.host = &runners.HostLauncher{Root: r.root, Cache: cache}
		launchers[runners.Windows] = r.host
	}
	r.pool = &runners.Pool{
		API: func(context.Context) (host.API, error) {
			api, ok := c.githubAPI()
			if !ok {
				return nil, errors.New("gh is not installed")
			}
			return api, nil
		},
		Launchers: launchers,
		Publish: func(st runners.Status) {
			c.publish(context.Background(), "runners.changed", "", map[string]any{"key": st.Key, "state": st.State, "job": st.Job, "jobs": st.Jobs})
		},
	}
	if c.cfg.Runners != nil {
		c.cfg.Runners(r.pool)
		if h, ok := r.pool.Launchers[runners.Windows].(*runners.HostLauncher); ok {
			r.host = h
			if h.Root == "" {
				r.resolveRoot(ctx)
				h.Root = r.root
			} else if r.root == "" {
				r.root, r.rootSource = h.Root, "default"
			}
		}
	}
	r.pool.Set(r.slots(ctx))
}

// resolveRoot picks where Windows jobs run: the folder a person chose, else a folder on the first Dev Drive, else the
// app's data folder.
func (r *runnersExt) resolveRoot(ctx context.Context) {
	var v string
	_ = r.c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingRunnerRoot).Scan(&v)
	switch {
	case v != "":
		r.root, r.rootSource = v, "chosen"
	case len(r.devDrives) > 0:
		r.root, r.rootSource = r.devDrives[0].Letter+`\DrydockRunners`, "devdrive"
	default:
		r.root, r.rootSource = filepath.Join(r.c.cfg.DataDir, "runners", "work"), "default"
	}
}

func (r *runnersExt) slots(ctx context.Context) []runners.SlotConfig {
	var v string
	if err := r.c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingRunnerSlots).Scan(&v); err != nil {
		return nil
	}
	var out []runners.SlotConfig
	_ = json.Unmarshal([]byte(v), &out)
	return out
}

func (r *runnersExt) save(ctx context.Context, slots []runners.SlotConfig) error {
	b, err := json.Marshal(slots)
	if err != nil {
		return err
	}
	return r.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingRunnerSlots, string(b))
		return err
	})
}

// RunnerKind says whether runners of a kind can run on this PC, and where their jobs write.
type RunnerKind struct {
	Kind      runners.Kind `json:"kind"`
	Available bool         `json:"available"`
	Reason    string       `json:"reason,omitempty"`
	Where     string       `json:"where"`
	FreeBytes uint64       `json:"freeBytes,omitempty"`
	RunsOn    string       `json:"runsOn"` // what a workflow writes to use it
}

// RunnerSlot is a runner a person asked for, with what it is doing.
type RunnerSlot struct {
	runners.Status
	By string    `json:"by,omitempty"`
	At time.Time `json:"at,omitempty"`
}

// RunnersView is the Connections page's CI runners section.
type RunnersView struct {
	Kinds        []RunnerKind    `json:"kinds"`
	Root         string          `json:"root,omitempty"`
	RootSource   string          `json:"rootSource,omitempty"`
	DevDrives    []runners.Drive `json:"devDrives"`
	Slots        []RunnerSlot    `json:"slots"`
	MinFreeBytes uint64          `json:"minFreeBytes"`
	Max          int             `json:"max"`
}

func (r *runnersExt) view(ctx context.Context) RunnersView {
	v := RunnersView{DevDrives: r.devDrives, Slots: []RunnerSlot{}, MinFreeBytes: runners.DefaultMinFree, Max: maxRunnerSlots}
	if r.pool.MinFree > 0 {
		v.MinFreeBytes = r.pool.MinFree
	}
	if v.DevDrives == nil {
		v.DevDrives = []runners.Drive{}
	}
	r.mu.Lock()
	v.Root, v.RootSource = r.root, r.rootSource
	r.mu.Unlock()
	for _, k := range []runners.Kind{runners.Windows, runners.Linux} {
		info := RunnerKind{Kind: k}
		switch k {
		case runners.Windows:
			info.Where = "On this PC, as you, in a fresh folder for each job"
			info.RunsOn = "[self-hosted, Windows, drydock]"
		case runners.Linux:
			info.Where = "In a fresh Linux container for each job (Docker Desktop, WSL 2)"
			info.RunsOn = "[self-hosted, Linux, drydock]"
		}
		l := r.pool.Launchers[k]
		if l == nil {
			info.Reason = "Windows runners run on Windows only."
		} else {
			actx, cancel := context.WithTimeout(ctx, 8*time.Second)
			err := l.Available(actx)
			cancel()
			if err != nil {
				info.Reason = err.Error()
				if k == runners.Linux {
					info.Reason = "No Linux container engine answered: install Docker Desktop (it runs on WSL 2) and start it. (" + err.Error() + ")"
				}
			} else {
				info.Available = true
			}
			if free, ok := l.Free(); ok {
				info.FreeBytes = free
			}
		}
		v.Kinds = append(v.Kinds, info)
	}
	byKey := map[string]runners.SlotConfig{}
	for _, s := range r.slots(ctx) {
		byKey[s.Key()] = s
	}
	for _, st := range r.pool.Status() {
		cfg := byKey[st.Key]
		v.Slots = append(v.Slots, RunnerSlot{Status: st, By: cfg.By, At: cfg.At})
	}
	return v
}

func humanOnly(by, what string) error {
	if !strings.HasPrefix(by, "human:") || len(by) == len("human:") {
		return fmt.Errorf("only a person may %s", what)
	}
	return nil
}

func validKind(k runners.Kind) error {
	if k != runners.Windows && k != runners.Linux {
		return fmt.Errorf("%q is not a runner kind (windows or linux)", k)
	}
	return nil
}

func (r *runnersExt) add(ctx context.Context, repoS string, kind runners.Kind, accept bool, by string) (RunnersView, error) {
	if err := humanOnly(by, "add a CI runner"); err != nil {
		return RunnersView{}, err
	}
	repo, err := runners.ParseRepo(repoS)
	if err != nil {
		return RunnersView{}, err
	}
	if err := validKind(kind); err != nil {
		return RunnersView{}, err
	}
	if r.pool.Launchers[kind] == nil {
		return RunnersView{}, errors.New("Windows runners run on Windows only")
	}
	if kind == runners.Windows && !accept {
		return RunnersView{}, errors.New("a Windows runner runs each job's commands on this PC as you, with your files and your network: confirm that you accept this for " + repo.String())
	}
	taken := func(slots []runners.SlotConfig) error {
		for _, s := range slots {
			if strings.EqualFold(s.Repo.String(), repo.String()) && s.Kind == kind {
				return fmt.Errorf("%s already has a %s runner", repo, kind)
			}
		}
		if len(slots) >= maxRunnerSlots {
			return fmt.Errorf("at most %d runners: remove one first", maxRunnerSlots)
		}
		return nil
	}
	if err := taken(r.slots(ctx)); err != nil {
		return RunnersView{}, err
	}
	api, err := r.pool.API(ctx)
	if err != nil || api == nil {
		return RunnersView{}, errors.New("GitHub is not connected: sign in with gh on the Connections page first")
	}
	if _, err := runners.CheckRepo(ctx, api, repo); err != nil {
		return RunnersView{}, err
	}
	r.mu.Lock()
	slots := r.slots(ctx)
	if err := taken(slots); err != nil {
		r.mu.Unlock()
		return RunnersView{}, err
	}
	slots = append(slots, runners.SlotConfig{Repo: repo, Kind: kind, Enabled: true, By: by, At: time.Now().UTC()})
	err = r.save(ctx, slots)
	r.mu.Unlock()
	if err != nil {
		return RunnersView{}, err
	}
	r.pool.Set(slots)
	r.c.publish(ctx, "runners.changed", "", map[string]any{"key": repo.String() + ":" + string(kind), "added": true, "by": by})
	return r.view(ctx), nil
}

func (r *runnersExt) change(ctx context.Context, repoS string, kind runners.Kind, by string, f func([]runners.SlotConfig, int) []runners.SlotConfig) (RunnersView, error) {
	if err := humanOnly(by, "change a CI runner"); err != nil {
		return RunnersView{}, err
	}
	r.mu.Lock()
	slots := r.slots(ctx)
	i := -1
	for j, s := range slots {
		if strings.EqualFold(s.Repo.String(), strings.TrimSpace(repoS)) && s.Kind == kind {
			i = j
		}
	}
	if i < 0 {
		r.mu.Unlock()
		return RunnersView{}, fmt.Errorf("there is no %s runner for %s", kind, repoS)
	}
	slots = f(slots, i)
	err := r.save(ctx, slots)
	r.mu.Unlock()
	if err != nil {
		return RunnersView{}, err
	}
	// stops the runner and deletes its registration when it was removed or switched off
	r.pool.Set(slots)
	r.c.publish(ctx, "runners.changed", "", map[string]any{"key": strings.TrimSpace(repoS) + ":" + string(kind), "by": by})
	return r.view(ctx), nil
}

// setRoot chooses where Windows jobs run ("" goes back to the Dev Drive or the app's folder). Windows runners must be off.
func (r *runnersExt) setRoot(ctx context.Context, path, by string) (RunnersView, error) {
	if err := humanOnly(by, "choose where CI jobs run"); err != nil {
		return RunnersView{}, err
	}
	if r.host == nil {
		return RunnersView{}, errors.New("Windows runners run on Windows only")
	}
	path = strings.TrimSpace(strings.Trim(strings.TrimSpace(path), `"`))
	if path != "" && !filepath.IsAbs(path) {
		return RunnersView{}, fmt.Errorf("give the full path of a folder, for example D:\\DrydockRunners")
	}
	if err := r.storeRoot(ctx, path, by); err != nil {
		return RunnersView{}, err
	}
	return r.view(ctx), nil
}

func (r *runnersExt) storeRoot(ctx context.Context, path, by string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.slots(ctx) {
		if s.Kind == runners.Windows && s.Enabled {
			return errors.New("switch the Windows runners off first: the folder changes for the next job")
		}
	}
	if path != "" {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("could not use %s: %w", path, err)
		}
	}
	if err := r.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if path == "" {
			_, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, settingRunnerRoot)
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingRunnerRoot, path)
		return err
	}); err != nil {
		return err
	}
	r.resolveRoot(ctx)
	r.host.Root = r.root
	r.c.publish(ctx, "runners.changed", "", map[string]any{"root": r.root, "by": by})
	return nil
}

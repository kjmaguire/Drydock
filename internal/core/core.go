// Package core is the application facade: it wires the storage, event bus, workflow engine, agent runtime, tasks, room and
// pipeline into one object with no UI or Wails dependency. The Wails shell and the development HTTP transport both call it
// through the method registry in rpc.go, so what the tests drive is what the app runs.
package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/connections"
	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/memory"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/watchdog"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Config configures a Core.
type Config struct {
	// DataDir holds the database, run artifacts and task worktrees (<app data>/Drydock).
	DataDir string
	// User is the human's display name; their identity is "human:<User>". Defaults to the OS user.
	User string
	// Adapters override or extend the agent adapters (tests install the scripted agent here).
	Adapters map[string]agents.Adapter
	// Procs overrides the process manager (default proc.New()).
	Procs proc.Manager
	// GitHub resolves the GitHub client for a project in pr_flow mode (default: the user's `gh`).
	GitHub func(ctx context.Context, p Project) (pipeline.PRConfig, error)
	// Credentials stores provider API keys. Default: the Windows Credential Manager; on other development hosts there is none
	// and providers that need a key cannot be created (tests inject providers.NewMemoryCredentialStoreForTests).
	Credentials providers.CredentialStore
	// LeaseTTL for workflow runs (default 30s).
	LeaseTTL time.Duration
}

// Core is the running application.
type Core struct {
	cfg         Config
	DB          *storage.DB
	Bus         *events.SQLBus
	Tasks       *tasks.Service
	Room        *room.Service
	Engine      *workflow.Engine
	Runtime     *rt.Runtime
	Watch       *watchdog.Watchdog
	Context     *ctxeng.Engine
	Presence    *room.Tracker
	Scanner     *security.Scanner
	Providers   *providers.Registry
	Prober      *providers.Prober
	Health      *providers.Monitor
	Connections *connections.Service
	Deps        pipeline.Deps

	extMethods map[string]Method
	extClose   []func()

	mu       sync.Mutex
	adapters map[string]agents.Adapter
	repos    map[string]*git.Repo
	cancel   context.CancelFunc
}

// Open creates (or reopens) the application state in cfg.DataDir and resumes interrupted runs.
func Open(ctx context.Context, cfg Config) (*Core, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("core: DataDir is required")
	}
	if cfg.User == "" {
		cfg.User = firstNonEmpty(os.Getenv("USERNAME"), os.Getenv("USER"), "me")
	}
	if cfg.Procs == nil {
		cfg.Procs = proc.New()
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = 30 * time.Second
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	db, err := storage.Open(ctx, filepath.Join(cfg.DataDir, "drydock.db"))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	bg, cancel := context.WithCancel(context.Background())
	c := &Core{cfg: cfg, DB: db, cancel: cancel, adapters: map[string]agents.Adapter{}, repos: map[string]*git.Repo{}}
	acked, err := c.loadGateAcks(ctx)
	if err != nil {
		cancel()
		db.Close()
		return nil, err
	}
	// Adapters come from the embedded manifests (Claude headless; Codex, Gemini, OpenCode and Goose over ACP; Antigravity is
	// declared but gated). ACP permission requests are rejected until the approval UI is wired (nil handler rejects).
	c.Connections, err = connections.NewService(connections.Config{
		Manifests: manifests.Options{ScrubEnv: true},
		Detection: connections.Options{PM: cfg.Procs, Acked: acked},
	})
	if err != nil {
		cancel()
		db.Close()
		return nil, fmt.Errorf("connections: %w", err)
	}
	built, failed := c.Connections.Registry.BuildAll()
	for id, e := range failed {
		c.logf("adapter %s unavailable: %v", id, e)
	}
	for k, v := range built {
		c.adapters[k] = v
	}
	for k, v := range cfg.Adapters {
		c.adapters[k] = v
	}
	c.Bus = events.NewSQLBus(db, 4096)
	if c.Scanner, err = security.NewScanner(); err != nil {
		cancel()
		db.Close()
		return nil, fmt.Errorf("secret scanner: %w", err)
	}
	// every message body is scanned before it is stored, so a secret pasted into the room never reaches the database, the
	// event log or another agent's context (security.md s8)
	c.Room = room.New(db, c.Bus, room.Options{
		Redact:  func(body string) (string, error) { return c.Scanner.Redact(body), nil },
		OnError: func(err error) { c.logf("room: %v", err) },
	})
	c.Tasks = &tasks.Service{DB: db, Bus: c.Bus, Hooks: tasks.Hooks{
		OnCreate: func(ctx context.Context, t tasks.Task) {
			if _, err := c.Room.CreateTaskChannel(ctx, room.TaskChannelSpec{ProjectID: t.ProjectID, TaskID: t.ID, Number: t.Seq, Title: t.Title, CreatedBy: room.Human(cfg.User)}); err != nil {
				c.logf("task channel: %v", err)
			}
		},
		OnTerminal: func(ctx context.Context, t tasks.Task) { _ = c.Room.ArchiveTaskChannel(ctx, t.ID, string(t.Status)) },
	}}
	c.Context = &ctxeng.Engine{DB: db, Memory: memory.Store{DB: db}}
	c.Runtime = &rt.Runtime{PM: cfg.Procs, Bus: c.Bus, DB: db, Adapters: c.adapters, NudgeWait: 5 * time.Second}
	c.Deps = pipeline.Deps{
		Tasks: c.Tasks, Runtime: c.Runtime, PM: cfg.Procs, Bus: c.Bus, Context: c.Context,
		Repo:         c.repo,
		WorktreeRoot: filepath.Join(cfg.DataDir, "worktrees"), DataDir: cfg.DataDir,
		AgentConfig: c.agentConfig,
		OnCheck:     c.postCheck,
		OnTurn:      c.postTurn,
	}
	c.Engine = workflow.New(workflow.Config{DB: db, Bus: c.Bus, DataDir: cfg.DataDir, LeaseTTL: cfg.LeaseTTL, SafePoint: pipeline.SafePointHook(c.Deps)})
	c.Watch = watchdog.New(watchdog.SQLStore{DB: db}, watchdog.Hooks{Notify: c.watchdogNotify}, nil)
	c.Engine.Use(workflow.KindAgentTask, c.Watch.Middleware())
	c.Engine.Observe(c.Watch.Observer())
	c.Engine.Use(workflow.KindCheck, c.Watch.CheckMiddleware(nil))
	for _, g := range []*workflow.Graph{
		pipeline.TaskPipeline(c.Deps, 5),
		pipeline.IterateUntilVerified(c.Deps, c.projectInfo, 5),
		pipeline.IterateUntilVerifiedPR(c.Deps, c.prSource, 5),
	} {
		if err := c.Engine.Register(g); err != nil {
			cancel()
			db.Close()
			return nil, err
		}
	}
	c.Presence = room.NewTracker(c.Bus, room.TrackerOptions{EmitEvery: time.Second})
	if err := c.Presence.Start(bg); err != nil {
		c.logf("presence: %v", err)
	}
	var secrets providers.SecretStore
	cred := cfg.Credentials
	if cred == nil {
		if cs, cerr := providers.NewCredentialStore(); cerr == nil {
			cred = cs
		} else {
			c.logf("provider keys: %v", cerr)
		}
	}
	if cred != nil {
		secrets = providers.NewVault(cred, db)
	}
	c.Providers = providers.NewRegistry(db, providers.RegistryOptions{Secrets: secrets, Bus: c.Bus})
	c.Prober = providers.NewProber(c.Providers, c.Bus)
	c.Health = providers.NewMonitor(c.Providers, c.Prober, c.Bus, providers.MonitorOptions{})
	c.Health.Start(bg)
	go func() { _ = c.Room.WatchTasks(bg) }()
	if _, err := c.Room.Reconcile(ctx); err != nil {
		c.logf("room reconcile: %v", err)
	}
	if err := c.startExtensions(bg); err != nil {
		c.logf("extensions: %v", err)
	}
	if n, err := c.Engine.Recover(ctx); err != nil {
		c.logf("recover: %v", err)
	} else if n > 0 {
		c.logf("resumed %d interrupted run(s)", n)
	}
	return c, nil
}

// Close stops background work. Running agent processes are killed with their job objects.
func (c *Core) Close() {
	c.cancel()
	for i := len(c.extClose) - 1; i >= 0; i-- {
		c.extClose[i]()
	}
	c.Health.Stop()
	c.Engine.Close()
	c.Bus.Close()
	c.DB.Close()
}

func (c *Core) logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "drydock: "+format+"\n", a...)
}

// Human is the identity every human action carries. Only "human:"-prefixed identities can accept tasks and approve merges.
func (c *Core) Human() string { return "human:" + c.cfg.User }

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// RegisterAdapter installs an agent adapter (manifest-driven ACP agents, the Claude adapter, test agents).
func (c *Core) RegisterAdapter(id string, a agents.Adapter) {
	c.mu.Lock()
	c.adapters[id] = a
	c.mu.Unlock()
}

// --- projects ---

// Project is a repository Drydock works in.
type Project struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	RepoPath      string `json:"repoPath"`
	DefaultBranch string `json:"defaultBranch"`
	MergeStrategy string `json:"mergeStrategy"`
	Mode          string `json:"mode"` // local_merge | pr_flow
	Created       int64  `json:"created"`
}

// CreateProject registers a local Git repository as a project and creates its default channels.
func (c *Core) CreateProject(ctx context.Context, name, repoPath string) (Project, error) {
	repo, err := git.Open(ctx, repoPath)
	if err != nil {
		return Project{}, fmt.Errorf("%s is not a usable Git repository: %w", repoPath, err)
	}
	branch, err := repo.CurrentBranch(ctx)
	if err != nil || branch == "" || branch == "HEAD" {
		return Project{}, fmt.Errorf("the repository at %s has no checked-out branch: commit once on a branch first", repoPath)
	}
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(repo.Root)
	}
	p := Project{ID: ids.New("prj"), Name: name, RepoPath: repo.Root, DefaultBranch: branch, MergeStrategy: "merge", Mode: "local_merge", Created: storage.Now()}
	err = c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO projects(id,name,repo_path,default_branch,merge_strategy,mode,created_at) VALUES(?,?,?,?,?,?,?)`,
			p.ID, p.Name, p.RepoPath, p.DefaultBranch, p.MergeStrategy, p.Mode, p.Created)
		return err
	})
	if err != nil {
		return Project{}, err
	}
	if _, err := c.Room.EnsureDefaultChannels(ctx, p.ID, room.Human(c.cfg.User)); err != nil {
		return p, fmt.Errorf("project created but channels failed: %w", err)
	}
	c.publish(ctx, "project.created", p.ID, map[string]any{"name": p.Name})
	return p, nil
}

func (c *Core) publish(ctx context.Context, typ, projectID string, data any) {
	b, _ := json.Marshal(data)
	_, _ = c.Bus.Publish(ctx, events.Event{Type: typ, ProjectID: projectID, Data: b})
}

// Projects lists projects, newest first.
func (c *Core) Projects(ctx context.Context) ([]Project, error) {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT id,name,repo_path,default_branch,merge_strategy,mode,created_at FROM projects ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.RepoPath, &p.DefaultBranch, &p.MergeStrategy, &p.Mode, &p.Created); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProject returns one project.
func (c *Core) GetProject(ctx context.Context, id string) (Project, error) {
	var p Project
	err := c.DB.Reader().QueryRowContext(ctx, `SELECT id,name,repo_path,default_branch,merge_strategy,mode,created_at FROM projects WHERE id=?`, id).
		Scan(&p.ID, &p.Name, &p.RepoPath, &p.DefaultBranch, &p.MergeStrategy, &p.Mode, &p.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return p, fmt.Errorf("project %s not found", id)
	}
	return p, err
}

func (c *Core) repo(ctx context.Context, projectID string) (*git.Repo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.repos[projectID]; ok {
		return r, nil
	}
	p, err := c.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	r, err := git.Open(ctx, p.RepoPath)
	if err != nil {
		return nil, err
	}
	c.repos[projectID] = r
	return r, nil
}

func (c *Core) projectInfo(ctx context.Context, projectID string) (pipeline.ProjectInfo, error) {
	p, err := c.GetProject(ctx, projectID)
	if err != nil {
		return pipeline.ProjectInfo{}, err
	}
	return pipeline.ProjectInfo{DefaultBranch: p.DefaultBranch, MergeStrategy: git.MergeStrategy(p.MergeStrategy), DeleteBranchAfterMerge: true}, nil
}

func (c *Core) prSource(ctx context.Context, projectID string) (pipeline.PRConfig, error) {
	p, err := c.GetProject(ctx, projectID)
	if err != nil {
		return pipeline.PRConfig{}, err
	}
	if c.cfg.GitHub != nil {
		return c.cfg.GitHub(ctx, p)
	}
	repo, err := c.repo(ctx, projectID)
	if err != nil {
		return pipeline.PRConfig{}, err
	}
	owner, name, err := remoteRepo(ctx, repo)
	if err != nil {
		return pipeline.PRConfig{}, err
	}
	return pipeline.PRConfig{GitHub: host.GitHub{API: host.GhCLI{}, Owner: owner, Repo: name}, BaseBranch: p.DefaultBranch,
		MergeStrategy: host.MergeStrategy(p.MergeStrategy), DeleteBranch: true}, nil
}

// --- agents (the team) ---

// Agent is a configured team member: an adapter plus role, model and policy.
type Agent struct {
	ID             string `json:"id"`
	ProjectID      string `json:"projectId"`
	Name           string `json:"name"`
	Adapter        string `json:"adapter"`
	Role           string `json:"role"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	ProviderID     string `json:"providerId"`
	PermissionMode string `json:"permissionMode"`
	Status         string `json:"status"`
}

// AgentSpec creates an agent.
type AgentSpec struct {
	ProjectID      string `json:"projectId"`
	Name           string `json:"name"`
	Adapter        string `json:"adapter"`
	Role           string `json:"role"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	ProviderID     string `json:"providerId"`
	PermissionMode string `json:"permissionMode"`
}

// CreateAgent adds an agent to the team. The adapter must be one Drydock knows.
func (c *Core) CreateAgent(ctx context.Context, s AgentSpec) (Agent, error) {
	c.mu.Lock()
	_, known := c.adapters[s.Adapter]
	c.mu.Unlock()
	if !known {
		return Agent{}, fmt.Errorf("unknown adapter %q", s.Adapter)
	}
	if _, err := c.GetProject(ctx, s.ProjectID); err != nil {
		return Agent{}, err
	}
	// a gated Connection (for example Antigravity's terms notice) cannot be used until the human acknowledged the gate
	if conn, err := c.Connections.Check(ctx, s.Adapter); err == nil {
		for _, g := range conn.Gates {
			if !g.Acked {
				return Agent{}, fmt.Errorf("%s needs your acknowledgement first (%s): %s", s.Adapter, g.ID, g.Text)
			}
		}
	}
	if strings.TrimSpace(s.Name) == "" || strings.TrimSpace(s.Role) == "" {
		return Agent{}, errors.New("an agent needs a name and a role")
	}
	if s.PermissionMode == "" {
		s.PermissionMode = "acceptEdits" // headless agents cannot answer prompts; edits inside the worktree are the job
	}
	a := Agent{ID: ids.New("agt"), ProjectID: s.ProjectID, Name: s.Name, Adapter: s.Adapter, Role: s.Role, Model: s.Model, Effort: s.Effort, ProviderID: s.ProviderID, PermissionMode: s.PermissionMode, Status: "STOPPED"}
	err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO agents(id,project_id,name,adapter,role,model,effort,provider_id,permission_mode,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			a.ID, a.ProjectID, a.Name, a.Adapter, a.Role, a.Model, a.Effort, a.ProviderID, a.PermissionMode, storage.Now())
		return err
	})
	if err != nil {
		return Agent{}, err
	}
	// the new member joins the project's channels so it can be mentioned and heard
	if rooms, err := c.Room.ListRooms(ctx, a.ProjectID, false); err == nil {
		for _, r := range rooms {
			if r.Kind == room.KindChannel {
				if err := c.Room.AddMember(ctx, r.ID, room.Agent(a.ID), a.Role, room.StateActive, room.Human(c.cfg.User)); err != nil {
					c.logf("join #%s: %v", r.Name, err)
				}
			}
		}
	}
	c.publish(ctx, "agent.created", a.ProjectID, a)
	return a, nil
}

// Agents lists a project's team.
func (c *Core) Agents(ctx context.Context, projectID string) ([]Agent, error) {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT id,project_id,name,adapter,role,model,effort,provider_id,permission_mode,status FROM agents WHERE project_id=? ORDER BY created_at, id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Agent{}
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.Name, &a.Adapter, &a.Role, &a.Model, &a.Effort, &a.ProviderID, &a.PermissionMode, &a.Status); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (c *Core) agent(ctx context.Context, id string) (Agent, error) {
	var a Agent
	err := c.DB.Reader().QueryRowContext(ctx, `SELECT id,project_id,name,adapter,role,model,effort,provider_id,permission_mode,status FROM agents WHERE id=?`, id).
		Scan(&a.ID, &a.ProjectID, &a.Name, &a.Adapter, &a.Role, &a.Model, &a.Effort, &a.ProviderID, &a.PermissionMode, &a.Status)
	return a, err
}

// agentConfig is the pipeline's AgentConfig: the task's assignee does the work, its reviewer reviews.
func (c *Core) agentConfig(t tasks.Task, role string) rt.Config {
	id := t.Assignee
	if role == "review" {
		id = t.Reviewer
	}
	a, err := c.agent(context.Background(), id)
	if err != nil {
		return rt.Config{AgentID: id, Adapter: "missing:" + id} // surfaces as a clear "unknown adapter" failure on the node
	}
	spec := agents.RunSpec{AgentID: a.ID, Role: a.Role, Model: a.Model, Effort: a.Effort, PermissionMode: a.PermissionMode}
	if role == "review" {
		spec.PermissionMode = "plan" // the reviewer reads; it never edits (the prompt also says so)
		spec.Role = "reviewer"
	}
	return rt.Config{AgentID: a.ID, Adapter: a.Adapter, Spec: spec, Provider: providerLabel(a), Tier: "premium"}
}

func providerLabel(a Agent) string {
	if a.ProviderID != "" {
		return a.ProviderID
	}
	return a.Adapter
}

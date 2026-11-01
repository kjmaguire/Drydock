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
	"sync/atomic"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/budget"
	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/connections"
	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/datalock"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/memory"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/repomap"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/runners"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/watchdog"
	"github.com/kjmaguire/drydock/internal/workflow"
	"github.com/kjmaguire/drydock/internal/wsl"
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
	// BackupFirstCheck is how long after start the backup scheduler first considers a backup (zero: 5s; negative is refused).
	// In the internal/core tests zero means an hour (they drive the scheduler with TestBackupTick); elsewhere it means 5s.
	BackupFirstCheck time.Duration
	// GitHubAPI answers the account calls (signed-in user, organizations, repositories). Default: the user's `gh`.
	GitHubAPI host.API
	// GitHubCloneURL, when set, makes github.clone run `git clone <url>` instead of `gh repo clone` (tests clone a local
	// repository this way).
	GitHubCloneURL func(fullName string) string
	// MCPCatalog replaces the built-in MCP catalog that the library and the connector gallery offer. TEST HARNESS ONLY: the
	// browser end-to-end harness adds an entry for its fake OAuth server (a test double); the product leaves it nil.
	MCPCatalog *mcp.Catalog
	// WSL reaches the WSL distros (the WSL run mode, decision P41). Default: the machine's wsl.exe through Procs; tests use
	// the labelled stand-in (internal/wsl/wsltest).
	WSL *wsl.Host
	// ScanTools say where the security scan's external tools are (decision P44). Default: osv-scanner and opengrep on PATH;
	// tests and the e2e harness point them at the labelled stand-ins (internal/scan/scantest).
	ScanTools scan.Paths
	// ScanRulesDir is the Opengrep rules folder a project uses until a person chooses one. The product leaves it empty
	// (Drydock downloads no rules); the e2e harness gives its stand-in a folder.
	ScanRulesDir string
	// PickFile opens the window's own file dialog and returns the file the person chose ("" when they cancelled). The desktop
	// app sets it (app/app_windows.go); without it a person types a full path instead. The e2e harness sets a labelled
	// stand-in.
	PickFile func(ctx context.Context, p FilePick) (string, error)
	// PickFolder opens the window's own folder dialog and returns the folder the person chose ("" when they cancelled): the
	// new project's "Choose folder..." (decision P58). The desktop app sets it (app/app_windows.go); without it a person types
	// the path. The e2e harness sets a labelled stand-in.
	PickFolder func(ctx context.Context, p FolderPick) (string, error)
	// OpenURL opens a web or mail address in the person's own browser or mail program (audit 2026-10-16, M23): a link in the window
	// does not open inside the app. The desktop app sets it (app/app_windows.go); the address has passed ExternalURL by then. Without
	// it (the browser harness) the page uses the browser's own link.
	OpenURL func(ctx context.Context, rawURL string) error
	// OpenSandboxApp shows a forwarded port of a task's sandbox in a window of Drydock's own, in a process of its own with a
	// storage partition of its own (decision P134). The desktop app sets it (app/sandboxview_windows.go); without it (the browser
	// harness) the page says the viewer is not available, and never opens the address in a browser.
	OpenSandboxApp func(ctx context.Context, app SandboxApp) error
	// Restart starts Drydock again and quits this one: "Restart now" after a staged restore (decision P116). The desktop app sets
	// it (app/restart_windows.go); without it (the browser harness) the page says to quit and reopen.
	Restart func(ctx context.Context) error
	// NoticesFile is the third-party notices the build ships (THIRD_PARTY_NOTICES.md), which Settings > About shows (decision
	// P129). Empty: the file beside the executable, where the installer puts it (packaging/windows/drydock.nsi).
	NoticesFile string
	// WebView2Version is the WebView2 runtime the window runs on, for the support bundle; the desktop app sets it.
	WebView2Version func() string
	// DevDrives lists this computer's Dev Drives, or says why none was found, for the project folder's hint (decision P58) and
	// the CI runners' Windows folder (decision P56). Default: runners.DevDrives on Windows (asked once, no administrator rights
	// needed), none elsewhere; tests and the e2e harness set a labelled stand-in.
	DevDrives func(ctx context.Context) runners.DevDriveScan
	// Runners, when set, adjusts the CI runners' pool before it starts (decision P56): tests and the e2e harness replace
	// GitHub, the release and the launchers with labelled test doubles.
	Runners func(p *runners.Pool)
	// LocalModels, when set, adjusts the local models' manager before it starts (decision P65): tests and the e2e harness
	// replace GitHub, the Hugging Face Hub, the download client and the server program with labelled test doubles.
	LocalModels func(m *localllm.Manager)
	// ScanInstall, when set, adjusts the security scan's tool installer before it is used (decision P66): tests and the e2e
	// harness replace GitHub, the download client and the installed program with labelled test doubles.
	ScanInstall func(i *scan.Installer)
	// BundleDir is the tools folder the download carries beside drydock.exe (decision P77): osv-scanner, Opengrep and a
	// llama.cpp build with the TurboQuant KV cache types, each with the SHA-256 its release recorded, installed from there at
	// start. The desktop app sets it (bundle.Dir()); empty, or a folder without bundle.json, carries nothing.
	BundleDir string
	// AutoSetup makes the app set itself up (decision P95, on the owner's words: "I don't want to hit a button that says
	// install llama.cpp"): at start, a scan tool or the local models' runtime that is neither installed (nor bundled) nor
	// removed by a person is downloaded and installed, and the twice-daily update check installs a newer scan tool release
	// instead of only offering it. The desktop app sets it; tests leave it off so nothing installs behind their back.
	AutoSetup bool
	// CrashOutput is the file this process's crash output goes to (internal/crashreport.Arm, set by the desktop app before the
	// core opens). At start the core turns every other such file that has text, the ones earlier runs died writing, into a crash
	// report; empty leaves it alone except for not collecting others' files as its own.
	CrashOutput string
}

// FilePick is what a file dialog asks for.
type FilePick struct {
	Title         string
	FilterName    string // for example "Programs"
	FilterPattern string // semicolon-separated globs, for example "*.exe"
}

// FolderPick is what a folder dialog asks for.
type FolderPick struct {
	Title string
}

// Core is the running application.
type Core struct {
	cfg         Config
	fwdCache    forwardCache // what sandbox.forward.list last asked the engine, for a few seconds
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

	lock *datalock.Lock // this process's claim on the data folder
	// budgets is the budget store once the economics extension created it; budgetGate says its middleware is installed on agent
	// turns. Open installs a gate that refuses every turn when it is not (budget.go, audit 2026-10-16, M4).
	budgets    atomic.Pointer[budget.Store]
	budgetGate atomic.Bool
	extMethods map[string]Method
	extClose   []func()
	mcpw       atomic.Pointer[mcpWiring] // the shared MCP gateway (ext_mcp.go)
	permOnce   sync.Once                 // the permission prompt service (ext_permprompt.go)
	perm       *permissionService
	lead       atomic.Pointer[pipeline.Lead] // the team lead service (ext_lead.go)
	secrets    providers.SecretStore         // API keys and tokens (Credential Manager plus an encrypted overflow); nil when the host has no store
	wsl        *wsl.Host                     // the WSL distros (ext_wsl.go)
	rmOnce     sync.Once                     // the repo map (ext_repomap.go)
	rm         *repomap.Mapper
	devOnce    sync.Once // this PC's Dev Drives, asked once for the folder hint and the CI runners
	dev        runners.DevDriveScan
	// customMu guards customLoaded: per person's own CLI, the SHA-256 of the manifest file its adapter was built from
	// (ext_customcli.go, decision P117)
	customMu     sync.Mutex
	customLoaded map[string]string

	mu       sync.Mutex
	adapters map[string]agents.Adapter
	// given are the adapters Config.Adapters put in place of the built ones: a Connection check reads the installed CLI, not
	// them, so it says nothing about what their agents start
	given map[string]bool
	repos map[string]*git.Repo
	// projHooks are the extensions' parts in deleting a project (project_delete.go)
	projHooks projectHooks
	// taskStarts serializes starting a task's run per task (lockTaskStart): a double click or the scheduler racing the
	// person must not start two runs on one worktree
	taskStarts sync.Map
	// limits are the rate limits the agent CLIs reported (agentlimits.go); the turn router reads them
	limits agentLimits
	// methods is the registry Call dispatches through, built once after the extensions started (nil before)
	methods atomic.Pointer[map[string]Method]
	// onCall, when set, is told the name of every method Call dispatches (the usage counters, decision P121)
	onCall atomic.Pointer[func(name string)]
	cancel context.CancelFunc
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
	if cfg.BackupFirstCheck < 0 {
		return nil, fmt.Errorf("core: BackupFirstCheck is %v; it must be zero (the default) or positive", cfg.BackupFirstCheck)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	// One process owns a data folder. Opening the core marks the agent runs the last stop left open as failed and takes over the
	// runs whose owner is gone: done by a second process (a second launch, the updater's helper) it would do that to the live
	// runs of the first (audit 2026-10-16, H10).
	lock, err := datalock.Acquire(cfg.DataDir)
	if err != nil {
		if errors.Is(err, datalock.ErrHeld) {
			return nil, fmt.Errorf("Drydock is already running with this data folder (%s): %w", cfg.DataDir, err)
		}
		return nil, err
	}
	locked := false
	defer func() {
		if !locked {
			lock.Release()
		}
	}()
	applyPendingRestore(ctx, cfg.DataDir) // a restore staged by backup.restore is applied BEFORE the database is opened
	db, err := storage.Open(ctx, filepath.Join(cfg.DataDir, "drydock.db"))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	bg, cancel := context.WithCancel(context.Background())
	c := &Core{cfg: cfg, DB: db, cancel: cancel, adapters: map[string]agents.Adapter{}, repos: map[string]*git.Repo{}}
	acked, err := c.loadGateAcks(ctx)
	if err != nil {
		cancel()
		_ = db.Close()
		return nil, err
	}
	// Checks, previews and the dependency scan get the same extra names agents get (the person's list in settings).
	checks.PassEnv = func() []string { return passEnvNames(context.Background(), db) }
	c.wsl = cfg.WSL
	if c.wsl == nil {
		c.wsl = &wsl.Host{PM: cfg.Procs}
	}
	// Adapters come from the embedded manifests (Claude headless; Codex, Gemini, OpenCode and Goose over ACP; Antigravity is
	// declared but gated). An ACP agent's permission requests are refused unless its session has an asker: worker turns get
	// one from Runtime.Permissions (ext_permprompt.go), chats ask in the chat.
	c.Connections, err = connections.NewService(connections.Config{
		Manifests: manifests.Options{ScrubEnv: true, PassEnv: func() []string { return passEnvNames(context.Background(), db) }, WSL: c.wsl},
		Detection: connections.Options{PM: cfg.Procs, Acked: acked, Last: c.loadTestOutcomes(ctx), Drift: c.loadDrift(ctx), WSL: c.wsl},
	})
	if err != nil {
		cancel()
		_ = db.Close()
		return nil, fmt.Errorf("connections: %w", err)
	}
	built, failed := c.Connections.Registry.BuildAll()
	for id, e := range failed {
		c.logf("adapter %s unavailable: %v", id, e)
	}
	for k, v := range built {
		c.adapters[k] = v
	}
	c.given = map[string]bool{}
	for k, v := range cfg.Adapters {
		c.adapters[k] = v
		c.given[k] = true
	}
	bus := events.NewSQLBus(db, 4096)
	c.Bus = bus
	if c.Scanner, err = security.NewScanner(); err != nil {
		cancel()
		_ = db.Close()
		return nil, fmt.Errorf("secret scanner: %w", err)
	}
	// what agents and tools printed is scanned before an event is stored or delivered (security.md s8; audit 2026-10-16, H7)
	bus.SetRedactor(eventRedactor(c.Scanner.Redact))
	c.installPanicReporter()
	checks.Redact = c.Scanner.Redact
	// every message body is scanned before it is stored, so a secret pasted into the room never reaches the database, the
	// event log or another agent's context (security.md s8)
	c.Room = room.New(db, c.Bus, room.Options{
		Redact:  func(body string) (string, error) { return c.Scanner.Redact(body), nil },
		OnError: func(err error) { c.logf("room: %v", err) },
	})
	c.Tasks = &tasks.Service{DB: db, Bus: c.Bus, Redact: c.Scanner.Redact, Hooks: tasks.Hooks{
		OnCreate: func(ctx context.Context, t tasks.Task) {
			if _, err := c.Room.CreateTaskChannel(ctx, room.TaskChannelSpec{ProjectID: t.ProjectID, TaskID: t.ID, Number: t.Seq, Title: t.Title, CreatedBy: room.Human(cfg.User)}); err != nil {
				c.logf("task channel: %v", err)
			}
		},
		OnTerminal: func(ctx context.Context, t tasks.Task) {
			_ = c.Room.ArchiveTaskChannel(ctx, t.ID, string(t.Status))
			c.endTaskRules(ctx, t) // a person's "approve for this task" ends with the task
		},
	}}
	c.Context = &ctxeng.Engine{DB: db, Memory: memory.Store{DB: db}, Standing: c.briefStanding, Redact: func(s string) string { return c.Scanner.Redact(s) },
		// the project's package ceiling (Development flow settings, P119): AGENTS.md keeps within its share of it
		Ceiling: func(ctx context.Context, projectID string) int {
			return pipeline.LoadDevSettings(ctx, db, projectID).PackageCeiling()
		}}
	c.Runtime = &rt.Runtime{PM: cfg.Procs, Bus: c.Bus, DB: db, Adapters: c.adapters, NudgeWait: 5 * time.Second, MCP: c.mcpAttach, Permissions: c.permissionAsker, Instructions: c.agentInstructions,
		RunMode: c.agentRunMode, Redact: c.Scanner.Redact, RepoConfigTrusted: c.agentConfigTrusted, StartGate: c.startGate,
		Grants: c.Room} // a turn in a room starts only on the room scheduler's grant (P118)
	c.Deps = pipeline.Deps{
		Tasks: c.Tasks, Runtime: c.Runtime, PM: cfg.Procs, Bus: c.Bus, Context: c.Context,
		Repo:         c.repo,
		WorktreeRoot: filepath.Join(cfg.DataDir, "worktrees"), DataDir: cfg.DataDir,
		AgentConfig:  c.agentConfig,
		OnCheck:      c.postCheck,
		OnTurn:       c.postTurn,
		OnFiles:      c.joinRulesAgain,
		MCPStatus:    c.mcpStatus,
		WorkerChecks: c.workerChecks,
		Sandbox:      sandboxHook{c},     // ext_sandbox.go
		ScanTools:    c.scanCheckOptions, // the `scan` check (ext_security_check.go, decision P131)
		Lead:         leadHook{c},
		ResetWatch:   c.resetWatch,
		ProjectBrief: c.ProjectBrief,
		Redact:       c.Scanner.Redact,
		PlanDir: func(ctx context.Context, projectID string) (string, error) {
			p, err := c.GetProject(ctx, projectID)
			if err != nil {
				return "", err
			}
			return c.projectDir(p)
		},
	}
	c.Deps.ContextWindow = c.modelWindow // sizes the occupancy estimate of a CLI that reports none (P128)
	c.Engine = workflow.New(workflow.Config{DB: db, Bus: c.Bus, DataDir: cfg.DataDir, LeaseTTL: cfg.LeaseTTL, SafePoint: pipeline.SafePointHook(c.Deps), Redact: c.Scanner.Redact})
	c.Watch = watchdog.New(watchdog.SQLStore{DB: db}, c.watchdogHooks(), watchdog.ForNode)
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
			_ = db.Close()
			return nil, err
		}
	}
	// Presence is rebuilt from the log on every start. No agent process survives a restart (they die with the app), so nobody is
	// running yet, and what an agent was last doing is not worth reading a month of tool output for: the last two days of
	// lifecycle events say which agents failed or need a person, and the rest are shown as stopped (audit 2026-10-16, M15).
	c.Presence = room.NewTracker(c.Bus, room.TrackerOptions{EmitEvery: time.Second, Window: 48 * time.Hour, LifecycleOnly: true,
		Alive: func(string) bool { return false }, Redact: c.Scanner.Redact})
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
	c.secrets = secrets
	c.Providers = providers.NewRegistry(db, providers.RegistryOptions{Secrets: secrets, Bus: c.Bus})
	c.Prober = providers.NewProber(c.Providers, c.Bus)
	c.Health = providers.NewMonitor(c.Providers, c.Prober, c.Bus, providers.MonitorOptions{})
	c.Health.Start(bg)
	safe.Go("core.Open", func() { _ = c.Room.WatchTasks(bg) })
	if _, err := c.Room.Reconcile(ctx); err != nil {
		c.logf("room reconcile: %v", err)
	}
	if err := c.backfillProjectKeys(ctx); err != nil { // the projects made before migration 00129
		c.logf("project keys: %v", err)
	}
	if n, err := c.Runtime.CloseStale(ctx); err != nil {
		c.logf("close stale agent runs: %v", err)
	} else if n > 0 {
		c.logf("closed %d agent run(s) the last stop left open", n)
	}
	if err := c.startExtensions(bg); err != nil {
		c.logf("extensions: %v", err)
	}
	if !c.budgetGate.Load() {
		// the budgets did not start, so nothing limits what agents spend: no agent turn runs until they do
		c.logf("the budget gate did not start: agent turns are refused until it does")
		c.Engine.Use(workflow.KindAgentTask, budget.FailClosed("the budget gate did not start (see the log), so no agent turn runs"))
		c.Engine.Use(workflow.KindGroup, budget.FailClosed("the budget gate did not start (see the log), so no group discussion runs"))
	}
	// the method registry is complete once the extensions have started: build it once for Call
	m := c.Methods()
	c.methods.Store(&m)
	if n, err := c.Engine.Recover(ctx); err != nil {
		c.logf("recover: %v", err)
	} else if n > 0 {
		c.logf("resumed %d interrupted run(s)", n)
	}
	// a run whose last owner's lease was still valid at start (killed and restarted within the lease TTL) is taken over
	// as soon as that lease expires
	safe.Go("core.Open#2", func() { c.Engine.WatchRecover(bg, 0) })
	c.lock, locked = lock, true
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
	_ = c.DB.Close()
	c.lock.Release() // last: until the database is closed this folder is still ours
}

func (c *Core) logf(format string, a ...any) {
	// <data>/logs gets the line scrubbed (ext_packaging.go), and so does stderr: a CI log or a terminal is a log too
	fmt.Fprintf(os.Stderr, "drydock: %s\n", c.fileLog(fmt.Sprintf(format, a...)))
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

// Project is the work Drydock does for one product: its team, tasks and room, and (once attached) the local Git repository the
// agents work in. A project can start without a repository: the team can plan and talk, and nothing that changes code runs
// until a repository is attached (AttachRepository).
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Key is the prefix of the project's task identifiers: GOC for "GOC-41" (projectkey.go). Unique; the person can change it.
	Key           string `json:"key"`
	RepoPath      string `json:"repoPath"` // empty: no repository attached yet
	HasRepo       bool   `json:"hasRepo"`
	DefaultBranch string `json:"defaultBranch"`
	MergeStrategy string `json:"mergeStrategy"`
	Mode          string `json:"mode"` // local_merge | pr_flow
	// Brief is the human's description of the project (what it is for, how the team should work); every agent of the project
	// is given it as standing context.
	Brief   string `json:"brief"`
	Created int64  `json:"created"`
}

// MaxBriefLen bounds the project brief: it is added to agent prompts, so every character is paid for on many turns.
const MaxBriefLen = 8000

// ErrNoRepository is returned by everything that needs the project's repository while none is attached.
var ErrNoRepository = errors.New("this project has no repository yet: attach one in the project's settings first")

const projectCols = `id,name,key,repo_path,default_branch,merge_strategy,mode,brief,created_at`

func scanProject(sc interface{ Scan(...any) error }) (Project, error) {
	var p Project
	err := sc.Scan(&p.ID, &p.Name, &p.Key, &p.RepoPath, &p.DefaultBranch, &p.MergeStrategy, &p.Mode, &p.Brief, &p.Created)
	p.HasRepo = p.RepoPath != ""
	if !p.HasRepo {
		p.DefaultBranch = ""
	}
	return p, err
}

// openProjectRepo validates a local repository for a project: it must be a Git repository with a checked-out branch.
func (c *Core) openProjectRepo(ctx context.Context, repoPath string) (root, branch string, err error) {
	if err := needFullPath("the repository folder", repoPath); err != nil {
		return "", "", err
	}
	repo, err := c.openRepo(ctx, repoPath)
	if err != nil {
		return "", "", fmt.Errorf("%s is not a usable Git repository: %w", repoPath, err)
	}
	branch, err = repo.CurrentBranch(ctx)
	if err != nil || branch == "" || branch == "HEAD" {
		return "", "", fmt.Errorf("the repository at %s has no checked-out branch: commit once on a branch first", repoPath)
	}
	return repo.Root, branch, nil
}

// needFullPath refuses a relative path: it would be resolved against the folder Drydock happens to run in, not one the
// person chose.
func needFullPath(what, path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s must be a full path (for example C:\\Users\\you\\code\\app), not %q", what, path)
	}
	return nil
}

// CreateProject creates a project and its default channels. repoPath names a local Git repository, or is empty for a project
// whose repository is attached later (then the name is required).
func (c *Core) CreateProject(ctx context.Context, name, repoPath string) (Project, error) {
	if err := c.RequireLicensed(ctx, "create_project"); err != nil {
		return Project{}, err
	}
	name, repoPath = strings.TrimSpace(name), strings.TrimSpace(repoPath)
	root, branch := "", ""
	if repoPath != "" {
		var err error
		if root, branch, err = c.openProjectRepo(ctx, repoPath); err != nil {
			return Project{}, err
		}
		if name == "" {
			name = filepath.Base(root)
		}
	}
	if name == "" {
		return Project{}, errors.New("a project needs a name (or a repository to take the name from)")
	}
	if len(name) > 200 {
		return Project{}, errors.New("a project name is at most 200 characters")
	}
	p := Project{ID: ids.New("prj"), Name: name, RepoPath: root, HasRepo: root != "", DefaultBranch: branch, MergeStrategy: "merge", Mode: "local_merge", Created: storage.Now()}
	err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		// the key is chosen in the transaction that inserts, so two projects made at once cannot both take it
		taken, err := takenKeys(ctx, tx, "")
		if err != nil {
			return err
		}
		if p.Key, err = deriveProjectKey(p.Name, taken); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO projects(id,name,key,repo_path,default_branch,merge_strategy,mode,created_at) VALUES(?,?,?,?,?,?,?,?)`,
			p.ID, p.Name, p.Key, p.RepoPath, p.DefaultBranch, p.MergeStrategy, p.Mode, p.Created)
		return err
	})
	if err != nil {
		return Project{}, err
	}
	if _, err := c.Room.EnsureDefaultChannels(ctx, p.ID, room.Human(c.cfg.User)); err != nil {
		return p, fmt.Errorf("project created but channels failed: %w", err)
	}
	if p.HasRepo {
		c.trustGitConfigNow(ctx, p.ID)
	}
	c.publish(ctx, "project.created", p.ID, map[string]any{"name": p.Name, "hasRepo": p.HasRepo})
	c.placeNewProject(ctx, p.ID) // where its agents run: on Windows, or where the agent CLI is ready (decision P58)
	return p, nil
}

// trustGitConfigNow records the repository's git configuration as trusted the moment the person gives the project its
// repository (create, attach, import). Recorded later, on the repository's first use, a setting planted in between would be
// taken as trusted without anyone seeing it. A failure is logged: the state is then recorded on first use, as before.
func (c *Core) trustGitConfigNow(ctx context.Context, projectID string) {
	if _, err := c.repo(ctx, projectID); err != nil {
		c.logf("project %s: recording the trusted git configuration: %v", projectID, err)
	}
}

// AttachRepository links a local Git repository to a project that has none yet. A project's repository is not swapped once
// attached: task branches, worktrees and merges belong to it.
func (c *Core) AttachRepository(ctx context.Context, projectID, repoPath string) (Project, error) {
	if err := c.RequireLicensed(ctx, "create_project"); err != nil {
		return Project{}, err
	}
	p, err := c.GetProject(ctx, projectID)
	if err != nil {
		return p, err
	}
	if p.HasRepo {
		return p, fmt.Errorf("this project already works in %s; a project's repository cannot be swapped (create another project instead)", p.RepoPath)
	}
	if strings.TrimSpace(repoPath) == "" {
		return p, errors.New("choose a local Git repository to attach")
	}
	root, branch, err := c.openProjectRepo(ctx, strings.TrimSpace(repoPath))
	if err != nil {
		return p, err
	}
	res, err := c.DB.Writer().ExecContext(ctx, `UPDATE projects SET repo_path=?, default_branch=?, version=version+1 WHERE id=? AND repo_path=''`, root, branch, projectID)
	if err != nil {
		return p, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return p, errors.New("the project's repository was attached by someone else in the meantime")
	}
	c.mu.Lock()
	delete(c.repos, projectID)
	c.mu.Unlock()
	c.trustGitConfigNow(ctx, projectID)
	c.publish(ctx, "project.updated", projectID, map[string]any{"repoAttached": true})
	return c.GetProject(ctx, projectID)
}

// SetBrief stores the human's project brief.
func (c *Core) SetBrief(ctx context.Context, projectID, brief string) (Project, error) {
	brief = strings.TrimSpace(brief)
	if len(brief) > MaxBriefLen {
		return Project{}, fmt.Errorf("the project brief is at most %d characters (it is given to every agent on every turn)", MaxBriefLen)
	}
	if _, err := c.GetProject(ctx, projectID); err != nil {
		return Project{}, err
	}
	brief = c.Scanner.Redact(brief) // agents read it: a pasted secret must not reach them
	if _, err := c.DB.Writer().ExecContext(ctx, `UPDATE projects SET brief=?, version=version+1 WHERE id=?`, brief, projectID); err != nil {
		return Project{}, err
	}
	c.publish(ctx, "project.updated", projectID, map[string]any{"brief": true})
	return c.GetProject(ctx, projectID)
}

// ProjectBrief returns the project's brief (empty when there is none or the project is unknown).
func (c *Core) ProjectBrief(ctx context.Context, projectID string) string {
	p, err := c.GetProject(ctx, projectID)
	if err != nil {
		return ""
	}
	return p.Brief
}

// projectDir is where an agent works when it is not on a task: the repository, or for a project without one a private
// folder in the data directory (so a planning chat never runs in the app's own working directory).
func (c *Core) projectDir(p Project) (string, error) {
	if p.HasRepo {
		return p.RepoPath, nil
	}
	dir := filepath.Join(c.cfg.DataDir, "projects", p.ID, "workspace")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func (c *Core) publish(ctx context.Context, typ, projectID string, data any) {
	b, _ := json.Marshal(data)
	_, _ = c.Bus.Publish(ctx, events.Event{Type: typ, ProjectID: projectID, Data: b})
}

// Projects lists projects, newest first.
func (c *Core) Projects(ctx context.Context) ([]Project, error) {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT `+projectCols+` FROM projects ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProject returns one project.
func (c *Core) GetProject(ctx context.Context, id string) (Project, error) {
	p, err := scanProject(c.DB.Reader().QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, fmt.Errorf("project %s not found", id)
	}
	return p, err
}

// repo returns the project's repository, opened once. Opening runs git, so it happens outside c.mu: a repository on a slow
// or hung drive must not stall every other project behind the lock.
func (c *Core) repo(ctx context.Context, projectID string) (*git.Repo, error) {
	c.mu.Lock()
	r, ok := c.repos[projectID]
	c.mu.Unlock()
	if ok {
		return r, nil
	}
	p, err := c.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if !p.HasRepo {
		return nil, ErrNoRepository
	}
	r, err = c.openRepo(ctx, p.RepoPath)
	if err != nil {
		return nil, err
	}
	if err := c.restoreGitTrust(ctx, projectID, r); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if prev, ok := c.repos[projectID]; ok {
		return prev, nil // another caller opened it meanwhile; one Repo per project keeps one trusted state
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
	Purpose        string `json:"purpose"`
	// Practices says whether the Claude best-practices text (internal/agents/claude/practices.go) is added to the agent's system
	// prompt. It only has an effect for the claude adapter.
	Practices bool   `json:"practices"`
	Status    string `json:"status"`
	Archived  bool   `json:"archived,omitempty"`
	// RouteModels are the models the model router may choose between for this agent's turns. Empty: always Model.
	RouteModels []RouteModel `json:"routeModels"`
	// RunMode is where the agent's CLI runs (decision P41): "" follows the project's default, "native" on Windows, "wsl"
	// inside the WSL distro WSLDistro.
	RunMode   string `json:"runMode"`
	WSLDistro string `json:"wslDistro"`
}

// RouteModel is one model the router may choose for an agent, with the tier the router's ladder treats it as.
type RouteModel struct {
	Model string `json:"model"`
	Tier  string `json:"tier"` // cheap | premium
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
	Purpose        string `json:"purpose"`
	// Practices turns the Claude best-practices text on or off; omitted means on.
	Practices *bool `json:"practices"`
	// RunMode and WSLDistro: where the CLI runs ("" follows the project, "native", or "wsl" with a distro).
	RunMode   string `json:"runMode"`
	WSLDistro string `json:"wslDistro"`
	// ConfirmSkipsApprovals is the person's explicit confirmation of a permission mode that lets the agent act without asking
	// (bypassPermissions, auto, dontAsk); without it such a mode is refused (NeedsConfirmationError).
	ConfirmSkipsApprovals bool `json:"confirmSkipsApprovals,omitempty"`
}

// CreateAgent adds an agent to the team. The adapter must be one Drydock knows.
func (c *Core) CreateAgent(ctx context.Context, s AgentSpec) (Agent, error) {
	if err := c.RequireLicensed(ctx, "create_agent"); err != nil {
		return Agent{}, err
	}
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
	if err := c.validateAgentFields(s.Adapter, s.Model, s.Effort, s.Purpose); err != nil {
		return Agent{}, err
	}
	s.Model, s.Effort, s.Purpose = strings.TrimSpace(s.Model), strings.TrimSpace(s.Effort), strings.TrimSpace(s.Purpose)
	if s.PermissionMode == "" {
		s.PermissionMode = "acceptEdits" // headless agents cannot answer prompts; edits inside the worktree are the job
	}
	if err := c.checkPermissionMode(s.Adapter, s.PermissionMode); err != nil {
		return Agent{}, err
	}
	var err error
	if s.RunMode, s.WSLDistro, err = c.checkRunMode(ctx, s.ProjectID, s.Adapter, s.RunMode, s.WSLDistro); err != nil {
		return Agent{}, err
	}
	agentID := ids.New("agt")
	if err := c.confirmSkip(ctx, s.ProjectID, agentID, "this agent", "", s.PermissionMode, s.ConfirmSkipsApprovals); err != nil {
		return Agent{}, err
	}
	a := Agent{ID: agentID, ProjectID: s.ProjectID, Name: s.Name, Adapter: s.Adapter, Role: s.Role, Model: s.Model, Effort: s.Effort, ProviderID: s.ProviderID, PermissionMode: s.PermissionMode, Purpose: s.Purpose, Practices: s.Practices == nil || *s.Practices, Status: "STOPPED",
		RunMode: s.RunMode, WSLDistro: s.WSLDistro}
	err = c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO agents(id,project_id,name,adapter,role,model,effort,provider_id,permission_mode,purpose,practices,run_mode,wsl_distro,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			a.ID, a.ProjectID, a.Name, a.Adapter, a.Role, a.Model, a.Effort, a.ProviderID, a.PermissionMode, a.Purpose, b2i(a.Practices), a.RunMode, a.WSLDistro, storage.Now())
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
	return c.agentsWhere(ctx, `project_id=? AND archived_at=0 ORDER BY created_at, id`, projectID)
}

// AllAgents lists every project's team (the workspace's crew), by project, then as Agents orders them.
func (c *Core) AllAgents(ctx context.Context) ([]Agent, error) {
	return c.agentsWhere(ctx, `archived_at=0 ORDER BY project_id, created_at, id`)
}

func (c *Core) agentsWhere(ctx context.Context, where string, args ...any) ([]Agent, error) {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT id,project_id,name,adapter,role,model,effort,provider_id,permission_mode,purpose,practices,status,archived_at,route_models,run_mode,wsl_distro FROM agents WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Agent{}
	for rows.Next() {
		var a Agent
		var archived, practices int64
		var routes string
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.Name, &a.Adapter, &a.Role, &a.Model, &a.Effort, &a.ProviderID, &a.PermissionMode, &a.Purpose, &practices, &a.Status, &archived, &routes, &a.RunMode, &a.WSLDistro); err != nil {
			return nil, err
		}
		a.Archived, a.Practices = archived != 0, practices != 0
		a.RouteModels = parseRouteModels(routes)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (c *Core) agent(ctx context.Context, id string) (Agent, error) {
	var a Agent
	var archived, practices int64
	var routes string
	err := c.DB.Reader().QueryRowContext(ctx, `SELECT id,project_id,name,adapter,role,model,effort,provider_id,permission_mode,purpose,practices,status,archived_at,route_models,run_mode,wsl_distro FROM agents WHERE id=?`, id).
		Scan(&a.ID, &a.ProjectID, &a.Name, &a.Adapter, &a.Role, &a.Model, &a.Effort, &a.ProviderID, &a.PermissionMode, &a.Purpose, &practices, &a.Status, &archived, &routes, &a.RunMode, &a.WSLDistro)
	a.Archived, a.Practices = archived != 0, practices != 0
	a.RouteModels = parseRouteModels(routes)
	return a, err
}

// parseRouteModels reads the stored route_models JSON; anything unreadable means none.
func parseRouteModels(s string) []RouteModel {
	out := []RouteModel{}
	if s != "" {
		_ = json.Unmarshal([]byte(s), &out)
	}
	return out
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
	spec := c.agentRunSpec(context.Background(), a)
	if role == "review" {
		spec.PermissionMode, spec.ReadOnly = "plan", true // the reviewer reads; it never edits (the prompt also says so)
		spec.Role = "reviewer"
	}
	return rt.Config{AgentID: a.ID, Adapter: a.Adapter, Spec: spec, Provider: providerLabel(a), Tier: c.agentTier(a)}
}

// agentRunSpec is the run spec of a pipeline turn by agent a: its role, model, effort and permission mode, and where its CLI
// runs (its own run mode, else its project's; decision P41). Every pipeline config (worker, reviewer, template role, planner)
// starts from it, so a turn's checks (the sandbox+WSL refusal) see the run mode the runtime will start the CLI in.
func (c *Core) agentRunSpec(ctx context.Context, a Agent) agents.RunSpec {
	spec := agents.RunSpec{AgentID: a.ID, Role: a.Role, Model: a.Model, Effort: a.Effort, PermissionMode: a.PermissionMode}
	spec.RunMode, spec.WSLDistro, spec.RunModeWhy = c.runPlacement(ctx, a)
	return spec
}

// workerChecks is Deps.WorkerChecks: a Claude worker may run the project's check commands when the project switched that on.
// Only Claude Code honors exact allow rules (docs/worker-commands.md), so other CLIs never get them.
func (c *Core) workerChecks(ctx context.Context, t tasks.Task, cfg rt.Config) bool {
	return cfg.Adapter == "claude" && pipeline.LoadDevSettings(ctx, c.DB, t.ProjectID).WorkerChecks
}

// agentTier is the cost tier of an agent's usage rows: the registered provider's tier when the agent runs on one (so a
// cheap OpenRouter model is counted as cheap), premium for a subscription CLI that has no provider.
func (c *Core) agentTier(a Agent) string {
	if a.ProviderID != "" && c.Providers != nil {
		if p, err := c.Providers.Get(context.Background(), a.ProviderID); err == nil && p.Tier != "" {
			return string(p.Tier)
		}
	}
	return "premium"
}

// modelWindow is pipeline.Deps.ContextWindow (decision P128): the context length the provider catalog gives a turn's model, on
// the agent's own provider when it has one, else the smallest any provider lists for that model (an estimate held to the smaller
// window errs toward a fresh session); 0 when no catalog row knows the model.
func (c *Core) modelWindow(ctx context.Context, cfg rt.Config) int64 {
	if cfg.Spec.Model == "" {
		return 0
	}
	if a, err := c.agent(ctx, cfg.AgentID); err == nil && a.ProviderID != "" && c.Providers != nil {
		if m, err := c.Providers.Model(ctx, a.ProviderID, cfg.Spec.Model); err == nil && m.ContextLength > 0 {
			return int64(m.ContextLength)
		}
	}
	var n sql.NullInt64
	_ = c.DB.Reader().QueryRowContext(ctx, `SELECT MIN(context_length) FROM model_catalog WHERE model=? AND context_length>0`, cfg.Spec.Model).Scan(&n)
	return n.Int64
}

func providerLabel(a Agent) string {
	if a.ProviderID != "" {
		return a.ProviderID
	}
	return a.Adapter
}

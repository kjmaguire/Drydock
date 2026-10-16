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

	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/sandbox"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The per-task container sandbox (docs/sandbox.md). A project switches it on (a human decision, off by default, never carried
// by an import because the other machine may have no container engine); its definition is .drydock/sandbox.json on the
// project's default branch, so an agent's change to it takes effect only after a human merged it. With it on, a Claude worker
// turn starts without any command tool (restricted mode) and runs its commands in the task's container through the gateway's
// sandbox tools, and the project's checks run there too. The container, its services and its networks go away when the task
// ends.

func init() {
	RegisterExtension(Extension{Name: "sandbox", After: []string{"mcp"}, Start: startSandbox})
}

// settingSandbox + project id holds the switch.
const settingSandbox = "sandbox:"

// SandboxSwitch is a project's sandbox switch and who set it.
type SandboxSwitch struct {
	Enabled bool   `json:"enabled"`
	By      string `json:"by,omitempty"`
	At      int64  `json:"at,omitempty"` // unix milliseconds
}

func (c *Core) sandboxSwitch(ctx context.Context, projectID string) SandboxSwitch {
	var raw string
	if projectID == "" || c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingSandbox+projectID).Scan(&raw) != nil {
		return SandboxSwitch{}
	}
	var s SandboxSwitch
	if json.Unmarshal([]byte(raw), &s) != nil {
		return SandboxSwitch{}
	}
	return s
}

// SetSandbox switches a project's sandbox on or off. Only a human identity may.
func (c *Core) SetSandbox(ctx context.Context, projectID string, on bool, by string) (SandboxSwitch, error) {
	if !strings.HasPrefix(by, "human:") || len(by) == len("human:") {
		return SandboxSwitch{}, errors.New("sandbox: only a human may switch a project's sandbox")
	}
	if _, err := c.GetProject(ctx, projectID); err != nil {
		return SandboxSwitch{}, err
	}
	if names := c.projectWSLAgents(ctx, projectID); on && len(names) > 0 {
		// decision P41: the container runs the commands of an agent on Windows; an agent inside WSL would run its own there
		return SandboxSwitch{}, fmt.Errorf("the sandbox needs this project's agents to run on Windows, and %s run in WSL: run them natively first (their settings, or the project's run mode)", strings.Join(names, ", "))
	}
	s := SandboxSwitch{Enabled: on, By: by, At: time.Now().UnixMilli()}
	b, _ := json.Marshal(s)
	if err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingSandbox+projectID, string(b))
		return err
	}); err != nil {
		return SandboxSwitch{}, err
	}
	c.publish(ctx, "sandbox.switched", projectID, map[string]any{"enabled": on, "by": by})
	return s, nil
}

var sandboxRegistry sync.Map // *Core -> *sandbox.Manager

// sandboxes is the manager of this core's sandboxes, created on first use.
func (c *Core) sandboxes() *sandbox.Manager {
	if v, ok := sandboxRegistry.Load(c); ok {
		return v.(*sandbox.Manager)
	}
	m := sandbox.NewManager(sandbox.NewEngine(), filepath.Join(c.cfg.DataDir, "sandbox-logs"))
	v, _ := sandboxRegistry.LoadOrStore(c, m)
	return v.(*sandbox.Manager)
}

// SandboxConfig reads a project's sandbox definition from its default branch. found is false when the file is not there.
func (c *Core) SandboxConfig(ctx context.Context, projectID string) (cfg sandbox.Config, branch string, found bool, err error) {
	p, err := c.GetProject(ctx, projectID)
	if err != nil {
		return cfg, "", false, err
	}
	if !p.HasRepo {
		return cfg, "", false, ErrNoRepository
	}
	repo, err := c.repo(ctx, projectID)
	if err != nil {
		return cfg, "", false, err
	}
	branch = p.DefaultBranch
	text, ok, err := repo.ShowFile(ctx, branch, sandbox.ConfigPath)
	if err != nil {
		return cfg, branch, false, err
	}
	if !ok {
		return cfg, branch, false, fmt.Errorf("the project has no %s on %s: add one (the project settings suggest a starting point)", sandbox.ConfigPath, branch)
	}
	cfg, err = sandbox.ParseConfig([]byte(text))
	return cfg, branch, true, err
}

// sandboxBox returns the task's running sandbox, creating it when needed.
func (c *Core) sandboxBox(ctx context.Context, t tasks.Task) (*sandbox.Box, error) {
	if !c.sandboxSwitch(ctx, t.ProjectID).Enabled {
		return nil, errors.New("the sandbox is off for this project")
	}
	if t.Worktree == "" {
		return nil, errors.New("the task has no worktree yet")
	}
	cfg, _, _, err := c.SandboxConfig(ctx, t.ProjectID)
	if err != nil {
		return nil, err
	}
	return c.sandboxes().Ensure(ctx, sandbox.Spec{TaskID: t.ID, ProjectID: t.ProjectID, Worktree: t.Worktree, Config: cfg})
}

// sandboxHook is pipeline.Deps.Sandbox.
type sandboxHook struct{ c *Core }

func (h sandboxHook) On(ctx context.Context, projectID string) bool {
	return h.c.sandboxSwitch(ctx, projectID).Enabled
}

func (h sandboxHook) Prepare(ctx context.Context, t tasks.Task) (string, error) {
	b, err := h.c.sandboxBox(ctx, t)
	if err != nil {
		return "", err
	}
	return h.c.sandboxes().Describe(ctx, b), nil
}

func (h sandboxHook) Check(ctx context.Context, t tasks.Task, s checks.Spec) (checks.Spec, error) {
	b, err := h.c.sandboxBox(ctx, t)
	if err != nil {
		return s, err
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	m := h.c.sandboxes()
	args, err := m.CommandArgs(b, s.Argv, s.Dir, append([]string{"CI=1", "NO_COLOR=1", "FORCE_COLOR=0"}, s.Env...), timeout)
	if err != nil {
		return s, err
	}
	s.Argv = append([]string{m.Engine.Bin}, args...)
	s.Env = nil
	s.Timeout = timeout + 30*time.Second // the container stops the command first
	return s, nil
}

// mcpSandbox is mcp.Workspace.Sandbox: the gateway's sandbox tools for a task's worker.
type mcpSandbox struct{ c *Core }

func (s mcpSandbox) box(ctx context.Context, run mcp.RunInfo) (*sandbox.Box, tasks.Task, error) {
	t, err := s.c.Tasks.Get(ctx, run.TaskID)
	if err != nil {
		return nil, t, err
	}
	b, err := s.c.sandboxBox(ctx, t)
	return b, t, err
}

func (s mcpSandbox) Run(ctx context.Context, run mcp.RunInfo, command, dir string, timeout time.Duration) (string, error) {
	b, t, err := s.box(ctx, run)
	if err != nil {
		return "", err
	}
	res, err := s.c.sandboxes().Exec(ctx, b, sandbox.ExecRequest{Command: command, Dir: dir, Timeout: timeout})
	if err != nil {
		return "", err
	}
	shown := command
	if s.c.Scanner != nil {
		shown = s.c.Scanner.Redact(shown)
	}
	if len(shown) > 300 {
		shown = shown[:300] + "..."
	}
	// on the task's and the agent's timeline, so the activity view and a task's log show what ran in its sandbox
	data, _ := json.Marshal(map[string]any{"command": shown, "exitCode": res.ExitCode, "timedOut": res.TimedOut, "durationMs": res.Duration.Milliseconds(), "bytes": res.Bytes})
	if _, err := s.c.Bus.Publish(ctx, events.Event{Type: "sandbox.command", ProjectID: t.ProjectID, TaskID: t.ID, AgentID: run.AgentID, RunID: run.RunID, Data: data}); err != nil {
		s.c.logf("sandbox: event: %v", err)
	}
	return res.Format(command), nil
}

func (s mcpSandbox) Start(ctx context.Context, run mcp.RunInfo, name, command, dir string) (string, error) {
	b, _, err := s.box(ctx, run)
	if err != nil {
		return "", err
	}
	out, err := s.c.sandboxes().Start(ctx, b, name, command, dir)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s (%s). Read its output with sandbox_logs and end it with sandbox_stop.", out, name), nil
}

func (s mcpSandbox) Logs(ctx context.Context, run mcp.RunInfo, name string, maxBytes int) (string, error) {
	b, _, err := s.box(ctx, run)
	if err != nil {
		return "", err
	}
	out, running, err := s.c.sandboxes().Logs(ctx, b, name, maxBytes)
	if err != nil {
		return "", err
	}
	state := "stopped"
	if running {
		state = "running"
	}
	if strings.TrimSpace(out) == "" {
		out = "(no output yet)"
	}
	return fmt.Sprintf("%s is %s. Its latest output:\n%s", name, state, out), nil
}

func (s mcpSandbox) Stop(ctx context.Context, run mcp.RunInfo, name string) (string, error) {
	b, _, err := s.box(ctx, run)
	if err != nil {
		return "", err
	}
	if err := s.c.sandboxes().Stop(ctx, b, name); err != nil {
		return "", err
	}
	return name + " stopped.", nil
}

func (s mcpSandbox) Info(ctx context.Context, run mcp.RunInfo) (string, error) {
	b, _, err := s.box(ctx, run)
	if err != nil {
		return "", err
	}
	return s.c.sandboxes().Describe(ctx, b), nil
}

// errSandboxedHere refuses to start a project's own code on this machine while the project's sandbox is on (the e2e check and
// the preview on demand run the project's code; in a sandboxed project the worker runs them in the container).
var errSandboxedHere = errors.New("this project runs its commands in a sandbox: the e2e check and the preview do not run on this machine; the worker runs them in the sandbox (sandbox_run)")

// SandboxTools are the gateway tools a sandboxed worker turn gets.
var SandboxTools = []string{"sandbox_info", "sandbox_logs", "sandbox_run", "sandbox_start", "sandbox_stop"}

// SandboxStatus is what the project settings show.
type SandboxStatus struct {
	Switch     SandboxSwitch   `json:"switch"`
	Engine     *sandbox.Info   `json:"engine,omitempty"`
	EngineErr  string          `json:"engineError,omitempty"`
	Found      bool            `json:"found"`
	Branch     string          `json:"branch,omitempty"`
	Config     *sandbox.Config `json:"config,omitempty"`
	ConfigErr  string          `json:"configError,omitempty"`
	Hosts      []string        `json:"hosts,omitempty"`
	Starter    string          `json:"starter,omitempty"` // a definition to start from, when the project has none
	EgressErr  string          `json:"egressError,omitempty"`
	ConfigPath string          `json:"configPath"`
}

func (c *Core) sandboxStatus(ctx context.Context, projectID string) (SandboxStatus, error) {
	if _, err := c.GetProject(ctx, projectID); err != nil {
		return SandboxStatus{}, err
	}
	st := SandboxStatus{Switch: c.sandboxSwitch(ctx, projectID), ConfigPath: sandbox.ConfigPath}
	ectx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if info, err := c.sandboxes().Info(ectx); err != nil {
		st.EngineErr = err.Error()
	} else {
		st.Engine = &info
	}
	cfg, branch, found, err := c.SandboxConfig(ctx, projectID)
	st.Branch, st.Found = branch, found
	switch {
	case err != nil && found:
		st.ConfigErr = err.Error()
	case err != nil:
		st.ConfigErr = err.Error()
		st.Starter = c.sandboxStarter(ctx, projectID, branch)
	default:
		st.Config, st.Hosts = &cfg, cfg.Hosts()
		if cfg.Network == sandbox.NetworkAllowlist && st.Engine != nil {
			if _, err := sandbox.EgressBinary(st.Engine.Arch); err != nil {
				st.EgressErr = err.Error()
			}
		}
	}
	return st, nil
}

// sandboxStarter suggests a definition from the files on the default branch.
func (c *Core) sandboxStarter(ctx context.Context, projectID, branch string) string {
	repo, err := c.repo(ctx, projectID)
	if err != nil || branch == "" {
		return ""
	}
	has := func(name string) bool {
		_, ok, err := repo.ShowFile(ctx, branch, name)
		return err == nil && ok
	}
	b, _ := json.MarshalIndent(sandbox.Starter(has), "", "  ")
	return string(b)
}

// SandboxTestStep is one step of the sandbox test.
type SandboxTestStep struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Detail   string `json:"detail,omitempty"`
	Duration int64  `json:"durationMs"`
}

// testSandbox starts the project's sandbox for an empty folder (without its setup commands), runs one command, and removes it:
// it shows that the engine works, the images can be pulled and the services start.
func (c *Core) testSandbox(ctx context.Context, projectID string) ([]SandboxTestStep, error) {
	var steps []SandboxTestStep
	step := func(name string, f func() (string, error)) bool {
		start := time.Now()
		detail, err := f()
		s := SandboxTestStep{Name: name, OK: err == nil, Detail: detail, Duration: time.Since(start).Milliseconds()}
		if err != nil {
			s.Detail = err.Error()
		}
		steps = append(steps, s)
		return err == nil
	}
	m := c.sandboxes()
	if !step("container engine", func() (string, error) {
		i, err := m.Info(ctx)
		return fmt.Sprintf("%s %s (%s/%s)", i.Client, i.Version, i.OS, i.Arch), err
	}) {
		return steps, nil
	}
	var cfg sandbox.Config
	if !step("definition", func() (string, error) {
		var err error
		var branch string
		cfg, branch, _, err = c.SandboxConfig(ctx, projectID)
		return sandbox.ConfigPath + " on " + branch, err
	}) {
		return steps, nil
	}
	cfg.Setup = nil // the test folder is empty: the project's setup belongs to a real task
	dir, err := os.MkdirTemp("", "drydock-sandbox-test-*")
	if err != nil {
		return steps, err
	}
	defer os.RemoveAll(dir)
	task := ids.New("sandbox-test-" + projectID)
	defer func() { _ = m.Remove(context.WithoutCancel(ctx), task) }()
	var box *sandbox.Box
	if !step("start (pulls missing images, starts the services)", func() (string, error) {
		var err error
		box, err = m.Ensure(ctx, sandbox.Spec{TaskID: task, ProjectID: projectID, Worktree: dir, Config: cfg})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s with %d service(s), network %s", cfg.Image, len(cfg.Services), cfg.Network), nil
	}) {
		return steps, nil
	}
	step("run a command", func() (string, error) {
		res, err := m.Exec(ctx, box, sandbox.ExecRequest{Command: "uname -sm && id -u && pwd", Timeout: time.Minute})
		if err != nil {
			return "", err
		}
		if res.ExitCode != 0 {
			return "", fmt.Errorf("exit code %d: %s", res.ExitCode, strings.TrimSpace(res.Output))
		}
		return strings.TrimSpace(res.Output), nil
	})
	return steps, nil
}

func startSandbox(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	bg, cancel := context.WithCancel(context.WithoutCancel(ctx))
	safe.Go("core.startSandbox", func() { c.watchSandboxTasks(bg) })
	safe.Go("core.startSandbox#2", func() { c.pruneSandboxes(bg) })
	m := map[string]Method{
		"sandbox.status": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			return c.sandboxStatus(ctx, p.ProjectID)
		}),
		"sandbox.set": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
			Enabled   bool   `json:"enabled"`
		}) (any, error) {
			return c.SetSandbox(ctx, p.ProjectID, p.Enabled, c.Human())
		}),
		"sandbox.test": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			steps, err := c.testSandbox(ctx, p.ProjectID)
			return map[string]any{"steps": steps}, err
		}),
	}
	return m, cancel, nil
}

// watchSandboxTasks removes a task's sandbox when the task completes, fails or is cancelled.
func (c *Core) watchSandboxTasks(ctx context.Context) {
	types := []string{"task.completed", "task.failed", "task.cancelled"}
	ch, cancel := c.Bus.Subscribe(events.Filter{Types: types})
	defer func() { cancel() }()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				ch, cancel = c.Bus.Subscribe(events.Filter{Types: types})
				continue
			}
			if e.TaskID == "" || !c.sandboxSwitch(ctx, e.ProjectID).Enabled {
				continue
			}
			rctx, rcancel := context.WithTimeout(ctx, 2*time.Minute)
			if err := c.sandboxes().Remove(rctx, e.TaskID); err != nil {
				c.logf("sandbox: removing the sandbox of %s: %v", e.TaskID, err)
			}
			rcancel()
		}
	}
}

// pruneSandboxes removes, at start, the sandboxes of tasks that ended or no longer exist (left by a crash or a closed
// window). It asks the engine only when some project uses a sandbox.
func (c *Core) pruneSandboxes(ctx context.Context) {
	var n int
	if c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key LIKE ? AND value LIKE '%"enabled":true%'`, settingSandbox+"%").Scan(&n) != nil || n == 0 {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	removed, err := c.sandboxes().Prune(pctx, func(taskID string) bool {
		t, err := c.Tasks.Get(pctx, taskID)
		return err == nil && t.Status != tasks.Completed && t.Status != tasks.Failed && t.Status != tasks.Cancelled
	})
	if err != nil {
		c.logf("sandbox: cleanup at start: %v", err)
	}
	if len(removed) > 0 {
		c.logf("sandbox: removed the sandboxes of %d finished task(s)", len(removed))
	}
}

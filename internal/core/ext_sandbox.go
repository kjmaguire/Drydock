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
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/sandbox"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
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
	// Trusted is the definition the person saw when they switched the sandbox on or confirmed a change. The definition on the
	// default branch is a file in the repository, and a merge can change it (the network, the allowed hosts, the image, the
	// setup commands): a definition that differs from this one does not run until the person confirms it (audit 2026-10-16, M7).
	Trusted *sandbox.Config `json:"trusted,omitempty"`
}

// SandboxUnconfirmedError is the refusal to run a definition the person has not confirmed.
type SandboxUnconfirmedError struct {
	Branch  string
	Changes []string
	// Fingerprint is the digest of the definition that was refused: the task page's "Confirm this definition" confirms exactly it.
	Fingerprint string
}

// SandboxGate says why the sandbox did not start, for the question the person answers (pipeline.SandboxGate, X38).
func (e *SandboxUnconfirmedError) SandboxGate() (string, string, []string) {
	return pipeline.SandboxUnconfirmed, e.Fingerprint, e.Changes
}

// sandboxGateError is a sandbox that did not start for a reason the question names (pipeline.SandboxGate).
type sandboxGateError struct {
	reason string
	err    error
}

func (e *sandboxGateError) Error() string                           { return e.err.Error() }
func (e *sandboxGateError) Unwrap() error                           { return e.err }
func (e *sandboxGateError) SandboxGate() (string, string, []string) { return e.reason, "", nil }

func (e *SandboxUnconfirmedError) Error() string {
	return fmt.Sprintf("the sandbox definition on %s (%s) is not the one you confirmed: %s. Confirming it, here or in the project's sandbox settings (or switching the sandbox off and on again), lets every task of the project that waits for it go on",
		e.Branch, sandbox.ConfigPath, strings.Join(e.Changes, "; "))
}

// trustedSandboxDefinition reads the definition on the default branch and returns it only when it is the one the person
// confirmed.
func (c *Core) trustedSandboxDefinition(ctx context.Context, projectID string) (sandbox.Config, string, error) {
	cfg, branch, _, err := c.SandboxConfig(ctx, projectID)
	if err != nil {
		return cfg, branch, err
	}
	sw := c.sandboxSwitch(ctx, projectID)
	if sw.Trusted != nil && sw.Trusted.Fingerprint() == cfg.Fingerprint() {
		return cfg, branch, nil
	}
	return cfg, branch, &SandboxUnconfirmedError{Branch: branch, Changes: sandbox.Diff(sw.Trusted, cfg), Fingerprint: cfg.Fingerprint()}
}

// TrustSandbox records that the person confirmed the definition whose digest is fingerprint, which must be the one on the default
// branch now: they confirm what they were shown, not whatever is there when the call arrives. Only a human may.
func (c *Core) TrustSandbox(ctx context.Context, projectID, fingerprint, by string) (SandboxSwitch, error) {
	if !strings.HasPrefix(by, "human:") || len(by) == len("human:") {
		return SandboxSwitch{}, errors.New("sandbox: only a human may confirm a project's sandbox definition")
	}
	sw := c.sandboxSwitch(ctx, projectID)
	if !sw.Enabled {
		return SandboxSwitch{}, errors.New("the sandbox is off for this project: switching it on confirms the definition")
	}
	cfg, _, _, err := c.SandboxConfig(ctx, projectID)
	if err != nil {
		return SandboxSwitch{}, err
	}
	if fingerprint == "" || cfg.Fingerprint() != fingerprint {
		return SandboxSwitch{}, errors.New("the definition changed since you looked at it: read it again before you confirm")
	}
	changes := sandbox.Diff(sw.Trusted, cfg)
	sw.Trusted = &cfg
	if err := c.saveSandboxSwitch(ctx, projectID, sw); err != nil {
		return SandboxSwitch{}, err
	}
	if err := auditAppend(ctx, c, "sandbox.trust", projectID, map[string]any{"fingerprint": fingerprint, "changes": changes}); err != nil {
		c.logf("audit sandbox.trust: %v", err)
	}
	c.publish(ctx, "sandbox.trusted", projectID, map[string]any{"by": by})
	c.answerSandboxQuestions(ctx, projectID, by)
	return sw, nil
}

// answerSandboxQuestions answers, as the person who just confirmed the project's sandbox definition (sandbox.trust, or switching
// the sandbox on), every question of the project's tasks that waits because the definition was not confirmed: their steps run
// again (effort review X38). One confirmation resumes every such task of the project, and the task page says so beside the
// button. Questions for other reasons (no engine, a broken definition) are not answered: confirming does not fix them. Nothing is
// answered in read-only (licence) mode, where a person cannot answer them either.
func (c *Core) answerSandboxQuestions(ctx context.Context, projectID, by string) int {
	if c.RequireLicensed(ctx, "approve") != nil {
		return 0
	}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT id, run_id, payload FROM approvals WHERE status='REQUESTED' AND kind='question' AND project_id=?`, projectID)
	if err != nil {
		return 0
	}
	type waiting struct{ id, run string }
	var ws []waiting
	for rows.Next() {
		var id, run string
		var payload []byte
		if rows.Scan(&id, &run, &payload) != nil {
			continue
		}
		var p struct {
			Gate   string `json:"gate"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal(payload, &p) == nil && p.Gate == "sandbox" && p.Reason == pipeline.SandboxUnconfirmed && run != "" {
			ws = append(ws, waiting{id, run})
		}
	}
	_ = rows.Close()
	n := 0
	data := []byte(`{}`) // the same answer as the task page's Try again
	for _, w := range ws {
		if err := c.Engine.Resume(ctx, workflow.RunID(w.run), workflow.Response{RequestID: workflow.RequestID(w.id), Decision: "answered", By: by, Data: data}); err != nil {
			c.logf("sandbox: resuming %s after the confirmation: %v", w.run, err)
			continue
		}
		n++
	}
	return n
}

func (c *Core) saveSandboxSwitch(ctx context.Context, projectID string, s SandboxSwitch) error {
	b, _ := json.Marshal(s)
	return c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingSandbox+projectID, string(b))
		return err
	})
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
	if on {
		// switching on is the person looking at the definition and accepting it; a definition that is not there yet, or does
		// not parse, is confirmed when it is
		if cfg, _, _, err := c.SandboxConfig(ctx, projectID); err == nil {
			s.Trusted = &cfg
		}
	}
	if err := c.saveSandboxSwitch(ctx, projectID, s); err != nil {
		return SandboxSwitch{}, err
	}
	c.publish(ctx, "sandbox.switched", projectID, map[string]any{"enabled": on, "by": by})
	if on && s.Trusted != nil {
		c.answerSandboxQuestions(ctx, projectID, by) // switching on confirms the definition
	}
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
	cfg, _, err := c.trustedSandboxDefinition(ctx, t.ProjectID)
	if err != nil {
		var unconfirmed *SandboxUnconfirmedError
		if errors.As(err, &unconfirmed) {
			return nil, err
		}
		return nil, &sandboxGateError{reason: pipeline.SandboxDefinition, err: err}
	}
	// creating the sandbox can take minutes (pulling images, starting services, running setup): each step is said on the task
	// page while it goes, instead of minutes of silence (effort review X39)
	b, err := c.sandboxes().Ensure(ctx, sandbox.Spec{TaskID: t.ID, ProjectID: t.ProjectID, Worktree: t.Worktree, Config: cfg, Progress: func(step string) {
		c.sbStarts.step(t.ID, step)
		c.publishTask(ctx, EventSandboxStarting, t, "", map[string]any{"step": step})
	}})
	if c.sbStarts.finish(t.ID, err == nil) {
		if err == nil {
			c.publishTask(ctx, EventSandboxReady, t, "", map[string]any{})
		} else {
			c.publishTask(ctx, EventSandboxStartFailed, t, "", map[string]any{})
		}
	}
	if err != nil {
		reason := pipeline.SandboxStart
		if errors.Is(err, sandbox.ErrNoEngine) {
			reason = pipeline.SandboxEngine
		}
		return nil, &sandboxGateError{reason: reason, err: err}
	}
	return b, nil
}

// Events of a sandbox that is being created (X39). The step is in plain words and names only images and services of the
// definition (sandbox.Spec.Progress).
const (
	EventSandboxStarting    = "sandbox.starting"
	EventSandboxReady       = "sandbox.ready"
	EventSandboxStartFailed = "sandbox.start_failed"
)

// SandboxStart is how the last start of a task's sandbox went, kept while Drydock runs: the steps so far, and whether it is ready
// or failed. While neither, the task page says the step it is at.
type SandboxStart struct {
	Steps  []string `json:"steps"`
	Ready  bool     `json:"ready,omitempty"`
	Failed bool     `json:"failed,omitempty"`
}

// sandboxStarts holds each task's SandboxStart; a task's goes when the task ends.
type sandboxStarts struct {
	mu sync.Mutex
	m  map[string]SandboxStart
}

// step records a step; a start that had finished is replaced by a new one.
func (s *sandboxStarts) step(taskID, step string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]SandboxStart{}
	}
	cur := s.m[taskID]
	if cur.Ready || cur.Failed {
		cur = SandboxStart{}
	}
	if len(cur.Steps) < 50 {
		cur.Steps = append(append([]string(nil), cur.Steps...), step)
	}
	s.m[taskID] = cur
}

// finish ends a start that is going on; it reports whether the record changed. A failure before any step (no engine, a network
// that could not be created) also ends a start recorded earlier as ready, so the page does not say "started" above a sandbox that
// could not start.
func (s *sandboxStarts) finish(taskID string, ok bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, has := s.m[taskID]
	if !has || cur.Failed {
		return false
	}
	if cur.Ready {
		if ok {
			return false
		}
		s.m[taskID] = SandboxStart{Failed: true}
		return true
	}
	cur.Ready, cur.Failed = ok, !ok
	s.m[taskID] = cur
	return true
}

func (s *sandboxStarts) get(taskID string) *SandboxStart {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.m[taskID]
	if !ok {
		return nil
	}
	cur.Steps = append([]string(nil), cur.Steps...)
	return &cur
}

func (s *sandboxStarts) drop(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, taskID)
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
	// on the task's and the agent's timeline, so the activity view and a task's log show what ran in its sandbox
	s.c.recordSandboxCommand(ctx, t, run.AgentID, run.RunID, "sandbox_run", command, res)
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
	Switch    SandboxSwitch   `json:"switch"`
	Engine    *sandbox.Info   `json:"engine,omitempty"`
	EngineErr string          `json:"engineError,omitempty"`
	Found     bool            `json:"found"`
	Branch    string          `json:"branch,omitempty"`
	Config    *sandbox.Config `json:"config,omitempty"`
	ConfigErr string          `json:"configError,omitempty"`
	Hosts     []string        `json:"hosts,omitempty"`
	Starter   string          `json:"starter,omitempty"` // a definition to start from, when the project has none
	// StarterFrom names the project's files the starter took something from (a compose file, a dev container, a Dockerfile; X35):
	// their values are copied as they are, so the settings say to read them before committing.
	StarterFrom []string `json:"starterFrom,omitempty"`
	EgressErr   string   `json:"egressError,omitempty"`
	ConfigPath  string   `json:"configPath"`
	// Fingerprint is the digest of the definition shown; sandbox.trust confirms the definition with that digest. Trusted is true
	// when the sandbox is on and this is the definition the person confirmed; otherwise Changes says what differs.
	Fingerprint string   `json:"fingerprint,omitempty"`
	Trusted     bool     `json:"trusted"`
	Changes     []string `json:"changes,omitempty"`
	// WSLAgents are the project's agents that run in WSL (decision P41): while there is one, sandbox.set refuses to switch the
	// sandbox on, so the settings say so before the click.
	WSLAgents []WSLAgent `json:"wslAgents,omitempty"`
}

func (c *Core) sandboxStatus(ctx context.Context, projectID string) (SandboxStatus, error) {
	if _, err := c.GetProject(ctx, projectID); err != nil {
		return SandboxStatus{}, err
	}
	st := SandboxStatus{Switch: c.sandboxSwitch(ctx, projectID), ConfigPath: sandbox.ConfigPath, WSLAgents: c.projectWSLAgentList(ctx, projectID)}
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
		st.Starter, st.StarterFrom = c.sandboxStarter(ctx, projectID, branch)
	default:
		st.Config, st.Hosts = &cfg, cfg.Hosts()
		st.Fingerprint = cfg.Fingerprint()
		if st.Switch.Enabled {
			st.Trusted = st.Switch.Trusted != nil && st.Switch.Trusted.Fingerprint() == st.Fingerprint
			if !st.Trusted {
				st.Changes = sandbox.Diff(st.Switch.Trusted, cfg)
			}
		}
		if cfg.Network == sandbox.NetworkAllowlist && st.Engine != nil {
			if _, err := sandbox.EgressBinary(st.Engine.Arch); err != nil {
				st.EgressErr = err.Error()
			}
		}
	}
	return st, nil
}

// sandboxStarter suggests a definition from the files on the default branch: the language files, and the compose file, dev
// container and Dockerfile the project already has (X35). from names the files it took something from.
func (c *Core) sandboxStarter(ctx context.Context, projectID, branch string) (starter string, from []string) {
	repo, err := c.repo(ctx, projectID)
	if err != nil || branch == "" {
		return "", nil
	}
	read := func(name string) (string, bool) {
		text, ok, err := repo.ShowFile(ctx, branch, name)
		return text, err == nil && ok
	}
	cfg, from := sandbox.StarterFrom(read)
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return string(b), from
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
		if c.sandboxSwitch(ctx, projectID).Enabled {
			cfg, branch, err = c.trustedSandboxDefinition(ctx, projectID) // a definition that is in use must be one the person confirmed
		} else {
			cfg, branch, _, err = c.SandboxConfig(ctx, projectID) // before switching on, this is how a person tries a definition
		}
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
		"sandbox.trust": bind(func(ctx context.Context, p struct {
			ProjectID   string `json:"projectId"`
			Fingerprint string `json:"fingerprint"`
		}) (any, error) {
			return c.TrustSandbox(ctx, p.ProjectID, p.Fingerprint, c.Human())
		}),
		"sandbox.test": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			steps, err := c.testSandbox(ctx, p.ProjectID)
			return map[string]any{"steps": steps}, err
		}),
		"sandbox.forward.list": bind(func(ctx context.Context, p struct {
			TaskID string `json:"taskId"`
		}) (any, error) {
			return c.SandboxForwardList(ctx, p.TaskID)
		}),
		"sandbox.forward.open": bind(func(ctx context.Context, p struct {
			TaskID string `json:"taskId"`
			Port   int    `json:"port"`
		}) (any, error) {
			return c.SandboxForwardOpen(ctx, p.TaskID, p.Port, c.Human())
		}),
		"sandbox.forward.close": bind(func(ctx context.Context, p struct {
			TaskID string `json:"taskId"`
			Port   int    `json:"port"`
		}) (any, error) {
			return map[string]any{}, c.SandboxForwardClose(ctx, p.TaskID, p.Port, c.Human())
		}),
		// the task page's "Open the app": forward the port and show it in the viewer, in one click of the person's
		"sandbox.forward.show": bind(func(ctx context.Context, p struct {
			TaskID string `json:"taskId"`
			Port   int    `json:"port"`
		}) (any, error) {
			return c.SandboxForwardShow(ctx, p.TaskID, p.Port, c.Human())
		}),
		"sandbox.forward.view": bind(func(ctx context.Context, p struct {
			TaskID string `json:"taskId"`
			Port   int    `json:"port"`
		}) (any, error) {
			return map[string]any{}, c.SandboxForwardView(ctx, p.TaskID, p.Port, c.Human())
		}),
	}
	return m, cancel, nil
}

// watchSandboxTasks removes a task's sandbox when the task completes, fails or is cancelled.
func (c *Core) watchSandboxTasks(ctx context.Context) {
	// Consume replays what the bus dropped when this watcher fell behind: a missed event would leave a sandbox running for a task
	// that has ended (audit 2026-10-16, L2)
	events.Consume(ctx, c.Bus, events.Filter{Types: []string{"task.completed", "task.failed", "task.cancelled"}}, func(e events.Event) {
		c.sbStarts.drop(e.TaskID)
		if e.TaskID == "" || !c.sandboxSwitch(ctx, e.ProjectID).Enabled {
			return
		}
		rctx, rcancel := context.WithTimeout(ctx, 2*time.Minute)
		defer rcancel()
		if err := c.sandboxes().Remove(rctx, e.TaskID); err != nil {
			c.logf("sandbox: removing the sandbox of %s: %v", e.TaskID, err)
		}
	})
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

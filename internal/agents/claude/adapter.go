package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/workflow"
	"github.com/kjmaguire/drydock/internal/wsl"
)

// MinVersion is the lowest Claude Code version Drydock supports: 2.1.205 introduced interrupt receipts (system/init
// `capabilities`). 2.1.295 is recommended (2.1.277 added reading AGENTS.md natively). Verified against 2.1.295 on 2026-10-09.
var (
	MinVersion         = [3]int{2, 1, 205}
	RecommendedVersion = [3]int{2, 1, 295}
)

// Adapter drives the user's installed `claude`.
type Adapter struct {
	// Exe overrides executable discovery (tests and the "custom path" Connection setting).
	Exe string
	// BaseArgs are prepended to every invocation (tests use them to select the fake agent mode).
	BaseArgs []string
	// ScrubEnv starts claude from an allowlisted environment instead of the app's whole environment: the base list
	// (proc.BaseEnvNames), the variables claude itself reads (CLAUDE_*, ANTHROPIC_*) and the names PassEnv returns.
	ScrubEnv bool
	// PM runs Detect's probes (nil: a process manager of its own); sessions use the one Start is given.
	PM proc.Manager
	// PassEnv returns extra variable NAMES (not values) the human chose to pass on, for setups the allowlist cannot know about
	// (a Bedrock or Vertex login, a company proxy variable). Read at every start. Nil means none.
	PassEnv func() []string
	// WSL reaches the WSL distros for the wsl run mode (RunSpec.RunMode, decision P41). Nil: native only.
	WSL *wsl.Host
	// UserSettingsOnly starts claude with --setting-sources user. A repository's own .claude/settings.json can define hooks, and
	// claude -p runs them without asking, even in a folder never trusted: with this on, commands written into a repository by
	// someone else are not run by an unattended agent. Measured with claude 2.1.295. The user's own settings still apply.
	UserSettingsOnly bool
}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) ID() string { return "claude" }

func (a *Adapter) Capabilities() agents.Capabilities {
	return agents.Capabilities{
		Streaming: true, StructuredOutput: true, ModelSelection: true, EffortControl: true, SessionResume: true,
		Interrupt: true, MCP: true, TokenUsage: true, CostReporting: true, Headless: true, ImageInput: false,
		ToolCallVisibility: true,
		PermissionModes:    []string{"default", "acceptEdits", "plan", "dontAsk", "auto", "bypassPermissions"},
		RunModes:           a.runModes(),
		// from `claude --help` (2.1.294): --effort <low, medium, high, xhigh, max>; --model takes an alias or a full name
		Efforts: []string{"low", "medium", "high", "xhigh", "max"},
		Models:  []string{"fable", "opus", "sonnet", "haiku"},
	}
}

// runModes lists native, and wsl where wsl.exe exists and a WSL host is configured.
func (a *Adapter) runModes() []string {
	if a.WSLAvailable() {
		return []string{agents.RunModeNative, agents.RunModeWSL}
	}
	return []string{agents.RunModeNative}
}

func (a *Adapter) executable() (string, error) {
	if a.Exe != "" {
		return a.Exe, nil
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	var cands []string
	if home, err := os.UserHomeDir(); err == nil {
		name := "claude"
		if runtime.GOOS == "windows" {
			name = "claude.exe"
		}
		cands = append(cands, filepath.Join(home, ".local", "bin", name), filepath.Join(home, ".claude", "local", name))
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", exec.ErrNotFound
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// ParseVersion extracts the first x.y.z from CLI output.
func ParseVersion(s string) ([3]int, bool) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := 0; i < 3; i++ {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

// Less reports whether a < b.
func Less(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func (a *Adapter) Detect(ctx context.Context) (agents.Detection, error) {
	d := agents.Detection{Adapter: "claude", Status: agents.StatusNotInstalled,
		InstallHint: `PowerShell: irm https://claude.ai/install.ps1 | iex   (or: winget install Anthropic.ClaudeCode)`}
	exe, err := a.executable()
	if err != nil {
		d.Problems = append(d.Problems, "claude was not found on PATH or in the known install locations")
		return d, nil
	}
	d.Installed, d.Path = true, exe
	// both probes go through the process manager, which kills the whole tree on a timeout: an npm claude.cmd on Windows is
	// cmd.exe then node, and killing only cmd.exe would leave node running
	pm := a.PM
	if pm == nil {
		pm = proc.New()
	}
	vr := proc.Probe(ctx, pm, proc.Spec{Path: exe, Args: append(append([]string(nil), a.BaseArgs...), "--version"), Label: "probe:claude"}, 15*time.Second)
	if vr.Err != nil || vr.Exit != 0 {
		d.Status = agents.StatusDegraded
		why := fmt.Sprintf("exit %d", vr.Exit)
		if vr.Err != nil {
			why = vr.Err.Error()
		}
		d.Problems = append(d.Problems, "claude --version failed: "+why)
		return d, nil
	}
	d.Version = strings.TrimSpace(vr.Stdout)
	if v, ok := ParseVersion(d.Version); ok && Less(v, MinVersion) {
		d.Status = agents.StatusUnsupportedVersion
		d.Problems = append(d.Problems, fmt.Sprintf("claude %s is older than the minimum supported %d.%d.%d", d.Version, MinVersion[0], MinVersion[1], MinVersion[2]))
		return d, nil
	}
	// Authentication state comes from the CLI's own probe; Drydock never reads credential files.
	ar := proc.Probe(ctx, pm, proc.Spec{Path: exe, Args: append(append([]string(nil), a.BaseArgs...), "auth", "status"), Label: "probe:claude"}, 20*time.Second)
	aout := []byte(ar.Stdout)
	var aerr error
	if ar.Err != nil || ar.Exit != 0 {
		aerr = fmt.Errorf("exit %d: %v", ar.Exit, ar.Err)
	}
	var st struct {
		LoggedIn   bool   `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
	}
	if aerr == nil && json.Unmarshal(bytes.TrimSpace(aout), &st) == nil {
		t := st.LoggedIn
		d.Authenticated = &t
		d.AuthDetail = st.AuthMethod
		if !t {
			d.Status = agents.StatusUnauthenticated
			d.Problems = append(d.Problems, "not logged in: run `claude auth login` in the embedded terminal")
			return d, nil
		}
	} else if aerr != nil {
		// A non-zero exit from `auth status` (it ran and answered) is how a logged-out CLI answers; treat it as unauthenticated.
		if ar.Exit > 0 && !ar.TimedOut {
			f := false
			d.Authenticated = &f
			d.Status = agents.StatusUnauthenticated
			d.Problems = append(d.Problems, "claude auth status reported not logged in")
			return d, nil
		}
	}
	d.Status = agents.StatusReady
	return d, nil
}

// ReadOnlyDenied are the Claude Code tools a read-only session may never use: everything that writes files or runs commands.
var ReadOnlyDenied = []string{"Edit", "Write", "MultiEdit", "NotebookEdit", "Bash"}

// PluginToolsDenied keeps the tools of MCP servers from the user's installed Claude Code plugins out of a session.
// --strict-mcp-config keeps the user's and the repository's MCP configuration out but not plugins' servers: measured with
// 2.1.296, 31 plugin servers joined and their tools were offered (contract CLAUDE-MCP-01). With this rule they still start,
// as when the user runs Claude Code, but the agent cannot call them, so its only tools beyond the CLI's own are the
// gateway's. --safe-mode, which drops plugins, also drops the gateway's --mcp-config server. Restricted mode loads no plugin.
const PluginToolsDenied = "mcp__plugin_*"

// UnattendedDenied are the Claude Code tools no Drydock session uses, denied in every session: they schedule or wake turns
// nobody asked for (cron, wakeups, monitors, workflows), reach past the session (the user's other Claude sessions,
// notifications, files sent to the user), search or change the user's Claude Code setup (plugins, skills, connectors, the MCP
// registry), or take the agent out of the folder Drydock gave it (worktrees). Measured with 2.1.296: with them, the quiet
// environment (QuietEnv) and --disable-slash-commands a worker is offered 13 tools (Bash, Edit, Write, Read, NotebookEdit,
// Task, TaskStop, ToolSearch, WebFetch, WebSearch and the three MCP resource tools); a name the CLI does not have is ignored.
var UnattendedDenied = []string{
	"CronCreate", "CronDelete", "CronList", "ScheduleWakeup", "Monitor", "Workflow", "RemoteTrigger",
	"SendMessage", "ListAgents", "PushNotification", "ReadNotifications", "SendUserFile",
	"ListPlugins", "SearchPlugins", "SuggestPluginInstall", "ListSkills", "SearchSkills", "SuggestSkills",
	"ListConnectors", "SuggestConnectors", "SearchMcpRegistry", "ShowOnboardingRolePicker", "DesignSync", "ReportFindings",
	"EnterWorktree", "ExitWorktree",
}

// QuietEnv switches off, in every Claude Code process Drydock starts, the features that spend tokens nobody asked for or keep
// a session working after its turn. They replace any value inherited from the human's environment (CLAUDE_* passes through),
// and a run's own Env still comes after them. Each name and value was read from the CLI's code (2.1.296). Measured there
// together with --disable-slash-commands and UnattendedDenied, the first request of a session in a small repository fell from
// 27,463 tokens to 9,846 (docs/token-control.md, "As built: Claude Code sessions").
var QuietEnv = []string{
	"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1",          // no memory folder loaded into every session or written behind Drydock's memory
	"CLAUDE_CODE_DISABLE_WORKFLOWS=1",            // no multi-agent workflow started from inside a turn
	"CLAUDE_CODE_DISABLE_ADVISOR_TOOL=1",         // no second, larger model consulted on the side
	"CLAUDE_CODE_DISABLE_CRON=1",                 // no prompt scheduled to run later (durable ones are saved into the repository)
	"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1",     // nothing left running after the turn ends
	"CLAUDE_AUTO_BACKGROUND_TASKS=false",         // a long command is waited for, not moved to the background and its result lost
	"CLAUDE_CODE_GOAL_CHECKIN_MINUTES=0",         // no check-in turns on an idle session
	"CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false", // no suggested next prompt generated after a turn
	"CLAUDE_CODE_ENABLE_AWAY_SUMMARY=0",          // no recap generated for someone coming back
}

// Args builds the argv (after the executable) for a session, with the system prompt addition inline. Start passes it in a
// file instead (argsWith).
func (a *Adapter) Args(spec agents.RunSpec) []string { return a.argsWith(spec, "") }

// argsWith builds the argv; with promptFile set, spec.SystemAppend is read by the CLI from that file
// (--append-system-prompt-file) rather than given on the command line. On Windows an npm install of Claude Code is
// claude.cmd, which runs through cmd.exe: cmd.exe ends the command line at the first newline, so a multi-line argument would
// lose the rest of the instructions and every flag after it (the allow rules), and it caps the line at 8191 characters.
func (a *Adapter) argsWith(spec agents.RunSpec, promptFile string) []string {
	args := append([]string(nil), a.BaseArgs...)
	args = append(args, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages")
	// no skills or slash commands of the user's Claude Code (bundled, the user's own, plugins', synced from claude.ai): their
	// list is read into every request (9,200 tokens with ten plugins, measured with 2.1.296) and no Drydock session uses them.
	// Drydock's own accepted skills reach Claude as rules in the instructions (internal/skills), which this leaves alone.
	args = append(args, "--disable-slash-commands")
	// one deny list: a read-only session's writing tools, installed plugins' MCP tools for every session that loads plugins
	// (restricted mode loads none), and the tools no Drydock session uses
	var denied []string
	if spec.ReadOnly {
		denied = append(denied, ReadOnlyDenied...)
	}
	if !spec.Sandboxed {
		denied = append(denied, PluginToolsDenied)
	}
	denied = append(denied, UnattendedDenied...)
	args = append(args, "--disallowedTools", strings.Join(denied, ","))
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.Effort != "" {
		args = append(args, "--effort", spec.Effort)
	}
	if spec.ReadOnly {
		// default mode refuses, in a headless session, every tool nobody approved; the deny list (above) also beats any allow
		// rule in the human's own settings (deny wins), so nothing in the session can write
		args = append(args, "--permission-mode", "default")
	} else if spec.Sandboxed {
		// restricted mode refuses bypassPermissions, and with no command tools on this machine there is nothing else to approve:
		// file edits in the working folder are the job
		args = append(args, "--permission-mode", "acceptEdits")
	} else if spec.PermissionMode != "" {
		args = append(args, "--permission-mode", spec.PermissionMode)
	}
	switch {
	case spec.Sandboxed:
		// a sandboxed worker's commands run in its container through the gateway; restricted mode removes every tool that runs
		// commands or code here, confines the file tools to the working folder, refuses writes to settings and .git, and ignores
		// every settings file (so no repository hooks). It does not put the project's CLAUDE.md in the model's context either
		// (contract CLAUDE-RESTRICTED-02), so Start appends the project memory, as for --setting-sources user
		args = append(args, "--restricted")
	case a.UserSettingsOnly:
		args = append(args, "--setting-sources", "user")
	}
	if spec.MCPConfigPath != "" {
		args = append(args, "--mcp-config", spec.MCPConfigPath, "--strict-mcp-config")
		// a worker outside a sandbox asks Drydock about every tool use its rules do not cover, instead of being refused by the
		// headless CLI (docs/security.md s4). Only through the gateway the session was given, never for a read-only or
		// sandboxed session (their modes already decide everything).
		if spec.PermissionPromptTool != "" && !spec.ReadOnly && !spec.Sandboxed {
			args = append(args, "--permission-prompt-tool", spec.PermissionPromptTool)
		}
	}
	if spec.ResumeID != "" {
		args = append(args, "--resume", spec.ResumeID)
	}
	if promptFile != "" {
		args = append(args, "--append-system-prompt-file", promptFile)
	} else if spec.SystemAppend != "" {
		args = append(args, "--append-system-prompt", spec.SystemAppend)
	}
	if len(spec.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(spec.AllowedTools, ","))
	}
	args = append(args, spec.ExtraArgs...)
	return args
}

func (a *Adapter) Start(ctx context.Context, pm proc.Manager, spec agents.RunSpec) (agents.Session, error) {
	env := Quiet(a.childEnv(os.Environ()))
	env = append(env, spec.Env...)
	text := spec.SystemAppend
	if a.UserSettingsOnly || spec.Sandboxed { // neither mode loads the project's CLAUDE.md, rules and imports into context
		if mem := projectMemory(spec.Cwd); mem != "" {
			text = strings.TrimSpace(text + "\n\n" + mem)
		}
	}
	switch spec.RunMode {
	case "", agents.RunModeNative:
	case agents.RunModeWSL:
		return a.startWSL(ctx, pm, spec, env, text)
	default:
		return nil, fmt.Errorf("claude: unknown run mode %q", spec.RunMode)
	}
	exe, err := a.executable()
	if err != nil {
		if d := a.wslInstall(ctx); d != "" {
			return nil, fmt.Errorf("claude: Claude Code is not installed on Windows, but it is installed in the WSL distro %s: run this agent there (Project settings, \"Where agents run\", or the agent's own settings) or install it on Windows (%w)", d, err)
		}
		return nil, fmt.Errorf("claude: %w", err)
	}
	promptFile, err := writePromptFile(text)
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	p, err := pm.Start(ctx, proc.Spec{Path: exe, Args: a.argsWith(spec, promptFile), Dir: spec.Cwd, Env: env, Label: "agent:" + spec.AgentID})
	if err != nil {
		removePromptFile(promptFile)
		return nil, err
	}
	return newSession(p, spec, nil, func() { removePromptFile(promptFile) }), nil
}

// newSession reads the process's stream. sweep, if set, ends what the run left running where the process tree kill cannot
// reach (the WSL run mode's Linux processes): Stop calls it before killing the tree, and the session runs it once more when
// the process has exited, before Wait answers. cleanup runs after that (temporary files).
func newSession(p proc.Proc, spec agents.RunSpec, sweep func(), cleanup func()) *session {
	s := &session{proc: p, parser: NewParser(), out: make(chan workflow.Chunk, 256), acks: map[string]chan bool{}, exited: make(chan struct{}), sweep: sweep}
	if spec.MCPConfigPath != "" {
		s.parser.RequireGateway(GatewayServer)
	}
	if spec.ResumeID != "" {
		s.parser.SetBaseline(spec.UsageBaseline)
	}
	safe.Go("claude.newSession", func() {
		s.ei = <-p.Wait()
		if sweep != nil {
			sweep()
		}
		if cleanup != nil {
			cleanup()
		}
		close(s.exited)
	})
	safe.Go("claude.newSession#2", func() { s.readStdout() })
	safe.Go("claude.newSession#3", func() { s.readStderr() })
	return s
}

// writePromptFile puts the system prompt addition in a private temporary file for --append-system-prompt-file ("" when there
// is none). It holds instructions, never credentials; it is removed when the process exits.
func writePromptFile(text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	f, err := os.CreateTemp("", "drydock-claude-prompt-*.md")
	if err != nil {
		return "", fmt.Errorf("writing the instructions file: %w", err)
	}
	_, werr := f.WriteString(text)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("writing the instructions file: %w", errors.Join(werr, cerr))
	}
	return f.Name(), nil
}

func removePromptFile(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}

type session struct {
	proc   proc.Proc
	parser *Parser
	out    chan workflow.Chunk
	wmu    sync.Mutex
	amu    sync.Mutex
	acks   map[string]chan bool
	errBuf tail
	exited chan struct{}
	ei     proc.ExitInfo
	sweep  func() // the WSL run mode: kill the run's processes inside the distro (nil natively)
}

type tail struct {
	mu sync.Mutex
	b  []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > 8192 {
		t.b = t.b[len(t.b)-8192:]
	}
	return len(p), nil
}
func (t *tail) String() string { t.mu.Lock(); defer t.mu.Unlock(); return string(t.b) }

func (s *session) readStdout() {
	defer close(s.out)
	sc := bufio.NewScanner(s.proc.Stdout())
	sc.Buffer(make([]byte, 1<<20), 64<<20) // init events are large
	for sc.Scan() {
		chunks, ack := s.parser.Parse(sc.Bytes())
		if ack != nil {
			s.amu.Lock()
			if ch, ok := s.acks[ack.RequestID]; ok {
				ch <- ack.Success
				delete(s.acks, ack.RequestID)
			}
			s.amu.Unlock()
		}
		for _, c := range chunks {
			s.out <- c
		}
	}
	// stdout ended: the process exited or closed its output
	if err := sc.Err(); err != nil {
		s.out <- workflow.NewChunk(workflow.ChunkError, workflow.ErrorChunk{Message: "stdout: " + err.Error(), Fatal: true})
	}
}

func (s *session) readStderr() {
	_, _ = bufio.NewReader(s.proc.Stderr()).WriteTo(&s.errBuf)
}

func (s *session) ID() string                    { return s.parser.SessionID() }
func (s *session) Output() <-chan workflow.Chunk { return s.out }
func (s *session) Wait() <-chan proc.ExitInfo {
	ch := make(chan proc.ExitInfo, 1)
	safe.Go("claude.session.Wait", func() { <-s.exited; ch <- s.ei })
	return ch
}
func (s *session) Stderr() string { return s.errBuf.String() }
func (s *session) PID() int       { return s.proc.PID() }

func (s *session) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err = s.proc.Stdin().Write(append(b, '\n'))
	return err
}

func (s *session) Send(ctx context.Context, msg string) error {
	return s.write(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": msg}})
}

func (s *session) Interrupt(ctx context.Context) error {
	id := ids.New("int")
	ack := make(chan bool, 1)
	s.amu.Lock()
	s.acks[id] = ack
	s.amu.Unlock()
	s.parser.MarkInterrupted()
	if err := s.write(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": "interrupt"}}); err != nil {
		return err
	}
	select {
	case ok := <-ack:
		if !ok {
			return errors.New("claude: interrupt rejected")
		}
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("claude: interrupt not acknowledged within 5s")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) Stop(ctx context.Context) error {
	_ = s.proc.Stdin().Close()
	select {
	case <-s.exited:
		return nil
	case <-time.After(2 * time.Second):
	case <-ctx.Done():
	}
	if s.sweep != nil {
		s.sweep() // inside the distro first: wsl.exe then ends with its program
	}
	return s.proc.Kill(true)
}

// childEnv is the environment of the claude process: all of parent, or with ScrubEnv only what claude needs to run and sign in.
// The user's own login is found through the home folder, CLAUDE_* and ANTHROPIC_* (including CLAUDE_CODE_OAUTH_TOKEN and
// ANTHROPIC_API_KEY): Drydock passes them through untouched and never reads them.
func (a *Adapter) childEnv(parent []string) []string {
	parent = WithoutEnclosingSession(parent)
	if !a.ScrubEnv {
		return parent
	}
	names := append([]string(nil), proc.BaseEnvNames...)
	if a.PassEnv != nil {
		names = append(names, a.PassEnv()...)
	}
	return proc.ScrubEnv(parent, names, []string{"CLAUDE_", "ANTHROPIC_"})
}

// Quiet is env with QuietEnv in place of any value of those names it had (names compared as Windows does, ignoring case). Start
// uses it for every session; the contract suite starts the real CLI with it too.
func Quiet(env []string) []string {
	names := make(map[string]bool, len(QuietEnv))
	for _, kv := range QuietEnv {
		k, _, _ := strings.Cut(kv, "=")
		names[k] = true
	}
	out := make([]string, 0, len(env)+len(QuietEnv))
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); !names[strings.ToUpper(k)] {
			out = append(out, kv)
		}
	}
	return append(out, QuietEnv...)
}

// enclosingSessionEnv are the variables a running Claude Code session sets for the processes it starts. When Drydock itself
// was started from inside Claude Code they would make every agent report and append to that outer session (observed with
// 2.1.296: each turn reported the parent's session id; in a cloud session CLAUDE_CODE_REMOTE_SESSION_ID does the same), and
// the messaging socket and token would let an agent message the outer session, so an agent never inherits them.
var enclosingSessionEnv = map[string]bool{"CLAUDECODE": true, "CLAUDE_CODE_ENTRYPOINT": true, "CLAUDE_CODE_SESSION_ID": true, "CLAUDE_CODE_CHILD_SESSION": true,
	"CLAUDE_CODE_REMOTE_SESSION_ID": true, "CLAUDE_CODE_MESSAGING_SOCKET": true, "CLAUDE_CODE_MESSAGING_TOKEN": true}

// WithoutEnclosingSession is env without enclosingSessionEnv. The trial harness (cmd/drydock-trial) starts its plain Claude
// Code arm with it too, so both arms run outside the session that started them.
func WithoutEnclosingSession(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); !enclosingSessionEnv[strings.ToUpper(k)] {
			out = append(out, kv)
		}
	}
	return out
}

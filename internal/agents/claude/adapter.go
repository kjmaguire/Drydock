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
	"github.com/kjmaguire/drydock/internal/workflow"
)

// MinVersion is the lowest Claude Code version Drydock supports: 2.1.205 introduced interrupt receipts (system/init
// `capabilities`). 2.1.277 is recommended (reads AGENTS.md natively). Verified against 2.1.294 on 2026-10-08.
var (
	MinVersion         = [3]int{2, 1, 205}
	RecommendedVersion = [3]int{2, 1, 277}
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
	// PassEnv returns extra variable NAMES (not values) the human chose to pass on, for setups the allowlist cannot know about
	// (a Bedrock or Vertex login, a company proxy variable). Read at every start. Nil means none.
	PassEnv func() []string
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
		RunModes:           []string{"native"},
		// from `claude --help` (2.1.294): --effort <low, medium, high, xhigh, max>; --model takes an alias or a full name
		Efforts: []string{"low", "medium", "high", "xhigh", "max"},
		Models:  []string{"fable", "opus", "sonnet", "haiku"},
	}
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
	vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(vctx, exe, append(append([]string(nil), a.BaseArgs...), "--version")...).Output()
	if err != nil {
		d.Status = agents.StatusDegraded
		d.Problems = append(d.Problems, "claude --version failed: "+err.Error())
		return d, nil
	}
	d.Version = strings.TrimSpace(string(out))
	if v, ok := ParseVersion(d.Version); ok && Less(v, MinVersion) {
		d.Status = agents.StatusUnsupportedVersion
		d.Problems = append(d.Problems, fmt.Sprintf("claude %s is older than the minimum supported %d.%d.%d", d.Version, MinVersion[0], MinVersion[1], MinVersion[2]))
		return d, nil
	}
	// Authentication state comes from the CLI's own probe; Drydock never reads credential files.
	actx, cancel2 := context.WithTimeout(ctx, 20*time.Second)
	defer cancel2()
	aout, aerr := exec.CommandContext(actx, exe, append(append([]string(nil), a.BaseArgs...), "auth", "status")...).Output()
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
		// A non-zero exit from `auth status` is how a logged-out CLI answers; treat it as unauthenticated.
		var ee *exec.ExitError
		if errors.As(aerr, &ee) {
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

// Args builds the argv (after the executable) for a session.
func (a *Adapter) Args(spec agents.RunSpec) []string {
	args := append([]string(nil), a.BaseArgs...)
	args = append(args, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages")
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.Effort != "" {
		args = append(args, "--effort", spec.Effort)
	}
	if spec.PermissionMode != "" {
		args = append(args, "--permission-mode", spec.PermissionMode)
	}
	if a.UserSettingsOnly {
		args = append(args, "--setting-sources", "user")
	}
	if spec.MCPConfigPath != "" {
		args = append(args, "--mcp-config", spec.MCPConfigPath, "--strict-mcp-config")
	}
	if spec.ResumeID != "" {
		args = append(args, "--resume", spec.ResumeID)
	}
	if spec.SystemAppend != "" {
		args = append(args, "--append-system-prompt", spec.SystemAppend)
	}
	if len(spec.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(spec.AllowedTools, ","))
	}
	args = append(args, spec.ExtraArgs...)
	return args
}

func (a *Adapter) Start(ctx context.Context, pm proc.Manager, spec agents.RunSpec) (agents.Session, error) {
	exe, err := a.executable()
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	env := a.childEnv(os.Environ())
	env = append(env, spec.Env...)
	p, err := pm.Start(ctx, proc.Spec{Path: exe, Args: a.Args(spec), Dir: spec.Cwd, Env: env, Label: "agent:" + spec.AgentID})
	if err != nil {
		return nil, err
	}
	s := &session{proc: p, parser: NewParser(), out: make(chan workflow.Chunk, 256), acks: map[string]chan bool{}, exited: make(chan struct{})}
	go func() { s.ei = <-p.Wait(); close(s.exited) }()
	go s.readStdout()
	go s.readStderr()
	return s, nil
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
	go func() { <-s.exited; ch <- s.ei }()
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
	return s.proc.Kill(true)
}

// childEnv is the environment of the claude process: all of parent, or with ScrubEnv only what claude needs to run and sign in.
// The user's own login is found through the home folder, CLAUDE_* and ANTHROPIC_* (including CLAUDE_CODE_OAUTH_TOKEN and
// ANTHROPIC_API_KEY): Drydock passes them through untouched and never reads them.
func (a *Adapter) childEnv(parent []string) []string {
	if !a.ScrubEnv {
		return append([]string(nil), parent...)
	}
	names := append([]string(nil), proc.BaseEnvNames...)
	if a.PassEnv != nil {
		names = append(names, a.PassEnv()...)
	}
	return proc.ScrubEnv(parent, names, []string{"CLAUDE_", "ANTHROPIC_"})
}

// Package agents defines what an agent CLI adapter is, and the capability matrix the router and runtime decide against.
// Provider-specific logic lives only in adapter packages (claude, ...); nothing outside them switches on a provider name.
package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Capabilities is the normalized capability matrix. "false" means not verified, not "impossible".
type Capabilities struct {
	Streaming          bool     `json:"streaming"`
	StructuredOutput   bool     `json:"structuredOutput"`
	ModelSelection     bool     `json:"modelSelection"`
	EffortControl      bool     `json:"effortControl"`
	SessionResume      bool     `json:"sessionResume"`
	Interrupt          bool     `json:"interrupt"`
	MCP                bool     `json:"mcp"`
	TokenUsage         bool     `json:"tokenUsage"`
	CostReporting      bool     `json:"costReporting"`
	Headless           bool     `json:"headless"`
	ImageInput         bool     `json:"imageInput"`
	ToolCallVisibility bool     `json:"toolCallVisibility"`
	PermissionModes    []string `json:"permissionModes,omitempty"`
	RunModes           []string `json:"runModes,omitempty"` // native, wsl
	// Efforts and Models are the values the adapter is known to accept. Efforts is enforced when set (a wrong value would fail
	// the run); Models only suggests (full model names are free text). Empty means the CLI reports its own choices when a
	// session starts, so nothing is checked or offered ahead of time.
	Efforts []string `json:"efforts,omitempty"`
	Models  []string `json:"models,omitempty"`
}

// ConnectionStatus is the Connections page status (brief section 5).
type ConnectionStatus string

const (
	StatusNotInstalled       ConnectionStatus = "not_installed"
	StatusUnsupportedVersion ConnectionStatus = "unsupported_version"
	StatusUnauthenticated    ConnectionStatus = "unauthenticated"
	StatusReady              ConnectionStatus = "ready"
	StatusDegraded           ConnectionStatus = "degraded"
)

// Detection is the result of looking for a CLI on this machine.
type Detection struct {
	Adapter       string           `json:"adapter"`
	Installed     bool             `json:"installed"`
	Path          string           `json:"path,omitempty"`
	Version       string           `json:"version,omitempty"`
	Authenticated *bool            `json:"authenticated,omitempty"` // nil: unknown
	AuthDetail    string           `json:"authDetail,omitempty"`
	Status        ConnectionStatus `json:"status"`
	Problems      []string         `json:"problems,omitempty"`
	InstallHint   string           `json:"installHint,omitempty"` // the vendor's official command; never executed by Drydock
}

// ToolAsk is a CLI's request to use one tool call its own rules do not allow.
type ToolAsk struct {
	ToolCallID string          // the CLI's id of the call, as in its tool_call chunk
	Tool       string          // normalized tool name
	Title      string          // the CLI's own description of the call
	Kind       string          // the protocol's tool kind (ACP: read, edit, delete, move, search, execute, think, fetch, other)
	Args       json.RawMessage // the exact input of the call
	Locations  []string        // the paths the call names
}

// PermissionAsker answers a ToolAsk: true allows exactly that call once, false refuses it (message, when set, is what the
// CLI is told). It may block while a person decides; ctx ends when the turn is interrupted or the session stops.
type PermissionAsker func(ctx context.Context, q ToolAsk) (allow bool, message string)

// RunSpec describes one agent session.
type RunSpec struct {
	AgentID        string
	Role           string
	Cwd            string
	Model          string
	Effort         string
	PermissionMode string
	Env            []string // extra environment (KEY=VALUE); the process environment is inherited
	MCPConfigPath  string   // path of the single gateway entry (docs/mcp.md)
	ResumeID       string   // CLI session id to resume
	// UsageBaseline is the session's running usage totals as the adapter reported them at the end of its last process
	// (workflow.UsageChunk.Totals), for a ResumeID session; nil means the session's totals start at zero.
	UsageBaseline json.RawMessage
	SystemAppend  string // appended to the CLI's system prompt where supported
	// PracticesVersion is set by the runtime when SystemAppend carries the Claude best-practices text (0 when it does not), so the
	// run records which wording the agent was given.
	PracticesVersion int
	AllowedTools     []string // tools the CLI may use without asking (Claude: --allowedTools)
	// ReadOnly marks a session that only reads (a reviewer, a room reply, a planner, the team lead). Such sessions also carry
	// PermissionMode "plan" for adapters that map it; Claude runs them in its default mode with every writing tool denied
	// instead, because Claude Code answers a plan-mode session started with --model haiku with Sonnet (since 2.0.17; measured
	// with 2.1.296: init reports haiku, every message is Sonnet, about 10 to 50 times the cost).
	ReadOnly bool
	// Sandboxed marks a worker session whose commands run in the task's container sandbox (docs/sandbox.md) through the
	// gateway's sandbox tools. Claude starts it with --restricted: no tool that runs commands or code, file tools confined to
	// the working folder, writes to settings and .git refused, no settings files; the adapter appends the project's CLAUDE.md,
	// which restricted mode does not load.
	Sandboxed bool
	// PermissionPrompts marks a worker session outside a sandbox whose permission prompts go to Drydock: a tool use its allow
	// rules do not cover is asked of Drydock's analyzer and, when the rules do not decide, of a person (docs/security.md s4).
	// The pipeline sets it; the MCP hook offers the gateway's permission tool to such a session and sets PermissionPromptTool.
	PermissionPrompts bool
	// PermissionPromptTool is the MCP tool the CLI asks (Claude: --permission-prompt-tool mcp__drydock__permission_prompt).
	// Empty: the CLI's own permission mode and rules decide alone (a headless Claude then refuses what they do not allow).
	PermissionPromptTool string
	// AskPermission answers a CLI that asks, over its own protocol (ACP session/request_permission), to use a tool its rules do
	// not allow. Nil refuses every request (nothing is approved on the human's behalf). The pipeline gives worker turns outside a
	// sandbox one that goes through Drydock's analyzer and, when the rules do not decide, a person; a chat gives one that asks
	// the person in the chat (docs/security.md s4).
	AskPermission PermissionAsker `json:"-"`
	ExtraArgs     []string        // adapter-specific escape hatch
	// RunMode is where the CLI runs: RunModeNative ("" too) or RunModeWSL, inside the WSL distro WSLDistro (decision P41).
	// Only an adapter whose Capabilities list the mode accepts it; the others refuse to start.
	RunMode   string
	WSLDistro string
	// RunModeWhy says why Drydock chose the run mode itself ("" when the agent's or the project's setting decided): the agent
	// follows its project's default, its CLI is not installed on Windows, and it is ready in WSLDistro (decision P58). The
	// runtime records it in the run's events so the choice is visible.
	RunModeWhy string

	// OnCallChanged, if set, hears of a call the CLI changed after Drydock allowed it (decision P117): an ACP agent runs an
	// allowed call by itself, so the adapter compares what the agent reports of the call afterwards with what was allowed (the
	// hashes of both are given) and stops the turn on the first difference. The core records it.
	OnCallChanged func(q ToolAsk, allowedSum, actualSum string) `json:"-"`

	// Terminals, if set, runs the session's ACP terminal requests (terminal/create and the rest) instead of this machine: a
	// sandboxed worker's go to the task's container (decision P131). An ACP adapter then offers the terminal capability for the
	// session. Nil: terminals run here, and only when the adapter offers them.
	Terminals TerminalHost `json:"-"`
	// OnSandboxEscape, if set, hears of a command a sandboxed session's CLI reported running without asking and without
	// Drydock's terminal (decision P131): it ran on this machine. The adapter has already stopped the turn; the core records it.
	OnSandboxEscape func(q ToolAsk) `json:"-"`
}

// TerminalRequest is one ACP terminal command to run elsewhere (RunSpec.Terminals): the executable and its arguments, or a
// command line when there are no arguments; the folder relative to the session's working folder ("/" separators, "" for the
// folder itself); extra KEY=VALUE pairs.
type TerminalRequest struct {
	Command string
	Args    []string
	Dir     string
	Env     []string
}

// TerminalHost runs terminal commands for a session. Run blocks until the command ends and writes its output to out as it
// comes; it returns the exit code (-1 for a command that was stopped). Ending ctx stops the command and everything it started.
type TerminalHost interface {
	Run(ctx context.Context, req TerminalRequest, out io.Writer) (exitCode int, err error)
}

// SandboxCapable is implemented by an adapter whose CLI can work in a sandboxed project (decision P131): its terminal
// requests can be routed into the task's container, and in Drydock's configuration it asks before every command it would run
// itself (which Drydock then refuses). Mode is the session mode Drydock selects for that ("" when the manifest's own setting
// already does it); ok is false for a CLI that has no such way.
type SandboxCapable interface {
	SandboxMode() (mode string, ok bool)
}

// NativeLocator is implemented by an adapter that can say, without starting anything, whether its CLI is installed on this
// machine itself (on Windows, not inside a WSL distro). Drydock uses it to run an agent that follows its project's default in
// the WSL distro its CLI is installed in when it is not installed natively (decision P58). An adapter that does not implement
// it counts as installed.
type NativeLocator interface {
	NativeInstalled() bool
}

// NativeInstalled reports whether a's CLI is installed on this machine (true for an adapter that cannot tell).
func NativeInstalled(a Adapter) bool {
	if n, ok := a.(NativeLocator); ok {
		return n.NativeInstalled()
	}
	return true
}

// Run modes (Capabilities.RunModes, RunSpec.RunMode).
const (
	RunModeNative = "native"
	RunModeWSL    = "wsl"
)

// IsWSL reports whether the spec asks for the WSL run mode.
func (s RunSpec) IsWSL() bool { return s.RunMode == RunModeWSL }

// CheckRunMode refuses a run mode the adapter does not list (empty and native are always accepted).
func CheckRunMode(a Adapter, spec RunSpec) error {
	if spec.RunMode == "" || spec.RunMode == RunModeNative {
		return nil
	}
	for _, m := range a.Capabilities().RunModes {
		if m == spec.RunMode {
			return nil
		}
	}
	return fmt.Errorf("%s cannot run in the %s run mode on this machine", a.ID(), spec.RunMode)
}

// Session is a running agent CLI process speaking a normalized chunk stream.
type Session interface {
	// ID is the CLI's own session id once known ("" before the first init event).
	ID() string
	// Send starts a turn (or queues a message) with a user message.
	Send(ctx context.Context, msg string) error
	// Output yields normalized chunks; it is closed when the process's output ends.
	Output() <-chan workflow.Chunk
	// Interrupt cancels the current turn without ending the session (protocol-level, not a signal).
	Interrupt(ctx context.Context) error
	// Stop ends the session and kills the process tree.
	Stop(ctx context.Context) error
	// Wait yields the process exit.
	Wait() <-chan proc.ExitInfo
	// Stderr returns the tail of the process's stderr for error reporting.
	Stderr() string
	PID() int
}

// Image is a picture a person gives an agent with a message (decision P90): its media type (image/png, image/jpeg,
// image/gif, image/webp) and its bytes.
type Image struct {
	MIME string
	Data []byte
}

// ImageSender is a Session that can start a turn with pictures beside the text (Capabilities.ImageInput). A session that is
// not one takes text only.
type ImageSender interface {
	SendImages(ctx context.Context, msg string, images []Image) error
}

// Adapter knows one CLI.
type Adapter interface {
	ID() string
	Detect(ctx context.Context) (Detection, error)
	Capabilities() Capabilities
	Start(ctx context.Context, pm proc.Manager, spec RunSpec) (Session, error)
}

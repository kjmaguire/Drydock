// Package agents defines what an agent CLI adapter is, and the capability matrix the router and runtime decide against.
// Provider-specific logic lives only in adapter packages (claude, ...); nothing outside them switches on a provider name.
package agents

import (
	"context"
	"encoding/json"
	"fmt"

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
	ExtraArgs            []string // adapter-specific escape hatch
	// RunMode is where the CLI runs: RunModeNative ("" too) or RunModeWSL, inside the WSL distro WSLDistro (decision P41).
	// Only an adapter whose Capabilities list the mode accepts it; the others refuse to start.
	RunMode   string
	WSLDistro string
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

// Adapter knows one CLI.
type Adapter interface {
	ID() string
	Detect(ctx context.Context) (Detection, error)
	Capabilities() Capabilities
	Start(ctx context.Context, pm proc.Manager, spec RunSpec) (Session, error)
}

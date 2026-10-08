// Package agents defines what an agent CLI adapter is, and the capability matrix the router and runtime decide against.
// Provider-specific logic lives only in adapter packages (claude, ...); nothing outside them switches on a provider name.
package agents

import (
	"context"

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
	SystemAppend   string   // appended to the CLI's system prompt where supported
	AllowedTools   []string // tools the CLI may use without asking (Claude: --allowedTools)
	ExtraArgs      []string // adapter-specific escape hatch
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

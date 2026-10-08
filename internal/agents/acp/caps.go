package acp

import (
	"encoding/json"
	"sort"

	"github.com/coder/acp-go-sdk"

	"github.com/kjmaguire/drydock/internal/agents"
)

// AuthMethodInfo is one entry of the agent's `authMethods`. Drydock never authenticates on the agent's behalf: the
// list is shown so the user can sign in with the CLI's own login (docs/connections.md section 4).
type AuthMethodInfo struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"` // "", "terminal", "env_var", "agent"
}

// AgentInfo is what `initialize` told us about the agent. Every field is read at runtime from the response; nothing
// here is inferred from the provider name (decision D10).
type AgentInfo struct {
	Name            string `json:"name,omitempty"`
	Title           string `json:"title,omitempty"`
	Version         string `json:"version,omitempty"`
	ProtocolVersion int    `json:"protocolVersion"`

	LoadSession bool `json:"loadSession"`
	Resume      bool `json:"resume"`
	Fork        bool `json:"fork"`
	List        bool `json:"list"`
	Close       bool `json:"close"`
	Delete      bool `json:"delete"`

	MCPHTTP bool `json:"mcpHttp"`
	MCPSSE  bool `json:"mcpSse"`
	MCPACP  bool `json:"mcpAcp"`

	Image           bool `json:"image"`
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`

	AuthMethods []AuthMethodInfo `json:"authMethods,omitempty"`
	// Meta is the agent's `_meta` on the initialize response and on agentCapabilities, kept raw for diagnostics.
	Meta json.RawMessage `json:"meta,omitempty"`
}

// parseInit extracts AgentInfo from an initialize response.
func parseInit(r acp.InitializeResponse) AgentInfo {
	c := r.AgentCapabilities
	ai := AgentInfo{
		ProtocolVersion: int(r.ProtocolVersion),
		LoadSession:     c.LoadSession,
		MCPHTTP:         c.McpCapabilities.Http,
		MCPSSE:          c.McpCapabilities.Sse,
		MCPACP:          c.McpCapabilities.Acp,
		Image:           c.PromptCapabilities.Image,
		Audio:           c.PromptCapabilities.Audio,
		EmbeddedContext: c.PromptCapabilities.EmbeddedContext,
		Resume:          c.SessionCapabilities.Resume != nil,
		Fork:            c.SessionCapabilities.Fork != nil,
		List:            c.SessionCapabilities.List != nil,
		Close:           c.SessionCapabilities.Close != nil,
		Delete:          c.SessionCapabilities.Delete != nil,
	}
	if r.AgentInfo != nil {
		ai.Name, ai.Version = r.AgentInfo.Name, r.AgentInfo.Version
		if r.AgentInfo.Title != nil {
			ai.Title = *r.AgentInfo.Title
		}
	}
	for _, m := range r.AuthMethods {
		b, err := json.Marshal(m)
		if err != nil {
			continue
		}
		var raw struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		}
		if json.Unmarshal(b, &raw) == nil && raw.ID != "" {
			ai.AuthMethods = append(ai.AuthMethods, AuthMethodInfo{ID: raw.ID, Name: raw.Name, Type: raw.Type})
		}
	}
	if len(r.Meta) > 0 || len(c.Meta) > 0 {
		ai.Meta, _ = json.Marshal(map[string]any{"initialize": r.Meta, "agentCapabilities": c.Meta})
	}
	return ai
}

// SessionInfo is what `session/new` (or load/resume) exposed: the model, mode and effort choices the agent offers.
type SessionInfo struct {
	Modes        []string `json:"modes,omitempty"`        // available session mode ids
	CurrentMode  string   `json:"currentMode,omitempty"`  // mode id in effect
	Models       []string `json:"models,omitempty"`       // values of the config option in the "model" category
	Efforts      []string `json:"efforts,omitempty"`      // values of the "thought_level" category
	ModelOption  string   `json:"modelOption,omitempty"`  // config option id to set the model through
	EffortOption string   `json:"effortOption,omitempty"` // config option id to set the effort through
	ModeOption   string   `json:"modeOption,omitempty"`   // config option id of the "mode" category, if modes are exposed that way
}

// Observed is what has actually been seen from the agent so far (as opposed to declared by a manifest).
type Observed struct {
	Info    AgentInfo
	Session SessionInfo
	// Usage is true once a usage_update or prompt usage arrived; Cost is true once a cost was supplied.
	Usage bool
	Cost  bool
	// Turns counts completed prompt turns; Cancel is true once a cancelled stop reason was seen.
	Turns     int
	Cancelled bool
}

// Capabilities merges the manifest's declared capabilities with what the agent reported (docs/connections.md section 6):
// observed wins; a declared value stands only when the agent's report does not contradict it. Protocol baselines
// (session/cancel, request_permission, tool_call updates) are true for every ACP agent; the T4 cancel step confirms them.
func Capabilities(declared agents.Capabilities, o Observed) agents.Capabilities {
	c := declared
	c.Streaming = true
	c.Headless = true
	c.Interrupt = true
	c.ToolCallVisibility = true
	c.MCP = true // stdio MCP servers are mandatory in ACP; http and sse are in AgentInfo
	c.SessionResume = o.Info.LoadSession || o.Info.Resume
	c.ImageInput = o.Info.Image
	c.RunModes = declared.RunModes
	if o.Session.ModelOption != "" || len(o.Session.Models) > 0 {
		c.ModelSelection = true
	}
	if o.Session.EffortOption != "" || len(o.Session.Efforts) > 0 {
		c.EffortControl = true
	}
	if len(o.Session.Modes) > 0 {
		c.PermissionModes = append([]string(nil), o.Session.Modes...)
	}
	if o.Usage {
		c.TokenUsage = true
	}
	if o.Cost {
		c.CostReporting = true
	}
	return c
}

// SessionResume reports which resume mechanism the agent advertised.
func (a AgentInfo) SessionResume() string {
	switch {
	case a.Resume:
		return "resume"
	case a.LoadSession:
		return "load"
	}
	return ""
}

// Matrix renders the agent's reported capabilities as sorted "name=value" lines for reports and drift diffs.
func (a AgentInfo) Matrix() []string {
	b := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	lines := []string{
		"loadSession=" + b(a.LoadSession), "session.resume=" + b(a.Resume), "session.fork=" + b(a.Fork),
		"session.list=" + b(a.List), "session.close=" + b(a.Close), "session.delete=" + b(a.Delete),
		"mcp.http=" + b(a.MCPHTTP), "mcp.sse=" + b(a.MCPSSE), "mcp.acp=" + b(a.MCPACP),
		"prompt.image=" + b(a.Image), "prompt.audio=" + b(a.Audio), "prompt.embeddedContext=" + b(a.EmbeddedContext),
	}
	for _, m := range a.AuthMethods {
		lines = append(lines, "auth="+m.ID)
	}
	sort.Strings(lines)
	return lines
}

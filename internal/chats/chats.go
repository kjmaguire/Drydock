// Package chats is the Chats section (decisions P29): the human talks to an agent CLI directly (Claude Code by default), every
// chat is kept as history, and a previous chat can be read, searched, continued in the CLI's own session, or forked.
//
// A turn is one real agent turn through the Agent Runtime (so MCP attach and agent.* events apply), in the chat's working
// folder, continuing the CLI's own session when the adapter can resume one. The process is stopped after the turn. Rules this
// package enforces:
//   - every stored body and tool input goes through the core's secret scanner first (Deps.Redact);
//   - one turn at a time per chat: a second send while a turn runs is refused, never queued;
//   - transcripts of other chats reach an agent only as quoted data, bounded, oldest trimmed first;
//   - a permission mode that skips approvals is only ever the human's explicit choice for that chat (a fork does not inherit
//     it), and tool allow rules are validated before they are stored;
//   - cost that no turn reported is unknown (NULL), never zero.
//
// The package does not import the core: everything that needs the application is a function in Deps.
package chats

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
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Roles of a stored message.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
	RoleDenied    = "denied"
	RoleError     = "error"
	RoleSystem    = "system"
)

// How a tool call ended (Message.ToolStatus).
const (
	ToolOK     = "ok"
	ToolFailed = "failed"
)

// msgCols are the columns scanMessage reads, in its order.
const msgCols = `id,chat_id,seq,role,body,tool_name,tool_input,turn_id,created_at,tool_call_id,tool_status,tool_result,tool_ms,thinking`

// Chat statuses.
const (
	StatusIdle    = "idle"
	StatusRunning = "running"
	StatusError   = "error"
)

// Turn outcomes (chat.turn events and chat_turns rows).
const (
	TurnRunning = "running"
	TurnDone    = "done"
	TurnError   = "error"
	TurnStopped = "stopped"
)

// AgentPrefix is the prefix of every chat id. A chat's sessions run with the chat id as their agent id, so the application can
// tell a chat session apart (the MCP hook gives it the chats tool instead of a team member's tools).
const AgentPrefix = "chat_"

// ErrBusy is returned when a turn is already running in the chat.
var ErrBusy = errors.New("a turn is already running in this chat: wait for it to finish or stop it")

// ErrNotFound is returned for an unknown chat id.
var ErrNotFound = errors.New("chat not found")

// SkipsApprovals reports whether a permission mode lets the CLI act without anyone approving (decisions P14).
func SkipsApprovals(mode string) bool {
	switch mode {
	case "bypassPermissions", "auto", "dontAsk":
		return true
	}
	return false
}

// Chat is one conversation.
type Chat struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"` // "" = not tied to a project
	Title     string `json:"title"`
	Adapter   string `json:"adapter"`
	// WSLDistro is where the chat's CLI runs: "" on Windows, else the WSL distro whose own install (its own Connection and
	// sign-in) every turn starts inside (decision P41). Chosen when the chat is created; a fork keeps it.
	WSLDistro      string `json:"wslDistro"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	PermissionMode string `json:"permissionMode"`
	// SkipsApprovals is true when PermissionMode lets the CLI act without approval (bypassPermissions, auto, dontAsk): the
	// human chose it for this chat and the UI must show it.
	SkipsApprovals bool     `json:"skipsApprovals"`
	Cwd            string   `json:"cwd"`
	SessionID      string   `json:"sessionId"` // the CLI's own session id; "" = the next turn starts a fresh session
	AllowedTools   []string `json:"allowedTools"`
	ReadHistory    bool     `json:"readHistory"`
	// Skills are the workspace library skills chosen for this chat, and MCPServers the workspace library MCP servers its
	// sessions are granted, with the tools each may call (decision P60). Both reach the CLI through Drydock's MCP gateway.
	Skills     []SkillRef `json:"skills"`
	MCPServers []MCPGrant `json:"mcpServers"`
	Status     string     `json:"status"` // idle | running | error
	LastError  string     `json:"lastError"`
	Pinned     bool       `json:"pinned"`
	Archived   bool       `json:"archived"`
	ArchivedAt int64      `json:"archivedAt"`
	ForkedFrom string     `json:"forkedFrom"`
	Created    int64      `json:"createdAt"`
	Updated    int64      `json:"updatedAt"`
	TokensIn   int64      `json:"tokensIn"`
	TokensOut  int64      `json:"tokensOut"`
	// CostUSD is the sum of the costs the CLI reported; null when no turn reported one (unknown, never zero).
	CostUSD *float64 `json:"costUsd"`
	// CostUnknownTurns counts turns that reported no cost; when it is non-zero CostUSD covers only the other turns.
	CostUnknownTurns int `json:"costUnknownTurns"`
	// Version goes up by one with every write to the chat's row. Two snapshots of a chat are ordered by it (never by the clock):
	// the higher version is the newer state.
	Version int64 `json:"version"`
}

// SkillRef names a workspace library skill chosen for a chat (decision P60).
type SkillRef struct {
	RegistryID string `json:"registryId"`
	Name       string `json:"name"`
}

// MCPGrant is a workspace library MCP server granted to a chat's sessions, with the tools they may call (decision P60). A
// grant chosen without tools gets the server's tools as they are known when it is chosen, by name, without the flagged ones
// (the rule for an agent's grant).
type MCPGrant struct {
	ServerID string   `json:"serverId"`
	Tools    []string `json:"tools"`
}

// ListItem is a chat in chat.list.
type ListItem struct {
	Chat
	Preview      string `json:"preview"`      // the start of the latest user or assistant message
	MessageCount int    `json:"messageCount"` // all stored messages
}

// Message is one stored message.
type Message struct {
	ID        string          `json:"id"`
	ChatID    string          `json:"chatId"`
	Seq       int64           `json:"seq"`
	Role      string          `json:"role"`
	Body      string          `json:"body"`
	ToolName  string          `json:"toolName,omitempty"`
	ToolInput json.RawMessage `json:"toolInput,omitempty"`
	TurnID    string          `json:"turnId,omitempty"`
	Created   int64           `json:"createdAt"`
	// ToolStatus is how a tool call ended: ToolOK, ToolFailed, or "" while it runs (and for calls that reported no result, and
	// rows from before results were kept). ToolResult is what it returned, redacted and bounded; ToolMS how long it took.
	ToolStatus string `json:"toolStatus,omitempty"`
	ToolResult string `json:"toolResult,omitempty"`
	ToolMS     int64  `json:"toolMs,omitempty"`
	// Thinking is the reasoning the CLI reported before this tool call or answer (P89), redacted and bounded.
	Thinking string `json:"thinking,omitempty"`
	toolCallID string // the CLI's id of the call, which its result names
	// SuggestedRule is, for a denied message of an adapter that takes allow rules, the rule chat.allowTool would need so the
	// next turn may use the tool ("Bash(npm test)", "Edit"). Computed when read; empty when no safe rule exists.
	SuggestedRule string `json:"suggestedRule,omitempty"`
}

// Deps are the application services the package uses.
type Deps struct {
	DB      *storage.DB
	Bus     events.Bus
	Runtime *rt.Runtime
	// Redact runs the secret scanner over a text before it is stored or published.
	Redact func(string) string
	// Capabilities returns an adapter's capabilities; ok is false for an adapter Drydock does not know.
	Capabilities func(adapter string) (agents.Capabilities, bool)
	// CheckAdapter refuses an adapter whose Connection is not usable yet (a gate not acknowledged). Optional.
	CheckAdapter func(ctx context.Context, adapter string) error
	// ValidateFields checks model and effort against the adapter (the same rules as for team members). Optional.
	ValidateFields func(adapter, model, effort string) error
	// ProjectDir is the folder a project's chats run in (the repository root, or the project's own workspace folder).
	ProjectDir func(ctx context.Context, projectID string) (string, error)
	// ScratchDir is the private folder of a chat that has no project and no folder the human picked.
	ScratchDir func(chatID string) string
	// CheckWSL refuses running adapter inside the WSL distro for a chat of projectID ("" = none): the adapter cannot run there,
	// the distro is gone, the CLI is not installed or not signed in there, or the project runs a sandbox. The error names the
	// distro. It returns the distro's name as WSL spells it. Nil: WSL chats are refused.
	CheckWSL func(ctx context.Context, projectID, adapter, distro string) (string, error)
	// WSLFolder is the folder of a WSL chat without a project, as Windows names it: cwd "" makes the chat's private folder in
	// the distro user's home; the private folder is made again when it is gone; a folder inside the distro (its Linux path or
	// \\wsl.localhost\<distro>\...) must exist there; a Windows folder must exist on Windows (the distro reaches it through
	// its mount root).
	WSLFolder func(ctx context.Context, distro, chatID, cwd string) (string, error)
	// CheckSkills validates the library skills chosen for a chat of adapter and returns them cleaned (trimmed, without
	// repeats, in the order given). It refuses a skill the library does not hold or holds blocked or gone, and an adapter that
	// cannot reach Drydock's MCP gateway. Nil: chats take no skills.
	CheckSkills func(ctx context.Context, adapter string, refs []SkillRef) ([]SkillRef, error)
	// CheckMCP validates the library MCP servers granted to a chat of adapter and returns the grants with their tools named.
	// Nil: chats take no MCP servers.
	CheckMCP func(ctx context.Context, adapter string, grants []MCPGrant) ([]MCPGrant, error)
	// Instructions is what a turn of the chat adds to the CLI's system prompt (the index of its skills; "" for none). Optional.
	Instructions func(ctx context.Context, c Chat) string
	// Licensed refuses starting work while the license is read-only. Optional.
	Licensed func(ctx context.Context) error
	Logf     func(format string, args ...any)
}

// Options tune the service. The zero value is valid.
type Options struct {
	TurnTimeout     time.Duration // per turn (default 30 minutes)
	TranscriptChars int           // the transcript a fresh session gets when the old one cannot be resumed (default 12000)
	AttachChars     int           // all attached chats together (default 24000)
	MaxAttach       int           // chats one send may attach (default 5)
	MaxTextBytes    int           // a user message (default 100 KiB)
	MaxBodyBytes    int           // a stored assistant message (default 256 KiB)
	MaxToolInput    int           // stored tool input JSON (default 4096)
	MaxToolResult   int           // stored tool result text (default 8192)
	MaxThinking     int           // reasoning kept on one row (default 16384)
	MaxRules        int           // allow rules per chat (default 50)
	DeltaEvery      time.Duration // chat.delta coalescing (default 60ms)
	AskTTL          time.Duration // how long a permission request waits for the person (default AskTTL)
}

func (o *Options) defaults() {
	if o.TurnTimeout <= 0 {
		o.TurnTimeout = 30 * time.Minute
	}
	if o.TranscriptChars <= 0 {
		o.TranscriptChars = 12000
	}
	if o.AttachChars <= 0 {
		o.AttachChars = 24000
	}
	if o.MaxAttach <= 0 {
		o.MaxAttach = 5
	}
	if o.MaxTextBytes <= 0 {
		o.MaxTextBytes = 100 << 10
	}
	if o.MaxBodyBytes <= 0 {
		o.MaxBodyBytes = 256 << 10
	}
	if o.MaxToolInput <= 0 {
		o.MaxToolInput = 4096
	}
	if o.MaxThinking <= 0 {
		o.MaxThinking = 16384
	}
	if o.MaxToolResult <= 0 {
		o.MaxToolResult = 8192
	}
	if o.MaxRules <= 0 {
		o.MaxRules = 50
	}
	if o.DeltaEvery <= 0 {
		o.DeltaEvery = 60 * time.Millisecond
	}
	if o.AskTTL <= 0 {
		o.AskTTL = AskTTL
	}
}

// Service owns chats and their turns.
type Service struct {
	d Deps
	o Options

	bg     context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	running map[string]*turnState
	asks    map[string]*pendingAsk // permission requests waiting for the person, by id
}

type turnState struct {
	turnID string
	cancel context.CancelCauseFunc
}

// New returns a Service. Call Start before use and Close on shutdown.
func New(d Deps, o Options) *Service {
	o.defaults()
	if d.Redact == nil {
		d.Redact = func(s string) string { return s }
	}
	bg, cancel := context.WithCancel(context.Background())
	return &Service{d: d, o: o, bg: bg, cancel: cancel, running: map[string]*turnState{}, asks: map[string]*pendingAsk{}}
}

// Start marks turns a crash left "running" as failed. They are never re-run: a re-run is a new, billed turn nobody asked for.
func (s *Service) Start(ctx context.Context) error {
	return s.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		now := storage.Now()
		const why = "the app stopped while this turn was running"
		if _, err := tx.ExecContext(ctx, `UPDATE chat_turns SET status=?, error=?, ended_at=? WHERE status=?`, TurnError, why, now, TurnRunning); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE chats SET status=?, last_error=?, version=version+1 WHERE status=?`, StatusError, why, StatusRunning)
		return err
	})
}

// Close stops running turns and waits for them to record their end.
func (s *Service) Close() {
	s.mu.Lock()
	for _, t := range s.running {
		t.cancel(errShutdown)
	}
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
}

func (s *Service) logf(format string, a ...any) {
	if s.d.Logf != nil {
		s.d.Logf(format, a...)
	}
}

func (s *Service) publish(ctx context.Context, typ, projectID string, data map[string]any) {
	if s.d.Bus == nil {
		return
	}
	b, _ := json.Marshal(data)
	if _, err := s.d.Bus.Publish(ctx, events.Event{Type: typ, ProjectID: projectID, Data: b}); err != nil {
		s.logf("chats: publish %s: %v", typ, err)
	}
}

// ---- create, read, update, delete ----

// CreateReq is chat.create.
type CreateReq struct {
	ProjectID string `json:"projectId"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title"`
	Adapter   string `json:"adapter"`
	// WSLDistro runs the chat's CLI inside this WSL distro ("" = on Windows).
	WSLDistro      string `json:"wslDistro"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	PermissionMode string `json:"permissionMode"`
	ReadHistory    *bool  `json:"readHistory"`
	// Skills and MCPServers are the chat's properties (decision P60); see Chat.
	Skills     []SkillRef `json:"skills"`
	MCPServers []MCPGrant `json:"mcpServers"`
}

// Create starts a new, empty chat.
func (s *Service) Create(ctx context.Context, r CreateReq) (Chat, error) {
	if s.d.Licensed != nil {
		if err := s.d.Licensed(ctx); err != nil {
			return Chat{}, err
		}
	}
	r.Adapter = strings.TrimSpace(r.Adapter)
	if r.Adapter == "" {
		r.Adapter = "claude"
	}
	caps, ok := s.caps(r.Adapter)
	if !ok {
		return Chat{}, fmt.Errorf("unknown adapter %q", r.Adapter)
	}
	if s.d.CheckAdapter != nil {
		if err := s.d.CheckAdapter(ctx, r.Adapter); err != nil {
			return Chat{}, err
		}
	}
	distro := ""
	if d := strings.TrimSpace(r.WSLDistro); d != "" {
		if s.d.CheckWSL == nil || s.d.WSLFolder == nil {
			return Chat{}, errors.New("a chat cannot run in WSL here")
		}
		var err error
		if distro, err = s.d.CheckWSL(ctx, strings.TrimSpace(r.ProjectID), r.Adapter, d); err != nil {
			return Chat{}, err
		}
	}
	r.Model, r.Effort = strings.TrimSpace(r.Model), strings.TrimSpace(r.Effort)
	if s.d.ValidateFields != nil {
		if err := s.d.ValidateFields(r.Adapter, r.Model, r.Effort); err != nil {
			return Chat{}, err
		}
	}
	mode, err := permissionMode(caps, r.PermissionMode)
	if err != nil {
		return Chat{}, err
	}
	title, err := s.cleanTitle(r.Title)
	if err != nil {
		return Chat{}, err
	}
	c := Chat{ID: ids.New(strings.TrimSuffix(AgentPrefix, "_")), ProjectID: strings.TrimSpace(r.ProjectID), Title: title, Adapter: r.Adapter, WSLDistro: distro, Model: r.Model,
		Effort: r.Effort, PermissionMode: mode, AllowedTools: []string{}, ReadHistory: r.ReadHistory == nil || *r.ReadHistory, Status: StatusIdle}
	if c.Skills, err = s.checkSkills(ctx, c.Adapter, r.Skills); err != nil {
		return Chat{}, err
	}
	if c.MCPServers, err = s.checkMCP(ctx, c.Adapter, r.MCPServers); err != nil {
		return Chat{}, err
	}
	if c.Cwd, err = s.folder(ctx, c.ProjectID, c.WSLDistro, c.ID, r.Cwd); err != nil {
		return Chat{}, err
	}
	c.SkipsApprovals = SkipsApprovals(c.PermissionMode)
	now := storage.Now()
	c.Created, c.Updated, c.Version = now, now, 1
	if err := s.insertChat(ctx, c); err != nil {
		return Chat{}, err
	}
	s.publish(ctx, "chat.created", c.ProjectID, map[string]any{"chatId": c.ID, "chat": c})
	return c, nil
}

func (s *Service) insertChat(ctx context.Context, c Chat) error {
	return s.d.DB.WriteTx(ctx, func(tx *sql.Tx) error { return insertChatTx(ctx, tx, c) })
}

func insertChatTx(ctx context.Context, tx *sql.Tx, c Chat) error {
	rules, _ := json.Marshal(nz(c.AllowedTools))
	sk, _ := json.Marshal(nz(c.Skills))
	ms, _ := json.Marshal(nz(c.MCPServers))
	_, err := tx.ExecContext(ctx, `INSERT INTO chats(id,project_id,title,adapter,model,effort,permission_mode,cwd,session_id,allowed_tools,read_history,status,
		pinned,archived_at,forked_from,created_at,updated_at,wsl_distro,skills,mcp_servers) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.ProjectID, c.Title, c.Adapter, c.Model, c.Effort, c.PermissionMode, c.Cwd, c.SessionID, string(rules), b2i(c.ReadHistory), c.Status,
		b2i(c.Pinned), c.ArchivedAt, c.ForkedFrom, c.Created, c.Updated, c.WSLDistro, string(sk), string(ms))
	return err
}

func nz[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

// folder is the folder a chat runs in: the project's for a project chat (cwd must be empty), else the folder the person
// picked (inside the WSL distro or on Windows for a chat in WSL), else the chat's private folder (made here, or in the
// distro user's home for a chat in WSL).
func (s *Service) folder(ctx context.Context, projectID, distro, chatID, cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	switch {
	case projectID != "":
		if cwd != "" {
			return "", errors.New("a chat in a project runs in the project's folder; pick a folder only for a chat without a project")
		}
		return s.d.ProjectDir(ctx, projectID)
	case distro != "":
		return s.d.WSLFolder(ctx, distro, chatID, cwd)
	case cwd != "":
		return ValidateFolder(cwd)
	}
	if s.d.ScratchDir == nil {
		return "", errors.New("a chat without a project needs a folder")
	}
	dir := s.d.ScratchDir(chatID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create the chat's folder: %w", err)
	}
	return dir, nil
}

// privateFolder reports whether the chat runs in the private folder Drydock made for it (on Windows, or in the distro).
func (s *Service) privateFolder(c Chat) bool {
	if s.d.ScratchDir != nil && filepath.Clean(c.Cwd) == filepath.Clean(s.d.ScratchDir(c.ID)) {
		return true
	}
	return strings.HasSuffix(strings.ReplaceAll(c.Cwd, `\`, "/"), "/.drydock/chats/"+c.ID)
}

// maxChatSkills and maxChatServers bound a chat's properties: each chosen skill is a line of every turn's instructions, each
// server a set of tools in every request.
const (
	maxChatSkills  = 20
	maxChatServers = 10
)

// checkSkills cleans the skills chosen for a chat of adapter (none: an empty list).
func (s *Service) checkSkills(ctx context.Context, adapter string, refs []SkillRef) ([]SkillRef, error) {
	if len(refs) == 0 {
		return []SkillRef{}, nil
	}
	if s.d.CheckSkills == nil {
		return nil, errors.New("skills cannot be given to a chat here")
	}
	if len(refs) > maxChatSkills {
		return nil, fmt.Errorf("a chat can have at most %d skills", maxChatSkills)
	}
	out, err := s.d.CheckSkills(ctx, adapter, refs)
	return nz(out), err
}

// checkMCP validates the MCP servers granted to a chat of adapter (none: an empty list).
func (s *Service) checkMCP(ctx context.Context, adapter string, grants []MCPGrant) ([]MCPGrant, error) {
	if len(grants) == 0 {
		return []MCPGrant{}, nil
	}
	if s.d.CheckMCP == nil {
		return nil, errors.New("MCP servers cannot be given to a chat here")
	}
	if len(grants) > maxChatServers {
		return nil, fmt.Errorf("a chat can have at most %d MCP servers", maxChatServers)
	}
	out, err := s.d.CheckMCP(ctx, adapter, grants)
	return nz(out), err
}

// ValidateFolder checks a folder the human picked for a chat without a project: absolute, existing, a directory. It returns
// the cleaned path.
func ValidateFolder(dir string) (string, error) {
	if strings.ContainsAny(dir, "\x00\r\n") {
		return "", errors.New("the folder path contains a control character")
	}
	dir = strings.TrimSpace(dir)
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("the folder %q is not an absolute path", dir)
	}
	dir = filepath.Clean(dir)
	st, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("the folder %s does not exist", dir)
		}
		return "", fmt.Errorf("the folder %s cannot be used: %w", dir, err)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("%s is a file, not a folder", dir)
	}
	return dir, nil
}

// permissionMode validates a requested mode against the adapter, or picks the default: "default" where the adapter offers it
// (headless Claude then refuses tools outside the mode and the chat shows the refusal), otherwise the agent's own default.
func permissionMode(caps agents.Capabilities, mode string) (string, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		if contains(caps.PermissionModes, "default") {
			return "default", nil
		}
		return "", nil
	}
	if strings.HasPrefix(mode, "-") || strings.ContainsFunc(mode, func(r rune) bool { return r <= ' ' || r == 0x7f }) || len(mode) > 64 {
		return "", fmt.Errorf("the permission mode %q is not valid", mode)
	}
	if len(caps.PermissionModes) > 0 && !contains(caps.PermissionModes, mode) {
		return "", fmt.Errorf("this agent offers these permission modes: %s (got %q)", strings.Join(caps.PermissionModes, ", "), mode)
	}
	return mode, nil
}

func (s *Service) caps(adapter string) (agents.Capabilities, bool) {
	if s.d.Capabilities == nil {
		return agents.Capabilities{}, false
	}
	return s.d.Capabilities(adapter)
}

func (s *Service) cleanTitle(t string) (string, error) {
	t = strings.TrimSpace(strings.ToValidUTF8(t, "�"))
	if strings.ContainsAny(t, "\x00\r\n") {
		return "", errors.New("a title is one line")
	}
	if utf8.RuneCountInString(t) > 200 {
		return "", errors.New("a title is at most 200 characters")
	}
	return s.d.Redact(t), nil
}

const chatCols = `id,project_id,title,adapter,model,effort,permission_mode,cwd,session_id,allowed_tools,read_history,status,last_error,pinned,
	archived_at,forked_from,created_at,updated_at,tokens_in,tokens_out,cost_usd,cost_unknown_turns,version,wsl_distro,skills,mcp_servers`

type scanner interface{ Scan(dest ...any) error }

func scanChat(r scanner, extra ...any) (Chat, error) {
	var c Chat
	var rules, skills, servers string
	var readHistory, pinned int64
	var cost sql.NullFloat64
	dest := []any{&c.ID, &c.ProjectID, &c.Title, &c.Adapter, &c.Model, &c.Effort, &c.PermissionMode, &c.Cwd, &c.SessionID, &rules, &readHistory, &c.Status,
		&c.LastError, &pinned, &c.ArchivedAt, &c.ForkedFrom, &c.Created, &c.Updated, &c.TokensIn, &c.TokensOut, &cost, &c.CostUnknownTurns, &c.Version, &c.WSLDistro,
		&skills, &servers}
	if err := r.Scan(append(dest, extra...)...); err != nil {
		return c, err
	}
	c.AllowedTools = []string{}
	_ = json.Unmarshal([]byte(rules), &c.AllowedTools)
	_ = json.Unmarshal([]byte(skills), &c.Skills)
	_ = json.Unmarshal([]byte(servers), &c.MCPServers)
	c.Skills, c.MCPServers = nz(c.Skills), nz(c.MCPServers)
	for i := range c.MCPServers {
		c.MCPServers[i].Tools = nz(c.MCPServers[i].Tools)
	}
	c.ReadHistory, c.Pinned, c.Archived = readHistory != 0, pinned != 0, c.ArchivedAt != 0
	if cost.Valid {
		v := cost.Float64
		c.CostUSD = &v
	}
	c.SkipsApprovals = SkipsApprovals(c.PermissionMode)
	return c, nil
}

// Get returns one chat.
func (s *Service) Get(ctx context.Context, id string) (Chat, error) {
	return getChat(s.d.DB.Reader().QueryRowContext(ctx, `SELECT `+chatCols+` FROM chats WHERE id=?`, id), id)
}

// getTx reads one chat inside a write transaction, so it sees that transaction's own writes.
func getTx(ctx context.Context, tx *sql.Tx, id string) (Chat, error) {
	return getChat(tx.QueryRowContext(ctx, `SELECT `+chatCols+` FROM chats WHERE id=?`, id), id)
}

func getChat(r scanner, id string) (Chat, error) {
	c, err := scanChat(r)
	if errors.Is(err, sql.ErrNoRows) {
		return c, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return c, err
}

// ListReq is chat.list.
type ListReq struct {
	ProjectID       string `json:"projectId"` // only this project's chats; "" = every chat
	Query           string `json:"query"`     // full-text over titles and messages
	IncludeArchived bool   `json:"includeArchived"`
	Limit           int    `json:"limit"` // at most this many (1-500; 0 = 500)
}

// List returns chats, pinned first, then the most recently updated.
func (s *Service) List(ctx context.Context, r ListReq) ([]ListItem, error) {
	q := `SELECT ` + prefixCols("c.") + `,
		COALESCE((SELECT m.body FROM chat_messages m WHERE m.chat_id=c.id AND m.role IN ('user','assistant') ORDER BY m.seq DESC LIMIT 1),''),
		(SELECT COUNT(*) FROM chat_messages m WHERE m.chat_id=c.id)
		FROM chats c WHERE 1=1`
	var args []any
	if r.ProjectID != "" {
		q += ` AND c.project_id=?`
		args = append(args, r.ProjectID)
	}
	if !r.IncludeArchived {
		q += ` AND c.archived_at=0`
	}
	if strings.TrimSpace(r.Query) != "" {
		match := ftsMatch(r.Query)
		if match == "" {
			return []ListItem{}, nil
		}
		q += ` AND (c.rowid IN (SELECT rowid FROM chats_fts WHERE chats_fts MATCH ?) OR c.id IN (SELECT m.chat_id FROM chat_messages_fts f JOIN chat_messages m ON m.rowid=f.rowid WHERE chat_messages_fts MATCH ?))`
		args = append(args, match, match)
	}
	limit := r.Limit
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	q += ` ORDER BY c.pinned DESC, c.updated_at DESC, c.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.d.DB.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ListItem{}
	for rows.Next() {
		var it ListItem
		var preview string
		c, err := scanChat(rows, &preview, &it.MessageCount)
		if err != nil {
			return nil, err
		}
		it.Chat, it.Preview = c, clip(oneLine(preview), 160)
		out = append(out, it)
	}
	return out, rows.Err()
}

func prefixCols(p string) string {
	parts := strings.Split(chatCols, ",")
	for i, c := range parts {
		parts[i] = p + strings.TrimSpace(c)
	}
	return strings.Join(parts, ",")
}

// Page is a page of messages, oldest first (chat.messages, and the latest messages of chat.get).
type Page struct {
	Messages []Message `json:"messages"`
	HasMore  bool      `json:"hasMore"` // older messages exist (chat.messages with beforeSeq)
}

// Detail is chat.get: the chat and its latest messages.
type Detail struct {
	Chat Chat `json:"chat"`
	Page
}

// Detail returns a chat with its latest messages.
func (s *Service) Detail(ctx context.Context, id string, limit int) (Detail, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	if limit <= 0 {
		limit = 200
	}
	ms, more, err := s.Messages(ctx, id, 0, limit)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Chat: c, Page: Page{Messages: ms, HasMore: more}}, nil
}

// Messages returns up to limit messages before beforeSeq (0 = the latest), oldest first, and whether older ones exist.
func (s *Service) Messages(ctx context.Context, id string, beforeSeq int64, limit int) ([]Message, bool, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	c, err := s.Get(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if beforeSeq <= 0 {
		beforeSeq = 1 << 62
	}
	rows, err := s.d.DB.Reader().QueryContext(ctx, `SELECT `+msgCols+` FROM chat_messages
		WHERE chat_id=? AND seq<? ORDER BY seq DESC LIMIT ?`, id, beforeSeq, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	caps, _ := s.caps(c.Adapter)
	for i := range out {
		if out[i].Role == RoleDenied && takesRules(c.Adapter, caps) {
			out[i].SuggestedRule = SuggestRule(out[i].ToolName, out[i].ToolInput)
		}
	}
	if out == nil {
		out = []Message{}
	}
	return out, more, nil
}

func scanMessage(r scanner) (Message, error) {
	var m Message
	var input string
	if err := r.Scan(&m.ID, &m.ChatID, &m.Seq, &m.Role, &m.Body, &m.ToolName, &input, &m.TurnID, &m.Created, &m.toolCallID, &m.ToolStatus, &m.ToolResult, &m.ToolMS, &m.Thinking); err != nil {
		return m, err
	}
	if input != "" {
		m.ToolInput = json.RawMessage(input)
	}
	return m, nil
}

// takesRules reports whether the adapter honours per-chat allow rules. Only Claude Code takes --allowedTools; an ACP agent's
// permission requests are answered by the application-wide handler (rejected), so a rule would do nothing there.
func takesRules(adapter string, caps agents.Capabilities) bool {
	return adapter == "claude" && caps.Headless
}

// UpdateReq is chat.update. Nil fields are left as they are.
type UpdateReq struct {
	ID             string  `json:"id"`
	Title          *string `json:"title"`
	Pinned         *bool   `json:"pinned"`
	Archived       *bool   `json:"archived"`
	Model          *string `json:"model"`
	Effort         *string `json:"effort"`
	PermissionMode *string `json:"permissionMode"`
	ReadHistory    *bool   `json:"readHistory"`
	// The chat's properties (decision P60). Adapter, WSLDistro and Cwd change where the chat runs: refused while a turn runs,
	// and the next turn starts a new session given the conversation so far. Cwd "" is the chat's private folder; a project
	// chat runs in its project's folder and takes no Cwd. Skills and MCPServers replace the chat's lists.
	Adapter    *string     `json:"adapter"`
	WSLDistro  *string     `json:"wslDistro"`
	Cwd        *string     `json:"cwd"`
	Skills     *[]SkillRef `json:"skills"`
	MCPServers *[]MCPGrant `json:"mcpServers"`
}

// placeChange is a change of where a chat runs, checked before the transaction that stores it (the checks start programs).
type placeChange struct {
	adapter, distro, cwd string
	moved                bool // the adapter, the distro or the folder changed: the CLI session cannot be continued
}

// checkPlace validates a change of the chat's adapter, WSL distro or folder. It returns nil when the request changes none.
func (s *Service) checkPlace(ctx context.Context, c Chat, r UpdateReq) (*placeChange, error) {
	if r.Adapter == nil && r.WSLDistro == nil && r.Cwd == nil {
		return nil, nil
	}
	p := &placeChange{adapter: c.Adapter, distro: c.WSLDistro, cwd: c.Cwd}
	if r.Adapter != nil {
		if p.adapter = strings.TrimSpace(*r.Adapter); p.adapter == "" {
			p.adapter = "claude"
		}
	}
	if r.WSLDistro != nil {
		p.distro = strings.TrimSpace(*r.WSLDistro)
	}
	if p.adapter != c.Adapter {
		if _, ok := s.caps(p.adapter); !ok {
			return nil, fmt.Errorf("unknown adapter %q", p.adapter)
		}
		if s.d.CheckAdapter != nil {
			if err := s.d.CheckAdapter(ctx, p.adapter); err != nil {
				return nil, err
			}
		}
	}
	if p.distro != "" && (p.distro != c.WSLDistro || p.adapter != c.Adapter) {
		if s.d.CheckWSL == nil || s.d.WSLFolder == nil {
			return nil, errors.New("a chat cannot run in WSL here")
		}
		d, err := s.d.CheckWSL(ctx, c.ProjectID, p.adapter, p.distro)
		if err != nil {
			return nil, err
		}
		p.distro = d
	}
	if c.ProjectID != "" {
		if r.Cwd != nil && strings.TrimSpace(*r.Cwd) != "" {
			return nil, errors.New("a chat in a project runs in the project's folder; pick a folder only for a chat without a project")
		}
	} else if r.Cwd != nil || p.distro != c.WSLDistro {
		want := c.Cwd
		switch {
		case r.Cwd != nil:
			want = *r.Cwd
		case s.privateFolder(c):
			want = "" // the private folder of the other side
		}
		dir, err := s.folder(ctx, "", p.distro, c.ID, want)
		if err != nil {
			return nil, err
		}
		p.cwd = dir
	}
	p.moved = p.adapter != c.Adapter || p.distro != c.WSLDistro || p.cwd != c.Cwd
	return p, nil
}

// Update changes a chat's settings. Model, effort, permission mode, skills and MCP servers apply from the next turn, which
// continues the session; a new adapter, distro or folder starts a new session given the conversation so far. The row is read,
// changed and written in one transaction, and the committed row (with its new version) is what is returned and published, so a
// reply never carries a status or counters from before a write that happened meanwhile.
func (s *Service) Update(ctx context.Context, r UpdateReq) (Chat, error) {
	before, err := s.Get(ctx, r.ID)
	if err != nil {
		return Chat{}, err
	}
	place, err := s.checkPlace(ctx, before, r)
	if err != nil {
		return Chat{}, err
	}
	adapter := before.Adapter
	if place != nil {
		adapter = place.adapter
	}
	var skills *[]SkillRef
	if r.Skills != nil || (adapter != before.Adapter && len(before.Skills) > 0) {
		in := before.Skills
		if r.Skills != nil {
			in = *r.Skills
		}
		v, err := s.checkSkills(ctx, adapter, in)
		if err != nil {
			return Chat{}, err
		}
		skills = &v
	}
	var servers *[]MCPGrant
	if r.MCPServers != nil || (adapter != before.Adapter && len(before.MCPServers) > 0) {
		in := before.MCPServers
		if r.MCPServers != nil {
			in = *r.MCPServers
		}
		v, err := s.checkMCP(ctx, adapter, in)
		if err != nil {
			return Chat{}, err
		}
		servers = &v
	}
	var out Chat
	var notes []string
	err = s.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		notes = nil // WriteTx may run this more than once
		c, err := getTx(ctx, tx, r.ID)
		if err != nil {
			return err
		}
		reset := false
		if place != nil && place.moved {
			if s.isRunning(c.ID) || c.Status == StatusRunning {
				return errors.New("stop the running turn before changing where the chat runs")
			}
			if c.Adapter != place.adapter {
				caps, _ := s.caps(place.adapter)
				// what the old agent took and the new one does not is reset to the new one's defaults
				if s.d.ValidateFields != nil && s.d.ValidateFields(place.adapter, c.Model, c.Effort) != nil {
					c.Model, c.Effort = "", ""
				}
				if m, err := permissionMode(caps, c.PermissionMode); err == nil {
					c.PermissionMode = m
				} else {
					c.PermissionMode, _ = permissionMode(caps, "")
				}
			}
			c.Adapter, c.WSLDistro, c.Cwd = place.adapter, place.distro, place.cwd
			reset = true
			where := adapterName(c.Adapter)
			if c.WSLDistro != "" {
				where += " in " + c.WSLDistro + " (WSL)"
			}
			notes = append(notes, "Now runs with "+where+" in "+c.Cwd+": the next message starts a new session, given the conversation so far.")
		}
		if skills != nil && !sameSkills(c.Skills, *skills) {
			c.Skills = *skills
			notes = append(notes, skillsNote(c.Skills))
		}
		if servers != nil && !sameServers(c.MCPServers, *servers) {
			c.MCPServers = *servers
			notes = append(notes, serversNote(c.MCPServers))
		}
		if r.Title != nil {
			if c.Title, err = s.cleanTitle(*r.Title); err != nil {
				return err
			}
		}
		if r.Pinned != nil {
			c.Pinned = *r.Pinned
		}
		if r.Archived != nil {
			switch {
			case *r.Archived && c.ArchivedAt == 0:
				if s.isRunning(c.ID) {
					return errors.New("stop the running turn before archiving the chat")
				}
				c.ArchivedAt = storage.Now()
			case !*r.Archived:
				c.ArchivedAt = 0
			}
		}
		if r.Model != nil || r.Effort != nil {
			model, effort := c.Model, c.Effort
			if r.Model != nil {
				model = strings.TrimSpace(*r.Model)
			}
			if r.Effort != nil {
				effort = strings.TrimSpace(*r.Effort)
			}
			if s.d.ValidateFields != nil {
				if err := s.d.ValidateFields(c.Adapter, model, effort); err != nil {
					return err
				}
			}
			c.Model, c.Effort = model, effort
		}
		if r.PermissionMode != nil {
			caps, _ := s.caps(c.Adapter)
			if c.PermissionMode, err = permissionMode(caps, *r.PermissionMode); err != nil {
				return err
			}
		}
		if r.ReadHistory != nil {
			c.ReadHistory = *r.ReadHistory
		}
		sk, _ := json.Marshal(nz(c.Skills))
		ms, _ := json.Marshal(nz(c.MCPServers))
		q := `UPDATE chats SET title=?, pinned=?, archived_at=?, model=?, effort=?, permission_mode=?, read_history=?, adapter=?, wsl_distro=?, cwd=?, skills=?, mcp_servers=?, updated_at=?, version=version+1`
		if reset {
			// the CLI's session belongs to the old agent or folder: the next turn starts a new one, given the transcript
			q += `, session_id='', cli_usage=''`
		}
		if _, err := tx.ExecContext(ctx, q+` WHERE id=?`, c.Title, b2i(c.Pinned), c.ArchivedAt, c.Model, c.Effort, c.PermissionMode, b2i(c.ReadHistory),
			c.Adapter, c.WSLDistro, c.Cwd, string(sk), string(ms), storage.Now(), c.ID); err != nil {
			return err
		}
		out, err = getTx(ctx, tx, c.ID)
		return err
	})
	if err != nil {
		return Chat{}, err
	}
	if len(notes) > 0 {
		for _, n := range notes {
			if _, err := s.insertMessage(ctx, out, "", RoleSystem, n, "", nil); err != nil {
				s.logf("chats: note a change: %v", err)
			}
		}
		if nc, err := s.Get(ctx, out.ID); err == nil {
			out = nc // the notes raised the version
		}
	}
	s.publish(ctx, "chat.updated", out.ProjectID, map[string]any{"chatId": out.ID, "chat": out})
	return out, nil
}

func adapterName(id string) string {
	if id == "claude" {
		return "Claude Code"
	}
	return id
}

func sameSkills(a, b []SkillRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameServers(a, b []MCPGrant) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ServerID != b[i].ServerID || strings.Join(a[i].Tools, ",") != strings.Join(b[i].Tools, ",") {
			return false
		}
	}
	return true
}

// skillsNote and serversNote are the system notes that record a change of the chat's skills or MCP servers in its history.
func skillsNote(ks []SkillRef) string {
	if len(ks) == 0 {
		return "No skills for this chat from the next message."
	}
	var names []string
	for _, k := range ks {
		names = append(names, k.Name)
	}
	return "Skills for this chat from the next message: " + strings.Join(names, ", ") + "."
}

func serversNote(gs []MCPGrant) string {
	if len(gs) == 0 {
		return "No MCP servers for this chat from the next message."
	}
	var names []string
	for _, g := range gs {
		names = append(names, g.ServerID)
	}
	return "MCP servers for this chat from the next message: " + strings.Join(names, ", ") + "."
}

// Delete removes a chat with its messages and turns. A running chat must be stopped first.
func (s *Service) Delete(ctx context.Context, id string) error {
	c, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if s.isRunning(id) {
		return errors.New("stop the running turn before deleting the chat")
	}
	if err := s.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{`DELETE FROM chat_messages WHERE chat_id=?`, `DELETE FROM chat_turns WHERE chat_id=?`, `DELETE FROM chats WHERE id=?`} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	s.publish(ctx, "chat.deleted", c.ProjectID, map[string]any{"chatId": id})
	return nil
}

// Fork starts a new chat with the messages of another up to and including uptoSeq (0 = all). The fork has no CLI session:
// its first turn gives a fresh session the transcript. It keeps adapter, WSL distro, folder, model, effort, allow rules, skills
// and MCP servers; a permission mode
// that skips approvals is not inherited (the human chooses that per chat).
func (s *Service) Fork(ctx context.Context, id string, uptoSeq int64) (Chat, error) {
	src, err := s.Get(ctx, id)
	if err != nil {
		return Chat{}, err
	}
	if uptoSeq <= 0 {
		uptoSeq = 1 << 62
	}
	n := src
	n.ID = ids.New(strings.TrimSuffix(AgentPrefix, "_"))
	n.SessionID, n.Status, n.LastError, n.Pinned, n.ArchivedAt, n.Archived = "", StatusIdle, "", false, 0, false
	n.TokensIn, n.TokensOut, n.CostUSD, n.CostUnknownTurns, n.ForkedFrom = 0, 0, nil, 0, src.ID
	if src.Title != "" {
		n.Title = clipRunes("Fork of "+src.Title, 200)
	}
	if SkipsApprovals(n.PermissionMode) {
		caps, _ := s.caps(n.Adapter)
		n.PermissionMode, _ = permissionMode(caps, "")
	}
	n.SkipsApprovals = SkipsApprovals(n.PermissionMode)
	now := storage.Now()
	n.Created, n.Updated, n.Version = now, now, 1
	err = s.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := insertChatTx(ctx, tx, n); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT seq,role,body,tool_name,tool_input,created_at,tool_status,tool_result,tool_ms,thinking FROM chat_messages WHERE chat_id=? AND seq<=? ORDER BY seq`, src.ID, uptoSeq)
		if err != nil {
			return err
		}
		type row struct {
			seq                                   int64
			role, body, tool, inp, status, result string
			thinking                              string
			created, ms                           int64
		}
		var copyRows []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.seq, &r.role, &r.body, &r.tool, &r.inp, &r.created, &r.status, &r.result, &r.ms, &r.thinking); err != nil {
				rows.Close()
				return err
			}
			copyRows = append(copyRows, r)
		}
		rows.Close()
		for i, r := range copyRows {
			if _, err := tx.ExecContext(ctx, `INSERT INTO chat_messages(id,chat_id,seq,role,body,tool_name,tool_input,turn_id,created_at,tool_status,tool_result,tool_ms,thinking) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				ids.New("cmsg"), n.ID, i+1, r.role, r.body, r.tool, r.inp, "", r.created, r.status, r.result, r.ms, r.thinking); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Chat{}, err
	}
	s.publish(ctx, "chat.created", n.ProjectID, map[string]any{"chatId": n.ID, "chat": n, "forkedFrom": src.ID})
	return n, nil
}

// ---- allow rules ----

// AllowTool adds an allow rule to a chat (Claude --allowedTools). It applies from the next turn.
func (s *Service) AllowTool(ctx context.Context, id, rule string) (Chat, error) {
	return s.editRules(ctx, id, func(c Chat) ([]string, string, error) {
		caps, _ := s.caps(c.Adapter)
		if !takesRules(c.Adapter, caps) {
			return nil, "", fmt.Errorf("%s does not take per-chat allow rules: its permission requests are answered by Drydock and refused", c.Adapter)
		}
		r, err := ValidateRule(rule)
		if err != nil {
			return nil, "", err
		}
		if contains(c.AllowedTools, r) {
			return nil, "", nil
		}
		if len(c.AllowedTools) >= s.o.MaxRules {
			return nil, "", fmt.Errorf("a chat can have at most %d allow rules", s.o.MaxRules)
		}
		return append(c.AllowedTools, r), "Allowed for this chat: " + r, nil
	})
}

// DisallowTool removes an allow rule.
func (s *Service) DisallowTool(ctx context.Context, id, rule string) (Chat, error) {
	rule = strings.TrimSpace(rule)
	return s.editRules(ctx, id, func(c Chat) ([]string, string, error) {
		keep := []string{}
		for _, r := range c.AllowedTools {
			if r != rule {
				keep = append(keep, r)
			}
		}
		if len(keep) == len(c.AllowedTools) {
			return nil, "", nil
		}
		return keep, "No longer allowed for this chat: " + rule, nil
	})
}

// editRules changes a chat's allow rules in one transaction: edit gets the current row and returns the new rules and the note
// that records the change, or nil rules for no change. The committed row is returned and published, as Update does.
func (s *Service) editRules(ctx context.Context, id string, edit func(Chat) (rules []string, note string, err error)) (Chat, error) {
	var out Chat
	var note string
	changed := false
	err := s.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		changed = false // WriteTx may run this more than once
		c, err := getTx(ctx, tx, id)
		if err != nil {
			return err
		}
		var rules []string
		if rules, note, err = edit(c); err != nil {
			return err
		}
		if rules == nil {
			out = c
			return nil
		}
		b, _ := json.Marshal(rules)
		if _, err := tx.ExecContext(ctx, `UPDATE chats SET allowed_tools=?, updated_at=?, version=version+1 WHERE id=?`, string(b), storage.Now(), id); err != nil {
			return err
		}
		changed = true
		out, err = getTx(ctx, tx, id)
		return err
	})
	if err != nil {
		return Chat{}, err
	}
	if !changed {
		return out, nil
	}
	if _, err := s.insertMessage(ctx, out, "", RoleSystem, note, "", nil); err != nil {
		s.logf("chats: note rule change: %v", err)
	}
	s.publish(ctx, "chat.updated", out.ProjectID, map[string]any{"chatId": out.ID, "chat": out})
	return out, nil
}

// ---- messages ----

// insertMessage redacts and stores one message, then publishes chat.message.
// msgExtra is what some rows carry beyond the common fields: a tool call's CLI id, and the reasoning before the row (P89).
type msgExtra struct {
	callID   string
	thinking string
}

func (s *Service) insertMessage(ctx context.Context, c Chat, turnID, role, body, toolName string, toolInput json.RawMessage, extra ...msgExtra) (Message, error) {
	body = strings.ToValidUTF8(body, "�")
	if len(body) > s.o.MaxBodyBytes {
		body = cutUTF8(body, s.o.MaxBodyBytes) + "\n[cut by Drydock: the message was longer than it keeps]"
	}
	m := Message{ID: ids.New("cmsg"), ChatID: c.ID, Role: role, Body: s.d.Redact(body), ToolName: s.d.Redact(toolName), TurnID: turnID, Created: storage.Now()}
	if len(toolInput) > 0 {
		m.ToolInput = s.boundInput(toolInput)
	}
	if len(extra) > 0 {
		m.toolCallID = extra[0].callID
		m.Thinking = s.boundThinking(extra[0].thinking)
	}
	err := s.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM chat_messages WHERE chat_id=?`, c.ID).Scan(&m.Seq); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO chat_messages(id,chat_id,seq,role,body,tool_name,tool_input,turn_id,created_at,tool_call_id,thinking) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			m.ID, m.ChatID, m.Seq, m.Role, m.Body, m.ToolName, string(m.ToolInput), m.TurnID, m.Created, m.toolCallID, m.Thinking); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE chats SET updated_at=?, version=version+1 WHERE id=?`, m.Created, c.ID)
		return err
	})
	if err != nil {
		return Message{}, err
	}
	if m.Role == RoleDenied {
		if caps, _ := s.caps(c.Adapter); takesRules(c.Adapter, caps) {
			m.SuggestedRule = SuggestRule(m.ToolName, m.ToolInput)
		}
	}
	s.publish(ctx, "chat.message", c.ProjectID, map[string]any{"chatId": c.ID, "message": m})
	return m, nil
}

// boundThinking redacts reasoning and keeps at most MaxThinking bytes of it.
func (s *Service) boundThinking(text string) string {
	text = strings.TrimSpace(strings.ToValidUTF8(text, "\uFFFD"))
	if text == "" {
		return ""
	}
	text = s.d.Redact(text)
	if len(text) > s.o.MaxThinking {
		text = cutUTF8(text, s.o.MaxThinking) + "\n[cut by Drydock: the reasoning was longer than it keeps]"
	}
	return text
}

// boundInput redacts tool input and keeps it valid JSON of at most MaxToolInput bytes.
func (s *Service) boundInput(in json.RawMessage) json.RawMessage {
	red := s.d.Redact(string(in))
	if !json.Valid([]byte(red)) {
		b, _ := json.Marshal(map[string]string{"text": red})
		red = string(b)
	}
	if len(red) <= s.o.MaxToolInput {
		return json.RawMessage(red)
	}
	for keep := s.o.MaxToolInput - 64; keep > 0; keep -= 256 {
		b, _ := json.Marshal(map[string]any{"truncated": true, "preview": cutUTF8(red, keep)})
		if len(b) <= s.o.MaxToolInput {
			return b
		}
	}
	return json.RawMessage(`{"truncated":true}`)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

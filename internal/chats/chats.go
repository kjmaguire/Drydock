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
	ID             string `json:"id"`
	ProjectID      string `json:"projectId"` // "" = not tied to a project
	Title          string `json:"title"`
	Adapter        string `json:"adapter"`
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
	Status         string   `json:"status"` // idle | running | error
	LastError      string   `json:"lastError"`
	Pinned         bool     `json:"pinned"`
	Archived       bool     `json:"archived"`
	ArchivedAt     int64    `json:"archivedAt"`
	ForkedFrom     string   `json:"forkedFrom"`
	Created        int64    `json:"createdAt"`
	Updated        int64    `json:"updatedAt"`
	TokensIn       int64    `json:"tokensIn"`
	TokensOut      int64    `json:"tokensOut"`
	// CostUSD is the sum of the costs the CLI reported; null when no turn reported one (unknown, never zero).
	CostUSD *float64 `json:"costUsd"`
	// CostUnknownTurns counts turns that reported no cost; when it is non-zero CostUSD covers only the other turns.
	CostUnknownTurns int `json:"costUnknownTurns"`
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
	MaxRules        int           // allow rules per chat (default 50)
	DeltaEvery      time.Duration // chat.delta coalescing (default 60ms)
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
	if o.MaxRules <= 0 {
		o.MaxRules = 50
	}
	if o.DeltaEvery <= 0 {
		o.DeltaEvery = 60 * time.Millisecond
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
	return &Service{d: d, o: o, bg: bg, cancel: cancel, running: map[string]*turnState{}}
}

// Start marks turns a crash left "running" as failed. They are never re-run: a re-run is a new, billed turn nobody asked for.
func (s *Service) Start(ctx context.Context) error {
	return s.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		now := storage.Now()
		const why = "the app stopped while this turn was running"
		if _, err := tx.ExecContext(ctx, `UPDATE chat_turns SET status=?, error=?, ended_at=? WHERE status=?`, TurnError, why, now, TurnRunning); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE chats SET status=?, last_error=? WHERE status=?`, StatusError, why, StatusRunning)
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
	ProjectID      string `json:"projectId"`
	Cwd            string `json:"cwd"`
	Title          string `json:"title"`
	Adapter        string `json:"adapter"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	PermissionMode string `json:"permissionMode"`
	ReadHistory    *bool  `json:"readHistory"`
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
	c := Chat{ID: ids.New(strings.TrimSuffix(AgentPrefix, "_")), ProjectID: strings.TrimSpace(r.ProjectID), Title: title, Adapter: r.Adapter, Model: r.Model,
		Effort: r.Effort, PermissionMode: mode, AllowedTools: []string{}, ReadHistory: r.ReadHistory == nil || *r.ReadHistory, Status: StatusIdle}
	switch {
	case c.ProjectID != "":
		if strings.TrimSpace(r.Cwd) != "" {
			return Chat{}, errors.New("a chat in a project runs in the project's folder; pick a folder only for a chat without a project")
		}
		dir, err := s.d.ProjectDir(ctx, c.ProjectID)
		if err != nil {
			return Chat{}, err
		}
		c.Cwd = dir
	case strings.TrimSpace(r.Cwd) != "":
		dir, err := ValidateFolder(r.Cwd)
		if err != nil {
			return Chat{}, err
		}
		c.Cwd = dir
	default:
		if s.d.ScratchDir == nil {
			return Chat{}, errors.New("a chat without a project needs a folder")
		}
		c.Cwd = s.d.ScratchDir(c.ID)
		if err := os.MkdirAll(c.Cwd, 0o755); err != nil {
			return Chat{}, fmt.Errorf("create the chat's folder: %w", err)
		}
	}
	c.SkipsApprovals = SkipsApprovals(c.PermissionMode)
	now := storage.Now()
	c.Created, c.Updated = now, now
	if err := s.insertChat(ctx, c); err != nil {
		return Chat{}, err
	}
	s.publish(ctx, "chat.created", c.ProjectID, map[string]any{"chatId": c.ID, "chat": c})
	return c, nil
}

func (s *Service) insertChat(ctx context.Context, c Chat) error {
	rules, _ := json.Marshal(c.AllowedTools)
	return s.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO chats(id,project_id,title,adapter,model,effort,permission_mode,cwd,session_id,allowed_tools,read_history,status,
			pinned,archived_at,forked_from,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.ID, c.ProjectID, c.Title, c.Adapter, c.Model, c.Effort, c.PermissionMode, c.Cwd, c.SessionID, string(rules), b2i(c.ReadHistory), c.Status,
			b2i(c.Pinned), c.ArchivedAt, c.ForkedFrom, c.Created, c.Updated)
		return err
	})
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
	archived_at,forked_from,created_at,updated_at,tokens_in,tokens_out,cost_usd,cost_unknown_turns`

type scanner interface{ Scan(dest ...any) error }

func scanChat(r scanner, extra ...any) (Chat, error) {
	var c Chat
	var rules string
	var readHistory, pinned int64
	var cost sql.NullFloat64
	dest := []any{&c.ID, &c.ProjectID, &c.Title, &c.Adapter, &c.Model, &c.Effort, &c.PermissionMode, &c.Cwd, &c.SessionID, &rules, &readHistory, &c.Status,
		&c.LastError, &pinned, &c.ArchivedAt, &c.ForkedFrom, &c.Created, &c.Updated, &c.TokensIn, &c.TokensOut, &cost, &c.CostUnknownTurns}
	if err := r.Scan(append(dest, extra...)...); err != nil {
		return c, err
	}
	c.AllowedTools = []string{}
	_ = json.Unmarshal([]byte(rules), &c.AllowedTools)
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
	c, err := scanChat(s.d.DB.Reader().QueryRowContext(ctx, `SELECT `+chatCols+` FROM chats WHERE id=?`, id))
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
	q += ` ORDER BY c.pinned DESC, c.updated_at DESC, c.id DESC LIMIT 500`
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

// Detail is chat.get.
type Detail struct {
	Chat     Chat      `json:"chat"`
	Messages []Message `json:"messages"` // the latest messages, oldest first
	HasMore  bool      `json:"hasMore"`  // older messages exist (chat.messages with beforeSeq)
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
	return Detail{Chat: c, Messages: ms, HasMore: more}, nil
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
	rows, err := s.d.DB.Reader().QueryContext(ctx, `SELECT id,chat_id,seq,role,body,tool_name,tool_input,turn_id,created_at FROM chat_messages
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
	if err := r.Scan(&m.ID, &m.ChatID, &m.Seq, &m.Role, &m.Body, &m.ToolName, &input, &m.TurnID, &m.Created); err != nil {
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
}

// Update changes a chat's settings. Model, effort and permission mode apply from the next turn.
func (s *Service) Update(ctx context.Context, r UpdateReq) (Chat, error) {
	c, err := s.Get(ctx, r.ID)
	if err != nil {
		return Chat{}, err
	}
	if r.Title != nil {
		if c.Title, err = s.cleanTitle(*r.Title); err != nil {
			return Chat{}, err
		}
	}
	if r.Pinned != nil {
		c.Pinned = *r.Pinned
	}
	if r.Archived != nil {
		switch {
		case *r.Archived && c.ArchivedAt == 0:
			if s.isRunning(c.ID) {
				return Chat{}, errors.New("stop the running turn before archiving the chat")
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
				return Chat{}, err
			}
		}
		c.Model, c.Effort = model, effort
	}
	if r.PermissionMode != nil {
		caps, _ := s.caps(c.Adapter)
		if c.PermissionMode, err = permissionMode(caps, *r.PermissionMode); err != nil {
			return Chat{}, err
		}
	}
	if r.ReadHistory != nil {
		c.ReadHistory = *r.ReadHistory
	}
	c.SkipsApprovals = SkipsApprovals(c.PermissionMode)
	c.Updated = storage.Now()
	if err := s.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE chats SET title=?, pinned=?, archived_at=?, model=?, effort=?, permission_mode=?, read_history=?, updated_at=? WHERE id=?`,
			c.Title, b2i(c.Pinned), c.ArchivedAt, c.Model, c.Effort, c.PermissionMode, b2i(c.ReadHistory), c.Updated, c.ID)
		return err
	}); err != nil {
		return Chat{}, err
	}
	s.publish(ctx, "chat.updated", c.ProjectID, map[string]any{"chatId": c.ID, "chat": c})
	return c, nil
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
// its first turn gives a fresh session the transcript. It keeps adapter, model, effort and allow rules; a permission mode
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
	n.Created, n.Updated = now, now
	rules, _ := json.Marshal(n.AllowedTools)
	err = s.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO chats(id,project_id,title,adapter,model,effort,permission_mode,cwd,session_id,allowed_tools,read_history,status,
			pinned,archived_at,forked_from,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			n.ID, n.ProjectID, n.Title, n.Adapter, n.Model, n.Effort, n.PermissionMode, n.Cwd, "", string(rules), b2i(n.ReadHistory), n.Status, 0, 0, n.ForkedFrom, n.Created, n.Updated); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT seq,role,body,tool_name,tool_input,created_at FROM chat_messages WHERE chat_id=? AND seq<=? ORDER BY seq`, src.ID, uptoSeq)
		if err != nil {
			return err
		}
		type row struct {
			seq                   int64
			role, body, tool, inp string
			created               int64
		}
		var copyRows []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.seq, &r.role, &r.body, &r.tool, &r.inp, &r.created); err != nil {
				rows.Close()
				return err
			}
			copyRows = append(copyRows, r)
		}
		rows.Close()
		for i, r := range copyRows {
			if _, err := tx.ExecContext(ctx, `INSERT INTO chat_messages(id,chat_id,seq,role,body,tool_name,tool_input,turn_id,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
				ids.New("cmsg"), n.ID, i+1, r.role, r.body, r.tool, r.inp, "", r.created); err != nil {
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
	c, err := s.Get(ctx, id)
	if err != nil {
		return Chat{}, err
	}
	caps, _ := s.caps(c.Adapter)
	if !takesRules(c.Adapter, caps) {
		return Chat{}, fmt.Errorf("%s does not take per-chat allow rules: its permission requests are answered by Drydock and refused", c.Adapter)
	}
	rule, err = ValidateRule(rule)
	if err != nil {
		return Chat{}, err
	}
	if contains(c.AllowedTools, rule) {
		return c, nil
	}
	if len(c.AllowedTools) >= s.o.MaxRules {
		return Chat{}, fmt.Errorf("a chat can have at most %d allow rules", s.o.MaxRules)
	}
	return s.setRules(ctx, c, append(c.AllowedTools, rule), "allowed", rule)
}

// DisallowTool removes an allow rule.
func (s *Service) DisallowTool(ctx context.Context, id, rule string) (Chat, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return Chat{}, err
	}
	rule = strings.TrimSpace(rule)
	var keep []string
	for _, r := range c.AllowedTools {
		if r != rule {
			keep = append(keep, r)
		}
	}
	if len(keep) == len(c.AllowedTools) {
		return c, nil
	}
	return s.setRules(ctx, c, keep, "removed", rule)
}

func (s *Service) setRules(ctx context.Context, c Chat, rules []string, what, rule string) (Chat, error) {
	if rules == nil {
		rules = []string{}
	}
	b, _ := json.Marshal(rules)
	c.AllowedTools, c.Updated = rules, storage.Now()
	if err := s.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE chats SET allowed_tools=?, updated_at=? WHERE id=?`, string(b), c.Updated, c.ID)
		return err
	}); err != nil {
		return Chat{}, err
	}
	if _, err := s.insertMessage(ctx, c, "", RoleSystem, fmt.Sprintf("Allow rule %s for this chat: %s", what, rule), "", nil); err != nil {
		s.logf("chats: note rule change: %v", err)
	}
	s.publish(ctx, "chat.updated", c.ProjectID, map[string]any{"chatId": c.ID, "chat": c})
	return c, nil
}

// ---- messages ----

// insertMessage redacts and stores one message, then publishes chat.message.
func (s *Service) insertMessage(ctx context.Context, c Chat, turnID, role, body, toolName string, toolInput json.RawMessage) (Message, error) {
	body = strings.ToValidUTF8(body, "�")
	if len(body) > s.o.MaxBodyBytes {
		body = cutUTF8(body, s.o.MaxBodyBytes) + "\n[cut by Drydock: the message was longer than it keeps]"
	}
	m := Message{ID: ids.New("cmsg"), ChatID: c.ID, Role: role, Body: s.d.Redact(body), ToolName: s.d.Redact(toolName), TurnID: turnID, Created: storage.Now()}
	if len(toolInput) > 0 {
		m.ToolInput = s.boundInput(toolInput)
	}
	err := s.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM chat_messages WHERE chat_id=?`, c.ID).Scan(&m.Seq); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO chat_messages(id,chat_id,seq,role,body,tool_name,tool_input,turn_id,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			m.ID, m.ChatID, m.Seq, m.Role, m.Body, m.ToolName, string(m.ToolInput), m.TurnID, m.Created); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE chats SET updated_at=? WHERE id=?`, m.Created, c.ID)
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

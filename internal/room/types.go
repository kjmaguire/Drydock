// Package room is the Engineering Room (docs/room-protocol.md): conversations, structured messages, mentions and
// markers, presence, unread and the "Needs you" inbox, turn-taking, tool members and exports. SQLite is the record;
// events on the bus are notifications of it (brief section 3). Nothing here calls an LLM (token-control rule 15).
package room

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Kind is a conversation kind (one rooms table, brief section 11).
type Kind string

const (
	KindChannel Kind = "channel"
	KindTask    Kind = "task"
	KindDM      Kind = "dm"
	KindGroup   Kind = "group"
)

// Mode is a room's turn-taking mode (room-protocol s6).
type Mode string

const (
	ModeAuto       Mode = "auto"
	ModeSequential Mode = "sequential"
	ModeModerator  Mode = "moderator"
)

// MsgType is a structured message type (brief section 11).
type MsgType string

const (
	TypeChat            MsgType = "CHAT"
	TypeTask            MsgType = "TASK"
	TypeDecision        MsgType = "DECISION"
	TypeStatus          MsgType = "STATUS"
	TypeResult          MsgType = "RESULT"
	TypeQuestion        MsgType = "QUESTION"
	TypeHandoff         MsgType = "HANDOFF"
	TypeError           MsgType = "ERROR"
	TypeApprovalRequest MsgType = "APPROVAL_REQUEST"
	TypeApproval        MsgType = "APPROVAL"
	TypeSystem          MsgType = "SYSTEM"
)

var allTypes = map[MsgType]bool{
	TypeChat: true, TypeTask: true, TypeDecision: true, TypeStatus: true, TypeResult: true, TypeQuestion: true,
	TypeHandoff: true, TypeError: true, TypeApprovalRequest: true, TypeApproval: true, TypeSystem: true,
}

// Valid reports whether t is one of the eleven message types.
func (t MsgType) Valid() bool { return allTypes[t] }

// isApprovalType reports types that only the approvals service may write (room-protocol s5).
func (t MsgType) isApprovalType() bool { return t == TypeApprovalRequest || t == TypeApproval }

// AuthorKind says who wrote a message or who a member is.
type AuthorKind string

const (
	AuthorHuman  AuthorKind = "human"
	AuthorAgent  AuthorKind = "agent"
	AuthorTool   AuthorKind = "tool"
	AuthorSystem AuthorKind = "system"
)

// Author identifies a message author or a room member. ID is the agent id, or the bare human or tool name.
type Author struct {
	Kind    AuthorKind `json:"kind"`
	ID      string     `json:"id"`
	Display string     `json:"display,omitempty"`
}

// Constructors for the four author kinds.
func Human(name string) Author { return Author{Kind: AuthorHuman, ID: name} }
func Agent(id string) Author   { return Author{Kind: AuthorAgent, ID: id} }
func Tool(name string) Author  { return Author{Kind: AuthorTool, ID: strings.ToLower(name)} }

// System is the system author.
var System = Author{Kind: AuthorSystem, ID: "system"}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// Key is the stored form: agent id, "human:<name>", "tool:<name>" or "system".
func (a Author) Key() string {
	switch a.Kind {
	case AuthorHuman:
		return "human:" + a.ID
	case AuthorTool:
		return "tool:" + a.ID
	case AuthorSystem:
		return "system"
	}
	return a.ID
}

// Validate checks the kind and that the id has no prefix that could be confused with another kind.
func (a Author) Validate() error {
	switch a.Kind {
	case AuthorSystem:
		if a.ID != "" && a.ID != "system" {
			return fmt.Errorf("%w: system author has an id", ErrInvalid)
		}
	case AuthorHuman, AuthorTool:
		if !nameRE.MatchString(a.ID) {
			return fmt.Errorf("%w: author name %q", ErrInvalid, a.ID)
		}
	case AuthorAgent:
		if a.ID == "" || strings.Contains(a.ID, ":") || a.ID == "system" {
			return fmt.Errorf("%w: agent id %q", ErrInvalid, a.ID)
		}
	default:
		return fmt.Errorf("%w: author kind %q", ErrInvalid, a.Kind)
	}
	return nil
}

// ParseAuthor is the inverse of Key.
func ParseAuthor(key string) Author {
	switch {
	case key == "system":
		return System
	case strings.HasPrefix(key, "human:"):
		return Author{Kind: AuthorHuman, ID: strings.TrimPrefix(key, "human:")}
	case strings.HasPrefix(key, "tool:"):
		return Author{Kind: AuthorTool, ID: strings.TrimPrefix(key, "tool:")}
	}
	return Author{Kind: AuthorAgent, ID: key}
}

// MemberState is a membership state (room-protocol s10).
type MemberState string

const (
	StateActive   MemberState = "active"   // eligible for turns
	StateObserver MemberState = "observer" // read access, turns only when mentioned
	StateMuted    MemberState = "muted"
)

// Trust says how a recipient must treat a message (room-protocol s2).
type Trust string

const (
	TrustTrusted   Trust = "trusted"
	TrustUntrusted Trust = "untrusted"
)

// Sentinel errors.
var (
	ErrNotFound      = errors.New("room: not found")
	ErrArchived      = errors.New("room: archived")
	ErrForbidden     = errors.New("room: forbidden")
	ErrInvalid       = errors.New("room: invalid")
	ErrNotMember     = errors.New("room: author is not a member")
	ErrConflict      = errors.New("room: version conflict")
	ErrBlocked       = errors.New("room: blocked by secret scan")
	ErrReceiptClash  = errors.New("room: receipt reused with different content")
	ErrUnknownAgent  = errors.New("room: unknown agent")
	ErrDMFull        = errors.New("room: a DM has exactly two members")
	ErrExportInvalid = errors.New("room: export does not match its hash")
)

// ModeParams tunes a room's mode (rooms.mode_params).
type ModeParams struct {
	Order       []string `json:"order,omitempty"`        // sequential: fixed speaker order (member keys)
	Moderator   string   `json:"moderator,omitempty"`    // moderator: member key
	MaxRounds   int      `json:"max_rounds,omitempty"`   // debate bound, default DefaultMaxRounds
	MaxParallel int      `json:"max_parallel,omitempty"` // grants per trigger, default 1
}

// DefaultMaxRounds is the debate bound (brief section 7, about 3 rounds).
const DefaultMaxRounds = 3

func (p ModeParams) rounds() int {
	if p.MaxRounds > 0 {
		return p.MaxRounds
	}
	return DefaultMaxRounds
}

func (p ModeParams) parallel() int {
	if p.MaxParallel > 0 {
		return p.MaxParallel
	}
	return 1
}

// Room is one conversation.
type Room struct {
	ID         string     `json:"id"`
	ProjectID  string     `json:"projectId"`
	Kind       Kind       `json:"kind"`
	Name       string     `json:"name"`
	Topic      string     `json:"topic,omitempty"`
	Mode       Mode       `json:"mode"`
	ModeParams ModeParams `json:"modeParams"`
	TaskID     string     `json:"taskId,omitempty"`
	Archived   bool       `json:"archived"`
	ArchivedAt time.Time  `json:"archivedAt,omitzero"`
	CreatedBy  string     `json:"createdBy,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	Version    int        `json:"version"`
}

// Member is a room membership row.
type Member struct {
	RoomID             string      `json:"roomId"`
	Author             Author      `json:"member"`
	Role               string      `json:"role,omitempty"`
	State              MemberState `json:"state"`
	AddedBy            string      `json:"addedBy,omitempty"`
	AddedAt            time.Time   `json:"addedAt"`
	LastReadSeq        int64       `json:"lastReadSeq"`
	LastMentionReadSeq int64       `json:"lastMentionReadSeq"`
}

// Marker is a parsed [DECISION] [TODO] [STATUS] [RESULT] line.
type Marker struct {
	Marker string `json:"marker"`
	Line   int    `json:"line"` // 1-based
	Text   string `json:"text,omitempty"`
}

// Mention is a resolved mention target (room-protocol s3). Unresolved mentions are not stored.
type Mention struct {
	Kind    string   `json:"kind"` // agent, role, human, tool, task, room, message
	ID      string   `json:"id"`
	Members []string `json:"members,omitempty"` // role mentions: the member keys the role resolved to
	Span    [2]int   `json:"span"`
}

// Refs are references from a message to other things (task, file, commit, workflow node, pack citation, ...).
type Refs struct {
	TaskID       string    `json:"task_id,omitempty"`
	RunID        string    `json:"run_id,omitempty"`
	NodeID       string    `json:"node_id,omitempty"`
	Files        []FileRef `json:"files,omitempty"`
	Commit       string    `json:"commit,omitempty"`
	PackCitation string    `json:"pack_citation,omitempty"`
	ApprovalID   string    `json:"approval_id,omitempty"`
	CheckpointID string    `json:"checkpoint_id,omitempty"`
	HandoffID    string    `json:"handoff_id,omitempty"`
	LogRef       string    `json:"log_ref,omitempty"`
	ExportID     string    `json:"export_id,omitempty"`
}

// FileRef points at a file or a line range of it.
type FileRef struct {
	Path  string `json:"path"`
	Start int    `json:"start,omitempty"`
	End   int    `json:"end,omitempty"`
}

// Message is an immutable room message (room-protocol s2).
type Message struct {
	ID           string          `json:"id"`
	RoomID       string          `json:"room_id"`
	Seq          int64           `json:"seq"`
	Type         MsgType         `json:"type"`
	Author       Author          `json:"author"`
	ThreadRootID string          `json:"thread_root_id,omitempty"`
	ReplyToID    string          `json:"reply_to_id,omitempty"`
	SupersedesID string          `json:"supersedes_id,omitempty"`
	Body         string          `json:"body"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	Refs         Refs            `json:"refs"`
	Markers      []Marker        `json:"markers,omitempty"`
	Mentions     []Mention       `json:"mentions,omitempty"`
	Trust        Trust           `json:"trust"`
	RunID        string          `json:"run_id,omitempty"`
	TaskID       string          `json:"task_id,omitempty"`
	EventSeq     uint64          `json:"event_seq,omitempty"`
	IdemKey      string          `json:"idempotency_key,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

// Attachment is a file a human attached to a message (payload field "attachments"). Text is the file's content when it is
// short UTF-8 text, after the secret scan; it is what an agent sees. The file itself stays on disk (room_attachments).
type Attachment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
	Text string `json:"text,omitempty"`
}

// Attachments returns the message's attached files.
func (m Message) Attachments() []Attachment {
	var a []Attachment
	if len(m.Payload) == 0 {
		return nil
	}
	m.PayloadField("attachments", &a)
	return a
}

// PayloadField decodes one top-level payload field into v. It reports whether the field was present.
func (m Message) PayloadField(name string, v any) bool {
	var raw map[string]json.RawMessage
	if json.Unmarshal(m.Payload, &raw) != nil {
		return false
	}
	f, ok := raw[name]
	if !ok {
		return false
	}
	return json.Unmarshal(f, v) == nil
}

// ViaSteer is the payload "via" of a person's message to a task's running agent (task.steer): it was delivered into the agent's
// turn itself and is in the channel for the history, so it starts no room conversation.
const ViaSteer = "steer"

// IsSteer reports whether the message is a person's steer of a running agent (ViaSteer).
func (m Message) IsSteer() bool {
	return m.Author.Kind == AuthorHuman && m.PayloadString("via") == ViaSteer
}

// PayloadString returns a string payload field or "".
func (m Message) PayloadString(name string) string {
	var s string
	m.PayloadField(name, &s)
	return s
}

// PayloadBool returns a bool payload field or false.
func (m Message) PayloadBool(name string) bool {
	var b bool
	m.PayloadField(name, &b)
	return b
}

// MaxBody is the body size cap in bytes (room-protocol s2, design default).
const MaxBody = 32 * 1024

// ToolInvocation is a human's request, typed from an @mention of a tool member, for the caller to act on. The room
// itself runs nothing (room-protocol s7): the caller decides whether to enqueue the deterministic check.
type ToolInvocation struct {
	Tool        string   `json:"tool"`       // Tests, Lint, ...
	MemberKey   string   `json:"memberKey"`  // tool:tests
	CheckKinds  []string `json:"checkKinds"` // check kinds the tool member owns
	RoomID      string   `json:"roomId"`
	MessageID   string   `json:"messageId"`
	TaskID      string   `json:"taskId,omitempty"`
	RequestedBy string   `json:"requestedBy"` // author key
	Args        string   `json:"args,omitempty"`
}

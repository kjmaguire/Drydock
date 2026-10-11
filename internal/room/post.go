package room

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
)

// allowedTypes is who may author what through Post (room-protocol s2). APPROVAL_REQUEST and APPROVAL are absent on
// purpose: only ApprovalWriter writes them.
var allowedTypes = map[AuthorKind]map[MsgType]bool{
	AuthorHuman:  {TypeChat: true, TypeTask: true, TypeDecision: true, TypeQuestion: true},
	AuthorAgent:  {TypeChat: true, TypeStatus: true, TypeResult: true, TypeQuestion: true, TypeHandoff: true, TypeError: true, TypeDecision: true, TypeTask: true},
	AuthorTool:   {TypeStatus: true, TypeResult: true, TypeError: true},
	AuthorSystem: {TypeTask: true, TypeDecision: true, TypeStatus: true, TypeQuestion: true, TypeHandoff: true, TypeError: true, TypeSystem: true},
}

// PostParams is one message to post.
type PostParams struct {
	RoomID         string
	Type           MsgType // default CHAT
	Author         Author
	Body           string
	Payload        any // a JSON object, or nil
	ThreadRootID   string
	ReplyToID      string
	SupersedesID   string
	Refs           Refs
	RunID          string
	TaskID         string // default: the room's task
	IdempotencyKey string // retried posts with the same key return the first message
}

// Posted is the result of Post.
type Posted struct {
	Message     Message
	Derived     []Message        // structured messages created from markers
	Hints       []Message        // SYSTEM hints, for example for unresolved mentions
	Invocations []ToolInvocation // tool-member requests typed from the mentions of a human or system author
	Unresolved  []string         // mention handles that matched no member (no wake)
	Duplicate   bool             // the idempotency key was already used
}

func trustFor(k AuthorKind) Trust {
	if k == AuthorAgent {
		return TrustUntrusted
	}
	return TrustTrusted
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if utf8.RuneCountInString(s) > max {
		r := []rune(s)
		s = string(r[:max])
	}
	return s
}

// normalizePayload validates and completes the per-type payload. Agent and system DECISION and TASK messages are always
// "proposed" (room-protocol s5).
func normalizePayload(t MsgType, a Author, body string, payload any) (json.RawMessage, error) {
	m := map[string]any{}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("%w: payload: %v", ErrInvalid, err)
		}
		if string(raw) != "null" {
			if err := json.Unmarshal(raw, &m); err != nil {
				return nil, fmt.Errorf("%w: payload must be a JSON object", ErrInvalid)
			}
		}
	}
	switch t {
	case TypeDecision:
		if s, _ := m["title"].(string); strings.TrimSpace(s) == "" {
			m["title"] = firstLine(body, 120)
		}
		if s, _ := m["decision"].(string); strings.TrimSpace(s) == "" {
			m["decision"] = body
		}
		st, _ := m["status"].(string)
		switch st {
		case "", "proposed", "accepted", "superseded":
		default:
			return nil, fmt.Errorf("%w: decision status %q", ErrInvalid, st)
		}
		if st == "" || a.Kind != AuthorHuman {
			st = "proposed"
		}
		m["status"] = st
	case TypeTask:
		if s, _ := m["title"].(string); strings.TrimSpace(s) == "" {
			m["title"] = firstLine(body, 120)
		}
		if a.Kind != AuthorHuman {
			m["state"] = "proposed"
		} else if s, _ := m["state"].(string); s == "" {
			m["state"] = "open"
		}
	case TypeQuestion:
		if _, ok := m["blocking"]; !ok {
			m["blocking"] = false
		}
	}
	return json.Marshal(m)
}

func (s *Service) prepare(p *PostParams) error {
	if p.Type == "" {
		p.Type = TypeChat
	}
	if err := p.Author.Validate(); err != nil {
		return err
	}
	if p.Author.Kind == AuthorSystem {
		p.Author = System
	}
	if !p.Type.Valid() {
		return fmt.Errorf("%w: message type %q", ErrInvalid, p.Type)
	}
	if p.Type.isApprovalType() {
		return fmt.Errorf("%w: %s is written only by the approvals service", ErrForbidden, p.Type)
	}
	if !allowedTypes[p.Author.Kind][p.Type] {
		return fmt.Errorf("%w: a %s author may not post %s", ErrForbidden, p.Author.Kind, p.Type)
	}
	if len(p.Body) > MaxBody {
		return fmt.Errorf("%w: body is %d bytes, cap %d", ErrInvalid, len(p.Body), MaxBody)
	}
	if !utf8.ValidString(p.Body) {
		return fmt.Errorf("%w: body is not valid UTF-8", ErrInvalid)
	}
	if strings.TrimSpace(p.Body) == "" && p.Payload == nil {
		return fmt.Errorf("%w: empty message", ErrInvalid)
	}
	return nil
}

const msgCols = `id,room_id,seq,type,author,thread_id,reply_to_id,supersedes_id,body,payload,refs,markers,mentions,trust,run_id,task_id,event_seq,idempotency_key,created_at`

func scanMessage(sc scanner) (Message, error) {
	var m Message
	var typ, author, trust, payload, refs, markers, mentions string
	var created int64
	if err := sc.Scan(&m.ID, &m.RoomID, &m.Seq, &typ, &author, &m.ThreadRootID, &m.ReplyToID, &m.SupersedesID, &m.Body,
		&payload, &refs, &markers, &mentions, &trust, &m.RunID, &m.TaskID, &m.EventSeq, &m.IdemKey, &created); err != nil {
		return Message{}, err
	}
	m.Type, m.Author, m.Trust = MsgType(typ), ParseAuthor(author), Trust(trust)
	m.Payload = json.RawMessage(payload)
	_ = json.Unmarshal([]byte(refs), &m.Refs)
	_ = json.Unmarshal([]byte(markers), &m.Markers)
	_ = json.Unmarshal([]byte(mentions), &m.Mentions)
	m.CreatedAt = fromMs(created)
	return m, nil
}

func getMessage(ctx context.Context, q querier, id string) (Message, error) {
	m, err := scanMessage(q.QueryRowContext(ctx, `SELECT `+msgCols+` FROM messages WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, fmt.Errorf("%w: message %s", ErrNotFound, id)
	}
	return m, err
}

// GetMessage returns one message.
func (s *Service) GetMessage(ctx context.Context, id string) (Message, error) {
	return getMessage(ctx, s.db.Reader(), id)
}

func queryMessages(ctx context.Context, q querier, where string, args ...any) ([]Message, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+msgCols+` FROM messages WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Messages returns messages with seq > afterSeq, oldest first, at most limit (0 means 200).
func (s *Service) Messages(ctx context.Context, roomID string, afterSeq int64, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 200
	}
	return queryMessages(ctx, s.db.Reader(), `room_id=? AND seq>? ORDER BY seq LIMIT ?`, roomID, afterSeq, limit)
}

// MessagesBefore returns the limit messages just before beforeSeq (the newest ones when beforeSeq is 0), oldest first: a room
// opens on its newest page and reads older pages as the person scrolls up.
func (s *Service) MessagesBefore(ctx context.Context, roomID string, beforeSeq int64, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 200
	}
	if beforeSeq <= 0 {
		beforeSeq = math.MaxInt64
	}
	ms, err := queryMessages(ctx, s.db.Reader(), `room_id=? AND seq<? ORDER BY seq DESC LIMIT ?`, roomID, beforeSeq, limit)
	if err != nil {
		return nil, err
	}
	slices.Reverse(ms)
	return ms, nil
}

// Thread returns a thread: its root and replies in order.
func (s *Service) Thread(ctx context.Context, rootID string) ([]Message, error) {
	return queryMessages(ctx, s.db.Reader(), `id=? OR thread_id=? ORDER BY seq`, rootID, rootID)
}

func insertMessageTx(ctx context.Context, tx *sql.Tx, m *Message, now int64) error {
	m.ID = ids.New("msg")
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM messages WHERE room_id=?`, m.RoomID).Scan(&m.Seq); err != nil {
		return err
	}
	m.CreatedAt = fromMs(now)
	if len(m.Payload) == 0 {
		m.Payload = json.RawMessage(`{}`)
	}
	refs, _ := json.Marshal(m.Refs)
	markers, _ := json.Marshal(nonNil(m.Markers))
	mentions, _ := json.Marshal(nonNilM(m.Mentions))
	_, err := tx.ExecContext(ctx, `INSERT INTO messages(id,room_id,seq,type,author,thread_id,reply_to_id,supersedes_id,body,payload,refs,markers,mentions,trust,run_id,task_id,idempotency_key,event_seq,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,?)`,
		m.ID, m.RoomID, m.Seq, string(m.Type), m.Author.Key(), m.ThreadRootID, m.ReplyToID, m.SupersedesID, m.Body, string(m.Payload),
		string(refs), string(markers), string(mentions), string(m.Trust), m.RunID, m.TaskID, m.IdemKey, now)
	return err
}

func nonNil(m []Marker) []Marker {
	if m == nil {
		return []Marker{}
	}
	return m
}

func nonNilM(m []Mention) []Mention {
	if m == nil {
		return []Mention{}
	}
	return m
}

// Post inserts a message, parses its mentions and markers, creates the structured messages the markers ask for, and
// publishes room.message_posted. Agent messages are stored untrusted. APPROVAL and APPROVAL_REQUEST are refused here
// for every author; text that merely looks like an approval is stored as the CHAT it is.
func (s *Service) Post(ctx context.Context, p PostParams) (Posted, error) {
	if err := s.prepare(&p); err != nil {
		return Posted{}, err
	}
	payload, err := normalizePayload(p.Type, p.Author, p.Body, p.Payload)
	if err != nil {
		return Posted{}, err
	}
	if s.opt.Redact != nil {
		red, err := s.opt.Redact(p.Body)
		if err != nil {
			return Posted{}, fmt.Errorf("%w: %v", ErrBlocked, err)
		}
		p.Body = red
	}
	var out Posted
	var evs []pendingEvent
	var room Room
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		out, evs = Posted{}, nil
		var err error
		if room, err = getRoom(ctx, tx, p.RoomID); err != nil {
			return err
		}
		if room.Archived {
			return ErrArchived
		}
		if p.IdempotencyKey != "" {
			ex, err := queryMessages(ctx, tx, `room_id=? AND idempotency_key=?`, p.RoomID, p.IdempotencyKey)
			if err != nil {
				return err
			}
			if len(ex) > 0 {
				out.Message, out.Duplicate = ex[0], true
				return nil
			}
		}
		members, err := listMembers(ctx, tx, p.RoomID)
		if err != nil {
			return err
		}
		if p.Author.Kind == AuthorAgent || p.Author.Kind == AuthorTool {
			ok := false
			for _, m := range members {
				if m.Author.Key() == p.Author.Key() {
					ok = true
				}
			}
			if !ok {
				return fmt.Errorf("%w: %s in %s", ErrNotMember, p.Author.Key(), room.Name)
			}
		}
		root, err := resolveThread(ctx, tx, p)
		if err != nil {
			return err
		}
		if p.SupersedesID != "" {
			sm, err := getMessage(ctx, tx, p.SupersedesID)
			if err != nil || sm.RoomID != p.RoomID {
				return fmt.Errorf("%w: superseded message %s", ErrNotFound, p.SupersedesID)
			}
		}
		taskID := p.TaskID
		if taskID == "" {
			taskID = room.TaskID
		}
		refs := p.Refs
		if refs.TaskID == "" {
			refs.TaskID = taskID
		}
		if refs.RunID == "" {
			refs.RunID = p.RunID
		}
		m := Message{RoomID: p.RoomID, Type: p.Type, Author: p.Author, ThreadRootID: root, ReplyToID: p.ReplyToID, SupersedesID: p.SupersedesID,
			Body: p.Body, Payload: payload, Refs: refs, Trust: trustFor(p.Author.Kind), RunID: p.RunID, TaskID: taskID, IdemKey: p.IdempotencyKey}

		// Markers and mentions are read only from human and agent prose. Tool and system bodies carry quoted output.
		prose := p.Author.Kind == AuthorHuman || p.Author.Kind == AuthorAgent
		var unresolved []string
		if prose && (p.Type == TypeChat || p.Type == TypeStatus || p.Type == TypeResult || p.Type == TypeQuestion || p.Type == TypeDecision || p.Type == TypeTask || p.Type == TypeHandoff) {
			if p.Type == TypeChat || p.Type == TypeStatus || p.Type == TypeResult {
				m.Markers = ParseMarkers(p.Body)
			}
			m.Mentions, unresolved, err = resolveMentions(ctx, tx, room, members, p.Body)
			if err != nil {
				return err
			}
		}
		now := s.nowMs()
		if err := insertMessageTx(ctx, tx, &m, now); err != nil {
			return err
		}
		out.Message = m
		scope := func(mm Message) events.Event {
			e := events.Event{ProjectID: room.ProjectID, TaskID: mm.TaskID, RunID: mm.RunID}
			if mm.Author.Kind == AuthorAgent {
				e.AgentID = mm.Author.ID
			}
			return e
		}
		evs = append(evs, postedEvent(scope(m), m))

		for _, d := range deriveFromMarkers(m) {
			if err := insertMessageTx(ctx, tx, &d, now); err != nil {
				return err
			}
			out.Derived = append(out.Derived, d)
			evs = append(evs, postedEvent(scope(d), d),
				pendingEvent{typ: "room.marker_promoted", scope: scope(d), data: map[string]any{"room": room.ID, "message": d.ID, "source": m.ID, "marker": d.PayloadString("from_marker")}})
		}
		if len(unresolved) > 0 {
			out.Unresolved = unresolved
			h := Message{RoomID: p.RoomID, Type: TypeSystem, Author: System, Trust: TrustTrusted, TaskID: taskID,
				Body:    "No member of this room matches " + strings.Join(prefixed(unresolved, "@"), ", ") + ". Nobody was woken. A human can add the member.",
				Payload: mustJSON(map[string]any{"event": "mention_unresolved", "params": map[string]any{"message_id": m.ID, "handles": unresolved, "known_agents": knownAgents(ctx, tx, room.ProjectID, unresolved)}})}
			h.ReplyToID = m.ID
			if err := insertMessageTx(ctx, tx, &h, now); err != nil {
				return err
			}
			out.Hints = append(out.Hints, h)
			evs = append(evs, postedEvent(events.Event{ProjectID: room.ProjectID, TaskID: taskID}, h))
		}
		if prose {
			out.Invocations = invocations(room, m)
		}
		return nil
	})
	if err != nil {
		return Posted{}, err
	}
	if out.Duplicate {
		return out, nil
	}
	s.flush(ctx, evs)
	if s.bus != nil {
		if m, err := s.GetMessage(ctx, out.Message.ID); err == nil {
			out.Message = m
		}
	}
	return out, nil
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func prefixed(in []string, p string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = p + s
	}
	return out
}

func postedEvent(scope events.Event, m Message) pendingEvent {
	return pendingEvent{typ: "room.message_posted", scope: scope, msgID: m.ID,
		data: map[string]any{"room": m.RoomID, "message": m.ID, "seq": m.Seq, "type": m.Type, "author": m.Author.Key()}}
}

// resolveThread derives the thread root. Threads are one level deep.
func resolveThread(ctx context.Context, tx *sql.Tx, p PostParams) (string, error) {
	root := ""
	if p.ReplyToID != "" {
		rm, err := getMessage(ctx, tx, p.ReplyToID)
		if err != nil || rm.RoomID != p.RoomID {
			return "", fmt.Errorf("%w: reply target %s", ErrNotFound, p.ReplyToID)
		}
		root = rm.ThreadRootID
		if root == "" {
			root = rm.ID
		}
	}
	if p.ThreadRootID != "" {
		rm, err := getMessage(ctx, tx, p.ThreadRootID)
		if err != nil || rm.RoomID != p.RoomID {
			return "", fmt.Errorf("%w: thread root %s", ErrNotFound, p.ThreadRootID)
		}
		if rm.ThreadRootID != "" {
			return "", fmt.Errorf("%w: %s is a reply; threads are one level deep", ErrInvalid, p.ThreadRootID)
		}
		if root != "" && root != p.ThreadRootID {
			return "", fmt.Errorf("%w: reply target is in another thread", ErrInvalid)
		}
		root = p.ThreadRootID
	}
	return root, nil
}

// deriveFromMarkers builds the proposals the markers ask for (room-protocol s3). A marker whose derived type the
// author may not post stays a display hint only.
func deriveFromMarkers(m Message) []Message {
	var out []Message
	for _, mk := range m.Markers {
		if mk.Text == "" {
			continue
		}
		var t MsgType
		payload := map[string]any{"source_message_id": m.ID, "from_marker": mk.Marker, "line": mk.Line}
		switch mk.Marker {
		case "DECISION":
			t = TypeDecision
			payload["title"], payload["decision"], payload["status"] = firstLine(mk.Text, 120), mk.Text, "proposed"
		case "TODO":
			t = TypeTask
			payload["title"], payload["state"], payload["todo"] = firstLine(mk.Text, 120), "proposed", true
		case "STATUS":
			t = TypeStatus
			payload["summary"] = mk.Text
		case "RESULT":
			t = TypeResult
			payload["summary"] = mk.Text
		}
		if !allowedTypes[m.Author.Kind][t] {
			continue
		}
		out = append(out, Message{RoomID: m.RoomID, Type: t, Author: m.Author, ThreadRootID: m.ThreadRootID, ReplyToID: m.ID, Body: mk.Text,
			Payload: mustJSON(payload), Refs: m.Refs, Trust: m.Trust, RunID: m.RunID, TaskID: m.TaskID})
	}
	return out
}

// resolveMentions resolves @handle, @role:Name, #room-name and ^msg_id against the room. Unresolved @ mentions are
// returned for a SYSTEM hint; unresolved # and ^ references are plain text.
func resolveMentions(ctx context.Context, tx *sql.Tx, room Room, members []Member, body string) ([]Mention, []string, error) {
	var out []Mention
	var unresolved []string
	for _, rm := range ParseMentions(body) {
		switch rm.Sigil {
		case "@":
			h := handleOf(rm.Name)
			var hit []Member
			var exact []Member
			for _, m := range members {
				if m.Author.ID == rm.Name || m.Author.Key() == rm.Name {
					exact = append(exact, m)
				}
				if handleOf(m.Author.ID) == h || handleOf(m.Author.Display) == h {
					hit = append(hit, m)
				}
			}
			if len(exact) == 1 {
				hit = exact
			}
			if len(hit) != 1 {
				unresolved = append(unresolved, rm.Name)
				continue
			}
			out = append(out, Mention{Kind: string(hit[0].Author.Kind), ID: hit[0].Author.Key(), Span: rm.Span})
		case "@role:":
			var keys []string
			for _, m := range members {
				if strings.EqualFold(m.Role, rm.Name) {
					keys = append(keys, m.Author.Key())
				}
			}
			if len(keys) == 0 {
				unresolved = append(unresolved, "role:"+rm.Name)
				continue
			}
			out = append(out, Mention{Kind: "role", ID: rm.Name, Members: keys, Span: rm.Span})
		case "#":
			var kind, taskID, id string
			err := tx.QueryRowContext(ctx, `SELECT id,kind,task_id FROM rooms WHERE project_id=? AND name=? AND archived=0 AND kind IN ('channel','task')`, room.ProjectID, rm.Name).Scan(&id, &kind, &taskID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, nil, err
			}
			if kind == "task" && taskID != "" {
				out = append(out, Mention{Kind: "task", ID: taskID, Span: rm.Span})
			} else {
				out = append(out, Mention{Kind: "room", ID: id, Span: rm.Span})
			}
		case "^":
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE id=? AND room_id=?`, rm.Name, room.ID).Scan(&n); err != nil {
				return nil, nil, err
			}
			if n > 0 {
				out = append(out, Mention{Kind: "message", ID: rm.Name, Span: rm.Span})
			}
		}
	}
	return out, unresolved, nil
}

func knownAgents(ctx context.Context, tx *sql.Tx, projectID string, handles []string) []string {
	var out []string
	rows, err := tx.QueryContext(ctx, `SELECT id,name FROM agents WHERE project_id=? AND archived_at=0`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if rows.Scan(&id, &name) != nil {
			continue
		}
		for _, h := range handles {
			if handleOf(h) == handleOf(name) || h == id {
				out = append(out, id)
			}
		}
	}
	return out
}

// invocations types the tool-member mentions of a human or system author. An agent mentioning a tool member runs
// nothing (room-protocol s7: only a human runs a tool member).
func invocations(room Room, m Message) []ToolInvocation {
	if m.Author.Kind != AuthorHuman {
		return nil
	}
	var out []ToolInvocation
	for _, mn := range m.Mentions {
		if mn.Kind != string(AuthorTool) {
			continue
		}
		tm, ok := ToolByName(ParseAuthor(mn.ID).ID)
		if !ok {
			continue
		}
		out = append(out, ToolInvocation{Tool: tm.Name, MemberKey: mn.ID, CheckKinds: append([]string(nil), tm.CheckKinds...), RoomID: room.ID,
			MessageID: m.ID, TaskID: m.TaskID, RequestedBy: m.Author.Key(), Args: lineAfter(m.Body, mn.Span[1])})
	}
	return out
}

// Reconcile publishes room.message_posted for messages committed without a recorded bus sequence (a crash between the
// commit and the publish, or a Service built without a bus). Delivery is at least once: consumers dedupe by message id.
func (s *Service) Reconcile(ctx context.Context) (int, error) {
	if s.bus == nil {
		return 0, errors.New("room: no bus")
	}
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT m.id,m.room_id,m.seq,m.type,m.author,m.task_id,m.run_id,r.project_id FROM messages m JOIN rooms r ON r.id=m.room_id WHERE m.event_seq=0 AND m.seq>0 ORDER BY m.created_at, m.seq`)
	if err != nil {
		return 0, err
	}
	var evs []pendingEvent
	for rows.Next() {
		var id, roomID, typ, author, task, run, project string
		var seq int64
		if err := rows.Scan(&id, &roomID, &seq, &typ, &author, &task, &run, &project); err != nil {
			rows.Close()
			return 0, err
		}
		e := events.Event{ProjectID: project, TaskID: task, RunID: run}
		if a := ParseAuthor(author); a.Kind == AuthorAgent {
			e.AgentID = a.ID
		}
		evs = append(evs, pendingEvent{typ: "room.message_posted", scope: e, msgID: id, data: map[string]any{"room": roomID, "message": id, "seq": seq, "type": typ, "author": author}})
	}
	rows.Close()
	s.flush(ctx, evs)
	return len(evs), nil
}

// ApprovalWriter writes the two system-only message types. It is handed to the approvals service; the UI service layer
// calls Decide there, and the approvals service then calls this. It checks the approvals table first, so a message can
// only record an approval that exists and a decision that was really made by a human.
type ApprovalWriter struct{ s *Service }

// ApprovalWriter returns the writer. Nothing else in the room package can write APPROVAL messages.
func (s *Service) ApprovalWriter() ApprovalWriter { return ApprovalWriter{s: s} }

type approvalRow struct {
	kind, title, hash, status, decision, decidedBy string
}

func (s *Service) loadApproval(ctx context.Context, id string) (approvalRow, error) {
	var a approvalRow
	err := s.db.Reader().QueryRowContext(ctx, `SELECT kind,title,action_hash,status,decision,decided_by FROM approvals WHERE id=?`, id).
		Scan(&a.kind, &a.title, &a.hash, &a.status, &a.decision, &a.decidedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return a, fmt.Errorf("%w: approval %s", ErrNotFound, id)
	}
	return a, err
}

// Requested posts APPROVAL_REQUEST for an approvals row in REQUESTED. The text shown is the row's canonical title, never
// agent prose.
func (w ApprovalWriter) Requested(ctx context.Context, roomID, approvalID string, expires int64) (Message, error) {
	a, err := w.s.loadApproval(ctx, approvalID)
	if err != nil {
		return Message{}, err
	}
	if a.status != "REQUESTED" {
		return Message{}, fmt.Errorf("%w: approval %s is %s", ErrInvalid, approvalID, a.status)
	}
	return w.s.postApproval(ctx, roomID, TypeApprovalRequest, "Approval requested: "+a.title, approvalID, map[string]any{
		"approval_id": approvalID, "kind": a.kind, "action_hash": a.hash, "canonical_action_summary": a.title, "expires_at": expires}, "approval:"+approvalID+":requested")
}

// Decided posts APPROVAL for a decided approvals row. by must be a human and must be the row's decider.
func (w ApprovalWriter) Decided(ctx context.Context, roomID, approvalID string, by Author) (Message, error) {
	if by.Kind != AuthorHuman || by.Validate() != nil {
		return Message{}, fmt.Errorf("%w: only a human decision is recorded as APPROVAL", ErrForbidden)
	}
	a, err := w.s.loadApproval(ctx, approvalID)
	if err != nil {
		return Message{}, err
	}
	if a.status == "REQUESTED" {
		return Message{}, fmt.Errorf("%w: approval %s is not decided", ErrInvalid, approvalID)
	}
	if a.decidedBy != by.Key() && a.decidedBy != by.ID {
		return Message{}, fmt.Errorf("%w: approval %s was decided by %q, not %q", ErrForbidden, approvalID, a.decidedBy, by.Key())
	}
	dec := a.decision
	if dec == "" {
		dec = strings.ToLower(a.status)
	}
	return w.s.postApproval(ctx, roomID, TypeApproval, "Decision recorded: "+dec+" by "+by.Key(), approvalID, map[string]any{
		"approval_id": approvalID, "decision": dec, "decided_by": by.Key(), "action_hash": a.hash}, "approval:"+approvalID+":decided")
}

func (s *Service) postApproval(ctx context.Context, roomID string, t MsgType, body, approvalID string, payload map[string]any, idem string) (Message, error) {
	raw, _ := json.Marshal(payload)
	var out Posted
	var evs []pendingEvent
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		out, evs = Posted{}, nil
		room, err := getRoom(ctx, tx, roomID)
		if err != nil {
			return err
		}
		if ex, err := queryMessages(ctx, tx, `room_id=? AND idempotency_key=?`, roomID, idem); err != nil {
			return err
		} else if len(ex) > 0 {
			out.Message, out.Duplicate = ex[0], true
			return nil
		}
		m := Message{RoomID: roomID, Type: t, Author: System, Body: body, Payload: raw, Trust: TrustTrusted, TaskID: room.TaskID, IdemKey: idem, Refs: Refs{TaskID: room.TaskID, ApprovalID: approvalID}}
		if err := insertMessageTx(ctx, tx, &m, s.nowMs()); err != nil {
			return err
		}
		out.Message = m
		evs = append(evs, postedEvent(events.Event{ProjectID: room.ProjectID, TaskID: room.TaskID}, m))
		return nil
	})
	if err != nil {
		return Message{}, err
	}
	if !out.Duplicate {
		s.flush(ctx, evs)
	}
	return out.Message, nil
}

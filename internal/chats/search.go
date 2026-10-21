package chats

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Hit is one chat.search result: a message (Seq > 0) or a chat title (Seq = 0).
type Hit struct {
	ChatID   string `json:"chatId"`
	Title    string `json:"title"`
	Seq      int64  `json:"seq"`
	Role     string `json:"role,omitempty"`
	Snippet  string `json:"snippet"`
	Archived bool   `json:"archived"`
}

// Search finds chats by title and by what was said in them (user and assistant messages), best matches first. projectID
// limits it to one project's chats ("" searches every chat). Archived chats are included and marked.
func (s *Service) Search(ctx context.Context, query, projectID string, limit int) ([]Hit, error) {
	return s.search(ctx, query, func(c Chat) bool { return projectID == "" || c.ProjectID == projectID }, limit)
}

func (s *Service) search(ctx context.Context, query string, keep func(Chat) bool, limit int) ([]Hit, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	match := ftsMatch(query)
	if match == "" {
		return []Hit{}, nil
	}
	out := []Hit{}
	chats := map[string]Chat{}
	get := func(id string) (Chat, bool) {
		if c, ok := chats[id]; ok {
			return c, true
		}
		c, err := s.Get(ctx, id)
		if err != nil {
			return c, false
		}
		chats[id] = c
		return c, true
	}
	rows, err := s.d.DB.Reader().QueryContext(ctx, `SELECT c.id FROM chats_fts f JOIN chats c ON c.rowid=f.rowid WHERE chats_fts MATCH ? ORDER BY bm25(chats_fts) LIMIT ?`, match, limit*4)
	if err != nil {
		return nil, err
	}
	var titleIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		titleIDs = append(titleIDs, id)
	}
	rows.Close()
	for _, id := range titleIDs {
		if c, ok := get(id); ok && keep(c) && len(out) < limit {
			out = append(out, Hit{ChatID: c.ID, Title: c.Title, Snippet: c.Title, Archived: c.Archived})
		}
	}
	rows, err = s.d.DB.Reader().QueryContext(ctx, `SELECT m.chat_id, m.seq, m.role, snippet(chat_messages_fts, 0, '[', ']', '…', 16)
		FROM chat_messages_fts f JOIN chat_messages m ON m.rowid=f.rowid WHERE chat_messages_fts MATCH ? ORDER BY bm25(chat_messages_fts) LIMIT ?`, match, limit*4)
	if err != nil {
		return nil, err
	}
	type mh struct {
		chat, role, snip string
		seq              int64
	}
	var hits []mh
	for rows.Next() {
		var h mh
		if err := rows.Scan(&h.chat, &h.seq, &h.role, &h.snip); err != nil {
			rows.Close()
			return nil, err
		}
		hits = append(hits, h)
	}
	rows.Close()
	for _, h := range hits {
		if len(out) >= limit {
			break
		}
		if c, ok := get(h.chat); ok && keep(c) {
			out = append(out, Hit{ChatID: c.ID, Title: c.Title, Seq: h.seq, Role: h.role, Snippet: oneLine(h.snip), Archived: c.Archived})
		}
	}
	return out, nil
}

// ---- read-only access for the chats workspace tool ----

// ToolChat is a chat as the chats tool lists it.
type ToolChat struct {
	ID        string `json:"chat_id"`
	Title     string `json:"title"`
	ProjectID string `json:"project_id,omitempty"`
	Adapter   string `json:"adapter"`
	Messages  int    `json:"messages"`
	Started   string `json:"started"`
	Updated   string `json:"updated"`
	Preview   string `json:"preview,omitempty"`
}

// ToolMessage is a message as the chats tool returns it.
type ToolMessage struct {
	Seq  int64  `json:"seq"`
	Role string `json:"role"`
	Tool string `json:"tool,omitempty"`
	Time string `json:"time"`
	Body string `json:"body"`
}

// ToolTranscript is the chats tool's read result.
type ToolTranscript struct {
	ChatID   string        `json:"chat_id"`
	Title    string        `json:"title"`
	Messages []ToolMessage `json:"messages"`
	// NextSeq continues the transcript (from_seq) when more messages follow; 0 when this page reaches the end.
	NextSeq int64 `json:"next_seq,omitempty"`
}

// caller returns the chat a tool call comes from, refusing when it may not read history.
func (s *Service) caller(ctx context.Context, callerID string) (Chat, error) {
	c, err := s.Get(ctx, callerID)
	if err != nil {
		return c, errors.New("the chats tool is only available inside a chat")
	}
	if !c.ReadHistory {
		return c, errors.New("reading earlier chats is switched off for this chat")
	}
	return c, nil
}

// visible reports whether a chat may read another: a chat in a project sees that project's chats and the chats without a
// project; a chat without a project sees every chat.
func visible(caller, target Chat) bool {
	return caller.ProjectID == "" || target.ProjectID == "" || target.ProjectID == caller.ProjectID
}

// ToolList lists the chats the calling chat may read, most recent first (archived ones are left out).
func (s *Service) ToolList(ctx context.Context, callerID string, limit int) ([]ToolChat, error) {
	me, err := s.caller(ctx, callerID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	all, err := s.List(ctx, ListReq{})
	if err != nil {
		return nil, err
	}
	out := []ToolChat{}
	for _, it := range all {
		if !visible(me, it.Chat) {
			continue
		}
		out = append(out, ToolChat{ID: it.ID, Title: it.Title, ProjectID: it.ProjectID, Adapter: it.Adapter, Messages: it.MessageCount,
			Started: stamp(it.Created), Updated: stamp(it.Updated), Preview: clip(it.Preview, 120)})
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// ToolSearch searches the chats the calling chat may read.
func (s *Service) ToolSearch(ctx context.Context, callerID, query string, k int) ([]Hit, error) {
	me, err := s.caller(ctx, callerID)
	if err != nil {
		return nil, err
	}
	if k <= 0 || k > 20 {
		k = 10
	}
	return s.search(ctx, query, func(c Chat) bool { return visible(me, c) }, k)
}

// ToolRead returns part of a readable chat's transcript, from fromSeq (0 = the start), at most limit messages.
func (s *Service) ToolRead(ctx context.Context, callerID, chatID string, fromSeq int64, limit int) (ToolTranscript, error) {
	me, err := s.caller(ctx, callerID)
	if err != nil {
		return ToolTranscript{}, err
	}
	target, err := s.Get(ctx, strings.TrimSpace(chatID))
	if err != nil || !visible(me, target) {
		return ToolTranscript{}, fmt.Errorf("no readable chat %q", chatID)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.d.DB.Reader().QueryContext(ctx, `SELECT `+msgCols+` FROM chat_messages
		WHERE chat_id=? AND seq>=? ORDER BY seq LIMIT ?`, target.ID, fromSeq, limit+1)
	if err != nil {
		return ToolTranscript{}, err
	}
	defer rows.Close()
	tr := ToolTranscript{ChatID: target.ID, Title: target.Title, Messages: []ToolMessage{}}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return ToolTranscript{}, err
		}
		if len(tr.Messages) == limit {
			tr.NextSeq = m.Seq
			break
		}
		tr.Messages = append(tr.Messages, ToolMessage{Seq: m.Seq, Role: m.Role, Tool: m.ToolName, Time: stamp(m.Created), Body: clip(m.Body, 4000)})
	}
	return tr, rows.Err()
}

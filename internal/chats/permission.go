package chats

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Permission requests in a chat: a CLI that asks over its own protocol (ACP session/request_permission) to use a tool call
// its rules do not allow is answered by the person in the chat. The request is stored as a note ("Asks to use ...") and listed
// as waiting (Asks, the chat.permission event), shown with Allow once and Refuse, and the CLI waits until the person answers, the request expires, or the turn ends. Nothing answers it on
// the person's behalf, and an answer allows exactly that one call (agents.PermissionAsker).

// AskTTL is how long a chat's permission request waits for the person by default.
const AskTTL = 30 * time.Minute

// Ask outcomes, as in the chat.permission_answered event.
const (
	AskAllowed   = "allowed"
	AskRefused   = "refused"
	AskExpired   = "expired"
	AskWithdrawn = "withdrawn" // the turn ended (stopped, timed out, or the app closed) before anyone answered
)

// PendingAsk is a permission request waiting for the person.
type PendingAsk struct {
	ID        string          `json:"id"`
	ChatID    string          `json:"chatId"`
	ChatTitle string          `json:"chatTitle,omitempty"`
	ProjectID string          `json:"projectId,omitempty"`
	TurnID    string          `json:"turnId"`
	MessageID string          `json:"messageId"`
	Tool      string          `json:"tool"`
	Summary   string          `json:"summary"`
	Input     json.RawMessage `json:"input,omitempty"`
	Created   int64           `json:"created"`
	ExpiresAt int64           `json:"expiresAt"`
}

type pendingAsk struct {
	info   PendingAsk
	answer chan bool
}

// ErrNoAsk is returned for an answer to a request that is not waiting (answered, expired, or never asked in that chat).
var ErrNoAsk = errors.New("this request is no longer waiting for an answer")

// asker is the turn's agents.PermissionAsker.
func (t *turnRun) asker() agents.PermissionAsker {
	return func(ctx context.Context, q agents.ToolAsk) (bool, string) {
		s, c := t.s, t.c
		name := firstNonEmpty(q.Tool, "tool")
		summary := s.d.Redact(ToolSummary(name, q.Args))
		if q.Title != "" && q.Title != name {
			summary = s.d.Redact(name + ": " + clip(oneLine(q.Title), 160))
		}
		msg, _ := t.store(RoleSystem, "Asks to use "+summary, name, q.Args)
		ttl := s.o.AskTTL
		now := storage.Now()
		p := &pendingAsk{answer: make(chan bool, 1), info: PendingAsk{ID: ids.New("cask"), ChatID: c.ID, ChatTitle: c.Title, ProjectID: c.ProjectID, TurnID: t.turnID, MessageID: msg.ID,
			Tool: msg.ToolName, Summary: summary, Input: msg.ToolInput, Created: now, ExpiresAt: now + ttl.Milliseconds()}}
		s.mu.Lock()
		s.asks[p.info.ID] = p
		s.mu.Unlock()
		s.publish(context.Background(), "chat.permission", c.ProjectID, map[string]any{"chatId": c.ID, "ask": p.info})

		timer := time.NewTimer(ttl)
		defer timer.Stop()
		var allow bool
		outcome := AskWithdrawn
		select {
		case allow = <-p.answer:
			outcome = AskRefused
			if allow {
				outcome = AskAllowed
			}
		case <-timer.C:
			outcome = AskExpired
		case <-ctx.Done():
		}
		s.mu.Lock()
		delete(s.asks, p.info.ID)
		s.mu.Unlock()
		s.publish(context.Background(), "chat.permission_answered", c.ProjectID, map[string]any{"chatId": c.ID, "id": p.info.ID, "outcome": outcome})
		switch outcome {
		case AskAllowed:
			t.store(RoleSystem, "Allowed once by you: "+summary, "", nil)
			return true, ""
		case AskRefused:
			t.store(RoleDenied, "Refused by you: "+summary, name, q.Args)
			return false, "The person refused this call. Continue without it, or stop and say what you needed."
		case AskExpired:
			t.store(RoleDenied, "Nobody answered within "+ttl.Round(time.Minute).String()+", so it was refused: "+summary, name, q.Args)
			return false, "Nobody answered, so the call was refused. Continue without it, or stop and say what you needed."
		}
		return false, ""
	}
}

// Asks lists the permission requests waiting in a chat, oldest first.
func (s *Service) Asks(ctx context.Context, chatID string) ([]PendingAsk, error) {
	if _, err := s.Get(ctx, chatID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []PendingAsk{}
	for _, p := range s.asks {
		if p.info.ChatID == chatID {
			out = append(out, p.info)
		}
	}
	sortAsks(out)
	return out, nil
}

// Waiting lists the waiting permission requests of a project's chats, oldest first; "" lists every chat's, as Needs you
// does for "". Needs you shows them.
func (s *Service) Waiting(projectID string) []PendingAsk {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []PendingAsk{}
	for _, p := range s.asks {
		if projectID == "" || p.info.ProjectID == projectID {
			out = append(out, p.info)
		}
	}
	sortAsks(out)
	return out
}

func sortAsks(out []PendingAsk) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Created != out[j].Created {
			return out[i].Created < out[j].Created
		}
		return out[i].ID < out[j].ID
	})
}

// Answer answers a waiting permission request of a chat: allow lets exactly that call run once.
func (s *Service) Answer(_ context.Context, chatID, askID string, allow bool) error {
	s.mu.Lock()
	p, ok := s.asks[askID]
	if ok && p.info.ChatID != chatID {
		ok = false
	}
	if ok {
		delete(s.asks, askID) // one answer only
	}
	s.mu.Unlock()
	if !ok {
		return ErrNoAsk
	}
	p.answer <- allow
	return nil
}

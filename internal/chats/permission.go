package chats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
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
	// Rule is the allow rule "Allow <tool> in this chat" would record for the call (AllowAndRemember); empty when the chat's
	// agent takes no per-chat allow rules, or no rule can name the call exactly. The UI offers the choice only when it is set.
	Rule      string `json:"rule,omitempty"`
	Created   int64  `json:"created"`
	ExpiresAt int64  `json:"expiresAt"`
}

type pendingAsk struct {
	info   PendingAsk
	answer chan askAnswer
}

// askAnswer is the person's answer: allow or refuse, and the allow rule recorded with an allow that is remembered for the chat.
type askAnswer struct {
	allow bool
	rule  string
}

// ErrNoAsk is returned for an answer to a request that is not waiting (answered, expired, or never asked in that chat).
var ErrNoAsk = errors.New("this request is no longer waiting for an answer")

// asker is the turn's agents.PermissionAsker.
func (t *turnRun) asker() agents.PermissionAsker {
	return func(ctx context.Context, q agents.ToolAsk) (bool, string) {
		s, c := t.s, t.c
		name := firstNonEmpty(q.Tool, "tool")
		// redacted before it is shortened: a secret the shortening cuts in two would not be found after (audit 2026-10-27)
		var summary string
		if q.Title != "" && q.Title != name {
			summary = s.d.Redact(name + ": " + clip(s.d.Redact(oneLine(cutUTF8(q.Title, summaryScanBytes))), 160))
		} else {
			summary = RedactedToolSummary(name, q.Args, s.d.Redact)
		}
		msg, _ := t.store(RoleSystem, "Asks to use "+summary, name, q.Args)
		ttl := s.o.AskTTL
		now := storage.Now()
		rule, _ := s.rememberRule(c.Adapter, msg.ToolName, msg.ToolInput)
		p := &pendingAsk{answer: make(chan askAnswer, 1), info: PendingAsk{ID: ids.New("cask"), ChatID: c.ID, ChatTitle: c.Title, ProjectID: c.ProjectID, TurnID: t.turnID, MessageID: msg.ID,
			Tool: msg.ToolName, Summary: summary, Input: msg.ToolInput, Rule: rule, Created: now, ExpiresAt: now + ttl.Milliseconds()}}
		s.mu.Lock()
		s.asks[p.info.ID] = p
		s.mu.Unlock()
		s.publish(context.Background(), "chat.permission", c.ProjectID, map[string]any{"chatId": c.ID, "ask": p.info})

		timer := time.NewTimer(ttl)
		defer timer.Stop()
		var ans askAnswer
		outcome := AskWithdrawn
		select {
		case ans = <-p.answer:
			outcome = AskRefused
			if ans.allow {
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
			if ans.rule != "" {
				t.store(RoleSystem, "Allowed by you, and from the next message on in this chat ("+ans.rule+"): "+summary, "", nil)
			} else {
				t.store(RoleSystem, "Allowed once by you: "+summary, "", nil)
			}
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
	return s.answer(chatID, askID, askAnswer{allow: allow})
}

func (s *Service) answer(chatID, askID string, a askAnswer) error {
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
	p.answer <- a
	return nil
}

// rememberRule is the allow rule that remembering a call of tool with input records for a chat of adapter, or why none can:
// the adapter takes no per-chat allow rules, the input was shortened or redacted, or no safe rule names the call.
func (s *Service) rememberRule(adapter, tool string, input json.RawMessage) (string, error) {
	if caps, _ := s.caps(adapter); !takesRules(adapter, caps) {
		return "", fmt.Errorf("%s does not take per-chat allow rules, so Drydock cannot remember this for the chat: allow it once instead", adapter)
	}
	in := string(input)
	if strings.Contains(in, "[REDACTED") || strings.Contains(in, `"truncated"`) {
		return "", errors.New("the call's input was shortened or hides a secret, so no rule can name it exactly: allow it once instead")
	}
	rule := SuggestRule(tool, input)
	if rule == "" {
		return "", fmt.Errorf("no safe rule names this %s call for the chat: allow it once instead", firstNonEmpty(tool, "tool"))
	}
	return rule, nil
}

// waitingAsk is a request still waiting in a chat, without taking it.
func (s *Service) waitingAsk(chatID, askID string) (PendingAsk, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.asks[askID]
	if !ok || p.info.ChatID != chatID {
		return PendingAsk{}, false
	}
	return p.info, true
}

// AllowAndRemember answers a waiting request with allow and records the allow rule for its tool call in the chat, as
// AllowTool does, so the chat's later turns may make the same call without asking ("Allow <tool> in this chat"). Only an
// adapter that takes per-chat allow rules (Claude Code) can remember; for any other the request is left waiting, to be
// answered once. A call whose input was shortened or redacted, or that no safe rule names, is refused the same way. The rule
// is recorded before the call is allowed; when the request is gone meanwhile (answered, expired, or its turn ended) a rule
// the chat did not have before is taken back. It returns the rule.
func (s *Service) AllowAndRemember(ctx context.Context, chatID, askID string) (string, error) {
	ask, ok := s.waitingAsk(chatID, askID)
	if !ok {
		return "", ErrNoAsk
	}
	c, err := s.Get(ctx, chatID)
	if err != nil {
		return "", err
	}
	rule, err := s.rememberRule(c.Adapter, ask.Tool, ask.Input)
	if err != nil {
		return "", err
	}
	had := contains(c.AllowedTools, rule)
	if _, err := s.AllowTool(ctx, chatID, rule); err != nil {
		return "", err
	}
	if err := s.answer(chatID, askID, askAnswer{allow: true, rule: rule}); err != nil {
		if !had {
			if _, derr := s.DisallowTool(ctx, chatID, rule); derr != nil {
				s.logf("chats: taking back the rule %s: %v", rule, derr)
			}
		}
		return "", err
	}
	return rule, nil
}

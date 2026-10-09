package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// memChats is a labelled stand-in for the chats service (internal/chats): it records what the tool asked for.
type memChats struct{ got []string }

func (m *memChats) List(_ context.Context, run RunInfo, limit int) (any, error) {
	m.got = append(m.got, fmt.Sprintf("list %s %d", run.AgentID, limit))
	return []map[string]any{{"chat_id": "chat_1", "title": "Earlier"}}, nil
}

func (m *memChats) Search(_ context.Context, run RunInfo, q string, k int) (any, error) {
	m.got = append(m.got, fmt.Sprintf("search %s %q %d", run.AgentID, q, k))
	return []map[string]any{{"chatId": "chat_1", "seq": 3, "snippet": "the [cache] fix"}}, nil
}

func (m *memChats) Read(_ context.Context, run RunInfo, id string, from, limit int) (any, error) {
	m.got = append(m.got, fmt.Sprintf("read %s %s %d %d", run.AgentID, id, from, limit))
	if id != "chat_1" {
		return nil, errors.New("no readable chat")
	}
	return map[string]any{"chat_id": id, "messages": []map[string]any{{"seq": 1, "role": "user", "body": "hi"}}}, nil
}

func TestChatsToolReadsThroughItsProvider(t *testing.T) {
	_, cs, _, _ := wsEnv(t, Workspace{}, nil)
	if r := callTool(t, cs, "chats", map[string]any{"action": "list"}); !r.IsError || !strings.Contains(textOf(r), "not available") {
		t.Fatalf("without a provider: %s", textOf(r))
	}
	mc := &memChats{}
	_, cs, _, _ = wsEnv(t, Workspace{Chats: mc}, nil)
	if r := callTool(t, cs, "chats", map[string]any{"action": "list"}); r.IsError || !strings.Contains(textOf(r), `"chat_1"`) {
		t.Fatalf("list: %s", textOf(r))
	}
	if r := callTool(t, cs, "chats", map[string]any{"action": "search", "query": "cache", "k": 3}); r.IsError || !strings.Contains(textOf(r), "[cache]") {
		t.Fatalf("search: %s", textOf(r))
	}
	if r := callTool(t, cs, "chats", map[string]any{"action": "read", "chat_id": "chat_1", "from_seq": 2, "limit": 5}); r.IsError || !strings.Contains(textOf(r), `"hi"`) {
		t.Fatalf("read: %s", textOf(r))
	}
	for _, c := range []struct{ args, want string }{
		{`{"action":"search"}`, "needs a query"},
		{`{"action":"read"}`, "needs a chat_id"},
		{`{"action":"read","chat_id":"chat_x"}`, "no readable chat"},
		{`{"action":"post"}`, "invalid arguments"},
	} {
		var a map[string]any
		_ = json.Unmarshal([]byte(c.args), &a)
		if r := callTool(t, cs, "chats", a); !r.IsError || !strings.Contains(textOf(r), c.want) {
			t.Errorf("%s: %s", c.args, textOf(r))
		}
	}
	want := []string{"list ag 30", `search ag "cache" 3`, "read ag chat_1 2 5", "read ag chat_x 0 50"}
	if strings.Join(mc.got, "|") != strings.Join(want, "|") {
		t.Fatalf("provider calls %q", mc.got)
	}
}

// The tool is listed only to a run whose policy allows it: the application allows it to chat sessions with history reading on.
func TestChatsToolIsOnlyListedWhenThePolicyAllowsIt(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Workspace = Workspace{Chats: &memChats{}} })
	ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-team", ProjectID: "p", AgentID: "agt_1"}, Policy: allow(map[string][]string{WorkspaceServerID: {"read_range", "skills"}})})
	cs := e.connect(ep)
	for _, n := range toolNames(t, cs) {
		if n == "chats" {
			t.Fatal("a run whose policy does not name chats must not see it")
		}
	}
	if r := callTool(t, cs, "chats", map[string]any{"action": "list"}); !r.IsError {
		t.Fatalf("calling a tool outside the policy must fail: %s", textOf(r))
	}
	ep2 := e.startRun(RunConfig{Info: RunInfo{RunID: "r-chat", AgentID: "chat_1"}, Policy: allow(map[string][]string{WorkspaceServerID: {"chats"}})})
	if got := strings.Join(toolNames(t, e.connect(ep2)), ","); got != "chats,fetch_range" {
		t.Fatalf("a chat run sees %s", got)
	}
}

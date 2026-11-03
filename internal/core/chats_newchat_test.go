package core_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/chats"
)

// D8: a chat that answered while the person was elsewhere lists as unread until chat.markRead moves its cursor; chat.unread
// counts it for the Chats link.
func TestChatUnreadUntilMarkedRead(t *testing.T) {
	e := openChats(t, fakeagent.Script{Default: []fakeagent.Step{{Say: "an answer"}}}, nil)
	p, _ := e.setup("unread")
	c := e.create(map[string]any{"projectId": p.ID})
	listItem := func() chats.ListItem {
		var list []chats.ListItem
		e.call("chat.list", map[string]any{"projectId": p.ID}, &list)
		for _, it := range list {
			if it.ID == c.ID {
				return it
			}
		}
		t.Fatalf("chat %s not listed", c.ID)
		return chats.ListItem{}
	}
	if it := listItem(); it.Unread != 0 || it.LastReadSeq != 0 {
		t.Fatalf("a new chat has nothing unread: %+v", it)
	}
	e.send(c.ID, "hello")
	it := listItem()
	if it.Unread != 1 {
		t.Fatalf("the answer is unread: %+v", it)
	}
	var sum chats.UnreadSummary
	e.call("chat.unread", map[string]any{}, &sum)
	if sum.Chats != 1 || sum.Messages != 1 {
		t.Fatalf("unread summary %+v", sum)
	}
	d := e.get(c.ID)
	last := d.Messages[len(d.Messages)-1].Seq
	var mark chats.ReadMark
	e.call("chat.markRead", map[string]any{"id": c.ID, "seq": last + 50}, &mark) // clamped to the newest message
	if mark.LastReadSeq != last {
		t.Fatalf("cursor %d, want %d", mark.LastReadSeq, last)
	}
	e.call("chat.markRead", map[string]any{"id": c.ID, "seq": 1}, &mark) // never back
	if mark.LastReadSeq != last {
		t.Fatalf("the cursor moved back: %d", mark.LastReadSeq)
	}
	if it := listItem(); it.Unread != 0 {
		t.Fatalf("read: %+v", it)
	}
	e.call("chat.unread", map[string]any{}, &sum)
	if sum.Chats != 0 {
		t.Fatalf("unread summary after reading %+v", sum)
	}
	if len(e.eventsOf("chat.read")) != 1 {
		t.Fatalf("chat.read once, got %d", len(e.eventsOf("chat.read")))
	}
	// a fork starts read
	var f chats.Chat
	e.call("chat.fork", map[string]any{"id": c.ID}, &f)
	var list []chats.ListItem
	e.call("chat.list", map[string]any{"projectId": p.ID}, &list)
	for _, x := range list {
		if x.ID == f.ID && x.Unread != 0 {
			t.Fatalf("a fork starts read: %+v", x)
		}
	}
}

// D2: a new chat starts with the model, effort and mode the person last chose with chat.update for the same scope and CLI; a
// mode that skips approvals is never carried.
func TestNewChatStartsWithTheLastChoice(t *testing.T) {
	e := openChats(t, fakeagent.Script{Default: []fakeagent.Step{{Say: "ok"}}}, nil)
	p, _ := e.setup("last")
	first := e.create(map[string]any{"projectId": p.ID})
	if first.Model != "" || len(first.FromLast) != 0 {
		t.Fatalf("nothing chosen yet: %+v", first)
	}
	var upd chats.Chat
	e.call("chat.update", map[string]any{"id": first.ID, "model": "sonnet", "effort": "high", "permissionMode": "acceptEdits"}, &upd)
	second := e.create(map[string]any{"projectId": p.ID})
	if second.Model != "sonnet" || second.Effort != "high" || second.PermissionMode != "acceptEdits" {
		t.Fatalf("the last choice carried: %+v", second)
	}
	if strings.Join(second.FromLast, ",") != "model,effort,permissionMode" {
		t.Fatalf("fromLast %v", second.FromLast)
	}
	// what the request names wins
	third := e.create(map[string]any{"projectId": p.ID, "model": "opus"})
	if third.Model != "opus" || strings.Join(third.FromLast, ",") != "effort,permissionMode" {
		t.Fatalf("explicit model: %+v", third)
	}
	// another scope (no project) has its own
	other := e.create(map[string]any{})
	if other.Model != "" || len(other.FromLast) != 0 {
		t.Fatalf("a chat without a project has no choice yet: %+v", other)
	}
	// a mode that skips approvals is never written: the earlier one stays
	e.call("chat.update", map[string]any{"id": first.ID, "permissionMode": "bypassPermissions", "confirmSkipsApprovals": true}, &upd)
	fourth := e.create(map[string]any{"projectId": p.ID})
	if fourth.PermissionMode != "acceptEdits" || fourth.SkipsApprovals {
		t.Fatalf("a skip mode must not carry: %+v", fourth)
	}
	var lc chats.LastChoice
	e.call("chat.lastChoice", map[string]any{"projectId": p.ID, "adapter": "claude"}, &lc)
	if lc.Model != "sonnet" || lc.Effort != "high" || lc.PermissionMode != "acceptEdits" {
		t.Fatalf("lastChoice %+v", lc)
	}
}

// D9: allow rules set for every new chat are checked like a chat's own, copied into each new chat (listed, removable, noted)
// and leave the chats that exist alone.
func TestDefaultAllowRulesReachNewChats(t *testing.T) {
	e := openChats(t, fakeagent.Script{Default: []fakeagent.Step{{Say: "ok"}}}, nil)
	p, _ := e.setup("rules")
	before := e.create(map[string]any{"projectId": p.ID})
	if err := e.callErr("chat.setDefaultRule", map[string]any{"tool": "Bash", "allow": true}); err == nil {
		t.Fatal("a bare Bash must be refused for every chat too")
	}
	var res struct {
		Rules []string `json:"rules"`
	}
	e.call("chat.setDefaultRule", map[string]any{"tool": "Bash(npm test)", "allow": true}, &res)
	if strings.Join(res.Rules, ",") != "Bash(npm test)" {
		t.Fatalf("rules %v", res.Rules)
	}
	c := e.create(map[string]any{"projectId": p.ID})
	if strings.Join(c.AllowedTools, ",") != "Bash(npm test)" {
		t.Fatalf("new chat rules %v", c.AllowedTools)
	}
	d := e.get(c.ID)
	if len(d.Messages) != 1 || d.Messages[0].Role != chats.RoleSystem || !strings.Contains(d.Messages[0].Body, "Allowed in every new chat: Bash(npm test)") {
		t.Fatalf("the rules are noted: %+v", d.Messages)
	}
	if got := e.get(before.ID).Chat; len(got.AllowedTools) != 0 {
		t.Fatalf("an existing chat keeps its own rules: %v", got.AllowedTools)
	}
	var after chats.Chat
	e.call("chat.disallowTool", map[string]any{"id": c.ID, "tool": "Bash(npm test)"}, &after)
	if len(after.AllowedTools) != 0 {
		t.Fatalf("removable: %v", after.AllowedTools)
	}
	e.call("chat.setDefaultRule", map[string]any{"tool": "Bash(npm test)", "allow": false}, &res)
	e.call("chat.defaultRules", map[string]any{}, &res)
	if len(res.Rules) != 0 {
		t.Fatalf("removed: %v", res.Rules)
	}
	if c2 := e.create(map[string]any{"projectId": p.ID}); len(c2.AllowedTools) != 0 {
		t.Fatalf("no rules after removal: %v", c2.AllowedTools)
	}
}

package core_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/chats"
)

// askingAdapter is the scripted ACP agent (fakeacp, a LABELLED TEST DOUBLE) that asks to run `go test ./...` under the tool
// name Bash, then answers "done". Registered under the id "claude" it stands in for a Claude Code that asks in the chat: the
// adapter id is what decides that a chat takes per-chat allow rules (chats.takesRules).
func askingAdapter(t *testing.T, id string) agents.Adapter {
	t.Helper()
	sc, _ := json.Marshal(fakeacp.Script{LoadSession: true, Default: []fakeacp.Step{
		{Perm: &fakeacp.Perm{Tool: fakeacp.Tool{ID: "t1", Title: "Bash", Kind: "execute", Input: map[string]any{"command": "go test ./..."}}}},
		{Say: "done"},
	}})
	sp := filepath.Join(t.TempDir(), "acp.json")
	if err := os.WriteFile(sp, sc, 0o644); err != nil {
		t.Fatal(err)
	}
	return acp.New(acp.Config{ID: id, Command: os.Args[0], Env: []string{"DRYDOCK_FAKE_ACP=1", "DRYDOCK_FAKE_ACP_SCRIPT=" + sp}})
}

// "Allow Bash in this chat" (decision P97): chat.answer with remember allows the waiting call and records the allow rule for
// it, as chat.allowTool does, so the chat's next turns may run it without asking. Remember goes with allow only.
func TestAllowInThisChatAllowsTheCallAndRemembersTheRule(t *testing.T) {
	e := openChats(t, fakeagent.Script{}, map[string]agents.Adapter{"claude": askingAdapter(t, "claude")})
	p, _ := e.setup("remember")
	c := e.create(map[string]any{"projectId": p.ID, "adapter": "claude"})
	turn := e.start(c.ID, "run the tests")
	ask := e.waitAsk(c.ID)
	// the request says which rule remembering it records, so the UI offers "Allow <tool> in this chat" only then
	var waiting []chats.PendingAsk
	e.call("chat.asks", map[string]string{"id": c.ID}, &waiting)
	if len(waiting) != 1 || waiting[0].Rule != "Bash(go test ./...)" {
		t.Fatalf("%+v", waiting)
	}
	if err := e.callErr("chat.answer", map[string]any{"id": c.ID, "askId": ask.ID, "allow": false, "remember": true}); err == nil || !strings.Contains(err.Error(), "only an allow") {
		t.Fatal(err)
	}
	var res struct {
		Answered bool   `json:"answered"`
		Rule     string `json:"rule"`
	}
	e.call("chat.answer", map[string]any{"id": c.ID, "askId": ask.ID, "allow": true, "remember": true}, &res)
	if !res.Answered || res.Rule != "Bash(go test ./...)" {
		t.Fatalf("%+v", res)
	}
	if ev := e.waitTurn(turn); ev["status"] != chats.TurnDone {
		t.Fatalf("turn %v", ev)
	}
	d := e.get(c.ID)
	if strings.Join(d.Chat.AllowedTools, ",") != "Bash(go test ./...)" {
		t.Fatalf("rules %v", d.Chat.AllowedTools)
	}
	all := transcriptText(d.Messages)
	if !strings.Contains(all, "permission: selected allow") || !strings.Contains(all, "Allowed for this chat: Bash(go test ./...)") || !strings.Contains(all, "from the next message on in this chat") {
		t.Fatalf("%s", all)
	}
	if err := e.callErr("chat.answer", map[string]any{"id": c.ID, "askId": ask.ID, "allow": true, "remember": true}); err == nil {
		t.Fatal("a request is answered once")
	}
}

// An agent that does not take per-chat allow rules (an ACP agent) cannot remember: the answer is refused with the reason and
// the request keeps waiting, so the person can still allow it once.
func TestAllowInThisChatIsRefusedForAnAgentWithoutAllowRules(t *testing.T) {
	e := openChats(t, fakeagent.Script{}, map[string]agents.Adapter{"acpchat": askingAdapter(t, "acpchat")})
	p, _ := e.setup("noremember")
	c := e.create(map[string]any{"projectId": p.ID, "adapter": "acpchat"})
	turn := e.start(c.ID, "run the tests")
	ask := e.waitAsk(c.ID)
	if err := e.callErr("chat.answer", map[string]any{"id": c.ID, "askId": ask.ID, "allow": true, "remember": true}); err == nil || !strings.Contains(err.Error(), "does not take per-chat allow rules") || !strings.Contains(err.Error(), "allow it once") {
		t.Fatal(err)
	}
	var asks []chats.PendingAsk
	e.call("chat.asks", map[string]string{"id": c.ID}, &asks)
	if len(asks) != 1 || asks[0].ID != ask.ID {
		t.Fatalf("the request must still wait: %+v", asks)
	}
	if asks[0].Rule != "" {
		t.Fatalf("no rule is offered for an agent without allow rules: %+v", asks[0])
	}
	e.call("chat.answer", map[string]any{"id": c.ID, "askId": ask.ID, "allow": true}, nil)
	if ev := e.waitTurn(turn); ev["status"] != chats.TurnDone {
		t.Fatalf("turn %v", ev)
	}
	if d := e.get(c.ID); len(d.Chat.AllowedTools) != 0 {
		t.Fatalf("rules %v", d.Chat.AllowedTools)
	}
}

// Moving a chat to a project (decision P97): only while it is idle; it then lists under the project and runs in the
// project's folder. A CLI session's folder cannot change, so the first turn after a move that changed the folder is a new
// session given the conversation so far; taken out of the project it keeps the folder, and so its session.
func TestAChatMovesToAProjectWhileIdleAndRunsInTheProjectsFolder(t *testing.T) {
	e := openChats(t, fakeagent.Script{
		Rules:   []fakeagent.Rule{{Match: "slow", Steps: []fakeagent.Step{{SleepMS: 1500}, {Say: "slow done"}}}},
		Default: []fakeagent.Step{{Say: "ok"}},
	}, nil)
	p, repo := e.setup("moveto")
	other, _ := e.setup("other")
	c := e.create(map[string]any{})
	if c.ProjectID != "" || c.Cwd == repo {
		t.Fatalf("%+v", c)
	}
	e.send(c.ID, "first")
	if d := e.get(c.ID); d.Chat.SessionID == "" {
		t.Fatalf("%+v", d.Chat)
	}
	if err := e.callErr("chat.update", map[string]any{"id": c.ID, "projectId": "prj_nothere"}); err == nil {
		t.Fatal("an unknown project")
	}
	// not while a turn runs
	turn := e.start(c.ID, "slow")
	if err := e.callErr("chat.update", map[string]any{"id": c.ID, "projectId": p.ID}); err == nil || !strings.Contains(err.Error(), "stop the running turn before moving the chat") {
		t.Fatal(err)
	}
	e.waitTurn(turn)

	var moved chats.Chat
	e.call("chat.update", map[string]any{"id": c.ID, "projectId": p.ID}, &moved)
	if moved.ProjectID != p.ID || moved.Cwd != repo || moved.SessionID != "" {
		t.Fatalf("%+v", moved)
	}
	var list []chats.ListItem
	e.call("chat.list", map[string]any{"projectId": p.ID}, &list)
	if len(list) != 1 || list[0].ID != c.ID {
		t.Fatalf("%+v", list)
	}
	d := e.get(c.ID)
	if last := d.Messages[len(d.Messages)-1]; last.Role != chats.RoleSystem || !strings.HasPrefix(last.Body, "Now in a project. Now runs with ") || !strings.Contains(last.Body, repo) || !strings.Contains(last.Body, "new session") {
		t.Fatalf("%+v", last)
	}
	ev := e.send(c.ID, "second")
	if ev["status"] != chats.TurnDone || ev["resumed"] == true {
		t.Fatalf("the turn after the move starts a new session: %v", ev)
	}
	av := e.argv()
	if _, ok := flagOf(av[len(av)-1], "--resume"); ok {
		t.Fatalf("%v", av[len(av)-1])
	}
	if pr := e.prompts(); !strings.Contains(pr[len(pr)-1], "first") {
		t.Fatalf("the new session is given the conversation so far: %q", pr[len(pr)-1])
	}
	// to another project: the chat.updated event reaches both projects
	e.call("chat.update", map[string]any{"id": c.ID, "projectId": other.ID}, &moved)
	if moved.ProjectID != other.ID {
		t.Fatalf("%+v", moved)
	}
	var seenOld, seenNew bool
	rows, _ := e.c.DB.Reader().Query(`SELECT project_id, data FROM events WHERE type='chat.updated' ORDER BY seq DESC LIMIT 2`)
	for rows.Next() {
		var pid, data string
		rows.Scan(&pid, &data)
		seenOld = seenOld || (pid == p.ID && strings.Contains(data, other.ID))
		seenNew = seenNew || pid == other.ID
	}
	rows.Close()
	if !seenOld || !seenNew {
		t.Fatalf("chat.updated: old %v new %v", seenOld, seenNew)
	}
	e.send(c.ID, "third")
	session := e.get(c.ID).Chat.SessionID

	// out of the project: the same folder, the same session
	e.call("chat.update", map[string]any{"id": c.ID, "projectId": ""}, &moved)
	if moved.ProjectID != "" || moved.Cwd != e.get(c.ID).Chat.Cwd || moved.SessionID != session || session == "" {
		t.Fatalf("%+v (session %s)", moved, session)
	}
	d = e.get(c.ID)
	if last := d.Messages[len(d.Messages)-1]; !strings.HasPrefix(last.Body, "No longer in a project. It still runs in ") || !strings.Contains(last.Body, "session continues") {
		t.Fatalf("%+v", last)
	}
	e.send(c.ID, "fourth")
	av = e.argv()
	if r, ok := flagOf(av[len(av)-1], "--resume"); !ok || r != session {
		t.Fatalf("the session continues: %v", av[len(av)-1])
	}
	// a project chat takes no folder of its own
	if err := e.callErr("chat.update", map[string]any{"id": c.ID, "projectId": p.ID, "cwd": t.TempDir()}); err == nil || !strings.Contains(err.Error(), "runs in the project's folder") {
		t.Fatal(err)
	}
}

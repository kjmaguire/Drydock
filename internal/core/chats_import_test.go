package core_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/chats"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/wsl"
)

// The import of Claude Code's own chats (decision P109) through the methods the UI calls: chat.importList finds the sessions
// in Claude Code's transcripts (CLAUDE_CONFIG_DIR here, a temporary folder with fixture transcripts), chat.import makes them
// chats, and an imported chat's next turn continues the session (--resume, the scripted real-process agent as Claude Code).

func ccLine(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func ccSession(t *testing.T, path, cwd string, at time.Time, user, answer string) {
	t.Helper()
	ts := func(d time.Duration) string { return at.Add(d).UTC().Format(time.RFC3339Nano) }
	lines := []string{
		ccLine(map[string]any{"type": "user", "cwd": cwd, "timestamp": ts(0), "uuid": "u1", "message": map[string]any{"role": "user", "content": user}}),
		`{"type":"assistant","message":`, // cut off mid-line, as a crash would leave it
		ccLine(map[string]any{"type": "assistant", "cwd": cwd, "timestamp": ts(time.Minute), "uuid": "u2",
			"message": map[string]any{"id": "msg_1", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": answer}}}}),
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestChatImportFromClaudeCode(t *testing.T) {
	const sessA, sessB = "1f1f1f1f-aaaa-4aaa-8aaa-000000000001", "2e2e2e2e-bbbb-4bbb-8bbb-000000000002"
	e := openChats(t, fakeagent.Script{Default: []fakeagent.Step{{Say: "Picked up where we left off."}}}, nil)
	p, repo := e.setup("importproj")
	config, other := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	secret := "ghp_" + strings.Repeat("aB3dE5fG7h", 3) + "aB3dE5"
	at := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	ccSession(t, filepath.Join(config, "projects", "-repo", sessA+".jsonl"), repo, at, "Why does the build fail? token "+secret, "A missing import.")
	ccSession(t, filepath.Join(config, "projects", "-other", sessB+".jsonl"), other, at.Add(time.Hour), "List the notes here", "Two notes.")
	// Claude Code's sign-in beside the projects folder: never read (D1), and a file the import would fail on if it tried
	cred := filepath.Join(config, ".credentials.json")
	if err := os.WriteFile(cred, []byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-x"}}`), 0o000); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(cred)

	var list chats.ImportList
	e.call("chat.importList", map[string]any{}, &list)
	if len(list.Sessions) != 2 || list.Sessions[0].SessionID != sessB || list.Sessions[1].SessionID != sessA {
		t.Fatalf("the list: %+v", list)
	}
	if a := list.Sessions[1]; a.ProjectID != p.ID || a.Cwd != repo || a.Messages != 2 || strings.Contains(a.Title, "ghp_") || !strings.HasPrefix(a.Title, "Why does the build fail?") {
		t.Fatalf("session A: %+v", a)
	}
	if len(list.Sources) == 0 || list.Sources[0].Folder != filepath.Join(config, "projects") || list.Sources[0].WSLDistro != "" {
		t.Fatalf("the sources: %+v", list.Sources)
	}

	var res chats.ImportResult
	e.call("chat.import", map[string]any{"sessions": []map[string]string{{"sessionId": sessA}, {"sessionId": sessB}}}, &res)
	if len(res.Imported) != 2 || len(res.Skipped) != 0 {
		t.Fatalf("the import: %+v", res)
	}
	a, b := res.Imported[0].Chat, res.Imported[1].Chat
	if a.ProjectID != p.ID || a.Cwd != repo || a.SessionID != sessA || a.ImportedFrom != chats.ImportSourceClaudeCode || a.Created != at.UnixMilli() {
		t.Fatalf("chat A: %+v", a)
	}
	if b.ProjectID != "" || b.Cwd != other || b.SessionID != sessB {
		t.Fatalf("chat B: %+v", b)
	}
	created := 0
	for _, ev := range e.eventsOf("chat.created") {
		if ev["chatId"] == a.ID || ev["chatId"] == b.ID {
			created++
		}
	}
	if created != 2 {
		t.Fatalf("%d chat.created events for the two imports", created)
	}
	if body := e.get(a.ID).Messages[0].Body; strings.Contains(body, "ghp_") || !strings.HasPrefix(body, "Why does the build fail?") {
		t.Fatalf("the first message: %q", body)
	}

	// the imported chat continues the Claude Code session, in its folder
	ev := e.send(a.ID, "Fix it, please.")
	if ev["status"] != chats.TurnDone || ev["resumed"] != true || ev["sessionId"] != sessA {
		t.Fatalf("the turn: %v", ev)
	}
	av := e.argv()
	if r, _ := flagOf(av[len(av)-1], "--resume"); r != sessA {
		t.Fatalf("the turn did not resume the session: %v", av[len(av)-1])
	}
	if got := e.get(a.ID); got.Chat.Cwd != repo || roles(got.Messages) != "user,assistant,system,user,assistant" {
		t.Fatalf("after the turn: cwd %s, %s", got.Chat.Cwd, roles(got.Messages))
	}

	// importing again does nothing; the list says where each session went
	var again chats.ImportResult
	e.call("chat.import", map[string]any{"sessions": []map[string]string{{"sessionId": sessA}, {"sessionId": sessB}}}, &again)
	if len(again.Imported) != 0 || len(again.Skipped) != 2 || again.Skipped[0].ChatID != a.ID {
		t.Fatalf("the second import: %+v", again)
	}
	e.call("chat.importList", map[string]any{}, &list)
	if len(list.Sessions) != 2 || list.Sessions[1].ChatID != a.ID || list.Sessions[0].ChatID != b.ID {
		t.Fatalf("the list after the import: %+v", list.Sessions)
	}
	var all []chats.ListItem
	e.call("chat.list", map[string]any{}, &all)
	if len(all) != 2 {
		t.Fatalf("%d chats", len(all))
	}
	if after, _ := os.Stat(cred); !after.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() {
		t.Fatal("Claude Code's sign-in file was touched")
	}
}

// A session of Claude Code inside a WSL distro (the stand-in wsl.exe and the LABELLED stand-in claude of internal/wsl/wsltest)
// is found in the distro user's ~/.claude, imported as a chat that runs in that distro in the session's folder, and its next
// turn starts inside the distro with --resume.
func TestChatImportFromClaudeCodeInWSL(t *testing.T) {
	const sess = "3d3d3d3d-cccc-4ccc-8ccc-000000000003"
	a, f := openWSLTTL(t, time.Minute)
	core.UseHostPathsForWSLFiles(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir()) // nothing on the Windows side
	e := &chatEnv{app: a}
	work := filepath.Join(f.Home, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	ccSession(t, filepath.Join(f.Home, ".claude", "projects", "-home-work", sess+".jsonl"), work, time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC), "Hello from the distro", "Hi.")

	var list chats.ImportList
	e.call("chat.importList", map[string]any{}, &list)
	if len(list.Sessions) != 1 || list.Sessions[0].WSLDistro != "Ubuntu" || list.Sessions[0].Cwd != wsl.UNC("Ubuntu", work) {
		t.Fatalf("the list: %+v", list)
	}
	var res chats.ImportResult
	e.call("chat.import", map[string]any{"sessions": []map[string]string{{"sessionId": sess, "wslDistro": "Ubuntu"}}}, &res)
	if len(res.Imported) != 1 {
		t.Fatalf("the import: %+v", res)
	}
	c := res.Imported[0].Chat
	if c.WSLDistro != "Ubuntu" || c.Cwd != wsl.UNC("Ubuntu", work) || c.SessionID != sess || c.ProjectID != "" {
		t.Fatalf("the chat: %+v", c)
	}
	if ev := e.send(c.ID, "go on"); ev["status"] != chats.TurnDone {
		t.Fatalf("the turn: %v", ev)
	}
	resumed := false
	for _, argv := range f.Calls() {
		s := strings.Join(argv, " ")
		resumed = resumed || strings.Contains(s, "-d Ubuntu ") && strings.Contains(s, "--cd "+work+" ") && strings.Contains(s, "--resume "+sess)
	}
	if !resumed {
		t.Fatal("the turn did not resume the session inside the distro, in its folder")
	}
}

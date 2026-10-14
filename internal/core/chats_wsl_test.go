package core_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/chats"
	"github.com/kjmaguire/drydock/internal/wsl"
)

// lastAnswer is the newest assistant message of a chat.
func (e *chatEnv) lastAnswer(id string) string {
	e.t.Helper()
	ms := e.get(id).Messages
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].Role == chats.RoleAssistant {
			return ms[i].Body
		}
	}
	e.t.Fatalf("no answer in the chat: %s", roles(ms))
	return ""
}

func wantAll(t *testing.T, what, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%s lacks %q: %s", what, w, got)
		}
	}
}

// A chat runs Claude Code inside a WSL distro the way an agent does (decision P41): the person picks the distro's install when
// creating the chat, it is stored on the chat, and every turn starts the CLI through the adapter's own WSL start (the
// stand-in wsl.exe and the LABELLED stand-in claude of internal/wsl/wsltest): the run marker, the MCP gateway through the
// Windows shim, the distro's own HOME. A chat without a project runs in a private folder in the distro user's home, made
// again when it is gone; a picked folder inside the distro must exist there. A chat whose distro is gone, or whose CLI there
// is signed out, is refused with a message naming the distro.
func TestAChatRunsClaudeCodeInsideAWSLDistro(t *testing.T) {
	a, f := openWSLTTL(t, time.Millisecond) // every check probes again, as after the cache's five minutes
	e := &chatEnv{app: a}

	// refused before anything is stored
	if err := e.callErr("chat.create", map[string]any{"adapter": "claude", "wslDistro": "Nope"}); err == nil || !strings.Contains(err.Error(), "Nope") {
		t.Fatalf("an unknown distro: %v", err)
	}
	if err := e.callErr("chat.create", map[string]any{"adapter": "codex", "wslDistro": "Ubuntu"}); err == nil || !strings.Contains(err.Error(), "cannot run in WSL") {
		t.Fatalf("codex in WSL: %v", err)
	}
	if err := e.callErr("chat.create", map[string]any{"adapter": "claude", "wslDistro": "docker-desktop"}); err == nil {
		t.Fatal("a container engine's own distro was accepted")
	}

	// no project, no folder: a private folder in the distro user's home, named the Windows way
	c := e.create(map[string]any{"adapter": "claude", "wslDistro": "ubuntu"})
	home := filepath.Join(f.Home, ".drydock", "chats", c.ID)
	if c.WSLDistro != "Ubuntu" || c.Cwd != wsl.UNC("Ubuntu", home) {
		t.Fatalf("the chat: distro %q cwd %q (want %q)", c.WSLDistro, c.Cwd, wsl.UNC("Ubuntu", home))
	}
	if st, err := os.Stat(home); err != nil || !st.IsDir() {
		t.Fatalf("the private folder was not made inside the distro: %v", err)
	}
	ev := e.send(c.ID, "hello from a chat")
	if ev["status"] != chats.TurnDone {
		t.Fatalf("the turn: %v", ev)
	}
	wantAll(t, "the answer", e.lastAnswer(c.ID), "standin turn=1", "cwd="+home+" ", "home="+f.Home+" ", "run=set", "mcp=yes", "mcpcmdok=yes")
	if got := e.get(c.ID).Chat; got.WSLDistro != "Ubuntu" || got.SessionID != "wsl-standin-session" {
		t.Fatalf("after the turn: %+v", got)
	}
	launched := false
	for _, argv := range f.Calls() {
		s := strings.Join(argv, " ")
		launched = launched || strings.Contains(s, "--cd "+home+" ") && strings.Contains(s, "-d Ubuntu ") && strings.Contains(s, wsl.RunEnv+"=")
	}
	if !launched {
		t.Fatal("no turn was started inside the distro, in the chat's folder, with the run marker")
	}

	// the private folder is made again when it is gone; the next turn continues the CLI's session
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	if ev := e.send(c.ID, "and again"); ev["status"] != chats.TurnDone || ev["resumed"] != true {
		t.Fatalf("the second turn: %v", ev)
	}
	wantAll(t, "the second answer", e.lastAnswer(c.ID), "cwd="+home+" ")

	// a fork keeps the distro
	var fork chats.Chat
	e.call("chat.fork", map[string]any{"id": c.ID}, &fork)
	if fork.WSLDistro != "Ubuntu" || fork.Cwd != c.Cwd {
		t.Fatalf("the fork: %+v", fork)
	}
	if ev := e.send(fork.ID, "forked"); ev["status"] != chats.TurnDone {
		t.Fatalf("the fork's turn: %v", ev)
	}

	// a folder the person picked inside the distro: its Linux path, kept the Windows way; one that is not there is refused
	picked := t.TempDir()
	pc := e.create(map[string]any{"adapter": "claude", "wslDistro": "Ubuntu", "cwd": picked})
	if pc.Cwd != wsl.UNC("Ubuntu", picked) {
		t.Fatalf("the picked folder: %q", pc.Cwd)
	}
	e.send(pc.ID, "in my folder")
	wantAll(t, "the picked folder's answer", e.lastAnswer(pc.ID), "cwd="+picked+" ")
	if err := e.callErr("chat.create", map[string]any{"adapter": "claude", "wslDistro": "Ubuntu", "cwd": filepath.Join(picked, "missing")}); err == nil ||
		!strings.Contains(err.Error(), "does not exist in the WSL distro Ubuntu") {
		t.Fatalf("a missing folder: %v", err)
	}
	if err := e.callErr("chat.create", map[string]any{"adapter": "claude", "wslDistro": "Ubuntu", "cwd": `\\wsl.localhost\Debian\home`}); err == nil ||
		!strings.Contains(err.Error(), "inside the WSL distro Debian") {
		t.Fatalf("another distro's folder: %v", err)
	}

	// a project's chat in the distro runs in the project's repository
	p, repo := a.setup("wsl-chat")
	pr := e.create(map[string]any{"projectId": p.ID, "adapter": "claude", "wslDistro": "Ubuntu"})
	if pr.Cwd != repo || pr.WSLDistro != "Ubuntu" {
		t.Fatalf("the project's chat: %+v", pr)
	}
	e.send(pr.ID, "in the project")
	wantAll(t, "the project chat's answer", e.lastAnswer(pr.ID), "cwd="+repo+" ", "run=set")

	// signed out inside the distro: refused, naming the distro; signed in again, it works
	out := filepath.Join(f.Home, ".drydock-standin-signed-out")
	if err := os.WriteFile(out, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.callErr("chat.send", map[string]any{"id": c.ID, "text": "signed out?"}); err == nil ||
		!strings.Contains(err.Error(), "Claude Code in the WSL distro Ubuntu is not signed in") {
		t.Fatalf("a signed-out distro: %v", err)
	}
	if err := e.callErr("chat.create", map[string]any{"adapter": "claude", "wslDistro": "Ubuntu"}); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("a new chat in a signed-out distro: %v", err)
	}
	if err := os.Remove(out); err != nil {
		t.Fatal(err)
	}
	if ev := e.send(c.ID, "signed in again"); ev["status"] != chats.TurnDone {
		t.Fatalf("after signing in again: %v", ev)
	}
	if n := len(e.get(c.ID).Messages); n == 0 {
		t.Fatal("no messages")
	}

	// the distro is gone (unregistered): the chat is refused with a message naming it, and nothing is claimed
	script, err := os.ReadFile(f.Exe)
	if err != nil {
		t.Fatal(err)
	}
	gone := strings.Replace(string(script), "'Ubuntu,docker-desktop'", "'docker-desktop'", 1)
	if gone == string(script) {
		t.Fatal("the stand-in wsl.exe's distro list was not found")
	}
	if err := os.WriteFile(f.Exe, []byte(gone), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := e.callErr("chat.send", map[string]any{"id": c.ID, "text": "still there?"}); err == nil || !strings.Contains(err.Error(), "no WSL distro named Ubuntu") {
		t.Fatalf("a distro that is gone: %v", err)
	}
	if got := e.get(c.ID).Chat; got.Status != chats.StatusIdle {
		t.Fatalf("a refused send claimed the chat: %+v", got)
	}
}

package core_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/chats"
	"github.com/kjmaguire/drydock/internal/contract"
	"github.com/kjmaguire/drydock/internal/core"
)

// A chat against the real Claude Code (decisions P29, P3: skipped with the reason when no logged-in claude is here): the second
// turn continues the CLI's own session (--resume) and remembers the first; a session id the CLI does not have falls back to a
// fresh session given the transcript, which still remembers; a shell command outside the default mode is refused and shown.
// About four haiku turns at low effort.
func TestRealClaudeChatResumesFallsBackAndShowsRefusals(t *testing.T) {
	if why := contract.RealClaudeSkip(); why != "" {
		t.Skip(why)
	}
	t.Setenv("DRYDOCK_FAKE_AGENT", "")
	// a Claude Code session that runs this test passes its own session id down; the chat must get ids of its own
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	os.Unsetenv("CLAUDE_CODE_SESSION_ID")
	det, err := claude.New().Detect(context.Background())
	if err != nil || det.Status != agents.StatusReady {
		t.Skipf("real claude not usable here: status=%s problems=%v err=%v", det.Status, det.Problems, err)
	}
	dir := t.TempDir()
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "data"), User: "kyle", LeaseTTL: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	e := &chatEnv{app: &app{t: t, c: c}}
	p, repo := e.setup("realchat")
	ch := e.create(map[string]any{"projectId": p.ID, "model": "haiku", "effort": "low", "readHistory": false})

	last := func() chats.Message {
		d := e.get(ch.ID)
		for i := len(d.Messages) - 1; i >= 0; i-- {
			if d.Messages[i].Role == chats.RoleAssistant {
				return d.Messages[i]
			}
		}
		t.Fatalf("no assistant message: %+v", d.Messages)
		return chats.Message{}
	}
	ev := e.send(ch.ID, "Remember this codeword for later: HERON-42. Reply with just OK.")
	if ev["status"] != chats.TurnDone {
		t.Fatalf("first turn %v", ev)
	}
	first := e.get(ch.ID).Chat
	if first.SessionID == "" || first.CostUSD == nil || *first.CostUSD <= 0 || first.Title == "" {
		t.Fatalf("after the first turn %+v", first)
	}
	ev = e.send(ch.ID, "What codeword did I give you? Reply with only the codeword.")
	if ev["status"] != chats.TurnDone || ev["resumed"] != true || ev["sessionId"] != first.SessionID {
		t.Fatalf("second turn %v (first session %s)", ev, first.SessionID)
	}
	if got := last().Body; !strings.Contains(got, "HERON-42") {
		t.Fatalf("the resumed session must remember: %q", got)
	}

	// the CLI no longer has the session: a fresh one is given the transcript
	if _, err := c.DB.Writer().Exec(`UPDATE chats SET session_id='5b0f7c55-0000-4000-8000-000000000000' WHERE id=?`, ch.ID); err != nil {
		t.Fatal(err)
	}
	ev = e.send(ch.ID, "Repeat the codeword once more. Reply with only the codeword.")
	if ev["status"] != chats.TurnDone || ev["fallback"] != true || ev["sessionId"] == "5b0f7c55-0000-4000-8000-000000000000" {
		t.Fatalf("fallback turn %v", ev)
	}
	if got := last().Body; !strings.Contains(got, "HERON-42") {
		t.Fatalf("the transcript must carry the conversation: %q", got)
	}
	var sawNote bool
	for _, m := range e.get(ch.ID).Messages {
		sawNote = sawNote || (m.Role == chats.RoleSystem && m.Body == chats.ResumeFailedNote)
	}
	if !sawNote {
		t.Fatal("the fallback must be noted in the chat")
	}

	// default mode: a command that creates a file is refused without anyone being asked, and the chat shows it
	ev = e.send(ch.ID, "Use the Bash tool to run exactly this command: touch drydock_probe.txt  Then reply DONE.")
	if ev["status"] != chats.TurnDone {
		t.Fatalf("bash turn %v", ev)
	}
	var calledBash bool
	var denied *chats.Message
	d := e.get(ch.ID)
	for i, m := range d.Messages {
		if m.TurnID != ev["turnId"] {
			continue
		}
		calledBash = calledBash || (m.Role == chats.RoleTool && m.ToolName == "Bash")
		if m.Role == chats.RoleDenied {
			denied = &d.Messages[i]
		}
	}
	if calledBash {
		if denied == nil || denied.ToolName != "Bash" || !strings.Contains(string(denied.ToolInput), "touch") || denied.SuggestedRule != "Bash(touch drydock_probe.txt)" {
			t.Fatalf("a refused call must be shown with a rule to allow it: %+v", denied)
		}
		if _, err := os.Stat(filepath.Join(repo, "drydock_probe.txt")); err == nil {
			t.Fatal("the refused command ran")
		}
		// the suggested rule, allowed for this chat, lets the real CLI run exactly that command on the next turn
		e.call("chat.allowTool", map[string]any{"id": ch.ID, "tool": denied.SuggestedRule}, nil)
		ev = e.send(ch.ID, "I have allowed it now. Run exactly the same command again with the Bash tool: touch drydock_probe.txt  Then reply DONE.")
		if ev["status"] != chats.TurnDone {
			t.Fatalf("allowed turn %v", ev)
		}
		if _, err := os.Stat(filepath.Join(repo, "drydock_probe.txt")); err != nil {
			t.Fatalf("with Bash(touch drydock_probe.txt) allowed the command must run: %v (%+v)", err, e.get(ch.ID).Messages)
		}
	} else {
		t.Logf("the model did not call Bash this time; the refusal path was not exercised (reply: %q)", last().Body)
	}
}

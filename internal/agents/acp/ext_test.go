package acp_test

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// One turn of a parent whose task tool runs a sub-agent: the child says something and runs one tool; a child of another
// session sends an update too; then two notifications nobody reads arrive.
func childTurn() []fakeacp.Step {
	return []fakeacp.Step{
		{ReportClientMeta: true},
		{Say: "delegating. "},
		{Child: &fakeacp.Child{ID: "ses_child", Title: "Explore", Say: "CHILD TEXT",
			Tool: &fakeacp.Tool{ID: "t1", Title: "bash", Kind: "execute", Input: map[string]any{"command": "ls"}, Output: "a.go b.go"}}},
		{Child: &fakeacp.Child{Root: "another-session", ID: "ses_other", Tool: &fakeacp.Tool{ID: "t9", Title: "read", Input: map[string]any{"path": "x"}}}},
		{Notify: &fakeacp.Notify{Method: "opencode/something_new", Params: map[string]any{"a": 1}}},
		{Notify: &fakeacp.Notify{Method: "_vendor/something_new", Params: map[string]any{"b": 2}}},
		{Say: "done"},
	}
}

func optInChildUpdates(c *acp.Config) {
	c.ClientMeta = map[string]any{acp.ChildSessionUpdatesMeta: true}
}

// With the opt-in a sub-agent's tool calls arrive on OpenCode's own notification and become tool events of the running turn
// (the watchdog's loop detection and the activity feed read those); its text stays out of the parent's message. Updates of
// another session's child and unknown notifications change nothing.
func TestChildSessionToolCallsBecomeTheTurnsToolEvents(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{Default: childTurn()}, optInChildUpdates)
	s := startSession(t, ad, agents.RunSpec{AgentID: "a"}).(*acp.Session)
	cs := turn(t, s, "go")
	if txt := text(cs); !strings.Contains(txt, "clientMeta="+acp.ChildSessionUpdatesMeta) || strings.Contains(txt, "CHILD TEXT") || !strings.HasSuffix(txt, "delegating. done") {
		t.Fatalf("text %q", txt)
	}
	calls, results := ofKind(cs, workflow.ChunkToolCall), ofKind(cs, workflow.ChunkToolResult)
	if len(calls) != 1 || len(results) != 1 {
		t.Fatalf("one child tool call expected: %v", kinds(cs))
	}
	c := decode[workflow.ToolCall](t, calls[0])
	if c.ID != "ses_child:t1" || c.Name != "Explore: bash" || string(c.Args) != `{"command":"ls"}` {
		t.Fatalf("call %+v", c)
	}
	if r := decode[workflow.ToolResult](t, results[0]); r.ID != "ses_child:t1" || r.Output != "a.go b.go" || r.IsError {
		t.Fatalf("result %+v", r)
	}
	if n := s.ChildUpdates(); n != 3 { // the child's text, its tool call and the update; not the other session's
		t.Fatalf("child updates %d", n)
	}
	if stopReason(t, cs) != "end_turn" || !alive(s) {
		t.Fatal("the turn and the session must survive unknown notifications")
	}
	// the next turn still works
	if cs := turn(t, s, "again"); stopReason(t, cs) != "end_turn" || len(ofKind(cs, workflow.ChunkToolCall)) != 1 {
		t.Fatalf("second turn %v", kinds(cs))
	}
}

// Without the opt-in (every adapter but OpenCode's) nothing is declared and nothing changes: OpenCode then forwards a child's
// updates on the session's own stream, so its tool calls are tool events already, and its text joins the parent's message,
// which is why the OpenCode manifest opts in.
func TestWithoutTheOptInChildUpdatesStayOnTheSessionStream(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{Default: childTurn()})
	s := startSession(t, ad, agents.RunSpec{AgentID: "a"}).(*acp.Session)
	cs := turn(t, s, "go")
	if txt := text(cs); !strings.HasPrefix(txt, "clientMeta=") || strings.Contains(txt, acp.ChildSessionUpdatesMeta) || !strings.Contains(txt, "CHILD TEXT") {
		t.Fatalf("text %q", txt)
	}
	if calls := ofKind(cs, workflow.ChunkToolCall); len(calls) != 2 || decode[workflow.ToolCall](t, calls[0]).ID != "ses_child:t1" {
		t.Fatalf("calls %v", kinds(cs))
	}
	if s.ChildUpdates() != 0 || stopReason(t, cs) != "end_turn" {
		t.Fatalf("child updates %d", s.ChildUpdates())
	}
}

// The renamer touches only the child-update notification, keeps every other line byte for byte and in order, and survives
// reads of any size and a last line without a newline.
func TestTheRenamerOnlyRenamesTheChildUpdateMethod(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"the text \"opencode/session/child_update\" is not a method"}}}}`,
		`{"jsonrpc":"2.0","method":"opencode/session/child_update","params":{"rootSessionId":"s","type":"status","status":"running"}}`,
		`{"jsonrpc":"2.0","id":7,"result":{"stopReason":"end_turn"}}`,
		`{"jsonrpc":"2.0","method":"_opencode/session/child_update","params":{}}`,
		`not json "opencode/session/child_update"`,
	}, "\n")
	b, err := io.ReadAll(iotest.OneByteReader(acp.NewExtRenamerForTest(strings.NewReader(in))))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	want := strings.Split(in, "\n")
	if len(lines) != len(want) {
		t.Fatalf("lines %q", lines)
	}
	for i, l := range lines {
		if i == 1 {
			var m map[string]any
			if json.Unmarshal([]byte(l), &m) != nil || m["method"] != "_opencode/session/child_update" || m["params"].(map[string]any)["status"] != "running" {
				t.Errorf("renamed line %q", l)
			}
			continue
		}
		if l != want[i] {
			t.Errorf("line %d changed: %q", i, l)
		}
	}
}

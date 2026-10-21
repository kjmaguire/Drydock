package agui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/workflow"
)

type stream struct {
	seq uint64
	evs []events.Event
}

func (s *stream) add(typ, run string, data map[string]any) {
	s.seq++
	data["agentRun"] = run
	b, _ := json.Marshal(data)
	s.evs = append(s.evs, events.Event{Seq: s.seq, Time: time.UnixMilli(int64(1000 * s.seq)), Type: typ, AgentID: "ag_" + run, TaskID: "t1", Data: b})
}

func (s *stream) out(run, kind string, v any) {
	b, _ := json.Marshal(v)
	s.add("agent.output", run, map[string]any{"kind": kind, "data": json.RawMessage(b)})
}

// types lists the events of a thread as "TYPE id" lines, enough to read the stream's shape.
func types(t Thread) []string {
	var out []string
	for _, e := range t.Events {
		line := e.EventType()
		switch x := e.(type) {
		case *TextMessageContent:
			line += " " + x.Delta
		case *ReasoningMessageContent:
			line += " " + x.Delta
		case *ToolCallStart:
			line += " " + x.ToolCallID + " " + x.ToolCallName
		case *ToolCallArgs:
			line += " " + x.Delta
		case *ToolCallResult:
			line += " " + x.ToolCallID + " " + x.Content
			if x.IsError {
				line += " (error)"
			}
		case *RunFinished:
			line += " " + x.RunID + " " + x.Outcome + " " + x.StopReason
		case *RunStarted:
			line += " " + x.RunID
		case *RunError:
			line += " " + x.Message
		case *ActivitySnapshot:
			var parts []string
			for _, p := range x.Content.Entries {
				parts = append(parts, p.Content+"="+p.Status)
			}
			line += " " + x.ActivityType + " " + strings.Join(parts, ",")
		}
		out = append(out, line)
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestATurnBecomesARunWithMessagesToolCallsAndAPlan(t *testing.T) {
	var s stream
	s.add("agent.started", "r1", map[string]any{})
	s.add("agent.waiting", "r1", map[string]any{"state": "working"})
	s.out("r1", workflow.ChunkThinking, workflow.TextDelta{Text: "let me "})
	s.out("r1", workflow.ChunkThinking, workflow.TextDelta{Text: "look"})
	s.out("r1", workflow.ChunkPlan, workflow.PlanChunk{Entries: []workflow.PlanEntry{{Content: "read", Status: "in_progress"}, {Content: "fix", Status: "pending"}}})
	s.out("r1", workflow.ChunkText, workflow.TextDelta{Text: "Reading "})
	s.out("r1", workflow.ChunkText, workflow.TextDelta{Text: "the file."})
	s.out("r1", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "Reading the file."})
	s.out("r1", workflow.ChunkToolCall, workflow.ToolCall{ID: "c1", Name: "Read", Args: json.RawMessage(`{"path":"a.go"}`)})
	s.out("r1", workflow.ChunkToolCall, workflow.ToolCall{ID: "c1", Name: "Read", Args: json.RawMessage(`{"path":"a.go"}`)}) // an update repeats the call
	s.out("r1", workflow.ChunkUsage, workflow.UsageChunk{InputTokens: 5})                                                    // no place in the vocabulary
	s.out("r1", workflow.ChunkToolResult, workflow.ToolResult{ID: "c1", Name: "Read", Output: "package a"})
	s.out("r1", workflow.ChunkToolResult, workflow.ToolResult{Name: "Bash", Args: json.RawMessage(`{"command":"go test"}`), Output: "FAIL", IsError: true})
	s.out("r1", workflow.ChunkPlan, workflow.PlanChunk{Entries: []workflow.PlanEntry{{Content: "read", Status: "completed"}, {Content: "fix", Status: "in_progress"}}})
	s.out("r1", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "Done."}) // a message that came whole
	s.add("agent.completed", "r1", map[string]any{"stopReason": "end_turn"})

	a := Project(s.evs)
	if len(a.Threads) != 1 || a.Threads[0].ThreadID != "r1" || a.Threads[0].AgentID != "ag_r1" {
		t.Fatalf("threads %+v", a.Threads)
	}
	eq(t, types(a.Threads[0]), []string{
		"RUN_STARTED r1:1",
		"REASONING_MESSAGE_START",
		"REASONING_MESSAGE_CONTENT let me look",
		"ACTIVITY_SNAPSHOT PLAN read=in_progress,fix=pending", // a plan is its own activity: it leaves the message open
		"REASONING_MESSAGE_END",
		"TEXT_MESSAGE_START",
		"TEXT_MESSAGE_CONTENT Reading the file.",
		"TEXT_MESSAGE_END",
		"TOOL_CALL_START c1 Read",
		`TOOL_CALL_ARGS {"path":"a.go"}`,
		"TOOL_CALL_END",
		"TOOL_CALL_RESULT c1 package a",
		"TOOL_CALL_START r1:c4 Bash",
		`TOOL_CALL_ARGS {"command":"go test"}`,
		"TOOL_CALL_END",
		"TOOL_CALL_RESULT r1:c4 FAIL (error)",
		"ACTIVITY_SNAPSHOT PLAN read=completed,fix=in_progress",
		"TEXT_MESSAGE_START",
		"TEXT_MESSAGE_CONTENT Done.",
		"TEXT_MESSAGE_END",
		"RUN_FINISHED r1:1 success end_turn",
	})
	// every event keeps the stored event it was read from, in order
	var last uint64
	for _, e := range a.Threads[0].Events {
		var b Base
		raw, _ := json.Marshal(e)
		_ = json.Unmarshal(raw, &b)
		if b.Seq < last || b.Timestamp != int64(b.Seq)*1000 || b.Type != e.EventType() {
			t.Fatalf("event %s: base %+v after seq %d", raw, b, last)
		}
		last = b.Seq
	}
}

func TestEachAgentProcessIsItsOwnThreadAndEndsCleanly(t *testing.T) {
	var s stream
	s.add("agent.waiting", "w", map[string]any{"state": "working"})
	s.out("w", workflow.ChunkText, workflow.TextDelta{Text: "half"})
	s.out("rv", workflow.ChunkText, workflow.TextDelta{Text: "review"}) // a turn read from its middle opens a run
	s.add("agent.stopped", "w", map[string]any{"exitCode": 1})          // the program ended during the turn
	s.out("rv", workflow.ChunkError, workflow.ErrorChunk{Message: "rate limited"})
	s.out("rv", workflow.ChunkError, workflow.ErrorChunk{Message: "boom", Fatal: true})
	s.add("agent.failed", "rv", map[string]any{"error": "exit 2"})
	s.add("agent.waiting", "w", map[string]any{"state": "idle"}) // not a turn start
	s.add("agent.completed", "w", map[string]any{})              // nothing open: nothing to finish
	s.add("task.created", "", map[string]any{})                  // no agent run: not an agent's event

	a := Project(s.evs)
	if len(a.Threads) != 2 {
		t.Fatalf("threads %+v", a.Threads)
	}
	eq(t, types(a.Threads[0]), []string{"RUN_STARTED w:1", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT half", "TEXT_MESSAGE_END", "RUN_FINISHED w:1 stopped "})
	eq(t, types(a.Threads[1]), []string{"RUN_STARTED rv:1", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT review", "TEXT_MESSAGE_END", "RUN_ERROR boom", "RUN_ERROR exit 2"})
}

func TestLongArgumentsAndResultsAreCutOnACharacterBoundary(t *testing.T) {
	var s stream
	long := strings.Repeat("é", MaxText) // two bytes each
	s.out("r", workflow.ChunkToolResult, workflow.ToolResult{ID: "c", Name: "Read", Args: json.RawMessage(`"` + long + `"`), Output: long})
	a := Project(s.evs)
	for _, e := range a.Threads[0].Events {
		var text string
		switch x := e.(type) {
		case *ToolCallArgs:
			text = x.Delta
		case *ToolCallResult:
			text = x.Content
		default:
			continue
		}
		if !strings.HasSuffix(text, "[cut by Drydock: longer than the activity view keeps]") || len(text) > MaxText+100 || !strings.HasPrefix(text, "é") && !strings.HasPrefix(text, `"é`) {
			t.Fatalf("%s: %d bytes, ends %q", e.EventType(), len(text), text[len(text)-60:])
		}
		body := strings.TrimSuffix(text, "\n[cut by Drydock: longer than the activity view keeps]")
		if strings.ContainsRune(body, '�') || !strings.HasSuffix(body, "é") {
			t.Fatalf("cut inside a character: %q", body[len(body)-4:])
		}
	}
}

func TestAPermissionWaitAndItsAnswerAreCustomEventsInTheTurn(t *testing.T) {
	var s stream
	add := func(typ, run string, data map[string]any) { // the gate's events carry the agent run as the event's run id
		s.seq++
		b, _ := json.Marshal(data)
		s.evs = append(s.evs, events.Event{Seq: s.seq, Time: time.UnixMilli(int64(1000 * s.seq)), Type: typ, RunID: run, AgentID: "ag", TaskID: "t1", Data: b})
	}
	s.add("agent.waiting", "r", map[string]any{"state": "working"})
	add("approval.requested", "r", map[string]any{"request": "apr_1", "title": security.PermissionTitlePrefix + "Bash: npm install", "expiresAt": 99})
	add("approval.requested", "r", map[string]any{"request": "apr_x", "title": "Merge to main"})          // not a permission request
	add("permission.decided", "r", map[string]any{"approval": "apr_other", "outcome": "approved"})        // asked elsewhere
	add("permission.decided", "r", map[string]any{"outcome": "allowed_by_rule"})                          // nobody was asked
	add("permission.decided", "r", map[string]any{"approval": "apr_1", "outcome": "approved_with_edits"}) // the answer
	s.add("agent.completed", "r", map[string]any{})
	a := Project(s.evs)
	var got []string
	for _, e := range a.Threads[0].Events {
		switch x := e.(type) {
		case *Interrupt:
			got = append(got, x.Type+" "+x.Name+" "+x.Value.ID+" "+x.Value.Title)
			if x.Value.ExpiresAt != 99 {
				t.Fatalf("expiry %+v", x.Value)
			}
		case *Resume:
			got = append(got, x.Type+" "+x.Name+" "+x.Value.ID+" "+x.Value.Outcome)
		default:
			got = append(got, e.EventType())
		}
	}
	eq(t, got, []string{"RUN_STARTED", "CUSTOM drydock.interrupt apr_1 Bash: npm install", "CUSTOM drydock.resume apr_1 approved_with_edits", "RUN_FINISHED"})
}

func TestAWorkflowsOwnRequestOpensNoThread(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"request": "apr_r", "title": security.PermissionTitlePrefix + "x"})
	a := Project([]events.Event{{Seq: 1, Type: "approval.requested", RunID: "run_wf", Data: b}, {Seq: 2, Type: "permission.decided", RunID: "run_wf", Data: b}})
	if len(a.Threads) != 0 {
		t.Fatalf("threads %+v", a.Threads)
	}
}

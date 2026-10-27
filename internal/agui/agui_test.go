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
			if x.Metadata.Drydock.IsError {
				line += " (error)"
			}
		case *RunFinished:
			line += " " + x.RunID + " " + x.Outcome.Type + " " + x.Metadata.Drydock.StopReason
		case *RunStarted:
			line += " " + x.RunID
		case *RunError:
			line += " " + x.Metadata.Drydock.RunID + " " + x.Message
			if x.Code != "" {
				line += " (" + x.Code + ")"
			}
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

	a := project(t, s.evs)
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
		seq := b.Metadata.Drydock.Seq
		if seq < last || b.Timestamp != int64(seq)*1000 || b.Type != e.EventType() {
			t.Fatalf("event %s: base %+v after seq %d", raw, b, last)
		}
		last = seq
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

	a := project(t, s.evs)
	if len(a.Threads) != 2 {
		t.Fatalf("threads %+v", a.Threads)
	}
	eq(t, types(a.Threads[0]), []string{"RUN_STARTED w:1", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT half", "TEXT_MESSAGE_END", "RUN_FINISHED w:1 cancelled "})
	// the fatal error ends the run; the process's failure right after it adds its words to that RUN_ERROR, not a second one
	eq(t, types(a.Threads[1]), []string{"RUN_STARTED rv:1", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT review", "TEXT_MESSAGE_END", "RUN_ERROR rv:1 boom; exit 2"})
}

// A turn the agent reported as failed is a run that failed, with the turn's last error: not a success with stop reason
// "error" and the error dropped.
func TestATurnThatFailedEndsWithRunErrorAndItsError(t *testing.T) {
	var s stream
	turn := func(stop string, errs ...workflow.ErrorChunk) {
		s.add("agent.waiting", "r", map[string]any{"state": "working"})
		s.out("r", workflow.ChunkText, workflow.TextDelta{Text: "t"})
		for _, e := range errs {
			s.out("r", workflow.ChunkError, e)
		}
		s.add("agent.completed", "r", map[string]any{"stopReason": stop})
	}
	turn("error_max_turns", workflow.ErrorChunk{Message: "older"}, workflow.ErrorChunk{Message: "Reached maximum number of turns (3)"}) // Claude is_error
	turn("tool_use", workflow.ErrorChunk{Message: "error_during_execution"})                                                            // Claude is_error with the message's stop reason
	turn("error", workflow.ErrorChunk{Message: "prompt failed: internal error"})                                                        // ACP prompt failed
	turn("error_during_execution")                                                                                                      // no error chunk
	turn("end_turn", workflow.ErrorChunk{Message: "model \"x\" was requested but this agent does not offer it"})                        // a warning, then success
	turn("max_tokens", workflow.ErrorChunk{Message: "adapter drift: ...", Code: workflow.CodeAdapterDrift})                             // a coded warning is no failure
	turn("interrupted")
	turn("cancelled")
	got := types(project(t, s.evs).Threads[0])
	var ends []string
	for _, l := range got {
		if strings.HasPrefix(l, "RUN_FINISHED") || strings.HasPrefix(l, "RUN_ERROR") {
			ends = append(ends, l)
		}
	}
	eq(t, ends, []string{
		"RUN_ERROR r:1 Reached maximum number of turns (3) (error_max_turns)",
		"RUN_ERROR r:2 error_during_execution (tool_use)",
		"RUN_ERROR r:3 prompt failed: internal error (error)",
		"RUN_ERROR r:4 the turn ended with error_during_execution (error_during_execution)",
		"RUN_FINISHED r:5 success end_turn",
		"RUN_FINISHED r:6 success max_tokens",
		"RUN_FINISHED r:7 cancelled interrupted",
		"RUN_FINISHED r:8 cancelled cancelled",
	})
}

// A failure outside a turn (a start that failed, a crash between turns) is a run of its own that errored, so it shows; the
// failures that follow it before the agent moves on add their words to it (the same words once).
func TestAFailureOutsideATurnIsARunThatErrored(t *testing.T) {
	var s stream
	s.add("agent.failed", "start", map[string]any{"error": "the folder has configuration nobody trusted", "phase": "start"})
	s.add("agent.waiting", "r", map[string]any{"state": "working"})
	s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
	s.add("agent.failed", "r", map[string]any{"exitCode": 2, "error": "exit 2: boom", "phase": "run"})
	s.add("agent.failed", "r", map[string]any{"error": "exit 2: boom"}) // said already
	s.add("agent.failed", "r", map[string]any{"error": "the app stopped while this agent was running", "phase": "app_stopped"})
	s.add("agent.stopped", "r", map[string]any{"exitCode": 2})
	s.out("q", workflow.ChunkError, workflow.ErrorChunk{Message: "stdout: closed", Fatal: true}) // before any turn
	s.add("agent.failed", "q", map[string]any{"exitCode": 1})                                    // no words
	a := project(t, s.evs)
	eq(t, types(a.Threads[0]), []string{"RUN_STARTED start:1", "RUN_ERROR start:1 the folder has configuration nobody trusted"})
	eq(t, types(a.Threads[1]), []string{"RUN_STARTED r:1", "RUN_FINISHED r:1 success end_turn", "RUN_STARTED r:2", "RUN_ERROR r:2 exit 2: boom; the app stopped while this agent was running"})
	eq(t, types(a.Threads[2]), []string{"RUN_STARTED q:1", "RUN_ERROR q:1 stdout: closed"})
	if a.Threads[0].AgentID != "ag_start" {
		t.Fatalf("the failed start's agent: %+v", a.Threads[0])
	}
}

// The wire shapes AG-UI 1.0 fixes: RUN_FINISHED's outcome is an object, a reasoning message's role is "reasoning", RUN_STARTED
// says the protocol version; what Drydock adds (the stored event's seq, the stop reason) is under metadata.drydock.
func TestTheOutcomeIsAnObjectAndReasoningHasItsRole(t *testing.T) {
	var s stream
	s.add("agent.waiting", "r", map[string]any{"state": "working"})
	s.out("r", workflow.ChunkThinking, workflow.TextDelta{Text: "hm"})
	s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
	b, _ := json.Marshal(project(t, s.evs).Threads[0].Events)
	for _, want := range []string{
		`"type":"RUN_STARTED","timestamp":1000,"metadata":{"drydock":{"seq":1}},"threadId":"r","runId":"r:1","protocolVersion":"1.0"`,
		`"type":"REASONING_MESSAGE_START","timestamp":2000,"metadata":{"drydock":{"seq":2}},"messageId":"r:r1","role":"reasoning"`,
		`"metadata":{"drydock":{"seq":3,"stopReason":"end_turn"}},"threadId":"r","runId":"r:1","outcome":{"type":"success"}}`,
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("%s lacks %s", b, want)
		}
	}
}

func TestLongArgumentsAndResultsAreCutOnACharacterBoundary(t *testing.T) {
	var s stream
	long := strings.Repeat("é", MaxText) // two bytes each
	s.out("r", workflow.ChunkToolResult, workflow.ToolResult{ID: "c", Name: "Read", Args: json.RawMessage(`"` + long + `"`), Output: long})
	a := project(t, s.evs)
	var args, result string
	for _, e := range a.Threads[0].Events {
		switch x := e.(type) {
		case *ToolCallArgs:
			args = x.Delta
		case *ToolCallResult:
			result = x.Content
		}
	}
	// the arguments stay JSON: the string is cut inside its quotes, ending with the marker
	var v string
	if err := json.Unmarshal([]byte(args), &v); err != nil || len(args) > MaxText || !strings.HasSuffix(v, " "+CutMarker) {
		t.Fatalf("arguments: %v, %d bytes, %q", err, len(args), args[max(0, len(args)-80):])
	}
	if body := strings.TrimSuffix(v, " "+CutMarker); strings.ContainsRune(body, '\uFFFD') || !strings.HasPrefix(body, "é") || !strings.HasSuffix(body, "é") {
		t.Fatalf("cut inside a character: %q", body[len(body)-4:])
	}
	// the result keeps its head and its tail around the middle marker, each cut between characters
	head, tail, ok := strings.Cut(result, "\n"+CutMiddleMarker+"\n")
	if !ok || len(result) > MaxText+200 || len(head) > resultHead || len(tail) > resultTail || len(head) < resultHead-4 || len(tail) < resultTail-4 {
		t.Fatalf("result: %d bytes, head %d, tail %d, marker %v", len(result), len(head), len(tail), ok)
	}
	for _, part := range []string{head, tail} {
		if strings.ContainsRune(part, '\uFFFD') || !strings.HasPrefix(part, "é") || !strings.HasSuffix(part, "é") {
			t.Fatalf("cut inside a character: %q", part[len(part)-4:])
		}
	}
}

// The end of a command's output is where a test runner puts its summary, so a long result keeps its first and its last
// lines and says that its middle is gone; the UI's last line is the output's, not a marker.
func TestALongResultKeepsItsFirstAndLastLines(t *testing.T) {
	lines := []string{"=== RUN TestParse"}
	for len(strings.Join(lines, "\n")) < 20<<10 {
		lines = append(lines, "    parse_test.go:31: got 0.04, want -0.04")
	}
	lines = append(lines, "FAIL\tpkg 1.2s")
	var s stream
	s.out("r", workflow.ChunkToolResult, workflow.ToolResult{ID: "c", Name: "Bash", Output: strings.Join(lines, "\n")})
	var got string
	for _, e := range project(t, s.evs).Threads[0].Events {
		if x, ok := e.(*ToolCallResult); ok {
			got = x.Content
		}
	}
	if !strings.HasPrefix(got, "=== RUN TestParse\n") || !strings.HasSuffix(got, "\nFAIL\tpkg 1.2s") || !strings.Contains(got, "\n"+CutMiddleMarker+"\n") || len(got) > MaxText+200 {
		t.Fatalf("got %d bytes: %q ... %q", len(got), got[:40], got[len(got)-60:])
	}
}

// A Write of a long file is arguments over MaxText; cut as text they stop being JSON (a raw newline and the marker inside a
// string literal) and the UI loses the path. The longest string is cut instead, so the arguments parse and keep the path.
func TestLongArgumentsStayJSONWithTheirPath(t *testing.T) {
	content := strings.Repeat("line of code\n", 20<<10/13)
	raw, _ := json.Marshal(map[string]any{"file_path": "C:/work/app/main.go", "content": content, "lines": []any{"short", strings.Repeat("y", 300)}})
	got := clipArgs(string(raw))
	var v struct {
		FilePath string   `json:"file_path"`
		Content  string   `json:"content"`
		Lines    []string `json:"lines"`
	}
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Fatalf("not JSON: %v: %q", err, got[max(0, len(got)-120):])
	}
	if len(got) > MaxText || v.FilePath != "C:/work/app/main.go" || !strings.HasSuffix(v.Content, " "+CutMarker) || !strings.HasPrefix(v.Content, "line of code\n") {
		t.Fatalf("%d bytes, path %q, content %q...%q", len(got), v.FilePath, v.Content[:20], v.Content[len(v.Content)-70:])
	}
	// only the longest string was cut: the shorter ones are whole
	if len(v.Lines) != 2 || v.Lines[0] != "short" || len(v.Lines[1]) != 300 {
		t.Fatalf("lines %q", v.Lines)
	}
	// arguments that are not JSON are cut as text
	text := strings.Repeat("not json ", 20<<10/9)
	if got := clipArgs(text); got != clip(text, MaxText) || !strings.HasSuffix(got, "\n"+CutMarker) {
		t.Fatalf("text arguments: %q", got[len(got)-80:])
	}
	// arguments that fit pass unchanged
	if got := clipArgs(`{"b":1,"a":2}`); got != `{"b":1,"a":2}` {
		t.Fatalf("short arguments changed: %q", got)
	}
}

// Project is pure: the same arguments are cut the same way on every read, and what the agent wrote stays as written.
func TestLongArgumentsAreCutTheSameWayOnEveryReadAndKeepTheirNumbers(t *testing.T) {
	t.Run("two strings of one length: the first in key order is cut, every time", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{"first": strings.Repeat("a", 5000), "second": strings.Repeat("a", 5000), "path": "x.go"})
		var first string
		for i := 0; i < 50; i++ {
			got := clipArgs(string(raw))
			if i == 0 {
				first = got
			} else if got != first {
				t.Fatalf("read %d differs:\n%s\n%s", i, got[:60], first[:60])
			}
		}
		var v struct{ First, Second, Path string }
		if err := json.Unmarshal([]byte(first), &v); err != nil || v.Path != "x.go" || len(v.Second) != 5000 || !strings.HasSuffix(v.First, " "+CutMarker) {
			t.Fatalf("err %v, path %q, first %d bytes, second %d bytes", err, v.Path, len(v.First), len(v.Second))
		}
	})
	t.Run("an integer above 2^53 is not rounded", func(t *testing.T) {
		raw := `{"offset": 12345678901234567890, "content": "` + strings.Repeat("c", 9000) + `"}`
		got := clipArgs(raw)
		if !strings.Contains(got, `"offset":12345678901234567890`) {
			t.Fatalf("offset changed: %s", got[:80])
		}
	})
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
	a := project(t, s.evs)
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

	// an answer that comes after its turn ended has no run to go in (AG-UI allows nothing after RUN_FINISHED but a new run)
	s.add("agent.waiting", "r", map[string]any{"state": "working"})
	add("approval.requested", "r", map[string]any{"request": "apr_2", "title": security.PermissionTitlePrefix + "Bash: ls"})
	s.add("agent.completed", "r", map[string]any{"stopReason": "interrupted"})
	add("permission.decided", "r", map[string]any{"approval": "apr_2", "outcome": "cancelled"})
	evs := project(t, s.evs).Threads[0].Events
	if last := evs[len(evs)-1]; last.EventType() != TypeRunFinished {
		t.Fatalf("after the turn: %s", last.EventType())
	}
}

func TestAWorkflowsOwnRequestOpensNoThread(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"request": "apr_r", "title": security.PermissionTitlePrefix + "x"})
	a := project(t, []events.Event{{Seq: 1, Type: "approval.requested", RunID: "run_wf", Data: b}, {Seq: 2, Type: "permission.decided", RunID: "run_wf", Data: b}})
	if len(a.Threads) != 0 {
		t.Fatalf("threads %+v", a.Threads)
	}
}

// ends lists how each run of a thread ended.
func ends(th Thread) []string {
	var out []string
	for _, l := range types(th) {
		if strings.HasPrefix(l, "RUN_FINISHED") || strings.HasPrefix(l, "RUN_ERROR") {
			out = append(out, l)
		}
	}
	return out
}

// A warning (an ACP agent asked for a model it does not offer) does not fail a turn that then answered, whatever its stop
// reason: the chat's rule (an error, no answer and no end_turn is a failed turn). Without an answer the warning is why it failed.
func TestAWarningDoesNotFailATurnThatAnswered(t *testing.T) {
	warn := workflow.ErrorChunk{Message: `model "x" was requested but this agent does not offer it (offered: a, b); the agent's default is in use`}
	var s stream
	turn := func(stop, answer string) {
		s.add("agent.waiting", "r", map[string]any{"state": "working"})
		s.out("r", workflow.ChunkError, warn) // session.warn: no code
		s.out("r", workflow.ChunkText, workflow.TextDelta{Text: answer + "…"})
		if answer != "" {
			s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: answer}) // ACP flushes the whole message
		}
		s.add("agent.completed", "r", map[string]any{"stopReason": stop})
	}
	turn("max_tokens", "The whole answer.")
	turn("refusal", "I can't help with that.")
	turn("max_tokens", "") // text began but no message was finished: no answer, as the chat counts one
	turn("refusal", "   ") // nothing but space is no answer either
	turn("end_turn", "")   // end_turn is a success whatever came before
	eq(t, ends(project(t, s.evs).Threads[0]), []string{
		"RUN_FINISHED r:1 success max_tokens",
		"RUN_FINISHED r:2 success refusal",
		"RUN_ERROR r:3 " + warn.Message + " (max_tokens)",
		"RUN_ERROR r:4 " + warn.Message + " (refusal)",
		"RUN_FINISHED r:5 success end_turn",
	})
}

// A turn that never reported its end (the next turn started while it was open) ends as agent.completed with no stop reason
// would end it: a warning and no answer is a failure, not a success.
func TestARunLeftOpenEndsAsItsCompletionWould(t *testing.T) {
	var s stream
	s.add("agent.waiting", "r", map[string]any{"state": "working"})
	s.out("r", workflow.ChunkError, workflow.ErrorChunk{Message: "rate limited"})
	s.out("r", workflow.ChunkThinking, workflow.TextDelta{Text: "hm"})
	s.add("agent.waiting", "r", map[string]any{"state": "working"}) // r:1 never completed
	s.out("r", workflow.ChunkError, workflow.ErrorChunk{Message: "rate limited"})
	s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "an answer"})
	s.add("agent.waiting", "r", map[string]any{"state": "working"}) // r:2 answered
	s.out("r", workflow.ChunkError, workflow.ErrorChunk{Message: "adapter drift: ...", Code: workflow.CodeAdapterDrift})
	s.out("r", workflow.ChunkToolCall, workflow.ToolCall{ID: "c1", Name: "Read"})
	s.add("agent.waiting", "r", map[string]any{"state": "working"}) // r:3 had only a coded warning
	s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
	eq(t, ends(project(t, s.evs).Threads[0]), []string{
		"RUN_ERROR r:1 rate limited",
		"RUN_FINISHED r:2 success ",
		"RUN_FINISHED r:3 success ",
		"RUN_FINISHED r:4 success end_turn",
	})
}

// One RUN_ERROR per failed turn: the process's failure right after a fatal error chunk adds its words to it. Once the agent
// has moved on from the failed turn (it completed, went idle, or its process stopped), a later failure is its own.
func TestAFailureAfterTheFailedTurnClosedIsItsOwn(t *testing.T) {
	var s stream
	// a Claude turn that failed (is_error), then a crash while the agent sat idle
	s.add("agent.waiting", "a", map[string]any{"state": "working"})
	s.out("a", workflow.ChunkError, workflow.ErrorChunk{Message: "Reached maximum number of turns (3)"})
	s.add("agent.completed", "a", map[string]any{"stopReason": "error_max_turns"})
	s.add("agent.failed", "a", map[string]any{"exitCode": 1, "error": "exit 1: panic", "phase": "run"})
	s.add("agent.stopped", "a", map[string]any{"exitCode": 1})
	// a fatal error, the agent idle afterwards, then a crash
	s.add("agent.waiting", "b", map[string]any{"state": "working"})
	s.out("b", workflow.ChunkError, workflow.ErrorChunk{Message: "boom", Fatal: true})
	s.add("agent.waiting", "b", map[string]any{"state": "idle"})
	s.add("agent.failed", "b", map[string]any{"exitCode": 2, "error": "exit 2", "phase": "run"})
	// a fatal error and the process's failure (one RUN_ERROR); its end went unrecorded, so the next start reports it again
	s.add("agent.waiting", "c", map[string]any{"state": "working"})
	s.out("c", workflow.ChunkError, workflow.ErrorChunk{Message: "connection to the agent ended during the turn: EOF", Fatal: true})
	s.add("agent.failed", "c", map[string]any{"exitCode": 1, "error": "exit 1", "phase": "run"})
	s.add("agent.failed", "c", map[string]any{"error": "record run end: disk full", "phase": "bookkeeping"})
	s.add("agent.stopped", "c", map[string]any{"exitCode": 1})
	s.add("agent.failed", "c", map[string]any{"error": "the app stopped while this agent was running", "phase": "app_stopped"})
	a := project(t, s.evs)
	if len(a.Threads) != 3 {
		t.Fatalf("threads %+v", a.Threads)
	}
	eq(t, types(a.Threads[0]), []string{"RUN_STARTED a:1", "RUN_ERROR a:1 Reached maximum number of turns (3) (error_max_turns)", "RUN_STARTED a:2", "RUN_ERROR a:2 exit 1: panic"})
	eq(t, types(a.Threads[1]), []string{"RUN_STARTED b:1", "RUN_ERROR b:1 boom", "RUN_STARTED b:2", "RUN_ERROR b:2 exit 2"})
	eq(t, types(a.Threads[2]), []string{"RUN_STARTED c:1", "RUN_ERROR c:1 connection to the agent ended during the turn: EOF; exit 1", "RUN_STARTED c:2", "RUN_ERROR c:2 the app stopped while this agent was running"})
}

// The runtime's agent.failed {phase: bookkeeping} says Drydock could not record a run's end (its database), not that the
// agent's turn failed: it opens no run, ends none, and adds nothing to a turn's error.
func TestABookkeepingFailureIsNoTurnFailure(t *testing.T) {
	var s stream
	s.add("agent.waiting", "r", map[string]any{"state": "working"})
	s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "done"})
	s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
	s.add("agent.failed", "r", map[string]any{"error": "record run end: database is locked", "phase": "bookkeeping"}) // after a clean exit
	s.add("agent.stopped", "r", map[string]any{"exitCode": 0})
	s.add("agent.failed", "only", map[string]any{"error": "record run end: disk full", "phase": "bookkeeping"}) // nothing else
	s.add("agent.failed", "f", map[string]any{"exitCode": 2, "error": "exit 2", "phase": "run"})
	s.add("agent.failed", "f", map[string]any{"error": "record run end: disk full", "phase": "bookkeeping"})
	s.add("agent.stopped", "f", map[string]any{"exitCode": 2})
	a := project(t, s.evs)
	if len(a.Threads) != 2 || a.Threads[0].ThreadID != "r" || a.Threads[1].ThreadID != "f" {
		t.Fatalf("threads %+v", a.Threads)
	}
	eq(t, types(a.Threads[0]), []string{"RUN_STARTED r:1", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT done", "TEXT_MESSAGE_END", "RUN_FINISHED r:1 success end_turn"})
	eq(t, types(a.Threads[1]), []string{"RUN_STARTED f:1", "RUN_ERROR f:1 exit 2"})
}

// An adapter streams a message as deltas and then sends the whole text again as one message_end (an ACP agent at its next
// tool call or at the end of the turn; Claude Code once per text block). When a thought or a tool result closed the streamed
// message first, the message_end is not a second message: the sentence shows once.
func TestAnAcpSentenceIsShownOnceWhenAThoughtOrAToolResultComesBeforeItsMessageEnd(t *testing.T) {
	msgs := func(th Thread) []string {
		var out []string
		for _, l := range types(th) {
			if strings.HasPrefix(l, "TEXT_MESSAGE_CONTENT") || strings.HasPrefix(l, "REASONING_MESSAGE_CONTENT") {
				out = append(out, l)
			}
		}
		return out
	}
	t.Run("a thought between the deltas and the message_end", func(t *testing.T) {
		var s stream
		s.add("agent.waiting", "r", map[string]any{"state": "working"})
		s.out("r", workflow.ChunkText, workflow.TextDelta{Text: "Hello "})
		s.out("r", workflow.ChunkThinking, workflow.TextDelta{Text: "hmm"})
		s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "Hello "})
		s.add("agent.completed", "r", map[string]any{"stopReason": "max_tokens"})
		th := project(t, s.evs).Threads[0]
		eq(t, msgs(th), []string{"TEXT_MESSAGE_CONTENT Hello ", "REASONING_MESSAGE_CONTENT hmm"})
		eq(t, ends(th), []string{"RUN_FINISHED r:1 success max_tokens"}) // the message_end still counts as the answer
	})
	t.Run("a permission step: tool_call, text, tool_result, message_end", func(t *testing.T) {
		var s stream
		s.add("agent.waiting", "r", map[string]any{"state": "working"})
		s.out("r", workflow.ChunkToolCall, workflow.ToolCall{ID: "t1", Name: "Bash"})
		s.out("r", workflow.ChunkText, workflow.TextDelta{Text: "Running it."})
		s.out("r", workflow.ChunkToolResult, workflow.ToolResult{ID: "t1", Output: "ok"})
		s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "Running it."})
		s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
		eq(t, msgs(project(t, s.evs).Threads[0]), []string{"TEXT_MESSAGE_CONTENT Running it."})
	})
	t.Run("two text blocks, then one message_end per block", func(t *testing.T) {
		var s stream
		s.add("agent.waiting", "r", map[string]any{"state": "working"})
		s.out("r", workflow.ChunkText, workflow.TextDelta{Text: "First. "})
		s.out("r", workflow.ChunkText, workflow.TextDelta{Text: "Second."})
		s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "First. "})
		s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "Second."})
		s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
		eq(t, msgs(project(t, s.evs).Threads[0]), []string{"TEXT_MESSAGE_CONTENT First. Second."})
	})
	t.Run("a message_end alone is a message that came whole", func(t *testing.T) {
		var s stream
		s.add("agent.waiting", "r", map[string]any{"state": "working"})
		s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "Done."})
		s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
		eq(t, types(project(t, s.evs).Threads[0]), []string{"RUN_STARTED r:1", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT Done.", "TEXT_MESSAGE_END", "RUN_FINISHED r:1 success end_turn"})
	})
	t.Run("two streamed messages in one run are two messages, in order", func(t *testing.T) {
		var s stream
		s.add("agent.waiting", "r", map[string]any{"state": "working"})
		s.out("r", workflow.ChunkText, workflow.TextDelta{Text: "A"})
		s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "A"})
		s.out("r", workflow.ChunkText, workflow.TextDelta{Text: "B"})
		s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "B"})
		s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
		eq(t, types(project(t, s.evs).Threads[0]), []string{"RUN_STARTED r:1",
			"TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT A", "TEXT_MESSAGE_END",
			"TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT B", "TEXT_MESSAGE_END",
			"RUN_FINISHED r:1 success end_turn"})
	})
	t.Run("three text blocks, one message_end per block, each taking its own part", func(t *testing.T) {
		var s stream
		s.add("agent.waiting", "r", map[string]any{"state": "working"})
		s.out("r", workflow.ChunkText, workflow.TextDelta{Text: "A"})
		s.out("r", workflow.ChunkText, workflow.TextDelta{Text: "B"})
		s.out("r", workflow.ChunkText, workflow.TextDelta{Text: "C"})
		s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "A"})
		s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "B"})
		s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "C"})
		s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
		eq(t, msgs(project(t, s.evs).Threads[0]), []string{"TEXT_MESSAGE_CONTENT ABC"})
	})
	t.Run("a short message that came whole is its own, though an earlier message of the run said its words", func(t *testing.T) {
		var s stream
		s.add("agent.waiting", "r", map[string]any{"state": "working"})
		s.out("r", workflow.ChunkText, workflow.TextDelta{Text: "I will say OK when it is done."})
		s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "I will say OK when it is done."})
		s.out("r", workflow.ChunkToolCall, workflow.ToolCall{ID: "t1", Name: "Bash"})
		s.out("r", workflow.ChunkToolResult, workflow.ToolResult{ID: "t1", Output: "ok"})
		s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "OK"})
		s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
		eq(t, msgs(project(t, s.evs).Threads[0]), []string{"TEXT_MESSAGE_CONTENT I will say OK when it is done.", "TEXT_MESSAGE_CONTENT OK"})
	})
	t.Run("a message_end whose text was streamed in an earlier run is a message of its own", func(t *testing.T) {
		var s stream
		s.add("agent.waiting", "r", map[string]any{"state": "working"})
		s.out("r", workflow.ChunkText, workflow.TextDelta{Text: "Same words."})
		s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
		s.add("agent.waiting", "r", map[string]any{"state": "working"})
		s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "Same words."})
		s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
		eq(t, msgs(project(t, s.evs).Threads[0]), []string{"TEXT_MESSAGE_CONTENT Same words.", "TEXT_MESSAGE_CONTENT Same words."})
	})
}

// Whether a turn failed is workflow.TurnFailed, the chat's rule too: a coded warning (a setting the agent does not offer)
// never fails a turn, an uncoded error with no answer and no end_turn does, and the adapter's own error stop reason does
// whatever came before, with the last uncoded error as the reason, else "the turn ended with <stop>".
func TestWhetherATurnFailedIsTheChatsRule(t *testing.T) {
	var s stream
	turn := func(stop string, chunks ...any) {
		s.add("agent.waiting", "r", map[string]any{"state": "working"})
		for _, c := range chunks {
			switch c := c.(type) {
			case workflow.ErrorChunk:
				s.out("r", workflow.ChunkError, c)
			case workflow.ToolCall:
				s.out("r", workflow.ChunkToolCall, c)
			case workflow.TextDelta:
				s.out("r", workflow.ChunkMessageEnd, c)
			}
		}
		s.add("agent.completed", "r", map[string]any{"stopReason": stop})
	}
	settings := workflow.ErrorChunk{Message: `model "x" was requested but this agent does not offer it`, Code: workflow.CodeSettings}
	turn("max_turn_requests", settings, workflow.ToolCall{ID: "c1", Name: "Read"})                              // a coded warning, no answer, no end_turn: success
	turn("max_tokens", workflow.ErrorChunk{Message: "rate limited"})                                            // an uncoded error, no answer: failed
	turn("error_during_execution", workflow.TextDelta{Text: "an answer"}, workflow.ErrorChunk{Message: "boom"}) // error_* after an answer: failed, with the error
	turn("error_max_turns", workflow.TextDelta{Text: "an answer"}, settings)                                    // error_* with only a coded warning: the stop reason is the reason
	turn("end_turn", workflow.ErrorChunk{Message: "older"}, workflow.ErrorChunk{Message: "newer"}, settings)    // end_turn is a success
	turn("max_tokens", workflow.ErrorChunk{Message: "older"}, workflow.ErrorChunk{Message: "newer"}, settings)  // the last uncoded error is the reason
	eq(t, ends(project(t, s.evs).Threads[0]), []string{
		"RUN_FINISHED r:1 success max_turn_requests",
		"RUN_ERROR r:2 rate limited (max_tokens)",
		"RUN_ERROR r:3 boom (error_during_execution)",
		"RUN_ERROR r:4 the turn ended with error_max_turns (error_max_turns)",
		"RUN_FINISHED r:5 success end_turn",
		"RUN_ERROR r:6 newer (max_tokens)",
	})
}

// The published AG-UI 1.0 client (@ag-ui/client 1.0.3) drops every top-level key its schema does not declare, with a warning,
// and keeps metadata: every projected event has only declared keys at the top, and Drydock's own fields live under
// metadata.drydock.
func TestEveryEventHasOnlyTheKeysAGUIDeclares(t *testing.T) {
	base := []string{"type", "timestamp", "rawEvent", "metadata"}
	declared := map[string][]string{ // @ag-ui/core 1.0.3 schemas.d.ts (the 1.0 schema is the same from 1.0.0 to 1.0.3)
		TypeRunStarted:              {"threadId", "runId", "protocolVersion", "parentRunId", "input"}, // the run events name no sub-agent run
		TypeRunFinished:             {"threadId", "runId", "result", "outcome", "usage"},
		TypeRunError:                {"message", "code", "usage"},
		TypeTextMessageStart:        {"subagentRunId", "messageId", "role", "name"},
		TypeTextMessageContent:      {"subagentRunId", "messageId", "delta"},
		TypeTextMessageEnd:          {"subagentRunId", "messageId"},
		TypeReasoningMessageStart:   {"subagentRunId", "messageId", "role"},
		TypeReasoningMessageContent: {"subagentRunId", "messageId", "delta"},
		TypeReasoningMessageEnd:     {"subagentRunId", "messageId"},
		TypeToolCallStart:           {"subagentRunId", "toolCallId", "toolCallName", "parentMessageId"},
		TypeToolCallArgs:            {"subagentRunId", "toolCallId", "delta"},
		TypeToolCallEnd:             {"subagentRunId", "toolCallId"},
		TypeToolCallResult:          {"subagentRunId", "messageId", "toolCallId", "content", "role"},
		TypeActivitySnapshot:        {"subagentRunId", "messageId", "activityType", "content", "replace"},
		TypeCustom:                  {"subagentRunId", "name", "value"},
	}
	var s stream
	add := func(typ, run string, data map[string]any) {
		s.seq++
		b, _ := json.Marshal(data)
		s.evs = append(s.evs, events.Event{Seq: s.seq, Time: time.UnixMilli(int64(1000 * s.seq)), Type: typ, RunID: run, AgentID: "ag", Data: b})
	}
	s.add("agent.waiting", "r", map[string]any{"state": "working"})
	s.out("r", workflow.ChunkThinking, workflow.TextDelta{Text: "hm"})
	s.out("r", workflow.ChunkText, workflow.TextDelta{Text: "Reading."})
	s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "Reading."})
	s.out("r", workflow.ChunkToolCall, workflow.ToolCall{ID: "c1", Name: "Read", Args: json.RawMessage(`{"path":"a.go"}`)})
	s.out("r", workflow.ChunkToolResult, workflow.ToolResult{ID: "c1", Output: "package a", IsError: true})
	s.out("r", workflow.ChunkPlan, workflow.PlanChunk{Entries: []workflow.PlanEntry{{Content: "read", Status: "completed"}}})
	add("approval.requested", "r", map[string]any{"request": "apr_1", "title": security.PermissionTitlePrefix + "Bash: ls"})
	add("permission.decided", "r", map[string]any{"approval": "apr_1", "outcome": "approved"})
	add("permission.decided", "r", map[string]any{"tool": "Bash", "outcome": "denied", "rule": "no pushes"})
	s.out("r", workflow.ChunkPermissionDenied, workflow.PermissionDenial{ToolName: "WebFetch", ToolUseID: "tu_1"})
	s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
	s.add("agent.waiting", "r", map[string]any{"state": "working"})
	s.out("r", workflow.ChunkError, workflow.ErrorChunk{Message: "boom", Fatal: true})
	seen := map[string]bool{}
	for _, e := range project(t, s.evs).Threads[0].Events {
		raw, _ := json.Marshal(e)
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		typ := e.EventType()
		seen[typ] = true
		allowed := map[string]bool{}
		for _, k := range append(append([]string{}, base...), declared[typ]...) {
			allowed[k] = true
		}
		for k := range m {
			if !allowed[k] {
				t.Errorf("%s carries %q at the top level, which AG-UI 1.0 does not declare: %s", typ, k, raw)
			}
		}
		var md struct {
			Metadata struct {
				Drydock *DrydockMeta `json:"drydock"`
			} `json:"metadata"`
		}
		if json.Unmarshal(raw, &md) != nil || md.Metadata.Drydock == nil || md.Metadata.Drydock.Seq == 0 {
			t.Errorf("%s lacks metadata.drydock.seq: %s", typ, raw)
		}
	}
	for typ := range declared {
		if !seen[typ] {
			t.Errorf("the sample has no %s", typ)
		}
	}
}

// A process stopped before its first turn (Cancel or Stop right after it spawned) leaves a thread with no events; the thread
// says its process ended, so the UI does not call it starting for good. A thread whose process still runs has not ended.
func TestAProcessStoppedBeforeItsFirstTurnIsAnEndedThreadWithNoEvents(t *testing.T) {
	var s stream
	s.add("agent.started", "r1", map[string]any{"adapter": "claude"})
	s.add("agent.stopped", "r1", map[string]any{"killed": true})
	s.add("agent.started", "r2", map[string]any{"adapter": "claude"})
	s.add("agent.waiting", "r2", map[string]any{"state": "working"})
	a := project(t, s.evs)
	if len(a.Threads) != 2 {
		t.Fatalf("threads %+v", a.Threads)
	}
	if !a.Threads[0].Ended || len(a.Threads[0].Events) != 0 || a.Threads[0].Adapter != "claude" {
		t.Fatalf("stopped before its first turn: ended %v, %d events, adapter %q", a.Threads[0].Ended, len(a.Threads[0].Events), a.Threads[0].Adapter)
	}
	if a.Threads[1].Ended {
		t.Fatal("a running process's thread says it ended")
	}
	// the stop during a turn is still the cancelled run, and the thread ended too
	s.add("agent.stopped", "r2", map[string]any{"killed": true})
	a = project(t, s.evs)
	eq(t, ends(a.Threads[1]), []string{"RUN_FINISHED r2:1 cancelled "})
	if !a.Threads[1].Ended {
		t.Fatal("a stopped process's thread does not say it ended")
	}
}

// The ids an ACP agent gives its calls are its own, and it may use one again in a later turn: that call is announced again
// in its run. A result for a call the parser never saw, with no name, is a call named "tool", not one with no name.
func TestACallIdUsedAgainInALaterTurnIsAnnouncedAgain(t *testing.T) {
	var s stream
	s.add("agent.waiting", "r", map[string]any{"state": "working"})
	s.out("r", workflow.ChunkToolCall, workflow.ToolCall{ID: "call_1", Name: "Read"})
	s.out("r", workflow.ChunkToolResult, workflow.ToolResult{ID: "call_1", Name: "Read", Output: "a"})
	s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
	s.add("agent.waiting", "r", map[string]any{"state": "working"})
	s.out("r", workflow.ChunkToolCall, workflow.ToolCall{ID: "call_1", Name: "Bash"})
	s.out("r", workflow.ChunkToolResult, workflow.ToolResult{ID: "call_1", Name: "Bash", Output: "b"})
	s.out("r", workflow.ChunkToolResult, workflow.ToolResult{ID: "toolu_9", Output: "c"}) // never announced, no name
	s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
	eq(t, types(project(t, s.evs).Threads[0]), []string{
		"RUN_STARTED r:1", "TOOL_CALL_START call_1 Read", "TOOL_CALL_END", "TOOL_CALL_RESULT call_1 a", "RUN_FINISHED r:1 success end_turn",
		"RUN_STARTED r:2", "TOOL_CALL_START call_1 Bash", "TOOL_CALL_END", "TOOL_CALL_RESULT call_1 b",
		"TOOL_CALL_START toolu_9 tool", "TOOL_CALL_END", "TOOL_CALL_RESULT toolu_9 c", "RUN_FINISHED r:2 success end_turn",
	})
}

// A decision nobody was asked for shows in the turn: a rule's refusal or an approval for the task (permission.decided without
// an approval id), and a tool use Drydock refused with nobody to ask (a permission_denied chunk with a reason). The
// allowed_by_rule of every ordinary call is left out, and so is the CLI's own permission_denied (no reason): its failed result
// and, when the gate was asked, the request's resume already tell it.
func TestARefusalOrARuleDecisionIsACustomEventInTheTurn(t *testing.T) {
	var s stream
	add := func(typ, run string, data map[string]any) {
		s.seq++
		b, _ := json.Marshal(data)
		s.evs = append(s.evs, events.Event{Seq: s.seq, Time: time.UnixMilli(int64(1000 * s.seq)), Type: typ, RunID: run, AgentID: "ag", Data: b})
	}
	s.add("agent.waiting", "r", map[string]any{"state": "working"})
	add("permission.decided", "r", map[string]any{"tool": "Read", "outcome": "allowed_by_rule", "kind": "read"})
	add("permission.decided", "r", map[string]any{"tool": "Bash", "outcome": "denied", "kind": "git_push", "rule": "pushes go through the merge gate"})
	add("permission.decided", "r", map[string]any{"tool": "Edit", "outcome": "approved_for_task", "kind": "edit", "rule": "apr_1"})
	s.out("r", workflow.ChunkPermissionDenied, workflow.PermissionDenial{ToolName: "WebFetch", ToolUseID: "toolu_1", Input: json.RawMessage(`{"url":"x"}`)})
	s.out("r", workflow.ChunkPermissionDenied, workflow.PermissionDenial{ToolName: "Bash", Reason: "nobody can answer this agent's permission requests"})
	s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
	var got []string
	for _, e := range project(t, s.evs).Threads[0].Events {
		if x, ok := e.(*Decided); ok {
			got = append(got, strings.TrimSpace(x.Name+" "+x.Value.Tool+" "+x.Value.Outcome+" "+x.Value.ToolCallID+" rule="+x.Value.Rule+" "+x.Value.Reason))
			continue
		}
		got = append(got, e.EventType())
	}
	eq(t, got, []string{
		"RUN_STARTED",
		"drydock.decided Bash denied  rule=pushes go through the merge gate",
		"drydock.decided Edit approved_for_task  rule=apr_1",
		"drydock.decided Bash refused  rule= nobody can answer this agent's permission requests",
		"RUN_FINISHED",
	})
	// a refusal is a CUSTOM event, as the wire shows it
	b, _ := json.Marshal(project(t, s.evs).Threads[0].Events[3])
	if want := `"name":"drydock.decided","value":{"tool":"Bash","outcome":"refused","reason":"nobody can answer this agent's permission requests"}`; !strings.Contains(string(b), want) {
		t.Fatalf("%s lacks %s", b, want)
	}
}

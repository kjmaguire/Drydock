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
			line += " " + x.RunID + " " + x.Outcome.Type + " " + x.StopReason
		case *RunStarted:
			line += " " + x.RunID
		case *RunError:
			line += " " + x.RunID + " " + x.Message
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

// The wire shapes AG-UI 1.0 fixes: RUN_FINISHED's outcome is an object, a reasoning message's role is "reasoning".
func TestTheOutcomeIsAnObjectAndReasoningHasItsRole(t *testing.T) {
	var s stream
	s.add("agent.waiting", "r", map[string]any{"state": "working"})
	s.out("r", workflow.ChunkThinking, workflow.TextDelta{Text: "hm"})
	s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
	b, _ := json.Marshal(project(t, s.evs).Threads[0].Events)
	for _, want := range []string{`"type":"REASONING_MESSAGE_START","seq":2,"timestamp":2000,"messageId":"r:r1","role":"reasoning"`, `"outcome":{"type":"success"},"stopReason":"end_turn"`} {
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

package agui

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// project is Project for the tests: every thread it returns must keep AG-UI's ordering rules (CheckOrder), whatever the test
// then looks at.
func project(t *testing.T, evs []events.Event) Activity {
	t.Helper()
	a := Project(evs)
	for _, th := range a.Threads {
		if err := CheckOrder(th.Events); err != nil {
			t.Fatalf("thread %s breaks AG-UI's ordering: %v\n  %s", th.ThreadID, err, strings.Join(types(th), "\n  "))
		}
	}
	return a
}

// The streams agents really leave on the bus, the failing and cut-short ones above all: every thread projected from them
// keeps AG-UI's ordering.
func TestEveryProjectedThreadKeepsAGUIsOrder(t *testing.T) {
	streams := map[string]func(s *stream){
		"a Claude turn that ran out of turns (is_error, error_max_turns)": func(s *stream) {
			s.add("agent.waiting", "r", map[string]any{"state": "working"})
			s.out("r", workflow.ChunkText, workflow.TextDelta{Text: "working"})
			s.out("r", workflow.ChunkToolCall, workflow.ToolCall{ID: "c1", Name: "Bash"})
			s.out("r", workflow.ChunkError, workflow.ErrorChunk{Message: "Reached maximum number of turns (3)"})
			s.add("agent.completed", "r", map[string]any{"stopReason": "error_max_turns"})
			s.add("agent.waiting", "r", map[string]any{"state": "working"}) // the next turn
			s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
		},
		"an ACP prompt that failed": func(s *stream) {
			s.add("agent.waiting", "r", map[string]any{"state": "working"})
			s.out("r", workflow.ChunkThinking, workflow.TextDelta{Text: "hm"})
			s.out("r", workflow.ChunkError, workflow.ErrorChunk{Message: "prompt failed: internal error"})
			s.add("agent.completed", "r", map[string]any{"stopReason": "error"})
		},
		"an ACP connection that ended during the turn": func(s *stream) {
			s.add("agent.waiting", "r", map[string]any{"state": "working"})
			s.out("r", workflow.ChunkText, workflow.TextDelta{Text: "half"})
			s.out("r", workflow.ChunkError, workflow.ErrorChunk{Message: "connection to the agent ended during the turn: EOF", Fatal: true})
			s.add("agent.failed", "r", map[string]any{"error": "exit 1: panic", "phase": "run"})
			s.add("agent.stopped", "r", map[string]any{"exitCode": 1})
		},
		"a start that failed": func(s *stream) {
			s.add("agent.failed", "r", map[string]any{"error": "the folder has configuration nobody trusted", "phase": "start"})
		},
		"a crash between turns": func(s *stream) {
			s.add("agent.waiting", "r", map[string]any{"state": "working"})
			s.out("r", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "done"})
			s.add("agent.completed", "r", map[string]any{"stopReason": "end_turn"})
			s.add("agent.failed", "r", map[string]any{"error": "exit 2", "phase": "run"})
			s.add("agent.stopped", "r", map[string]any{"exitCode": 2})
			s.add("agent.failed", "r", map[string]any{"error": "the app stopped while this agent was running", "phase": "app_stopped"})
		},
		"a fatal error before any turn": func(s *stream) {
			s.out("r", workflow.ChunkError, workflow.ErrorChunk{Message: "stdout: closed", Fatal: true})
			s.add("agent.failed", "r", map[string]any{"error": "exit 1"})
		},
		"an interrupted turn and a permission answered after it ended": func(s *stream) {
			s.add("agent.waiting", "r", map[string]any{"state": "working"})
			s.out("r", workflow.ChunkThinking, workflow.TextDelta{Text: "let me"})
			s.add("approval.requested", "r", map[string]any{"request": "apr_1", "title": security.PermissionTitlePrefix + "Bash: ls"})
			s.add("agent.completed", "r", map[string]any{"stopReason": "interrupted"})
			s.add("permission.decided", "r", map[string]any{"approval": "apr_1", "outcome": "cancelled"})
			s.add("agent.stopped", "r", map[string]any{})
		},
		"a turn stopped with its message open, and one read from its middle": func(s *stream) {
			s.out("r", workflow.ChunkText, workflow.TextDelta{Text: "tail of a message whose start was cut"})
			s.out("r", workflow.ChunkToolResult, workflow.ToolResult{ID: "c9", Output: "ok"})
			s.add("agent.waiting", "r", map[string]any{"state": "working"}) // a new turn while one is open
			s.out("r", workflow.ChunkText, workflow.TextDelta{Text: "x"})
			s.add("agent.stopped", "r", map[string]any{})
			s.add("agent.completed", "r", map[string]any{"stopReason": "error"}) // nothing open: nothing to end
		},
	}
	for name, build := range streams {
		t.Run(name, func(t *testing.T) {
			var s stream
			build(&s)
			if a := project(t, s.evs); len(a.Threads) != 1 {
				t.Fatalf("threads %+v", a.Threads)
			}
		})
	}
}

// CheckOrder refuses what AG-UI's verifier refuses (and RUN_ERROR outside a run, which Drydock never sends).
func TestCheckOrderRefusesWhatAGUIsVerifierRefuses(t *testing.T) {
	b := func(typ string) Base { return Base{Type: typ} }
	started := func(id string) Event { return &RunStarted{Base: b(TypeRunStarted), RunID: id} }
	finished := &RunFinished{Base: b(TypeRunFinished), Outcome: RunOutcome{Type: OutcomeSuccess}}
	failed := &RunError{Base: b(TypeRunError), Message: "x"}
	text := func(typ, id string) Event {
		switch typ {
		case TypeTextMessageStart:
			return &TextMessageStart{Base: b(typ), MessageID: id}
		case TypeTextMessageContent:
			return &TextMessageContent{Base: b(typ), MessageID: id}
		case TypeReasoningMessageStart:
			return &ReasoningMessageStart{Base: b(typ), MessageID: id}
		case TypeReasoningMessageContent:
			return &ReasoningMessageContent{Base: b(typ), MessageID: id}
		case TypeReasoningMessageEnd:
			return &ReasoningMessageEnd{Base: b(typ), MessageID: id}
		case TypeToolCallStart:
			return &ToolCallStart{Base: b(typ), ToolCallID: id}
		case TypeToolCallArgs:
			return &ToolCallArgs{Base: b(typ), ToolCallID: id}
		case TypeToolCallEnd:
			return &ToolCallEnd{Base: b(typ), ToolCallID: id}
		}
		return &TextMessageEnd{Base: b(typ), MessageID: id}
	}
	custom := &Resume{Base: b(TypeCustom), Name: NameResume}
	good := []Event{started("1"), text(TypeTextMessageStart, "m"), text(TypeTextMessageContent, "m"), text(TypeTextMessageEnd, "m"),
		text(TypeReasoningMessageStart, "r"), text(TypeReasoningMessageEnd, "r"), text(TypeToolCallStart, "c"), text(TypeToolCallArgs, "c"),
		text(TypeToolCallEnd, "c"), custom, finished, started("2"), text(TypeTextMessageStart, "m"), failed, started("3"), finished}
	if err := CheckOrder(good); err != nil {
		t.Fatal(err)
	}
	for want, evs := range map[string][]Event{
		"no run is open":                     {text(TypeTextMessageStart, "m")},
		"(RUN_ERROR): no run is open":        {failed},
		"(RUN_ERROR): no run":                {started("1"), failed, failed},
		"(CUSTOM): no run is open":           {started("1"), finished, custom},
		"(RUN_STARTED): a run is still open": {started("1"), started("2")},
		"text messages are still open: [m]":  {started("1"), text(TypeTextMessageStart, "m"), finished},
		"reasoning messages are still open":  {started("1"), text(TypeReasoningMessageStart, "r"), finished},
		"tool calls are still open: [c]":     {started("1"), text(TypeToolCallStart, "c"), finished},
		`text message "m" is not open`:       {started("1"), text(TypeTextMessageStart, "x"), text(TypeTextMessageContent, "m")},
		`reasoning message "r" is not open`:  {started("1"), text(TypeReasoningMessageContent, "r")},
		`tool call "c" is not open`:          {started("1"), text(TypeToolCallStart, "c"), text(TypeToolCallEnd, "c"), text(TypeToolCallArgs, "c")},
		`"m" is started again`:               {started("1"), text(TypeTextMessageStart, "m"), text(TypeTextMessageStart, "m")},
	} {
		if err := CheckOrder(evs); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q, got %v", want, err)
		}
	}
}

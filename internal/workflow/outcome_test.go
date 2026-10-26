package workflow

import "testing"

func TestTurnFailedIsOneRuleForTheChatAndTheActivity(t *testing.T) {
	warn := ErrorChunk{Message: "the model is not offered", Code: CodeSettings}
	drift := ErrorChunk{Message: "unknown event", Code: CodeAdapterDrift, Detail: "x"}
	hard := ErrorChunk{Message: "API error: overloaded"}
	fatal := ErrorChunk{Message: "the process died", Fatal: true}
	cases := []struct {
		name     string
		stop     string
		answered bool
		errs     []ErrorChunk
		failed   bool
		reason   string
	}{
		{"a plain answer", "end_turn", true, nil, false, ""},
		{"an error stop reason fails even after an answer", "error_max_turns", true, nil, true, "the turn ended with error_max_turns"},
		{"an error stop reason names the last uncoded error", "error_during_execution", true, []ErrorChunk{hard}, true, "API error: overloaded"},
		{"the ACP prompt failure", "error", false, []ErrorChunk{{Message: "prompt failed: Internal error"}}, true, "prompt failed: Internal error"},
		{"an error without an answer and without end_turn fails", "max_tokens", false, []ErrorChunk{hard}, true, "API error: overloaded"},
		{"an error with an answer and a plain stop reason is a warning", "max_tokens", true, []ErrorChunk{hard}, false, ""},
		{"an error before an end_turn answer is a warning", "end_turn", false, []ErrorChunk{hard}, false, ""},
		{"a settings warning never fails a tool-only turn", "end_turn", false, []ErrorChunk{warn}, false, ""},
		{"a settings warning never fails a turn with no stop reason", "", false, []ErrorChunk{warn}, false, ""},
		{"adapter drift never fails a turn", "max_tokens", false, []ErrorChunk{drift}, false, ""},
		{"coded warnings are not the reason", "error", false, []ErrorChunk{hard, warn, drift}, true, "API error: overloaded"},
		{"a fatal error is the run's own end, not this rule's", "", false, []ErrorChunk{fatal}, false, ""},
		{"no stop reason and no error", "", false, nil, false, ""},
	}
	for _, c := range cases {
		failed, reason := TurnFailed(c.stop, c.answered, c.errs)
		if failed != c.failed || reason != c.reason {
			t.Errorf("%s: TurnFailed(%q, %v) = %v %q, want %v %q", c.name, c.stop, c.answered, failed, reason, c.failed, c.reason)
		}
	}
}

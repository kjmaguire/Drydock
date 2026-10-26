package chats

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/workflow"
)

// How a turn ended is decided by the one rule the Activity reads too (workflow.TurnFailed), after the person's stop and the
// turn's own failures: a settings warning never fails a turn, an error_* stop reason fails it even after an answer, and a
// cancelled turn is stopped, not failed (AG-UI review 2026-10-25).
func TestTheChatJudgesATurnsEndByTheSameRuleAsTheActivity(t *testing.T) {
	warning := workflow.ErrorChunk{Message: `effort "high" was requested but this agent does not offer it`, Code: workflow.CodeSettings}
	apiErr := workflow.ErrorChunk{Message: "API Error: 529 Overloaded"}
	cases := []struct {
		name     string
		o        attemptOut
		cause    error
		answered bool
		status   string
		errMsg   string
		fromMsgs bool
	}{
		{"a tool-only turn with a settings warning that ran out of turns is done", attemptOut{stop: "max_turn_requests", errs: []workflow.ErrorChunk{warning}}, nil, false, TurnDone, "", false},
		{"an answered turn whose API then failed is an error with the API's words", attemptOut{stop: "error_api_error", errs: []workflow.ErrorChunk{apiErr}}, nil, true, TurnError, apiErr.Message, true},
		{"an error stop without an error chunk says what the stop was", attemptOut{stop: "error_max_turns"}, nil, true, TurnError, "the turn ended with error_max_turns", false},
		{"an error before any answer that did not end the turn properly", attemptOut{stop: "tool_use", errs: []workflow.ErrorChunk{apiErr}}, nil, false, TurnError, apiErr.Message, true},
		{"an error before an answer that still ended with end_turn is done", attemptOut{stop: "end_turn", errs: []workflow.ErrorChunk{apiErr}}, nil, false, TurnDone, "", false},
		{"a warning beside the agent's error is not the reason", attemptOut{stop: "error", errs: []workflow.ErrorChunk{apiErr, warning}}, nil, false, TurnError, apiErr.Message, true},
		{"a cancelled turn is stopped whatever else happened", attemptOut{stop: "cancelled", errs: []workflow.ErrorChunk{apiErr}}, nil, false, TurnStopped, "", false},
		{"an interrupted turn is stopped", attemptOut{stop: "interrupted"}, nil, true, TurnStopped, "", false},
		{"the person's stop wins", attemptOut{stop: "error"}, errStopped, false, TurnStopped, "", false},
		{"the app's shutdown is the error", attemptOut{stop: "end_turn"}, errShutdown, true, TurnError, errShutdown.Error(), false},
		{"a failure to start is the error", attemptOut{err: errors.New("spawn: no such file")}, nil, false, TurnError, "spawn: no such file", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, errMsg, fromMsgs := tc.o.outcome(tc.cause, tc.answered)
			if status != tc.status || errMsg != tc.errMsg || fromMsgs != tc.fromMsgs {
				t.Fatalf("got %s %q fromMsgs=%v, want %s %q fromMsgs=%v", status, errMsg, fromMsgs, tc.status, tc.errMsg, tc.fromMsgs)
			}
		})
	}
	// a live context has no cause: the agent's own outcome decides
	ctx, cancel := context.WithCancelCause(context.Background())
	if status, _, _ := (attemptOut{stop: "end_turn"}).outcome(context.Cause(ctx), true); status != TurnDone {
		t.Fatalf("live context: %s", status)
	}
	cancel(errStopped)
	if status, _, _ := (attemptOut{stop: "end_turn"}).outcome(context.Cause(ctx), true); status != TurnStopped {
		t.Fatalf("stopped context: %s", status)
	}
}

// A settings warning at the start of a resumed session is not a failure to resume: only the agent's own errors, a failure to
// start or a stop reason other than end_turn before any session or output are.
func TestASettingsWarningIsNotAFailureToResume(t *testing.T) {
	warning := workflow.ErrorChunk{Message: "model was requested but this agent has no model choice", Code: workflow.CodeSettings}
	if (attemptOut{stop: "end_turn", errs: []workflow.ErrorChunk{warning}}).resumeFailed() {
		t.Fatal("a coded warning counted as a failure to resume")
	}
	failed := attemptOut{stop: "end_turn", errs: []workflow.ErrorChunk{warning, {Message: "No conversation found with session ID"}}}
	if !failed.resumeFailed() || !strings.Contains(failed.reason(), "No conversation found") || strings.Contains(failed.reason(), "model choice") {
		t.Fatalf("resumeFailed=%v reason=%q", failed.resumeFailed(), failed.reason())
	}
	if (attemptOut{stop: "end_turn", sawOutput: true, errs: []workflow.ErrorChunk{{Message: "late error"}}}).resumeFailed() {
		t.Fatal("an attempt that produced output did resume")
	}
}

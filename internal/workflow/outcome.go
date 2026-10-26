package workflow

import "strings"

// CodeSettings marks an adapter's warning that a requested setting (a model, an effort, a permission mode, an MCP server) is
// not one the agent offers, so the agent's own default is in use. The turn goes on; the warning is never why it failed.
const CodeSettings = "settings"

// TurnFailed is the one rule for whether a turn failed, read the same way by the chat (internal/chats) and the Activity
// (internal/agui), so one turn is never told two ways (AG-UI review 2026-10-25). stop is the done chunk's stop reason,
// answered whether the agent gave an answer (an assistant message with text), errs the turn's non-fatal error chunks in order.
//
// A turn failed when the adapter said so with its stop reason ("error", or "error_*" such as Claude Code's error_max_turns
// and error_during_execution), whether or not the agent had answered first; and when it carried an error, gave no answer and
// did not end with end_turn. Warnings that carry a Code (adapter drift, a setting the agent does not offer) never fail a turn
// and are never its reason. The reason is the turn's last uncoded error, else "the turn ended with <stop>". A cancelled or
// interrupted turn is not a failure; callers read those stop reasons before this.
func TurnFailed(stop string, answered bool, errs []ErrorChunk) (failed bool, reason string) {
	last := ""
	for _, e := range errs {
		if e.Code == "" && !e.Fatal && strings.TrimSpace(e.Message) != "" {
			last = strings.TrimSpace(e.Message)
		}
	}
	switch {
	case stop == "error" || strings.HasPrefix(stop, "error_"):
		if last == "" {
			last = "the turn ended with " + stop
		}
		return true, last
	case last != "" && !answered && stop != "end_turn":
		return true, last
	}
	return false, ""
}

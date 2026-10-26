package agui

import (
	"fmt"
	"sort"
)

// CheckOrder applies the ordering rules of AG-UI's own event verifier (verify/verify.ts of the TypeScript client, @ag-ui/client
// 1.0.3) to one thread's events and returns the first event that breaks them. A projected thread keeps them, so a consumer
// that verifies AG-UI streams would accept it:
//
//   - a run's first event is RUN_STARTED, and RUN_STARTED does not come while a run is open;
//   - nothing but RUN_STARTED comes after RUN_FINISHED or RUN_ERROR (a run that ended is over; the next one starts anew), so
//     RUN_ERROR, like RUN_FINISHED, ends an open run and is sent once;
//   - the text messages, reasoning messages and tool calls a run started are ended before its RUN_FINISHED;
//   - a message's content and end, and a call's arguments and end, come only between the START and the END of the same id,
//     and an id is not started again while it is open.
//
// The verifier itself lets a stream open with RUN_ERROR and send RUN_ERROR after RUN_FINISHED; Drydock is stricter and sends
// RUN_ERROR only while a run is open.
func CheckOrder(events []Event) error {
	inRun := false
	var text, reasoning, calls map[string]bool
	reset := func() { text, reasoning, calls = map[string]bool{}, map[string]bool{}, map[string]bool{} }
	reset()
	open := func(set map[string]bool, kind, id string) error {
		if set[id] {
			return fmt.Errorf("%s %q is started again while it is open", kind, id)
		}
		set[id] = true
		return nil
	}
	within := func(set map[string]bool, kind, id string, end bool) error {
		if !set[id] {
			return fmt.Errorf("%s %q is not open", kind, id)
		}
		if end {
			delete(set, id)
		}
		return nil
	}
	for i, e := range events {
		typ := e.EventType()
		var err error
		switch {
		case typ == TypeRunStarted:
			if inRun {
				err = fmt.Errorf("a run is still open")
			}
			inRun = true
			reset()
		case !inRun:
			err = fmt.Errorf("no run is open (the first event of a run is %s)", TypeRunStarted)
		}
		if err == nil {
			switch x := e.(type) {
			case *RunFinished:
				switch {
				case len(text) > 0:
					err = fmt.Errorf("text messages are still open: %v", keys(text))
				case len(reasoning) > 0:
					err = fmt.Errorf("reasoning messages are still open: %v", keys(reasoning))
				case len(calls) > 0:
					err = fmt.Errorf("tool calls are still open: %v", keys(calls))
				}
				inRun = false
			case *RunError:
				inRun = false
			case *TextMessageStart:
				err = open(text, "text message", x.MessageID)
			case *TextMessageContent:
				err = within(text, "text message", x.MessageID, false)
			case *TextMessageEnd:
				err = within(text, "text message", x.MessageID, true)
			case *ReasoningMessageStart:
				err = open(reasoning, "reasoning message", x.MessageID)
			case *ReasoningMessageContent:
				err = within(reasoning, "reasoning message", x.MessageID, false)
			case *ReasoningMessageEnd:
				err = within(reasoning, "reasoning message", x.MessageID, true)
			case *ToolCallStart:
				err = open(calls, "tool call", x.ToolCallID)
			case *ToolCallArgs:
				err = within(calls, "tool call", x.ToolCallID, false)
			case *ToolCallEnd:
				err = within(calls, "tool call", x.ToolCallID, true)
			}
		}
		if err != nil {
			return fmt.Errorf("event %d (%s): %w", i, typ, err)
		}
	}
	return nil
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

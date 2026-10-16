package core

import (
	"strings"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/redact"
)

// redactedEvents are the families of event whose data can hold what an agent, a tool or a remote service printed: an agent's
// streamed output and failures (a stack trace, stderr), a workflow node's failure, a check's output, a task's feedback, an MCP
// call, a GitHub error. The rest are Drydock's own facts (ids, counts, states) and are not scanned.
var redactedEvents = []string{"agent.", "workflow.", "check.", "task.", "mcp.", "github.", "pipeline.", "watchdog.", "lead."}

// eventRedactor is the function the event bus applies to every event before it is stored and delivered (SQLBus.SetRedactor).
func eventRedactor(f func(string) string) func(events.Event) events.Event {
	return func(e events.Event) events.Event {
		for _, p := range redactedEvents {
			if strings.HasPrefix(e.Type, p) {
				e.Data = redact.JSON(e.Data, f)
				break
			}
		}
		return e
	}
}

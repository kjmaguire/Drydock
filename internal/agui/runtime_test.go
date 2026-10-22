package agui

import (
	"encoding/json"
	"testing"

	"github.com/kjmaguire/drydock/internal/workflow"
)

// What a process ran as (agent.started: the CLI, its model, the WSL distribution) is on its thread, so the activity can say
// "Builder · Claude Code · sonnet"; the event adds nothing to the stream, which keeps AG-UI's order.
func TestAThreadSaysWhatItsProcessRanAs(t *testing.T) {
	var s stream
	s.add("agent.started", "r1", map[string]any{"pid": 7, "adapter": "claude", "model": "sonnet", "runMode": "wsl", "wslDistro": "Ubuntu"})
	s.add("agent.waiting", "r1", map[string]any{"state": "working"})
	s.out("r1", workflow.ChunkMessageEnd, workflow.TextDelta{Text: "Done."})
	s.add("agent.completed", "r1", map[string]any{"stopReason": "end_turn"})
	s.add("agent.started", "r2", map[string]any{"adapter": "codex", "model": "gpt-5"})
	s.add("agent.waiting", "r2", map[string]any{"state": "working"})
	s.add("agent.stopped", "r2", map[string]any{})

	a := project(t, s.evs)
	if len(a.Threads) != 2 {
		t.Fatalf("threads %+v", a.Threads)
	}
	t1, t2 := a.Threads[0], a.Threads[1]
	if t1.Adapter != "claude" || t1.Model != "sonnet" || t1.RunMode != "wsl" || t1.WSLDistro != "Ubuntu" || t2.Adapter != "codex" || t2.Model != "gpt-5" || t2.RunMode != "" {
		t.Fatalf("threads %+v / %+v", t1, t2)
	}
	eq(t, types(t1), []string{"RUN_STARTED r1:1", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT Done.", "TEXT_MESSAGE_END", "RUN_FINISHED r1:1 success end_turn"})
	b, _ := json.Marshal(t2)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["adapter"] != "codex" || m["model"] != "gpt-5" {
		t.Fatalf("thread JSON %s", b)
	}
	if _, ok := m["wslDistro"]; ok {
		t.Fatalf("a native process has no distro: %s", b)
	}
}

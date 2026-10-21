package acp_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
)

// ACP has no system-prompt channel, so the standing instructions (RunSpec.SystemAppend) go in front of the first prompt of
// the session, once.
func TestSystemAppendIsPutInFrontOfTheFirstPromptOnly(t *testing.T) {
	ad := fakeAdapter(t, fakeacp.Script{
		Rules:   []fakeacp.Rule{{Match: "Own the payments service", Steps: []fakeacp.Step{{Say: "saw the purpose"}}}},
		Default: []fakeacp.Step{{Say: "plain"}},
	})
	s := startSession(t, ad, agents.RunSpec{AgentID: "a1", SystemAppend: "Own the payments service."})
	if got := text(turn(t, s, "do the task")); !strings.Contains(got, "saw the purpose") {
		t.Fatalf("first turn did not carry the instructions: %q", got)
	}
	if got := text(turn(t, s, "again")); !strings.Contains(got, "plain") || strings.Contains(got, "saw the purpose") {
		t.Fatalf("second turn repeated the instructions: %q", got)
	}
}

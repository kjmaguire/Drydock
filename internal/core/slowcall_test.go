package core_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
)

// A call that takes longer than the threshold is logged by its name and time, never with its parameters; a quick one is not.
func TestASlowCallIsLoggedByNameOnly(t *testing.T) {
	a := openScripted(t, nil, fakeagent.Script{})
	stderr := captureStderr(t)
	a.call("events.head", nil, nil)
	defer core.SetSlowCallForTests(0)()
	a.call("project.list", map[string]any{"marker": "do-not-log-me"}, nil)
	out := stderr()
	if strings.Contains(out, "rpc events.head") {
		t.Fatalf("a quick call was logged: %q", out)
	}
	if !strings.Contains(out, "rpc project.list took ") || strings.Contains(out, "do-not-log-me") {
		t.Fatalf("the slow call's line: %q", out)
	}
}

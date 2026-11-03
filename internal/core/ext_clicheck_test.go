package core_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/connections"
	"github.com/kjmaguire/drydock/internal/core"
)

// Drydock checks the agent CLIs itself (A2): a little after start, without any page asking, the CLIs are checked, the answers
// are kept and connection.list {cached} hands them out at once with when each was made, and connection.checked says so; a
// sign-in that ended and an agent that could not start ask for another check, a failure during a run does not.
func TestDrydockChecksTheCLIsItself(t *testing.T) {
	defer core.SetCLICheckFirst(50 * time.Millisecond)()
	a := openWith(t, nil)
	a.waitFor("the first check", func() bool { return len(a.eventsOf("connection.checked")) == 1 })
	ev := a.eventsOf("connection.checked")[0]
	if ev["why"] != "start" {
		t.Fatalf("first check %v", ev)
	}
	var cached []connections.Checked
	a.call("connection.list", map[string]bool{"cached": true}, &cached)
	if len(cached) == 0 {
		t.Fatal("nothing kept from the check")
	}
	seen := map[string]bool{}
	for _, c := range cached {
		if c.At.IsZero() || c.Key == "" {
			t.Fatalf("cached row without its time or key: %+v", c)
		}
		seen[c.Key] = true
	}
	if !seen["claude"] {
		t.Fatalf("claude was not checked: %v", seen)
	}
	for i := 1; i < len(cached); i++ {
		if cached[i-1].Key > cached[i].Key {
			t.Fatalf("not in key order: %s before %s", cached[i-1].Key, cached[i].Key)
		}
	}

	publish := func(typ string, data map[string]any) {
		b, _ := json.Marshal(data)
		a.c.TestPublish(typ, b)
	}
	publish("agent.failed", map[string]any{"phase": "run", "error": "exit 1"})
	publish("agent.failed", map[string]any{"phase": "start", "error": "not found"})
	a.waitFor("a check after the failed start", func() bool { return len(a.eventsOf("connection.checked")) == 2 })
	if got := a.eventsOf("connection.checked")[1]["why"]; got != "agent_failed" {
		t.Fatalf("second check because %v", got)
	}
	publish("connection.sign_in_finished", map[string]any{"key": "claude", "exitCode": 0})
	a.waitFor("a check after the sign-in", func() bool { return len(a.eventsOf("connection.checked")) == 3 })
	time.Sleep(300 * time.Millisecond)
	if n := len(a.eventsOf("connection.checked")); n != 3 {
		t.Fatalf("%d checks: a failure during a run must not ask for one", n)
	}
}

// Before anything uses a CLI every CLI Drydock knows is checked; once agents exist only theirs are.
func TestCLICheckCoversTheCLIsInUse(t *testing.T) {
	a := openWith(t, nil)
	all := a.c.CheckCLIs(t.Context(), "test")
	if len(all) < 2 {
		t.Fatalf("a first run checks every CLI: %d", len(all))
	}
	p, _ := a.setup("inuse")
	a.agent(p.ID, "Wren", "backend")
	used := a.c.CheckCLIs(t.Context(), "test")
	if len(used) != 1 || used[0].Adapter != "claude" {
		t.Fatalf("in use: %+v", used)
	}
}

// An agent that runs its CLI inside a WSL distro (P58) has that install checked in the background too: the Inbox reads the
// distro's Connection (claude@wsl:Ubuntu), so checking only the Windows claude would leave it unseen.
func TestCLICheckCoversWSLInstallsInUse(t *testing.T) {
	a := openWith(t, nil)
	p, _ := a.setup("wslinuse")
	ag := a.agent(p.ID, "Wren", "backend")
	if _, err := a.c.DB.Writer().Exec(`UPDATE agents SET run_mode='wsl', wsl_distro='Ubuntu' WHERE id=?`, ag.ID); err != nil {
		t.Fatal(err)
	}
	used := a.c.CheckCLIs(t.Context(), "test")
	keys := map[string]bool{}
	for _, c := range used {
		keys[c.Key] = true
	}
	if !keys[connections.WSLKey("claude", "Ubuntu")] || keys["claude"] || len(used) != 1 {
		t.Fatalf("checked %v, want only the install inside Ubuntu", keys)
	}
	if _, ok := a.c.Connections.Last()[connections.WSLKey("claude", "Ubuntu")]; !ok {
		t.Fatal("the WSL check was not kept for the Inbox")
	}
}

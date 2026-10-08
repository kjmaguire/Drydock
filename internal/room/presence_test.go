package room

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
)

func ev(typ, agent string, data any) events.Event {
	b, _ := json.Marshal(data)
	return events.Event{Type: typ, AgentID: agent, Data: b, Time: time.Unix(1000, 0).UTC()}
}

func TestDeriveTable(t *testing.T) {
	type step struct {
		e        events.Event
		want     Presence
		label    string
		activity string // substring; "" = don't care
		applied  bool
	}
	out := func(kind string, data any) events.Event {
		return ev("agent.output", "a1", map[string]any{"kind": kind, "data": data})
	}
	cases := []struct {
		name  string
		steps []step
	}{
		{"startup then a turn", []step{
			{ev("agent.starting", "a1", nil), PresenceIdle, "starting", "starting", true},
			{ev("agent.started", "a1", map[string]any{"pid": 1}), PresenceIdle, "", "started", true},
			{ev("agent.waiting", "a1", map[string]any{"state": "working"}), PresenceThinking, "", "turn started", true},
			{out("thinking", map[string]any{"text": "hm"}), PresenceThinking, "", "thinking", true},
			{out("tool_call", map[string]any{"name": "Read"}), PresenceWorking, "", "tool: Read", true},
			{out("tool_result", map[string]any{"name": "Read"}), PresenceThinking, "", "after tool: Read", true},
			{out("text", map[string]any{"text": "writing"}), PresenceWorking, "", "writing", true},
			{out("usage", map[string]any{}), PresenceWorking, "", "writing", false},
			{ev("agent.completed", "a1", map[string]any{"stopReason": "end_turn"}), PresenceCompleted, "", "end_turn", true},
			{ev("agent.stopped", "a1", map[string]any{"exitCode": 0}), PresencePaused, "stopped", "stopped", true},
		}},
		{"failure survives the exit event", []step{
			{ev("agent.started", "a1", nil), PresenceIdle, "", "", true},
			{ev("agent.failed", "a1", map[string]any{"error": "exit 2: boom"}), PresenceFailed, "", "boom", true},
			{ev("agent.stopped", "a1", map[string]any{"exitCode": 2}), PresenceFailed, "", "boom", true},
			{out("text", map[string]any{"text": "late"}), PresenceFailed, "", "boom", false},
			{ev("agent.started", "a1", nil), PresenceIdle, "", "started", true},
		}},
		{"waiting and permission", []step{
			{ev("agent.waiting", "a1", map[string]any{"message": "turn grant"}), PresenceWaiting, "", "turn grant", true},
			{out("permission_request", map[string]any{}), PresenceWaiting, "", "permission", true},
			{ev("agent.waiting", "a1", map[string]any{"state": "working"}), PresenceThinking, "", "", true},
		}},
		{"watchdog", []step{
			{ev("agent.waiting", "a1", map[string]any{"state": "working"}), PresenceThinking, "", "", true},
			{ev("watchdog.warning", "a1", map[string]any{"signal": "stagnation"}), PresenceThinking, "", "watchdog warning: stagnation", true},
			{ev("watchdog.tripped", "a1", map[string]any{"signal": "tool_spin"}), PresenceThinking, "", "watchdog tripped: tool_spin", true},
			{ev("watchdog.paused", "a1", map[string]any{"signal": "tool_spin", "message": "paused the task"}), PresenceNeedsAttention, "watchdog", "paused the task", true},
			{out("text", map[string]any{}), PresenceNeedsAttention, "watchdog", "", false},
			{ev("watchdog.tripped", "a1", map[string]any{"signal": "x"}), PresenceNeedsAttention, "watchdog", "paused the task", false},
			{ev("agent.waiting", "a1", map[string]any{"state": "working"}), PresenceThinking, "", "turn started", true}, // a human resumed
		}},
		{"explicit states map per the doc", []step{
			{ev("agent.state_changed", "a1", map[string]any{"state": "STARTING"}), PresenceIdle, "starting", "", true},
			{ev("agent.state_changed", "a1", map[string]any{"state": "WORKING"}), PresenceWorking, "", "", true},
			{ev("agent.state_changed", "a1", map[string]any{"state": "WAITING"}), PresenceWaiting, "", "", true},
			{ev("agent.state_changed", "a1", map[string]any{"state": "NEEDS_ATTENTION"}), PresenceNeedsAttention, "", "", true},
			{ev("agent.state_changed", "a1", map[string]any{"state": "PAUSED"}), PresencePaused, "", "", true},
			{ev("agent.state_changed", "a1", map[string]any{"state": "STOPPED"}), PresencePaused, "stopped", "", true},
			{ev("agent.state_changed", "a1", map[string]any{"state": "IDLE"}), PresenceIdle, "", "", true},
			{ev("agent.state_changed", "a1", map[string]any{"state": "COMPLETED"}), PresenceCompleted, "", "", true},
			{ev("agent.state_changed", "a1", map[string]any{"state": "FAILED"}), PresenceFailed, "", "", true},
			{ev("agent.state_changed", "a1", map[string]any{"state": "BOGUS"}), PresenceFailed, "", "", false},
		}},
		{"stopped agent ignores late output", []step{
			{ev("agent.stopped", "a1", nil), PresencePaused, "stopped", "", true},
			{out("tool_call", map[string]any{"name": "Bash"}), PresencePaused, "stopped", "", false},
		}},
		{"unknown and unattributed events are ignored", []step{
			{ev("agent.mystery", "a1", nil), "", "", "", false},
			{ev("agent.started", "", nil), "", "", "", false},
			{ev("task.created", "a1", nil), "", "", "", false},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var st PresenceState
			for i, s := range c.steps {
				next, ok := Derive(st, s.e)
				if ok != s.applied {
					t.Fatalf("step %d (%s): applied=%v want %v", i, s.e.Type, ok, s.applied)
				}
				st = next
				if st.Presence != s.want || st.Label != s.label || (s.activity != "" && !contains(st.Activity, s.activity)) {
					t.Fatalf("step %d (%s): got %s/%q/%q want %s/%q/%q", i, s.e.Type, st.Presence, st.Label, st.Activity, s.want, s.label, s.activity)
				}
			}
		})
	}
}

func contains(s, sub string) bool { return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestDeriveSinceOnlyMovesWhenPresenceChanges(t *testing.T) {
	e1 := ev("agent.waiting", "a1", map[string]any{"state": "working"})
	e1.Time = time.Unix(10, 0)
	e2 := ev("agent.output", "a1", map[string]any{"kind": "thinking"})
	e2.Time = time.Unix(20, 0)
	e3 := ev("agent.output", "a1", map[string]any{"kind": "text"})
	e3.Time = time.Unix(30, 0)
	s1, _ := Derive(PresenceState{}, e1)
	s2, _ := Derive(s1, e2)
	s3, _ := Derive(s2, e3)
	if !s2.Since.Equal(time.Unix(10, 0)) || !s3.Since.Equal(time.Unix(30, 0)) || !s3.Updated.Equal(time.Unix(30, 0)) {
		t.Fatalf("since: %v %v", s2.Since, s3.Since)
	}
}

func publish(t *testing.T, e *env, typ, agent string, data any) {
	t.Helper()
	b, _ := json.Marshal(data)
	if _, err := e.bus.Publish(e.ctx, events.Event{Type: typ, AgentID: agent, ProjectID: e.project, Data: b}); err != nil {
		t.Fatal(err)
	}
}

func TestTrackerRebuildsFromEventsAfterRestart(t *testing.T) {
	e := newEnv(t)
	// events written while no tracker runs (the app was down, or this is a restart)
	publish(t, e, "agent.started", "a1", map[string]any{"pid": 1})
	publish(t, e, "agent.waiting", "a1", map[string]any{"state": "working"})
	publish(t, e, "agent.output", "a1", map[string]any{"kind": "tool_call", "data": map[string]any{"name": "Edit"}})
	publish(t, e, "agent.started", "a2", nil)
	publish(t, e, "agent.failed", "a2", map[string]any{"error": "crash"})
	publish(t, e, "agent.stopped", "a2", nil)
	publish(t, e, "agent.started", "a3", nil)
	publish(t, e, "agent.completed", "a3", map[string]any{"stopReason": "end_turn"})
	publish(t, e, "task.created", "a1", nil) // not a presence event

	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	tr := NewTracker(e.bus, TrackerOptions{})
	if err := tr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	want := map[string]Presence{"a1": PresenceWorking, "a2": PresenceFailed, "a3": PresenceCompleted}
	snap := tr.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("%+v", snap)
	}
	for _, s := range snap {
		if s.Presence != want[s.AgentID] {
			t.Fatalf("%s: %s want %s", s.AgentID, s.Presence, want[s.AgentID])
		}
	}
	if a1, _ := tr.Get("a1"); a1.Activity != "tool: Edit" || a1.ProjectID != e.project {
		t.Fatalf("%+v", a1)
	}

	// live events after the rebuild
	publish(t, e, "agent.output", "a1", map[string]any{"kind": "text", "data": map[string]any{"text": "x"}})
	publish(t, e, "agent.paused", "a3", nil)
	eventually(t, 2*time.Second, func() bool {
		a1, _ := tr.Get("a1")
		a3, _ := tr.Get("a3")
		return a1.Activity == "writing" && a3.Presence == PresencePaused
	})

	// a second tracker (a restart) over the same log lands on the same view; replay and live overlap is harmless
	tr2 := NewTracker(e.bus, TrackerOptions{})
	if err := tr2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, 2*time.Second, func() bool { return tr2.LastSeq() == tr.LastSeq() })
	s1, s2 := tr.Snapshot(), tr2.Snapshot()
	if len(s1) != len(s2) {
		t.Fatal("snapshot sizes differ")
	}
	for i := range s1 {
		if s1[i] != s2[i] {
			t.Fatalf("rebuild differs for %s:\n%+v\n%+v", s1[i].AgentID, s1[i], s2[i])
		}
	}
}

func TestTrackerAliveCheckAfterRestart(t *testing.T) {
	e := newEnv(t)
	publish(t, e, "agent.started", "alive", nil)
	publish(t, e, "agent.waiting", "alive", map[string]any{"state": "working"})
	publish(t, e, "agent.started", "dead", nil)
	publish(t, e, "agent.waiting", "dead", map[string]any{"state": "working"})
	publish(t, e, "agent.started", "broken", nil)
	publish(t, e, "agent.failed", "broken", map[string]any{"error": "x"})
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	tr := NewTracker(e.bus, TrackerOptions{Alive: func(id string) bool { return id == "alive" }})
	if err := tr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if s, _ := tr.Get("alive"); s.Presence != PresenceThinking {
		t.Fatalf("alive: %+v", s)
	}
	if s, _ := tr.Get("dead"); s.Presence != PresencePaused || s.Label != "stopped" {
		t.Fatalf("dead: %+v", s)
	}
	if s, _ := tr.Get("broken"); s.Presence != PresenceFailed {
		t.Fatalf("a failure is not downgraded to stopped: %+v", s)
	}
}

func TestTrackerWatchAndCoalescedEmission(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	tr := NewTracker(e.bus, TrackerOptions{EmitEvery: 60 * time.Millisecond})
	if err := tr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	ch, stop := tr.Watch()
	defer stop()

	publish(t, e, "agent.started", "a1", nil)
	for i := 0; i < 50; i++ { // a busy agent: many changes inside one interval
		kind := "text"
		if i%2 == 1 {
			kind = "thinking"
		}
		publish(t, e, "agent.output", "a1", map[string]any{"kind": kind})
	}
	got := 0
	deadline := time.After(2 * time.Second)
loop:
	for {
		select {
		case s := <-ch:
			got++
			if s.AgentID != "a1" {
				t.Fatal(s)
			}
			if tr.LastSeq() >= 51 && s.Seq >= 51 {
				break loop
			}
		case <-deadline:
			t.Fatal("watch timed out")
		}
	}
	if got < 2 {
		t.Fatalf("watch saw %d changes", got)
	}
	time.Sleep(200 * time.Millisecond) // let the emitter run a few ticks
	emitted := e.events("room.presence_changed")
	if len(emitted) == 0 || len(emitted) > 4 {
		t.Fatalf("expected a few coalesced room.presence_changed events for 51 changes, got %d", len(emitted))
	}
	last := emitted[len(emitted)-1]
	var d struct{ Member, Presence string }
	json.Unmarshal(last.Data, &d)
	if d.Member != "a1" || d.Presence != string(PresenceWorking) && d.Presence != string(PresenceThinking) {
		t.Fatalf("%s", last.Data)
	}
}

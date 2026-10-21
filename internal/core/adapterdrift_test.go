package core_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/connections"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// An agent CLI that sends an event type Drydock does not read raises adapter.drift once per type, the Connection shows
// degraded (still usable) with reason drift:schema while it is recorded, the record survives a restart, and a passing Test
// connection clears it.
// The agent is the labelled scripted test double (fakeagent), whose raw step sends the unknown event.
func TestAnUnknownCLIEventRaisesAdapterDriftAndDegradesTheConnectionUntilATestPasses(t *testing.T) {
	drift := []fakeagent.Step{{Raw: json.RawMessage(`{"type":"brand_new_event","x":1}`)}, {Raw: json.RawMessage(`{"type":"brand_new_event","x":2}`)}}
	a, prompts := openDev(t, fakeagent.Script{
		Rules:   []fakeagent.Rule{{Match: "independent reviewer", Steps: append(drift, fakeagent.Step{Say: `{"verdict":"SHIP","feedback":""}`})}},
		Default: append(drift, fakeagent.Step{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, fakeagent.Step{Say: "implemented Subtract"}),
	})
	dir := filepath.Join(filepath.Dir(prompts), "data")
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &r)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "x"}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	a.waitFor("adapter.drift", func() bool {
		var n int
		_ = a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM events WHERE type='adapter.drift'`).Scan(&n)
		return n > 0
	})
	var data string
	if err := a.c.DB.Reader().QueryRow(`SELECT data FROM events WHERE type='adapter.drift'`).Scan(&data); err != nil ||
		!strings.Contains(data, `"brand_new_event"`) || !strings.Contains(data, `"schema"`) || !strings.Contains(data, `"claude"`) {
		t.Fatalf("event %s %v", data, err)
	}
	// the worker and the reviewer each met the type: still one event
	a.waitFor("the review", func() bool {
		var d core.TaskDetail
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		return len(d.Pending) == 1
	})
	var n int
	_ = a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM events WHERE type='adapter.drift'`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d drift events for one type", n)
	}
	if d, ok := a.c.Connections.Drift("claude"); !ok || d.Kind != "schema" || len(d.Types) != 1 || d.Types[0] != "brand_new_event" {
		t.Fatalf("drift record %+v %v", d, ok)
	}
	// kept across a restart (the Connection's degraded state from it: TestDriftDegradesUntilCleared in internal/connections)
	a.c.Close()
	b := open(t, dir)
	if d, ok := b.c.Connections.Drift("claude"); !ok || d.Types[0] != "brand_new_event" {
		t.Fatalf("after a restart: %+v %v", d, ok)
	}
	// a passing Test connection clears it
	b.c.Connections.RecordTest("claude", connections.Report{OK: true})
	if _, ok := b.c.Connections.Drift("claude"); ok {
		t.Fatal("a passing Test left the drift")
	}
}

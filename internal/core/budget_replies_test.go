package core_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/room"
)

// A project budget at its hard limit stops agent replies in the room too, not only workflow turns: the room says which
// budget and what to do, and the agent does not run. Raising the limit lets the next mention through.
func TestABudgetAtItsLimitStopsRoomReplies(t *testing.T) {
	a, _ := openDev(t, devScript())
	repo := newGreenRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	alice := a.agent(p.ID, "Alice", "backend")
	var rooms []room.Room
	a.call("room.list", map[string]any{"projectId": p.ID}, &rooms)
	general := ""
	for _, r := range rooms {
		if r.Name == "general" {
			general = r.ID
		}
	}
	msgs := func() []room.Message {
		var ms []room.Message
		a.call("room.messages", map[string]any{"roomId": general, "limit": 200}, &ms)
		return ms
	}
	// $0.24 already spent by the project's agent, against a $0.10 project budget
	if _, err := a.c.DB.Writer().Exec(`INSERT INTO model_usage(ts,task_id,agent_id,provider,model,tier,cost_usd,source,cost_source) VALUES(?, '', ?, 'anthropic','claude-haiku-5-5','premium',0.24,'reported','reported')`, time.Now().UnixMilli(), alice.ID); err != nil {
		t.Fatal(err)
	}
	var b struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
	}
	a.call("budget.set", map[string]any{"scope": "project", "scopeId": p.ID, "limitUsd": 0.10}, &b)
	a.call("room.post", map[string]any{"roomId": general, "body": "@" + alice.ID + " are you there?"}, nil)
	a.waitFor("the refusal in the room", func() bool {
		for _, m := range msgs() {
			if m.Type == room.TypeError && strings.Contains(m.Body, "Alice could not answer: budget:") && strings.Contains(m.Body, "Budgets page") {
				return true
			}
		}
		return false
	})
	for _, m := range msgs() {
		if m.Author.Key() == alice.ID && m.Type == room.TypeChat {
			t.Fatalf("Alice answered over budget: %q", m.Body)
		}
	}
	// the human raises the limit: the next mention is answered
	a.call("budget.set", map[string]any{"id": b.ID, "version": b.Version, "scope": "project", "scopeId": p.ID, "limitUsd": 10.0}, nil)
	a.call("room.post", map[string]any{"roomId": general, "body": "@" + alice.ID + " and now?"}, nil)
	a.waitFor("a reply once the budget is raised", func() bool {
		for _, m := range msgs() {
			if m.Author.Key() == alice.ID && m.Type == room.TypeChat {
				return true
			}
		}
		return false
	})
}

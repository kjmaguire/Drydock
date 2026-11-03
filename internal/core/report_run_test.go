package core_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// An Inbox item of an agent that failed its task names the task's latest workflow run (X56 with C6), so Report this opens the
// Support page with that run picked; an item of no task names none.
func TestAnAgentItemNamesItsTasksLatestRunForReportThis(t *testing.T) {
	a := open(t, "")
	p := gitProject(t, a, "gocalc")
	var w core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "W", Adapter: "claude", Role: "backend"}, &w)
	var tk tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Fetch the feed"}, &tk)
	a.sql(`INSERT INTO workflow_runs(id,workflow,status,project_id,task_id,topology_hash,created_at,updated_at) VALUES('run_older','w','FAILED',?,?,'h',1,1)`, p.ID, tk.ID)
	a.sql(`INSERT INTO workflow_runs(id,workflow,status,project_id,task_id,topology_hash,created_at,updated_at) VALUES('run_newer','w','FAILED',?,?,'h',2,2)`, p.ID, tk.ID)
	data, _ := json.Marshal(map[string]any{"error": "gave up"})
	if _, err := a.c.Bus.Publish(context.Background(), events.Event{Type: "agent.failed", ProjectID: p.ID, TaskID: tk.ID, AgentID: w.ID, Data: data}); err != nil {
		t.Fatal(err)
	}
	a.waitFor("the agent item with its run", func() bool {
		it := agentItems(a, p.ID)
		return len(it) == 1 && it[0].TaskID == tk.ID && it[0].RunID == "run_newer"
	})
}

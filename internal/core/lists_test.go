package core_test

import (
	"fmt"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// Audit 2026-10-16, M21: the task list was capped at the newest 500 without a word, and the Agent Inspector's "spent in all"
// summed only the newest 50 usage rows. A caller can now ask how many tasks there are, and the totals are sums over every row.
func TestTheTaskListSaysHowManyThereAreAndTheAgentTotalsCoverEveryRow(t *testing.T) {
	a := open(t, "")
	p, _ := a.setup("gocalc")
	for i := 0; i < 7; i++ {
		var task tasks.Task
		a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: fmt.Sprintf("Task %d", i)}, &task)
	}
	var list []tasks.Task
	a.call("task.list", map[string]any{"projectId": p.ID, "limit": 3}, &list)
	var count struct{ Total int }
	a.call("task.count", map[string]any{"projectId": p.ID}, &count)
	if len(list) != 3 || count.Total != 7 {
		t.Fatalf("list %d, total %d", len(list), count.Total)
	}
	if list[0].Title != "Task 6" {
		t.Fatalf("the newest come first: %q", list[0].Title)
	}
	a.call("task.list", map[string]any{"projectId": p.ID}, &list)
	if len(list) != 7 {
		t.Fatalf("the default shows %d of 7", len(list))
	}
	a.call("task.count", map[string]any{"projectId": p.ID, "statuses": []string{"COMPLETED"}}, &count)
	if count.Total != 0 {
		t.Fatalf("a status filter is applied to the count: %d", count.Total)
	}
	a.call("task.list", map[string]any{"projectId": p.ID, "limit": 1000000}, &list) // above the ceiling: capped, not refused
	if len(list) != 7 {
		t.Fatalf("an oversized limit: %d", len(list))
	}

	var ag core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "W", Adapter: "claude", Role: "backend"}, &ag)
	for i := 0; i < 120; i++ {
		src := "reported"
		if i%4 == 0 {
			src = "estimated"
		}
		a.sql(`INSERT INTO model_usage(ts,agent_id,provider,model,input_tokens,output_tokens,cost_usd,source) VALUES(?,?,'p','m',10,5,0.5,?)`, int64(1000+i), ag.ID, src)
	}
	var d core.AgentDetail
	a.call("agent.get", map[string]string{"id": ag.ID}, &d)
	if len(d.Usage) != 50 {
		t.Fatalf("the list is the newest 50: %d", len(d.Usage))
	}
	if d.UsageTotals.Rows != 120 || d.UsageTotals.CostUSD != 60 || d.UsageTotals.InputTokens != 1200 || d.UsageTotals.OutputTokens != 600 || d.UsageTotals.Estimated != 30 {
		t.Fatalf("totals %+v must cover all 120 rows", d.UsageTotals)
	}
}

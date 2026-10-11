package core

import (
	"context"
	"testing"
	"time"
)

// Agent output older than the retention period is deleted; newer output and every other event stay.
func TestRetentionDeletesOnlyOldAgentOutput(t *testing.T) {
	ctx := context.Background()
	c, err := Open(ctx, Config{DataDir: t.TempDir(), User: "kyle"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	now := time.Now()
	old, recent := now.Add(-agentOutputRetention-time.Hour).UnixMilli(), now.Add(-time.Hour).UnixMilli()
	for _, row := range []struct {
		ts  int64
		typ string
	}{{old, "agent.output"}, {old, "agent.output"}, {old, "task.completed"}, {recent, "agent.output"}} {
		if _, err := c.DB.Writer().ExecContext(ctx, `INSERT INTO events(ts,type,project_id,run_id,task_id,agent_id,data) VALUES(?,?,?,?,?,?,'{}')`, row.ts, row.typ, "p", "", "t", ""); err != nil {
			t.Fatal(err)
		}
	}
	n, err := c.pruneAgentOutput(ctx, now)
	if err != nil || n != 2 {
		t.Fatalf("deleted %d (%v), want the 2 old agent outputs", n, err)
	}
	count := func(q string) int {
		var k int
		_ = c.DB.Reader().QueryRowContext(ctx, q).Scan(&k)
		return k
	}
	if count(`SELECT COUNT(*) FROM events WHERE type='task.completed' AND task_id='t'`) != 1 || count(`SELECT COUNT(*) FROM events WHERE type='agent.output' AND task_id='t'`) != 1 {
		t.Fatal("retention deleted more than old agent output")
	}
}

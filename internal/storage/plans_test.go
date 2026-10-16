package storage

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// Audit 2026-10-16, M12: SQLite uses a partial index ("WHERE run_id <> ”") for a query only when it can prove the query's WHERE
// implies the index's, and it cannot prove that "run_id = ?" means "run_id <> ”". The lookups the application makes by run,
// task, project, agent, decision and room scanned their tables. These are the production queries; each must use the index that
// was made for it. A new query on a partial index must carry the index's own condition, and be added here.
func TestLookupsUseTheirIndexesInsteadOfScanningTheTable(t *testing.T) {
	db, err := OpenTemp(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cases := []struct {
		name  string
		query string
		args  []any
		index string
	}{
		{"events of a run", `SELECT data FROM events WHERE run_id=? ORDER BY seq DESC LIMIT 100`, []any{"r"}, "events_run"},
		{"events of a task", `SELECT data FROM events WHERE task_id=? AND type='agent.output' ORDER BY seq DESC LIMIT 400`, []any{"t"}, "events_task"},
		{"events of a task after a cursor", `SELECT seq FROM events WHERE task_id=? AND seq>? ORDER BY seq`, []any{"t", 5}, "events_task"},
		{"events of a project after a cursor", `SELECT seq FROM events WHERE project_id=? AND seq>? ORDER BY seq LIMIT 100`, []any{"p", 5}, "events_project"},
		{"usage rows of an agent", `SELECT id FROM model_usage WHERE agent_id=? ORDER BY id DESC LIMIT 50`, []any{"a"}, "model_usage_agent_id"},
		{"the totals of an agent", `SELECT COUNT(*), SUM(cost_usd) FROM model_usage WHERE agent_id=?`, []any{"a"}, "model_usage_agent_id"},
		{"agent runs of a task", `SELECT id FROM agent_runs WHERE task_id=?`, []any{"t"}, "agent_runs_task"},
		{"workflow runs of a task", `SELECT id FROM workflow_runs WHERE task_id=?`, []any{"t"}, "workflow_runs_task"},
		{"checkpoints that branch from one", `SELECT id FROM workflow_checkpoints WHERE parent_id=?`, []any{"c"}, "workflow_checkpoints_parent"},
		{"routing decisions of a task", `SELECT id FROM routing_decisions WHERE task_id=?`, []any{"t"}, "routing_decisions_task"},
		{"routing decisions of a run", `SELECT id FROM routing_decisions WHERE run_id=?`, []any{"r"}, "routing_decisions_run"},
		{"audit entries about a subject", `SELECT seq FROM audit_log WHERE subject=? ORDER BY seq`, []any{"s"}, "audit_log_subject"},
		{"messages of a thread", `SELECT id FROM messages WHERE thread_id=?`, []any{"m"}, "messages_thread"},
		{"replies to a message", `SELECT id FROM messages WHERE reply_to_id=?`, []any{"m"}, "messages_reply"},
		{"messages that supersede one", `SELECT id FROM messages WHERE supersedes_id=?`, []any{"m"}, "messages_supersede"},
		{"external items of a task", `SELECT id FROM external_items WHERE task_id=?`, []any{"t"}, "external_items_task"},
		// the unique partial indexes are constraints, so the lookups carry their condition
		{"a posted message by its idempotency key", `SELECT id FROM messages WHERE room_id=? AND idempotency_key<>'' AND idempotency_key=?`, []any{"r", "k"}, "messages_idem"},
		{"a message range of a room", `SELECT id FROM messages WHERE room_id=? AND seq>? AND seq>0 ORDER BY seq LIMIT ?`, []any{"r", 1, 10}, "messages_room_seq"},
		{"the room of a task", `SELECT id FROM rooms WHERE kind='task' AND task_id<>'' AND task_id=?`, []any{"t"}, "rooms_task"},
		{"a requirement by its source", `SELECT id FROM requirements WHERE project_id=? AND source_ref<>'' AND source_ref=?`, []any{"p", "s"}, "requirements_source"},
	}
	for _, c := range cases {
		rows, err := db.Reader().Query("EXPLAIN QUERY PLAN "+c.query, c.args...)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		joined := strings.Join(plan, " | ")
		if !regexp.MustCompile(`INDEX ` + regexp.QuoteMeta(c.index) + `( |\(|$)`).MatchString(joined) {
			t.Errorf("%s: plan %q does not use %s", c.name, joined, c.index)
		}
		for _, d := range plan {
			if strings.HasPrefix(d, "SCAN ") && !strings.Contains(d, "USING") {
				t.Errorf("%s scans its table: %q", c.name, joined)
			}
		}
	}
}

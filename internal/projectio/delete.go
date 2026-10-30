package projectio

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// Deleting a project (decision P51). DeleteSteps lists, children first, every table that holds rows of one project and the
// rows to delete; KeptOnDelete lists every other table with the reason its rows stay. UndecidedOnDelete (run by the test
// suite against the real migrations) fails when a migration adds a table that is in neither list, so a new kind of project
// data can never silently survive the project.

// DeleteStep deletes (or, with Update, rewrites) one table's rows of the project. @p is the project id.
type DeleteStep struct {
	Table string
	Where string
	// Update, when set, is the SET clause used instead of a delete (rows that outlive the project, unlinked from it).
	Update string
}

// Run ids of the project's agents' CLI runs (agent_runs), besides its workflow runs.
const agentRunIDs = `(SELECT id FROM agent_runs WHERE agent_id IN ` + agentIDs + `)`

// Every per-project key in the settings table: a per-project setting is a prefix and the project id (devloop:<p>,
// sandbox:<p>, runmode:<p>, security:rules:<p>, mcp.act_tools:<p>, git.trusted.<p>, and any later one).
const projectSettings = `length(key) > length(@p) AND substr(key, length(key) - length(@p) + 1) = @p`

// The connections' assignments to the project, and their rules.
const (
	assignIDs = `(SELECT id FROM integration_assignments WHERE project_id=@p)`
	ruleIDs   = `(SELECT id FROM integration_rules WHERE project_id=@p)`
	itemIDs   = `(SELECT id FROM external_items WHERE project_id=@p OR assignment_id IN ` + assignIDs + ` OR task_id IN ` + taskIDs + `)`
)

// DeleteSteps run in this order inside one transaction. A table may appear more than once; children come before the rows
// their Where clause selects through (the helper subselects read tasks, agents, rooms and runs, which go last).
var DeleteSteps = []DeleteStep{
	{Table: "settings", Where: projectSettings},
	{Table: "chats", Where: `project_id=@p`, Update: `project_id=''`},
	{Table: "integrations", Where: `project_id=@p`, Update: `project_id=''`},
	{Table: "integration_rule_matches", Where: `rule_id IN ` + ruleIDs},
	{Table: "integration_rules", Where: `project_id=@p`},
	{Table: "integration_cursors", Where: `scope IN ` + assignIDs},
	{Table: "integration_outbox", Where: `project_id=@p OR task_id IN ` + taskIDs},
	{Table: "integration_mappings", Where: `assignment_id IN ` + assignIDs},
	{Table: "integration_auto_approve", Where: `project_id=@p`},
	{Table: "integration_attachments", Where: `item_id IN ` + itemIDs},
	{Table: "external_items", Where: `project_id=@p OR assignment_id IN ` + assignIDs + ` OR task_id IN ` + taskIDs},
	{Table: "integration_assignments", Where: `project_id=@p`},
	{Table: "skill_uses", Where: `project_id=@p`},
	{Table: "skill_deliveries", Where: `project_id=@p`},
	{Table: "agent_skills", Where: `project_id=@p`},
	{Table: "skills", Where: `project_id=@p`},
	{Table: "security_scans", Where: `project_id=@p`},
	{Table: "security_dismissals", Where: `target_key=@p`},
	{Table: "recommendations", Where: `project_id=@p`},
	{Table: "lead_interventions", Where: `project_id=@p`},
	{Table: "generated_files", Where: `project_id=@p`},
	{Table: "llm_cache", Where: `project_id=@p`},
	{Table: "mcp_cache", Where: `scope='project:'||@p`},
	{Table: "mcp_usage", Where: `run_id IN ` + runIDs + ` OR run_id IN ` + agentRunIDs},
	{Table: "agent_mcp_access", Where: `project_id=@p`},
	{Table: "project_mcp_servers", Where: `project_id=@p`},
	{Table: "mcp_role_policy", Where: `project_id=@p`},
	{Table: "mcp_policy_rules", Where: `project_id=@p`},
	{Table: "task_mcp_tools", Where: `task_id IN ` + taskIDs},
	{Table: "pack_attachments", Where: `project_id=@p`},
	{Table: "milestone_tasks", Where: `milestone_id IN ` + msIDs + ` OR task_id IN ` + taskIDs},
	{Table: "milestones", Where: `project_id=@p`},
	{Table: "requirement_links", Where: `requirement_id IN ` + reqIDs + ` OR task_id IN ` + taskIDs},
	{Table: "requirement_chunks", Where: `requirement_id IN ` + reqIDs},
	{Table: "requirement_versions", Where: `requirement_id IN ` + reqIDs},
	{Table: "requirements", Where: `project_id=@p`},
	{Table: "plans", Where: `project_id=@p`},
	{Table: "project_check_settings", Where: `project_id=@p`},
	{Table: "context_manifests", Where: `run_id IN ` + runIDs + ` OR task_id IN ` + taskIDs},
	{Table: "memory", Where: `project_id=@p`},
	{Table: "budget_reservations", Where: `project_id=@p OR task_id IN ` + taskIDs + ` OR agent_id IN ` + agentIDs},
	{Table: "room_inbox_acks", Where: `item_key IN (SELECT 'budget:'||budget_id||':'||instance_id||':'||level||':'||ts FROM budget_alerts WHERE budget_id IN (SELECT id FROM budgets WHERE ` + budgetWh + `))`},
	{Table: "budget_alerts", Where: `budget_id IN (SELECT id FROM budgets WHERE ` + budgetWh + `)`},
	{Table: "budgets", Where: budgetWh},
	{Table: "routing_rules", Where: `scope='project:'||@p`},
	{Table: "routing_outcomes", Where: `project_id=@p OR task_id IN ` + taskIDs},
	{Table: "routing_decisions", Where: `task_id IN ` + taskIDs + ` OR run_id IN ` + runIDs + ` OR run_id IN (SELECT id FROM room_turns WHERE project_id=@p) OR (task_id='' AND json_extract(request,'$.ProjectID')=@p)`},
	{Table: "room_inbox_acks", Where: `item_key IN (SELECT 'question:'||id FROM messages WHERE room_id IN ` + roomIDs + `) OR item_key IN (SELECT 'check:'||id FROM messages WHERE room_id IN ` + roomIDs + `) OR item_key IN (SELECT 'mention:'||id FROM messages WHERE room_id IN ` + roomIDs + `) OR item_key IN (SELECT 'watchdog:'||id FROM tasks WHERE project_id=@p)`},
	{Table: "dead_letters", Where: `task_id IN ` + taskIDs},
	{Table: "handoffs", Where: `task_id IN ` + taskIDs},
	{Table: "watchdog_trips", Where: `task_id IN ` + taskIDs},
	{Table: "permission_allow_rules", Where: `project_id=@p OR task_id IN ` + taskIDs},
	{Table: "approvals", Where: `project_id=@p OR run_id IN ` + runIDs + ` OR task_id IN ` + taskIDs},
	{Table: "run_leases", Where: `run_id IN ` + runIDs},
	{Table: "node_executions", Where: `run_id IN ` + runIDs},
	{Table: "workflow_checkpoints", Where: `run_id IN ` + runIDs},
	{Table: "workflow_runs", Where: `id IN ` + runIDs},
	{Table: "acceptance_marks", Where: `task_id IN ` + taskIDs},
	{Table: "task_reviews", Where: `task_id IN ` + taskIDs},
	{Table: "task_evidence", Where: `task_id IN ` + taskIDs},
	{Table: "task_deps", Where: `task_id IN ` + taskIDs + ` OR depends_on IN ` + taskIDs},
	{Table: "room_join_requests", Where: `project_id=@p OR room_id IN ` + roomIDs},
	{Table: "room_join_rules", Where: `project_id=@p`},
	{Table: "room_turn_grants", Where: `project_id=@p OR room_id IN ` + roomIDs},
	{Table: "room_turns", Where: `project_id=@p OR room_id IN ` + roomIDs},
	{Table: "room_attachments", Where: `room_id IN ` + roomIDs},
	{Table: "room_exports", Where: `room_id IN ` + roomIDs},
	{Table: "messages", Where: `room_id IN ` + roomIDs},
	{Table: "room_members", Where: `room_id IN ` + roomIDs},
	{Table: "rooms", Where: `project_id=@p`},
	{Table: "tasks", Where: `project_id=@p`},
	{Table: "sessions", Where: `agent_id IN ` + agentIDs},
	{Table: "agent_runs", Where: `agent_id IN ` + agentIDs},
	{Table: "agents", Where: `project_id=@p`},
	{Table: "projects", Where: `id=@p`},
}

// KeptOnDelete lists every table whose rows stay when a project is deleted, with the reason. Full-text index tables follow
// the table they index (their triggers delete with it) and need no entry.
var KeptOnDelete = map[string]string{
	"events":                 "the event log is append-only and kept indefinitely (brief: retention); it is the record of what happened",
	"audit_log":              "hash-chained and append-only: a trigger refuses deletes; deleting a project is itself audited",
	"model_usage":            "what was spent, kept indefinitely: workspace budgets, provider quotas and the evals report still count money that was spent",
	"receipts":               "idempotency receipts of this installation, not project content",
	"workspaces":             "not project data",
	"providers":              "workspace connections to model providers, shared by every project",
	"provider_quotas":        "provider runtime state",
	"model_retention_blocks": "which models a provider refused for want of data retention: provider state, shared by every project",
	"model_catalog":          "a cache of what a provider lists",
	"probe_results":          "provider probe results of this installation",
	"secret_blobs":           "credentials belong to workspace connections (providers, integrations, MCP servers), not to a project",
	"telemetry_spans":        "local diagnostics sink, aged out by retention",
	"telemetry_metrics":      "local diagnostics sink, aged out by retention",
	"packs":                  "Expert Packs are installed for the workspace; the project's attachments to them are deleted",
	"pack_versions":          "Expert Packs are installed for the workspace",
	"pack_pages":             "Expert Packs are installed for the workspace",
	"pack_chunks":            "Expert Packs are installed for the workspace",
	"pack_pins":              "Expert Packs are installed for the workspace",
	"skill_registries":       "the workspace's registry allow-list",
	"skill_registry_watch":   "what each registry held at its last check: workspace state",
	"skill_library":          "the workspace skill library; the project's own skills are deleted",
	"agent_templates":        "the workspace's reusable team members",
	"mcp_library":            "the workspace's MCP servers; the project's grants to them are deleted",
	"chat_messages":          "messages of the human's own chats, which are kept (unlinked from the project)",
	"chat_turns":             "turn bookkeeping of the chats above",
	"chat_attachments":       "files attached to those chats, kept with them (decision P90)",

	"integration_webhook_deliveries": "the workspace receiver's log of deliveries (no bodies), by Connection: it names no project",
}

// DeleteResult says what deleting a project removed from the database, and the files that belonged to those rows (the
// caller removes them once the transaction committed).
type DeleteResult struct {
	Rows int64
	// Files the deleted rows referred to that are not kept per room, task or run: cached MCP results (file names in the
	// cache folder) and integration item bodies (<integration>/<item>.txt under the integrations folder).
	MCPCache   []string
	ItemBodies []string
	// Attachments are the folders of the deleted items' downloaded attachments (<integration>/attachments/<item> under the
	// integrations folder).
	Attachments []string
}

// Delete removes one project's rows in the order of DeleteSteps, inside the caller's transaction. It refuses an unknown
// project. It reads the file references of the rows it deletes first, so the caller can remove those files afterwards.
func Delete(ctx context.Context, tx *sql.Tx, projectID string) (DeleteResult, error) {
	var res DeleteResult
	p := sql.Named("p", projectID)
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=@p`, p).Scan(&n); err != nil {
		return res, err
	}
	if n == 0 {
		return res, fmt.Errorf("project %s not found", projectID)
	}
	var err error
	if res.MCPCache, err = queryStrings(ctx, tx, `SELECT raw_ref FROM mcp_cache WHERE raw_ref<>'' AND scope='project:'||@p`, p); err != nil {
		return res, err
	}
	if res.ItemBodies, err = queryStrings(ctx, tx, `SELECT integration_id||'/'||id||'.txt' FROM external_items WHERE (project_id=@p OR assignment_id IN `+assignIDs+` OR task_id IN `+taskIDs+`)`, p); err != nil {
		return res, err
	}
	if res.Attachments, err = queryStrings(ctx, tx, `SELECT DISTINCT integration_id||'/attachments/'||item_id FROM integration_attachments WHERE local_path<>'' AND item_id IN `+itemIDs, p); err != nil {
		return res, err
	}
	for _, st := range DeleteSteps {
		q := `DELETE FROM ` + st.Table + ` WHERE ` + st.Where
		if st.Update != "" {
			q = `UPDATE ` + st.Table + ` SET ` + st.Update + ` WHERE ` + st.Where
		}
		r, err := tx.ExecContext(ctx, q, p)
		if err != nil {
			return res, fmt.Errorf("delete the project's %s: %w", st.Table, err)
		}
		if st.Update == "" {
			k, _ := r.RowsAffected()
			res.Rows += k
		}
	}
	return res, nil
}

// UndecidedOnDelete returns one line per table that is neither deleted (DeleteSteps) nor kept with a reason (KeptOnDelete),
// and per entry that names a table no migration creates. Empty means every table was decided.
func UndecidedOnDelete(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, COALESCE(sql,'') FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	var virtual []string
	for rows.Next() {
		var n, s string
		if err := rows.Scan(&n, &s); err != nil {
			rows.Close()
			return nil, err
		}
		live[n] = true
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(s)), "CREATE VIRTUAL TABLE") {
			virtual = append(virtual, n)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	index := func(n string) bool { // a full-text table or one of its shadow tables
		for _, v := range virtual {
			if n == v || strings.HasPrefix(n, v+"_") {
				return true
			}
		}
		return false
	}
	deleted := map[string]bool{}
	for _, st := range DeleteSteps {
		deleted[st.Table] = true
	}
	var names []string
	for n := range live {
		names = append(names, n)
	}
	sort.Strings(names)
	var problems []string
	for _, n := range names {
		if n == "goose_db_version" || index(n) {
			continue
		}
		_, kept := KeptOnDelete[n]
		if !deleted[n] && !kept {
			problems = append(problems, fmt.Sprintf("table %q is neither in projectio.DeleteSteps nor in projectio.KeptOnDelete: decide whether deleting a project deletes its rows", n))
		}
		if deleted[n] && kept {
			problems = append(problems, fmt.Sprintf("table %q is both deleted and kept", n))
		}
	}
	for n := range deleted {
		if !live[n] {
			problems = append(problems, fmt.Sprintf("DeleteSteps names %q, which no migration creates", n))
		}
	}
	for n, why := range KeptOnDelete {
		if !live[n] {
			problems = append(problems, fmt.Sprintf("KeptOnDelete lists %q, which no migration creates", n))
		}
		if why == "" {
			problems = append(problems, fmt.Sprintf("KeptOnDelete entry %q has no reason", n))
		}
	}
	sort.Strings(problems)
	return problems, nil
}

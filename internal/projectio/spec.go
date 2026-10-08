// Package projectio exports one project to a portable .drydock-project.zip and imports such a file as a new project
// (docs/packaging.md section 7).
//
// What leaves the database is decided by an explicit allow-list: Tables lists every exported table with its exported
// columns, and ExcludedTables lists every other table with the reason. A test fails when a migration adds a table that is in
// neither list, or a column to an exported table that is neither exported nor explicitly skipped, so a schema change can
// never silently leak or drop data.
package projectio

// Row is one table row keyed by column name.
type Row map[string]any

// Table describes how one table is exported and imported.
type Table struct {
	Name string
	// Where selects this project's rows from the live database. @p is the project id. The helper sets below (taskIDs,
	// agentIDs...) are subselects over the live database.
	Where string
	// Columns are exported, in this order.
	Columns []string
	// Skipped columns are not exported; the value is why. Together with Columns they must cover every column.
	Skipped map[string]string
	// Const supplies a value for NOT NULL columns that are not exported and have no default (projects.repo_path).
	Const map[string]any
	// OrderBy makes export order deterministic (default rowid).
	OrderBy string
	// IgnoreDup makes the import skip rows that collide on the primary key (room members of several human names).
	IgnoreDup bool
	// Export may adjust a row before it is scrubbed and written (it runs on the exported values).
	Export func(r Row, e *ExportEnv)
	// Import may adjust a row after ids were remapped and before it is inserted into the live database. It is where
	// everything an imported file could use to act is switched off or marked pending acceptance.
	Import func(r Row, im *ImportEnv)
}

// Helper subselects over the live database, parameterized by @p (the project id).
const (
	taskIDs  = `(SELECT id FROM tasks WHERE project_id=@p)`
	agentIDs = `(SELECT id FROM agents WHERE project_id=@p)`
	roomIDs  = `(SELECT id FROM rooms WHERE project_id=@p)`
	runIDs   = `(SELECT id FROM workflow_runs WHERE project_id=@p OR task_id IN ` + taskIDs + `)`
	reqIDs   = `(SELECT id FROM requirements WHERE project_id=@p)`
	msIDs    = `(SELECT id FROM milestones WHERE project_id=@p)`
	budgetWh = `project_id=@p OR (scope='project' AND scope_id=@p) OR (scope='task' AND scope_id IN ` + taskIDs +
		`) OR (scope='run' AND scope_id IN ` + runIDs + `) OR (scope='agent' AND scope_id IN ` + agentIDs + `)`
)

const (
	why_localPath  = "a path on the exporting machine; it means nothing elsewhere"
	why_surrogate  = "autoincrement surrogate key; reassigned on import"
	why_busSeq     = "position in this installation's event log, which is not exported"
	why_runtime    = "runtime state of the exporting installation; re-probed after import"
	why_credential = "a reference to a stored credential; credentials are never exported"
)

// Tables is the export allow-list in dependency order (parents before children, so foreign keys hold on insert).
var Tables = []Table{
	{Name: "projects", Where: `id=@p`,
		Columns: []string{"id", "name", "default_branch", "merge_strategy", "mode", "created_at", "version"},
		Skipped: map[string]string{"workspace_id": "workspaces are not exported", "repo_path": why_localPath + "; importing requires a new repository path"},
		Const:   map[string]any{"repo_path": ""}, Import: importProject},
	{Name: "providers", Where: `id IN (SELECT provider_id FROM agents WHERE project_id=@p AND provider_id<>'')`,
		Columns: []string{"id", "name", "kind", "base_url", "data_class", "tier", "max_concurrent_agents", "hourly_cost", "flavor", "enabled",
			"tls_mode", "tls_fingerprint", "lan_unencrypted", "attestations", "wire_apis", "request_overrides", "created_at"},
		Skipped: map[string]string{"api_key_ref": why_credential, "status": why_runtime, "last_seen": why_runtime, "server_info": why_runtime,
			"responses_validated_at": why_runtime, "updated_at": why_runtime},
		Export: exportProvider, Import: importProvider},
	{Name: "agents", Where: `project_id=@p`,
		Columns: []string{"id", "project_id", "name", "adapter", "role", "model", "effort", "provider_id", "permission_mode", "config", "created_at", "version", "purpose", "archived_at"},
		Skipped: map[string]string{"status": "runtime state; imported agents are STOPPED"}, Import: importAgent},
	{Name: "agent_runs", Where: `agent_id IN ` + agentIDs,
		Columns: []string{"id", "agent_id", "task_id", "status", "started_at", "ended_at", "exit_code", "error"},
		Skipped: map[string]string{"session_id": "the agent CLI's own session id, which only resumes on the exporting machine", "pid": why_runtime, "cwd": why_localPath},
		Import:  importAgentRun},
	{Name: "rooms", Where: `project_id=@p`,
		Columns: []string{"id", "project_id", "kind", "name", "topic", "mode", "task_id", "archived", "created_at", "version", "mode_params", "created_by", "archived_at"}},
	{Name: "room_members", Where: `room_id IN ` + roomIDs,
		Columns: []string{"room_id", "member_id", "kind", "last_read_seq", "role", "state", "added_by", "added_at", "last_mention_read_seq"}, IgnoreDup: true, Import: importMember},
	{Name: "messages", Where: `room_id IN ` + roomIDs, OrderBy: `room_id, seq, created_at, rowid`,
		Columns: []string{"id", "room_id", "thread_id", "author", "type", "body", "refs", "run_id", "created_at", "seq", "reply_to_id", "supersedes_id",
			"payload", "markers", "mentions", "trust", "task_id", "idempotency_key"},
		Skipped: map[string]string{"event_seq": why_busSeq}},
	{Name: "room_exports", Where: `room_id IN ` + roomIDs,
		Columns: []string{"id", "room_id", "scope", "receipt_id", "kind", "format", "from_seq", "to_seq", "params", "content", "sha256", "created_by", "created_at"},
		Skipped: map[string]string{"event_seq": why_busSeq}},
	{Name: "room_turns", Where: `project_id=@p`,
		Columns: []string{"id", "room_id", "project_id", "trigger_id", "agent_id", "round", "reason", "mode", "status", "message_id", "agent_run_id",
			"context_msgs", "context_tokens", "input_tokens", "output_tokens", "cost_usd", "error", "started_at", "ended_at"}, Import: importTurn},
	{Name: "tasks", Where: `project_id=@p`, OrderBy: `seq`,
		Columns: []string{"id", "project_id", "seq", "title", "description", "status", "priority", "assignee", "reviewer", "template", "files", "acceptance",
			"branch", "base_branch", "iteration", "max_iterations", "review_feedback", "verdict", "model", "effort", "cost_usd", "issue_ref", "pr_ref",
			"run_id", "blocked_reason", "accepted_by", "created_at", "updated_at", "started_at", "completed_at", "version"},
		Skipped: map[string]string{"worktree": why_localPath}, Import: importTask},
	{Name: "task_deps", Where: `task_id IN ` + taskIDs + ` AND depends_on IN ` + taskIDs, Columns: []string{"task_id", "depends_on"}},
	{Name: "task_evidence", Where: `task_id IN ` + taskIDs, OrderBy: `id`,
		Columns: []string{"task_id", "attempt", "kind", "passed", "summary", "ref", "data", "created_at"},
		Skipped: map[string]string{"id": why_surrogate}, Export: exportEvidence},
	{Name: "task_reviews", Where: `task_id IN ` + taskIDs, OrderBy: `id`,
		Columns: []string{"task_id", "attempt", "reviewer", "verdict", "feedback", "model", "created_at"},
		Skipped: map[string]string{"id": why_surrogate}},
	{Name: "workflow_runs", Where: `id IN ` + runIDs, OrderBy: `created_at, id`,
		Columns: []string{"id", "workflow", "status", "project_id", "task_id", "input", "result", "topology_hash", "parent_checkpoint_id",
			"latest_checkpoint_id", "failed_reason", "created_at", "updated_at", "version"}, Import: importRun},
	{Name: "workflow_checkpoints", Where: `run_id IN ` + runIDs, OrderBy: `run_id, created_at, rowid`,
		Columns: []string{"id", "run_id", "workflow", "step", "kind", "parent_id", "state", "created_at"}},
	{Name: "node_executions", Where: `run_id IN ` + runIDs, OrderBy: `run_id, step, node_id, attempt`,
		Columns: []string{"run_id", "node_id", "step", "attempt", "status", "output", "error", "partial_ref", "safe_point_ref", "started_at", "finished_at"}},
	{Name: "approvals", Where: `project_id=@p OR run_id IN ` + runIDs + ` OR task_id IN ` + taskIDs, OrderBy: `created_at, id`,
		Columns: []string{"id", "run_id", "node_id", "task_id", "project_id", "kind", "title", "payload", "action_hash", "status", "decision", "response",
			"decided_by", "created_at", "resolved_at", "version", "expires_at", "action_kind"}, Import: importApproval},
	{Name: "watchdog_trips", Where: `task_id IN ` + taskIDs, OrderBy: `id`,
		Columns: []string{"task_id", "run_id", "node_id", "attempt", "agent_id", "signal", "stage", "evidence", "created_at"},
		Skipped: map[string]string{"id": why_surrogate}},
	{Name: "handoffs", Where: `task_id IN ` + taskIDs, Columns: []string{"id", "task_id", "run_id", "agent_id", "artifact", "reason", "created_at"}},
	{Name: "dead_letters", Where: `task_id IN ` + taskIDs, Columns: []string{"id", "task_id", "template", "reason", "detail", "resolved", "note", "created_at"}},
	{Name: "model_usage", Where: `task_id IN ` + taskIDs + ` OR agent_id IN ` + agentIDs + ` OR run_id IN ` + runIDs, OrderBy: `id`,
		Columns: []string{"ts", "run_id", "agent_run_id", "task_id", "agent_id", "provider", "host_id", "model", "tier", "input_tokens", "output_tokens",
			"cached_tokens", "cache_write_tokens", "reasoning_tokens", "cost_usd", "source", "cache_hit", "purpose", "decision_id", "latency_ms",
			"cost_source", "est_saved_usd"},
		Skipped: map[string]string{"id": why_surrogate}},
	{Name: "routing_decisions", Where: `task_id IN ` + taskIDs + ` OR run_id IN ` + runIDs, OrderBy: `ts, id`,
		Columns: []string{"id", "ts", "run_id", "task_id", "node_id", "attempt", "run_kind", "level", "agent", "provider_id", "model", "effort", "decided_by",
			"est_cost_usd", "escalated_from", "request", "decision"}},
	{Name: "routing_outcomes", Where: `task_id IN ` + taskIDs, OrderBy: `id`,
		Columns: []string{"decision_id", "task_id", "project_id", "outcome", "accepted", "accepted_at", "accepted_by", "iterations", "ship_count", "revise_count",
			"premium_revise_count", "escalated", "checks_passed", "watchdog_trips", "tokens_in", "tokens_out", "tokens_cached", "cost_usd", "cost_reported_usd",
			"cost_estimated_usd", "cost_unknown_rows", "cost_source", "premium_subscription_units", "premium_turns", "wall_ms", "failure_class", "recorded_at"},
		Skipped: map[string]string{"id": why_surrogate}},
	{Name: "routing_rules", Where: `scope='project:'||@p`,
		Columns: []string{"id", "scope", "priority", "when_expr", "then_json", "enabled", "version", "created_at", "updated_at"}, Import: importRoutingRule},
	{Name: "budgets", Where: budgetWh,
		Columns: []string{"id", "scope", "scope_id", "project_id", "limit_usd", "limit_tokens", "limit_turns", "warn_pct", "enabled", "note", "version",
			"created_at", "updated_at", "updated_by"}},
	{Name: "budget_alerts", Where: `budget_id IN (SELECT id FROM budgets WHERE ` + budgetWh + `)`, Columns: []string{"budget_id", "instance_id", "level", "ts"}},
	{Name: "memory", Where: `project_id=@p`, OrderBy: `created_at, id`,
		Columns: []string{"id", "project_id", "kind", "title", "body", "status", "source", "created_at", "updated_at", "version"}, Import: importMemory},
	{Name: "context_manifests", Where: `run_id IN ` + runIDs,
		Columns: []string{"run_id", "node_id", "attempt", "task_id", "agent_id", "items", "total_bytes", "created_at"}},
	{Name: "project_check_settings", Where: `project_id=@p`, Columns: []string{"project_id", "secrets_enabled", "deps_enabled", "deps_offline", "updated_at", "updated_by"}},
	{Name: "plans", Where: `project_id=@p`, OrderBy: `created_at, id`,
		Columns: []string{"id", "project_id", "run_id", "goal", "requirements_sha", "planner_agent", "status", "proposal", "accepted_tasks", "error", "created_at", "decided_at", "decided_by"}},
	{Name: "requirements", Where: `project_id=@p`, OrderBy: `created_at, id`,
		Columns: []string{"id", "project_id", "title", "source", "source_ref", "current_version", "content_hash", "archived", "created_by", "created_at", "updated_at"}},
	{Name: "requirement_versions", Where: `requirement_id IN ` + reqIDs, OrderBy: `requirement_id, version`,
		Columns: []string{"requirement_id", "version", "content_hash", "text", "chunks", "redactions", "created_by", "created_at"}},
	{Name: "requirement_chunks", Where: `requirement_id IN ` + reqIDs, OrderBy: `id`,
		Columns: []string{"requirement_id", "version", "ordinal", "heading_path", "body", "token_est", "chunk_hash", "risk", "flags", "quarantined", "current"},
		Skipped: map[string]string{"id": why_surrogate}, Import: importRequirementChunk},
	{Name: "requirement_links", Where: `requirement_id IN ` + reqIDs, OrderBy: `id`,
		Columns: []string{"requirement_id", "version", "task_id", "criterion", "criterion_hash", "created_by", "created_at"},
		Skipped: map[string]string{"id": why_surrogate}},
	{Name: "milestones", Where: `project_id=@p`,
		Columns: []string{"id", "project_id", "title", "description", "due_at", "status", "created_by", "created_at", "updated_at", "closed_at"}},
	{Name: "milestone_tasks", Where: `milestone_id IN ` + msIDs + ` AND task_id IN ` + taskIDs, Columns: []string{"milestone_id", "task_id"}},
	{Name: "skills", Where: `project_id=@p`, OrderBy: `name`,
		Columns: []string{"id", "project_id", "name", "description", "body", "license", "compatibility", "globs", "roles", "origin", "registry_id", "source_path",
			"pinned_commit", "content_hash", "has_scripts", "allowed_tools_ignored", "status", "risk", "flags", "block_reason", "decided_by", "decided_at",
			"created_at", "updated_at"}, Import: importSkill},
	{Name: "settings", Where: `key='devloop:'||@p`, Columns: []string{"key", "value"}, Import: importSetting},
}

// ExcludedTables lists every table that is not exported, with the reason. Virtual full-text tables are rebuilt by the
// triggers of the table they index and are listed here too.
var ExcludedTables = map[string]string{
	"events":                   "this installation's event log; sequence numbers are install-local and payloads are not part of a project record",
	"workspaces":               "not project data",
	"model_catalog":            "a cache of what a provider lists; refetched from the provider",
	"sessions":                 "agent CLI session ids only resume on the exporting machine",
	"provider_quotas":          "provider runtime state",
	"llm_cache":                "response cache; may hold prompts and answers of other work",
	"secret_blobs":             "encrypted secrets are never exported",
	"run_leases":               "runtime lock rows",
	"generated_files":          "hashes of files in the exporting machine's repository",
	"room_inbox_acks":          "dismissals of this installation's Needs-you inbox",
	"receipts":                 "idempotency receipts of this installation",
	"audit_log":                "hash-chained per installation; rows cannot be moved to another chain",
	"mcp_cache":                "tool-result cache",
	"mcp_usage":                "estimated MCP token accounting of this installation",
	"packs":                    "Expert Packs are installed content, not project data",
	"pack_versions":            "Expert Packs are installed content, not project data",
	"pack_pages":               "Expert Packs are installed content, not project data",
	"pack_chunks":              "Expert Packs are installed content, not project data",
	"pack_chunks_fts":          "full-text index, rebuilt by triggers",
	"pack_attachments":         "refer to packs installed on the exporting machine; attach packs again after import",
	"pack_pins":                "Expert Packs are installed content, not project data",
	"skill_registries":         "registry allow-list of this installation",
	"skill_deliveries":         "delivery log of files written into the exporting machine's worktrees",
	"probe_results":            "provider probe results of this installation",
	"telemetry_spans":          "local diagnostics sink",
	"telemetry_metrics":        "local diagnostics sink",
	"integrations":             "vendor Connections hold credential references; reconnect after import",
	"integration_cursors":      "sync state of a Connection",
	"external_items":           "vendor data belonging to a Connection",
	"integration_rules":        "rules belong to a Connection",
	"integration_rule_matches": "rules belong to a Connection",
	"integration_outbox":       "outbound writes to a vendor, bound to approvals on the exporting machine",
	"project_mcp_servers":      "an MCP server is a program or URL the human chose on this machine, with credentials in the vault: an archive must never bring one in; add servers again after import",
	"agent_mcp_access":         "grants to the servers above",
	"memory_fts":               "full-text index, rebuilt by triggers",
	"requirement_chunks_fts":   "full-text index, rebuilt by triggers",
}

// ExcludedSettings documents settings keys that exist for a project but are deliberately not exported.
var ExcludedSettings = map[string]string{
	"mcp.actTools:<project>": "which MCP tools may act is a permission grant; the human grants it again after import",
}

-- +goose Up
-- Indexes that serve "column = ?" (audit 2026-10-16, M12). These were partial indexes ("WHERE column <> ''"), and SQLite uses a
-- partial index for a query only when it can prove the query's WHERE implies the index's: it cannot prove that "run_id = ?" means
-- "run_id <> ''", so every lookup by run, task, project, agent or decision scanned its table (plans: "SCAN events"). Most rows
-- these indexes covered have a value in the column, so leaving the empty ones in costs little.
DROP INDEX IF EXISTS events_run;
DROP INDEX IF EXISTS events_task;
DROP INDEX IF EXISTS events_project;
DROP INDEX IF EXISTS messages_thread;
DROP INDEX IF EXISTS messages_reply;
DROP INDEX IF EXISTS messages_supersede;
DROP INDEX IF EXISTS workflow_runs_task;
DROP INDEX IF EXISTS workflow_checkpoints_parent;
DROP INDEX IF EXISTS llm_cache_project;
DROP INDEX IF EXISTS routing_decisions_task;
DROP INDEX IF EXISTS routing_decisions_run;
DROP INDEX IF EXISTS audit_log_subject;
DROP INDEX IF EXISTS model_usage_agent_id;
DROP INDEX IF EXISTS agent_runs_task;
DROP INDEX IF EXISTS routing_outcomes_decision;
DROP INDEX IF EXISTS external_items_task;
DROP INDEX IF EXISTS external_items_rule;

CREATE INDEX events_run ON events(run_id, seq);
CREATE INDEX events_task ON events(task_id, seq);
CREATE INDEX events_project ON events(project_id, seq);
CREATE INDEX messages_thread ON messages(thread_id);
CREATE INDEX messages_reply ON messages(reply_to_id);
CREATE INDEX messages_supersede ON messages(supersedes_id);
CREATE INDEX workflow_runs_task ON workflow_runs(task_id);
CREATE INDEX workflow_checkpoints_parent ON workflow_checkpoints(parent_id);
CREATE INDEX llm_cache_project ON llm_cache(project_id);
CREATE INDEX routing_decisions_task ON routing_decisions(task_id);
CREATE INDEX routing_decisions_run ON routing_decisions(run_id);
CREATE INDEX audit_log_subject ON audit_log(subject, seq);
-- the Agent Inspector lists an agent's newest usage rows and sums all of them
CREATE INDEX model_usage_agent_id ON model_usage(agent_id, id);
CREATE INDEX agent_runs_task ON agent_runs(task_id);
CREATE INDEX routing_outcomes_decision ON routing_outcomes(decision_id);
CREATE INDEX external_items_task ON external_items(task_id);
CREATE INDEX external_items_rule ON external_items(imported_by, imported_at);

-- +goose Down
DROP INDEX IF EXISTS events_run;
DROP INDEX IF EXISTS events_task;
DROP INDEX IF EXISTS events_project;
DROP INDEX IF EXISTS messages_thread;
DROP INDEX IF EXISTS messages_reply;
DROP INDEX IF EXISTS messages_supersede;
DROP INDEX IF EXISTS workflow_runs_task;
DROP INDEX IF EXISTS workflow_checkpoints_parent;
DROP INDEX IF EXISTS llm_cache_project;
DROP INDEX IF EXISTS routing_decisions_task;
DROP INDEX IF EXISTS routing_decisions_run;
DROP INDEX IF EXISTS audit_log_subject;
DROP INDEX IF EXISTS model_usage_agent_id;
DROP INDEX IF EXISTS agent_runs_task;
DROP INDEX IF EXISTS routing_outcomes_decision;
DROP INDEX IF EXISTS external_items_task;
DROP INDEX IF EXISTS external_items_rule;

CREATE INDEX events_run  ON events(run_id, seq) WHERE run_id <> '';
CREATE INDEX events_task ON events(task_id, seq) WHERE task_id <> '';
CREATE INDEX events_project ON events(project_id, seq) WHERE project_id <> '';
CREATE INDEX messages_thread ON messages(thread_id) WHERE thread_id <> '';
CREATE INDEX messages_reply ON messages(reply_to_id) WHERE reply_to_id <> '';
CREATE INDEX messages_supersede ON messages(supersedes_id) WHERE supersedes_id <> '';
CREATE INDEX workflow_runs_task ON workflow_runs(task_id) WHERE task_id <> '';
CREATE INDEX workflow_checkpoints_parent ON workflow_checkpoints(parent_id) WHERE parent_id <> '';
CREATE INDEX llm_cache_project ON llm_cache(project_id) WHERE project_id <> '';
CREATE INDEX routing_decisions_task ON routing_decisions(task_id) WHERE task_id <> '';
CREATE INDEX routing_decisions_run  ON routing_decisions(run_id) WHERE run_id <> '';
CREATE INDEX audit_log_subject ON audit_log(subject, seq) WHERE subject <> '';
CREATE INDEX model_usage_agent_id ON model_usage(agent_id) WHERE agent_id <> '';
CREATE INDEX agent_runs_task ON agent_runs(task_id) WHERE task_id <> '';
CREATE INDEX routing_outcomes_decision ON routing_outcomes(decision_id) WHERE decision_id <> '';
CREATE INDEX external_items_task ON external_items(task_id) WHERE task_id <> '';
CREATE INDEX external_items_rule ON external_items(imported_by, imported_at) WHERE imported_by <> '';

-- +goose Up
-- The team lead's interventions (decision P26): one row per supervision turn whose decision was applied, including the
-- escalations that followed two invalid answers. Agent-authored text (message, reason) is scrubbed before it is stored.
CREATE TABLE lead_interventions (
  id            TEXT PRIMARY KEY,
  project_id    TEXT NOT NULL,
  task_id       TEXT NOT NULL DEFAULT '',
  run_id        TEXT NOT NULL DEFAULT '',
  lead_agent_id TEXT NOT NULL,
  trigger_kind  TEXT NOT NULL,
  action        TEXT NOT NULL,
  message       TEXT NOT NULL DEFAULT '',
  reason        TEXT NOT NULL DEFAULT '',
  target_agent  TEXT NOT NULL DEFAULT '',
  created_tasks TEXT NOT NULL DEFAULT '[]',
  evidence      TEXT NOT NULL DEFAULT '',
  invalid       INTEGER NOT NULL DEFAULT 0,
  message_id    TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL
);
CREATE INDEX lead_interventions_task ON lead_interventions(task_id, run_id);
CREATE INDEX lead_interventions_project ON lead_interventions(project_id, created_at);

-- +goose Down
DROP TABLE lead_interventions;

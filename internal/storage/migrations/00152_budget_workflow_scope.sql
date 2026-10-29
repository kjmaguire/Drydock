-- +goose Up
-- A budget can limit a workflow (decision P118, docs/token-control.md rule 14): scope 'workflow', scope_id the workflow's name
-- (iterate-until-verified, task-pipeline, ...), and project_id the project whose runs of it count ('' = every project's). Its
-- spend is what every run of that workflow spent. SQLite cannot change a CHECK, so the table is rebuilt; the fired alerts
-- are kept aside first, because dropping budgets with foreign keys on deletes them through the cascade.
CREATE TABLE budget_alerts_keep AS SELECT budget_id, instance_id, level, ts FROM budget_alerts;

CREATE TABLE budgets_new (
  id          TEXT PRIMARY KEY,
  scope       TEXT NOT NULL CHECK (scope IN ('project','task','run','agent','workflow')),
  scope_id    TEXT NOT NULL,                       -- the id of the project/task/run/agent, the workflow's name, or '*' = every task/run/agent
  project_id  TEXT NOT NULL DEFAULT '',            -- restricts a '*' template or a workflow budget to one project; '' = any project
  limit_usd   REAL,
  limit_tokens INTEGER,
  limit_turns INTEGER,
  warn_pct    REAL NOT NULL DEFAULT 0.8,
  enabled     INTEGER NOT NULL DEFAULT 1,
  note        TEXT NOT NULL DEFAULT '',
  version     INTEGER NOT NULL DEFAULT 1,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  updated_by  TEXT NOT NULL,
  CHECK (limit_usd IS NOT NULL OR limit_tokens IS NOT NULL OR limit_turns IS NOT NULL)
);
INSERT INTO budgets_new(id,scope,scope_id,project_id,limit_usd,limit_tokens,limit_turns,warn_pct,enabled,note,version,created_at,updated_at,updated_by)
  SELECT id,scope,scope_id,project_id,limit_usd,limit_tokens,limit_turns,warn_pct,enabled,note,version,created_at,updated_at,updated_by FROM budgets;
DROP INDEX budgets_target;
DROP TABLE budgets;
ALTER TABLE budgets_new RENAME TO budgets;
CREATE UNIQUE INDEX budgets_target ON budgets(scope, scope_id, project_id);
INSERT OR IGNORE INTO budget_alerts(budget_id, instance_id, level, ts) SELECT budget_id, instance_id, level, ts FROM budget_alerts_keep;
DROP TABLE budget_alerts_keep;

-- A reservation names its turn's workflow, so a workflow budget sees what the turns in flight of that workflow hold.
ALTER TABLE budget_reservations ADD COLUMN workflow TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE budget_reservations DROP COLUMN workflow;
-- workflow budgets cannot exist under the older CHECK: they are removed with their alerts.
DELETE FROM budget_alerts WHERE budget_id IN (SELECT id FROM budgets WHERE scope = 'workflow');
DELETE FROM budgets WHERE scope = 'workflow';
CREATE TABLE budget_alerts_keep AS SELECT budget_id, instance_id, level, ts FROM budget_alerts;
CREATE TABLE budgets_old (
  id          TEXT PRIMARY KEY,
  scope       TEXT NOT NULL CHECK (scope IN ('project','task','run','agent')),
  scope_id    TEXT NOT NULL,
  project_id  TEXT NOT NULL DEFAULT '',
  limit_usd   REAL,
  limit_tokens INTEGER,
  limit_turns INTEGER,
  warn_pct    REAL NOT NULL DEFAULT 0.8,
  enabled     INTEGER NOT NULL DEFAULT 1,
  note        TEXT NOT NULL DEFAULT '',
  version     INTEGER NOT NULL DEFAULT 1,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  updated_by  TEXT NOT NULL,
  CHECK (limit_usd IS NOT NULL OR limit_tokens IS NOT NULL OR limit_turns IS NOT NULL)
);
INSERT INTO budgets_old(id,scope,scope_id,project_id,limit_usd,limit_tokens,limit_turns,warn_pct,enabled,note,version,created_at,updated_at,updated_by)
  SELECT id,scope,scope_id,project_id,limit_usd,limit_tokens,limit_turns,warn_pct,enabled,note,version,created_at,updated_at,updated_by FROM budgets;
DROP INDEX budgets_target;
DROP TABLE budgets;
ALTER TABLE budgets_old RENAME TO budgets;
CREATE UNIQUE INDEX budgets_target ON budgets(scope, scope_id, project_id);
INSERT OR IGNORE INTO budget_alerts(budget_id, instance_id, level, ts) SELECT budget_id, instance_id, level, ts FROM budget_alerts_keep;
DROP TABLE budget_alerts_keep;

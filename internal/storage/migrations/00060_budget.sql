-- +goose Up
-- Budgets (docs/token-control.md s7, brief s7 rule 14, rule 12 per-run ceilings). Usage is never stored here: it is summed
-- from model_usage on every evaluation, so a budget can only ever disagree with the usage rows if the rows are wrong.

CREATE TABLE budgets (
  id          TEXT PRIMARY KEY,
  scope       TEXT NOT NULL CHECK (scope IN ('project','task','run','agent')),
  scope_id    TEXT NOT NULL,                       -- the id of the project/task/run/agent, or '*' = every task/run/agent (a ceiling template)
  project_id  TEXT NOT NULL DEFAULT '',            -- restricts a '*' template to one project; '' = any project
  limit_usd   REAL,                                -- NULL = no dollar limit; spend counts only reported and estimated cost (never unknown)
  limit_tokens INTEGER,                            -- NULL = no token limit; tokens = input + output as recorded in model_usage
  limit_turns INTEGER,                             -- NULL = no turn limit; a turn is one agent session start (agent_runs)
  warn_pct    REAL NOT NULL DEFAULT 0.8,
  enabled     INTEGER NOT NULL DEFAULT 1,
  note        TEXT NOT NULL DEFAULT '',
  version     INTEGER NOT NULL DEFAULT 1,          -- compare-and-swap
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  updated_by  TEXT NOT NULL,                       -- always a human: identity
  CHECK (limit_usd IS NOT NULL OR limit_tokens IS NOT NULL OR limit_turns IS NOT NULL)
);
CREATE UNIQUE INDEX budgets_target ON budgets(scope, scope_id, project_id);

-- One row per (budget, concrete instance, level) so a threshold fires once; raising a limit clears the budget's rows.
CREATE TABLE budget_alerts (
  budget_id   TEXT NOT NULL REFERENCES budgets(id) ON DELETE CASCADE,
  instance_id TEXT NOT NULL,                       -- the concrete project/task/run/agent id the threshold was crossed for
  level       TEXT NOT NULL CHECK (level IN ('warning','exceeded')),
  ts          INTEGER NOT NULL,
  PRIMARY KEY (budget_id, instance_id, level)
);

CREATE INDEX IF NOT EXISTS model_usage_agent_id ON model_usage(agent_id) WHERE agent_id <> '';
CREATE INDEX IF NOT EXISTS model_usage_ts ON model_usage(ts);
CREATE INDEX IF NOT EXISTS agent_runs_task ON agent_runs(task_id) WHERE task_id <> '';

-- +goose Down
DROP INDEX IF EXISTS agent_runs_task;
DROP INDEX IF EXISTS model_usage_ts;
DROP INDEX IF EXISTS model_usage_agent_id;
DROP TABLE budget_alerts;
DROP TABLE budgets;

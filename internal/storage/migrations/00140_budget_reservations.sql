-- +goose Up
-- Pre-call budget reservations (decision P112): before a routed agent turn starts, its estimated cost (dollars when the price
-- is known, tokens, and one turn) is held against every budget it counts toward, in the same write transaction that checks
-- the budgets, so two turns that start together cannot both pass a budget only one of them fits in. A held row counts toward
-- the check of every later reservation until the turn ends: then it is settled (its real cost is in model_usage and
-- agent_runs, which the budgets read, and is copied here for the record) or released (the turn failed before it ran, or was
-- cancelled). A row still held when Drydock starts belongs to a turn of an earlier process: it is expired at start, and a row
-- past expires_at no longer counts, so a crash cannot hold budget for good. While its turn runs, what the turn has already
-- reported spending (real_usd, real_tokens: those are in model_usage now) is taken off what the row holds, and the turn it
-- holds is let go once the turn has started (its agent_runs row counts it from then on), so nothing is counted twice.
CREATE TABLE budget_reservations (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL DEFAULT '',
  task_id     TEXT NOT NULL DEFAULT '',
  run_id      TEXT NOT NULL DEFAULT '',
  agent_id    TEXT NOT NULL DEFAULT '',
  decision_id TEXT NOT NULL DEFAULT '',
  provider_id TEXT NOT NULL DEFAULT '',
  model       TEXT NOT NULL DEFAULT '',
  est_usd     REAL NOT NULL DEFAULT 0,     -- 0 when the price is unknown (never a guess)
  est_tokens  INTEGER NOT NULL DEFAULT 0,
  state       TEXT NOT NULL DEFAULT 'held' CHECK (state IN ('held','settled','released','expired')),
  real_usd    REAL NOT NULL DEFAULT 0,
  real_tokens INTEGER NOT NULL DEFAULT 0,
  holds_turn  INTEGER NOT NULL DEFAULT 1,  -- 0 once the turn started (its agent_runs row counts it)
  note        TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  expires_at  INTEGER NOT NULL,
  ended_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX budget_reservations_held ON budget_reservations(state, expires_at) WHERE state = 'held';
CREATE INDEX budget_reservations_project ON budget_reservations(project_id);

-- +goose Down
DROP INDEX budget_reservations_project;
DROP INDEX budget_reservations_held;
DROP TABLE budget_reservations;

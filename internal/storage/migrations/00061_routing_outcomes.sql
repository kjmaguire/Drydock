-- +goose Up
-- One row per routing decision of a task when the task ends, plus one task-level row (decision_id '') when the task had no
-- decision. Recorded from day one (brief s6, docs/model-routing.md s12) by subscribing to task.completed, task.failed and
-- task.cancelled on the event bus. Every figure is read from the database at that moment; nothing is estimated here.
CREATE TABLE routing_outcomes (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  decision_id   TEXT NOT NULL DEFAULT '',
  task_id       TEXT NOT NULL,
  project_id    TEXT NOT NULL DEFAULT '',
  outcome       TEXT NOT NULL CHECK (outcome IN ('accepted','failed','cancelled')),
  accepted      INTEGER NOT NULL,                  -- 1 only when the task reached COMPLETED through a human: identity
  accepted_at   INTEGER NOT NULL DEFAULT 0,
  accepted_by   TEXT NOT NULL DEFAULT '',
  iterations    INTEGER NOT NULL DEFAULT 0,
  ship_count    INTEGER NOT NULL DEFAULT 0,
  revise_count  INTEGER NOT NULL DEFAULT 0,
  premium_revise_count INTEGER NOT NULL DEFAULT 0,
  escalated     INTEGER NOT NULL DEFAULT 0,
  checks_passed INTEGER,                           -- NULL = no check evidence recorded
  watchdog_trips INTEGER NOT NULL DEFAULT 0,
  tokens_in     INTEGER NOT NULL DEFAULT 0,
  tokens_out    INTEGER NOT NULL DEFAULT 0,
  tokens_cached INTEGER NOT NULL DEFAULT 0,
  cost_usd      REAL NOT NULL DEFAULT 0,           -- reported + estimated; unknown rows are NOT in here
  cost_reported_usd  REAL NOT NULL DEFAULT 0,
  cost_estimated_usd REAL NOT NULL DEFAULT 0,
  cost_unknown_rows  INTEGER NOT NULL DEFAULT 0,
  cost_source   TEXT NOT NULL DEFAULT 'none',      -- none|reported|estimated|mixed|unknown
  premium_subscription_units INTEGER NOT NULL DEFAULT 0,  -- premium-tier tokens (input+output); approximate, vendor units differ
  premium_turns INTEGER NOT NULL DEFAULT 0,
  wall_ms       INTEGER NOT NULL DEFAULT 0,
  failure_class TEXT NOT NULL DEFAULT '',
  recorded_at   INTEGER NOT NULL,
  UNIQUE (task_id, decision_id)
);
CREATE INDEX routing_outcomes_decision ON routing_outcomes(decision_id) WHERE decision_id <> '';

-- +goose Down
DROP TABLE routing_outcomes;

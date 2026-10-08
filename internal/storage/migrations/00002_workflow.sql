-- +goose Up
CREATE TABLE workflow_runs (
  id            TEXT PRIMARY KEY,
  workflow      TEXT NOT NULL,
  status        TEXT NOT NULL,
  project_id    TEXT NOT NULL DEFAULT '',
  task_id       TEXT NOT NULL DEFAULT '',
  input         BLOB NOT NULL DEFAULT x'6e756c6c',
  result        BLOB,
  topology_hash TEXT NOT NULL,
  parent_checkpoint_id TEXT NOT NULL DEFAULT '',
  latest_checkpoint_id TEXT NOT NULL DEFAULT '',
  failed_reason TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  version       INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX workflow_runs_status ON workflow_runs(status);
CREATE INDEX workflow_runs_task ON workflow_runs(task_id) WHERE task_id <> '';

CREATE TABLE workflow_checkpoints (
  id         TEXT PRIMARY KEY,
  run_id     TEXT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
  workflow   TEXT NOT NULL,
  step       INTEGER NOT NULL,
  kind       TEXT NOT NULL CHECK (kind IN ('entry','step','response_entry')),
  parent_id  TEXT NOT NULL DEFAULT '',
  state      BLOB NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX workflow_checkpoints_run ON workflow_checkpoints(run_id, created_at);
CREATE INDEX workflow_checkpoints_parent ON workflow_checkpoints(parent_id) WHERE parent_id <> '';

CREATE TABLE node_executions (
  run_id      TEXT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
  node_id     TEXT NOT NULL,
  step        INTEGER NOT NULL,
  attempt     INTEGER NOT NULL,
  status      TEXT NOT NULL CHECK (status IN ('started','done','failed','abandoned')),
  output      BLOB,
  error       TEXT NOT NULL DEFAULT '',
  partial_ref TEXT NOT NULL DEFAULT '',
  safe_point_ref TEXT NOT NULL DEFAULT '',
  started_at  INTEGER NOT NULL,
  finished_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (run_id, node_id, step, attempt)
);

CREATE TABLE run_leases (
  run_id     TEXT PRIMARY KEY,
  owner      TEXT NOT NULL,
  expires_at INTEGER NOT NULL
);

CREATE TABLE approvals (
  id          TEXT PRIMARY KEY,
  run_id      TEXT NOT NULL DEFAULT '',
  node_id     TEXT NOT NULL DEFAULT '',
  task_id     TEXT NOT NULL DEFAULT '',
  project_id  TEXT NOT NULL DEFAULT '',
  kind        TEXT NOT NULL CHECK (kind IN ('approval','question','review')),
  title       TEXT NOT NULL,
  payload     BLOB NOT NULL DEFAULT x'6e756c6c',
  action_hash TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL CHECK (status IN ('REQUESTED','APPROVED','REJECTED','EXPIRED','CANCELLED','CONSUMED')),
  decision    TEXT NOT NULL DEFAULT '',
  response    BLOB,
  decided_by  TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  resolved_at INTEGER NOT NULL DEFAULT 0,
  version     INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX approvals_status ON approvals(status);
CREATE INDEX approvals_run ON approvals(run_id);

CREATE TABLE watchdog_trips (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id    TEXT NOT NULL DEFAULT '',
  run_id     TEXT NOT NULL DEFAULT '',
  node_id    TEXT NOT NULL DEFAULT '',
  attempt    INTEGER NOT NULL DEFAULT 0,
  agent_id   TEXT NOT NULL DEFAULT '',
  signal     TEXT NOT NULL,
  stage      INTEGER NOT NULL,
  evidence   TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL
);
CREATE INDEX watchdog_trips_task ON watchdog_trips(task_id);

CREATE TABLE handoffs (
  id         TEXT PRIMARY KEY,
  task_id    TEXT NOT NULL DEFAULT '',
  run_id     TEXT NOT NULL DEFAULT '',
  agent_id   TEXT NOT NULL DEFAULT '',
  artifact   TEXT NOT NULL,
  reason     TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);

CREATE TABLE dead_letters (
  id         TEXT PRIMARY KEY,
  task_id    TEXT NOT NULL,
  template   TEXT NOT NULL DEFAULT '',
  reason     TEXT NOT NULL,
  detail     TEXT NOT NULL DEFAULT '{}',
  resolved   INTEGER NOT NULL DEFAULT 0,
  note       TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE dead_letters; DROP TABLE handoffs; DROP TABLE watchdog_trips; DROP TABLE approvals;
DROP TABLE run_leases; DROP TABLE node_executions; DROP TABLE workflow_checkpoints; DROP TABLE workflow_runs;

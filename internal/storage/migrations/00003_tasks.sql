-- +goose Up
CREATE TABLE tasks (
  id            TEXT PRIMARY KEY,
  project_id    TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  seq           INTEGER NOT NULL,
  title         TEXT NOT NULL,
  description   TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL,
  priority      INTEGER NOT NULL DEFAULT 0,
  assignee      TEXT NOT NULL DEFAULT '',
  reviewer      TEXT NOT NULL DEFAULT '',
  template      TEXT NOT NULL DEFAULT '',
  files         TEXT NOT NULL DEFAULT '[]',
  acceptance    TEXT NOT NULL DEFAULT '[]',
  worktree      TEXT NOT NULL DEFAULT '',
  branch        TEXT NOT NULL DEFAULT '',
  base_branch   TEXT NOT NULL DEFAULT '',
  iteration     INTEGER NOT NULL DEFAULT 0,
  max_iterations INTEGER NOT NULL DEFAULT 5,
  review_feedback TEXT NOT NULL DEFAULT '',
  verdict       TEXT NOT NULL DEFAULT '',
  model         TEXT NOT NULL DEFAULT '',
  effort        TEXT NOT NULL DEFAULT '',
  cost_usd      REAL NOT NULL DEFAULT 0,
  issue_ref     TEXT NOT NULL DEFAULT '',
  pr_ref        TEXT NOT NULL DEFAULT '',
  run_id        TEXT NOT NULL DEFAULT '',
  blocked_reason TEXT NOT NULL DEFAULT '',
  accepted_by   TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  started_at    INTEGER NOT NULL DEFAULT 0,
  completed_at  INTEGER NOT NULL DEFAULT 0,
  version       INTEGER NOT NULL DEFAULT 1,
  UNIQUE (project_id, seq)
);
CREATE INDEX tasks_status ON tasks(project_id, status);

CREATE TABLE task_deps (
  task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  depends_on TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  PRIMARY KEY (task_id, depends_on)
);

CREATE TABLE task_evidence (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  attempt    INTEGER NOT NULL DEFAULT 0,
  kind       TEXT NOT NULL,      -- test | build | lint | typecheck | diff | commit | check | screenshot | pr | preview
  passed     INTEGER,            -- NULL when not applicable
  summary    TEXT NOT NULL DEFAULT '',
  ref        TEXT NOT NULL DEFAULT '',   -- commit id, file path of the full log, URL
  data       TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL
);
CREATE INDEX task_evidence_task ON task_evidence(task_id, id);

CREATE TABLE task_reviews (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  attempt    INTEGER NOT NULL DEFAULT 0,
  reviewer   TEXT NOT NULL,      -- agent id or 'human:<name>'
  verdict    TEXT NOT NULL CHECK (verdict IN ('SHIP','REVISE')),
  feedback   TEXT NOT NULL DEFAULT '',
  model      TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);

CREATE TABLE receipts (
  scope      TEXT NOT NULL,
  receipt_id TEXT NOT NULL,
  kind       TEXT NOT NULL,
  data       TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL,
  PRIMARY KEY (scope, receipt_id)
);

-- +goose Down
DROP TABLE receipts; DROP TABLE task_reviews; DROP TABLE task_evidence; DROP TABLE task_deps; DROP TABLE tasks;

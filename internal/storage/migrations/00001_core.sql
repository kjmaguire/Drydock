-- +goose Up
CREATE TABLE events (
  seq        INTEGER PRIMARY KEY AUTOINCREMENT,
  ts         INTEGER NOT NULL,
  type       TEXT    NOT NULL,
  project_id TEXT    NOT NULL DEFAULT '',
  run_id     TEXT    NOT NULL DEFAULT '',
  task_id    TEXT    NOT NULL DEFAULT '',
  agent_id   TEXT    NOT NULL DEFAULT '',
  data       BLOB    NOT NULL DEFAULT x'7b7d'
);
CREATE INDEX events_type ON events(type, seq);
CREATE INDEX events_run  ON events(run_id, seq) WHERE run_id <> '';
CREATE INDEX events_task ON events(task_id, seq) WHERE task_id <> '';

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE workspaces (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE projects (
  id           TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL DEFAULT '',
  name         TEXT NOT NULL,
  repo_path    TEXT NOT NULL,
  default_branch TEXT NOT NULL DEFAULT 'main',
  merge_strategy TEXT NOT NULL DEFAULT 'merge',
  mode         TEXT NOT NULL DEFAULT 'local_merge',
  created_at   INTEGER NOT NULL,
  version      INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE providers (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  kind          TEXT NOT NULL CHECK (kind IN ('aggregator','direct','host')),
  base_url      TEXT NOT NULL,
  api_key_ref   TEXT NOT NULL DEFAULT '',
  data_class    TEXT NOT NULL CHECK (data_class IN ('private','trainable')),
  tier          TEXT NOT NULL CHECK (tier IN ('premium','cheap','free')),
  max_concurrent_agents INTEGER NOT NULL DEFAULT 0,
  hourly_cost   REAL NOT NULL DEFAULT 0,
  status        TEXT NOT NULL DEFAULT 'unknown',
  last_seen     INTEGER NOT NULL DEFAULT 0,
  server_info   TEXT NOT NULL DEFAULT '{}',
  created_at    INTEGER NOT NULL
);

CREATE TABLE model_catalog (
  provider_id       TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  model             TEXT NOT NULL,
  context_length    INTEGER NOT NULL DEFAULT 0,
  supports_tools    INTEGER NOT NULL DEFAULT 0,
  supports_reasoning INTEGER NOT NULL DEFAULT 0,
  quality_tier      TEXT NOT NULL DEFAULT 'unknown',
  data_class        TEXT NOT NULL DEFAULT 'private',
  tier              TEXT NOT NULL DEFAULT 'cheap',
  input_price_per_m  REAL NOT NULL DEFAULT 0,
  output_price_per_m REAL NOT NULL DEFAULT 0,
  cached_price_per_m REAL NOT NULL DEFAULT 0,
  price_updated_at  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (provider_id, model)
);

CREATE TABLE agents (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  adapter     TEXT NOT NULL,            -- manifest id: claude, codex, opencode, ...
  role        TEXT NOT NULL,
  model       TEXT NOT NULL DEFAULT '',
  effort      TEXT NOT NULL DEFAULT '',
  provider_id TEXT NOT NULL DEFAULT '', -- model provider for OpenAI-compatible agents
  permission_mode TEXT NOT NULL DEFAULT 'default',
  config      TEXT NOT NULL DEFAULT '{}',
  status      TEXT NOT NULL DEFAULT 'STOPPED',
  created_at  INTEGER NOT NULL,
  version     INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE agent_runs (
  id          TEXT PRIMARY KEY,
  agent_id    TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  task_id     TEXT NOT NULL DEFAULT '',
  session_id  TEXT NOT NULL DEFAULT '',
  pid         INTEGER NOT NULL DEFAULT 0,
  cwd         TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL,
  started_at  INTEGER NOT NULL,
  ended_at    INTEGER NOT NULL DEFAULT 0,
  exit_code   INTEGER,
  error       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX agent_runs_agent ON agent_runs(agent_id, started_at);

CREATE TABLE sessions (
  id          TEXT PRIMARY KEY,
  agent_id    TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  task_id     TEXT NOT NULL DEFAULT '',
  external_id TEXT NOT NULL DEFAULT '', -- the CLI's session id
  model       TEXT NOT NULL DEFAULT '',
  occupancy_pct REAL NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  ended_at    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE model_usage (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  ts            INTEGER NOT NULL,
  run_id        TEXT NOT NULL DEFAULT '',
  agent_run_id  TEXT NOT NULL DEFAULT '',
  task_id       TEXT NOT NULL DEFAULT '',
  agent_id      TEXT NOT NULL DEFAULT '',
  provider      TEXT NOT NULL DEFAULT '',
  host_id       TEXT,
  model         TEXT NOT NULL DEFAULT '',
  tier          TEXT NOT NULL DEFAULT '',
  input_tokens  INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  cached_tokens INTEGER NOT NULL DEFAULT 0,
  cache_write_tokens INTEGER NOT NULL DEFAULT 0,
  reasoning_tokens INTEGER NOT NULL DEFAULT 0,
  cost_usd      REAL NOT NULL DEFAULT 0,
  source        TEXT NOT NULL DEFAULT 'reported' CHECK (source IN ('reported','estimated')),
  cache_hit     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX model_usage_task ON model_usage(task_id);
CREATE INDEX model_usage_agent ON model_usage(agent_run_id);

CREATE TABLE rooms (
  id         TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL CHECK (kind IN ('channel','task','dm','group')),
  name       TEXT NOT NULL,
  topic      TEXT NOT NULL DEFAULT '',
  mode       TEXT NOT NULL DEFAULT 'auto',
  task_id    TEXT NOT NULL DEFAULT '',
  archived   INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  version    INTEGER NOT NULL DEFAULT 1
);
CREATE UNIQUE INDEX rooms_name ON rooms(project_id, kind, name);

CREATE TABLE room_members (
  room_id   TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
  member_id TEXT NOT NULL,             -- agent id, 'human:<name>', or tool member 'tool:tests'
  kind      TEXT NOT NULL CHECK (kind IN ('agent','human','tool')),
  last_read_seq INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (room_id, member_id)
);

CREATE TABLE messages (
  id         TEXT PRIMARY KEY,
  room_id    TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
  thread_id  TEXT NOT NULL DEFAULT '',
  author     TEXT NOT NULL,
  type       TEXT NOT NULL,
  body       TEXT NOT NULL DEFAULT '',
  refs       TEXT NOT NULL DEFAULT '{}',   -- task, file, commit, workflow node, pack citation
  run_id     TEXT NOT NULL DEFAULT '',
  event_seq  INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE INDEX messages_room ON messages(room_id, created_at);
CREATE INDEX messages_thread ON messages(thread_id) WHERE thread_id <> '';

-- +goose Down
DROP TABLE messages; DROP TABLE room_members; DROP TABLE rooms; DROP TABLE model_usage;
DROP TABLE sessions; DROP TABLE agent_runs; DROP TABLE agents; DROP TABLE model_catalog;
DROP TABLE providers; DROP TABLE projects; DROP TABLE workspaces; DROP TABLE settings; DROP TABLE events;

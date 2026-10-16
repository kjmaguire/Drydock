-- +goose Up
-- Per-project verification settings: the secrets check defaults ON, the dependency check defaults OFF (brief s13a).
CREATE TABLE project_check_settings (
  project_id      TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
  secrets_enabled INTEGER NOT NULL DEFAULT 1,
  deps_enabled    INTEGER NOT NULL DEFAULT 0,
  deps_offline    INTEGER NOT NULL DEFAULT 0,      -- pass --offline to osv-scanner (no package metadata leaves the machine)
  updated_at      INTEGER NOT NULL,
  updated_by      TEXT NOT NULL
);

-- Startup tool-call probe results (docs/model-routing.md s5): one row per probed model run set.
CREATE TABLE probe_results (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  provider_id  TEXT NOT NULL,
  model        TEXT NOT NULL,
  ts           INTEGER NOT NULL,
  pass         INTEGER NOT NULL,
  runs         INTEGER NOT NULL,
  passes       INTEGER NOT NULL,
  latency_ms   INTEGER NOT NULL DEFAULT 0,         -- mean over the runs that completed
  failure_mode TEXT NOT NULL DEFAULT '',           -- no_tool_call|bad_arguments|wrong_tool|leaked_tool_text|unavailable_private|error:<kind>
  endpoint     TEXT NOT NULL DEFAULT '',           -- the model id the provider reports having served
  data_class   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX probe_results_model ON probe_results(provider_id, model, ts);

-- +goose Down
DROP TABLE probe_results;
DROP TABLE project_check_settings;

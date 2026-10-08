-- +goose Up
-- Local telemetry sink (decision D12). Off by default; attribute names are Drydock's own constants. attrs holds only
-- allowlisted keys with identifier-shaped values: never prompts, code, tool output or secrets.
CREATE TABLE telemetry_spans (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  trace_id   TEXT NOT NULL,
  span_id    TEXT NOT NULL,
  parent_id  TEXT NOT NULL DEFAULT '',
  name       TEXT NOT NULL,
  start_ms   INTEGER NOT NULL,
  end_ms     INTEGER NOT NULL,
  status     TEXT NOT NULL DEFAULT 'ok',           -- ok|error
  attrs      TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX telemetry_spans_start ON telemetry_spans(start_ms);

CREATE TABLE telemetry_metrics (
  id     INTEGER PRIMARY KEY AUTOINCREMENT,
  ts     INTEGER NOT NULL,
  name   TEXT NOT NULL,
  kind   TEXT NOT NULL CHECK (kind IN ('counter','histogram','gauge')),
  unit   TEXT NOT NULL DEFAULT '',
  value  REAL NOT NULL,
  attrs  TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX telemetry_metrics_ts ON telemetry_metrics(ts);

-- +goose Down
DROP TABLE telemetry_metrics;
DROP TABLE telemetry_spans;

-- +goose Up
-- Every Test connection run of an agent CLI (decision P132): the Agents page lists them under Test results as the install's
-- conformance history, and draws its capability matrix from the newest. conn_key is the install (the adapter id, or the key of
-- its install inside a WSL distro); passed, failed and skipped count the steps; reason is the first failed step's, redacted;
-- matrix is the report's capability matrix as JSON (each cell a value and where it came from). Only the newest 30 runs of an
-- install are kept. The settings row connection.test.<key> still holds the last outcome that degrades a Connection.
CREATE TABLE connection_tests (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  conn_key    TEXT NOT NULL,
  adapter     TEXT NOT NULL,
  cli_version TEXT NOT NULL DEFAULT '',
  transport   TEXT NOT NULL DEFAULT '',
  ok          INTEGER NOT NULL,
  prompt      INTEGER NOT NULL DEFAULT 0,
  passed      INTEGER NOT NULL DEFAULT 0,
  failed      INTEGER NOT NULL DEFAULT 0,
  skipped     INTEGER NOT NULL DEFAULT 0,
  reason      TEXT NOT NULL DEFAULT '',
  matrix      TEXT NOT NULL DEFAULT '{}',
  elapsed_ms  INTEGER NOT NULL DEFAULT 0,
  tested_at   INTEGER NOT NULL
);
CREATE INDEX connection_tests_key ON connection_tests(conn_key, id);

-- +goose Down
DROP TABLE connection_tests;

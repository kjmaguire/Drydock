-- +goose Up
-- One row per security scan of a project's working tree (decision P44, docs/security-scan.md). report_json is the whole
-- scan.Report after redaction: engines, counts and findings that name a rule, a file and a line. No finding carries a
-- secret's value or a line of code. Scans are kept so the Security page can show the history; a scan in progress is
-- not here (it lives in memory until it ends).
CREATE TABLE security_scans (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL,
  status      TEXT NOT NULL,              -- done | cancelled
  started_by  TEXT NOT NULL DEFAULT '',   -- human:<name>
  started_at  INTEGER NOT NULL,
  finished_at INTEGER NOT NULL,
  counts_json TEXT NOT NULL DEFAULT '{}',
  summary     TEXT NOT NULL DEFAULT '',
  report_json TEXT NOT NULL
);
CREATE INDEX security_scans_project ON security_scans(project_id, started_at DESC);

-- +goose Down
DROP TABLE security_scans;

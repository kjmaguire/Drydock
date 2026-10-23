-- +goose Up
-- Findings of the security scan a person dismissed (decision P97): "not a real key", an accepted risk, or a false positive.
-- One row per project, or folder of a folder scan, and finding fingerprint (scan.Fingerprint: a hash of the engine, the rule,
-- the file and the finding's line with any secret masked, never a secret's value). Every later report of the target marks the
-- finding and leaves it out of its counts. engine, rule, title, file and line say what was dismissed for the list of
-- dismissed findings; the title is the report's, redacted. Restoring a finding deletes its row.
CREATE TABLE security_dismissals (
  target_key   TEXT NOT NULL,              -- the project id, or 'folder:' + the folder (security_scans.project_id)
  fingerprint  TEXT NOT NULL,
  reason       TEXT NOT NULL CHECK (reason IN ('not_real','accepted_risk','false_positive')),
  dismissed_by TEXT NOT NULL,              -- human:<name>
  dismissed_at INTEGER NOT NULL,
  scan_id      TEXT NOT NULL DEFAULT '',   -- the scan whose report it was dismissed in
  engine       TEXT NOT NULL DEFAULT '',
  rule         TEXT NOT NULL DEFAULT '',
  title        TEXT NOT NULL DEFAULT '',
  file         TEXT NOT NULL DEFAULT '',
  line         INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (target_key, fingerprint)
);

-- +goose Down
DROP TABLE security_dismissals;

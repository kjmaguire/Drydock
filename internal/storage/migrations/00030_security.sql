-- +goose Up
-- Approvals that are not bound to a workflow run (security.md s5) need an expiry and the security kind; workflow-bound
-- rows keep the defaults (expires_at 0 = no expiry, action_kind '').
ALTER TABLE approvals ADD COLUMN expires_at  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE approvals ADD COLUMN action_kind TEXT    NOT NULL DEFAULT '';
CREATE INDEX approvals_expiry ON approvals(expires_at) WHERE expires_at > 0 AND status IN ('REQUESTED','APPROVED');

-- Append-only, hash-chained audit log (security.md s12). hash = SHA-256(prev_hash || canonicalJSON(row without hash)).
CREATE TABLE audit_log (
  seq       INTEGER PRIMARY KEY,
  ts        INTEGER NOT NULL,
  actor     TEXT    NOT NULL,
  action    TEXT    NOT NULL,
  subject   TEXT    NOT NULL DEFAULT '',
  detail    TEXT    NOT NULL DEFAULT '{}',
  prev_hash TEXT    NOT NULL,
  hash      TEXT    NOT NULL
);
CREATE INDEX audit_log_action ON audit_log(action, seq);
CREATE INDEX audit_log_subject ON audit_log(subject, seq) WHERE subject <> '';

-- Defense in depth: the application never updates or deletes audit rows, and the database refuses to.
-- Verify() detects edits made with these triggers dropped.
-- +goose StatementBegin
CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
BEGIN SELECT RAISE(ABORT, 'audit_log is append-only'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
BEGIN SELECT RAISE(ABORT, 'audit_log is append-only'); END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER audit_log_no_delete;
DROP TRIGGER audit_log_no_update;
DROP TABLE audit_log;
DROP INDEX approvals_expiry;
ALTER TABLE approvals DROP COLUMN action_kind;
ALTER TABLE approvals DROP COLUMN expires_at;

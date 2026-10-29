-- +goose Up
-- Auto-approve classes (decision P114, docs/integrations.md section 4): a person may let one narrow, templated write class of one
-- connection in one project go out without being asked each time. Off by default (no row, or enabled 0); only a human creates,
-- changes or removes a row; a destructive or signal-suppressing operation is never in a class (the code has a closed list). An
-- outbox row sent under a class records it in auto_class, and its approval is decided in the name of the person who turned the
-- class on.
CREATE TABLE integration_auto_approve (
  integration_id TEXT NOT NULL,
  project_id     TEXT NOT NULL,
  class          TEXT NOT NULL CHECK (class IN ('linkback_comment','status_sync')),
  enabled        INTEGER NOT NULL DEFAULT 0,
  created_by     TEXT NOT NULL,
  updated_by     TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  version        INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY (integration_id, project_id, class)
);
ALTER TABLE integration_outbox ADD COLUMN auto_class TEXT NOT NULL DEFAULT '';
-- the short reference a comment carries ("drydock:<marker>"), so a retry first looks on the vendor for the comment that may
-- already have arrived instead of posting it twice
ALTER TABLE integration_outbox ADD COLUMN marker TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE integration_outbox DROP COLUMN marker;
ALTER TABLE integration_outbox DROP COLUMN auto_class;
DROP TABLE integration_auto_approve;

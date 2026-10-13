-- +goose Up
-- One row per skill registry Drydock has checked for new or changed skills (decision P46, docs/expert-packs.md section 9).
-- remote_json is the list of skills the registry held at last_commit (name, description and the hash of SKILL.md; never the
-- text), so the Skills page can say, against a project's skills now, what is new there and what changed upstream.
-- notified_commit is the commit whose changes were already told to the projects. A failed check keeps the previous commit and
-- snapshot and records last_error.
CREATE TABLE skill_registry_watch (
  registry_id     TEXT PRIMARY KEY,
  last_commit     TEXT NOT NULL DEFAULT '',
  checked_at      INTEGER NOT NULL DEFAULT 0,
  notified_commit TEXT NOT NULL DEFAULT '',
  remote_json     TEXT NOT NULL DEFAULT '[]',
  last_error      TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE skill_registry_watch;

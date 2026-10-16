-- +goose Up
-- The workspace skill library (decision P48, docs/expert-packs.md section 9): every skill of every enabled registry, imported
-- by Drydock itself (at start and twice a day) so a project only chooses. One row per registry skill at the registry's last
-- synced commit; body is the SKILL.md text (empty when a secret was found). status is 'available' or 'blocked' (unlicensed,
-- a secret, injection: never addable). gone marks a skill the registry no longer holds; its row stays so projects that use it
-- keep their copy and the page can say so.
CREATE TABLE skill_library (
  id            TEXT PRIMARY KEY,
  registry_id   TEXT NOT NULL,
  name          TEXT NOT NULL,
  description   TEXT NOT NULL DEFAULT '',
  body          TEXT NOT NULL,
  license       TEXT NOT NULL DEFAULT '',
  compatibility TEXT NOT NULL DEFAULT '',
  globs         TEXT NOT NULL DEFAULT '[]',
  roles         TEXT NOT NULL DEFAULT '[]',
  source_path   TEXT NOT NULL DEFAULT '',
  pinned_commit TEXT NOT NULL DEFAULT '',
  content_hash  TEXT NOT NULL,
  has_scripts   INTEGER NOT NULL DEFAULT 0,
  allowed_tools_ignored INTEGER NOT NULL DEFAULT 0,
  status        TEXT NOT NULL CHECK (status IN ('available','blocked')),
  risk          INTEGER NOT NULL DEFAULT 0,
  flags         TEXT NOT NULL DEFAULT '',
  block_reason  TEXT NOT NULL DEFAULT '',
  parse_notes   TEXT NOT NULL DEFAULT '[]',
  gone          INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  UNIQUE (registry_id, name)
);
CREATE INDEX skill_library_registry ON skill_library(registry_id, status);

-- Skill uses (decision P49): an agent opened a skill it had on demand (Claude read its file, or an agent fetched it through
-- the skills tool). Deliveries say what reached a run; uses say what was read. Names only, never the text.
CREATE TABLE skill_uses (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  ts         INTEGER NOT NULL,
  project_id TEXT NOT NULL,
  task_id    TEXT NOT NULL DEFAULT '',
  agent_id   TEXT NOT NULL DEFAULT '',
  run_id     TEXT NOT NULL DEFAULT '',
  skill_name TEXT NOT NULL,
  how        TEXT NOT NULL                      -- file_read | tool
);
CREATE INDEX skill_uses_project ON skill_uses(project_id, skill_name);

-- +goose Down
DROP TABLE skill_uses;
DROP TABLE skill_library;

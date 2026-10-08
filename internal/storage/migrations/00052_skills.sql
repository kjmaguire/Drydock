-- +goose Up
-- Skills (docs/expert-packs.md section 9): glob-scoped Markdown rules resolved from repository scans and from an explicit
-- allowlist of curated registries, pinned to commits, scanned before use. Skills from outside the repository are only
-- 'offered' until a human accepts them; only accepted skills are ever written into a worktree or served to an agent.
CREATE TABLE skill_registries (
  id            TEXT PRIMARY KEY,
  repo          TEXT NOT NULL,                  -- owner/name on github.com
  root          TEXT NOT NULL DEFAULT '',       -- directory inside the repo that holds the skills
  license       TEXT NOT NULL DEFAULT '',
  license_scope TEXT NOT NULL DEFAULT '',       -- '' or 'per_skill'
  trust         TEXT NOT NULL DEFAULT 'community',
  import_mode   TEXT NOT NULL DEFAULT '',       -- '', 'selective_only', 'per_skill_review'
  notes         TEXT NOT NULL DEFAULT '',
  enabled       INTEGER NOT NULL DEFAULT 1,
  curated       INTEGER NOT NULL DEFAULT 0,     -- shipped with the app (0 = added by a human)
  added_by      TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL
);

CREATE TABLE skills (
  id             TEXT PRIMARY KEY,
  project_id     TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name           TEXT NOT NULL,
  description    TEXT NOT NULL DEFAULT '',
  body           TEXT NOT NULL,
  license        TEXT NOT NULL DEFAULT '',
  compatibility  TEXT NOT NULL DEFAULT '',
  globs          TEXT NOT NULL DEFAULT '[]',    -- JSON array
  roles          TEXT NOT NULL DEFAULT '[]',    -- JSON array
  origin         TEXT NOT NULL CHECK (origin IN ('repo','registry')),
  registry_id    TEXT NOT NULL DEFAULT '',
  source_path    TEXT NOT NULL DEFAULT '',      -- repo-relative path of the SKILL.md (or the path inside the registry repo)
  pinned_commit  TEXT NOT NULL DEFAULT '',
  content_hash   TEXT NOT NULL,
  has_scripts    INTEGER NOT NULL DEFAULT 0,    -- scripts/ exist upstream; they are never delivered
  allowed_tools_ignored INTEGER NOT NULL DEFAULT 0,
  status         TEXT NOT NULL CHECK (status IN ('offered','accepted','rejected','blocked')),
  risk           INTEGER NOT NULL DEFAULT 0,
  flags          TEXT NOT NULL DEFAULT '',
  block_reason   TEXT NOT NULL DEFAULT '',
  decided_by     TEXT NOT NULL DEFAULT '',
  decided_at     INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  UNIQUE (project_id, name)
);
CREATE INDEX skills_project ON skills(project_id, status);

-- How a skill reached an agent (docs/expert-packs.md section 9: on-demand delivery cannot be guaranteed, so the method and
-- fidelity are recorded and evals can report coverage).
CREATE TABLE skill_deliveries (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  ts           INTEGER NOT NULL,
  project_id   TEXT NOT NULL DEFAULT '',
  task_id      TEXT NOT NULL DEFAULT '',
  agent_id     TEXT NOT NULL DEFAULT '',
  run_id       TEXT NOT NULL DEFAULT '',
  skill_name   TEXT NOT NULL,
  content_hash TEXT NOT NULL DEFAULT '',
  method       TEXT NOT NULL,                   -- claude_rules | nested_file | agents_skills | tool
  fidelity     TEXT NOT NULL,                   -- exact | run_start | on_demand
  path         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX skill_deliveries_task ON skill_deliveries(task_id);

-- +goose Down
DROP TABLE skill_deliveries; DROP TABLE skills; DROP TABLE skill_registries;

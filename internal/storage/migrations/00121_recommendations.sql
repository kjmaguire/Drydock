-- +goose Up
-- Recommendations (decision P52): one agent turn reads the project (brief, plan, team, recent tasks, its skills and MCP
-- servers) and what the workspace offers (the skill library, connected MCP servers and integrations) and says which agent
-- should get which skill, server or integration, and why. One row per turn; items is the validated list, each with its own
-- status (open, applied, dismissed). trigger says who asked: the person ('manual'), the person after picking skills in the
-- Library ('skills_added', candidates holds those picks), or the project itself as it evolved ('evolution', bounded by
-- tasks completed since the last turn: tasks_done is the project's count of completed tasks when this one ran).
CREATE TABLE recommendations (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL,
  agent_id    TEXT NOT NULL DEFAULT '',
  trigger     TEXT NOT NULL CHECK (trigger IN ('manual','skills_added','evolution')),
  status      TEXT NOT NULL CHECK (status IN ('running','done','failed')),
  candidates  TEXT NOT NULL DEFAULT '[]',
  summary     TEXT NOT NULL DEFAULT '',
  items       TEXT NOT NULL DEFAULT '[]',
  dropped     TEXT NOT NULL DEFAULT '[]',
  error       TEXT NOT NULL DEFAULT '',
  cost_usd    REAL NOT NULL DEFAULT 0,
  tasks_done  INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  finished_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX recommendations_project ON recommendations(project_id, created_at DESC);

-- +goose Down
DROP TABLE recommendations;

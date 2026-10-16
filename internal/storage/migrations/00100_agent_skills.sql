-- +goose Up
-- Per-agent skill assignment. A skill with at least one row here is delivered only to the agents listed (still subject to its
-- globs and to being accepted); a skill with no row keeps the role-based delivery of its drydock.roles metadata. The rows only
-- narrow who receives a skill: they can never make an offered, rejected or blocked skill reach anyone.
CREATE TABLE agent_skills (
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  agent_id    TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  skill_name  TEXT NOT NULL,
  assigned_by TEXT NOT NULL DEFAULT '',
  assigned_at INTEGER NOT NULL,
  PRIMARY KEY (project_id, agent_id, skill_name)
);
CREATE INDEX agent_skills_skill ON agent_skills(project_id, skill_name);

-- +goose Down
DROP TABLE agent_skills;

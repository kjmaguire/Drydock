-- +goose Up
-- Agent templates: a team member defined once in the workspace (CLI, model, effort, role, purpose, permission mode and the library
-- MCP servers it should get) and dropped into any project, where it becomes an ordinary agent of that project. A template is
-- never linked to the agents made from it: changing it later changes nothing that already exists.
CREATE TABLE agent_templates (
  id              TEXT PRIMARY KEY,
  name            TEXT NOT NULL,
  role            TEXT NOT NULL,
  purpose         TEXT NOT NULL DEFAULT '',
  adapter         TEXT NOT NULL,
  model           TEXT NOT NULL DEFAULT '',
  effort          TEXT NOT NULL DEFAULT '',
  permission_mode TEXT NOT NULL DEFAULT '',
  provider_id     TEXT NOT NULL DEFAULT '',
  mcp_servers     TEXT NOT NULL DEFAULT '[]',   -- JSON array of library server ids granted when the template is applied
  created_by      TEXT NOT NULL DEFAULT '',
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);

-- +goose Down
DROP TABLE agent_templates;

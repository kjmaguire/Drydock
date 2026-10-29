-- +goose Up
-- The per-role MCP tool table (decision P115, docs/mcp.md section 3). The defaults are in the code (internal/mcp/rolepolicy.go);
-- a row here is one cell a person changed for one project: the role's row, the column, and whether its tools are on. Deleting
-- the row puts the cell back to its default.
CREATE TABLE mcp_role_policy (
  project_id TEXT NOT NULL,
  role TEXT NOT NULL,
  col TEXT NOT NULL,
  allowed INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  updated_by TEXT NOT NULL,
  PRIMARY KEY (project_id, role, col)
);
-- A project's CEL rules: each takes the tools its expression is true for away from the project's runs. A rule cannot give a
-- tool; the expression is checked when it is saved and again when a run is planned.
CREATE TABLE mcp_policy_rules (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  name TEXT NOT NULL,
  expr TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  created_by TEXT NOT NULL
);
CREATE INDEX mcp_policy_rules_project ON mcp_policy_rules(project_id, created_at);

-- +goose Down
DROP INDEX mcp_policy_rules_project;
DROP TABLE mcp_policy_rules;
DROP TABLE mcp_role_policy;

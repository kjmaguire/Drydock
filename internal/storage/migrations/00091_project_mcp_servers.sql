-- +goose Up
-- MCP servers the human adds to a project (docs/mcp.md "user-added"), and which agents of that project may use them.
-- A server's credentials are NOT here: the bearer token and secret environment values live in the credential vault under
-- mcp:<project>:<id>:token and mcp:<project>:<id>:env:<NAME>; this table keeps only whether they exist and the variable names.
CREATE TABLE project_mcp_servers (
  project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  id              TEXT NOT NULL,                 -- short slug; the tools an agent sees are <id>__<tool>
  display         TEXT NOT NULL,
  transport       TEXT NOT NULL,                 -- stdio | http
  url             TEXT NOT NULL DEFAULT '',
  argv            TEXT NOT NULL DEFAULT '[]',    -- JSON array: the command that starts a stdio server
  cwd             TEXT NOT NULL DEFAULT '',
  env             TEXT NOT NULL DEFAULT '{}',    -- JSON object of non-secret environment entries
  secret_env      TEXT NOT NULL DEFAULT '[]',    -- JSON array of variable NAMES whose values are in the vault
  has_token       INTEGER NOT NULL DEFAULT 0,    -- a bearer token is in the vault (http servers)
  guard           INTEGER NOT NULL DEFAULT 1,    -- limit what agents may send out (code, files, secrets, long text)
  read_only_tools TEXT NOT NULL DEFAULT '[]',    -- JSON array of tool names/globs the HUMAN declared read-only
  tools           TEXT NOT NULL DEFAULT '[]',    -- JSON array of discovered tools (name, description, schema, claimed readOnly)
  discovered_at   INTEGER NOT NULL DEFAULT 0,
  enabled         INTEGER NOT NULL DEFAULT 1,
  created_by      TEXT NOT NULL DEFAULT '',
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL,
  PRIMARY KEY (project_id, id)
);

-- Deny by default: a server reaches an agent only through a row here.
CREATE TABLE agent_mcp_access (
  project_id TEXT NOT NULL,
  server_id  TEXT NOT NULL,
  agent_id   TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  tools      TEXT NOT NULL DEFAULT '["*"]',      -- JSON array of tool-name globs this agent may call
  granted_by TEXT NOT NULL DEFAULT '',
  granted_at INTEGER NOT NULL,
  PRIMARY KEY (project_id, server_id, agent_id),
  FOREIGN KEY (project_id, server_id) REFERENCES project_mcp_servers(project_id, id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE agent_mcp_access;
DROP TABLE project_mcp_servers;

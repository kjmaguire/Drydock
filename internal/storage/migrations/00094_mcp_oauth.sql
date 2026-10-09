-- +goose Up
-- OAuth for http MCP servers (docs/mcp.md "OAuth"). The tokens and the registered client are in the credential vault under
-- mcp:<id>:oauth; this table keeps only that the server is set up for OAuth and when the human last signed in.
ALTER TABLE mcp_library ADD COLUMN oauth INTEGER NOT NULL DEFAULT 0;
ALTER TABLE mcp_library ADD COLUMN oauth_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE mcp_library ADD COLUMN oauth_scope TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE mcp_library DROP COLUMN oauth_scope;
ALTER TABLE mcp_library DROP COLUMN oauth_at;
ALTER TABLE mcp_library DROP COLUMN oauth;

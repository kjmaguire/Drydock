-- +goose Up
-- An OAuth client the human registered with an MCP server's provider (decision P122), for a provider without dynamic client
-- registration (GitHub's hosted MCP server). Only that one is set is kept here; its id and secret are in the credential vault
-- (mcp:<id>:oauth-client) next to the tokens.
ALTER TABLE mcp_library ADD COLUMN oauth_client INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE mcp_library DROP COLUMN oauth_client;

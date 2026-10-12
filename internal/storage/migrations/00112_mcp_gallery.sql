-- +goose Up
-- The MCP connector gallery (decision P43). A library server remembers which catalog entry it was connected from, what the
-- server said about itself when it was last reached (serverInfo and instructions from initialize, as JSON), the account it
-- reported (a login or email, never a token) and why the last connection failed. None of it is a secret.
ALTER TABLE mcp_library ADD COLUMN catalog_id TEXT NOT NULL DEFAULT '';
ALTER TABLE mcp_library ADD COLUMN server_info TEXT NOT NULL DEFAULT '';
ALTER TABLE mcp_library ADD COLUMN account TEXT NOT NULL DEFAULT '';
ALTER TABLE mcp_library ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
ALTER TABLE mcp_library ADD COLUMN last_error_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE mcp_library DROP COLUMN last_error_at;
ALTER TABLE mcp_library DROP COLUMN last_error;
ALTER TABLE mcp_library DROP COLUMN account;
ALTER TABLE mcp_library DROP COLUMN server_info;
ALTER TABLE mcp_library DROP COLUMN catalog_id;

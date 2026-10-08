-- +goose Up
-- MCP Gateway (docs/mcp.md sections 4 and 7). Raw cached results live on disk under <app data>/mcp-cache/ (redacted);
-- the row holds the key, the provenance of the entry and its expiry.
CREATE TABLE mcp_cache (
  key            TEXT PRIMARY KEY,   -- sha256(scope || server_id || server_ident || tool || canonical_json(args))
  server_id      TEXT    NOT NULL,
  tool           TEXT    NOT NULL,
  scope          TEXT    NOT NULL,   -- 'global' or 'project:<id>'
  server_version TEXT    NOT NULL DEFAULT '',
  args_hash      TEXT    NOT NULL DEFAULT '',
  raw_ref        TEXT    NOT NULL,   -- file name inside the cache directory
  bytes          INTEGER NOT NULL DEFAULT 0,
  is_error       INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  expires_at     INTEGER NOT NULL,
  hits           INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX mcp_cache_expiry ON mcp_cache(expires_at);
CREATE INDEX mcp_cache_server ON mcp_cache(server_id, tool);

-- Estimated token accounting per call (never mixed with provider-reported usage: source is always 'estimated' here).
CREATE TABLE mcp_usage (
  call_id                     TEXT PRIMARY KEY,
  run_id                      TEXT    NOT NULL,
  server_id                   TEXT    NOT NULL DEFAULT '',
  tool                        TEXT    NOT NULL DEFAULT '',
  tokens_est_returned         INTEGER NOT NULL DEFAULT 0,
  tokens_est_saved_truncation INTEGER NOT NULL DEFAULT 0,
  tokens_est_saved_cache      INTEGER NOT NULL DEFAULT 0,
  source                      TEXT    NOT NULL DEFAULT 'estimated',
  created_at                  INTEGER NOT NULL
);
CREATE INDEX mcp_usage_run ON mcp_usage(run_id);

-- +goose Down
DROP INDEX mcp_usage_run;
DROP TABLE mcp_usage;
DROP INDEX mcp_cache_server;
DROP INDEX mcp_cache_expiry;
DROP TABLE mcp_cache;

-- +goose Up
-- Providers, model routing and the LLM wrapper (docs/model-routing.md s4-s7, docs/token-control.md s8-s9).
-- providers, model_catalog and model_usage exist in 00001_core.sql; this migration extends them and adds the rest.

ALTER TABLE providers ADD COLUMN flavor TEXT NOT NULL DEFAULT 'openai';          -- openai|openrouter|gemini|anthropic|vllm|sglang|ollama
ALTER TABLE providers ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE providers ADD COLUMN tls_mode TEXT NOT NULL DEFAULT 'system';        -- system|pinned_fingerprint|none_loopback|none_private
ALTER TABLE providers ADD COLUMN tls_fingerprint TEXT NOT NULL DEFAULT '';       -- hex SHA-256 of the pinned leaf certificate
ALTER TABLE providers ADD COLUMN lan_unencrypted INTEGER NOT NULL DEFAULT 0;     -- plain http on a private (non-loopback) address
ALTER TABLE providers ADD COLUMN attestations TEXT NOT NULL DEFAULT '{}';        -- {"billing_enabled":true,"lan_attested":true}
ALTER TABLE providers ADD COLUMN wire_apis TEXT NOT NULL DEFAULT '["chat"]';     -- chat|responses|anthropic_messages
ALTER TABLE providers ADD COLUMN request_overrides TEXT NOT NULL DEFAULT '{}';   -- extra JSON merged into requests (OpenRouter provider object)
ALTER TABLE providers ADD COLUMN responses_validated_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE providers ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0;

ALTER TABLE model_catalog ADD COLUMN max_output INTEGER NOT NULL DEFAULT 0;
ALTER TABLE model_catalog ADD COLUMN supported_parameters TEXT NOT NULL DEFAULT '[]';
ALTER TABLE model_catalog ADD COLUMN is_free INTEGER NOT NULL DEFAULT 0;
ALTER TABLE model_catalog ADD COLUMN cache_write_price_per_m REAL NOT NULL DEFAULT 0;
ALTER TABLE model_catalog ADD COLUMN price_source TEXT NOT NULL DEFAULT 'catalog';  -- catalog|builtin|user (user rows are never overwritten)
ALTER TABLE model_catalog ADD COLUMN source TEXT NOT NULL DEFAULT 'listing';        -- listing|endpoints|user
ALTER TABLE model_catalog ADD COLUMN fetched_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE model_catalog ADD COLUMN probe_status TEXT NOT NULL DEFAULT '';
ALTER TABLE model_catalog ADD COLUMN probe_at INTEGER NOT NULL DEFAULT 0;

ALTER TABLE model_usage ADD COLUMN purpose TEXT NOT NULL DEFAULT '';
ALTER TABLE model_usage ADD COLUMN decision_id TEXT NOT NULL DEFAULT '';
ALTER TABLE model_usage ADD COLUMN latency_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE model_usage ADD COLUMN cost_source TEXT NOT NULL DEFAULT 'unknown';  -- reported|computed_from_table|unknown
ALTER TABLE model_usage ADD COLUMN est_saved_usd REAL NOT NULL DEFAULT 0;        -- cache hits: estimated saving, labeled estimated

-- Quota windows per provider (and optionally per model). limit_value is the window cap; used is consumed so far.
-- model '' = provider-wide; '*:free' = every model id ending in ':free'. source 'configured' is soft (decremented locally);
-- 'headers' and 'api' are authoritative (docs/model-routing.md s6).
CREATE TABLE provider_quotas (
  provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  model       TEXT NOT NULL DEFAULT '',
  window_kind TEXT NOT NULL CHECK (window_kind IN ('rpm','rpd','tpm','tpd','itpm','otpm','credits')),
  limit_value REAL NOT NULL,
  used        REAL NOT NULL DEFAULT 0,
  reset_at    INTEGER NOT NULL DEFAULT 0,
  reset_tz    TEXT NOT NULL DEFAULT 'UTC',
  source      TEXT NOT NULL CHECK (source IN ('configured','headers','api')),
  updated_at  INTEGER NOT NULL,
  PRIMARY KEY (provider_id, model, window_kind)
);

-- Exact-match response cache (docs/token-control.md s8). Never written for side-effecting calls.
CREATE TABLE llm_cache (
  key              TEXT PRIMARY KEY,           -- hex SHA-256 of the canonical request
  provider         TEXT NOT NULL,
  model            TEXT NOT NULL,
  response         BLOB NOT NULL,
  data_class       TEXT NOT NULL,
  purpose          TEXT NOT NULL DEFAULT '',
  project_id       TEXT NOT NULL DEFAULT '',
  pack_version     TEXT NOT NULL DEFAULT '',
  cost_usd_at_fill REAL NOT NULL DEFAULT 0,
  created_at       INTEGER NOT NULL,
  expires_at       INTEGER NOT NULL,
  hits             INTEGER NOT NULL DEFAULT 0,
  last_hit_at      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX llm_cache_expires ON llm_cache(expires_at);
CREATE INDEX llm_cache_project ON llm_cache(project_id) WHERE project_id <> '';

-- Secrets over the Windows Credential Manager blob limit (2560 bytes) live here, AES-256-GCM encrypted; the key is
-- held in the credential store (docs/security.md s8). Never holds plaintext.
CREATE TABLE secret_blobs (
  ref        TEXT PRIMARY KEY,
  nonce      BLOB NOT NULL,
  ciphertext BLOB NOT NULL,
  created_at INTEGER NOT NULL
);

-- One row per router decision (docs/model-routing.md s2 step 7).
CREATE TABLE routing_decisions (
  id          TEXT PRIMARY KEY,
  ts          INTEGER NOT NULL,
  run_id      TEXT NOT NULL DEFAULT '',
  task_id     TEXT NOT NULL DEFAULT '',
  node_id     TEXT NOT NULL DEFAULT '',
  attempt     INTEGER NOT NULL DEFAULT 0,
  run_kind    TEXT NOT NULL DEFAULT '',
  level       TEXT NOT NULL DEFAULT '',
  agent       TEXT NOT NULL DEFAULT '',
  provider_id TEXT NOT NULL DEFAULT '',
  model       TEXT NOT NULL DEFAULT '',
  effort      TEXT NOT NULL DEFAULT '',
  decided_by  TEXT NOT NULL,                   -- static|rules|explicit|llm
  est_cost_usd REAL,                           -- NULL = price unknown, never silently zero
  escalated_from TEXT NOT NULL DEFAULT '',
  request     BLOB NOT NULL,
  decision    BLOB NOT NULL
);
CREATE INDEX routing_decisions_task ON routing_decisions(task_id) WHERE task_id <> '';
CREATE INDEX routing_decisions_run  ON routing_decisions(run_id) WHERE run_id <> '';

-- Stored routing policies: CEL source text in when_expr, a declarative action in then_json (docs/model-routing.md s7).
CREATE TABLE routing_rules (
  id         TEXT PRIMARY KEY,
  scope      TEXT NOT NULL DEFAULT 'workspace',   -- workspace|project:<id>
  priority   INTEGER NOT NULL DEFAULT 50,
  when_expr  TEXT NOT NULL,
  then_json  TEXT NOT NULL,
  enabled    INTEGER NOT NULL DEFAULT 1,
  version    INTEGER NOT NULL DEFAULT 1,           -- compare-and-swap
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE routing_rules;
DROP TABLE routing_decisions;
DROP TABLE secret_blobs;
DROP TABLE llm_cache;
DROP TABLE provider_quotas;
ALTER TABLE model_usage DROP COLUMN est_saved_usd;
ALTER TABLE model_usage DROP COLUMN cost_source;
ALTER TABLE model_usage DROP COLUMN latency_ms;
ALTER TABLE model_usage DROP COLUMN decision_id;
ALTER TABLE model_usage DROP COLUMN purpose;
ALTER TABLE model_catalog DROP COLUMN probe_at;
ALTER TABLE model_catalog DROP COLUMN probe_status;
ALTER TABLE model_catalog DROP COLUMN fetched_at;
ALTER TABLE model_catalog DROP COLUMN source;
ALTER TABLE model_catalog DROP COLUMN price_source;
ALTER TABLE model_catalog DROP COLUMN cache_write_price_per_m;
ALTER TABLE model_catalog DROP COLUMN is_free;
ALTER TABLE model_catalog DROP COLUMN supported_parameters;
ALTER TABLE model_catalog DROP COLUMN max_output;
ALTER TABLE providers DROP COLUMN updated_at;
ALTER TABLE providers DROP COLUMN responses_validated_at;
ALTER TABLE providers DROP COLUMN request_overrides;
ALTER TABLE providers DROP COLUMN wire_apis;
ALTER TABLE providers DROP COLUMN attestations;
ALTER TABLE providers DROP COLUMN lan_unencrypted;
ALTER TABLE providers DROP COLUMN tls_fingerprint;
ALTER TABLE providers DROP COLUMN tls_mode;
ALTER TABLE providers DROP COLUMN enabled;
ALTER TABLE providers DROP COLUMN flavor;

-- +goose Up
-- Integrations (docs/integrations.md sections 2 to 6): Connections for Linear, Notion, Sentry and Aikido, polling cursors,
-- external items (always untrusted), human-authored task rules, and the approval-gated outbound outbox.
-- No secret is ever stored here: credential_ref names an entry in the credential store / Vault (providers.SecretStore).

CREATE TABLE integrations (
  id              TEXT PRIMARY KEY,
  kind            TEXT NOT NULL CHECK (kind IN ('linear','notion','sentry','aikido')),
  name            TEXT NOT NULL DEFAULT '',
  project_id      TEXT NOT NULL DEFAULT '',        -- the Drydock project imported tasks land in
  status          TEXT NOT NULL DEFAULT 'untested',-- untested | ok | unauthenticated | unreachable | degraded | disconnected
  status_detail   TEXT NOT NULL DEFAULT '',        -- plain language, secret-scanned
  auth_kind       TEXT NOT NULL DEFAULT 'api_key', -- api_key (token entry); OAuth flows are not built in this slice
  credential_ref  TEXT NOT NULL DEFAULT '',        -- a reference, not a secret
  base_url        TEXT NOT NULL DEFAULT '',        -- optional MCP endpoint override (https, or http on loopback)
  config_json     TEXT NOT NULL DEFAULT '{}',      -- remote scope: Linear team, Sentry org/project, Notion query, Aikido workspace
  poll_interval_s INTEGER NOT NULL DEFAULT 0,      -- 0 = the kind's default
  enabled         INTEGER NOT NULL DEFAULT 1,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL,
  version         INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX integrations_kind ON integrations(kind);

-- One cursor row per (integration, scope). Committed in the same transaction as the page of items it covers, so a crash or
-- restart resumes from the last committed page and never re-imports or skips items.
CREATE TABLE integration_cursors (
  integration_id       TEXT NOT NULL,
  scope                TEXT NOT NULL DEFAULT 'default',
  cursor_json          TEXT NOT NULL DEFAULT '{}',  -- {"after":"<vendor page cursor>","pending":<max updated ms of the running sweep>}
  watermark            INTEGER NOT NULL DEFAULT 0,  -- ms; items older than this were already seen
  last_success_at      INTEGER NOT NULL DEFAULT 0,
  last_attempt_at      INTEGER NOT NULL DEFAULT 0,
  last_error           TEXT NOT NULL DEFAULT '',    -- plain language, secret-scanned
  last_error_code      TEXT NOT NULL DEFAULT '',    -- unauthorized | rate_limited | unreachable | tool_missing | schema_drift | timeout | error
  items_last_sync      INTEGER NOT NULL DEFAULT 0,
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  next_attempt_at      INTEGER NOT NULL DEFAULT 0,  -- backoff / Retry-After
  PRIMARY KEY (integration_id, scope)
);

CREATE TABLE external_items (
  id                 TEXT PRIMARY KEY,
  integration_id     TEXT NOT NULL,
  source             TEXT NOT NULL,                 -- linear | notion | sentry | aikido
  project_id         TEXT NOT NULL DEFAULT '',
  external_id        TEXT NOT NULL,
  kind               TEXT NOT NULL CHECK (kind IN ('issue','page','pr','error_event','finding')),
  issue_ref          TEXT NOT NULL DEFAULT '',      -- LIN-123, a Sentry short id, AIK-<id>
  url                TEXT NOT NULL DEFAULT '',
  title              TEXT NOT NULL DEFAULT '',      -- sanitized, single line, size-limited, secret-redacted
  excerpt            TEXT NOT NULL DEFAULT '',      -- sanitized and redacted
  body_ref           TEXT NOT NULL DEFAULT '',      -- file under <app data>/integrations/<integration>/ (sanitized, redacted copy)
  state              TEXT NOT NULL DEFAULT '',
  labels_json        TEXT NOT NULL DEFAULT '[]',
  assignee           TEXT NOT NULL DEFAULT '',
  priority           TEXT NOT NULL DEFAULT '',
  parent_external_id TEXT NOT NULL DEFAULT '',
  meta_json          TEXT NOT NULL DEFAULT '{}',    -- normalized facts rules can match: level, events, severity, project...
  external_created_at INTEGER NOT NULL DEFAULT 0,
  external_updated_at INTEGER NOT NULL DEFAULT 0,
  etag               TEXT NOT NULL DEFAULT '',      -- the vendor's own version marker (updated-at, last-edited); MCP exposes no HTTP ETag
  content_hash       TEXT NOT NULL,                 -- sha256 of the sanitized payload: upserts are idempotent, change events fire on difference
  fetched_at         INTEGER NOT NULL,
  risk               INTEGER NOT NULL DEFAULT 0,
  risk_flags         TEXT NOT NULL DEFAULT '[]',
  redactions         INTEGER NOT NULL DEFAULT 0,
  truncated          INTEGER NOT NULL DEFAULT 0,
  untrusted          INTEGER NOT NULL DEFAULT 1 CHECK (untrusted = 1),
  task_id            TEXT NOT NULL DEFAULT '',
  imported_by        TEXT NOT NULL DEFAULT '',      -- 'human:<user>' or 'rule:<id>'; empty until a task exists
  imported_at        INTEGER NOT NULL DEFAULT 0,
  gone               INTEGER NOT NULL DEFAULT 0,
  version            INTEGER NOT NULL DEFAULT 1,
  UNIQUE (integration_id, external_id)
);
CREATE INDEX external_items_integration ON external_items(integration_id, external_updated_at DESC);
CREATE INDEX external_items_task ON external_items(task_id) WHERE task_id <> '';
CREATE INDEX external_items_rule ON external_items(imported_by, imported_at) WHERE imported_by <> '';

-- Stored rules are authored by a human, versioned for compare-and-swap, and start disabled in dry-run mode.
CREATE TABLE integration_rules (
  id             TEXT PRIMARY KEY,
  integration_id TEXT NOT NULL,
  project_id     TEXT NOT NULL,                     -- the Drydock project the rule's tasks land in
  name           TEXT NOT NULL,
  when_cel       TEXT NOT NULL,                     -- CEL over the normalized item fields only
  action         TEXT NOT NULL CHECK (action IN ('create_task','notify')),
  params_json    TEXT NOT NULL DEFAULT '{}',        -- fixed by the human: priority, template. Item text never supplies parameters.
  max_per_day    INTEGER NOT NULL DEFAULT 5,
  dry_run        INTEGER NOT NULL DEFAULT 1,
  enabled        INTEGER NOT NULL DEFAULT 0,
  created_by     TEXT NOT NULL,
  updated_by     TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  version        INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX integration_rules_integration ON integration_rules(integration_id);

-- What each rule did with each item: dry-run listings, rate-limited waits, risk skips. One row per (rule, item).
CREATE TABLE integration_rule_matches (
  rule_id    TEXT NOT NULL,
  item_id    TEXT NOT NULL,
  outcome    TEXT NOT NULL,                         -- would_create | created | rate_limited | risky | deduped | error
  detail     TEXT NOT NULL DEFAULT '',
  first_at   INTEGER NOT NULL,
  last_at    INTEGER NOT NULL,
  PRIMARY KEY (rule_id, item_id)
);

CREATE TABLE integration_outbox (
  id                 TEXT PRIMARY KEY,
  integration_id     TEXT NOT NULL,
  project_id         TEXT NOT NULL DEFAULT '',
  task_id            TEXT NOT NULL DEFAULT '',
  op                 TEXT NOT NULL,
  target_external_id TEXT NOT NULL DEFAULT '',
  tool               TEXT NOT NULL,                 -- the exact MCP tool that will be called
  payload_json       TEXT NOT NULL,                 -- the exact arguments (secret-scanned at proposal time; proposals containing a secret are refused)
  payload_hash       TEXT NOT NULL,                 -- sha256 of the canonical call; the approval is bound to it
  preview            TEXT NOT NULL DEFAULT '',      -- what the human sees
  approval_id        TEXT NOT NULL DEFAULT '',
  status             TEXT NOT NULL CHECK (status IN ('planned','awaiting_approval','approved','executing','sent','failed','rejected','expired','cancelled')),
  idempotency_key    TEXT NOT NULL,
  created_by         TEXT NOT NULL,
  created_at         INTEGER NOT NULL,
  decided_by         TEXT NOT NULL DEFAULT '',
  sent_at            INTEGER NOT NULL DEFAULT 0,
  receipt            TEXT NOT NULL DEFAULT '',      -- redacted, truncated
  error              TEXT NOT NULL DEFAULT '',
  version            INTEGER NOT NULL DEFAULT 1
);
-- At most one live outbox row per idempotency key; a failed or rejected one can be proposed again.
CREATE UNIQUE INDEX integration_outbox_live ON integration_outbox(idempotency_key) WHERE status IN ('planned','awaiting_approval','approved','executing','sent');
CREATE INDEX integration_outbox_integration ON integration_outbox(integration_id, created_at DESC);

-- +goose Down
DROP INDEX integration_outbox_integration;
DROP INDEX integration_outbox_live;
DROP TABLE integration_outbox;
DROP TABLE integration_rule_matches;
DROP INDEX integration_rules_integration;
DROP TABLE integration_rules;
DROP INDEX external_items_rule;
DROP INDEX external_items_task;
DROP INDEX external_items_integration;
DROP TABLE external_items;
DROP TABLE integration_cursors;
DROP INDEX integrations_kind;
DROP TABLE integrations;

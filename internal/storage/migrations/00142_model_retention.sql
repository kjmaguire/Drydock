-- +goose Up
-- Models a provider refused because the organization has no data retention enabled (decision P112; Anthropic's Covered Models
-- answer 400 invalid_request_error "... must have data retention enabled", docs/phase0/model-providers.md D). The router
-- leaves a model listed here out of every decision until a person clears it (after enabling retention, or to try again).
-- provider_id is a registry provider, or agent:<adapter> for an agent CLI's own login.
CREATE TABLE model_retention_blocks (
  provider_id TEXT NOT NULL,
  model       TEXT NOT NULL,
  source      TEXT NOT NULL DEFAULT '',  -- api (Drydock's own call) | agent (an agent turn's error)
  message     TEXT NOT NULL DEFAULT '',  -- what happened, in Drydock's words (never the provider's error body)
  seen_at     INTEGER NOT NULL,
  PRIMARY KEY (provider_id, model)
);

-- +goose Down
DROP TABLE model_retention_blocks;

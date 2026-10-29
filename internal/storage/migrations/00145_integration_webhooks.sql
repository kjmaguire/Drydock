-- +goose Up
-- The webhook receiver (decision P114): off by default, on 127.0.0.1 only, on a port the person chooses. Every delivery it
-- answered is recorded here without its body: which vendor, the delivery's own id (the replay guard: an id seen before is
-- refused), what was done, and the external id of the item it queued a sync of (that item is then read through the vendor's
-- API, never taken from the payload). Rows older than a day are deleted; the timestamp window is minutes, so an older delivery
-- could not be replayed anyway.
CREATE TABLE integration_webhook_deliveries (
  id             TEXT PRIMARY KEY,               -- '<kind>:<the vendor's delivery id>', or a hash of the signed body without one
  integration_id TEXT NOT NULL DEFAULT '',
  kind           TEXT NOT NULL,
  received_at    INTEGER NOT NULL,
  outcome        TEXT NOT NULL,                  -- queued | ignored | verification | refused_*
  external_id    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX integration_webhook_deliveries_time ON integration_webhook_deliveries(received_at);

-- +goose Down
DROP INDEX integration_webhook_deliveries_time;
DROP TABLE integration_webhook_deliveries;

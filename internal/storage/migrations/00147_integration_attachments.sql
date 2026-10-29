-- +goose Up
-- Attachments of external items (decision P114): listed and downloaded only on a person's action. The metadata is the vendor's
-- (cleaned like every outside string); a download is size-capped and lands under <app data>/integrations/<connection>/attachments,
-- never in a repository. A text file is kept only as its secret-redacted, sanitized copy (that copy is the only text of it an
-- agent is given); any other file is kept as it is and never shown to an agent as text.
CREATE TABLE integration_attachments (
  id             TEXT PRIMARY KEY,
  item_id        TEXT NOT NULL,
  integration_id TEXT NOT NULL,
  external_id    TEXT NOT NULL,                  -- the vendor's id of the attachment
  name           TEXT NOT NULL DEFAULT '',
  mime           TEXT NOT NULL DEFAULT '',
  size           INTEGER NOT NULL DEFAULT 0,     -- as the vendor reports it; 0 = unknown
  url            TEXT NOT NULL DEFAULT '',       -- a link to open; fetched only when downloadable
  downloadable   INTEGER NOT NULL DEFAULT 0,     -- a file the vendor hosts (not a link to another site)
  local_path     TEXT NOT NULL DEFAULT '',       -- relative to <app data>/integrations
  sha256         TEXT NOT NULL DEFAULT '',
  text_kept      INTEGER NOT NULL DEFAULT 0,     -- the stored copy is redacted text
  redactions     INTEGER NOT NULL DEFAULT 0,
  risk           INTEGER NOT NULL DEFAULT 0,
  listed_at      INTEGER NOT NULL,
  downloaded_at  INTEGER NOT NULL DEFAULT 0,
  downloaded_by  TEXT NOT NULL DEFAULT '',
  UNIQUE (item_id, external_id)
);
-- A Notion data source row's typed properties: {name: {type, text}}, cleaned and size-limited like every outside string.
ALTER TABLE external_items ADD COLUMN properties_json TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE external_items DROP COLUMN properties_json;
DROP TABLE integration_attachments;

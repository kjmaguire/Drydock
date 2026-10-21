-- +goose Up
-- Files a human attached to a room message (the composer's attachments). The file itself stays on disk under the data folder;
-- this row maps the id the UI and the message carry to that file.
CREATE TABLE room_attachments (
  id          TEXT PRIMARY KEY,
  room_id     TEXT NOT NULL,
  message_id  TEXT NOT NULL DEFAULT '',
  name        TEXT NOT NULL,
  mime        TEXT NOT NULL DEFAULT '',
  size        INTEGER NOT NULL,
  sha256      TEXT NOT NULL,
  path        TEXT NOT NULL,
  created_at  INTEGER NOT NULL
);
CREATE INDEX room_attachments_room ON room_attachments(room_id);

-- +goose Down
DROP TABLE room_attachments;

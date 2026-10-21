-- +goose Up
-- Files and pictures attached to chat messages (decision P90). The file itself is kept on disk under the chat's attachment
-- folder, named by its id; the row holds its name, type, size and hash, never its content. The message lists its attachments.
CREATE TABLE chat_attachments (
  id         TEXT PRIMARY KEY,
  chat_id    TEXT NOT NULL,
  message_id TEXT NOT NULL,
  name       TEXT NOT NULL,
  mime       TEXT NOT NULL,
  size       INTEGER NOT NULL,
  kind       TEXT NOT NULL CHECK (kind IN ('text','image','file')),
  sha256     TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX chat_attachments_chat ON chat_attachments(chat_id, message_id);
ALTER TABLE chat_messages ADD COLUMN attachments TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE chat_messages DROP COLUMN attachments;
DROP TABLE chat_attachments;

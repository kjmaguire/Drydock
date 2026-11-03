-- +goose Up
-- A chat's read cursor (D8): the newest message seq the person has seen, so a chat that answered while they were elsewhere
-- shows an unread dot and count. Every chat that exists now counts as read up to its newest message.
ALTER TABLE chats ADD COLUMN last_read_seq INTEGER NOT NULL DEFAULT 0;
UPDATE chats SET last_read_seq = COALESCE((SELECT MAX(m.seq) FROM chat_messages m WHERE m.chat_id = chats.id), 0);

-- +goose Down
ALTER TABLE chats DROP COLUMN last_read_seq;

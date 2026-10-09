-- +goose Up
-- A chat's version: every write to its row adds one. Snapshots of a chat (action replies, chat.updated events, chat.get) are
-- ordered by it instead of by the wall clock, so a reply read before a concurrent write can never replace the newer state.
ALTER TABLE chats ADD COLUMN version INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE chats DROP COLUMN version;

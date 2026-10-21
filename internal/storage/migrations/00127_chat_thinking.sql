-- +goose Up
-- A chat keeps the agent's reasoning (decision P89): the thinking the CLI reported before a tool call or an answer is kept on
-- that row, redacted and bounded, and shown folded away. Rows written before have none.
ALTER TABLE chat_messages ADD COLUMN thinking TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE chat_messages DROP COLUMN thinking;

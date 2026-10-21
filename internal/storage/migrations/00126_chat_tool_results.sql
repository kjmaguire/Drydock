-- +goose Up
-- A chat's tool calls keep how they ended (decision P84): the CLI's id of the call, whether it succeeded or failed, what it
-- returned (redacted and bounded, as the input is) and how long it took. Rows written before have none: shown as "no result".
ALTER TABLE chat_messages ADD COLUMN tool_call_id TEXT NOT NULL DEFAULT '';
ALTER TABLE chat_messages ADD COLUMN tool_status TEXT NOT NULL DEFAULT '';
ALTER TABLE chat_messages ADD COLUMN tool_result TEXT NOT NULL DEFAULT '';
ALTER TABLE chat_messages ADD COLUMN tool_ms INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE chat_messages DROP COLUMN tool_ms;
ALTER TABLE chat_messages DROP COLUMN tool_result;
ALTER TABLE chat_messages DROP COLUMN tool_status;
ALTER TABLE chat_messages DROP COLUMN tool_call_id;

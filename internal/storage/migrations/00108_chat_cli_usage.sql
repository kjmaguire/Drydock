-- +goose Up
-- The CLI's running usage totals of a chat's current session. Claude Code reports a resumed session's cost and token totals
-- including every earlier process of that session (measured with 2.1.296), so the next turn, which resumes it in a new
-- process, starts its parser from these totals and records only its own share. Opaque to everything but the adapter; ''
-- when the chat has no session.
ALTER TABLE chats ADD COLUMN cli_usage TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE chats DROP COLUMN cli_usage;

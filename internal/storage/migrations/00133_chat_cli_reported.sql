-- +goose Up
-- What the chat's agent CLI said it runs with (decision P107): the model and the permission mode of its session, as its
-- start-up message reported them (Claude Code's system/init `model` and `permissionMode`). A chat that chose no model shows
-- the CLI's own default by name instead of "adapter default"; a new chat starts with the default the same CLI last reported
-- for a chat that chose none. '' = not reported yet.
ALTER TABLE chats ADD COLUMN cli_model TEXT NOT NULL DEFAULT '';
ALTER TABLE chats ADD COLUMN cli_permission_mode TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE chats DROP COLUMN cli_permission_mode;
ALTER TABLE chats DROP COLUMN cli_model;

-- +goose Up
-- A chat imported from a Claude Code session transcript (decision P109): where it came from ('claude-code'; '' = made in
-- Drydock) and the Claude Code session id it was read from. The session id is unique among imported chats, so importing the
-- same session again does nothing; deleting the imported chat frees it to be imported again. session_id starts equal to it
-- (the chat's next turn resumes that session) and follows the CLI from then on; import_session keeps the origin.
ALTER TABLE chats ADD COLUMN import_source TEXT NOT NULL DEFAULT '';
ALTER TABLE chats ADD COLUMN import_session TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX chats_import_session ON chats(import_session) WHERE import_session <> '';

-- +goose Down
DROP INDEX chats_import_session;
ALTER TABLE chats DROP COLUMN import_session;
ALTER TABLE chats DROP COLUMN import_source;

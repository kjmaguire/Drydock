-- +goose Up
-- Where a chat's agent CLI runs (decision P41 for chats): '' on Windows, else the WSL distro whose own install (its own
-- Connection, its own sign-in) every turn of the chat starts inside. Chosen when the chat is created; a fork keeps it.
ALTER TABLE chats ADD COLUMN wsl_distro TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE chats DROP COLUMN wsl_distro;

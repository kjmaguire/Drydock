-- +goose Up
-- A chat's properties (decision P60): the workspace library skills chosen for the chat (JSON [{registryId, name}]) and the
-- workspace MCP library servers granted to the chat's sessions with the tools each may call (JSON [{serverId, tools}]). Both
-- reach the chat's agent CLI through Drydock's MCP gateway from the next turn on.
ALTER TABLE chats ADD COLUMN skills TEXT NOT NULL DEFAULT '[]';
ALTER TABLE chats ADD COLUMN mcp_servers TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE chats DROP COLUMN mcp_servers;
ALTER TABLE chats DROP COLUMN skills;

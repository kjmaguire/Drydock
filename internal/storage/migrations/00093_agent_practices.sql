-- +goose Up
-- Claude best practices: a short set of working rules (docs/claude-best-practices.md) added to the system prompt of Claude Code
-- agents. On by default; the human can switch it off per agent or template.
ALTER TABLE agents ADD COLUMN practices INTEGER NOT NULL DEFAULT 1;
ALTER TABLE agent_templates ADD COLUMN practices INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE agent_templates DROP COLUMN practices;
ALTER TABLE agents DROP COLUMN practices;

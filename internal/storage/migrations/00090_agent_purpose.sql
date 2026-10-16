-- +goose Up
-- Agent purpose: standing instructions the human wrote for this team member, sent to the agent CLI with every run.
-- archived_at: agents are removed from a team by archiving (history, usage and room messages keep pointing at the row).
ALTER TABLE agents ADD COLUMN purpose TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN archived_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE agents DROP COLUMN archived_at;
ALTER TABLE agents DROP COLUMN purpose;

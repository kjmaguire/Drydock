-- +goose Up
-- Registry skills an agent template carries, as JSON [{"registry": "...", "name": "...", "ref": "<commit>"}]. Applying the
-- template imports each one at its pinned commit, accepts it when the scan passes, and assigns it to the new agent.
ALTER TABLE agent_templates ADD COLUMN skills TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE agent_templates DROP COLUMN skills;

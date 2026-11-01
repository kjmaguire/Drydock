-- +goose Up
-- The template an agent was made from (decision P137): agenttemplate.apply (and so project.setup) records it, and the agent
-- editor's "Everywhere" saves a change to that template and to the other agents made from it. '' is an agent made any other
-- way, or one whose template was deleted. It is not exported with a project: templates belong to the workspace.
ALTER TABLE agents ADD COLUMN template_id TEXT NOT NULL DEFAULT '';
CREATE INDEX agents_template ON agents(template_id) WHERE template_id <> '';

-- +goose Down
DROP INDEX IF EXISTS agents_template;
ALTER TABLE agents DROP COLUMN template_id;

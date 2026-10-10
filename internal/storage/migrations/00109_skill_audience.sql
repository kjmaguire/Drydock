-- +goose Up
-- Who receives an accepted skill. 'team': every agent whose role matches its drydock.roles (every agent when it names none),
-- plus each agent it is given to in agent_skills. 'agents': only the agents in agent_skills. Until now a skill with any
-- agent_skills row reached only those agents, so those skills start as 'agents' and keep reaching exactly whom they reached.
ALTER TABLE skills ADD COLUMN audience TEXT NOT NULL DEFAULT 'team' CHECK (audience IN ('team', 'agents'));
UPDATE skills SET audience = 'agents'
  WHERE EXISTS (SELECT 1 FROM agent_skills a WHERE a.project_id = skills.project_id AND a.skill_name = skills.name);

-- +goose Down
ALTER TABLE skills DROP COLUMN audience;

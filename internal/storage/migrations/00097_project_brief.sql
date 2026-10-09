-- +goose Up
-- The human's project brief: what the project is for and how the team should work on it. It is given to every agent of the
-- project as standing context (AGENTS.md, planner and room prompts). Empty: no brief.
ALTER TABLE projects ADD COLUMN brief TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE projects DROP COLUMN brief;

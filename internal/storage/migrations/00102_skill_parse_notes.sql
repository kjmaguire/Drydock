-- +goose Up
-- What the SKILL.md parser tolerated and ignored (front matter keys outside the Agent Skills format, a registry skill whose
-- front matter name differs from its directory), as a JSON array of sentences shown next to the skill.
ALTER TABLE skills ADD COLUMN parse_notes TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE skills DROP COLUMN parse_notes;

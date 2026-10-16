-- +goose Up
-- Who made a task when it was not a person (audit 2026-10-16, M27). "lead:N" is a follow-up the team lead split off from a task
-- that is N-1 steps from one a person made, so a task made from an agent's own text can be told apart from one a person made,
-- kept in BACKLOG until a person moves it on, and counted. Empty for every task a person, an import or a plan made.
ALTER TABLE tasks ADD COLUMN origin TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE tasks DROP COLUMN origin;

-- +goose Up
-- A task's own model routing level (decision P127, docs/model-routing.md s3 "per-task overrides"): set by a person on the task
-- page, it decides the level of every agent turn of the task, above the level its workflow template declares, the project's
-- own and the workspace's. '' follows those. A project export leaves it out, as it leaves out the project's own level.
ALTER TABLE tasks ADD COLUMN route_level TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE tasks DROP COLUMN route_level;

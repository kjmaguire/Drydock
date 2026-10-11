-- +goose Up
-- The live transport and replay filter events by project (events.Filter.ProjectID); without this index every replay of a
-- project scanned the whole table (audit F10).
CREATE INDEX events_project ON events(project_id, seq) WHERE project_id <> '';

-- +goose Down
DROP INDEX events_project;

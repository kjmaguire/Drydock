-- +goose Up
-- A project's short key (decision P97): the prefix of its task identifiers, "GOC-41" for task 41 of gocalc. Two to five upper-case
-- letters or digits, unique among projects. A new project takes one made from its name; the core gives each project that has
-- none (every project made before this migration) one at start, so '' only lives between this migration and that start.
ALTER TABLE projects ADD COLUMN key TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX projects_key ON projects(key) WHERE key <> '';

-- +goose Down
DROP INDEX projects_key;
ALTER TABLE projects DROP COLUMN key;

-- +goose Up
-- A connection made before decision P42 without a project kept its remote scope (config_json), items and cursor for its first
-- assignment to adopt (migration 00114). legacy_adopted records that the connection has been assigned once, so assigning it
-- again after it was unassigned does not adopt that old scope a second time. config_json itself stays as 00114 left it.
ALTER TABLE integrations ADD COLUMN legacy_adopted INTEGER NOT NULL DEFAULT 0;
-- Already adopted: a connection that has an assignment, one that had a project (00114 gave it an assignment with its
-- config), or one whose items carry an assignment (it was assigned, then unassigned: its imported items stay behind).
UPDATE integrations SET legacy_adopted = 1
  WHERE project_id <> ''
     OR id IN (SELECT integration_id FROM integration_assignments)
     OR id IN (SELECT integration_id FROM external_items WHERE assignment_id <> '');

-- +goose Down
ALTER TABLE integrations DROP COLUMN legacy_adopted;

-- +goose Up
-- The trace links an accepted plan could not make (X48 review): a JSON array of {taskId, requirementId} for each created task
-- that could not be linked to a requirement the plan was made from (archived, removed or refused between Propose and Accept),
-- so the accepted plan can say so. '' is none (or a plan from before this column existed).
ALTER TABLE plans ADD COLUMN unlinked TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE plans DROP COLUMN unlinked;

-- +goose Up
-- The project's requirement documents a plan was made from (X48): plan.propose builds the planner's requirements from them, and
-- accepting the plan links each created task's acceptance criteria to each of them (the human's accept is the decision). A JSON
-- array of requirement ids; '' is a plan made without any (or before this column existed).
ALTER TABLE plans ADD COLUMN requirement_ids TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE plans DROP COLUMN requirement_ids;

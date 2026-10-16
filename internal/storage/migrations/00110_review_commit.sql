-- +goose Up
-- The commit a review looked at (the task commit Finalize recorded before the review ran), so the version each verdict was
-- given on is known afterwards: what a REVISE asked to change, and what a SHIP approved. '' for reviews before this column.
ALTER TABLE task_reviews ADD COLUMN commit_sha TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE task_reviews DROP COLUMN commit_sha;

-- +goose Up
-- The run whose subworkflow node started a run (decision P124): a nested run that waits for a person parks its parent with it,
-- the parent goes on with the nested run's result when it ends (also after a restart: recovery looks for parents waiting on a
-- nested run that has ended), and cancelling the parent cancels the nested run. '' for a run nothing nested.
ALTER TABLE workflow_runs ADD COLUMN parent_run_id TEXT NOT NULL DEFAULT '';
CREATE INDEX workflow_runs_parent ON workflow_runs(parent_run_id) WHERE parent_run_id <> '';

-- +goose Down
DROP INDEX workflow_runs_parent;
ALTER TABLE workflow_runs DROP COLUMN parent_run_id;

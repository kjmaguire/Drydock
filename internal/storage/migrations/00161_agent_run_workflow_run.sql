-- +goose Up
-- The workflow run an agent session belongs to when it is a turn in a room that a workflow node holds (a turn of a group
-- discussion, decision P124): the run and workflow budgets count such a turn by this id, wherever the discussion is held (a
-- channel has no task, so the task's time window cannot find it). Its usage rows name the run in model_usage.run_id. '' for
-- every other session, which the budgets count through its task as before.
ALTER TABLE agent_runs ADD COLUMN workflow_run_id TEXT NOT NULL DEFAULT '';
CREATE INDEX agent_runs_workflow_run ON agent_runs(workflow_run_id) WHERE workflow_run_id <> '';

-- +goose Down
DROP INDEX agent_runs_workflow_run;
ALTER TABLE agent_runs DROP COLUMN workflow_run_id;

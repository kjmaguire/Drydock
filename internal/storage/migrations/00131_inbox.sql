-- +goose Up
-- The phase of a review request, from the request that made it (RequestSpec.Phase): "spec" for the spec gate, "merge" for review
-- and merge, "plan" for a plan review; '' for every other request. Rows made before this column get it from the title the pipeline gave them.
ALTER TABLE approvals ADD COLUMN phase TEXT NOT NULL DEFAULT '';
UPDATE approvals SET phase='spec' WHERE kind='review' AND title LIKE 'Accept the spec: %';
UPDATE approvals SET phase='merge' WHERE kind='review' AND title LIKE 'Review and merge: %';
UPDATE approvals SET phase='plan' WHERE kind='review' AND title LIKE 'Review the plan: %';
-- the crew timeline reads the requests and the agents' runs of a time window
CREATE INDEX approvals_window ON approvals(resolved_at, created_at);
CREATE INDEX agent_runs_window ON agent_runs(ended_at, started_at);

-- A person's "approve for this task" of an agent's tool permission request (security.TaskAllowRule): later requests of the same
-- task, agent and tool that are the same call are approved without asking, until the task ends (the core deletes its rules
-- then, and the gate reads only the rules of an open task). A shell rule holds the command's programs and arguments
-- (commands, JSON [StoredRule]); any other tool's rule holds the digest of the exact input. Neither holds a secret: a call
-- whose display copy had to be redacted can only be approved once.
CREATE TABLE permission_allow_rules (
  id           TEXT PRIMARY KEY,
  project_id   TEXT NOT NULL,
  task_id      TEXT NOT NULL,
  agent_id     TEXT NOT NULL,
  tool         TEXT NOT NULL,
  program      TEXT NOT NULL DEFAULT '',
  commands     TEXT NOT NULL DEFAULT '[]',
  input_digest TEXT NOT NULL DEFAULT '',
  summary      TEXT NOT NULL DEFAULT '',
  approval_id  TEXT NOT NULL DEFAULT '',
  created_by   TEXT NOT NULL,
  created_at   INTEGER NOT NULL
);
CREATE INDEX permission_allow_rules_task ON permission_allow_rules(task_id, agent_id, tool);

-- +goose Down
DROP TABLE permission_allow_rules;
DROP INDEX agent_runs_window;
DROP INDEX approvals_window;
ALTER TABLE approvals DROP COLUMN phase;

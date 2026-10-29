-- +goose Up
-- What a person or an agent said about one acceptance criterion of a task (decision P125). Rows are only added: a criterion's
-- status (open, met, not met, waived) is worked out from them together with the task's check results and reviews
-- (pipeline.CriteriaStatus). criterion is the criterion's place in tasks.acceptance (from 0) and criterion_sha the first 16 hex
-- digits of the SHA-256 of its text when the row was written, so a criterion whose text changed starts open again. mark is a
-- person's met, not_met, waived or open (their earlier word no longer counts), or an agent's claim_met, which is shown beside the
-- status and never sets it. actor is human:<name> or the agent's id; note is the person's reason or the agent's evidence.
CREATE TABLE acceptance_marks (
  id            TEXT PRIMARY KEY,
  task_id       TEXT NOT NULL,
  criterion     INTEGER NOT NULL,
  criterion_sha TEXT NOT NULL,
  mark          TEXT NOT NULL CHECK (mark IN ('met','not_met','waived','open','claim_met')),
  actor         TEXT NOT NULL,
  note          TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL
);
CREATE INDEX acceptance_marks_task ON acceptance_marks(task_id, created_at);

-- +goose Down
DROP TABLE acceptance_marks;

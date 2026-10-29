-- +goose Up
-- Wall-time ceilings (decision P112): a turn that runs past the ceiling of its level (or of its agent) is stopped by Drydock.
-- Why it was stopped is written on the decision it ran on when it happens, and copied onto the decision's routing outcome
-- when the task ends, so the evaluation and "Why this model?" both show it.
ALTER TABLE routing_decisions ADD COLUMN stop_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE routing_outcomes ADD COLUMN stop_reason TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE routing_outcomes DROP COLUMN stop_reason;
ALTER TABLE routing_decisions DROP COLUMN stop_reason;

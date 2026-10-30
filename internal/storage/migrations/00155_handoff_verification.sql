-- +goose Up
-- What Drydock found when it checked a handoff an agent wrote with the handoff tool before seeding a fresh session with it
-- (decision P119, token rule 6): JSON {status: accepted|rejected, reasons, checkedAt, handoff (the handoff Drydock wrote for
-- that session)}. Empty: not checked yet, so a handoff is checked, and used, at most once.
ALTER TABLE handoffs ADD COLUMN verification TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE handoffs DROP COLUMN verification;

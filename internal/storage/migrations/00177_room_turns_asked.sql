-- +goose Up
-- A turn a person asked for by name (X12: "Ask Ada to answer" on their message, Ask again after a failed, empty or interrupted
-- turn) is a new room_turns row for the same message and agent. ask counts them: 0 is the turn the turn-taking rules granted
-- (still at most once per message and agent, whatever the event delivery does), 1, 2, ... each asked turn. The unique index
-- widens to include it; nothing is removed.
ALTER TABLE room_turns ADD COLUMN ask INTEGER NOT NULL DEFAULT 0;
DROP INDEX room_turns_once;
CREATE UNIQUE INDEX room_turns_once ON room_turns(trigger_id, agent_id, ask);

-- +goose Down
DROP INDEX room_turns_once;
DELETE FROM room_turns WHERE ask > 0;
CREATE UNIQUE INDEX room_turns_once ON room_turns(trigger_id, agent_id);
ALTER TABLE room_turns DROP COLUMN ask;

-- +goose Up
-- How a granted turn ended, recorded by the room's scheduler itself (decision P130, room-protocol s6 Complete): the Agent
-- Runtime used the grant, and room.Complete closes it with the turn's outcome (done, passed, empty, failed, stopped,
-- interrupted, or renewed for a fallback hop's used grant) and the reply it posted, if any. A grant is completed once; one
-- that was never used is closed by its completion too, so it can no longer start a turn. 0 and '' for grants before.
ALTER TABLE room_turn_grants ADD COLUMN completed_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE room_turn_grants ADD COLUMN outcome TEXT NOT NULL DEFAULT '';
ALTER TABLE room_turn_grants ADD COLUMN reply_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE room_turn_grants DROP COLUMN reply_id;
ALTER TABLE room_turn_grants DROP COLUMN outcome;
ALTER TABLE room_turn_grants DROP COLUMN completed_at;

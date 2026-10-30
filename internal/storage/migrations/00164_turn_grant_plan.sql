-- +goose Up
-- What a turn grant decides besides who speaks (decision P127, room-protocol s6 rules 2 and 4): the debate bound the turn
-- belongs to (max_rounds) and the turn's context plan (context_plan, the JSON of room.ContextPlan: the trigger, the task, the
-- agent's roles, its previous message, the thread window, the mention and token bounds). The scheduler writes both when it
-- issues the grant; the context selection uses the plan the grant carries, and a grant whose bound or plan differs from what
-- was issued is refused like any other changed grant. 0 and '' for grants issued before.
ALTER TABLE room_turn_grants ADD COLUMN max_rounds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE room_turn_grants ADD COLUMN context_plan TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE room_turn_grants DROP COLUMN context_plan;
ALTER TABLE room_turn_grants DROP COLUMN max_rounds;

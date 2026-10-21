-- +goose Up
-- The models the model router may choose between for this agent's turns, as JSON [{"model": "...", "tier": "cheap|premium"}].
-- Empty: the agent always runs its configured model (a static assignment).
ALTER TABLE agents ADD COLUMN route_models TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE agents DROP COLUMN route_models;

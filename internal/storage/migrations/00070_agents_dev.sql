-- +goose Up
-- Slice 4: agents answering in the Engineering Room, and the planner's proposals. Numbers 00070-00079 belong to this area.

-- One row per agent turn granted by the room's turn-taking rules. UNIQUE(trigger_id, agent_id) is what makes a turn
-- happen at most once per triggering message, however many times the event is delivered or the app restarts.
CREATE TABLE room_turns (
  id             TEXT PRIMARY KEY,
  room_id        TEXT NOT NULL,
  project_id     TEXT NOT NULL DEFAULT '',
  trigger_id     TEXT NOT NULL,              -- the message that granted the turn
  agent_id       TEXT NOT NULL,
  round          INTEGER NOT NULL DEFAULT 1,
  reason         TEXT NOT NULL DEFAULT '',   -- mention | owner | role | sequence | named_by_moderator | moderator_turn | dm
  mode           TEXT NOT NULL DEFAULT 'auto',
  status         TEXT NOT NULL,              -- running | done | passed | empty | failed | interrupted
  message_id     TEXT NOT NULL DEFAULT '',   -- the reply that was posted
  agent_run_id   TEXT NOT NULL DEFAULT '',
  context_msgs   INTEGER NOT NULL DEFAULT 0, -- room messages the agent was given (the manifest lists them)
  context_tokens INTEGER NOT NULL DEFAULT 0,
  input_tokens   INTEGER NOT NULL DEFAULT 0,
  output_tokens  INTEGER NOT NULL DEFAULT 0,
  cost_usd       REAL    NOT NULL DEFAULT 0,
  error          TEXT NOT NULL DEFAULT '',
  started_at     INTEGER NOT NULL,
  ended_at       INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX room_turns_once ON room_turns(trigger_id, agent_id);
CREATE INDEX room_turns_room ON room_turns(room_id, started_at);

-- PlanTeam: a goal, the planner's validated proposal, and what the human accepted. Nothing in a plan is a task until a
-- human accepts it; accepted tasks are created in BACKLOG by the workflow run that owns the plan.
CREATE TABLE plans (
  id             TEXT PRIMARY KEY,
  project_id     TEXT NOT NULL,
  run_id         TEXT NOT NULL DEFAULT '',
  goal           TEXT NOT NULL,
  requirements_sha TEXT NOT NULL DEFAULT '',
  planner_agent  TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL,              -- PLANNING | PROPOSED | ACCEPTED | REJECTED | FAILED
  proposal       TEXT NOT NULL DEFAULT '{}', -- the validated proposal (JSON)
  accepted_tasks TEXT NOT NULL DEFAULT '{}', -- proposal key -> task id
  error          TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  decided_at     INTEGER NOT NULL DEFAULT 0,
  decided_by     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX plans_project ON plans(project_id, created_at);

-- +goose Down
DROP TABLE plans; DROP TABLE room_turns;

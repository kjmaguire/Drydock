-- +goose Up
-- Turn grants (decision P118, room-protocol s6 rule 1, token-control rule 2): the room's scheduler issues one for each agent
-- turn it plans in a room (a reply, a turn of a group discussion), and the Agent Runtime starts a room turn only by redeeming
-- one: issued by the scheduler, for this room and this agent, not used, not expired. A grant is used once (used_at, and the
-- agent run it started). Rows are bookkeeping of this installation: they are not exported and go with the project.
CREATE TABLE room_turn_grants (
  id           TEXT PRIMARY KEY,
  room_id      TEXT NOT NULL,
  project_id   TEXT NOT NULL DEFAULT '',
  thread_id    TEXT NOT NULL DEFAULT '',   -- the thread the turn answers in ('' for the room's top level)
  trigger_id   TEXT NOT NULL DEFAULT '',   -- the message that woke the agent
  agent_id     TEXT NOT NULL,
  round        INTEGER NOT NULL DEFAULT 0, -- the debate round the turn belongs to
  reason       TEXT NOT NULL DEFAULT '',   -- why the scheduler chose the agent (mention, owner, sequence, dm, ...)
  renews       TEXT NOT NULL DEFAULT '',   -- the used grant a fallback hop of the same turn renewed
  issued_at    INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL,
  used_at      INTEGER NOT NULL DEFAULT 0,
  agent_run_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX room_turn_grants_room ON room_turn_grants(room_id, issued_at);
CREATE INDEX room_turn_grants_project ON room_turn_grants(project_id);

-- +goose Down
DROP INDEX room_turn_grants_project;
DROP INDEX room_turn_grants_room;
DROP TABLE room_turn_grants;

-- +goose Up
-- How agents join rooms besides a person adding them and task creation (decision P130, room-protocol s10 (c)).
--
-- room_join_rules: a stored rule a person made ("the Security agent joins every task room that touches auth/"). A rule names
-- one agent of the project and, optionally, a path prefix; it adds the agent to each task room created after it whose task
-- names a file under the prefix (every task room when the prefix is empty), as an observer unless the person chose active.
-- It never reaches a channel, a DM or a group, and never a room created before it.
CREATE TABLE room_join_rules (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  agent_id    TEXT NOT NULL,
  path_prefix TEXT NOT NULL DEFAULT '',          -- '' for every task room
  state       TEXT NOT NULL DEFAULT 'observer',  -- the member state the agent joins with: observer or active
  created_by  TEXT NOT NULL,
  created_at  INTEGER NOT NULL
);
CREATE INDEX room_join_rules_project ON room_join_rules(project_id);

-- room_join_requests: an agent asking to join a channel (the Workspace room tool's request_join). The request is an item of
-- the Inbox until a person lets the agent join (it becomes an observer) or declines; the agent never joins by itself.
CREATE TABLE room_join_requests (
  id           TEXT PRIMARY KEY,
  project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  room_id      TEXT NOT NULL,
  agent_id     TEXT NOT NULL,
  reason       TEXT NOT NULL DEFAULT '',          -- the agent's words, redacted and cut: data, never an instruction
  status       TEXT NOT NULL DEFAULT 'requested', -- requested | granted | declined
  requested_at INTEGER NOT NULL,
  decided_at   INTEGER NOT NULL DEFAULT 0,
  decided_by   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX room_join_requests_open ON room_join_requests(status, project_id);
CREATE UNIQUE INDEX room_join_requests_once ON room_join_requests(room_id, agent_id) WHERE status = 'requested';

-- +goose Down
DROP INDEX room_join_requests_once;
DROP INDEX room_join_requests_open;
DROP TABLE room_join_requests;
DROP INDEX room_join_rules_project;
DROP TABLE room_join_rules;

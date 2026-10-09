-- +goose Up
-- Chats (decisions P29): the human's own conversations with an agent CLI (Claude Code by default), kept as history that can be
-- read, searched, continued and forked. Not project records: never exported (projectio.ExcludedTables).
CREATE TABLE chats (
  id                 TEXT PRIMARY KEY,                -- chat_*
  project_id         TEXT NOT NULL DEFAULT '',        -- '' = not tied to a project (no foreign key, '' is not a project)
  title              TEXT NOT NULL DEFAULT '',
  adapter            TEXT NOT NULL,
  model              TEXT NOT NULL DEFAULT '',
  effort             TEXT NOT NULL DEFAULT '',
  permission_mode    TEXT NOT NULL DEFAULT '',
  cwd                TEXT NOT NULL DEFAULT '',        -- the folder the chat runs in (resolved again per turn for project chats)
  session_id         TEXT NOT NULL DEFAULT '',        -- the CLI's own session id; '' = the next turn starts a fresh session
  allowed_tools      TEXT NOT NULL DEFAULT '[]',      -- JSON array of validated allow rules (Claude --allowedTools)
  read_history       INTEGER NOT NULL DEFAULT 1,      -- the chats workspace tool is offered to this chat's sessions
  status             TEXT NOT NULL DEFAULT 'idle' CHECK (status IN ('idle','running','error')),
  last_error         TEXT NOT NULL DEFAULT '',
  pinned             INTEGER NOT NULL DEFAULT 0,
  archived_at        INTEGER NOT NULL DEFAULT 0,
  forked_from        TEXT NOT NULL DEFAULT '',
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL,
  tokens_in          INTEGER NOT NULL DEFAULT 0,
  tokens_out         INTEGER NOT NULL DEFAULT 0,
  cost_usd           REAL,                            -- NULL = no turn reported a cost: unknown, never zero (decision P10b)
  cost_unknown_turns INTEGER NOT NULL DEFAULT 0       -- turns that reported no cost (cost_usd then covers only the others)
);
CREATE INDEX chats_recent ON chats(archived_at, pinned, updated_at);
CREATE INDEX chats_project ON chats(project_id, updated_at);

CREATE TABLE chat_messages (
  id          TEXT PRIMARY KEY,                       -- cmsg_*
  chat_id     TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
  seq         INTEGER NOT NULL,
  role        TEXT NOT NULL CHECK (role IN ('user','assistant','tool','denied','error','system')),
  body        TEXT NOT NULL DEFAULT '',               -- passed through the secret scanner before it is stored
  tool_name   TEXT NOT NULL DEFAULT '',
  tool_input  TEXT NOT NULL DEFAULT '',               -- JSON, redacted, at most 4 KB (cut with a marker)
  turn_id     TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  UNIQUE (chat_id, seq)
);

CREATE TABLE chat_turns (
  id            TEXT PRIMARY KEY,                     -- cturn_*
  chat_id       TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
  status        TEXT NOT NULL CHECK (status IN ('running','done','error','stopped')),
  agent_run_id  TEXT NOT NULL DEFAULT '',             -- the runtime's run id (agent.* events, model_usage rows)
  session_id    TEXT NOT NULL DEFAULT '',
  resumed       INTEGER NOT NULL DEFAULT 0,           -- the turn continued the stored CLI session
  fallback      INTEGER NOT NULL DEFAULT 0,           -- the session could not be resumed: a fresh one got the transcript
  attached      TEXT NOT NULL DEFAULT '[]',           -- JSON array of chat ids whose transcripts were given to this turn
  tokens_in     INTEGER NOT NULL DEFAULT 0,
  tokens_out    INTEGER NOT NULL DEFAULT 0,
  cost_usd      REAL,
  error         TEXT NOT NULL DEFAULT '',
  started_at    INTEGER NOT NULL,
  ended_at      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX chat_turns_chat ON chat_turns(chat_id, started_at);

-- full-text search over what was said (user and assistant messages) and over chat titles
CREATE VIRTUAL TABLE chat_messages_fts USING fts5(body, content='chat_messages', content_rowid='rowid', tokenize='porter unicode61');
-- +goose StatementBegin
CREATE TRIGGER chat_messages_ai AFTER INSERT ON chat_messages WHEN new.role IN ('user','assistant') BEGIN
  INSERT INTO chat_messages_fts(rowid, body) VALUES (new.rowid, new.body);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER chat_messages_ad AFTER DELETE ON chat_messages WHEN old.role IN ('user','assistant') BEGIN
  INSERT INTO chat_messages_fts(chat_messages_fts, rowid, body) VALUES ('delete', old.rowid, old.body);
END;
-- +goose StatementEnd

CREATE VIRTUAL TABLE chats_fts USING fts5(title, content='chats', content_rowid='rowid', tokenize='porter unicode61');
-- +goose StatementBegin
CREATE TRIGGER chats_ai AFTER INSERT ON chats BEGIN
  INSERT INTO chats_fts(rowid, title) VALUES (new.rowid, new.title);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER chats_ad AFTER DELETE ON chats BEGIN
  INSERT INTO chats_fts(chats_fts, rowid, title) VALUES ('delete', old.rowid, old.title);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER chats_au AFTER UPDATE OF title ON chats BEGIN
  INSERT INTO chats_fts(chats_fts, rowid, title) VALUES ('delete', old.rowid, old.title);
  INSERT INTO chats_fts(rowid, title) VALUES (new.rowid, new.title);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER chats_au; DROP TRIGGER chats_ad; DROP TRIGGER chats_ai; DROP TABLE chats_fts;
DROP TRIGGER chat_messages_ad; DROP TRIGGER chat_messages_ai; DROP TABLE chat_messages_fts;
DROP TABLE chat_turns; DROP TABLE chat_messages; DROP TABLE chats;

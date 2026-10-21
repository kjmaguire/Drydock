-- +goose Up
-- Engineering Room (docs/room-protocol.md). Extends the rooms, room_members and messages tables from 00001.
ALTER TABLE rooms ADD COLUMN mode_params TEXT    NOT NULL DEFAULT '{}';
ALTER TABLE rooms ADD COLUMN created_by  TEXT    NOT NULL DEFAULT '';
ALTER TABLE rooms ADD COLUMN archived_at INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX rooms_task ON rooms(task_id) WHERE kind = 'task' AND task_id <> '';

-- member_id is the author key: an agent id, 'human:<name>' or 'tool:<name>'. last_read_seq is the per-room message seq.
ALTER TABLE room_members ADD COLUMN role                  TEXT    NOT NULL DEFAULT '';
ALTER TABLE room_members ADD COLUMN state                 TEXT    NOT NULL DEFAULT 'active';
ALTER TABLE room_members ADD COLUMN added_by              TEXT    NOT NULL DEFAULT '';
ALTER TABLE room_members ADD COLUMN added_at              INTEGER NOT NULL DEFAULT 0;
ALTER TABLE room_members ADD COLUMN last_mention_read_seq INTEGER NOT NULL DEFAULT 0;

-- messages.author holds the author key ('system', 'human:<name>', 'tool:<name>' or an agent id); thread_id is the thread root.
ALTER TABLE messages ADD COLUMN seq             INTEGER NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN reply_to_id     TEXT    NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN supersedes_id   TEXT    NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN payload         TEXT    NOT NULL DEFAULT '{}';
ALTER TABLE messages ADD COLUMN markers         TEXT    NOT NULL DEFAULT '[]';
ALTER TABLE messages ADD COLUMN mentions        TEXT    NOT NULL DEFAULT '[]';
ALTER TABLE messages ADD COLUMN trust           TEXT    NOT NULL DEFAULT 'untrusted';
ALTER TABLE messages ADD COLUMN task_id         TEXT    NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN idempotency_key TEXT    NOT NULL DEFAULT '';
CREATE UNIQUE INDEX messages_room_seq  ON messages(room_id, seq) WHERE seq > 0;
CREATE UNIQUE INDEX messages_idem      ON messages(room_id, idempotency_key) WHERE idempotency_key <> '';
CREATE INDEX        messages_reply     ON messages(reply_to_id) WHERE reply_to_id <> '';
CREATE INDEX        messages_supersede ON messages(supersedes_id) WHERE supersedes_id <> '';

-- Messages are immutable rows. The one allowed change is recording the bus sequence once (event_seq 0 -> n).
-- +goose StatementBegin
CREATE TRIGGER messages_immutable BEFORE UPDATE ON messages
WHEN NOT (OLD.event_seq = 0 AND NEW.event_seq > 0
  AND NEW.id = OLD.id AND NEW.room_id = OLD.room_id AND NEW.thread_id = OLD.thread_id AND NEW.author = OLD.author
  AND NEW.type = OLD.type AND NEW.body = OLD.body AND NEW.refs = OLD.refs AND NEW.run_id = OLD.run_id
  AND NEW.created_at = OLD.created_at AND NEW.seq = OLD.seq AND NEW.reply_to_id = OLD.reply_to_id
  AND NEW.supersedes_id = OLD.supersedes_id AND NEW.payload = OLD.payload AND NEW.markers = OLD.markers
  AND NEW.mentions = OLD.mentions AND NEW.trust = OLD.trust AND NEW.task_id = OLD.task_id
  AND NEW.idempotency_key = OLD.idempotency_key)
BEGIN
  SELECT RAISE(ABORT, 'messages are immutable');
END;
-- +goose StatementEnd

-- Defence in depth under the service checks: only the system author may write approval messages, and types are closed.
-- +goose StatementBegin
CREATE TRIGGER messages_type_guard BEFORE INSERT ON messages
WHEN NEW.type NOT IN ('CHAT','TASK','DECISION','STATUS','RESULT','QUESTION','HANDOFF','ERROR','APPROVAL_REQUEST','APPROVAL','SYSTEM')
  OR (NEW.type IN ('APPROVAL_REQUEST','APPROVAL') AND NEW.author <> 'system')
BEGIN
  SELECT RAISE(ABORT, 'message type or author not allowed');
END;
-- +goose StatementEnd

-- Exports are immutable snapshots, idempotent by (scope, receipt_id) (brief section 18).
CREATE TABLE room_exports (
  id         TEXT PRIMARY KEY,
  room_id    TEXT NOT NULL,
  scope      TEXT NOT NULL,
  receipt_id TEXT NOT NULL,
  kind       TEXT NOT NULL CHECK (kind IN ('minutes','pr_description','adr_candidates')),
  format     TEXT NOT NULL DEFAULT 'markdown',
  from_seq   INTEGER NOT NULL,
  to_seq     INTEGER NOT NULL,
  params     TEXT NOT NULL DEFAULT '{}',
  content    TEXT NOT NULL,
  sha256     TEXT NOT NULL,
  created_by TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  event_seq  INTEGER NOT NULL DEFAULT 0,
  UNIQUE (scope, receipt_id)
);
CREATE INDEX room_exports_room ON room_exports(room_id, created_at);

-- +goose StatementBegin
CREATE TRIGGER room_exports_immutable BEFORE UPDATE ON room_exports
WHEN NOT (OLD.event_seq = 0 AND NEW.event_seq > 0 AND NEW.id = OLD.id AND NEW.room_id = OLD.room_id
  AND NEW.scope = OLD.scope AND NEW.receipt_id = OLD.receipt_id AND NEW.kind = OLD.kind AND NEW.format = OLD.format
  AND NEW.from_seq = OLD.from_seq AND NEW.to_seq = OLD.to_seq AND NEW.params = OLD.params
  AND NEW.content = OLD.content AND NEW.sha256 = OLD.sha256 AND NEW.created_by = OLD.created_by
  AND NEW.created_at = OLD.created_at)
BEGIN
  SELECT RAISE(ABORT, 'exports are immutable');
END;
-- +goose StatementEnd

-- A human dismissal of a "Needs you" item that has no underlying state to flip (failed check, watchdog pause).
CREATE TABLE room_inbox_acks (
  item_key TEXT PRIMARY KEY,
  acked_by TEXT NOT NULL,
  acked_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE room_inbox_acks;
DROP TRIGGER room_exports_immutable;
DROP TABLE room_exports;
DROP TRIGGER messages_type_guard;
DROP TRIGGER messages_immutable;
DROP INDEX messages_supersede;
DROP INDEX messages_reply;
DROP INDEX messages_idem;
DROP INDEX messages_room_seq;
ALTER TABLE messages DROP COLUMN idempotency_key;
ALTER TABLE messages DROP COLUMN task_id;
ALTER TABLE messages DROP COLUMN trust;
ALTER TABLE messages DROP COLUMN mentions;
ALTER TABLE messages DROP COLUMN markers;
ALTER TABLE messages DROP COLUMN payload;
ALTER TABLE messages DROP COLUMN supersedes_id;
ALTER TABLE messages DROP COLUMN reply_to_id;
ALTER TABLE messages DROP COLUMN seq;
ALTER TABLE room_members DROP COLUMN last_mention_read_seq;
ALTER TABLE room_members DROP COLUMN added_at;
ALTER TABLE room_members DROP COLUMN added_by;
ALTER TABLE room_members DROP COLUMN state;
ALTER TABLE room_members DROP COLUMN role;
DROP INDEX rooms_task;
ALTER TABLE rooms DROP COLUMN archived_at;
ALTER TABLE rooms DROP COLUMN created_by;
ALTER TABLE rooms DROP COLUMN mode_params;

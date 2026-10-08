-- +goose Up
-- Requirements (docs/expert-packs.md section 10): uploaded or synced documents, chunked into FTS5 and cited like a pack
-- ("REQ:<doc>@<version>#<heading_path>"). Always private: never sent to a trainable provider. Every content change is a new
-- version; chunks of older versions stay (current = 0) so a citation to an old version keeps resolving.
CREATE TABLE requirements (
  id              TEXT PRIMARY KEY,
  project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  title           TEXT NOT NULL,
  source          TEXT NOT NULL CHECK (source IN ('upload','paste','sync')),
  source_ref      TEXT NOT NULL DEFAULT '',      -- external item id for synced documents
  current_version INTEGER NOT NULL DEFAULT 1,
  content_hash    TEXT NOT NULL,
  archived        INTEGER NOT NULL DEFAULT 0,
  created_by      TEXT NOT NULL DEFAULT '',
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);
CREATE INDEX requirements_project ON requirements(project_id, archived);
CREATE UNIQUE INDEX requirements_source ON requirements(project_id, source_ref) WHERE source_ref <> '';

CREATE TABLE requirement_versions (
  requirement_id TEXT NOT NULL REFERENCES requirements(id) ON DELETE CASCADE,
  version        INTEGER NOT NULL,
  content_hash   TEXT NOT NULL,
  text           TEXT NOT NULL,                  -- normalized Markdown with secrets redacted
  chunks         INTEGER NOT NULL DEFAULT 0,
  redactions     INTEGER NOT NULL DEFAULT 0,
  created_by     TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  PRIMARY KEY (requirement_id, version)
);

CREATE TABLE requirement_chunks (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  requirement_id TEXT NOT NULL REFERENCES requirements(id) ON DELETE CASCADE,
  version        INTEGER NOT NULL,
  ordinal        INTEGER NOT NULL,
  heading_path   TEXT NOT NULL,
  body           TEXT NOT NULL,
  token_est      INTEGER NOT NULL DEFAULT 0,
  chunk_hash     TEXT NOT NULL,
  risk           INTEGER NOT NULL DEFAULT 0,
  flags          TEXT NOT NULL DEFAULT '',
  quarantined    INTEGER NOT NULL DEFAULT 0,
  current        INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX requirement_chunks_doc ON requirement_chunks(requirement_id, version);
CREATE INDEX requirement_chunks_current ON requirement_chunks(requirement_id, current);

CREATE VIRTUAL TABLE requirement_chunks_fts USING fts5(heading_path, body, content='requirement_chunks', content_rowid='id', tokenize='porter unicode61');
-- +goose StatementBegin
CREATE TRIGGER requirement_chunks_ai AFTER INSERT ON requirement_chunks BEGIN
  INSERT INTO requirement_chunks_fts(rowid, heading_path, body) VALUES (new.id, new.heading_path, new.body);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER requirement_chunks_ad AFTER DELETE ON requirement_chunks BEGIN
  INSERT INTO requirement_chunks_fts(requirement_chunks_fts, rowid, heading_path, body) VALUES ('delete', old.id, old.heading_path, old.body);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER requirement_chunks_au AFTER UPDATE OF heading_path, body ON requirement_chunks BEGIN
  INSERT INTO requirement_chunks_fts(requirement_chunks_fts, rowid, heading_path, body) VALUES ('delete', old.id, old.heading_path, old.body);
  INSERT INTO requirement_chunks_fts(rowid, heading_path, body) VALUES (new.id, new.heading_path, new.body);
END;
-- +goose StatementEnd

-- Spec gate: a Given/When/Then criterion of a requirement version linked to the task that implements it.
CREATE TABLE requirement_links (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  requirement_id TEXT NOT NULL REFERENCES requirements(id) ON DELETE CASCADE,
  version        INTEGER NOT NULL,              -- the requirement version the criterion was written against
  task_id        TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  criterion      TEXT NOT NULL,
  criterion_hash TEXT NOT NULL,
  created_by     TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  UNIQUE (requirement_id, task_id, criterion_hash)
);
CREATE INDEX requirement_links_task ON requirement_links(task_id);

-- Milestones (brief section 21, Project section).
CREATE TABLE milestones (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  title       TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  due_at      INTEGER NOT NULL DEFAULT 0,       -- ms, 0 = no date
  status      TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','closed')),
  created_by  TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  closed_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX milestones_project ON milestones(project_id, status);

CREATE TABLE milestone_tasks (
  milestone_id TEXT NOT NULL REFERENCES milestones(id) ON DELETE CASCADE,
  task_id      TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  PRIMARY KEY (milestone_id, task_id)
);

-- +goose Down
DROP TABLE milestone_tasks; DROP TABLE milestones; DROP TABLE requirement_links;
DROP TRIGGER requirement_chunks_au; DROP TRIGGER requirement_chunks_ad; DROP TRIGGER requirement_chunks_ai; DROP TABLE requirement_chunks_fts;
DROP TABLE requirement_chunks; DROP TABLE requirement_versions; DROP TABLE requirements;

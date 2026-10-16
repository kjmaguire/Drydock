-- +goose Up
-- Expert Packs (docs/expert-packs.md section 3). Chunks are never overwritten: a refresh marks vanished chunks as removed
-- (version_removed / seq_removed) so a citation to <pack>@<version> keeps resolving while that version is retained.
-- seq_* are the integer ordering of versions (the YYYYMMDD.n strings do not sort numerically once n >= 10).
CREATE TABLE packs (
  id              TEXT PRIMARY KEY,
  title           TEXT NOT NULL,
  kind            TEXT NOT NULL CHECK (kind IN ('indexed','live','hybrid')),
  visibility      TEXT NOT NULL CHECK (visibility IN ('public','private')),
  version         TEXT NOT NULL DEFAULT '',
  version_seq     INTEGER NOT NULL DEFAULT 0,
  manifest        TEXT NOT NULL,                 -- the validated pack.yaml text
  manifest_hash   TEXT NOT NULL,
  status          TEXT NOT NULL DEFAULT 'new' CHECK (status IN ('new','ready','refreshing','failed')),
  last_refresh_at INTEGER NOT NULL DEFAULT 0,    -- last attempt that finished (ms)
  last_success_at INTEGER NOT NULL DEFAULT 0,
  last_error      TEXT NOT NULL DEFAULT '',
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);

CREATE TABLE pack_versions (
  pack_id    TEXT NOT NULL REFERENCES packs(id) ON DELETE CASCADE,
  seq        INTEGER NOT NULL,
  version    TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  pages      INTEGER NOT NULL DEFAULT 0,
  chunks     INTEGER NOT NULL DEFAULT 0,
  added      INTEGER NOT NULL DEFAULT 0,
  changed    INTEGER NOT NULL DEFAULT 0,
  removed    INTEGER NOT NULL DEFAULT 0,
  skipped    INTEGER NOT NULL DEFAULT 0,
  failed     INTEGER NOT NULL DEFAULT 0,
  digest     TEXT NOT NULL DEFAULT '{}',         -- the changed-pages digest (JSON, deterministic)
  summary    TEXT NOT NULL DEFAULT '',           -- optional cheap-model summary of the digest
  PRIMARY KEY (pack_id, seq)
);

CREATE TABLE pack_pages (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  pack_id       TEXT NOT NULL REFERENCES packs(id) ON DELETE CASCADE,
  url           TEXT NOT NULL,                   -- the citation URL (or local:<relative path>)
  title         TEXT NOT NULL DEFAULT '',
  content_hash  TEXT NOT NULL,                   -- sha256 of the normalized page
  etag          TEXT NOT NULL DEFAULT '',
  last_modified TEXT NOT NULL DEFAULT '',
  fetched_at    INTEGER NOT NULL,
  first_version TEXT NOT NULL DEFAULT '',
  last_version  TEXT NOT NULL DEFAULT '',        -- version in which the content last changed
  status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','removed')),
  risk          INTEGER NOT NULL DEFAULT 0,
  raw_ref       TEXT NOT NULL DEFAULT '',        -- file name of the normalized page under <data>/packs/<id>/pages/
  UNIQUE (pack_id, url)
);

CREATE TABLE pack_chunks (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  pack_id         TEXT NOT NULL REFERENCES packs(id) ON DELETE CASCADE,
  page_id         INTEGER NOT NULL REFERENCES pack_pages(id) ON DELETE CASCADE,
  ordinal         INTEGER NOT NULL,
  heading_path    TEXT NOT NULL,
  body            TEXT NOT NULL,
  token_est       INTEGER NOT NULL DEFAULT 0,
  chunk_hash      TEXT NOT NULL,                 -- sha256(heading_path + body)
  version_added   TEXT NOT NULL,
  seq_added       INTEGER NOT NULL,
  version_removed TEXT,
  seq_removed     INTEGER,
  risk            INTEGER NOT NULL DEFAULT 0,
  flags           TEXT NOT NULL DEFAULT '',      -- comma separated scan labels
  quarantined     INTEGER NOT NULL DEFAULT 0,    -- held back from retrieval until a human releases it
  released_by     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX pack_chunks_live ON pack_chunks(pack_id, seq_removed);
CREATE INDEX pack_chunks_page ON pack_chunks(page_id, seq_removed);

CREATE VIRTUAL TABLE pack_chunks_fts USING fts5(heading_path, body, content='pack_chunks', content_rowid='id', tokenize='porter unicode61');
-- +goose StatementBegin
CREATE TRIGGER pack_chunks_ai AFTER INSERT ON pack_chunks BEGIN
  INSERT INTO pack_chunks_fts(rowid, heading_path, body) VALUES (new.id, new.heading_path, new.body);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER pack_chunks_ad AFTER DELETE ON pack_chunks BEGIN
  INSERT INTO pack_chunks_fts(pack_chunks_fts, rowid, heading_path, body) VALUES ('delete', old.id, old.heading_path, old.body);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER pack_chunks_au AFTER UPDATE OF heading_path, body ON pack_chunks BEGIN
  INSERT INTO pack_chunks_fts(pack_chunks_fts, rowid, heading_path, body) VALUES ('delete', old.id, old.heading_path, old.body);
  INSERT INTO pack_chunks_fts(rowid, heading_path, body) VALUES (new.id, new.heading_path, new.body);
END;
-- +goose StatementEnd

-- ATTACH: a pack attached to a project, optionally to one agent or one role (empty = every agent of the project).
CREATE TABLE pack_attachments (
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  pack_id    TEXT NOT NULL REFERENCES packs(id) ON DELETE CASCADE,
  agent_id   TEXT NOT NULL DEFAULT '',
  role       TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  created_by TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (project_id, pack_id, agent_id, role)
);

-- A version that a stored citation points at is kept past the normal retention (last 2 versions).
CREATE TABLE pack_pins (
  pack_id    TEXT NOT NULL REFERENCES packs(id) ON DELETE CASCADE,
  version    TEXT NOT NULL,
  reason     TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  PRIMARY KEY (pack_id, version)
);

-- +goose Down
DROP TABLE pack_pins; DROP TABLE pack_attachments;
DROP TRIGGER pack_chunks_au; DROP TRIGGER pack_chunks_ad; DROP TRIGGER pack_chunks_ai; DROP TABLE pack_chunks_fts;
DROP TABLE pack_chunks; DROP TABLE pack_pages; DROP TABLE pack_versions; DROP TABLE packs;

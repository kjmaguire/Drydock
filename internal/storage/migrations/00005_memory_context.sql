-- +goose Up
CREATE TABLE memory (
  id         TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL CHECK (kind IN ('decision','convention','api_contract','rejected','known_issue','preference')),
  title      TEXT NOT NULL,
  body       TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'proposed' CHECK (status IN ('proposed','accepted','rejected','superseded')),
  source     TEXT NOT NULL DEFAULT '',   -- room message id, task id, 'human', 'import:<file>'
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  version    INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX memory_project ON memory(project_id, status, kind);

CREATE VIRTUAL TABLE memory_fts USING fts5(title, body, content='memory', content_rowid='rowid', tokenize='porter unicode61');
-- +goose StatementBegin
CREATE TRIGGER memory_ai AFTER INSERT ON memory BEGIN
  INSERT INTO memory_fts(rowid, title, body) VALUES (new.rowid, new.title, new.body);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER memory_ad AFTER DELETE ON memory BEGIN
  INSERT INTO memory_fts(memory_fts, rowid, title, body) VALUES ('delete', old.rowid, old.title, old.body);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER memory_au AFTER UPDATE ON memory BEGIN
  INSERT INTO memory_fts(memory_fts, rowid, title, body) VALUES ('delete', old.rowid, old.title, old.body);
  INSERT INTO memory_fts(rowid, title, body) VALUES (new.rowid, new.title, new.body);
END;
-- +goose StatementEnd

CREATE TABLE context_manifests (
  run_id     TEXT NOT NULL,
  node_id    TEXT NOT NULL,
  attempt    INTEGER NOT NULL,
  task_id    TEXT NOT NULL DEFAULT '',
  agent_id   TEXT NOT NULL DEFAULT '',
  items      TEXT NOT NULL,          -- JSON array of manifest items
  total_bytes INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (run_id, node_id, attempt)
);

CREATE TABLE generated_files (
  project_id TEXT NOT NULL,
  path       TEXT NOT NULL,          -- repo-relative, e.g. AGENTS.md
  body_sha   TEXT NOT NULL,          -- hash of the generated body, to detect human edits
  generated_at INTEGER NOT NULL,
  PRIMARY KEY (project_id, path)
);

-- +goose Down
DROP TABLE generated_files; DROP TABLE context_manifests; DROP TRIGGER memory_au; DROP TRIGGER memory_ad; DROP TRIGGER memory_ai; DROP TABLE memory_fts; DROP TABLE memory;

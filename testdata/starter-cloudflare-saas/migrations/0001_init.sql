-- 0001_init: users and items.
-- Apply locally:  npm run migrate:local
-- D1 wraps each migration in a transaction; keep statements idempotent where cheap.

CREATE TABLE IF NOT EXISTS users (
  id         TEXT PRIMARY KEY,
  email      TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS items (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id        TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  title          TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
  done           INTEGER NOT NULL DEFAULT 0 CHECK (done IN (0, 1)),
  attachment_key TEXT,
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_items_user_created ON items (user_id, created_at DESC, id DESC);

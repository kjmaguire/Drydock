-- +goose Up
-- The `scan` check (decision P131): a project may run the security scan's code and dependency engines (Opengrep and
-- osv-scanner) on every task's change. Off by default: it needs both tools, and osv-scanner sends package names to osv.dev.
ALTER TABLE project_check_settings ADD COLUMN scan_enabled INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE project_check_settings DROP COLUMN scan_enabled;

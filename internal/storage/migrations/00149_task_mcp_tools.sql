-- +goose Up
-- A task's narrowing of its agents' MCP tools (decision P115): the exposed tool names (globs) its turns may still use, out of
-- what the role table, the agent's grants and the project's rules give them. A task never widens: a tool outside those stays
-- out. No row: the task does not narrow.
CREATE TABLE task_mcp_tools (
  task_id TEXT PRIMARY KEY,
  tools TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  updated_by TEXT NOT NULL
);

-- +goose Down
DROP TABLE task_mcp_tools;

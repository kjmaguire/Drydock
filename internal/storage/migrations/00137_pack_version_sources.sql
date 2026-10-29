-- +goose Up
-- What each source of a pack version was read from (decision P110): for a git_docs source the commit its ref pointed at
-- ({"sources[0]": "<40 hex>"}), so a version's citations can be traced to the exact text of the repository. '{}' for versions
-- made before, and for sources that have no commit.
ALTER TABLE pack_versions ADD COLUMN sources TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE pack_versions DROP COLUMN sources;

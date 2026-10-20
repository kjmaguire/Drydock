-- +goose Up
-- The version of Drydock's skill checks (size, licence, safety) that last read each registry. A registry read under older
-- checks is read again even when its commit has not moved, so a skill blocked by a check that has since changed (a SKILL.md
-- "larger than 64 KiB" before the limit became 128 KiB) is judged again instead of staying blocked until upstream changes.
ALTER TABLE skill_registry_watch ADD COLUMN rules_version INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE skill_registry_watch DROP COLUMN rules_version;

-- +goose Up
-- Where the agent's CLI runs (decision P41): '' follows the project's default, 'native' runs it on Windows, 'wsl' inside the
-- WSL distro wsl_distro names. Machine-specific: a project export leaves both out.
ALTER TABLE agents ADD COLUMN run_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN wsl_distro TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE agents DROP COLUMN wsl_distro;
ALTER TABLE agents DROP COLUMN run_mode;

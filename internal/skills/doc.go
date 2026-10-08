// Package skills implements glob-scoped Markdown rules for agents (brief section 9, docs/expert-packs.md section 9).
//
// A skill is a directory with a SKILL.md in the Agent Skills format (YAML front matter plus Markdown). Drydock extends it only
// through metadata keys: drydock.globs, drydock.roles and drydock.source. Skills come from two places and from nowhere else:
// the repository's own .aet/skills/ (found by a scan of the project's main checkout, never of an agent's worktree) and an
// explicit allowlist of curated registries that a human edits. Registry content is pinned to a commit, validated against the
// specification, license-checked and scanned for secrets and injection patterns, and is only OFFERED: nothing from outside is
// written into a worktree or served to an agent until a human accepts it.
//
// Delivery follows what each CLI can do: Claude Code gets native path-scoped rules (.claude/rules/<name>.md with paths:
// front matter); Gemini CLI and Goose get nested instruction files for directory-anchored globs; Codex, OpenCode and
// Antigravity get a run-start selection copied into .agents/skills plus the on-demand skills tool. Every delivery records its
// method and fidelity. A skill never changes permissions: allowed-tools is ignored and scripts are never delivered.
package skills

// The per-role tool table's words and arithmetic (decision P115): what a click on a cell sends, what a row says, the rule
// form's check. The component is role-tools.tsx.

import type { RolePolicyCell, RolePolicyView } from '../../api/mcpClient.ts';

/**
 * What a click on a cell sends: the other value, or null when that other value is the table's own (the cell goes back to its
 * default, so a later change of the defaults reaches the project).
 */
export function nextCell(c: RolePolicyCell): boolean | null {
  const want = !c.allowed;
  return want === c.default ? null : want;
}

/** The cells of a row a person changed. */
export function changedColumns(view: RolePolicyView, role: string): string[] {
  const row = view.rows.find((r) => r.role === role);
  if (!row) return [];
  return view.columns.filter((c) => row.cells[c.key]?.changed).map((c) => c.key);
}

/** Why a column gives nothing right now, or '' when it does. */
export function columnNote(view: RolePolicyView, key: string): string {
  if (key === 'act' && !view.actTools) return 'Off while agents may not act in this project';
  if (key === 'repo_map' && !view.repoMap) return 'This build has no repo map';
  return '';
}

/** The accessible name of a cell's checkbox. */
export function cellLabel(roleLabel: string, columnLabel: string): string {
  return `${roleLabel}: ${columnLabel}`;
}

/** The rule form's check before anything is sent ('' when it may be sent). */
export function ruleProblem(name: string, expr: string): string {
  if (!name.trim()) return 'Give the rule a name.';
  if (name.trim().length > 120) return 'The name is at most 120 characters.';
  if (!expr.trim()) return 'Write the expression.';
  if (expr.length > 2048) return 'The expression is at most 2048 characters.';
  return '';
}

/** The variables a rule reads, for the form's help. */
export const RULE_VARIABLES: { name: string; help: string }[] = [
  { name: 'role', help: 'the row: pm, architect, ux designer, frontend, backend, database, devops, qa, security, reviewer, other' },
  { name: 'column', help: 'the column: read_range, repo_map, skills, search_memory, search_requirements, search_pack, act, docs, github, trackers, sentry, other' },
  { name: 'server', help: '"workspace" for Drydock\'s own tools, else the server\'s id' },
  { name: 'tool', help: 'the tool\'s own name' },
  { name: 'adapter', help: 'the agent\'s CLI: claude, codex, ...' },
  { name: 'task', help: 'true when the run works on a task' },
];

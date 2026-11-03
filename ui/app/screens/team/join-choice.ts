// An agent's join rule as one choice (X17): "Joins task rooms: never / every task / tasks that touch <folder>", on the agent's
// editor panel. The core keeps rules per project and agent (room.joinRules); this reads an agent's rules as one choice and says
// which rules to remove and add to make a new choice so. Pure functions; the panel calls the core.

import type { JoinRule } from '../../api/roomClient.ts';

export type JoinMode = 'never' | 'every' | 'folder';

export interface JoinChoice {
  mode: JoinMode;
  folder: string;
  /** How the agent joins: observer (reads, answers when mentioned) or active. */
  state: string;
}

/** The choice an agent's rules make: never without one, every task when one has no folder, else the first rule's folder. */
export function joinChoiceOf(rules: readonly JoinRule[], agentId: string): JoinChoice {
  const mine = rules.filter((r) => r.agentId === agentId);
  if (!mine.length) return { mode: 'never', folder: '', state: 'observer' };
  const every = mine.find((r) => !r.pathPrefix);
  if (every) return { mode: 'every', folder: '', state: every.state || 'observer' };
  return { mode: 'folder', folder: mine[0]!.pathPrefix ?? '', state: mine[0]!.state || 'observer' };
}

/** The rules to remove and the one to add so the agent's rules say `next`; nothing when they say it already. */
export function joinChange(rules: readonly JoinRule[], agentId: string, next: JoinChoice): { remove: string[]; add?: { pathPrefix: string; state: string } } {
  const mine = rules.filter((r) => r.agentId === agentId);
  const folder = next.mode === 'folder' ? normFolder(next.folder) : '';
  if (next.mode === 'never') return { remove: mine.map((r) => r.id) };
  const same = mine.length === 1 && (mine[0]!.pathPrefix ?? '') === folder && (mine[0]!.state || 'observer') === next.state;
  if (same) return { remove: [] };
  return { remove: mine.map((r) => r.id), add: { pathPrefix: folder, state: next.state } };
}

/** A folder as the core keeps it: slashes, no leading "./" or "/", one trailing "/". */
export function normFolder(f: string): string {
  const s = f.trim().replaceAll('\\', '/').replace(/^\.?\/+/, '').replace(/\/+$/, '');
  return s ? `${s}/` : '';
}

/** The choice in words, for the Room tab's summary line: "Sec joins every task as an observer". */
export function joinWords(name: string, c: JoinChoice): string {
  const as = c.state === 'active' ? 'an active member' : 'an observer';
  if (c.mode === 'never') return `${name} joins no task rooms by rule`;
  return c.mode === 'every' ? `${name} joins every task room as ${as}` : `${name} joins the rooms of tasks that touch ${c.folder} as ${as}`;
}

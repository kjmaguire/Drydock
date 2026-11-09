// An agent's join rules as one choice (X17): "Joins task rooms: never / every task / tasks that touch these folders", on the
// agent's editor panel. The core keeps rules per project and agent (room.joinRules), and an agent may have several (one per
// folder); this reads them as one choice that holds every folder, and says which rules to remove and add to make a new choice
// so, leaving a rule that already says what the choice says untouched. Pure functions; the panel calls the core.

import type { JoinRule } from '../../api/roomClient.ts';

export type JoinMode = 'never' | 'every' | 'folder';

export interface JoinChoice {
  mode: JoinMode;
  /** Every folder the agent joins tasks by (mode folder), as the core keeps them. */
  folders: string[];
  /** How the agent joins: observer (reads, answers when mentioned) or active. */
  state: string;
  /** The agent's folder rules join in different ways (some observer, some active): choosing Joins as sets one for all. */
  mixed?: boolean;
}

const stateOf = (r: JoinRule) => r.state || 'observer';

/** The choice an agent's rules make: never without one, every task when one has no folder, else every folder of its rules. */
export function joinChoiceOf(rules: readonly JoinRule[], agentId: string): JoinChoice {
  const mine = rules.filter((r) => r.agentId === agentId);
  if (!mine.length) return { mode: 'never', folders: [], state: 'observer' };
  const every = mine.find((r) => !r.pathPrefix);
  if (every) return { mode: 'every', folders: [], state: stateOf(every) };
  const folders = [...new Set(mine.map((r) => r.pathPrefix ?? ''))];
  const states = new Set(mine.map(stateOf));
  return { mode: 'folder', folders, state: stateOf(mine[0]!), ...(states.size > 1 ? { mixed: true } : {}) };
}

/**
 * The rules to remove and add so the agent's rules say `next`: a rule that already says one of next's folders (or every task)
 * with next's state stays; every other rule of the agent goes; each folder no rule says yet is added. Nothing when they say it.
 */
export function joinChange(rules: readonly JoinRule[], agentId: string, next: JoinChoice): { remove: string[]; add: { pathPrefix: string; state: string }[] } {
  const mine = rules.filter((r) => r.agentId === agentId);
  if (next.mode === 'never') return { remove: mine.map((r) => r.id), add: [] };
  const want = next.mode === 'every' ? [''] : [...new Set(next.folders.map(normFolder).filter(Boolean))];
  const kept = new Set<string>();
  const remove: string[] = [];
  for (const r of mine) {
    const p = r.pathPrefix ?? '';
    if (want.includes(p) && stateOf(r) === next.state && !kept.has(p)) kept.add(p);
    else remove.push(r.id);
  }
  return { remove, add: want.filter((p) => !kept.has(p)).map((pathPrefix) => ({ pathPrefix, state: next.state })) };
}

/** A folder as the core keeps it: slashes, no leading "./" or "/", one trailing "/". */
export function normFolder(f: string): string {
  const s = f.trim().replaceAll('\\', '/').replace(/^\.?\/+/, '').replace(/\/+$/, '');
  return s ? `${s}/` : '';
}

const asWords = (state: string) => (state === 'active' ? 'an active member' : 'an observer');
const orList = (xs: readonly string[]) => (xs.length < 2 ? (xs[0] ?? '') : `${xs.slice(0, -1).join(', ')} or ${xs[xs.length - 1]}`);

/**
 * An agent's rules in words, for the Room tab's summary line, naming every folder: "Sec joins the rooms of tasks that touch
 * auth/ or api/ as an observer"; folders that join in different ways are said apart.
 */
export function joinWords(name: string, rules: readonly JoinRule[], agentId: string): string {
  const mine = rules.filter((r) => r.agentId === agentId);
  if (!mine.length) return `${name} joins no task rooms by rule`;
  const every = mine.find((r) => !r.pathPrefix);
  if (every) return `${name} joins every task room as ${asWords(stateOf(every))}`;
  const byState = new Map<string, string[]>();
  for (const r of mine) {
    const list = byState.get(stateOf(r)) ?? [];
    if (!list.includes(r.pathPrefix ?? '')) list.push(r.pathPrefix ?? '');
    byState.set(stateOf(r), list);
  }
  const parts = [...byState].map(([state, folders]) => `${orList(folders)} as ${asWords(state)}`);
  return `${name} joins the rooms of tasks that touch ${parts.join(', and ')}`;
}

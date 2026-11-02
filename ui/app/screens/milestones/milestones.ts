// Pure helpers for the Milestones screen.

import type { Milestone } from '../../api/extTypes.ts';

export const zeroTime = (s?: string): boolean => !s || s.startsWith('0001-');

/** The progress in words, always next to the bar (never color alone). */
export function progressText(p: Milestone['progress']): string {
  return `${p.completed} of ${p.total} completed; ${p.inProgress} in progress, ${p.blocked} blocked, ${p.open} not started, ${p.failed} failed, ${p.cancelled} cancelled.`;
}

/** A date input's yyyy-mm-dd as the ISO time the core stores (midnight UTC); '' means no due date. */
export const dueISO = (date: string): string | undefined => (date ? new Date(`${date}T00:00:00Z`).toISOString() : undefined);

/** The bar's segments as percentages of the total, in a fixed order. */
export function segments(p: Milestone['progress']): { key: 'completed' | 'inProgress' | 'blocked' | 'failed'; pct: number }[] {
  if (p.total <= 0) return [];
  const pct = (n: number) => (n / p.total) * 100;
  return (['completed', 'inProgress', 'blocked', 'failed'] as const).map((key) => ({ key, pct: pct(p[key]) })).filter((x) => x.pct > 0);
}

/** A task row as the Add tasks list shows it. */
export const taskLabel = (x: { seq: number; title: string }): string => `#${x.seq} ${x.title}`;

/** Whether a task matches the Add tasks filter: its number, title or one of its labels, ignoring case. */
export function matchesFilter(x: { seq: number; title: string; labels?: string[] }, q: string): boolean {
  const s = q.trim().toLowerCase().replace(/^#/, '');
  if (!s) return true;
  return String(x.seq) === s || x.title.toLowerCase().includes(s) || (x.labels ?? []).some((l) => l.includes(s));
}

export interface TaskGroup {
  key: string;
  label: string;
  ids: string[];
}

/**
 * The ways to pick many tasks at once, over the tasks that can still be added: all tasks of an accepted plan, all tasks with a
 * label. A group with nothing left to add is left out.
 */
export function taskGroups(free: readonly { id: string; labels?: string[] }[], plans: readonly { id: string; goal: string; status: string; createdTasks?: Record<string, string> | null }[]): TaskGroup[] {
  const freeIds = new Set(free.map((x) => x.id));
  const out: TaskGroup[] = [];
  for (const p of plans) {
    if (p.status !== 'ACCEPTED') continue;
    const ids = Object.values(p.createdTasks ?? {}).filter((id) => freeIds.has(id));
    const goal = p.goal.length > 50 ? `${p.goal.slice(0, 49)}…` : p.goal;
    if (ids.length > 0) out.push({ key: `plan:${p.id}`, label: `All tasks of the plan "${goal}"`, ids });
  }
  const labels = [...new Set(free.flatMap((x) => x.labels ?? []))].sort();
  for (const l of labels) out.push({ key: `label:${l}`, label: `All tasks labelled ${l}`, ids: free.filter((x) => (x.labels ?? []).includes(l)).map((x) => x.id) });
  return out;
}

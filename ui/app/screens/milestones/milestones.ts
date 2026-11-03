// Pure helpers for the Milestones screen.

import type { Milestone } from '../../api/extTypes.ts';

export const zeroTime = (s?: string): boolean => !s || s.startsWith('0001-');

/** The progress in words, always next to the bar (never color alone). */
export function progressText(p: Milestone['progress']): string {
  return `${p.completed} of ${p.total} completed; ${p.inProgress} in progress, ${p.blocked} blocked, ${p.open} not started, ${p.failed} failed, ${p.cancelled} cancelled.`;
}

/** Tasks of a milestone still to finish, as the core counts them when it is closed: not completed, cancelled or failed. */
export const unfinishedOf = (p: Milestone['progress']): number => Math.max(0, p.total - p.completed - p.cancelled - p.failed);

/** Whether every task was completed or cancelled and at least one was completed: only then does the footer say "All done". A
 * failed task counts as finished for closing (the core closes without force), but not as done. */
export const allDoneOf = (p: Milestone['progress']): boolean => p.completed > 0 && p.completed + p.cancelled === p.total;

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

const DAY = 86_400_000;

/**
 * Where an open milestone stands against its date (X411): "due today", "due in 3 days", "overdue by 2 days", or "" with no date.
 * Overdue is the core's word (an open milestone past its date); the days are counted from `now`.
 */
export function dueText(m: Pick<Milestone, 'dueAt' | 'overdue' | 'status'>, now: number): string {
  if (m.status !== 'open' || zeroTime(m.dueAt)) return '';
  const days = Math.round((new Date(m.dueAt!).getTime() - now) / DAY);
  if (m.overdue) {
    const late = Math.max(1, -days);
    return `overdue by ${late} ${late === 1 ? 'day' : 'days'}`;
  }
  if (days <= 0) return 'due today';
  return `due in ${days} ${days === 1 ? 'day' : 'days'}`;
}

/** The milestone Work's rail shows (X411): an overdue open one first, then the open one due soonest; none without a date. */
export function nextMilestone<M extends Pick<Milestone, 'dueAt' | 'overdue' | 'status'>>(list: readonly M[]): M | undefined {
  const dated = list.filter((m) => m.status === 'open' && !zeroTime(m.dueAt));
  return [...dated].sort((a, b) => Number(b.overdue) - Number(a.overdue) || new Date(a.dueAt!).getTime() - new Date(b.dueAt!).getTime())[0];
}

/** The open milestones a task is in, and the open ones it could be added to (X411: the task page's Milestone row). */
export function milestonesOfTask<M extends Pick<Milestone, 'id' | 'status' | 'taskIds'>>(list: readonly M[], taskId: string): { in: M[]; open: M[] } {
  const has = (m: M) => (m.taskIds ?? []).includes(taskId);
  return { in: list.filter(has), open: list.filter((m) => m.status === 'open' && !has(m)) };
}

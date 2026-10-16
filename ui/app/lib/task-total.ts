// How much of a project's task list a screen has (audit 2026-10-16, M21). task.list returns only the newest TASK_LIST_LIMIT tasks
// (core.TaskListDefault) and task.count says how many there are, so a screen that presents the list as the whole must say when it
// is not: "Showing the newest 500 of 612 tasks". Pure, so the wording and the rule are tested in Node.

import { TASK_LIST_LIMIT } from '../api/types.ts';

/** What was loaded against what the project has. */
export interface TaskCoverage {
  /** How many tasks the screen has. */
  loaded: number;
  /** How many tasks the project has in all, when the core said (undefined while unknown: the count could not be read). */
  total: number | undefined;
  /** The screen does not have every task. */
  partial: boolean;
}

/**
 * The coverage of `loaded` tasks. With the core's count the list is partial when the project has more tasks than were loaded.
 * Without it (the count could not be read) a list as long as the core ever sends is taken to be cut, never to be complete.
 */
export function taskCoverage(loaded: number, total: number | undefined): TaskCoverage {
  return { loaded, total, partial: total !== undefined ? total > loaded : loaded >= TASK_LIST_LIMIT };
}

const n = (x: number): string => x.toLocaleString('en-US');

/** "Showing the newest 500 of 612 tasks", or, when the count is unknown, that there may be more. */
export function taskNoticeText(c: TaskCoverage): string {
  return c.total !== undefined ? `Showing the newest ${n(c.loaded)} of ${n(c.total)} tasks` : `Showing the newest ${n(c.loaded)} tasks; there may be more`;
}

/** What a search over the task list leaves out: "Only the newest 500 of 612 tasks are searched." */
export function taskSearchNote(c: TaskCoverage): string {
  return c.total !== undefined ? `Only the newest ${n(c.loaded)} of ${n(c.total)} tasks are searched.` : `Only the newest ${n(c.loaded)} tasks are searched; there may be more.`;
}

/** The count a page head states: "12 tasks", "1 task", or "500 of 612 tasks" when the list is not everything. */
export function taskCountText(c: TaskCoverage): string {
  if (c.partial && c.total !== undefined) return `${n(c.loaded)} of ${n(c.total)} tasks`;
  return `${n(c.loaded)} ${c.loaded === 1 ? 'task' : 'tasks'}${c.partial ? ' (newest; there may be more)' : ''}`;
}

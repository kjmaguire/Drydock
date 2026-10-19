// Pure helpers of the Overview: the day's words and the Insights numbers. Each number is what the core sent, in a window
// this file states; a measurement the core has not made reads n/a, never 0.

import type { BudgetSpend, BudgetStatus, EvalsReport } from '../../api/extTypes.ts';
import type { Task } from '../../api/types.ts';
import { ratioRate } from '../evals/evals.ts';

export const DONE = new Set(['COMPLETED', 'FAILED', 'CANCELLED']);
export const WORKING = /^(working|thinking)/i;
export const WEEK_MS = 7 * 86_400_000;

/** A core timestamp in milliseconds: the core writes seconds in some fields and milliseconds in others. */
export function ms(n: number | undefined): number {
  if (!n) return 0;
  return n < 1e11 ? n * 1000 : n;
}

export function greeting(hour: number): string {
  return hour < 12 ? 'Good morning' : hour < 18 ? 'Good afternoon' : 'Good evening';
}

/** What an agent is doing, in one line (one implementation, with the Crew pages). */
export { doing } from '../team/team.ts';

/** The tasks completed at or after `since` (milliseconds). */
export const mergedSince = (tasks: readonly Pick<Task, 'status' | 'completed' | 'updated'>[], since: number): number =>
  tasks.filter((x) => x.status === 'COMPLETED' && ms(x.completed ?? x.updated) >= since).length;

/** The start of today, local time. */
export function dayStart(now = Date.now()): number {
  const d = new Date(now);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

export interface SpendLine {
  /** "$2.50", or "n/a" when nothing was priced. */
  amount: string;
  /** "of $5 budget", "3 calls not priced", "no budget set"; the words under the number. */
  note: string;
  /** The project budget's used fraction, when one with a dollar limit exists. */
  fraction?: number;
}

/**
 * The project's spend so far (the core's budget.status usage, which has no time window) against its dollar budget. Calls
 * with unknown cost are counted, never priced at zero, and are said.
 */
export function spendLine(usage: BudgetSpend | undefined, statuses: readonly BudgetStatus[]): SpendLine {
  const known = usage ? usage.reportedUsd + usage.estimatedUsd : 0;
  const unknown = usage?.unknownCostRows ?? 0;
  const project = statuses.find((s) => s.budget.scope === 'project' && s.budget.enabled && s.budget.limitUsd != null);
  const amount = known > 0 ? `$${known.toFixed(2)}` : unknown > 0 ? 'n/a' : '$0.00';
  const notes: string[] = [];
  if (project) notes.push(`of $${project.budget.limitUsd} budget`);
  else notes.push('no budget set');
  if (unknown > 0) notes.push(`${unknown} ${unknown === 1 ? 'call' : 'calls'} not priced`);
  return { amount, note: notes.join(' · '), fraction: project ? (project.pctUsd ?? undefined) : undefined };
}

/** "88%" of tasks whose first check passed its tests, or "n/a" before any task was checked. */
export function firstCheckText(report: EvalsReport | undefined): string {
  const rate = ratioRate(report?.quality?.firstCheckTestPass);
  return rate === undefined ? 'n/a' : `${Math.round(rate * 100)}%`;
}

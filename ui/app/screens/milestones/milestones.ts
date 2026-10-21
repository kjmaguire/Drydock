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

// Pure helpers for the Evals report: ratio text, the window choices and the options a read sends.

import type { EvalsOptions, Ratio } from '../../api/extTypes.ts';

/** A ratio over a denominator. A zero denominator is "n/a", never 0%: a missing measurement is not a measured zero. */
export function ratioText(r: Ratio | undefined | null): string {
  if (!r || r.den === 0 || r.rate === null || r.rate === undefined) return 'n/a';
  return `${r.num} of ${r.den} (${Math.round(r.rate * 100)}%)`;
}

/** The rate to draw, or undefined when there is no measurement. */
export function ratioRate(r: Ratio | undefined | null): number | undefined {
  if (!r || r.den === 0 || r.rate === null || r.rate === undefined) return undefined;
  return r.rate;
}

export const WINDOWS: [string, number][] = [
  ['All time', 0],
  ['Last 7 days', 7],
  ['Last 30 days', 30],
];

const DAY_MS = 86_400_000;

/** The report options: this project only (when chosen and known) and the window start. */
export function evalsOptions(projectId: string | undefined, days: number, thisProject: boolean, now = Date.now()): EvalsOptions {
  return { projectId: thisProject ? projectId : undefined, sinceMs: days > 0 ? now - days * DAY_MS : undefined };
}

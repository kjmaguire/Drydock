// Pure helpers for the project screens (no DOM, no services).

import { CONTEXT_CEILING, type DevSettings } from '../../api/extTypes.ts';

/** One argument per line; blank lines and surrounding spaces are dropped. */
export const argv = (s: string): string[] =>
  s
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean);

/** A number typed into a field, or undefined when it is empty or not a number. */
export function numOrUndef(s: string): number | undefined {
  const v = s.trim();
  if (v === '') return undefined;
  const n = Number(v);
  return Number.isFinite(n) ? n : undefined;
}

/** The settings sent to projectdev.set: the command boxes become argv lists, timeouts default to 0. */
export function devBody(s: DevSettings, e2eCmd: string, previewCmd: string): DevSettings {
  return {
    ...s,
    e2e: { ...s.e2e, command: argv(e2eCmd), timeoutSec: s.e2e.timeoutSec ?? 0 },
    preview: { ...s.preview, command: argv(previewCmd), timeoutSec: s.preview.timeoutSec ?? 0 },
  };
}

export const MODES: [string, string][] = [
  ['local_merge', 'Merge locally after my approval'],
  ['pr_flow', 'Open a GitHub pull request (needs a github.com origin and gh)'],
];

export const STRATEGIES: [string, string][] = [
  ['merge', 'Merge commit'],
  ['squash', 'Squash'],
  ['rebase', 'Rebase'],
];

/** Anthropic suggests 3 to 5 agents at once; above five the page says why more rarely pays off. */
export const parallelWarn = (n: number): boolean => n > 5;

/** A context ceiling the core refuses: set, and outside CONTEXT_CEILING's bounds (0 or empty means the default). */
export const ceilingOff = (n: number | undefined): boolean => !!n && (n < CONTEXT_CEILING.min || n > CONTEXT_CEILING.max);

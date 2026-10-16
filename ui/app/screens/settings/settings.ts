// Pure data for the Settings screen.

import { boxLines, isEnvName } from '../../lib/envname.ts';
import type { ThemeChoice } from '../../lib/theme.ts';

/** docs/ui.md section 4: features that do not exist yet, with the slice that owns them. Keep in step with that table. */
export const NOT_BUILT: { feature: string; slice: string; meanwhile: string }[] = [
  {
    feature: 'Live verification against vendor accounts (Linear, Notion, Sentry, Aikido)',
    slice: 'Slice 5',
    meanwhile:
      "Token and OAuth sign-in are built and tested against protocol fixtures; Drydock's own tests have used no vendor account. Check against your account, on each connected service's Integrations panel, runs the read-only checks against yours.",
  },
  { feature: 'Cloud sync, accounts, team sharing', slice: 'never', meanwhile: 'Drydock has no cloud component.' },
];

export const THEMES: { value: ThemeChoice; label: string; hint: string }[] = [
  { value: 'system', label: 'System', hint: 'Follow Windows' },
  { value: 'dark', label: 'Dark', hint: 'Dark everywhere' },
  { value: 'light', label: 'Light', hint: 'Light everywhere' },
];

/**
 * What the agent-environment box says about line `n` when it is not a name. It names the line and never repeats it: a line that
 * is not a name may be NAME=value with a secret in it (audit 2026-10-16, L38).
 */
export const envLineError = (n: number): string => `Line ${n} is not a variable name. Give names only, never values.`;

/**
 * The names typed into the agent-environment box: one per line, trimmed, blank lines skipped. The first line that is not a name is
 * refused here, before anything is sent, and counted as the person sees the lines in the box.
 */
export function parseEnvNames(text: string): { ok: true; names: string[] } | { ok: false; error: string } {
  const names: string[] = [];
  for (const line of boxLines(text)) {
    const name = line.text.trim();
    if (name === '') continue;
    if (!isEnvName(name)) return { ok: false, error: envLineError(line.n) };
    names.push(name);
  }
  return { ok: true, names };
}

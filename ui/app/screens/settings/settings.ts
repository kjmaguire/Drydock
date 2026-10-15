// Pure data for the Settings screen.

import type { ThemeChoice } from '../../lib/theme.ts';

/** docs/ui.md section 4: features that do not exist yet, with the slice that owns them. Keep in step with that table. */
export const NOT_BUILT: { feature: string; slice: string; meanwhile: string }[] = [
  { feature: 'Repo map view', slice: 'gated by decision D5', meanwhile: 'Absent until the Windows CI job (tree-sitter with CGO) is green, per decision D5.' },
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

/** The names typed into the agent-environment box: one per line, trimmed, blank lines dropped. */
export function envNames(text: string): string[] {
  return text
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean);
}

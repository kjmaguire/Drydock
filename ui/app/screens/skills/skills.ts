// Pure helpers for the Skills screen.

import type { SkillCoverage, SkillScan } from '../../api/extTypes.ts';
import type { Tone } from '../../ui/tokens.ts';

export const skillTone = (s: string): Tone => (s === 'accepted' ? 'ok' : s === 'offered' ? 'warn' : s === 'blocked' ? 'err' : 'muted');

/** What a scan did, in one sentence, with the core's problems verbatim. */
export function scanText(scan: SkillScan): string {
  const errors = scan.errors ?? [];
  return `Found ${scan.found}: ${scan.offered} offered, ${scan.accepted} accepted, ${scan.blocked} blocked, ${scan.removed} removed.${errors.length > 0 ? ` Problems: ${errors.join('; ')}` : ''}`;
}

export const coverageText = (c: SkillCoverage | undefined): string => (c ? `${c.files} delivered as rule files, ${c.tool} through a tool.` : '');

/** A list as words, with a fallback for none ('all'). */
export const listOr = (xs: readonly string[] | null | undefined, none = 'all'): string => (xs ?? []).join(', ') || none;

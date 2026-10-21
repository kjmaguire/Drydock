// Pure helpers of the Repo map page (decision P62).

import { REPO_MAP_DEFAULT_BUDGET, REPO_MAP_MAX_BUDGET, REPO_MAP_MIN_BUDGET, type RepoMapResult, type RepoMapSkipped } from '../../api/repomapClient.ts';

/** The focus field: files or folders separated by commas or new lines, trimmed, without blanks or repeats. */
export function parseFocus(text: string): string[] {
  const out: string[] = [];
  for (const part of text.split(/[,\n]/)) {
    const p = part.trim();
    if (p && !out.includes(p)) out.push(p);
  }
  return out;
}

/** The budget field: a number kept inside 256..8192; anything else is the default. */
export function budgetOf(text: string): number {
  const n = Math.round(Number(text.trim()));
  if (!text.trim() || !Number.isFinite(n)) return REPO_MAP_DEFAULT_BUDGET;
  return Math.min(REPO_MAP_MAX_BUDGET, Math.max(REPO_MAP_MIN_BUDGET, n));
}

const SKIP_LABELS: [keyof RepoMapSkipped, string][] = [
  ['unsupported', 'in other languages or not code'],
  ['vendored', 'vendored'],
  ['generated', 'generated'],
  ['tooLarge', 'too large'],
  ['binary', 'binary'],
  ['unreadable', 'unreadable'],
  ['linked', 'links (not read)'],
  ['overLimit', 'over the file limit'],
];

/** "12 in other languages or not code, 3 vendored" (only the reasons that left files out), or "". */
export function skippedText(s: RepoMapSkipped): string {
  return SKIP_LABELS.filter(([k]) => s[k] > 0)
    .map(([k, label]) => `${s[k]} ${label}`)
    .join(', ');
}

/** "go 40, typescript 12" by count, most first. */
export function languagesText(langs: Record<string, number>): string {
  return Object.entries(langs)
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([l, n]) => `${l} ${n}`)
    .join(', ');
}

/** The summary line under the form. */
export function statsText(r: RepoMapResult): string {
  const files = r.files === 1 ? '1 source file' : `${r.files} source files`;
  const cache = r.cached > 0 ? `, ${r.cached} from the cache` : '';
  return `${r.shown} of ${files} in the map, about ${r.tokens} of ${r.budget} tokens. Made in ${r.elapsedMs} ms${cache}.`;
}

/** A file's PageRank as a percentage of the whole repository's (they add up to 100). */
export function rankText(rank: number): string {
  const p = rank * 100;
  return p >= 10 ? `${p.toFixed(0)}%` : p >= 1 ? `${p.toFixed(1)}%` : `${p.toFixed(2)}%`;
}

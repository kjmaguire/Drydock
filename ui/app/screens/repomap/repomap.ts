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

// --- what the person chose, kept per project (X412) ---

/** A project's repo map as the person last made it: the focus paths and the budget (kept per project in drydock.repomap). */
export interface RepoMapPrefs {
  focus: string[];
  budget: number;
}

/** How many projects' choices are remembered; the oldest written is let go first. */
export const REPO_MAP_PREFS_MAX = 50;
const MAX_FOCUS = 64;

const isRec = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);

/** The project's remembered choices out of what is stored; anything else, or nothing, is the whole repository at the default budget. */
export function repoMapPrefsOf(raw: unknown, projectId: string): RepoMapPrefs {
  const v = isRec(raw) && isRec(raw[projectId]) ? raw[projectId] : {};
  const focus = Array.isArray(v.focus) ? parseFocus(v.focus.filter((x): x is string => typeof x === 'string' && x.length <= 512).join(',')).slice(0, MAX_FOCUS) : [];
  const budget = typeof v.budget === 'number' ? budgetOf(String(v.budget)) : REPO_MAP_DEFAULT_BUDGET;
  return { focus, budget };
}

/** What is stored once the project's choices are kept: the other projects' kept as they are (well-formed ones only), at most REPO_MAP_PREFS_MAX. */
export function withRepoMapPrefs(raw: unknown, projectId: string, prefs: RepoMapPrefs): Record<string, RepoMapPrefs> {
  const out: Record<string, RepoMapPrefs> = {};
  const others = isRec(raw) ? Object.keys(raw).filter((k) => k !== projectId) : [];
  for (const k of others.slice(Math.max(0, others.length - (REPO_MAP_PREFS_MAX - 1)))) out[k] = repoMapPrefsOf(raw, k);
  out[projectId] = repoMapPrefsOf({ [projectId]: prefs }, projectId);
  return out;
}

/** The budgets offered as one click each. */
export const BUDGET_CHOICES: readonly { value: number; label: string }[] = [
  { value: 1024, label: '1k' },
  { value: 2048, label: '2k' },
  { value: 4096, label: '4k' },
  { value: 8192, label: '8k' },
];

/** The next budget up for "Double the budget", or undefined at the most the map takes. */
export const doubled = (budget: number): number | undefined => (budget >= REPO_MAP_MAX_BUDGET ? undefined : Math.min(REPO_MAP_MAX_BUDGET, budget * 2));

/** The focus with one path put in, or taken out when it is there already. */
export const toggleFocus = (focus: readonly string[], path: string): string[] => (focus.includes(path) ? focus.filter((p) => p !== path) : [...focus, path]);

/** The tasks whose files make a focus in one click: open tasks that name files, the latest changed first, at most `n`. */
export function taskFocusChips<T extends { id: string; seq: number; status: string; updated: number; files?: string[] }>(tasks: readonly T[], n = 5): { id: string; seq: number; files: string[] }[] {
  const done = new Set(['COMPLETED', 'CANCELLED', 'FAILED']);
  return tasks
    .filter((x) => !done.has(x.status) && (x.files ?? []).some((f) => f.trim()))
    .sort((a, b) => b.updated - a.updated)
    .slice(0, n)
    .map((x) => ({ id: x.id, seq: x.seq, files: parseFocus((x.files ?? []).join(',')) }));
}

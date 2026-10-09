// Pure helpers for the task screens: status groups and filters for the list, evidence results and labels, the shape of a
// pending request's payload, diff line classes, and the text of acceptance criteria. No DOM and no client here.

import type { BudgetPausePayload, Criterion, SpecReviewPayload } from '../../api/extTypes.ts';
import type { Evidence, PendingRequest, Task } from '../../api/types.ts';

// --- the list: status groups (Linear-style) and filters ---

/** The order status groups appear in: what waits on the human first, then work in flight, then the backlog, then closed. */
export const STATUS_ORDER = ['WAITING_HUMAN', 'VERIFICATION', 'BLOCKED', 'IN_PROGRESS', 'REVIEW', 'CLAIMED', 'READY', 'BACKLOG', 'COMPLETED', 'FAILED', 'CANCELLED'] as const;

export type TaskFilter = 'all' | 'waiting' | 'active' | 'backlog' | 'done';

export const FILTERS: { name: TaskFilter; label: string; statuses?: readonly string[] }[] = [
  { name: 'all', label: 'All' },
  { name: 'waiting', label: 'Waiting on you', statuses: ['WAITING_HUMAN', 'VERIFICATION', 'BLOCKED'] },
  { name: 'active', label: 'Active', statuses: ['IN_PROGRESS', 'REVIEW', 'CLAIMED', 'READY'] },
  { name: 'backlog', label: 'Backlog', statuses: ['BACKLOG'] },
  { name: 'done', label: 'Done', statuses: ['COMPLETED', 'FAILED', 'CANCELLED'] },
];

/** True when the task belongs under the filter. A status the UI does not know shows under All only. */
export function inFilter(t: Pick<Task, 'status'>, f: TaskFilter): boolean {
  const def = FILTERS.find((x) => x.name === f);
  return !def?.statuses || def.statuses.includes(t.status);
}

/** Free-text match on the title, the #number and the branch (case-insensitive). Empty text matches everything. */
export function matchesText(t: Pick<Task, 'title' | 'seq' | 'branch'>, text: string): boolean {
  const q = text.trim().toLowerCase();
  if (!q) return true;
  if (q.replace(/^#/, '') === String(t.seq)) return true;
  return t.title.toLowerCase().includes(q) || (t.branch ?? '').toLowerCase().includes(q);
}

export function filterCounts(tasks: readonly Pick<Task, 'status'>[]): Record<TaskFilter, number> {
  const out = { all: 0, waiting: 0, active: 0, backlog: 0, done: 0 } as Record<TaskFilter, number>;
  for (const t of tasks) for (const f of FILTERS) if (inFilter(t, f.name)) out[f.name]++;
  return out;
}

export interface TaskGroup<T> {
  status: string;
  tasks: T[];
}

/** Group tasks by status in STATUS_ORDER (unknown statuses last), newest update first within a group. */
export function groupByStatus<T extends Pick<Task, 'status' | 'updated' | 'seq'>>(tasks: readonly T[]): TaskGroup<T>[] {
  const by = new Map<string, T[]>();
  for (const t of tasks) {
    const g = by.get(t.status);
    if (g) g.push(t);
    else by.set(t.status, [t]);
  }
  const rank = (s: string) => {
    const i = (STATUS_ORDER as readonly string[]).indexOf(s);
    return i < 0 ? STATUS_ORDER.length : i;
  };
  return [...by.entries()]
    .sort((a, b) => rank(a[0]) - rank(b[0]) || a[0].localeCompare(b[0]))
    .map(([status, list]) => ({ status, tasks: list.sort((a, b) => b.updated - a.updated || b.seq - a.seq) }));
}

/** "now", "5m", "3h", "2d", then a date. For the dense list only: the task page shows full times. */
export function ago(ms: number, now = Date.now()): string {
  if (!ms) return '';
  const s = Math.max(0, Math.round((now - ms) / 1000));
  if (s < 60) return 'now';
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  if (s < 86400 * 30) return `${Math.floor(s / 86400)}d`;
  return new Date(ms).toLocaleDateString();
}

// --- create ---

/** Acceptance criteria are typed one per line; blank lines are dropped and each line is trimmed. */
export function parseAcceptance(text: string): string[] {
  return text
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean);
}

// --- detail ---

const CLOSED = ['COMPLETED', 'FAILED', 'CANCELLED'];

/** A closed task (completed, failed or cancelled) has no assignment or controls. */
export const isTerminal = (status: string): boolean => CLOSED.includes(status);

/** A run still in flight: assignment and start are hidden while it runs; only cancel stays. */
export const isRunActive = (run: { status: string } | undefined): boolean => !!run && !CLOSED.includes(run.status);

/** A BLOCKED task waiting for an earlier task can be let go on by the human. */
export const isWaitingForTask = (t: Pick<Task, 'status' | 'blockedReason'>): boolean => t.status === 'BLOCKED' && !!t.blockedReason?.startsWith('waiting for');

// --- evidence ---

export const EVIDENCE_LABEL: Record<string, string> = {
  spec: 'Spec written',
  spec_accept: 'Spec accepted',
  red_test: 'Test written first (red)',
  green_test: 'Tests now pass (green)',
  design: 'Design notes',
  e2e: 'End-to-end check',
  screenshot: 'Screenshot',
  preview: 'Preview',
  test: 'Tests',
  diff: 'Diff',
  commit: 'Commit',
};

export type EvidenceResult = 'pass' | 'fail' | 'skipped' | 'info';

/** passed true/false is pass/fail; an e2e or preview without a result was skipped (never a pass); anything else is info. */
export function evidenceResult(e: Pick<Evidence, 'kind' | 'passed'>): EvidenceResult {
  if (e.passed === true) return 'pass';
  if (e.passed === false) return 'fail';
  return e.kind === 'e2e' || e.kind === 'preview' ? 'skipped' : 'info';
}

export const RESULT_TEXT: Record<EvidenceResult, string> = { pass: 'PASS', fail: 'FAIL', skipped: 'SKIPPED', info: 'INFO' };

// --- pending requests ---

/** The files and branches a review request names (anything malformed is left out). */
export function payloadOf(p: PendingRequest): { files: string[]; branch?: string; base?: string } {
  const v = (p.spec.payload ?? {}) as { files?: unknown; branch?: unknown; base?: unknown };
  return {
    files: Array.isArray(v.files) ? v.files.filter((f): f is string => typeof f === 'string') : [],
    branch: typeof v.branch === 'string' ? v.branch : undefined,
    base: typeof v.base === 'string' ? v.base : undefined,
  };
}

/** The spec gate's payload, only for a review request in the spec phase. */
export function specPayload(p: PendingRequest): SpecReviewPayload | undefined {
  const v = p.spec.payload as Partial<SpecReviewPayload> | undefined;
  return v && v.phase === 'spec' && p.spec.kind === 'review' ? (v as SpecReviewPayload) : undefined;
}

/** A budget pause's payload (gate: budget). */
export function budgetPayload(p: PendingRequest): BudgetPausePayload | undefined {
  const v = p.spec.payload as Partial<BudgetPausePayload> | undefined;
  return v && v.gate === 'budget' ? (v as BudgetPausePayload) : undefined;
}

export const criterionLine = (c: Criterion): string => `Given ${c.given}, when ${c.when}, then ${c.then}`;

/** The edited criteria to send, or undefined when the text is the original (then Accept sends no data). */
export function editedCriteria(original: readonly Criterion[], text: string): string[] | undefined {
  const orig = original.map(criterionLine).join('\n').trim();
  if (text.trim() === orig) return undefined;
  return parseAcceptance(text);
}

// --- diff ---

export type DiffLineKind = 'add' | 'del' | 'hunk' | 'meta' | 'ctx';

export function classifyDiffLine(l: string): DiffLineKind {
  if (l.startsWith('+++') || l.startsWith('---') || l.startsWith('diff ') || l.startsWith('index ') || l.startsWith('new file') || l.startsWith('deleted file')) return 'meta';
  if (l.startsWith('@@')) return 'hunk';
  if (l.startsWith('+')) return 'add';
  if (l.startsWith('-')) return 'del';
  return 'ctx';
}

export const DIFF_LINE_CAP = 3000;

/** Split a patch into lines (a trailing newline is not an extra empty line). */
export const diffLines = (text: string): string[] => text.replace(/\n$/, '').split('\n');

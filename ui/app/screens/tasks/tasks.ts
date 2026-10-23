// Pure helpers for the task screens: the board's columns, cards, checks bar, filters and timeline, evidence results and labels, the shape of a
// pending request's payload, diff line classes, and the text of acceptance criteria. No DOM and no client here.

import type { BudgetPausePayload, Criterion, SpecReviewPayload } from '../../api/extTypes.ts';
import { TASK_LABEL_LEN, TASK_LABELS_MAX, type Evidence, type PendingRequest, type SummaryCheck, type Task, type TaskSummary } from '../../api/types.ts';

// --- the board (graphite, decision P97): five columns of task.summaries ---

/** The board's columns, in order. A failed or cancelled task is in none of them: it is listed under Stopped, below Merged. */
export type BoardColumn = 'spec' | 'building' | 'checks' | 'review' | 'merged';

export const BOARD_COLUMNS: readonly { name: BoardColumn; label: string }[] = [
  { name: 'spec', label: 'Spec' },
  { name: 'building', label: 'Building' },
  { name: 'checks', label: 'Checks' },
  { name: 'review', label: 'Review' },
  { name: 'merged', label: 'Merged' },
];

const STOPPED = new Set(['FAILED', 'CANCELLED']);

/** The task ended without its change: failed or cancelled. */
export const isStoppedTask = (status: string): boolean => STOPPED.has(status);

/**
 * The column of a task, from the step the core read for it (tasks.StepIndex): spec is Spec (also a task not started yet, and
 * one whose spec waits for the person), code is Building, checks is Checks, review and you are Review, a completed task is
 * Merged. The core puts a task whose checks are running at code (its status is still In progress): a pass under way moves it to
 * Checks. A failed or cancelled task is 'stopped'.
 */
export function columnOf(s: Pick<TaskSummary, 'status' | 'step' | 'checks'>): BoardColumn | 'stopped' {
  if (isStoppedTask(s.status)) return 'stopped';
  if (s.status === 'COMPLETED' || s.step === 'done') return 'merged';
  switch (s.step) {
    case 'spec':
      return 'spec';
    case 'checks':
      return 'checks';
    case 'review':
    case 'you':
      return 'review';
    default:
      return s.checks.some((c) => c.status === 'running') ? 'checks' : 'building';
  }
}

/** The board: each column with its tasks, and the tasks that stopped (failed or cancelled), so none is dropped silently. */
export interface BoardLayout<T> {
  columns: { column: BoardColumn; tasks: T[] }[];
  stopped: T[];
}

type Sortable = Pick<TaskSummary, 'status' | 'step' | 'checks' | 'seq' | 'updatedAt' | 'completedAt' | 'startedAt' | 'pendingCount'>;

/**
 * The five columns, always all five. In every column what waits on the person comes first. Spec then has the tasks under way
 * (started) newest first, then the ones not started in the order the crew takes them (lowest number first); Merged and Stopped
 * are newest finish first; the others newest update first.
 */
export function boardOf<T extends Sortable>(list: readonly T[], waiting: (t: T) => boolean = (t) => t.pendingCount > 0): BoardLayout<T> {
  const by = new Map<BoardColumn | 'stopped', T[]>([...BOARD_COLUMNS.map((c) => [c.name, []] as [BoardColumn, T[]]), ['stopped', []]]);
  for (const t of list) by.get(columnOf(t))!.push(t);
  const finished = (a: T, b: T) => toMs(b.completedAt) - toMs(a.completedAt) || b.updatedAt - a.updatedAt || b.seq - a.seq;
  const asked = (a: T, b: T) => Number(waiting(b)) - Number(waiting(a));
  const order = (c: BoardColumn | 'stopped') => (a: T, b: T) => {
    if (c === 'merged' || c === 'stopped') return finished(a, b);
    const w = asked(a, b);
    if (w) return w;
    if (c === 'spec') {
      const started = Number(!!b.startedAt) - Number(!!a.startedAt);
      if (started) return started;
      if (!a.startedAt) return a.seq - b.seq;
    }
    return b.updatedAt - a.updatedAt || b.seq - a.seq;
  };
  for (const [c, l] of by) l.sort(order(c));
  return { columns: BOARD_COLUMNS.map((c) => ({ column: c.name, tasks: by.get(c.name)! })), stopped: by.get('stopped')! };
}

/** A task's key: the project's key and the task's number ("GOC-41"); "#41" when the project has no key. */
export const taskKey = (s: Pick<TaskSummary, 'projectKey' | 'seq'>): string => (s.projectKey ? `${s.projectKey}-${s.seq}` : `#${s.seq}`);

/** "1st", "2nd", "3rd", "4th", "11th", "21st". */
export function ordinal(n: number): string {
  const tens = n % 100;
  const suffix = tens >= 11 && tens <= 13 ? 'th' : ({ 1: 'st', 2: 'nd', 3: 'rd' } as Record<number, string>)[n % 10] ?? 'th';
  return `${n}${suffix}`;
}

/** A running time as a clock: "0:42", "9:47", "1:02:05". */
export function clock(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, '0');
  return h ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`;
}

/** When a task finished, as the Merged column says it: "Today", "Yesterday", a weekday within the week ("Mon"), else the date. */
export function finishedWhen(at: number | undefined, now = Date.now()): string {
  const ms = toMs(at);
  if (!ms) return '';
  const day = (x: number) => {
    const d = new Date(x);
    return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  };
  const days = Math.round((day(now) - day(ms)) / 86_400_000);
  if (days <= 0) return 'Today';
  if (days === 1) return 'Yesterday';
  if (days < 7) return new Date(ms).toLocaleDateString('en-US', { weekday: 'short' });
  return new Date(ms).toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
}

/** The tag at the right of a card's top line. */
export interface CardTag {
  kind: 'waiting' | 'merge' | 'elapsed' | 'next' | 'backlog';
  text: string;
}

/**
 * What the card's top line says at its right: "Ready to merge" when a merge waits for the person, "Waiting on you" for any
 * other request (or an item in Needs you, `asked`), the time it has run while it runs, "Up next" or "Backlog" before it
 * starts. Nothing on a finished task (Merged says when it finished instead).
 */
export function cardTag(s: Pick<TaskSummary, 'status' | 'pending' | 'startedAt'>, asked = false, now = Date.now()): CardTag | undefined {
  if (s.pending?.phase === 'merge') return { kind: 'merge', text: 'Ready to merge' };
  if (s.pending || asked) return { kind: 'waiting', text: 'Waiting on you' };
  if (isTerminal(s.status)) return undefined;
  const start = toMs(s.startedAt);
  if (start) return { kind: 'elapsed', text: clock(now - start) };
  if (s.status === 'BACKLOG') return { kind: 'backlog', text: 'Backlog' };
  return { kind: 'next', text: 'Up next' };
}

/** The spec gate's line on a card: how many criteria the planner wrote and what the person can do. */
export function specGateText(criteria: number): string {
  return `Spec gate: ${criteria} acceptance ${criteria === 1 ? 'criterion' : 'criteria'} written. Accept, edit or send back with feedback.`;
}

/** What the agent is doing, with the try it is on after the first: "Running go test ./... · 3rd try". Empty when unknown. */
export function doingLine(s: Pick<TaskSummary, 'doing' | 'attempt'>): string {
  const tryText = s.attempt > 1 ? `${ordinal(s.attempt)} try` : '';
  return [s.doing?.trim() ?? '', tryText].filter(Boolean).join(' · ');
}

/** How far along the task is, 0 to 100: stepIndex of stepCount. */
export const progressOf = (s: Pick<TaskSummary, 'stepIndex' | 'stepCount'>): number => (s.stepCount > 0 ? Math.round((Math.min(Math.max(s.stepIndex, 0), s.stepCount) / s.stepCount) * 100) : 0);

/** "+48 −6", never broken between the two numbers (a no-break space). */
export const diffText = (d: { insertions: number; deletions: number }): string => `+${d.insertions}\u00a0−${d.deletions}`;

/** The reviewer's line on a card: "Rex approved · 2 files · +31 −2". Empty without a verdict or a diff. */
export function verdictLine(s: Pick<TaskSummary, 'verdict' | 'diff'>, reviewer: string): string {
  const who = reviewer || 'The reviewer';
  const verdict = s.verdict === 'SHIP' ? `${who} approved` : s.verdict === 'REVISE' ? `${who} asked for changes` : '';
  const files = s.diff ? `${s.diff.files} ${s.diff.files === 1 ? 'file' : 'files'}` : '';
  return [verdict, files, s.diff ? diffText(s.diff) : ''].filter(Boolean).join(' · ');
}

// the checks bar: seven segments, each one or more of the core's check kinds

export type CheckSegmentName = 'tests' | 'lint' | 'types' | 'secrets' | 'deps' | 'ci' | 'preview';
export type SegmentState = 'passed' | 'failed' | 'running' | 'inconclusive' | 'not_run';

/** The segments in order, and the check kinds (pipeline.CheckKinds) each one shows. */
export const CHECK_SEGMENTS: readonly { name: CheckSegmentName; kinds: readonly string[] }[] = [
  { name: 'tests', kinds: ['build', 'test', 'e2e'] },
  { name: 'lint', kinds: ['lint'] },
  { name: 'types', kinds: ['typecheck'] },
  { name: 'secrets', kinds: ['secrets'] },
  { name: 'deps', kinds: ['deps'] },
  { name: 'ci', kinds: ['ci', 'merge-conflicts'] },
  { name: 'preview', kinds: ['preview'] },
];

const RANK: Record<SegmentState, number> = { failed: 4, running: 3, inconclusive: 2, not_run: 1, passed: 0 };

const stateOf = (status: string): SegmentState => (status === 'passed' || status === 'failed' || status === 'running' || status === 'inconclusive' ? status : 'not_run');

export interface CheckSegment {
  name: CheckSegmentName;
  state: SegmentState;
  /** The kinds the segment stands for, with their state: "test: passed, e2e: not run". */
  title: string;
}

/**
 * The segments of the checks the task's project runs (a segment none of whose kinds ran is left out), in CHECK_SEGMENTS
 * order. A segment of several kinds shows the worst of them: failed, then running, then inconclusive, then not run.
 */
export function checkSegments(checks: readonly SummaryCheck[]): CheckSegment[] {
  const out: CheckSegment[] = [];
  for (const seg of CHECK_SEGMENTS) {
    const mine = checks.filter((c) => seg.kinds.includes(c.kind));
    if (!mine.length) continue;
    const state = mine.map((c) => stateOf(c.status)).reduce((a, b) => (RANK[b] > RANK[a] ? b : a));
    out.push({ name: seg.name, state, title: mine.map((c) => `${c.kind}: ${STATE_WORDS[stateOf(c.status)]}`).join(', ') });
  }
  return out;
}

const STATE_WORDS: Record<SegmentState, string> = { passed: 'passed', failed: 'failed', running: 'running', inconclusive: 'inconclusive', not_run: 'not run' };

/** The bar's accessible name: "Checks: 5 passed, 1 running, 1 failed". */
export function checksLabel(segs: readonly CheckSegment[]): string {
  const order: SegmentState[] = ['passed', 'running', 'failed', 'inconclusive', 'not_run'];
  const parts = order.map((st) => [st, segs.filter((x) => x.state === st).length] as const).filter(([, n]) => n > 0).map(([st, n]) => `${n} ${STATE_WORDS[st]}`);
  return `Checks: ${parts.join(', ') || 'none'}`;
}

/** The first failing check's line: "preview: the page did not start on port 5173" (what failed when it gave no reason). */
export function failingLine(checks: readonly SummaryCheck[]): string {
  const f = checks.find((c) => c.status === 'failed');
  if (!f) return '';
  const why = f.reason?.trim() || (f.failing?.length ? `${f.failing.slice(0, 3).join(', ')} failed` : 'failed');
  const text = `${f.kind}: ${why}`;
  return text.length > 160 ? `${text.slice(0, 159)}…` : text;
}

// filters

export interface BoardFilter {
  /** An agent's id: the tasks it works on or reviews. */
  agent?: string;
  label?: string;
  /** Only the tasks with a request waiting for the person. */
  needsMe?: boolean;
}

/** The task waits for the person: a request of its run, or an item in Needs you (`asked`: the ids of those tasks). */
export const needsMe = (s: Pick<TaskSummary, 'taskId' | 'pendingCount'>, asked?: ReadonlySet<string>): boolean => s.pendingCount > 0 || !!asked?.has(s.taskId);

export function matchesFilter(s: Pick<TaskSummary, 'taskId' | 'pendingCount' | 'assignee' | 'reviewer' | 'labels'>, f: BoardFilter, asked?: ReadonlySet<string>): boolean {
  if (f.agent && s.assignee !== f.agent && s.reviewer !== f.agent) return false;
  if (f.label && !s.labels.includes(f.label)) return false;
  if (f.needsMe && !needsMe(s, asked)) return false;
  return true;
}

/** Every label the tasks carry, sorted. */
export const labelsOf = (list: readonly Pick<TaskSummary, 'labels'>[]): string[] => [...new Set(list.flatMap((s) => s.labels))].sort((a, b) => a.localeCompare(b));

/**
 * The labels a person typed, comma-separated, as the core keeps them (trimmed, lower-cased, deduplicated), or what is wrong
 * with them: more than TASK_LABELS_MAX, or one longer than TASK_LABEL_LEN.
 */
export function parseLabels(text: string): { labels: string[]; error?: string } {
  const labels = [...new Set(text.split(',').map((x) => x.trim().toLowerCase()).filter(Boolean))];
  const long = labels.find((l) => [...l].length > TASK_LABEL_LEN);
  if (long) return { labels, error: `"${long.slice(0, 30)}" is longer than ${TASK_LABEL_LEN} characters.` };
  if (labels.length > TASK_LABELS_MAX) return { labels, error: `A task takes up to ${TASK_LABELS_MAX} labels; this is ${labels.length}.` };
  return { labels };
}

/** The merge request a ready-to-merge card answers: run.respond(runId, requestId, 'approved'), as the review panel's Approve. */
export function mergeRequestOf(s: Pick<TaskSummary, 'pending'>): { runId: string; requestId: string } | undefined {
  const p = s.pending;
  return p && p.phase === 'merge' && p.runId && p.requestId ? { runId: p.runId, requestId: p.requestId } : undefined;
}

/** The review panel's answer to a review request that approves it (and merges, for a merge request). */
export const APPROVE = 'approved';

// the timeline

export type TimelineRange = 'today' | 'week';

export interface TimelineBlock<T> {
  task: T;
  /** Where the block starts and how wide it is, in percent of the range. */
  left: number;
  width: number;
  state: 'running' | 'waiting' | 'done' | 'stopped';
  /** The row of the lane it is drawn on: blocks that overlap in time go on separate rows. */
  row: number;
}

export interface TimelineLane<T> {
  /** The agent's id, '' for the tasks nobody works on. */
  agent: string;
  blocks: TimelineBlock<T>[];
  rows: number;
}

/** The start and the end of a range: today from midnight, or the last seven days, up to now. */
export function rangeOf(range: TimelineRange, now = Date.now()): { from: number; to: number } {
  if (range === 'week') return { from: now - 7 * 86_400_000, to: now };
  const d = new Date(now);
  const from = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  return { from, to: Math.max(now, from + 3_600_000) };
}

/**
 * One lane per agent (the worker), each task a block from its start to its finish (or now while it runs), clipped to the
 * range; a task that has not started, or ended before the range, is left out. Lanes in the order of their first block.
 */
export function timelineOf<T extends Pick<TaskSummary, 'assignee' | 'startedAt' | 'completedAt' | 'status' | 'pendingCount' | 'updatedAt'>>(
  list: readonly T[],
  range: { from: number; to: number },
  now = Date.now(),
): TimelineLane<T>[] {
  const span = Math.max(1, range.to - range.from);
  const lanes = new Map<string, TimelineBlock<T>[]>();
  const firsts = new Map<string, number>();
  for (const s of list) {
    const start = toMs(s.startedAt);
    if (!start) continue;
    const end = isTerminal(s.status) ? toMs(s.completedAt) || s.updatedAt : now;
    if (end < range.from || start > range.to) continue;
    const a = Math.max(start, range.from);
    const b = Math.min(Math.max(end, a), range.to);
    const left = ((a - range.from) / span) * 100;
    const width = Math.max(((b - a) / span) * 100, 0.6);
    const state = s.status === 'COMPLETED' ? 'done' : isStoppedTask(s.status) ? 'stopped' : s.pendingCount > 0 ? 'waiting' : 'running';
    const key = s.assignee ?? '';
    if (!lanes.has(key)) lanes.set(key, []);
    lanes.get(key)!.push({ task: s, left, width: Math.min(width, 100 - left), state, row: 0 });
    firsts.set(key, Math.min(firsts.get(key) ?? Infinity, a));
  }
  return [...lanes.entries()]
    .sort((x, y) => (firsts.get(x[0]) ?? 0) - (firsts.get(y[0]) ?? 0))
    .map(([agent, blocks]) => {
      blocks.sort((p, q) => p.left - q.left);
      const ends: number[] = [];
      for (const b of blocks) {
        let row = ends.findIndex((e) => e <= b.left);
        if (row < 0) row = ends.push(0) - 1;
        ends[row] = b.left + b.width;
        b.row = row;
      }
      return { agent, blocks, rows: Math.max(1, ends.length) };
    });
}

/** The marks under the timeline: every two hours today ("10:00"), every day of the week ("Mon"), in percent of the range. */
export function timelineTicks(range: TimelineRange, r: { from: number; to: number }): { at: number; label: string }[] {
  const span = Math.max(1, r.to - r.from);
  const out: { at: number; label: string }[] = [];
  if (range === 'today') {
    for (let h = 0; h <= 24; h += 2) {
      const at = r.from + h * 3_600_000;
      if (at > r.to) break;
      out.push({ at: ((at - r.from) / span) * 100, label: `${String(h).padStart(2, '0')}:00` });
    }
    return out;
  }
  const d = new Date(r.from);
  let day = new Date(d.getFullYear(), d.getMonth(), d.getDate() + 1).getTime();
  for (; day <= r.to; day = new Date(new Date(day).getFullYear(), new Date(day).getMonth(), new Date(day).getDate() + 1).getTime()) {
    out.push({ at: ((day - r.from) / span) * 100, label: new Date(day).toLocaleDateString('en-US', { weekday: 'short' }) });
  }
  return out;
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

/** A core timestamp in milliseconds: the core writes seconds in some fields and milliseconds in others. */
export function toMs(n: number | undefined): number {
  if (!n) return 0;
  return n < 1e11 ? n * 1000 : n;
}

/** How long a task has run: from its start to its completion, or to now while it runs. "5 m 12 s", "1 h 03 m"; empty before it starts. */
export function elapsed(t: Pick<Task, 'started' | 'completed'>, now = Date.now()): string {
  const start = toMs(t.started);
  if (!start) return '';
  const end = toMs(t.completed) || now;
  const s = Math.max(0, Math.round((end - start) / 1000));
  if (s < 60) return `${s} s`;
  if (s < 3600) return `${Math.floor(s / 60)} m ${String(s % 60).padStart(2, '0')} s`;
  return `${Math.floor(s / 3600)} h ${String(Math.floor((s % 3600) / 60)).padStart(2, '0')} m`;
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

/** The newest diff a review request carries: its evidence id (to read the patch) and its size. */
export interface PayloadDiff {
  id: number;
  files: number;
  insertions: number;
  deletions: number;
}

/** The size of the newest diff evidence in `evidence` (a task's evidence, or the evidence a review request carries); malformed counts are 0. */
export function newestDiffStat(evidence: unknown): PayloadDiff | undefined {
  let diff: PayloadDiff | undefined;
  if (!Array.isArray(evidence)) return undefined;
  for (const e of evidence as { id?: unknown; kind?: unknown; data?: unknown }[]) {
    if (!e || e.kind !== 'diff' || typeof e.id !== 'number' || (diff && e.id < diff.id)) continue;
    const st = (e.data ?? {}) as { Files?: unknown; Insertions?: unknown; Deletions?: unknown };
    const n = (x: unknown) => (typeof x === 'number' && Number.isFinite(x) && x >= 0 ? x : 0);
    diff = { id: e.id, files: n(st.Files), insertions: n(st.Insertions), deletions: n(st.Deletions) };
  }
  return diff;
}

/** The files, branches and newest diff a review request names (anything malformed is left out). */
export function payloadOf(p: PendingRequest): { files: string[]; branch?: string; base?: string; diff?: PayloadDiff } {
  const v = (p.spec.payload ?? {}) as { files?: unknown; branch?: unknown; base?: unknown; evidence?: unknown };
  return {
    files: Array.isArray(v.files) ? v.files.filter((f): f is string => typeof f === 'string') : [],
    branch: typeof v.branch === 'string' ? v.branch : undefined,
    base: typeof v.base === 'string' ? v.base : undefined,
    diff: newestDiffStat(v.evidence),
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

/**
 * The id of the newest evidence of kind `diff`: the patch a task's card is about (audit 2026-10-16, L36). A patch recorded later
 * (a new iteration) has a higher id. Undefined when no diff was recorded.
 */
export function newestDiffId(evidence: readonly Pick<Evidence, 'id' | 'kind'>[]): number | undefined {
  let id: number | undefined;
  for (const e of evidence) if (e.kind === 'diff' && (id === undefined || e.id > id)) id = e.id;
  return id;
}

/** Split a patch into lines (a trailing newline is not an extra empty line). */
export const diffLines = (text: string): string[] => text.replace(/\n$/, '').split('\n');

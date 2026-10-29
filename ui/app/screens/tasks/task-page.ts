// What the task page (the graphite mockup "Task", decision P97) says about a task in words: the status pill ("Building · run 3",
// "Paused by kyle"), a criterion's state and its own status with what showed it (P125), the check kinds of the Checks grid,
// "This run"'s time and test runs, and the labels a person types. Pure: no DOM and no client.

import type { CriterionState, CriterionStatus, Evidence, SummaryCheck, Task, TaskPause } from '../../api/types.ts';
import { TASK_LABEL_LEN, TASK_LABELS_MAX } from '../../api/types.ts';
import { statusLabel } from '../../lib/format.ts';
import { toMs } from './tasks.ts';

/** The person's name in a "human:<name>" identity (what taskPause records). */
export const personName = (id: string): string => id.replace(/^human:/, '') || id;

/**
 * The status in a word or two: "Paused by kyle" while a person has the run paused, "Running checks" while a check runs,
 * "Building" while the agent works, else the status's own label ("Waiting for you", "Completed", ...).
 */
export function statusWords(task: Pick<Task, 'status'>, paused?: TaskPause, checks: readonly Pick<SummaryCheck, 'status'>[] = []): string {
  if (paused) return `Paused by ${personName(paused.by)}`;
  if (task.status === 'IN_PROGRESS') return checks.some((c) => c.status === 'running') ? 'Running checks' : 'Building';
  return statusLabel(task.status);
}

/**
 * Which run the task is on, from 1, once it started (the core counts iterations from 0: task.summaries' attempt is iteration + 1);
 * 0 before it started.
 */
export const runNumber = (task: Pick<Task, 'iteration' | 'started'>): number => (task.started || task.iteration > 0 ? task.iteration + 1 : 0);

/** The pill: the status words and, once the task has started, which run it is on ("Building · run 3"). */
export function statusPill(task: Pick<Task, 'status' | 'iteration' | 'started'>, paused?: TaskPause, checks: readonly Pick<SummaryCheck, 'status'>[] = []): string {
  const words = statusWords(task, paused, checks);
  const n = runNumber(task);
  return n ? `${words} · run ${n}` : words;
}

/** The tone of the status dot: accent while it works, warning while it waits on you or is paused, success done, danger failed. */
export function statusDot(task: Pick<Task, 'status'>, paused?: TaskPause): 'accent' | 'warn' | 'ok' | 'err' | 'muted' {
  if (paused) return 'warn';
  switch (task.status) {
    case 'IN_PROGRESS':
    case 'REVIEW':
    case 'CLAIMED':
      return 'accent';
    case 'WAITING_HUMAN':
    case 'VERIFICATION':
    case 'BLOCKED':
      return 'warn';
    case 'COMPLETED':
      return 'ok';
    case 'FAILED':
    case 'CANCELLED':
      return 'err';
    default:
      return 'muted';
  }
}

export type CriterionMark = 'passed' | 'failing' | 'unknown';

/** Each criterion with its state by the newest test check (TaskDetail.acceptanceState), unknown when the core says nothing. */
export function criteriaWithState(acceptance: readonly string[] | undefined, states: readonly CriterionState[] | undefined): { text: string; state: CriterionMark; id: string }[] {
  return (acceptance ?? []).map((text, index) => {
    const s = states?.find((x) => x.index === index);
    const state: CriterionMark = s?.state === 'passed' ? 'passed' : s?.state === 'failing' ? 'failing' : 'unknown';
    return { text, state, id: s?.id ?? '' };
  });
}

export const CRITERION_WORDS: Record<CriterionMark, string> = { passed: 'passes', failing: 'fails', unknown: 'not checked yet' };

export type CriterionStatusMark = 'open' | 'met' | 'not_met' | 'waived';

export const STATUS_WORDS: Record<CriterionStatusMark, string> = { open: 'open', met: 'met', not_met: 'not met', waived: 'waived' };

/** One criterion as the task page shows it (P125): its status, the line that says what showed it, and an agent's open claim. */
export interface CriterionRow {
  index: number;
  id: string;
  text: string;
  status: CriterionStatusMark;
  /** What showed the status, in a sentence; '' when the criterion is open and nothing was said about it. */
  why: string;
  /** An agent's claim that it is met, in a sentence ("Dana says it is met: ... Not confirmed."); '' when there is none. */
  claim: string;
  /** A person gave their word on it (met, not met or waived): Reopen takes the word back. */
  personal: boolean;
}

const statusMark = (s: string | undefined): CriterionStatusMark => (s === 'met' || s === 'not_met' || s === 'waived' ? s : 'open');

/**
 * The criteria with their own status (TaskDetail.criteria). An older core sends only acceptanceState: a passing state reads as
 * met and a failing one as not met, both by the test check. nameOf turns an agent id into its name.
 */
export function criteriaRows(
  acceptance: readonly string[] | undefined,
  criteria: readonly CriterionStatus[] | undefined,
  states: readonly CriterionState[] | undefined,
  nameOf: (id: string) => string = (id) => id,
): CriterionRow[] {
  return (acceptance ?? []).map((text, index) => {
    const c = criteria?.find((x) => x.index === index);
    if (!c) {
      const st = criteriaWithState([text], states?.filter((x) => x.index === index).map((x) => ({ ...x, index: 0 })))[0]!;
      const status: CriterionStatusMark = st.state === 'passed' ? 'met' : st.state === 'failing' ? 'not_met' : 'open';
      return { index, id: st.id, text, status, why: status === 'open' ? '' : `By the test check: the tests ${st.state === 'passed' ? 'pass' : 'fail'}.`, claim: '', personal: false };
    }
    const status = statusMark(c.status);
    return { index, id: c.id, text, status, why: criterionWhy(c, status, nameOf), claim: c.claim ? claimLine(c.claim, nameOf) : '', personal: c.source === 'person' };
  });
}

const sentence = (s: string | undefined): string => {
  const v = (s ?? '').trim();
  return !v ? '' : /[.!?]$/.test(v) ? v : `${v}.`;
};

/** What showed a criterion's status, as the task page says it. */
export function criterionWhy(c: CriterionStatus, status: CriterionStatusMark, nameOf: (id: string) => string): string {
  const ev = sentence(c.evidence);
  switch (c.source) {
    case 'check':
      return `By the ${c.by || 'test'} check: ${ev || (status === 'met' ? 'it passes.' : 'it fails.')}`;
    case 'review':
      return `${nameOf(c.by ?? '')}'s review: ${ev}`.trim();
    case 'person': {
      const who = personName(c.by ?? '');
      const verb = status === 'waived' ? 'Waived by' : status === 'met' ? 'Confirmed by' : 'Marked not met by';
      return `${verb} ${who}${ev ? `: ${ev}` : '.'}`;
    }
  }
  return '';
}

const claimLine = (cl: NonNullable<CriterionStatus['claim']>, nameOf: (id: string) => string): string =>
  `${nameOf(cl.by)} says it is met${cl.evidence ? `: ${sentence(cl.evidence)}` : '.'} Not confirmed: a check or a person confirms it.`;

/** The name a check kind has in the Checks grid. */
const CHECK_NAMES: Record<string, string> = { test: 'tests', typecheck: 'types', 'merge-conflicts': 'conflicts' };
export const checkName = (kind: string): string => CHECK_NAMES[kind] ?? kind;

export type CheckMark = 'passed' | 'failed' | 'running' | 'inconclusive' | 'not_run';

/** A check's state as one of the marks the grid draws (anything the core adds later reads as not run). */
export const checkMark = (status: string): CheckMark =>
  status === 'passed' || status === 'failed' || status === 'running' || status === 'inconclusive' ? status : 'not_run';

export const CHECK_WORDS: Record<CheckMark, string> = { passed: 'passed', failed: 'failed', running: 'running', inconclusive: 'no verdict', not_run: 'not run yet' };

/** The evidence kinds that are a test run of the task's checks. */
const TEST_KINDS = new Set(['test', 'red_test', 'green_test', 'e2e']);

/**
 * How many times the task's tests ran in this run: its test evidence of the newest attempt the evidence records (the attempt the
 * core's Checks read too, pipeline.LatestChecks).
 */
export function testRuns(evidence: readonly Pick<Evidence, 'kind' | 'attempt'>[]): number {
  const attempt = evidence.reduce((m, e) => Math.max(m, e.attempt), 0);
  return evidence.filter((e) => TEST_KINDS.has(e.kind) && e.attempt === attempt).length;
}

/** How long the task has run: from its start to its end, or to now while it is open. Undefined before it started. */
export function runMs(task: Pick<Task, 'started' | 'completed'>, now = Date.now()): number | undefined {
  const start = toMs(task.started);
  if (!start) return undefined;
  return Math.max(0, (toMs(task.completed) || now) - start);
}

/**
 * A label a person typed, checked as the core checks it (trimmed and lower-cased; at most TASK_LABELS_MAX labels of up to
 * TASK_LABEL_LEN characters): the labels to send, or the reason nothing is sent.
 */
export function addLabel(labels: readonly string[], typed: string): { labels: string[] } | { error: string } {
  const l = typed.trim().toLowerCase();
  if (!l) return { error: 'Type a label first.' };
  if (l.length > TASK_LABEL_LEN) return { error: `A label is at most ${TASK_LABEL_LEN} characters.` };
  if (labels.includes(l)) return { error: `The task already has the label ${l}.` };
  if (labels.length >= TASK_LABELS_MAX) return { error: `A task has at most ${TASK_LABELS_MAX} labels.` };
  return { labels: [...labels, l] };
}

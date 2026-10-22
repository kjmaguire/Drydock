// What the task page (the graphite mockup "Task", decision P97) says about a task in words: the status pill ("Building · run 3",
// "Paused by kyle"), a criterion's state, the check kinds of the Checks grid, "This run"'s time and test runs, and the labels a
// person types. Pure: no DOM and no client.

import type { CriterionState, Evidence, SummaryCheck, Task, TaskPause } from '../../api/types.ts';
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

// The step bar of a task's page (Crew HQ, decision P64): Spec → Code → Checks → Review → You, read off the task's status,
// its evidence, the reviewer verdicts and what is pending. Pure: no DOM and no client here.

import type { Evidence, PendingRequest, ReviewRecord, Task } from '../../api/types.ts';
import { evidenceResult, specPayload } from './tasks.ts';

export type StepName = 'spec' | 'code' | 'checks' | 'review' | 'you';
export type StepState = 'done' | 'current' | 'todo' | 'failed';

export interface Step {
  name: StepName;
  label: string;
  state: StepState;
  /** One or two words under the label: who or what, never a sentence. */
  note: string;
}

export const STEP_NAMES: readonly StepName[] = ['spec', 'code', 'checks', 'review', 'you'];
const LABEL: Record<StepName, string> = { spec: 'Spec', code: 'Code', checks: 'Checks', review: 'Review', you: 'You' };

/** Evidence kinds that are checks: the tests and the end-to-end or preview runs the run recorded. */
const CHECK_KINDS = new Set(['test', 'red_test', 'green_test', 'e2e', 'preview']);

/** How far the status alone says the task got: the index of the current step, 5 when it is closed. */
function reach(status: string): number {
  switch (status) {
    case 'BACKLOG':
    case 'READY':
    case 'CLAIMED':
      return 0;
    case 'IN_PROGRESS':
    case 'BLOCKED':
      return 1;
    case 'REVIEW':
      return 3;
    case 'VERIFICATION':
    case 'WAITING_HUMAN':
      return 4;
    case 'COMPLETED':
    case 'FAILED':
    case 'CANCELLED':
      return 5;
    default:
      return 1;
  }
}

const SPEC_NOTE: Record<string, string> = { BACKLOG: 'not started', READY: 'ready', CLAIMED: 'claimed' };

/**
 * The index in STEP_NAMES of the step the task is at, 5 when it is completed. A pending spec review keeps it at Spec; a failed or
 * cancelled task is at the first step its evidence does not cover; any other task is where its status puts it. The core reads
 * the step of task.summaries by the same rule (tasks.StepIndex): both pass the cases in internal/tasks/testdata/steps.json.
 */
export function stepIndex(
  task: Pick<Task, 'status' | 'started'>,
  evidence: readonly Pick<Evidence, 'kind' | 'passed'>[],
  reviews: readonly unknown[],
  pending: readonly PendingRequest[] = [],
): number {
  if (pending.some((p) => !!specPayload(p))) return 0;
  if (task.status === 'FAILED' || task.status === 'CANCELLED') {
    const specAccepted = evidence.some((e) => e.kind === 'spec_accept' && e.passed !== false);
    const coded = evidence.some((e) => e.kind === 'diff' || e.kind === 'commit' || e.kind === 'green_test');
    const checked = evidence.some((e) => CHECK_KINDS.has(e.kind));
    return coded ? (checked ? (reviews.length ? 4 : 3) : 2) : specAccepted || task.started ? 1 : 0;
  }
  return reach(task.status);
}

/**
 * The five steps with their state. The current step is the one the status puts the task at (stepIndex); the steps before it
 * are done (their notes come from the evidence: the criteria count, the attempt, the checks passed, the verdict), the ones
 * after it are to do. A pending spec review keeps the task at Spec with You lit too. A failed or cancelled task fails at the
 * first step the evidence does not cover; a completed one has every step done.
 */
export function stepsFor(
  task: Pick<Task, 'status' | 'iteration' | 'acceptance' | 'acceptedBy' | 'started'>,
  evidence: readonly Pick<Evidence, 'kind' | 'passed' | 'attempt'>[],
  reviews: readonly Pick<ReviewRecord, 'verdict' | 'attempt' | 'created'>[],
  pending: readonly PendingRequest[] = [],
): Step[] {
  const status = task.status;
  const specGate = pending.some((p) => !!specPayload(p));
  const specAccepted = evidence.some((e) => e.kind === 'spec_accept' && e.passed !== false);
  const checks = evidence.filter((e) => CHECK_KINDS.has(e.kind));
  const latestAttempt = checks.reduce((m, e) => Math.max(m, e.attempt), 0);
  const latest = checks.filter((e) => e.attempt === latestAttempt).map(evidenceResult);
  const passed = latest.filter((x) => x === 'pass').length;
  const failedChecks = latest.filter((x) => x === 'fail').length;
  const last = reviews.reduce<(typeof reviews)[number] | undefined>((m, r) => (!m || r.attempt > m.attempt || (r.attempt === m.attempt && r.created >= m.created) ? r : m), undefined);
  const closed = status === 'FAILED' || status === 'CANCELLED';

  // the current step's index
  const p = stepIndex(task, evidence, reviews, pending);

  const state = (i: number): StepState => (i < p ? 'done' : i === p ? 'current' : 'todo');
  const steps: Step[] = STEP_NAMES.map((name, i) => ({ name, label: LABEL[name], state: state(i), note: '' }));
  const [spec, code, chk, review, you] = steps as [Step, Step, Step, Step, Step];

  // Spec
  if (specGate) spec.note = 'your call';
  else if (spec.state === 'done') spec.note = specAccepted ? 'accepted' : task.acceptance?.length ? `${task.acceptance.length} criteria` : 'written';
  else spec.note = SPEC_NOTE[status] ?? '';

  // Code
  if (status === 'BLOCKED' && code.state === 'current') code.note = 'blocked';
  // the core counts iterations from 0 (task.summaries' attempt is iteration + 1); the page's pill says the same run
  else if (code.state !== 'todo' && (task.started || task.iteration > 0)) code.note = `attempt ${task.iteration + 1}`;

  // Checks
  if (checks.length > 0) {
    chk.note = `${passed} of ${latest.length}`;
    if (failedChecks > 0 && p <= 2) chk.state = 'failed';
  } else if (chk.state === 'done') chk.note = 'none recorded';

  // Review
  if (last) review.note = last.verdict === 'SHIP' ? 'ship it' : 'sent back';

  // You
  if (closed) {
    you.state = 'failed';
    you.note = status === 'FAILED' ? 'failed' : 'cancelled';
  } else if (status === 'COMPLETED') you.note = task.acceptedBy ? 'approved' : 'done';
  else if (specGate) {
    you.state = 'current';
    you.note = 'accept';
  } else if (you.state === 'current') {
    const kind = pending[0]?.spec.kind;
    you.note = kind === 'review' ? 'approve' : kind === 'question' ? 'answer' : 'decide';
  }
  return steps;
}

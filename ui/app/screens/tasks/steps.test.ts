import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Evidence, PendingRequest, ReviewRecord } from '../../api/types.ts';
import { STEP_NAMES, stepIndex, stepsFor } from './steps.ts';

// internal/tasks/testdata/steps.json: the cases internal/tasks (TestTheStepRuleMatchesTheTaskPage) passes too, so the board's
// task.summaries and this page put a task at the same step. fileURLToPath, not .pathname: on Windows the pathname is "/D:/a/...".
const STEP_CASES = fileURLToPath(new URL('../../../../internal/tasks/testdata/steps.json', import.meta.url));

interface StepCase {
  name: string;
  task: { status: string; started?: number };
  evidence: { kind: string; passed: boolean | null }[];
  reviews: number;
  specGate: boolean;
  want: { step: string; index: number };
}

const ev = (kind: string, passed: boolean | null = null, attempt = 1): Evidence => ({ id: 1, taskId: 't', attempt, kind, passed, summary: '', created: 0 });
const review = (verdict: string, attempt = 1, created = 0): ReviewRecord => ({ id: 1, taskId: 't', attempt, reviewer: 'a2', verdict, feedback: '', created });
const task = (status: string, over: Partial<{ iteration: number; acceptance: string[]; acceptedBy: string; started: number }> = {}) => ({ status, iteration: 0, ...over });

const states = (steps: ReturnType<typeof stepsFor>) => steps.map((s) => s.state);
const notes = (steps: ReturnType<typeof stepsFor>) => steps.map((s) => s.note);

describe('the step bar', () => {
  it('names the five steps in order', () => {
    expect(stepsFor(task('BACKLOG'), [], []).map((s) => s.label)).toEqual(['Spec', 'Code', 'Checks', 'Review', 'You']);
  });

  it('a task not started sits at Spec with the rest to do', () => {
    expect(states(stepsFor(task('BACKLOG'), [], []))).toEqual(['current', 'todo', 'todo', 'todo', 'todo']);
    expect(stepsFor(task('BACKLOG'), [], [])[0]!.note).toBe('not started');
    expect(stepsFor(task('READY'), [], [])[0]!.note).toBe('ready');
  });

  it('a running task is at Code, the spec done with its criteria count and the attempt noted', () => {
    const s = stepsFor(task('IN_PROGRESS', { iteration: 2, acceptance: ['a', 'b'] }), [], []);
    expect(states(s)).toEqual(['done', 'current', 'todo', 'todo', 'todo']);
    expect(notes(s).slice(0, 2)).toEqual(['2 criteria', 'attempt 3']);
    expect(stepsFor(task('BLOCKED'), [], [])[1]!.note).toBe('blocked');
  });

  it('waiting on the human lights You, with what is asked; checks and the verdict are read off the evidence', () => {
    const pending: PendingRequest[] = [{ id: 'r', nodeId: 'n', step: 3, spec: { kind: 'review', title: 'Review and merge' } }];
    const s = stepsFor(task('WAITING_HUMAN', { iteration: 1 }), [ev('test', true), ev('diff'), ev('e2e', null)], [review('SHIP')], pending);
    expect(states(s)).toEqual(['done', 'done', 'done', 'done', 'current']);
    expect(notes(s)).toEqual(['written', 'attempt 2', '1 of 2', 'ship it', 'approve']);
    expect(stepsFor(task('VERIFICATION'), [], [], [{ id: 'q', nodeId: 'n', step: 3, spec: { kind: 'question', title: '?' } }])[4]!.note).toBe('answer');
    expect(stepsFor(task('VERIFICATION'), [], [])[4]!.note).toBe('decide');
  });

  it('only the latest attempt counts for the checks; a failing check while the worker is at it is a failed step', () => {
    const s = stepsFor(task('IN_PROGRESS', { iteration: 2 }), [ev('test', false, 1), ev('test', false, 2), ev('e2e', true, 2)], [review('REVISE', 1)]);
    expect(states(s)).toEqual(['done', 'current', 'failed', 'todo', 'todo']);
    expect(s[2]!.note).toBe('1 of 2');
    expect(s[3]!.note).toBe('sent back');
    // the same failing check after the task reached review is history
    expect(stepsFor(task('REVIEW'), [ev('test', false)], [])[2]!.state).toBe('done');
  });

  it('in review the Review step is current; the latest verdict wins', () => {
    const s = stepsFor(task('REVIEW'), [ev('diff')], [review('REVISE', 1, 1), review('SHIP', 2, 2)]);
    expect(states(s)).toEqual(['done', 'done', 'done', 'current', 'todo']);
    expect(s[2]!.note).toBe('none recorded');
    expect(s[3]!.note).toBe('ship it');
  });

  it('a pending spec review keeps the task at Spec and lights You for the acceptance', () => {
    const spec: PendingRequest = { id: 'r', nodeId: 'n', step: 2, spec: { kind: 'review', title: 'Accept the spec', payload: { phase: 'spec', taskId: 't', title: 'S', spec: 'x', criteria: [] } } };
    const s = stepsFor(task('WAITING_HUMAN'), [ev('spec')], [], [spec]);
    expect(states(s)).toEqual(['current', 'todo', 'todo', 'todo', 'current']);
    expect(s[0]!.note).toBe('your call');
    expect(s[4]!.note).toBe('accept');
    expect(stepsFor(task('IN_PROGRESS'), [ev('spec_accept', true)], [])[0]!.note).toBe('accepted');
  });

  it('a completed task has every step done; approved by a person says so', () => {
    expect(states(stepsFor(task('COMPLETED'), [], []))).toEqual(['done', 'done', 'done', 'done', 'done']);
    expect(stepsFor(task('COMPLETED', { acceptedBy: 'human:kyle' }), [], [])[4]!.note).toBe('approved');
    expect(stepsFor(task('COMPLETED'), [], [])[4]!.note).toBe('done');
  });

  it('a failed or cancelled task fails at You, with the steps the evidence covers done', () => {
    const cancelled = stepsFor(task('CANCELLED'), [], []);
    expect(states(cancelled)).toEqual(['current', 'todo', 'todo', 'todo', 'failed']);
    expect(cancelled[4]!.note).toBe('cancelled');
    const failed = stepsFor(task('FAILED', { started: 5 }), [ev('diff'), ev('test', false)], []);
    expect(states(failed)).toEqual(['done', 'done', 'done', 'current', 'failed']);
    expect(failed[4]!.note).toBe('failed');
    expect(failed[2]!.note).toBe('0 of 1');
  });

  it('an unknown status reads as work in progress', () => {
    expect(states(stepsFor(task('SOMETHING_NEW'), [], []))).toEqual(['done', 'current', 'todo', 'todo', 'todo']);
  });

  it('is at the step the core puts it at (the shared cases)', () => {
    const cases = (JSON.parse(readFileSync(STEP_CASES, 'utf8')) as { cases: StepCase[] }).cases;
    expect(cases.length >= 10).toBe(true);
    const spec: PendingRequest = { id: 'r', nodeId: 'n', step: 0, spec: { kind: 'review', title: 'Accept the spec: x', payload: { phase: 'spec' } } };
    for (const c of cases) {
      const evidence = c.evidence.map((e) => ev(e.kind, e.passed, 0));
      const reviews = Array.from({ length: c.reviews }, () => review('SHIP', 0));
      const i = stepIndex({ status: c.task.status, started: c.task.started }, evidence, reviews, c.specGate ? [spec] : []);
      expect([c.name, i, STEP_NAMES[i] ?? 'done']).toEqual([c.name, c.want.index, c.want.step]);
      // the step bar lights the same step
      const bar = stepsFor({ ...task(c.task.status), started: c.task.started }, evidence, reviews, c.specGate ? [spec] : []);
      if (i < STEP_NAMES.length) expect([c.name, bar[i]!.state === 'current' || bar[i]!.state === 'failed']).toEqual([c.name, true]);
      else expect([c.name, bar.every((st) => st.state === 'done')]).toEqual([c.name, true]);
    }
  });
});

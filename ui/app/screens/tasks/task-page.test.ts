import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { TASK_LABELS_MAX } from '../../api/types.ts';
import { addLabel, checkMark, checkName, criteriaRows, criteriaWithState, criterionWhy, personName, runMs, runNumber, statusDot, statusPill, statusWords, testRuns } from './task-page.ts';

describe('the status pill of the task page', () => {
  it('says Building while the agent works, Running checks while a check runs, and which run it is on', () => {
    // the core counts iterations from 0: the first run is iteration 0 of a started task
    expect(statusPill({ status: 'IN_PROGRESS', iteration: 0, started: 1 })).toBe('Building · run 1');
    expect(statusPill({ status: 'IN_PROGRESS', iteration: 2, started: 1 })).toBe('Building · run 3');
    expect(statusPill({ status: 'IN_PROGRESS', iteration: 0, started: 1 }, undefined, [{ status: 'passed' }, { status: 'running' }])).toBe('Running checks · run 1');
    expect(statusPill({ status: 'BACKLOG', iteration: 0 })).toBe('Backlog');
    expect(statusPill({ status: 'WAITING_HUMAN', iteration: 1 })).toBe('Waiting for you · run 2');
    expect(statusPill({ status: 'COMPLETED', iteration: 0, started: 1 })).toBe('Completed · run 1');
    expect([runNumber({ iteration: 0 }), runNumber({ iteration: 0, started: 5 }), runNumber({ iteration: 4 })]).toEqual([0, 1, 5]);
  });

  it('a paused run says who paused it', () => {
    expect(statusWords({ status: 'IN_PROGRESS' }, { by: 'human:kyle', at: 1 })).toBe('Paused by kyle');
    expect(statusPill({ status: 'IN_PROGRESS', iteration: 1, started: 1 }, { by: 'human:kyle', at: 1 })).toBe('Paused by kyle · run 2');
    expect(personName('human:ana')).toBe('ana');
    expect(statusDot({ status: 'IN_PROGRESS' }, { by: 'human:kyle', at: 1 })).toBe('warn');
    expect(['IN_PROGRESS', 'WAITING_HUMAN', 'COMPLETED', 'FAILED', 'BACKLOG'].map((status) => statusDot({ status }))).toEqual(['accent', 'warn', 'ok', 'err', 'muted']);
  });
});

describe('the acceptance criteria and the checks', () => {
  it("gives each criterion the core's state by its index, unknown when the core says nothing", () => {
    expect(
      criteriaWithState(['a', 'b', 'c', 'd'], [
        { index: 0, id: 'AC1', state: 'passed' },
        { index: 1, id: 'AC2', state: 'failing' },
        { index: 3, id: '', state: 'something new' },
      ]),
    ).toEqual([
      { text: 'a', state: 'passed', id: 'AC1' },
      { text: 'b', state: 'failing', id: 'AC2' },
      { text: 'c', state: 'unknown', id: '' },
      { text: 'd', state: 'unknown', id: '' },
    ]);
    expect(criteriaWithState(['a'], undefined)).toEqual([{ text: 'a', state: 'unknown', id: '' }]);
    expect(criteriaWithState(undefined, [])).toEqual([]);
  });

  it('gives each criterion its own status with what showed it, an older core its check state (P125)', () => {
    const names = (id: string) => ({ a1: 'Dana', a2: 'Rex' })[id] ?? id;
    expect(
      criteriaRows(
        ['a', 'b', 'c', 'd', 'e'],
        [
          { index: 0, id: 'AC1', status: 'met', source: 'person', by: 'human:kyle', evidence: 'tried it' },
          { index: 1, id: 'AC2', status: 'not_met', source: 'check', by: 'test', evidence: 'failing: TestB_AC2' },
          { index: 2, id: 'AC3', status: 'open', claim: { by: 'a1', evidence: 'TestC_AC3 passes', at: 1 } },
          { index: 3, id: '', status: 'something new' },
        ],
        [{ index: 4, id: 'AC5', state: 'passed' }],
        names,
      ),
    ).toEqual([
      { index: 0, id: 'AC1', text: 'a', status: 'met', why: 'Confirmed by kyle: tried it.', claim: '', personal: true },
      { index: 1, id: 'AC2', text: 'b', status: 'not_met', why: 'By the test check: failing: TestB_AC2.', claim: '', personal: false },
      { index: 2, id: 'AC3', text: 'c', status: 'open', why: '', claim: 'Dana says it is met: TestC_AC3 passes. Not confirmed: a check or a person confirms it.', personal: false },
      { index: 3, id: '', text: 'd', status: 'open', why: '', claim: '', personal: false },
      { index: 4, id: 'AC5', text: 'e', status: 'met', why: 'By the test check: the tests pass.', claim: '', personal: false },
    ]);
    expect(criterionWhy({ index: 0, id: 'AC1', status: 'not_met', source: 'review', by: 'a2', evidence: 'AC1 fails for zero' }, 'not_met', names)).toBe("Rex's review: AC1 fails for zero.");
    expect(criterionWhy({ index: 0, id: 'AC1', status: 'not_met', source: 'person', by: 'human:ana' }, 'not_met', names)).toBe('Marked not met by ana.');
    expect(criteriaRows(undefined, undefined, undefined)).toEqual([]);
  });

  it('names the check kinds as the grid does and reads a state the core adds later as not run', () => {
    expect(['test', 'typecheck', 'lint', 'secrets', 'merge-conflicts'].map(checkName)).toEqual(['tests', 'types', 'lint', 'secrets', 'conflicts']);
    expect(['passed', 'failed', 'running', 'inconclusive', 'not_run', 'queued'].map(checkMark)).toEqual(['passed', 'failed', 'running', 'inconclusive', 'not_run', 'not_run']);
  });
});

describe('this run', () => {
  it('counts the test evidence of the newest attempt the evidence records', () => {
    const ev = [
      { kind: 'test', attempt: 1 },
      { kind: 'test', attempt: 2 },
      { kind: 'green_test', attempt: 2 },
      { kind: 'diff', attempt: 2 },
      { kind: 'e2e', attempt: 2 },
    ];
    expect(testRuns(ev)).toBe(3);
    expect(testRuns([...ev, { kind: 'diff', attempt: 3 }])).toBe(0);
    expect(testRuns([])).toBe(0);
  });

  it('runs from the start to the end, or to now while open; nothing before it started', () => {
    const t0 = 1_760_000_000_000;
    expect(runMs({ started: t0, completed: t0 + 60_000 })).toBe(60_000);
    expect(runMs({ started: t0 }, t0 + 10_000)).toBe(10_000);
    expect(runMs({})).toBe(undefined);
  });
});

describe('a label a person types', () => {
  it('is trimmed and lower-cased like the core does, and refused before sending when it cannot be kept', () => {
    expect(addLabel(['parser'], '  Lexer ')).toEqual({ labels: ['parser', 'lexer'] });
    expect(addLabel([], '   ')).toEqual({ error: 'Type a label first.' });
    expect(addLabel(['parser'], 'PARSER')).toEqual({ error: 'The task already has the label parser.' });
    expect(addLabel([], 'x'.repeat(25))).toEqual({ error: 'A label is at most 24 characters.' });
    expect(addLabel(Array.from({ length: TASK_LABELS_MAX }, (_, i) => `l${i}`), 'more')).toEqual({ error: 'A task has at most 8 labels.' });
  });
});

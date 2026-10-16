import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { PendingRequest, Task } from '../../api/types.ts';
import {
  ago,
  boardColumns,
  budgetPayload,
  classifyDiffLine,
  columnOf,
  criterionLine,
  diffLines,
  editedCriteria,
  elapsed,
  evidenceResult,
  filterCounts,
  groupByStatus,
  inFilter,
  isRunActive,
  isTerminal,
  isWaitingForTask,
  matchesText,
  newestDiffId,
  parseAcceptance,
  payloadOf,
  specPayload,
  toMs,
} from './tasks.ts';

const task = (over: Partial<Task>): Task => ({
  id: 't',
  projectId: 'p',
  seq: 1,
  title: 'T',
  description: '',
  status: 'BACKLOG',
  priority: 0,
  iteration: 0,
  maxIterations: 5,
  costUsd: 0,
  created: 0,
  updated: 0,
  version: 1,
  ...over,
});

describe('task list helpers', () => {
  it('groups by status with what waits on you first, newest first within a group, unknown statuses last', () => {
    const groups = groupByStatus([
      task({ id: 'a', status: 'BACKLOG', updated: 1 }),
      task({ id: 'b', status: 'COMPLETED', updated: 5 }),
      task({ id: 'c', status: 'WEIRD', updated: 3 }),
      task({ id: 'd', status: 'VERIFICATION', updated: 2 }),
      task({ id: 'e', status: 'BACKLOG', updated: 9 }),
    ]);
    expect(groups.map((g) => g.status)).toEqual(['VERIFICATION', 'BACKLOG', 'COMPLETED', 'WEIRD']);
    expect(groups[1]!.tasks.map((x) => x.id)).toEqual(['e', 'a']);
  });

  it('filters by status group and counts each filter', () => {
    const list = [task({ id: 't1', status: 'WAITING_HUMAN' }), task({ id: 't2', status: 'BLOCKED' }), task({ id: 't3', status: 'IN_PROGRESS' }), task({ id: 't4', status: 'CANCELLED' }), task({ id: 't5', status: 'NEW_KIND' })];
    expect(inFilter(list[0]!, 'waiting')).toBe(true);
    expect(inFilter(list[2]!, 'waiting')).toBe(false);
    expect(inFilter(list[4]!, 'all')).toBe(true);
    expect(inFilter(list[4]!, 'active')).toBe(false);
    expect(filterCounts(list)).toEqual({ all: 5, waiting: 2, active: 1, backlog: 0, done: 1 });
    // a task whose run waits for the human (a watchdog question) is waiting on you, not active, whatever its status
    const asked = new Set([list[2]!.id]);
    expect(list[2]!.status).toBe('IN_PROGRESS');
    expect(inFilter(list[2]!, 'waiting', asked)).toBe(true);
    expect(inFilter(list[2]!, 'active', asked)).toBe(false);
    expect(filterCounts(list, asked)).toEqual({ all: 5, waiting: 3, active: 0, backlog: 0, done: 1 });
  });

  it('matches text on the title, the number and the branch', () => {
    const x = task({ seq: 12, title: 'Add Multiply', branch: 'drydock/t-12' });
    expect(matchesText(x, '')).toBe(true);
    expect(matchesText(x, 'multi')).toBe(true);
    expect(matchesText(x, '#12')).toBe(true);
    expect(matchesText(x, '12')).toBe(true);
    expect(matchesText(x, 'drydock/')).toBe(true);
    expect(matchesText(x, 'divide')).toBe(false);
  });

  it('puts each status in its board column; a task that asks for the person is in Needs you whatever its status', () => {
    expect(['BACKLOG', 'READY', 'CLAIMED'].map((status) => columnOf({ status }))).toEqual(['next', 'next', 'next']);
    expect(['IN_PROGRESS', 'REVIEW', 'VERIFICATION', 'BLOCKED'].map((status) => columnOf({ status }))).toEqual(['working', 'working', 'working', 'working']);
    expect(columnOf({ status: 'WAITING_HUMAN' })).toBe('needs');
    expect(['COMPLETED', 'FAILED', 'CANCELLED'].map((status) => columnOf({ status }))).toEqual(['done', 'done', 'done']);
    expect(columnOf({ status: 'NEW_KIND' })).toBe('working');
    expect(columnOf({ id: 't3', status: 'IN_PROGRESS' }, new Set(['t3']))).toBe('needs');
  });

  it('the board always has its four columns, Up next lowest number first, the others newest first', () => {
    const cols = boardColumns([
      task({ id: 'a', seq: 4, status: 'BACKLOG', updated: 9 }),
      task({ id: 'b', seq: 2, status: 'READY', updated: 1 }),
      task({ id: 'c', seq: 3, status: 'COMPLETED', updated: 3 }),
      task({ id: 'd', seq: 1, status: 'COMPLETED', updated: 8 }),
      task({ id: 'e', seq: 5, status: 'WAITING_HUMAN', updated: 2 }),
    ]);
    expect(cols.map((c) => c.column)).toEqual(['next', 'working', 'needs', 'done']);
    expect(cols.map((c) => c.tasks.map((x) => x.id))).toEqual([['b', 'a'], [], ['e'], ['d', 'c']]);
  });

  it('a task\'s elapsed time runs from its start to its completion or now, in seconds or milliseconds', () => {
    expect(toMs(undefined)).toBe(0);
    expect(toMs(1_700_000_000)).toBe(1_700_000_000_000);
    expect(toMs(1_700_000_000_000)).toBe(1_700_000_000_000);
    expect(elapsed({ started: undefined }, 1000)).toBe('');
    expect(elapsed({ started: 1_700_000_000_000, completed: 1_700_000_312_000 })).toBe('5 m 12 s');
    expect(elapsed({ started: 1_700_000_000 }, 1_700_000_040_000)).toBe('40 s');
    expect(elapsed({ started: 1_700_000_000_000 }, 1_700_003_780_000)).toBe('1 h 03 m');
  });

  it('says how long ago in a compact form', () => {
    const now = 10_000_000_000;
    expect(ago(0, now)).toBe('');
    expect(ago(now - 5_000, now)).toBe('now');
    expect(ago(now - 5 * 60_000, now)).toBe('5m');
    expect(ago(now - 3 * 3600_000, now)).toBe('3h');
    expect(ago(now - 2 * 86400_000, now)).toBe('2d');
  });
});

describe('task create and detail helpers', () => {
  it('acceptance criteria are one per line, trimmed, blank lines dropped', () => {
    expect(parseAcceptance('  Given a\n\n When b \n')).toEqual(['Given a', 'When b']);
    expect(parseAcceptance('')).toEqual([]);
  });

  it('knows closed tasks, active runs and tasks waiting for another task', () => {
    expect(isTerminal('COMPLETED')).toBe(true);
    expect(isTerminal('VERIFICATION')).toBe(false);
    expect(isRunActive(undefined)).toBe(false);
    expect(isRunActive({ status: 'RUNNING' })).toBe(true);
    expect(isRunActive({ status: 'FAILED' })).toBe(false);
    expect(isWaitingForTask({ status: 'BLOCKED', blockedReason: 'waiting for "A" to finish' })).toBe(true);
    expect(isWaitingForTask({ status: 'BLOCKED', blockedReason: 'the build server is down' })).toBe(false);
    expect(isWaitingForTask({ status: 'READY', blockedReason: 'waiting for x' })).toBe(false);
  });

  it('evidence results: true/false are pass/fail, e2e and preview without a result are skipped, never a pass', () => {
    expect(evidenceResult({ kind: 'test', passed: true })).toBe('pass');
    expect(evidenceResult({ kind: 'test', passed: false })).toBe('fail');
    expect(evidenceResult({ kind: 'e2e', passed: null })).toBe('skipped');
    expect(evidenceResult({ kind: 'preview', passed: undefined })).toBe('skipped');
    expect(evidenceResult({ kind: 'spec', passed: null })).toBe('info');
  });
});

describe('pending request payloads', () => {
  const req = (kind: string, payload: unknown): PendingRequest => ({ id: 'r', nodeId: 'n', step: 1, spec: { kind, title: 'x', payload } });

  it('reads files and branches and drops anything malformed', () => {
    expect(payloadOf(req('review', { files: ['a.go', 3, 'b.go'], branch: 'drydock/t1', base: 7 }))).toEqual({ files: ['a.go', 'b.go'], branch: 'drydock/t1', base: undefined });
    expect(payloadOf(req('review', undefined))).toEqual({ files: [], branch: undefined, base: undefined });
  });

  it('a spec payload is only a review in the spec phase; a budget pause has gate budget', () => {
    expect(specPayload(req('review', { phase: 'spec', title: 'S' }))?.title).toBe('S');
    expect(specPayload(req('approval', { phase: 'spec' }))).toBeUndefined();
    expect(budgetPayload(req('approval', { gate: 'budget', message: 'm' }))?.message).toBe('m');
    expect(budgetPayload(req('approval', { gate: 'other' }))).toBeUndefined();
  });

  it('criteria are sent only when edited', () => {
    const c = [{ id: 'AC1', given: '3 and 4', when: 'Multiply is called', then: 'it returns 12' }];
    expect(criterionLine(c[0]!)).toBe('Given 3 and 4, when Multiply is called, then it returns 12');
    expect(editedCriteria(c, 'Given 3 and 4, when Multiply is called, then it returns 12\n')).toBeUndefined();
    expect(editedCriteria(c, 'Given 1, when x, then y\n\n Given 2, when x, then z ')).toEqual(['Given 1, when x, then y', 'Given 2, when x, then z']);
  });
});

describe('diff lines', () => {
  it('classifies diff lines', () => {
    expect(['+++ b/a', '--- a/a', '@@ -1 +1 @@', '+x', '-x', ' x', 'diff --git a b'].map(classifyDiffLine)).toEqual(['meta', 'meta', 'hunk', 'add', 'del', 'ctx', 'meta']);
  });

  it('a trailing newline is not an extra line', () => {
    expect(diffLines('+a\n-b\n')).toEqual(['+a', '-b']);
  });

  it('the patch a card is about is the newest evidence of kind diff (L36)', () => {
    const e = (id: number, kind: string) => ({ id, kind });
    expect(newestDiffId([])).toBeUndefined();
    expect(newestDiffId([e(1, 'test'), e(2, 'review')])).toBeUndefined();
    expect(newestDiffId([e(1, 'test'), e(2, 'diff'), e(3, 'test')])).toBe(2);
    expect(newestDiffId([e(2, 'diff'), e(5, 'test'), e(9, 'diff'), e(11, 'test')])).toBe(9);
    // whatever order the core lists them in
    expect(newestDiffId([e(9, 'diff'), e(2, 'diff'), e(5, 'diff')])).toBe(9);
  });
});

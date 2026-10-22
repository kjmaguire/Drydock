import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { PendingRequest, TaskSummary } from '../../api/types.ts';
import {
  newestDiffStat,
  ago,
  APPROVE,
  boardOf,
  budgetPayload,
  cardTag,
  checkSegments,
  checksLabel,
  classifyDiffLine,
  clock,
  columnOf,
  criterionLine,
  diffLines,
  diffText,
  doingLine,
  editedCriteria,
  elapsed,
  evidenceResult,
  failingLine,
  finishedWhen,
  isRunActive,
  isTerminal,
  isWaitingForTask,
  labelsOf,
  matchesFilter,
  mergeRequestOf,
  needsMe,
  newestDiffId,
  ordinal,
  parseAcceptance,
  parseLabels,
  payloadOf,
  progressOf,
  rangeOf,
  specGateText,
  specPayload,
  taskKey,
  timelineOf,
  timelineTicks,
  toMs,
  verdictLine,
  type BoardFilter,
} from './tasks.ts';

const sum = (over: Partial<TaskSummary> = {}): TaskSummary => ({
  taskId: 't',
  projectId: 'p',
  projectKey: 'GOC',
  seq: 1,
  title: 'T',
  status: 'IN_PROGRESS',
  labels: [],
  criteria: 0,
  step: 'code',
  stepIndex: 1,
  stepCount: 5,
  attempt: 1,
  maxAttempts: 5,
  checks: [],
  pendingCount: 0,
  updatedAt: 0,
  ...over,
});

const req = (phase: string): TaskSummary['pending'] => ({ kind: phase === 'merge' || phase === 'spec' ? 'review' : phase, phase, title: 'x', requestId: 'r1', runId: 'run1', createdAt: 0 });

describe('the board', () => {
  it('puts each step in its column: spec Spec, code Building (Checks while a check runs), checks Checks, review and you Review, done Merged', () => {
    expect(columnOf(sum({ status: 'BACKLOG', step: 'spec' }))).toBe('spec');
    expect(columnOf(sum({ status: 'WAITING_HUMAN', step: 'spec', pending: req('spec') }))).toBe('spec');
    expect(columnOf(sum({ step: 'code' }))).toBe('building');
    expect(columnOf(sum({ step: 'code', checks: [{ kind: 'test', status: 'passed' }, { kind: 'lint', status: 'running' }] }))).toBe('checks');
    expect(columnOf(sum({ step: 'code', checks: [{ kind: 'test', status: 'failed' }] }))).toBe('building');
    expect(columnOf(sum({ step: 'checks' }))).toBe('checks');
    expect(columnOf(sum({ status: 'REVIEW', step: 'review' }))).toBe('review');
    expect(columnOf(sum({ status: 'VERIFICATION', step: 'you' }))).toBe('review');
    expect(columnOf(sum({ status: 'COMPLETED', step: 'done' }))).toBe('merged');
    // failed and cancelled are kept apart, whatever step their evidence reached
    expect(columnOf(sum({ status: 'FAILED', step: 'checks' }))).toBe('stopped');
    expect(columnOf(sum({ status: 'CANCELLED', step: 'spec' }))).toBe('stopped');
    expect(columnOf(sum({ status: 'NEW_KIND', step: 'mystery' }))).toBe('building');
  });

  it('always has its five columns; waiting first; Spec under way first, then not started lowest number first; Merged newest finish first; stopped apart', () => {
    const b = boardOf([
      sum({ taskId: 'a', seq: 4, status: 'BACKLOG', step: 'spec', updatedAt: 9 }),
      sum({ taskId: 'b', seq: 2, status: 'READY', step: 'spec', updatedAt: 1 }),
      sum({ taskId: 'c', seq: 6, status: 'IN_PROGRESS', step: 'spec', startedAt: 5, updatedAt: 3 }),
      sum({ taskId: 'd', seq: 7, status: 'WAITING_HUMAN', step: 'spec', pending: req('spec'), pendingCount: 1, updatedAt: 0 }),
      sum({ taskId: 'e', seq: 3, status: 'COMPLETED', step: 'done', completedAt: 100 }),
      sum({ taskId: 'f', seq: 1, status: 'COMPLETED', step: 'done', completedAt: 200 }),
      sum({ taskId: 'g', seq: 8, status: 'IN_PROGRESS', step: 'code', updatedAt: 1 }),
      sum({ taskId: 'h', seq: 9, status: 'IN_PROGRESS', step: 'code', updatedAt: 5 }),
      sum({ taskId: 'i', seq: 10, status: 'FAILED', step: 'code', completedAt: 50 }),
    ]);
    expect(b.columns.map((c) => c.column)).toEqual(['spec', 'building', 'checks', 'review', 'merged']);
    expect(b.columns.map((c) => c.tasks.map((x) => x.taskId))).toEqual([['d', 'c', 'b', 'a'], ['h', 'g'], [], [], ['f', 'e']]);
    expect(b.stopped.map((x) => x.taskId)).toEqual(['i']);
    // a task whose run asks for the person through Needs you (no request of its own) comes first too
    const asked = new Set(['g']);
    expect(boardOf([sum({ taskId: 'g', updatedAt: 1 }), sum({ taskId: 'h', updatedAt: 5 })], (x) => needsMe(x, asked)).columns[1]!.tasks.map((x) => x.taskId)).toEqual(['g', 'h']);
  });

  it('names a task by its project key and number', () => {
    expect(taskKey({ projectKey: 'GOC', seq: 41 })).toBe('GOC-41');
    expect(taskKey({ projectKey: '', seq: 41 })).toBe('#41');
  });

  it('tags a card: ready to merge, waiting on you, the time it has run, up next or backlog, nothing when finished', () => {
    const now = 1_700_000_600_000;
    expect(cardTag(sum({ pending: req('merge') }), false, now)).toEqual({ kind: 'merge', text: 'Ready to merge' });
    for (const p of ['spec', 'approval', 'question', 'review']) expect(cardTag(sum({ pending: req(p) }), false, now)?.text).toBe('Waiting on you');
    expect(cardTag(sum({}), true, now)?.text).toBe('Waiting on you');
    expect(cardTag(sum({ startedAt: now - 587_000 }), false, now)).toEqual({ kind: 'elapsed', text: '9:47' });
    // the core writes seconds in some fields
    expect(cardTag(sum({ startedAt: (now - 80_000) / 1000 }), false, now)?.text).toBe('1:20');
    expect(cardTag(sum({ status: 'BACKLOG' }), false, now)).toEqual({ kind: 'backlog', text: 'Backlog' });
    expect(cardTag(sum({ status: 'READY' }), false, now)).toEqual({ kind: 'next', text: 'Up next' });
    expect(cardTag(sum({ status: 'COMPLETED', startedAt: 1 }), false, now)).toBeUndefined();
  });

  it('writes clocks, ordinals and when a task finished', () => {
    expect(clock(42_000)).toBe('0:42');
    expect(clock(587_000)).toBe('9:47');
    expect(clock(3_725_000)).toBe('1:02:05');
    expect([1, 2, 3, 4, 11, 12, 13, 21, 22, 23, 101].map(ordinal)).toEqual(['1st', '2nd', '3rd', '4th', '11th', '12th', '13th', '21st', '22nd', '23rd', '101st']);
    const now = new Date(2026, 9, 22, 15, 0).getTime(); // a Thursday
    expect(finishedWhen(new Date(2026, 9, 22, 9, 0).getTime(), now)).toBe('Today');
    expect(finishedWhen(new Date(2026, 9, 21, 23, 0).getTime(), now)).toBe('Yesterday');
    expect(finishedWhen(new Date(2026, 9, 19, 12, 0).getTime(), now)).toBe('Mon');
    expect(finishedWhen(new Date(2026, 9, 1, 12, 0).getTime(), now)).toBe('Oct 1');
    expect(finishedWhen(undefined, now)).toBe('');
  });

  it('says what a card says at its step: the spec gate, what the agent is doing on which try, how far along, the verdict', () => {
    expect(specGateText(4)).toBe('Spec gate: 4 acceptance criteria written. Accept, edit or send back with feedback.');
    expect(specGateText(1)).toBe('Spec gate: 1 acceptance criterion written. Accept, edit or send back with feedback.');
    expect(doingLine({ doing: 'Running go test ./...', attempt: 3 })).toBe('Running go test ./... · 3rd try');
    expect(doingLine({ doing: 'Editing format.go', attempt: 1 })).toBe('Editing format.go');
    expect(doingLine({ attempt: 2 })).toBe('2nd try');
    expect(doingLine({ attempt: 1 })).toBe('');
    expect(progressOf({ stepIndex: 1, stepCount: 5 })).toBe(20);
    expect(progressOf({ stepIndex: 9, stepCount: 5 })).toBe(100);
    expect(progressOf({ stepIndex: 0, stepCount: 0 })).toBe(0);
    expect(diffText({ insertions: 48, deletions: 6 })).toBe('+48\u00a0−6');
    expect(verdictLine({ verdict: 'SHIP', diff: { files: 2, insertions: 31, deletions: 2 } }, 'Reviewer')).toBe('Reviewer approved · 2 files · +31\u00a0−2');
    expect(verdictLine({ verdict: 'REVISE', diff: { files: 1, insertions: 1, deletions: 0 } }, 'Rex')).toBe('Rex asked for changes · 1 file · +1\u00a0−0');
    expect(verdictLine({ verdict: 'SHIP' }, '')).toBe('The reviewer approved');
    expect(verdictLine({}, 'Rex')).toBe('');
  });

  it('maps the check kinds onto the seven segments, only those the project runs, each showing the worst of its kinds', () => {
    const checks = [
      { kind: 'build', status: 'passed' },
      { kind: 'test', status: 'passed' },
      { kind: 'lint', status: 'passed' },
      { kind: 'typecheck', status: 'passed' },
      { kind: 'secrets', status: 'passed' },
      { kind: 'deps', status: 'passed' },
      { kind: 'ci', status: 'running' },
      { kind: 'preview', status: 'failed', reason: 'the page did not start on port 5173' },
    ];
    const segs = checkSegments(checks);
    expect(segs.map((x) => [x.name, x.state])).toEqual([
      ['tests', 'passed'],
      ['lint', 'passed'],
      ['types', 'passed'],
      ['secrets', 'passed'],
      ['deps', 'passed'],
      ['ci', 'running'],
      ['preview', 'failed'],
    ]);
    expect(segs[0]!.title).toBe('build: passed, test: passed');
    expect(checksLabel(segs)).toBe('Checks: 5 passed, 1 running, 1 failed');
    expect(failingLine(checks)).toBe('preview: the page did not start on port 5173');
    // a project without lint, deps or a preview: only its own segments
    const few = checkSegments([{ kind: 'test', status: 'failed', failing: ['TestSubtract'] }, { kind: 'e2e', status: 'not_run' }, { kind: 'secrets', status: 'inconclusive' }, { kind: 'merge-conflicts', status: 'passed' }]);
    expect(few.map((x) => [x.name, x.state])).toEqual([
      ['tests', 'failed'],
      ['secrets', 'inconclusive'],
      ['ci', 'passed'],
    ]);
    expect(checksLabel(few)).toBe('Checks: 1 passed, 1 failed, 1 inconclusive');
    expect(checksLabel(checkSegments([{ kind: 'test', status: 'not_run' }]))).toBe('Checks: 1 not run');
    expect(failingLine([{ kind: 'test', status: 'failed', failing: ['TestSubtract'] }])).toBe('test: TestSubtract failed');
    expect(failingLine([{ kind: 'lint', status: 'failed' }])).toBe('lint: failed');
    expect(failingLine([{ kind: 'lint', status: 'passed' }])).toBe('');
    expect(failingLine([{ kind: 'test', status: 'failed', reason: 'x'.repeat(300) }]).length).toBe(160);
  });

  it('filters by agent (worker or reviewer), by label, and to what needs the person', () => {
    const a = sum({ taskId: 'a', assignee: 'w1', reviewer: 'r1', labels: ['docs'] });
    const b = sum({ taskId: 'b', assignee: 'w2', labels: [], pendingCount: 1 });
    const c = sum({ taskId: 'c', assignee: 'w2', labels: ['api', 'docs'] });
    const asked = new Set(['c']);
    const pick = (f: BoardFilter) => [a, b, c].filter((x) => matchesFilter(x, f, asked)).map((x) => x.taskId);
    expect(pick({})).toEqual(['a', 'b', 'c']);
    expect(pick({ agent: 'w2' })).toEqual(['b', 'c']);
    expect(pick({ agent: 'r1' })).toEqual(['a']);
    expect(pick({ label: 'docs' })).toEqual(['a', 'c']);
    expect(pick({ needsMe: true })).toEqual(['b', 'c']);
    expect(pick({ needsMe: true, label: 'docs', agent: 'w2' })).toEqual(['c']);
    expect(labelsOf([a, b, c])).toEqual(['api', 'docs']);
  });

  it('reads typed labels as the core keeps them, and refuses too many or too long before sending', () => {
    expect(parseLabels(' Docs, api ,docs,, ')).toEqual({ labels: ['docs', 'api'] });
    expect(parseLabels('')).toEqual({ labels: [] });
    expect(parseLabels('a,b,c,d,e,f,g,h,i').error).toBe('A task takes up to 8 labels; this is 9.');
    expect(parseLabels('x'.repeat(25)).error).toBe(`"${'x'.repeat(25)}" is longer than 24 characters.`);
  });

  it('a ready-to-merge card answers its merge request, nothing else', () => {
    expect(mergeRequestOf(sum({ pending: req('merge') }))).toEqual({ runId: 'run1', requestId: 'r1' });
    expect(mergeRequestOf(sum({ pending: req('spec') }))).toBeUndefined();
    expect(mergeRequestOf(sum({}))).toBeUndefined();
    expect(APPROVE).toBe('approved');
  });

  it('lays the tasks along the range, one lane per worker, overlapping runs on separate rows, the unstarted and the old left out', () => {
    const T = 1_700_000_000_000;
    const r = { from: T, to: T + 1000 };
    const lanes = timelineOf(
      [
        sum({ taskId: 'a', assignee: 'w1', startedAt: T + 100, status: 'COMPLETED', completedAt: T + 300 }),
        sum({ taskId: 'b', assignee: 'w1', startedAt: T + 200, status: 'IN_PROGRESS' }),
        sum({ taskId: 'c', assignee: 'w2', startedAt: T + 50, status: 'VERIFICATION', pendingCount: 1 }),
        sum({ taskId: 'd', assignee: 'w2', status: 'BACKLOG' }),
        sum({ taskId: 'e', assignee: 'w3', startedAt: T - 500, status: 'FAILED', completedAt: T - 100, updatedAt: T - 100 }),
        sum({ taskId: 'f', startedAt: T + 900, status: 'CANCELLED', completedAt: T + 950 }),
      ],
      r,
      T + 1000,
    );
    expect(lanes.map((l) => l.agent)).toEqual(['w2', 'w1', '']);
    const w1 = lanes[1]!;
    expect(w1.rows).toBe(2);
    expect(w1.blocks.map((b) => [b.task.taskId, b.left, Math.round(b.width), b.row, b.state])).toEqual([
      ['a', 10, 20, 0, 'done'],
      ['b', 20, 80, 1, 'running'],
    ]);
    expect(lanes[0]!.blocks.map((b) => b.state)).toEqual(['waiting']);
    expect(lanes[2]!.blocks.map((b) => b.state)).toEqual(['stopped']);
    expect(rangeOf('week', 10 * 86_400_000)).toEqual({ from: 3 * 86_400_000, to: 10 * 86_400_000 });
    const today = rangeOf('today', new Date(2026, 9, 22, 15, 0).getTime());
    expect(today.from).toBe(new Date(2026, 9, 22).getTime());
    expect(timelineTicks('today', today).map((x) => x.label)).toEqual(['00:00', '02:00', '04:00', '06:00', '08:00', '10:00', '12:00', '14:00']);
    const week = rangeOf('week', new Date(2026, 9, 22, 15, 0).getTime());
    expect(timelineTicks('week', week).map((x) => x.label)).toEqual(['Fri', 'Sat', 'Sun', 'Mon', 'Tue', 'Wed', 'Thu']);
  });
});

describe('task list helpers', () => {
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
    expect(payloadOf(req('review', { files: ['a.go', 3, 'b.go'], branch: 'drydock/t1', base: 7 }))).toEqual({ files: ['a.go', 'b.go'], branch: 'drydock/t1', base: undefined, diff: undefined });
    expect(payloadOf(req('review', undefined))).toEqual({ files: [], branch: undefined, base: undefined, diff: undefined });
    // the newest diff evidence the request was raised on (UI review P1-1)
    const evidence = [
      { id: 4, kind: 'diff', data: { Files: 3, Insertions: 40, Deletions: 2 } },
      { id: 9, kind: 'diff', data: { Files: 1, Insertions: 4, Deletions: 'x' } },
      { id: 7, kind: 'diff', data: { Files: 2, Insertions: 1, Deletions: 1 } },
      { id: 12, kind: 'test', data: {} },
    ];
    expect(payloadOf(req('review', { evidence })).diff).toEqual({ id: 9, files: 1, insertions: 4, deletions: 0 });
    // a task's own evidence is read the same way; anything that is not a list has no diff
    expect(newestDiffStat(evidence)).toEqual({ id: 9, files: 1, insertions: 4, deletions: 0 });
    expect(newestDiffStat(undefined)).toBeUndefined();
    expect(newestDiffStat([{ id: 2, kind: 'test' }])).toBeUndefined();
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

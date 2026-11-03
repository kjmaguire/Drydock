import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { allDoneOf, dueISO, dueText, milestonesOfTask, nextMilestone, matchesFilter, progressText, segments, taskGroups, unfinishedOf, zeroTime } from './milestones.ts';

const p = {
  total: 4,
  completed: 2,
  inProgress: 1,
  blocked: 0,
  open: 1,
  cancelled: 0,
  failed: 0,
  percent: 50,
};

describe('Milestone helpers', () => {
  it('counts the unfinished tasks as the core does when a milestone is closed (X410)', () => {
    expect(unfinishedOf(p)).toBe(2);
    expect(unfinishedOf({ ...p, completed: 3, open: 0, inProgress: 0, cancelled: 1 })).toBe(0);
    expect(unfinishedOf({ ...p, total: 0, completed: 0, inProgress: 0, open: 0 })).toBe(0);
  });

  it('says "All done" only when every task was completed or cancelled, not when some failed', () => {
    expect(allDoneOf({ ...p, completed: 3, open: 0, inProgress: 0, cancelled: 1 })).toBe(true);
    expect(allDoneOf({ ...p, total: 3, completed: 0, inProgress: 0, open: 0, failed: 3 })).toBe(false);
    expect(allDoneOf({ ...p, total: 3, completed: 2, inProgress: 0, open: 0, failed: 1 })).toBe(false);
    expect(allDoneOf({ ...p, total: 2, completed: 0, inProgress: 0, open: 0, cancelled: 2 })).toBe(false);
    expect(allDoneOf({ ...p, total: 0, completed: 0, inProgress: 0, open: 0 })).toBe(false);
    expect(allDoneOf(p)).toBe(false);
  });

  it('describes progress in words', () => {
    expect(progressText(p)).toBe('2 of 4 completed; 1 in progress, 0 blocked, 1 not started, 0 failed, 0 cancelled.');
  });

  it('sends a due date as midnight UTC and nothing when empty', () => {
    expect(dueISO('2027-03-01')).toBe('2027-03-01T00:00:00.000Z');
    expect(dueISO('')).toBe(undefined);
    expect(zeroTime('0001-01-01T00:00:00Z')).toBe(true);
    expect(zeroTime('2027-01-01T00:00:00Z')).toBe(false);
  });

  it('splits the bar by state and drops empty segments', () => {
    expect(segments(p)).toEqual([
      { key: 'completed', pct: 50 },
      { key: 'inProgress', pct: 25 },
    ]);
    expect(segments({ ...p, total: 0 })).toEqual([]);
  });
});

describe('adding many tasks (X49)', () => {
  it('filters by number, title or label', () => {
    const x = { seq: 12, title: 'Add Divide', labels: ['math'] };
    expect([matchesFilter(x, ''), matchesFilter(x, '#12'), matchesFilter(x, 'divide'), matchesFilter(x, 'MATH'), matchesFilter(x, '1'), matchesFilter(x, 'ui')]).toEqual([true, true, true, true, false, false]);
  });

  it('offers an accepted plan and each label, over the tasks that can still be added', () => {
    const free = [{ id: 'a', labels: ['ui'] }, { id: 'b' }, { id: 'c', labels: ['api', 'ui'] }];
    const plans: { id: string; goal: string; status: string; createdTasks: Record<string, string> }[] = [
      { id: 'p1', goal: 'G', status: 'ACCEPTED', createdTasks: { x: 'a', y: 'z' } },
      { id: 'p2', goal: 'H', status: 'PROPOSED', createdTasks: { x: 'b' } },
      { id: 'p3', goal: 'I', status: 'ACCEPTED', createdTasks: { x: 'gone' } },
    ];
    expect(taskGroups(free, plans)).toEqual([
      { key: 'plan:p1', label: 'All tasks of the plan "G"', ids: ['a'] },
      { key: 'label:api', label: 'All tasks labelled api', ids: ['c'] },
      { key: 'label:ui', label: 'All tasks labelled ui', ids: ['a', 'c'] },
    ]);
  });
});

describe('due and overdue milestones (X411)', () => {
  const NOW = Date.parse('2026-11-03T12:00:00Z');
  const ms = (id: string, dueAt: string | undefined, over: Partial<{ overdue: boolean; status: string; taskIds: string[] }> = {}) => ({
    id,
    title: id,
    dueAt,
    overdue: false,
    status: 'open',
    taskIds: [] as string[],
    ...over,
  });
  // local wall-clock times, so the calendar days hold in any time zone the test runs in
  const local = (d: number, h: number) => new Date(2026, 10, d, h, 0).toISOString();
  const at = (d: number, h: number) => new Date(2026, 10, d, h, 0).getTime();
  it('says how far an open milestone is from its date, in days', () => {
    expect(dueText(ms('a', local(6, 12)), at(3, 12))).toBe('due in 3 days');
    expect(dueText(ms('a', local(4, 12)), at(3, 12))).toBe('due in 1 day');
    expect(dueText(ms('a', local(3, 18)), at(3, 12))).toBe('due today');
    expect(dueText(ms('a', local(1, 12), { overdue: true }), at(3, 12))).toBe('overdue by 2 days');
    expect(dueText(ms('a', local(2, 23), { overdue: true }), at(3, 1))).toBe('overdue by 1 day');
    expect(dueText(ms('a', undefined), NOW)).toBe('');
    expect(dueText(ms('a', '0001-01-01T00:00:00Z'), NOW)).toBe('');
    expect(dueText(ms('a', '2026-11-01T12:00:00Z', { status: 'closed' }), NOW)).toBe('');
  });
  it('counts calendar days, not 24-hour gaps (X411 review)', () => {
    // due at 09:00 today, now 11:00: past due today, not "overdue by 1 day"
    expect(dueText(ms('a', local(3, 9), { overdue: true }), at(3, 11))).toBe('was due today');
    // due at 01:00 tomorrow, now 15:00: tomorrow by the calendar, not "due today"
    expect(dueText(ms('a', local(4, 1)), at(3, 15))).toBe('due in 1 day');
    // due at 22:00 today, now 09:00 (13 hours away): today, not "due in 1 day"
    expect(dueText(ms('a', local(3, 22)), at(3, 9))).toBe('due today');
    // due at 23:00 yesterday, now 01:00 (2 hours late): yesterday by the calendar
    expect(dueText(ms('a', local(2, 23), { overdue: true }), at(3, 1))).toBe('overdue by 1 day');
  });
  it('the next milestone is an overdue one first, then the soonest due; never a closed or undated one', () => {
    const list = [ms('later', '2026-12-01T00:00:00Z'), ms('soon', '2026-11-05T00:00:00Z'), ms('none', undefined), ms('closed', '2026-11-04T00:00:00Z', { status: 'closed' })];
    expect(nextMilestone(list)?.id).toBe('soon');
    expect(nextMilestone([...list, ms('late', '2026-10-01T00:00:00Z', { overdue: true })])?.id).toBe('late');
    expect(nextMilestone([ms('none', undefined)])).toBe(undefined);
  });
  it("splits a project's milestones into the task's and the open ones it could join", () => {
    const list = [ms('a', undefined, { taskIds: ['t1'] }), ms('b', undefined), ms('c', undefined, { status: 'closed' })];
    const r = milestonesOfTask(list, 't1');
    expect([r.in.map((m) => m.id), r.open.map((m) => m.id)]).toEqual([['a'], ['b']]);
  });
});

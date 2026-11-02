import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { allDoneOf, dueISO, matchesFilter, progressText, segments, taskGroups, unfinishedOf, zeroTime } from './milestones.ts';

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

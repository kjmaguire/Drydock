import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { dueISO, progressText, segments, zeroTime } from './milestones.ts';

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

import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { TASK_LIST_LIMIT } from '../api/types.ts';
import { taskCountText, taskCoverage, taskNoticeText, taskSearchNote } from './task-total.ts';

describe('how much of the task list a screen has (M21)', () => {
  it('is the core\'s default of 500, the number the list is cut at', () => {
    expect(TASK_LIST_LIMIT).toBe(500);
  });

  it('is partial when the project has more tasks than were loaded, and complete when the numbers meet', () => {
    expect(taskCoverage(500, 612)).toEqual({ loaded: 500, total: 612, partial: true });
    expect(taskCoverage(12, 12)).toEqual({ loaded: 12, total: 12, partial: false });
    expect(taskCoverage(0, 0).partial).toBe(false);
    // the count is read a moment after the list: a task made in between is not a cut list
    expect(taskCoverage(12, 11).partial).toBe(false);
  });

  it('never takes a list as long as the core sends it for complete when the count is unknown', () => {
    expect(taskCoverage(499, undefined).partial).toBe(false);
    expect(taskCoverage(500, undefined)).toEqual({ loaded: 500, total: undefined, partial: true });
    expect(taskCoverage(0, undefined).partial).toBe(false);
  });

  it('says "Showing the newest N of M tasks" and, without the count, that there may be more', () => {
    expect(taskNoticeText(taskCoverage(500, 612))).toBe('Showing the newest 500 of 612 tasks');
    expect(taskNoticeText(taskCoverage(500, 1203))).toBe('Showing the newest 500 of 1,203 tasks');
    expect(taskNoticeText(taskCoverage(500, undefined))).toBe('Showing the newest 500 tasks; there may be more');
  });

  it('states the count a page head gives, and the search note', () => {
    expect(taskCountText(taskCoverage(1, 1))).toBe('1 task');
    expect(taskCountText(taskCoverage(12, 12))).toBe('12 tasks');
    expect(taskCountText(taskCoverage(500, 612))).toBe('500 of 612 tasks');
    expect(taskCountText(taskCoverage(500, undefined))).toBe('500 tasks (newest; there may be more)');
    expect(taskSearchNote(taskCoverage(500, 612))).toBe('Only the newest 500 of 612 tasks are searched.');
    expect(taskSearchNote(taskCoverage(500, undefined))).toBe('Only the newest 500 tasks are searched; there may be more.');
  });
});

import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { offsetsOf, planWindow, rowAt, rowsBetween } from './virtual.ts';

describe('offsetsOf / rowAt / rowsBetween', () => {
  const offs = offsetsOf([10, 20, 30, 40]); // tops 0, 10, 30, 60; total 100

  it('gives each row its top and the total last', () => {
    expect(offs).toEqual([0, 10, 30, 60, 100]);
    expect(offsetsOf([])).toEqual([0]);
  });

  it('finds the row that contains a point, clamped to the list', () => {
    expect(rowAt(offs, -5)).toBe(0);
    expect(rowAt(offs, 0)).toBe(0);
    expect(rowAt(offs, 9.9)).toBe(0);
    expect(rowAt(offs, 10)).toBe(1);
    expect(rowAt(offs, 59)).toBe(2);
    expect(rowAt(offs, 60)).toBe(3);
    expect(rowAt(offs, 500)).toBe(3);
    expect(rowAt([0], 3)).toBe(0);
  });

  it('lists the rows that meet a band, at least one', () => {
    expect(rowsBetween(offs, 0, 10)).toEqual({ start: 0, end: 1 });
    expect(rowsBetween(offs, 5, 35)).toEqual({ start: 0, end: 3 });
    expect(rowsBetween(offs, 30, 60)).toEqual({ start: 2, end: 3 });
    expect(rowsBetween(offs, -100, 1000)).toEqual({ start: 0, end: 4 });
    expect(rowsBetween(offs, 200, 300)).toEqual({ start: 3, end: 4 });
    expect(rowsBetween([0], 0, 100)).toEqual({ start: 0, end: 0 });
  });
});

describe('planWindow', () => {
  // 1,000 rows of 20 px: 20,000 px
  const offsets = offsetsOf(new Array(1000).fill(20));

  it('covers the view and one overscan on each side, the rest as padding', () => {
    const p = planWindow({ offsets, viewTop: 10_000, viewHeight: 400, overscan: 400 });
    expect(p).toEqual({ start: 480, end: 540, padTop: 9600, padBottom: 20_000 - 10_800 });
    expect(p.end - p.start).toBe((400 + 2 * 400) / 20);
  });

  it('at the bottom renders the last rows, at the top the first', () => {
    expect(planWindow({ offsets, viewTop: 20_000 - 400, viewHeight: 400, overscan: 400 })).toMatchObject({ start: 960, end: 1000, padBottom: 0 });
    expect(planWindow({ offsets, viewTop: -30, viewHeight: 400, overscan: 400 })).toMatchObject({ start: 0, end: 39, padTop: 0 });
  });

  it('keeps the rows rendered before while they cover the view and half an overscan', () => {
    const keep = { start: 480, end: 540 };
    // scrolled 150 px down: still covered (the window ends at 10,800, the view and half an overscan at 10,750)
    expect(planWindow({ offsets, viewTop: 10_150, viewHeight: 400, overscan: 400, keep })).toMatchObject(keep);
    // 250 px down: no longer covered, a new window around the view
    expect(planWindow({ offsets, viewTop: 10_250, viewHeight: 400, overscan: 400, keep })).toMatchObject({ start: 492, end: 553 });
  });

  it('drops a kept window that grew too large (rows appended at the bottom for a long time)', () => {
    const keep = { start: 700, end: 1000 }; // 6,000 px for a 400 px view
    expect(planWindow({ offsets, viewTop: 19_600, viewHeight: 400, overscan: 400, keep })).toMatchObject({ start: 960, end: 1000 });
  });

  it('ignores a kept window that does not fit the list', () => {
    expect(planWindow({ offsets, viewTop: 0, viewHeight: 400, overscan: 400, keep: { start: 990, end: 1200 } })).toMatchObject({ start: 0 });
    expect(planWindow({ offsets: [0], viewTop: 0, viewHeight: 400, overscan: 400, keep: { start: 0, end: 3 } })).toEqual({ start: 0, end: 0, padTop: 0, padBottom: 0 });
  });

  it('measured rows of different heights move the window with them', () => {
    const mixed = offsetsOf([500, ...new Array(99).fill(10)]); // one tall row, then small ones
    // the view starts inside the tall row: it and the small rows below are in the window
    const p = planWindow({ offsets: mixed, viewTop: 300, viewHeight: 150, overscan: 100 });
    expect(p.start).toBe(0);
    expect(p.end).toBe(1 + (300 + 150 + 100 - 500) / 10);
  });
});

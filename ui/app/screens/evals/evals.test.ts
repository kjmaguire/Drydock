import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { evalsOptions, ratioRate, ratioText, WINDOWS } from './evals.ts';

describe('evals helpers', () => {
  it('prints n/a for a zero denominator, never 0%', () => {
    expect(ratioText({ num: 0, den: 0 })).toBe('n/a');
    expect(ratioText({ num: 0, den: 0, rate: null })).toBe('n/a');
    expect(ratioText(undefined)).toBe('n/a');
    expect(ratioText({ num: 0, den: 4, rate: 0 })).toBe('0 of 4 (0%)');
    expect(ratioText({ num: 3, den: 4, rate: 0.75 })).toBe('3 of 4 (75%)');
    expect(ratioRate({ num: 0, den: 0 })).toBe(undefined);
    expect(ratioRate({ num: 0, den: 4, rate: 0 })).toBe(0);
  });

  it('reads this project only when asked, from the window start', () => {
    const now = 1_000_000_000_000;
    expect(evalsOptions('prj_1', 0, true, now)).toEqual({ projectId: 'prj_1', sinceMs: undefined });
    expect(evalsOptions('prj_1', 7, false, now)).toEqual({ projectId: undefined, sinceMs: now - 7 * 86_400_000 });
    expect(WINDOWS.map(([, d]) => d)).toEqual([0, 7, 30]);
  });
});

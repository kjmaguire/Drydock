import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { DevSettings } from '../../api/extTypes.ts';
import { argv, ceilingOff, devBody, numOrUndef, parallelWarn } from './project.ts';

describe('project helpers', () => {
  it('reads one argument per line', () => {
    expect(argv(' npx \n\nplaywright\n test ')).toEqual(['npx', 'playwright', 'test']);
    expect(argv('')).toEqual([]);
  });

  it('reads a number or nothing', () => {
    expect(numOrUndef(' 3 ')).toBe(3);
    expect(numOrUndef('')).toBe(undefined);
    expect(numOrUndef('x')).toBe(undefined);
  });

  it('builds the development settings with argv commands and zero timeouts by default', () => {
    const s: DevSettings = { maxParallel: 3, autoSchedule: true, respond: true, e2e: { enabled: true }, preview: { enabled: false, url: 'http://x', timeoutSec: 9 } };
    expect(devBody(s, 'npx\nplaywright\ntest', '')).toEqual({
      maxParallel: 3,
      autoSchedule: true,
      respond: true,
      e2e: { enabled: true, command: ['npx', 'playwright', 'test'], timeoutSec: 0 },
      preview: { enabled: false, url: 'http://x', timeoutSec: 9, command: [] },
    });
  });

  it('warns above five parallel agents only', () => {
    expect(parallelWarn(5)).toBe(false);
    expect(parallelWarn(6)).toBe(true);
  });

  it('flags a context ceiling outside the bounds, never an empty one', () => {
    expect(ceilingOff(undefined)).toBe(false);
    expect(ceilingOff(0)).toBe(false);
    expect(ceilingOff(4000)).toBe(false);
    expect(ceilingOff(200000)).toBe(false);
    expect(ceilingOff(3999)).toBe(true);
    expect(ceilingOff(200001)).toBe(true);
  });
});

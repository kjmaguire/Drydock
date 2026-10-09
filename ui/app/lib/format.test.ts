import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { formatBytes } from './format.ts';

describe('formatBytes', () => {
  it('rounds before choosing the unit, so a unit boundary never prints 1024', () => {
    expect(formatBytes(1_048_500)).toBe('1.0 MB');
    expect(formatBytes(1023)).toBe('1023 B');
    expect(formatBytes(1536)).toBe('1.5 KB');
    expect(formatBytes(10 * 1024)).toBe('10 KB');
    expect(formatBytes(1024 * 1024 - 1)).toBe('1.0 MB');
    expect(formatBytes(5 * 1024 ** 3)).toBe('5.0 GB');
    expect(formatBytes(undefined)).toBe('unknown');
  });
});

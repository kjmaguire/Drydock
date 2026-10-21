import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { boxLines, ENV_NAME_RULE, isEnvName } from './envname.ts';

describe('environment variable names (L38)', () => {
  it('takes what the core takes: letters, digits and _, at most 64, not starting with a digit', () => {
    const names = ['A', 'a', '_', 'AWS_PROFILE', 'CLAUDE_CODE_USE_BEDROCK', '_private1', 'x'.repeat(64)];
    expect(names.filter((x) => !isEnvName(x))).toEqual([]);
    const notNames = ['', '1ABC', 'A-B', 'A B', 'A.B', 'A=b', 'AWS_SECRET_ACCESS_KEY=abc123', 'ÄB', 'x'.repeat(65), ' A', 'A ', 'A\n'];
    expect(notNames.filter((x) => isEnvName(x))).toEqual([]);
  });

  it('numbers the lines as the box shows them, blank ones included, and drops a carriage return', () => {
    expect(boxLines('A\n\nB')).toEqual([
      { n: 1, text: 'A' },
      { n: 2, text: '' },
      { n: 3, text: 'B' },
    ]);
    expect(boxLines('A\r\nB\r\n')).toEqual([
      { n: 1, text: 'A' },
      { n: 2, text: 'B' },
      { n: 3, text: '' },
    ]);
    expect(boxLines('')).toEqual([{ n: 1, text: '' }]);
  });

  it('states the rule in words', () => {
    expect(ENV_NAME_RULE).toBe('A name has letters, digits and _ only, at most 64, and does not start with a digit.');
  });
});

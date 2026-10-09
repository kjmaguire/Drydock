import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { coverageText, listOr, scanText, skillTone } from './skills.ts';

describe('Skills helpers', () => {
  it('reports a scan with counts and the problems verbatim', () => {
    expect(scanText({ found: 2, accepted: 0, offered: 1, blocked: 1, removed: 0, errors: null })).toBe('Found 2: 1 offered, 0 accepted, 1 blocked, 0 removed.');
    expect(scanText({ found: 0, accepted: 0, offered: 0, blocked: 0, removed: 0, errors: ['bad yaml', 'too big'] })).toContain('Problems: bad yaml; too big');
  });

  it('tones, coverage and lists', () => {
    expect(skillTone('accepted')).toBe('ok');
    expect(skillTone('offered')).toBe('warn');
    expect(skillTone('blocked')).toBe('err');
    expect(skillTone('rejected')).toBe('muted');
    expect(coverageText({ files: 2, tool: 1 })).toBe('2 delivered as rule files, 1 through a tool.');
    expect(coverageText(undefined)).toBe('');
    expect(listOr(null)).toBe('all');
    expect(listOr(['a', 'b'])).toBe('a, b');
  });
});

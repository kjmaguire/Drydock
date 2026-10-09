import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { kindLabel, MEMORY_KINDS, memoryMeta, memoryTone } from './memory.ts';

describe('Memory helpers', () => {
  it('says a proposal is invisible to agents until accepted', () => {
    expect(memoryMeta({ source: 'agt_1', status: 'proposed' }, 'today')).toBe('Proposed by agt_1. Updated today. Agents do not see it until you accept it.');
    expect(memoryMeta({ status: 'accepted' }, 'today')).toBe('Updated today.');
  });

  it('colors status and prints kinds as words', () => {
    expect(memoryTone('accepted')).toBe('ok');
    expect(memoryTone('proposed')).toBe('warn');
    expect(memoryTone('rejected')).toBe('muted');
    expect(kindLabel('api_contract')).toBe('api contract');
    expect(MEMORY_KINDS).toContain('convention');
  });
});

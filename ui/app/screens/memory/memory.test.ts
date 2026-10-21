import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { bodyNote, charCount, kindLabel, MEMORY_BODY_LIMIT, MEMORY_KINDS, MEMORY_TITLE_LIMIT, memoryMeta, memorySource, memoryTone } from './memory.ts';

describe('Memory helpers', () => {
  it('says a proposal is invisible to agents until accepted', () => {
    expect(memoryMeta({ source: 'agt_1', status: 'proposed' }, 'today')).toBe('Proposed by agt_1. Updated today. Agents do not see it until you accept it.');
    expect(memoryMeta({ status: 'accepted' }, 'today')).toBe('Updated today.');
  });

  it('says in words where a proposal came from (P92)', () => {
    const name = (id: string) => (id === 'agt_1' ? 'Backend #1' : 'an agent');
    const task = (id: string) => (id === 'task_3' ? '#3 Implement Subtract' : 'a task');
    expect(memorySource('agent:agt_1 task:task_3', name, task)).toBe('Backend #1, working on #3 Implement Subtract');
    expect(memorySource('agent:agt_9', name, task)).toBe('an agent');
    expect(memorySource('review:task_3', name, task)).toBe('your change request on #3 Implement Subtract');
    expect(memorySource('edit:AGENTS.md', name, task)).toBe('your edit of AGENTS.md');
    expect(memorySource('human:kyle', name, task)).toBe('human:kyle');
    expect(memorySource('', name, task)).toBe('');
    expect(memoryMeta({ source: 'review:task_3', status: 'proposed' }, 'today', memorySource('review:task_3', name, task))).toBe(
      'Proposed by your change request on #3 Implement Subtract. Updated today. Agents do not see it until you accept it.',
    );
  });

  it('colors status and prints kinds as words', () => {
    expect(memoryTone('accepted')).toBe('ok');
    expect(memoryTone('proposed')).toBe('warn');
    expect(memoryTone('rejected')).toBe('muted');
    expect(kindLabel('api_contract')).toBe('api contract');
    expect(MEMORY_KINDS).toContain('convention');
  });

  it('has the limits the core puts on a proposal, and counts characters as it does (M26)', () => {
    expect(MEMORY_TITLE_LIMIT).toBe(200);
    expect(MEMORY_BODY_LIMIT).toBe(8000);
    expect(charCount('')).toBe(0);
    expect(charCount('abc')).toBe(3);
    expect(charCount('é')).toBe(1);
    expect(charCount('😀')).toBe(1); // two UTF-16 units, one character
    expect(charCount('a😀b')).toBe(3);
    expect(charCount('\ud83d')).toBe(1); // a lone half of a pair counts once
    expect('x'.repeat(8000)).toHaveLength(8000);
  });

  it('says how long an entry is, and that it is longer than a proposal may be when it is', () => {
    expect(bodyNote('one')).toEqual({ count: 3, over: false, text: '3 characters' });
    expect(bodyNote('x')).toEqual({ count: 1, over: false, text: '1 character' });
    expect(bodyNote('x'.repeat(8000)).over).toBe(false);
    const long = bodyNote('x'.repeat(8001));
    expect(long.over).toBe(true);
    expect(long.text).toBe(`${(8001).toLocaleString()} characters. This is longer than the ${(8000).toLocaleString()} characters a proposal may have now. All of it is shown above.`);
  });
});

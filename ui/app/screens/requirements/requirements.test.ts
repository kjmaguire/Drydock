import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { criteriaLines, evidenceOutcome, fileProblem, linkedTaskIds, titleFromFile } from './requirements.ts';

describe('Requirements helpers', () => {
  it('refuses a file over 1 MB before reading it and titles a file by its name', () => {
    expect(fileProblem(1_000_001)).toBe('That file is larger than 1 MB.');
    expect(fileProblem(1_000_000)).toBe('');
    expect(titleFromFile('checkout.md')).toBe('checkout');
    expect(titleFromFile('notes.v2.txt')).toBe('notes.v2');
  });

  it('criteria are one per line, trimmed', () => {
    expect(criteriaLines(' Given a\n\nGiven b \n')).toEqual(['Given a', 'Given b']);
  });

  it('evidence is PASS or FAIL only when the core says so', () => {
    expect(evidenceOutcome({ passed: true }).label).toBe('PASS');
    expect(evidenceOutcome({ passed: false }).label).toBe('FAIL');
    expect(evidenceOutcome({ passed: null }).label).toBe('INFO');
    expect(evidenceOutcome({}).label).toBe('INFO');
  });

  it('lists each linked task once', () => {
    expect(linkedTaskIds([{ taskId: 'a' }, { taskId: 'b' }, { taskId: 'a' }])).toEqual(['a', 'b']);
  });
});

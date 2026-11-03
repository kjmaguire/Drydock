import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { bulkImportLine, hasLabels, importedTask, importLine, labelList } from './pulls.ts';

describe('the issue list (C11)', () => {
  it('filters by every label typed, ignoring case and spaces', () => {
    expect(labelList(' Bug, ,good first issue ')).toEqual(['bug', 'good first issue']);
    expect(hasLabels({ labels: ['bug', 'Good first issue'] }, ['bug', 'good first issue'])).toBe(true);
    expect(hasLabels({ labels: ['bug'] }, ['bug', 'docs'])).toBe(false);
    expect(hasLabels({ labels: null }, [])).toBe(true);
    expect(hasLabels({ labels: null }, ['bug'])).toBe(false);
  });

  it('finds the task an issue was imported as', () => {
    const list = [{ id: 'a', issueRef: '#12' }, { id: 'b', issueRef: 'linear:ENG-1' }, { id: 'c' }];
    expect(importedTask(list, 12)?.id).toBe('a');
    expect(importedTask(list, 1)).toBeUndefined();
  });

  it('says what an import did', () => {
    expect(importLine(12, { seq: 34 }, { worker: 'Ada', reviewer: 'Rex' })).toBe('Issue #12 is task #34. Ada builds it and Rex reviews it; it has started.');
    expect(importLine(12, { seq: 34, startError: 'no licence' }, { worker: 'Ada', reviewer: 'Rex' })).toBe('Issue #12 is task #34, but it did not start: no licence');
    expect(importLine(12, { seq: 34 }, undefined)).toBe('Issue #12 is task #34 in the backlog.');
  });

  it('says what a bulk import did, and each issue it could not import', () => {
    expect(bulkImportLine(1, [])).toBe('Imported 1 issue as backlog tasks.');
    expect(bulkImportLine(2, [{ n: 5, why: 'high injection risk' }, { n: 7, why: 'not open.' }])).toBe('Imported 2 issues as backlog tasks. #5 was not imported: high injection risk. #7 was not imported: not open.');
  });
});

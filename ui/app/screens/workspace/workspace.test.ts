import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Project } from '../../api/types.ts';
import { needsText, projectWhere } from './workspace.ts';

describe('the workspace Home text', () => {
  it("says where a project's work happens", () => {
    expect(projectWhere({ id: 'p', name: 'a', repoPath: 'C:\\code\\a', hasRepo: true } as Project)).toBe('C:\\code\\a');
    expect(projectWhere({ id: 'p', name: 'a', repoPath: '', hasRepo: false } as Project)).toBe('No repository yet');
  });

  it('says what needs the human, in words, and marks a count above zero', () => {
    expect(needsText(undefined)).toEqual({ text: 'Checking what needs you…', tone: '' });
    expect(needsText(0)).toEqual({ text: 'Nothing needs you', tone: '' });
    expect(needsText(1)).toEqual({ text: '1 item needs you', tone: 'warn' });
    expect(needsText(4)).toEqual({ text: '4 items need you', tone: 'warn' });
  });
});

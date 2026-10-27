import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Project } from '../../api/types.ts';
import { crewsByProject, needsByProject, needsText, projectWhere } from './workspace.ts';

describe('the workspace Home text', () => {
  it("says where a project's work happens", () => {
    expect(projectWhere({ id: 'p', name: 'a', repoPath: 'C:\\code\\a', hasRepo: true } as Project)).toBe('C:\\code\\a');
    expect(projectWhere({ id: 'p', name: 'a', repoPath: '', hasRepo: false } as Project)).toBe('No repository yet');
  });

  it('says what needs the human, in words, and marks a count above zero', () => {
    expect(needsText(undefined)).toEqual({ text: 'Checking what needs you…', tone: '' });
    expect(needsText(null)).toEqual({ text: 'Could not check what needs you', tone: '' });
    expect(needsText(0)).toEqual({ text: 'Nothing needs you', tone: '' });
    expect(needsText(1)).toEqual({ text: '1 item needs you', tone: 'warn' });
    expect(needsText(4)).toEqual({ text: '4 items need you', tone: 'warn' });
  });
});

describe('needsByProject and crewsByProject (one read for every project, review 2026-10-27)', () => {
  const projects = [{ id: 'p1' }, { id: 'p2' }];
  it('counts what needs the person by project, leaving out what belongs to none or to a project not listed', () => {
    expect(needsByProject([{ projectId: 'p1' }, { projectId: 'p1' }, { projectId: '' }, {}, { projectId: 'gone' }], false, projects)).toEqual({ p1: 2, p2: 0 });
  });
  it('is undefined while the list loads and null for every project when it could not be read', () => {
    expect(needsByProject(undefined, false, projects)).toEqual({ p1: undefined, p2: undefined });
    expect(needsByProject(undefined, true, projects)).toEqual({ p1: null, p2: null });
  });
  it('groups the agents by project, in order', () => {
    expect(crewsByProject([{ id: 'a', name: 'A', projectId: 'p1' }, { id: 'b', name: 'B', projectId: 'p2' }, { id: 'c', name: 'C', projectId: 'p1' }])).toEqual({
      p1: [{ id: 'a', name: 'A' }, { id: 'c', name: 'C' }],
      p2: [{ id: 'b', name: 'B' }],
    });
  });
});

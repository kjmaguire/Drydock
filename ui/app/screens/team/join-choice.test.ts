import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { JoinRule } from '../../api/roomClient.ts';
import { joinChange, joinChoiceOf, joinWords, normFolder } from './join-choice.ts';

const rule = (over: Partial<JoinRule>): JoinRule => ({ id: 'jr1', projectId: 'p', agentId: 'a1', pathPrefix: '', state: 'observer', createdBy: 'human:kyle', createdAt: 1, ...over });

describe("an agent's join rules as one choice (X17)", () => {
  it('reads the rules: never, every task, every folder', () => {
    expect(joinChoiceOf([], 'a1')).toEqual({ mode: 'never', folders: [], state: 'observer' });
    expect(joinChoiceOf([rule({ agentId: 'a2' }), rule({ id: 'jr2', state: 'active' })], 'a1')).toEqual({ mode: 'every', folders: [], state: 'active' });
    expect(joinChoiceOf([rule({ pathPrefix: 'auth/' })], 'a1')).toEqual({ mode: 'folder', folders: ['auth/'], state: 'observer' });
    expect(joinChoiceOf([rule({ pathPrefix: 'auth/' }), rule({ id: 'jr2', pathPrefix: 'api/' })], 'a1')).toEqual({ mode: 'folder', folders: ['auth/', 'api/'], state: 'observer' });
    expect(joinChoiceOf([rule({ pathPrefix: 'auth/' }), rule({ id: 'jr2', pathPrefix: 'api/', state: 'active' })], 'a1').mixed).toBe(true);
  });

  it('says which rules to remove and add to make a choice so, and nothing when it already is', () => {
    const rules = [rule({ pathPrefix: 'auth/' }), rule({ id: 'jr2', pathPrefix: 'web/' }), rule({ id: 'jr3', agentId: 'a2' })];
    expect(joinChange(rules, 'a1', { mode: 'never', folders: [], state: 'observer' })).toEqual({ remove: ['jr1', 'jr2'], add: [] });
    expect(joinChange(rules, 'a1', { mode: 'every', folders: [], state: 'active' })).toEqual({ remove: ['jr1', 'jr2'], add: [{ pathPrefix: '', state: 'active' }] });
    expect(joinChange(rules, 'a1', { mode: 'folder', folders: ['auth/', 'web/', ' .\\cmd\\drydock '], state: 'observer' })).toEqual({ remove: [], add: [{ pathPrefix: 'cmd/drydock/', state: 'observer' }] });
    expect(joinChange([rule({ pathPrefix: 'auth/' })], 'a1', { mode: 'folder', folders: ['auth'], state: 'observer' })).toEqual({ remove: [], add: [] });
  });

  it('keeps the other folder rules when only Joins as changes, or one folder is removed (two folder rules)', () => {
    const rules = [rule({ pathPrefix: 'auth/' }), rule({ id: 'jr2', pathPrefix: 'api/' })];
    const c = joinChoiceOf(rules, 'a1');
    // Joins as: active replaces both rules with active ones, no folder lost
    expect(joinChange(rules, 'a1', { ...c, state: 'active' })).toEqual({
      remove: ['jr1', 'jr2'],
      add: [
        { pathPrefix: 'auth/', state: 'active' },
        { pathPrefix: 'api/', state: 'active' },
      ],
    });
    // removing auth/ removes only that rule
    expect(joinChange(rules, 'a1', { ...c, folders: ['api/'] })).toEqual({ remove: ['jr1'], add: [] });
  });

  it('writes folders as the core keeps them and every rule in words', () => {
    expect(normFolder('/auth//')).toBe('auth/');
    expect(normFolder('  ')).toBe('');
    expect(joinWords('Sec', [rule({ pathPrefix: 'auth/' })], 'a1')).toBe('Sec joins the rooms of tasks that touch auth/ as an observer');
    expect(joinWords('Sec', [rule({ pathPrefix: 'auth/' }), rule({ id: 'jr2', pathPrefix: 'api/' })], 'a1')).toBe('Sec joins the rooms of tasks that touch auth/ or api/ as an observer');
    expect(joinWords('Sec', [rule({ pathPrefix: 'auth/' }), rule({ id: 'jr2', pathPrefix: 'api/', state: 'active' })], 'a1')).toBe(
      'Sec joins the rooms of tasks that touch auth/ as an observer, and api/ as an active member',
    );
    expect(joinWords('Sec', [rule({ state: 'active' })], 'a1')).toBe('Sec joins every task room as an active member');
    expect(joinWords('Sec', [], 'a1')).toBe('Sec joins no task rooms by rule');
  });
});

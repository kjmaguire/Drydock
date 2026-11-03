import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { JoinRule } from '../../api/roomClient.ts';
import { joinChange, joinChoiceOf, joinWords, normFolder } from './join-choice.ts';

const rule = (over: Partial<JoinRule>): JoinRule => ({ id: 'jr1', projectId: 'p', agentId: 'a1', pathPrefix: '', state: 'observer', createdBy: 'human:kyle', createdAt: 1, ...over });

describe("an agent's join rule as one choice (X17)", () => {
  it('reads the rules: never, every task, a folder', () => {
    expect(joinChoiceOf([], 'a1')).toEqual({ mode: 'never', folder: '', state: 'observer' });
    expect(joinChoiceOf([rule({ agentId: 'a2' }), rule({ id: 'jr2', state: 'active' })], 'a1')).toEqual({ mode: 'every', folder: '', state: 'active' });
    expect(joinChoiceOf([rule({ pathPrefix: 'auth/' })], 'a1')).toEqual({ mode: 'folder', folder: 'auth/', state: 'observer' });
  });

  it('says which rules to remove and add to make a choice so, and nothing when it already is', () => {
    const rules = [rule({ pathPrefix: 'auth/' }), rule({ id: 'jr2', pathPrefix: 'web/' }), rule({ id: 'jr3', agentId: 'a2' })];
    expect(joinChange(rules, 'a1', { mode: 'never', folder: '', state: 'observer' })).toEqual({ remove: ['jr1', 'jr2'] });
    expect(joinChange(rules, 'a1', { mode: 'every', folder: '', state: 'active' })).toEqual({ remove: ['jr1', 'jr2'], add: { pathPrefix: '', state: 'active' } });
    expect(joinChange(rules, 'a1', { mode: 'folder', folder: ' .\\cmd\\drydock ', state: 'observer' })).toEqual({ remove: ['jr1', 'jr2'], add: { pathPrefix: 'cmd/drydock/', state: 'observer' } });
    expect(joinChange([rule({ pathPrefix: 'auth/' })], 'a1', { mode: 'folder', folder: 'auth', state: 'observer' })).toEqual({ remove: [] });
  });

  it('writes folders as the core keeps them and the choice in words', () => {
    expect(normFolder('/auth//')).toBe('auth/');
    expect(normFolder('  ')).toBe('');
    expect(joinWords('Sec', { mode: 'folder', folder: 'auth/', state: 'observer' })).toBe('Sec joins the rooms of tasks that touch auth/ as an observer');
    expect(joinWords('Sec', { mode: 'never', folder: '', state: 'observer' })).toBe('Sec joins no task rooms by rule');
  });
});

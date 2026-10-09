import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Plan, ProposedTask } from '../../api/extTypes.ts';
import { closure, deselect, editsFor, lines, newestFirst, stepStates, toggle } from './plan.ts';

const pt = (key: string, deps: string[] = []): ProposedTask => ({ key, title: `Task ${key}`, description: `Do ${key}`, role: 'backend', dependsOn: deps, acceptance: [] });
const tasks = [pt('a'), pt('b', ['a']), pt('c', ['b']), pt('d')];

describe('PlanTeam selection', () => {
  it('is dependency closed in both directions', () => {
    expect([...closure(['c'], tasks)].sort()).toEqual(['a', 'b', 'c']);
    expect([...closure(['d'], tasks)]).toEqual(['d']);
    expect([...deselect(new Set(['a', 'b', 'c', 'd']), 'a', tasks)]).toEqual(['d']);
    expect([...deselect(new Set(['a', 'b', 'c']), 'c', tasks)].sort()).toEqual(['a', 'b']);
    expect([...closure(['zzz'], tasks)]).toEqual([]);
  });

  it('toggle selects what a task needs and clears what needs it', () => {
    expect([...toggle(new Set(['d']), 'c', true, tasks)].sort()).toEqual(['a', 'b', 'c', 'd']);
    expect([...toggle(new Set(['a', 'b', 'c']), 'b', false, tasks)]).toEqual(['a']);
  });

  it('sends only the edits to selected tasks', () => {
    const edits = { a: { ...pt('a'), title: 'A2' }, d: { ...pt('d'), title: 'D2' } };
    expect(Object.keys(editsFor(new Set(['a', 'b']), edits))).toEqual(['a']);
  });

  it('splits criteria by line and drops blanks', () => {
    expect(lines(' one \n\n two\n')).toEqual(['one', 'two']);
  });
});

describe('PlanTeam flow', () => {
  const plan = (status: string, createdAt = 1): Plan => ({ id: `p${createdAt}`, projectId: 'prj_1', runId: 'r', goal: 'g', status, plannerAgent: '', createdAt });
  it('maps each plan status to the goal, proposal, review, create steps', () => {
    expect(stepStates(undefined)).toEqual(['current', 'todo', 'todo', 'todo']);
    expect(stepStates(plan('PLANNING'))).toEqual(['done', 'working', 'todo', 'todo']);
    expect(stepStates(plan('PROPOSED'))).toEqual(['done', 'done', 'current', 'todo']);
    expect(stepStates(plan('ACCEPTED'))).toEqual(['done', 'done', 'done', 'done']);
    expect(stepStates(plan('REJECTED'))[2]).toBe('failed');
    expect(stepStates(plan('FAILED'))[1]).toBe('failed');
  });

  it('lists the newest plan first', () => {
    expect(newestFirst([plan('A', 1), plan('B', 3), plan('C', 2)]).map((p) => p.createdAt)).toEqual([3, 2, 1]);
  });
});

import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Plan, ProposedTask } from '../../api/extTypes.ts';
import { acceptLabel, andList, assignable, autoPlanner, closure, plansByRole, dependencyWaits, deselect, editsFor, lines, milestoneTitle, newestFirst, nobodyCount, startedText, stepStates, toggle } from './plan.ts';

const pt = (key: string, deps: string[] = []): ProposedTask => ({
  key,
  title: `Task ${key}`,
  description: `Do ${key}`,
  role: 'backend',
  dependsOn: deps,
  acceptance: [],
});
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
    const edits = {
      a: { ...pt('a'), title: 'A2' },
      d: { ...pt('d'), title: 'D2' },
    };
    expect(Object.keys(editsFor(new Set(['a', 'b']), edits))).toEqual(['a']);
  });

  it('splits criteria by line and drops blanks', () => {
    expect(lines(' one \n\n two\n')).toEqual(['one', 'two']);
  });
});

describe('PlanTeam flow', () => {
  const plan = (status: string, createdAt = 1): Plan => ({
    id: `p${createdAt}`,
    projectId: 'prj_1',
    runId: 'r',
    goal: 'g',
    status,
    plannerAgent: '',
    createdAt,
  });
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

describe('who can take a task, and the words around starting', () => {
  const p = (over: Partial<Plan> = {}): Plan =>
    ({ id: 'p', projectId: 'x', runId: 'r', goal: 'g', status: 'PROPOSED', plannerAgent: '', createdAt: 1, proposal: { tasks: [pt('a'), { ...pt('q'), role: 'qa' }] }, ...over }) as Plan;

  it('uses the core answer for the planner role and the crew roles for an edited one', () => {
    const plan = p({ assignable: { a: true, q: false }, crewRoles: ['backend'] });
    expect(assignable(plan, pt('a'))).toBe(true);
    expect(assignable(plan, { ...pt('q'), role: 'qa' })).toBe(false);
    expect(assignable(plan, { ...pt('q'), role: 'backend' })).toBe(true);
    expect(assignable(p(), pt('a'))).toBe(undefined);
    expect(nobodyCount(plan, new Set(['a', 'q']), {})).toBe(1);
    expect(nobodyCount(plan, new Set(['a', 'q']), { q: { ...pt('q'), role: 'backend' } })).toBe(0);
  });

  it('says it in words', () => {
    expect(acceptLabel(1, 0)).toBe('Accept 1 task');
    expect(acceptLabel(5, 2)).toBe('Accept 5 tasks · 2 have nobody');
    expect(acceptLabel(5, 1)).toBe('Accept 5 tasks · 1 has nobody');
    expect(startedText({ promoted: [], started: [], skipped: null, active: 0, capacity: 0, max: 1 })).toBe('Nothing started.');
    expect(startedText({ promoted: [], started: ['a'], skipped: [{ taskId: 'b', reason: 'x' }, { taskId: 'c', reason: 'y' }], active: 1, capacity: 0, max: 1 })).toBe('Started 1 task. 2 tasks wait:');
    expect(startedText({ promoted: [], started: ['a'], skipped: [], active: 1, capacity: 0, max: 1 }, 1)).toBe('Started 1 task. 1 task waits:');
    expect([andList([]), andList(['A']), andList(['A', 'B']), andList(['A', 'B', 'C'])]).toEqual(['', 'A', 'A and B', 'A, B and C']);
    expect(milestoneTitle('First line\nmore')).toBe('First line');
    expect(new TextEncoder().encode(milestoneTitle('é'.repeat(150))).length).toBeLessThanOrEqual(200);
    expect(milestoneTitle('  ')).toBe('Plan');
  });
});

describe('dependencyWaits (X47)', () => {
  const t = (key: string, title: string, dependsOn: string[] = []) => ({ key, title, description: '', role: 'backend', dependsOn, acceptance: [] });
  const p = {
    id: 'p',
    projectId: 'prj',
    runId: 'r',
    goal: 'g',
    status: 'ACCEPTED',
    plannerAgent: '',
    createdAt: 1,
    proposal: { tasks: [t('mul', 'Multiply'), t('div', 'Divide', ['mul']), t('mod', 'Modulo', ['mul', 'div']), t('doc', 'Docs')], notes: '' },
    createdTasks: { mul: 'task_mul', div: 'task_div', mod: 'task_mod', doc: 'task_doc' },
  };

  it("names each planned task the pass did not look at because it waits for the plan's other tasks", () => {
    const tick = { promoted: ['task_mul'], started: ['task_mul'], skipped: [{ taskId: 'task_doc', reason: 'at the limit' }], active: 1, capacity: 1, max: 1 };
    expect(dependencyWaits(p, tick)).toEqual([
      { taskId: 'task_div', title: 'Divide', after: ['Multiply'] },
      { taskId: 'task_mod', title: 'Modulo', after: ['Multiply', 'Divide'] },
    ]);
  });

  it('claims nothing for tasks that started, were skipped with a reason, were not created, or have no dependencies', () => {
    const tick = { promoted: [], started: ['task_mul', 'task_div'], skipped: [{ taskId: 'task_mod', reason: 'x' }], active: 2, capacity: 2, max: 2 };
    expect(dependencyWaits(p, tick)).toEqual([]);
    expect(dependencyWaits({ ...p, createdTasks: { mul: 'task_mul' } }, { ...tick, started: [], skipped: [] })).toEqual([]);
  });
});

describe('who plans (X45)', () => {
  const a = (id: string, role: string) => ({ id, role });
  it('picks as the core does: pm or planner, then architect, then the lead, then anyone', () => {
    expect(autoPlanner([a('b', 'backend'), a('p', 'Planner')])).toEqual({ agent: a('p', 'Planner'), fallback: false });
    expect(autoPlanner([a('x', 'architect'), a('p', 'product_manager')]).agent?.id).toBe('p');
    expect(autoPlanner([a('b', 'backend'), a('x', 'architect')]).agent?.id).toBe('x');
    expect(autoPlanner([a('b', 'backend'), a('l', 'Team Lead')])).toEqual({ agent: a('l', 'Team Lead'), fallback: true });
    expect(autoPlanner([a('b', 'backend'), a('r', 'reviewer')])).toEqual({ agent: a('b', 'backend'), fallback: true });
    expect(autoPlanner([])).toEqual({ agent: undefined, fallback: false });
  });
  it('a planner, pm or architect plans by role; the lead and the rest do not', () => {
    expect([plansByRole('planner'), plansByRole('PM'), plansByRole('software-architect'), plansByRole('team lead'), plansByRole('backend')]).toEqual([true, true, true, false, false]);
  });
});

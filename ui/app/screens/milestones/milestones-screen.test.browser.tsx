import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Milestone, Plan } from '../../api/extTypes.ts';
import type { Task } from '../../api/types.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { MilestonesScreen } from './milestones-screen.tsx';

afterEach(cleanup);

const need = <T,>(x: T | null): T => {
  if (!x) throw new Error('not yet');
  return x;
};

const task = (id: string, seq: number): Task =>
  ({
    id,
    projectId: 'prj_1',
    seq,
    title: `Task ${seq}`,
    description: '',
    status: 'BACKLOG',
    priority: 0,
    iteration: 0,
    maxIterations: 5,
    costUsd: 0,
    created: 0,
    updated: 0,
    version: 1,
  }) as Task;
const ms = (over: Partial<Milestone> = {}): Milestone => ({
  id: 'ms_1',
  projectId: 'prj_1',
  title: 'Beta',
  status: 'open',
  created: '',
  updated: '',
  taskIds: ['t1'],
  overdue: true,
  dueAt: '2020-01-01T00:00:00Z',
  progress: {
    total: 2,
    completed: 1,
    inProgress: 1,
    blocked: 0,
    open: 0,
    cancelled: 0,
    failed: 0,
    percent: 50,
  },
  ...over,
});

function servicesWith(client: Partial<Client>) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as never],
    taskList: async () => [task('t1', 1), task('t2', 2)],
    ...client,
  });
  s.workspace.start();
  s.project.start();
  return s;
}

describe('MilestonesScreen', () => {
  it('milestones: progress, overdue, due date sent as ISO, adding a task', async () => {
    const created: unknown[][] = [];
    const set: unknown[][] = [];
    const r = mount(
      <MilestonesScreen />,
      servicesWith({
        milestoneList: async () => [ms()],
        milestoneCreate: async (...a) => (created.push(a), ms()),
        milestoneSetTasks: async (...a) => (set.push(a), ms()),
      }),
    );
    const card = await waitFor(() => need(r.$('[data-testid="milestone"]')), r);
    expect(card.getAttribute('data-title')).toBe('Beta');
    expect(card.textContent).toContain('overdue');
    expect(card.textContent).toContain('1 of 2 completed');
    expect(card.querySelector('[role="img"]')!.getAttribute('aria-label')).toBe('50% complete');
    await waitFor(() => byText(card, 'a', '#1 Task 1'), r);
    expect(byText(card, 'a', '#1 Task 1').getAttribute('href')).toBe('/task/t1');
    // a task already in the milestone is not offered again
    await r.act(() => byText(card, 'button', 'Add tasks…').click());
    const list = byLabel(card, 'Tasks to add to Beta');
    expect(Array.from(list.querySelectorAll('li')).map((li) => li.textContent)).toEqual(['#2 Task 2']);
    await r.act(() => byLabel<HTMLInputElement>(card, '#2 Task 2').click());
    await r.act(() => byText(card, 'button', 'Add 1 task').click());
    await waitFor(() => expect(set).toEqual([['ms_1', ['t2'], []]]), r);
    type(byLabel(r.container, 'Title'), 'GA');
    type(byLabel(r.container, /Due date/), '2027-03-01');
    r.root.flush();
    await r.act(() => byText(r.container, 'button', 'Create milestone').click());
    await waitFor(() => expect(created).toEqual([['prj_1', 'GA', undefined, '2027-03-01T00:00:00.000Z']]), r);
  });

  it('fills a milestone in one go: a filtered checklist, all tasks of a plan, all tasks with a label, one setTasks (X49)', async () => {
    const set: unknown[][] = [];
    const lab = (id: string, seq: number, labels: string[]) => ({ ...task(id, seq), labels }) as Task;
    const plan = { id: 'plan_1', projectId: 'prj_1', runId: 'r', goal: 'Add arithmetic', status: 'ACCEPTED', plannerAgent: '', createdAt: 1, createdTasks: { a: 't2', b: 't3', c: 't1' } } as Plan;
    const r = mount(
      <MilestonesScreen />,
      servicesWith({
        taskList: async () => [task('t1', 1), lab('t2', 2, ['ui']), lab('t3', 3, []), lab('t4', 4, ['ui']), task('t5', 5)],
        planList: async () => [plan, { ...plan, id: 'plan_2', status: 'REJECTED' }],
        milestoneList: async () => [ms()],
        milestoneSetTasks: async (...a) => (set.push(a), ms()),
      }),
    );
    const card = await waitFor(() => need(r.$('[data-testid="milestone"]')), r);
    await waitFor(() => byText(card, 'a', '#1 Task 1'), r);
    await r.act(() => byText(card, 'button', 'Add tasks…').click());
    // only the plan's tasks not in the milestone yet are counted; a rejected plan offers nothing
    await waitFor(() => byText(card, 'button', 'All tasks of the plan "Add arithmetic" (2)'), r);
    expect(Array.from(card.querySelectorAll('button')).filter((b) => /All tasks of the plan/.test(b.textContent ?? '')).length).toBe(1);
    await r.act(() => byText(card, 'button', 'All tasks of the plan "Add arithmetic" (2)').click());
    await r.act(() => byText(card, 'button', 'All tasks labelled ui (2)').click());
    // the filter narrows the list; ticked tasks stay ticked
    type(byLabel(card, 'Filter tasks for Beta'), '#5');
    r.root.flush();
    expect(Array.from(byLabel(card, 'Tasks to add to Beta').querySelectorAll('li')).map((li) => li.textContent)).toEqual(['#5 Task 5']);
    await r.act(() => byLabel<HTMLInputElement>(card, '#5 Task 5').click());
    await r.act(() => byText(card, 'button', 'Add 4 tasks').click());
    await waitFor(() => expect(set.length).toBe(1), r);
    expect(set[0]![0]).toBe('ms_1');
    expect([...(set[0]![1] as string[])].sort()).toEqual(['t2', 't3', 't4', 't5']);
  });

  it('says the picker holds only the newest tasks while the project has more (M21), and a milestone task beyond them shows by its id', async () => {
    const r = mount(<MilestonesScreen />, servicesWith({ milestoneList: async () => [ms({ taskIds: ['t_old'] })], taskCount: async () => ({ total: 800 }) }));
    const card = await waitFor(() => need(r.$('[data-testid="milestone"]')), r);
    const note = await waitFor(() => need(r.$('[data-testid="task-total-notice"]')), r);
    expect(note.textContent).toContain('Showing the newest 2 of 800 tasks.');
    expect(note.textContent).toContain('Older tasks cannot be added to a milestone from here');
    // the older task is still in the milestone, named by its id, not dropped
    expect(byText(card, 'a', 't_old').getAttribute('href')).toBe('/task/t_old');
    cleanup();
    const all = mount(<MilestonesScreen />, servicesWith({ milestoneList: async () => [ms()], taskCount: async () => ({ total: 2 }) }));
    await waitFor(() => need(all.$('[data-testid="milestone"]')), all);
    await waitFor(() => byText(all.container, 'a', '#1 Task 1'), all);
    expect(all.$('[data-testid="task-total-notice"]')).toBeNull();
  });

  it('removes a task, closes the milestone and deletes only after a confirm', async () => {
    const calls: string[] = [];
    const r = mount(
      <MilestonesScreen />,
      servicesWith({
        milestoneList: async () => [ms({ overdue: false, dueAt: '0001-01-01T00:00:00Z' })],
        milestoneSetTasks: async (id, add, remove) => (calls.push(`set ${id} +${add.join(',')} -${remove.join(',')}`), ms()),
        milestoneSetStatus: async (id, status, force) => (calls.push(`status ${id} ${status} ${force}`), ms()),
        milestoneDelete: async (id) => void calls.push(`delete ${id}`),
      }),
    );
    const card = await waitFor(() => need(r.$('[data-testid="milestone"]')), r);
    expect(card.textContent).toContain('No due date');
    expect(card.textContent).not.toContain('overdue');
    await r.act(() => byText(card, 'button', 'Remove').click());
    await waitFor(() => expect(calls).toEqual(['set ms_1 + -t1']), r);
    await r.act(() => byText(r.$('[data-testid="milestone"]')!, 'button', 'Close milestone').click());
    await waitFor(() => expect(calls[1]).toBe('status ms_1 closed false'), r);
    await r.act(() => byText(r.$('[data-testid="milestone"]')!, 'button', 'Delete').click());
    expect(calls.length).toBe(2);
    await r.act(() => byText(r.$('[data-testid="milestone"]')!, 'button', 'Yes, delete').click());
    await waitFor(() => expect(calls[2]).toBe('delete ms_1'), r);
  });

  // audit 2026-10-16, M27: a task the team lead split off says so wherever it is a row
  it('marks a task the team lead split off in the milestone, and no other', async () => {
    const r = mount(
      <MilestonesScreen />,
      servicesWith({
        milestoneList: async () => [ms({ taskIds: ['t1', 't2', 't3'] })],
        taskList: async () => [{ ...task('t1', 1), origin: 'lead:2' }, task('t2', 2), { ...task('t3', 3), origin: 'nonsense' } as Task],
      }),
    );
    const items = await waitFor(() => {
      const x = Array.from(r.$$('ul[aria-label="Tasks in Beta"] li'));
      if (x.length !== 3) throw new Error('not yet');
      return x;
    }, r);
    expect(items[0]!.querySelector('[data-testid="task-origin"]')!.textContent).toBe('Split off by the team lead');
    expect(items[1]!.querySelector('[data-testid="task-origin"]')).toBeNull();
    expect(items[2]!.querySelector('[data-testid="task-origin"]')).toBeNull();
  });

  it('shows the refusal when a milestone is not created', async () => {
    const r = mount(
      <MilestonesScreen />,
      servicesWith({
        milestoneList: async () => [],
        milestoneCreate: async () => {
          throw new Error('title is required');
        },
      }),
    );
    await waitFor(() => byText(r.container, 'h2', 'No milestones.'), r);
    type(byLabel(r.container, 'Title'), 'X');
    r.root.flush();
    await r.act(() => byText(r.container, 'button', 'Create milestone').click());
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('Not created: title is required'), r);
  });
});

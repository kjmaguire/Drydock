import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Milestone } from '../../api/extTypes.ts';
import type { Task } from '../../api/types.ts';
import { byLabel, byText, cleanup, mount, optionsOf, testServices, type, waitFor } from '../../test/render.tsx';
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
    expect(optionsOf(byLabel(card, 'Add a task to Beta')).map((o) => o.value)).toEqual(['', 't2']);
    type(byLabel<HTMLElement>(card, 'Add a task to Beta'), 't2');
    r.root.flush();
    await r.act(() => byText(card, 'button', 'Add').click());
    await waitFor(() => expect(set).toEqual([['ms_1', ['t2'], []]]), r);
    type(byLabel(r.container, 'Title'), 'GA');
    type(byLabel(r.container, /Due date/), '2027-03-01');
    r.root.flush();
    await r.act(() => byText(r.container, 'button', 'Create milestone').click());
    await waitFor(() => expect(created).toEqual([['prj_1', 'GA', undefined, '2027-03-01T00:00:00.000Z']]), r);
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

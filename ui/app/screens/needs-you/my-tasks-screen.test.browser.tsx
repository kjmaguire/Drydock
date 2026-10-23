import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Task } from '../../api/types.ts';
import { cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { MyTasksScreen, myTaskOrder } from './my-tasks-screen.tsx';

afterEach(cleanup);

// TEST DOUBLES of the core's answers
const task = (over: Partial<Task>): Task => ({ id: 't', projectId: 'p1', seq: 1, title: 't', status: 'IN_PROGRESS', created: 0, updated: 0, ...over }) as Task;
const mine = [task({ id: 't1', seq: 41, title: 'Write the release notes', status: 'COMPLETED', completed: Date.now() - 3_600_000 }), task({ id: 't2', projectId: 'p2', seq: 12, title: 'Check the invoice totals', status: 'IN_PROGRESS' })];

function servicesWith(list: Task[]) {
  const calls: unknown[][] = [];
  const s = testServices({
    projectList: async () => [{ id: 'p1', name: 'gocalc', key: 'GOC' }, { id: 'p2', name: 'shop-api', key: 'SHP' }] as never,
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({ name: 'Drydock', user: 'kyle', human: 'human:kyle', dataDir: '' }),
    taskList: async (...a) => {
      calls.push(a);
      return list;
    },
  });
  s.workspace.start();
  return { s, calls };
}

describe('My tasks', () => {
  it("lists the tasks assigned to the person in every project, the open ones first, each opening in its own project", async () => {
    const { s, calls } = servicesWith(mine);
    const r = mount(<MyTasksScreen />, s);
    await waitFor(() => expect(r.$$('[data-testid="my-task"]').length).toBe(2), r);
    expect(calls.filter((c) => c[0] === '')).toEqual([['', [], { assignee: 'human:kyle' }]]);
    expect(r.$('h1')!.textContent).toContain('My tasks');
    const rows = Array.from(r.$$('[data-testid="my-task"]'));
    expect(rows.map((x) => x.querySelector('.title')!.textContent)).toEqual(['SHP-12Check the invoice totals', 'GOC-41Write the release notes']);
    expect(rows[1]!.querySelector('.sub')!.textContent).toBe('gocalc · Completed · done 1 h ago');
    expect(rows[0]!.querySelector('a')!.getAttribute('href')).toBe('/task/t2');
    await waitFor(() => expect(s.workspace.projectId).toBe('p1'), r);
    const a = rows[0]!.querySelector('a')!;
    a.addEventListener('click', (e) => e.preventDefault());
    await r.act(() => a.click());
    expect(s.workspace.projectId).toBe('p2');
  });

  it('says so when nothing is assigned to the person', async () => {
    const { s } = servicesWith([]);
    const r = mount(<MyTasksScreen />, s);
    await waitFor(() => expect(r.container.textContent).toContain('No task is assigned to you'), r);
  });

  it('puts the open tasks first and keeps the order the core lists them in', () => {
    const list = [task({ id: 'a', status: 'COMPLETED' }), task({ id: 'b', status: 'BACKLOG' }), task({ id: 'c', status: 'FAILED' }), task({ id: 'd', status: 'REVIEW' })];
    expect(myTaskOrder(list).map((x) => x.id)).toEqual(['b', 'd', 'a', 'c']);
  });
});

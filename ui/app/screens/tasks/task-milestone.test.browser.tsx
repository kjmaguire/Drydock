import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Milestone } from '../../api/extTypes.ts';
import { byLabel, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { TaskMilestones } from './task-milestone.tsx';

afterEach(cleanup);

const NOW = Date.parse('2026-11-03T12:00:00Z');
const ms = (id: string, title: string, over: Partial<Milestone> = {}): Milestone => ({
  id,
  projectId: 'prj_1',
  title,
  dueAt: '2026-11-06T12:00:00Z',
  status: 'open',
  created: '',
  updated: '',
  taskIds: [],
  progress: { total: 5, completed: 3, inProgress: 0, blocked: 0, open: 2, cancelled: 0, failed: 0, percent: 60 },
  overdue: false,
  ...over,
});

describe('the task page Milestone row (X411)', () => {
  it("names the task's milestones with their due date and progress, red when overdue; adds to and takes out of one", async () => {
    const calls: unknown[][] = [];
    let list = [ms('m1', 'Beta', { taskIds: ['t1'] }), ms('m2', 'Launch', { taskIds: ['t1'], dueAt: '2026-11-01T12:00:00Z', overdue: true, progress: { ...ms('x', '').progress, percent: 40 } }), ms('m3', 'GA'), ms('m4', 'Old', { status: 'closed' })];
    const r = mount(
      <TaskMilestones taskId="t1" projectId="prj_1" now={() => NOW} />,
      testServices({
        milestoneList: async () => list,
        milestoneSetTasks: async (...a) => (calls.push(a), (list = list.map((m) => (m.id === a[0] ? { ...m, taskIds: [...(m.taskIds ?? []), ...a[1]].filter((x) => !a[2].includes(x)) } : m))), list[0]!),
      }),
    );
    await waitFor(() => expect(r.$$('[data-testid="task-milestone"]').length).toBe(2), r);
    const chips = Array.from(r.$$('[data-testid="task-milestone"]'));
    expect(chips.map((c) => [c.querySelector('a')!.textContent, c.getAttribute('data-overdue')])).toEqual([
      ['Beta · due in 3 days · 60%', null],
      ['Launch · overdue by 2 days · 40%', 'true'],
    ]);
    expect(chips[0]!.querySelector('a')!.getAttribute('href')).toBe('/milestones');
    // only the open milestones the task is not in can be chosen
    type(byLabel<HTMLElement>(r.container, 'Add to a milestone'), 'm3');
    await waitFor(() => expect(calls).toEqual([['m3', ['t1'], []]]), r);
    await waitFor(() => expect(r.$$('[data-testid="task-milestone"]').length).toBe(3), r);
    await r.act(() => byLabel<HTMLButtonElement>(r.container, 'Take this task out of Beta').click());
    await waitFor(() => expect(calls[1]).toEqual(['m1', [], ['t1']]), r);
  });

  it('says none when the task is in no milestone, and offers nothing when there is no open one', async () => {
    const r = mount(<TaskMilestones taskId="t1" projectId="prj_1" now={() => NOW} />, testServices({ milestoneList: async () => [ms('m4', 'Old', { status: 'closed' })] }));
    await waitFor(() => expect(r.$('[data-testid="task-milestones"]')!.textContent).toBe('none'), r);
  });

  it('a failed read says so quietly, never as the panel alert', async () => {
    const r = mount(<TaskMilestones taskId="t1" projectId="prj_1" now={() => NOW} />, testServices({ milestoneList: async () => Promise.reject(new Error('core down')) }));
    await waitFor(() => expect(r.$('[data-testid="task-milestones-unread"]')!.textContent).toBe('Milestones could not be read.'), r);
    expect(r.$('[role="alert"]')).toBe(null);
  });
});

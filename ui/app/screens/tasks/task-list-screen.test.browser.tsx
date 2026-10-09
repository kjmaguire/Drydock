import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Task } from '../../api/types.ts';
import { expiredLicense } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, type, waitFor } from '../../test/render.tsx';
import { SchedulerPanel } from './scheduler-panel.tsx';
import { TaskCreateScreen } from './task-create-screen.tsx';
import { TaskListScreen } from './task-list-screen.tsx';
import { recorder, svc, task } from './testing.ts';

afterEach(() => {
  cleanup();
  try {
    localStorage.removeItem('drydock.tasks.filter');
  } catch {
    /* ignore */
  }
});

const status = { settings: { maxParallel: 2, autoSchedule: false, respond: true, e2e: { enabled: false }, preview: { enabled: false } }, activeRuns: 0, agentSlotsInUse: 0, peakAgentSlots: 1 } as never;

const tasks: Task[] = [
  task({ id: 't1', seq: 1, title: 'Add Multiply', status: 'VERIFICATION', updated: Date.now() - 120_000, costUsd: 0.25 }),
  task({ id: 't2', seq: 2, title: 'Add Divide', status: 'BACKLOG', assignee: undefined, reviewer: undefined }),
  task({ id: 't3', seq: 3, title: 'Fix rounding', status: 'COMPLETED', branch: 'drydock/t3' }),
];

const rows = (r: ReturnType<typeof mount>) => Array.from(r.$$('[data-testid="task-row"]'));

async function listed(r: ReturnType<typeof mount>, n: number) {
  await waitFor(() => {
    if (rows(r).length !== n) throw new Error(`have ${rows(r).length}`);
  }, r);
}

describe('TaskListScreen', () => {
  it('lists tasks grouped by status, each row a link with the status, people and cost', async () => {
    const r = mount(<TaskListScreen />, svc({ schedulerStatus: async () => status }, { tasks }));
    await listed(r, 3);
    const [first, second, third] = rows(r);
    // waiting on you first, then the backlog, then done
    expect(first!.textContent).toContain('Add Multiply');
    expect(second!.textContent).toContain('Add Divide');
    expect(third!.textContent).toContain('Fix rounding');
    expect(first!.querySelector('a')!.getAttribute('href')).toBe('/task/t1');
    expect(first!.querySelector('a')!.textContent).toBe('Add Multiply');
    expect(first!.querySelector('[data-testid="task-status"]')!.textContent).toContain('Awaiting your approval');
    expect(first!.textContent).toContain('#1');
    expect(first!.textContent).toContain('$0.25');
    expect(first!.textContent).toContain('2m');
    await waitFor(() => expect(first!.textContent).toContain('Worker: Dana. Reviewer: Rex.'), r);
    expect(second!.textContent).toContain('Worker: unassigned. Reviewer: unassigned.');
    expect(second!.textContent).toContain('cost not reported');
    expect(third!.textContent).toContain('drydock/t3');
    expect(byText(r.container, 'a', 'New task').getAttribute('href')).toBe('/tasks/new');
    expect(r.$('h1')!.textContent).toContain('Tasks');
  });

  it('filters by status and by text', async () => {
    const r = mount(<TaskListScreen />, svc({ schedulerStatus: async () => status }, { tasks }));
    await listed(r, 3);
    await r.act(() => (r.$('[data-testid="task-filter-waiting"]') as HTMLButtonElement).click());
    await listed(r, 1);
    expect(rows(r)[0]!.textContent).toContain('Add Multiply');
    await r.act(() => (r.$('[data-testid="task-filter-done"]') as HTMLButtonElement).click());
    await listed(r, 1);
    expect(rows(r)[0]!.textContent).toContain('Fix rounding');
    await r.act(() => (r.$('[data-testid="task-filter-all"]') as HTMLButtonElement).click());
    await listed(r, 3);
    await r.act(() => type(byLabel(r.container, 'Filter tasks'), 'divide'));
    await listed(r, 1);
    await r.act(() => type(byLabel(r.container, 'Filter tasks'), 'nothing like this'));
    await listed(r, 0);
    expect(r.container.textContent).toContain('No tasks match');
  });

  it('explains what a task is when there are none, and still shows the scheduler', async () => {
    const r = mount(<TaskListScreen />, svc({ schedulerStatus: async () => status }));
    await waitFor(() => expect(r.container.textContent).toContain('No tasks yet'), r);
    expect(r.container.textContent).toContain('assign a worker and a different reviewer');
    await waitFor(() => expect(r.$('[data-testid="scheduler"]')!.textContent).toContain('manual'), r);
  });

  it('re-reads when a task event arrives', async () => {
    let list: Task[] = [];
    const s = svc({ schedulerStatus: async () => status, taskList: async () => list });
    const r = mount(<TaskListScreen />, s);
    await waitFor(() => expect(r.container.textContent).toContain('No tasks yet'), r);
    list = [tasks[0]!];
    s.emit({ type: 'task.created', projectId: 'prj_1', taskId: 't1' });
    await listed(r, 1);
  });

  it('shows the core error when tasks cannot be read', async () => {
    const r = mount(<TaskListScreen />, svc({ schedulerStatus: async () => status, taskList: async () => Promise.reject(new Error('database is locked')) }));
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('database is locked'), r);
  });
});

describe('Scheduler', () => {
  it('shows the scheduler and what one tick did, including why tasks were skipped', async () => {
    const schedulerTick = recorder(() => ({ promoted: ['t1'], started: ['t1'], skipped: [{ taskId: 't2', reason: 'the worker is busy' }], active: 1, capacity: 1, max: 2 }));
    const r = mount(<SchedulerPanel projectId="prj_1" />, svc({ schedulerStatus: async () => status, schedulerTick }));
    await waitFor(() => expect(r.$('[data-testid="scheduler"]')!.textContent).toContain('manual'), r);
    expect(r.$('[data-testid="scheduler"]')!.textContent).toContain('up to 2 task(s) in parallel');
    await r.act(() => byText(r.container, 'button', 'Start ready tasks now').click());
    await waitFor(() => expect(r.$('[data-testid="tick-result"]')).toBeTruthy(), r);
    const out = r.$('[data-testid="tick-result"]')!;
    expect(out.textContent).toContain('Started 1');
    expect(out.textContent).toContain('the worker is busy');
    expect(out.querySelector('a')!.getAttribute('href')).toBe('/task/t2');
    expect(schedulerTick.calls).toEqual([['prj_1']]);
  });

  it('disables the scheduler tick when read-only and leaves reading alone', async () => {
    const r = mount(<SchedulerPanel projectId="prj_1" />, svc({ schedulerStatus: async () => ({ settings: { autoSchedule: false, maxParallel: 0 }, activeRuns: 0, agentSlotsInUse: 0, peakAgentSlots: 0 }) as never }, { license: expiredLicense }));
    await waitFor(() => expect(r.$('[data-testid="readonly-note"]')).toBeTruthy(), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Start ready tasks now').disabled).toBe(true);
    await waitFor(() => expect(r.$('[data-testid="scheduler"]')!.textContent).toContain('the default number of'), r);
  });
});

describe('TaskCreateScreen', () => {
  it('creates the task with trimmed title and one criterion per line, then hands over the new task', async () => {
    const taskCreate = recorder(() => task({ id: 't9' }));
    const created: Task[] = [];
    const r = mount(<TaskCreateScreen onCreated={(x) => created.push(x)} />, svc({ taskCreate }));
    await waitFor(() => expect(byLabel(r.container, 'Task title')).toBeTruthy(), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Create task').disabled).toBe(true);
    await r.act(() => type(byLabel(r.container, 'Task title'), '  Add Multiply  '));
    await r.act(() => type(byLabel<HTMLTextAreaElement>(r.container, 'Description'), 'Add a Multiply function.'));
    await r.act(() => type(byLabel<HTMLTextAreaElement>(r.container, 'Acceptance criteria'), 'Given 3 and 4 When Multiply Then 12\n\n  Given 0 When Multiply Then 0 '));
    await waitFor(() => expect(byText<HTMLButtonElement>(r.container, 'button', 'Create task').disabled).toBe(false), r);
    await r.act(() => byText(r.container, 'button', 'Create task').click());
    await waitFor(() => expect(created.map((x) => x.id)).toEqual(['t9']), r);
    expect(taskCreate.calls).toEqual([
      [{ projectId: 'prj_1', title: 'Add Multiply', description: 'Add a Multiply function.', acceptance: ['Given 3 and 4 When Multiply Then 12', 'Given 0 When Multiply Then 0'] }],
    ]);
    expect(byText(r.container, 'a', 'Cancel').getAttribute('href')).toBe('/tasks');
  });

  it('shows the core refusal and stays on the form', async () => {
    const r = mount(<TaskCreateScreen onCreated={() => undefined} />, svc({ taskCreate: async () => Promise.reject(new Error('title is required')) }));
    await waitFor(() => expect(byLabel(r.container, 'Task title')).toBeTruthy(), r);
    await r.act(() => type(byLabel(r.container, 'Task title'), 'x'));
    await waitFor(() => expect(byText<HTMLButtonElement>(r.container, 'button', 'Create task').disabled).toBe(false), r);
    await r.act(() => byText(r.container, 'button', 'Create task').click());
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('Task not created: title is required'), r);
    expect(byLabel<HTMLInputElement>(r.container, 'Task title').value).toBe('x');
  });
});

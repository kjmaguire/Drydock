import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Task } from '../../api/types.ts';
import { expiredLicense } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, drain, mount, type, waitFor } from '../../test/render.tsx';
import { SchedulerPanel } from './scheduler-panel.tsx';
import { TaskCreateScreen } from './task-create-screen.tsx';
import { TaskListScreen } from './task-list-screen.tsx';
import { recorder, svc, task } from './testing.ts';

afterEach(() => {
  cleanup();
  try {
    localStorage.removeItem('drydock.tasks.filter');
    localStorage.removeItem('drydock.tasks.view');
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
const columns = (r: ReturnType<typeof mount>) => Array.from(r.$$('[data-testid="board-column"]'));
const pressed = (r: ReturnType<typeof mount>, id: string) => r.$(`[data-testid="${id}"]`)!.getAttribute('aria-pressed');

async function listed(r: ReturnType<typeof mount>, n: number) {
  await waitFor(() => {
    if (rows(r).length !== n) throw new Error(`have ${rows(r).length}`);
  }, r);
}

async function showList(r: ReturnType<typeof mount>) {
  await r.act(() => (r.$('[data-testid="task-view-list"]') as HTMLButtonElement).click());
  await waitFor(() => expect(pressed(r, 'task-view-list')).toBe('true'), r);
}

describe('TaskListScreen: the board', () => {
  it('is the default view: four columns, each card a link with the number, status, crew dots, cost and age', async () => {
    const r = mount(<TaskListScreen />, svc({ schedulerStatus: async () => status }, { tasks }));
    await listed(r, 3);
    expect(pressed(r, 'task-view-board')).toBe('true');
    expect(pressed(r, 'task-view-list')).toBe('false');
    expect(columns(r).map((c) => c.getAttribute('data-column'))).toEqual(['next', 'working', 'needs', 'done']);
    expect(columns(r).map((c) => c.getAttribute('aria-label'))).toEqual(['Up next', 'Crew working', 'Needs you', 'Merged / Done']);
    // Up next holds the backlog, Crew working the task awaiting approval, Merged / Done the completed one
    const [next, working, needs, done] = columns(r);
    expect(next!.querySelector('[data-testid="task-row"]')!.textContent).toContain('Add Divide');
    expect(working!.querySelector('[data-testid="task-row"]')!.textContent).toContain('Add Multiply');
    expect(needs!.querySelectorAll('[data-testid="task-row"]').length).toBe(0);
    expect(needs!.textContent).toContain('Nothing needs you');
    expect(done!.querySelector('[data-testid="task-row"]')!.textContent).toContain('Fix rounding');
    expect(byText(r.container, 'a', /Describe a task/).getAttribute('href')).toBe('/tasks/new');

    const card = working!.querySelector('[data-testid="task-row"]')!;
    expect(card.getAttribute('data-status')).toBe('VERIFICATION');
    expect(card.querySelector('a')!.getAttribute('href')).toBe('/task/t1');
    expect(card.querySelector('a')!.textContent).toBe('Add Multiply');
    expect(card.querySelector('[data-testid="task-status"]')!.textContent).toContain('Awaiting your approval');
    expect(card.textContent).toContain('#1');
    expect(card.textContent).toContain('$0.25');
    expect(card.textContent).toContain('2m');
    await waitFor(() => expect(card.textContent).toContain('Worker: Dana. Reviewer: Rex.'), r);
    expect(card.textContent).toContain('Dana writes · Rex reviews');
    expect(card.querySelectorAll('[data-dot="true"]').length).toBe(2);
    const backlog = next!.querySelector('[data-testid="task-row"]')!;
    expect(backlog.textContent).toContain('Worker: unassigned. Reviewer: unassigned.');
    expect(backlog.textContent).toContain('cost not reported');
    expect(done!.querySelector('[data-testid="task-row"]')!.textContent).toContain('drydock/t3');
    // nothing is approved from the board
    expect(Array.from(r.$$('button')).map((b) => b.textContent?.trim())).not.toContain('Approve');
    expect(byText(r.container, 'a', 'New task').getAttribute('href')).toBe('/tasks/new');
    expect(r.$('h1')!.textContent).toContain('Tasks');
    expect(r.container.textContent).toContain('3 tasks · 1 needs you');
  });

  it('a task waiting for you is highlighted in Needs you with an Open link to its page; so is a task whose run asks for you', async () => {
    const list = [...tasks, task({ id: 't4', seq: 4, title: 'Ship the invoice export', status: 'WAITING_HUMAN' })];
    const needsYouList = async () => [{ key: 'k1', kind: 'question', severity: 2, title: 'Which API?', taskId: 't2', createdAt: '' }] as never;
    const r = mount(<TaskListScreen />, svc({ schedulerStatus: async () => status, needsYouList }, { tasks: list }));
    await listed(r, 4);
    const needs = columns(r)[2]!;
    await waitFor(() => expect(needs.querySelectorAll('[data-testid="task-row"]').length).toBe(2), r);
    const cards = Array.from(needs.querySelectorAll('[data-testid="task-row"]'));
    expect(cards.map((c) => c.getAttribute('data-needs'))).toEqual(['true', 'true']);
    expect(cards.map((c) => c.textContent?.includes('Ship the invoice export') || c.textContent?.includes('Add Divide'))).toEqual([true, true]);
    const open = cards.find((c) => c.textContent?.includes('Ship the invoice export'))!.querySelector('a[aria-label^="Open"]')!;
    expect(open.textContent?.trim()).toBe('Open');
    expect(open.getAttribute('href')).toBe('/task/t4');
    expect(columns(r)[0]!.querySelectorAll('[data-testid="task-row"]').length).toBe(0);
    expect(r.container.textContent).toContain('4 tasks · 3 need you');
  });

  it('switches to the list and back, and remembers the choice', async () => {
    const r = mount(<TaskListScreen />, svc({ schedulerStatus: async () => status }, { tasks }));
    await listed(r, 3);
    await showList(r);
    expect(r.$('[data-testid="task-board"]')).toBeNull();
    expect(r.$('[data-testid="task-filter-waiting"]')).toBeTruthy();
    expect(localStorage.getItem('drydock.tasks.view')).toBe('list');
    await r.act(() => (r.$('[data-testid="task-view-board"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$('[data-testid="task-board"]')).toBeTruthy(), r);
    expect(localStorage.getItem('drydock.tasks.view')).toBe('board');
    localStorage.setItem('drydock.tasks.view', 'list');
    const r2 = mount(<TaskListScreen />, svc({ schedulerStatus: async () => status }, { tasks }));
    await listed(r2, 3);
    expect(pressed(r2, 'task-view-list')).toBe('true');
    expect(r2.$('[data-testid="task-board"]')).toBeNull();
  });

  it('the text filter applies to the board', async () => {
    const r = mount(<TaskListScreen />, svc({ schedulerStatus: async () => status }, { tasks }));
    await listed(r, 3);
    await r.act(() => type(byLabel(r.container, 'Filter tasks'), '#3'));
    await listed(r, 1);
    expect(rows(r)[0]!.textContent).toContain('Fix rounding');
    await r.act(() => type(byLabel(r.container, 'Filter tasks'), 'nothing like this'));
    await listed(r, 0);
    expect(r.container.textContent).toContain('No tasks match');
  });
});

// audit 2026-10-16, M27: a task the team lead split off (origin "lead:N") is marked on its card and in its row; every other task,
// whatever the core sent as its origin, says nothing and draws as before.
describe('TaskListScreen: tasks the team lead split off', () => {
  const BADGE = 'Split off by the team lead';
  const odd: unknown[] = [7, true, {}, ['lead:2'], 'lead:', 'lead:abc', 'lead:-1', 'lead:0', 'lead:1000', 'LEAD:1', ' lead:1', 'lead:1 ', 'user:1', '\u202Elead:1'];
  const mixed: Task[] = [
    task({ id: 'a', seq: 11, title: 'Follow-up from the lead', status: 'BACKLOG', origin: 'lead:2' }),
    task({ id: 'b', seq: 12, title: 'A deeper follow-up', status: 'BACKLOG', origin: 'lead:1' }),
    task({ id: 'c', seq: 13, title: 'Made by you', status: 'BACKLOG' }),
    task({ id: 'd', seq: 14, title: 'Empty origin', status: 'BACKLOG', origin: '' }),
    ...odd.map((origin, i) => task({ id: `o${i}`, seq: 20 + i, title: `Odd origin ${i}`, status: 'BACKLOG', origin: origin as never })),
  ];
  const badges = (root: ParentNode) => Array.from(root.querySelectorAll('[data-testid="task-origin"]')) as HTMLElement[];
  const rowOf = (r: ReturnType<typeof mount>, title: string) => rows(r).find((x) => x.querySelector('a')!.textContent === title)!;

  it('the board marks them on the card, with the step as the tooltip, and nothing else', async () => {
    const r = mount(<TaskListScreen />, svc({ schedulerStatus: async () => status }, { tasks: mixed }));
    await listed(r, mixed.length);
    expect(badges(r.container).map((b) => b.textContent)).toEqual([BADGE, BADGE]);
    const a = badges(rowOf(r, 'Follow-up from the lead'));
    expect(a).toHaveLength(1);
    expect(a[0]!.getAttribute('title')).toBe('Step 2 from a task you made');
    expect(a[0]!.getAttribute('data-tone')).toBe('info');
    expect(badges(rowOf(r, 'A deeper follow-up'))[0]!.getAttribute('title')).toBe('Step 1 from a task you made');
    // the badge sits beside the status, and the card is otherwise as before
    expect(rowOf(r, 'Follow-up from the lead').querySelector('[data-testid="task-status"]')!.textContent).toContain('Backlog');
    for (const title of ['Made by you', 'Empty origin', ...odd.map((_, i) => `Odd origin ${i}`)]) {
      const card = rowOf(r, title);
      expect(card).toBeTruthy();
      expect(badges(card)).toHaveLength(0);
      expect(card.textContent).not.toContain(BADGE);
      expect(card.querySelector('a')!.textContent).toBe(title);
      expect(card.querySelector('[data-testid="task-status"]')).toBeTruthy();
    }
  });

  it('the list marks them in the row, and nothing else', async () => {
    const r = mount(<TaskListScreen />, svc({ schedulerStatus: async () => status }, { tasks: mixed }));
    await listed(r, mixed.length);
    await showList(r);
    await listed(r, mixed.length);
    expect(badges(r.container).map((b) => b.textContent)).toEqual([BADGE, BADGE]);
    const a = rowOf(r, 'Follow-up from the lead');
    expect(badges(a)).toHaveLength(1);
    expect(badges(a)[0]!.getAttribute('title')).toBe('Step 2 from a task you made');
    expect(a.querySelector('a')!.textContent).toBe('Follow-up from the lead');
    expect(badges(rowOf(r, 'Made by you'))).toHaveLength(0);
    for (const i of odd.keys()) expect(badges(rowOf(r, `Odd origin ${i}`))).toHaveLength(0);
  });
});

describe('TaskListScreen: the list', () => {
  it('lists tasks grouped by status, each row a link with the status, people and cost', async () => {
    const r = mount(<TaskListScreen />, svc({ schedulerStatus: async () => status }, { tasks }));
    await listed(r, 3);
    await showList(r);
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
    await showList(r);
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
    expect(r.$('[data-testid="scheduler"]')!.textContent).toContain('up to 2 tasks in parallel');
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

describe('TaskListScreen: a list that is not every task (M21)', () => {
  const notice = (r: ReturnType<typeof mount>) => r.$('[data-testid="task-total-notice"]');

  it('says "Showing the newest N of M tasks" on the board while the project has more, and counts "N of M" in its head', async () => {
    const r = mount(<TaskListScreen />, svc({ schedulerStatus: async () => status, taskCount: async () => ({ total: 612 }) }, { tasks }));
    await listed(r, 3);
    await waitFor(() => expect(notice(r)).not.toBeNull(), r);
    expect(notice(r)!.textContent).toContain('Showing the newest 3 of 612 tasks.');
    expect(notice(r)!.textContent).toContain('Older tasks are not on this board or in this list');
    expect(r.container.textContent).toContain('3 of 612 tasks · 1 needs you');
    expect(r.container.textContent).not.toContain('3 tasks · 1 needs you');
  });

  it('keeps saying it in the list view, and does not go away by itself', async () => {
    let counts = 0;
    const s = svc({ schedulerStatus: async () => status, taskCount: async () => (counts++, { total: 612 }) }, { tasks });
    const r = mount(<TaskListScreen />, s);
    await listed(r, 3);
    await waitFor(() => expect(notice(r)).not.toBeNull(), r);
    await showList(r);
    expect(notice(r)!.textContent).toContain('Showing the newest 3 of 612 tasks.');
    // a task event reads the list and the count again; the notice stays
    const before = counts;
    s.emit({ type: 'task.updated', projectId: 'prj_1' });
    await waitFor(() => expect(counts).toBeGreaterThan(before), r);
    await drain();
    r.root.flush();
    expect(notice(r)!.textContent).toContain('Showing the newest 3 of 612 tasks.');
  });

  it('shows nothing while the list is every task', async () => {
    const r = mount(<TaskListScreen />, svc({ schedulerStatus: async () => status, taskCount: async () => ({ total: 3 }) }, { tasks }));
    await listed(r, 3);
    await waitFor(() => expect(r.container.textContent).toContain('3 tasks · 1 needs you'), r);
    expect(notice(r)).toBeNull();
  });

  it('does not take a list as long as the core sends it for complete when the count cannot be read', async () => {
    const full = Array.from({ length: 500 }, (_, i) => task({ id: `t${i}`, seq: 500 - i, title: `Task ${500 - i}` }));
    const r = mount(
      <TaskListScreen />,
      svc(
        {
          schedulerStatus: async () => status,
          taskCount: async () => {
            throw new Error('the database is busy');
          },
        },
        { tasks: full },
      ),
    );
    await waitFor(() => expect(notice(r)).not.toBeNull(), r);
    expect(notice(r)!.textContent).toContain('Showing the newest 500 tasks; there may be more.');
  });
});

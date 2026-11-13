import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../api/client.ts';
import type { Task } from '../api/types.ts';
import { byText, choose, cleanup, mount, testServices, waitFor } from '../test/render.tsx';
import { Dock } from './dock.tsx';

afterEach(cleanup);

/** The element, or a throw that keeps waitFor waiting. */
const need = (r: { $: (sel: string) => Element | null }, sel: string): HTMLElement => {
  const el = r.$(sel);
  if (!el) throw new Error(`no ${sel}`);
  return el as HTMLElement;
};

const now = Date.now();
const task = (over: Partial<Task>): Task => ({ id: 't1', projectId: 'p1', seq: 1, title: 'Task', description: '', status: 'IN_PROGRESS', priority: 0, iteration: 0, maxIterations: 3, costUsd: 0, created: now, updated: now, version: 1, ...over });

function svc(tasks: Task[], over: Partial<Client> = {}) {
  const logsFor: string[] = [];
  const s = testServices({
    projectList: async () => [{ id: 'p1', name: 'gocalc', key: 'GOC', hasRepo: true }] as never,
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({}) as never,
    agentList: async () => [],
    roomList: async () => [],
    roomUnread: async () => [],
    presenceList: async () => [],
    needsYouList: async () => [],
    taskList: async () => tasks,
    logsList: async (id) => {
      logsFor.push(id);
      return [];
    },
    terminalList: async () => [],
    ...over,
  });
  s.workspace.start();
  s.project.start();
  return Object.assign(s, { logsFor });
}

describe('the dock without a task in context (X29)', () => {
  it("shows the project's one running task at once, under a line naming it", async () => {
    const s = svc([task({ id: 't7', seq: 7, title: 'Add Multiply' }), task({ id: 't8', seq: 8, title: 'Old', status: 'COMPLETED' })]);
    s.ui.setDock({ open: true, tab: 'logs' });
    const r = mount(<Dock projectId="p1" taskId={undefined} />, s);
    const line = await waitFor(() => need(r, '[data-testid="dock-task-line"]'), r);
    expect(line.textContent).toBe('The one running task: GOC-7 Add Multiply');
    await waitFor(() => expect(s.logsFor).toEqual(['t7']), r);
  });

  it('offers the running tasks in a one-line pick when there are several', async () => {
    const s = svc([task({ id: 't7', seq: 7, title: 'Add Multiply' }), task({ id: 't9', seq: 9, title: 'Fix divide', status: 'REVIEW' })]);
    s.ui.setDock({ open: true, tab: 'logs' });
    const r = mount(<Dock projectId="p1" taskId={undefined} />, s);
    const pick = await waitFor(() => need(r, '[data-testid="dock-task-pick"]'), r);
    expect(r.container.textContent).toContain('2 tasks are running:');
    expect(s.logsFor).toEqual([]);
    await r.act(() => choose(pick, 't9'));
    await waitFor(() => expect(s.logsFor).toEqual(['t9']), r);
  });

  it('keeps the note when no task runs', async () => {
    const s = svc([task({ id: 't8', status: 'READY' })]);
    s.ui.setDock({ open: true, tab: 'diff' });
    const r = mount(<Dock projectId="p1" taskId={undefined} />, s);
    await waitFor(() => expect(r.container.textContent).toContain('Open a task (or its room) to see its diff.'), r);
    expect(r.$('[data-testid="dock-task-line"], [data-testid="dock-task-pick"]')).toBe(null);
  });
});

describe('the Terminal tab (G6)', () => {
  it('opens a terminal when the person opens the tab, and closes when it is shown', async () => {
    const opens: unknown[] = [];
    const s = svc([], {
      terminalOpen: async (where) => {
        opens.push(where);
        throw new Error('no shell here');
      },
      usageList: () => new Promise<never>(() => {}),
    });
    // the dock left open on the terminal: drawn again, nothing starts
    s.ui.setDock({ open: true, tab: 'terminal' });
    const r = mount(<Dock projectId="p1" taskId={undefined} />, s);
    await waitFor(() => expect(r.container.textContent).toContain('No terminal is open'), r);
    await r.act(() => byText<HTMLButtonElement>(r.container, '[role="tab"]', 'Activity').click());
    expect(opens).toEqual([]);
    // the person clicks Terminal: one opens in the project's repository
    await r.act(() => byText<HTMLButtonElement>(r.container, '[role="tab"]', 'Terminal').click());
    expect(s.ui.dock).toEqual({ ...s.ui.dock, open: true, tab: 'terminal' });
    await waitFor(() => expect(r.container.textContent).toContain('no shell here'), r);
    expect(opens).toEqual([{ projectId: 'p1', reuse: true }]);
    // clicked again while shown: the dock closes
    await r.act(() => byText<HTMLButtonElement>(r.container, '[role="tab"]', 'Terminal').click());
    expect(s.ui.dock.open).toBe(false);
    expect(opens.length).toBe(1);
  });
});

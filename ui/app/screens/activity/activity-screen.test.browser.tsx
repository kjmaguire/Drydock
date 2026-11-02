import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { DrydockEvent, Project } from '../../api/types.ts';
import { byLabel, cleanup, fromMenu, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { ActivityScreen } from './activity-screen.tsx';

// the filter is remembered per project in localStorage: every test starts from none
const forget = () => {
  for (const k of Object.keys(localStorage)) if (k.startsWith('drydock.activity.')) localStorage.removeItem(k);
};
afterEach(() => {
  cleanup();
  forget();
});

const ev = (seq: number, type: string, extra: Partial<DrydockEvent> = {}): DrydockEvent => ({ seq, time: '2026-01-01T10:00:00Z', type, ...extra });

function setup(client: Partial<Client> = {}) {
  const s = testServices({ projectList: async () => [{ id: 'p1', name: 'gocalc' } as Project], licenseStatus: async () => ({}) as never, appInfo: async () => ({}) as never, ...client });
  s.workspace.start();
  return s;
}

const types = (root: ParentNode) => Array.from(root.querySelectorAll('[data-testid="event-row"]')).map((e) => e.getAttribute('data-type'));

describe('ActivityScreen', () => {
  it('loads earlier events on demand and shows them newest first with live ones', async () => {
    const asked: [number, unknown][] = [];
    const s = setup({
      eventsReplay: async (after, opts) => (asked.push([after, opts]), [ev(1, 'task.created', { projectId: 'p1', taskId: 't1' }), ev(2, 'agent.created', { projectId: 'p1', agentId: 'a1', data: { name: 'Pay' } })]),
    });
    const r = mount(<ActivityScreen />, s);
    await waitFor(() => expect(types(r.container)).toEqual(['agent.created', 'task.created']), r);
    expect(asked).toEqual([[0, { limit: 1000 }]]);
    const row = r.container.querySelector('[data-type="agent.created"]')!;
    expect(row.textContent).toContain('a1');
    expect(row.textContent).toContain('{"name":"Pay"}');
    s.emit({ type: 'task.completed', projectId: 'p1', taskId: 't1' });
    await waitFor(() => expect(types(r.container)[0]).toBe('task.completed'), r);
    // the stream's state is the page's subtitle; what the log is lives in the Guide
    expect(r.container.textContent).toContain('events held');
    expect(r.container.querySelector('a[data-testid="learn-more"]')!.getAttribute('href')).toBe('/guide/events');
  });

  it('shows the start of each payload from the ring, which holds a preview and not the payload (M22)', async () => {
    const big = { diff: 'z'.repeat(300_000) };
    const s = setup({ eventsReplay: async () => [ev(1, 'task.diff', { projectId: 'p1', taskId: 't1', data: big }), ev(2, 'agent.created', { projectId: 'p1', data: { name: 'Pay' } })] });
    const r = mount(<ActivityScreen />, s);
    await waitFor(() => expect(types(r.container)).toEqual(['agent.created', 'task.diff']), r);
    const cell = r.$('[data-type="task.diff"] td.data')!;
    expect(cell.textContent!.startsWith('{"diff":"zzzz')).toBe(true);
    expect(cell.textContent!.length).toBe(200);
    // opening the row shows the stored preview (2 KB at most) and says the rest is not here
    expect(cell.querySelector('button')!.getAttribute('aria-expanded')).toBe('false');
    await r.act(() => (cell.querySelector('button') as HTMLButtonElement).click());
    const detail = await waitFor(() => r.$('[data-testid="event-detail"]')!, r);
    expect(detail.querySelector('pre')!.textContent!.length).toBeGreaterThan(2000);
    expect(detail.querySelector('pre')!.textContent!.length).toBeLessThan(2200);
    expect(detail.textContent).toContain('Cut here');
    expect(r.$('[data-type="task.diff"] td.data button')!.getAttribute('aria-expanded')).toBe('true');
    // a short payload opens whole, with no note; closing the row hides it again
    await r.act(() => (r.$('[data-type="agent.created"] td.data button') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$$('[data-testid="event-detail"]')).toHaveLength(2), r);
    const second = Array.from(r.$$('[data-testid="event-detail"]'))[0] as HTMLElement;
    expect(second.textContent).toBe('{"name":"Pay"}');
    await r.act(() => (r.$('[data-type="task.diff"] td.data button') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$$('[data-testid="event-detail"]')).toHaveLength(1), r);
    for (const row of s.store.getSnapshot().events) expect('data' in row).toBe(false);
    // a live event is kept the same way
    s.emit({ type: 'task.completed', projectId: 'p1', data: big });
    await waitFor(() => expect(types(r.container)[0]).toBe('task.completed'), r);
    expect('data' in s.store.getSnapshot().events.at(-1)!).toBe(false);
  });

  it('filters by type prefix', async () => {
    const s = setup({ eventsReplay: async () => [ev(1, 'task.created', { projectId: 'p1' }), ev(2, 'agent.created', { projectId: 'p1' }), ev(3, 'task.completed', { projectId: 'p1' })] });
    const r = mount(<ActivityScreen />, s);
    await waitFor(() => expect(types(r.container).length).toBe(3), r);
    type(byLabel(r.container, 'Filter by type prefix'), 'task.*');
    r.root.flush();
    expect(types(r.container)).toEqual(['task.completed', 'task.created']);
    type(byLabel(r.container, 'Filter by type prefix'), 'nothing.matches');
    r.root.flush();
    expect(r.container.textContent).toContain('No matching events');
  });

  it('shows this project only by default, and everything when the box is cleared', async () => {
    const s = setup({ eventsReplay: async () => [ev(1, 'task.created', { projectId: 'p1' }), ev(2, 'task.created', { projectId: 'p2' }), ev(3, 'license.changed')] });
    const r = mount(<ActivityScreen />, s);
    await waitFor(() => expect(r.services.workspace.projectId).toBe('p1'), r);
    await waitFor(() => expect(types(r.container)).toEqual(['license.changed', 'task.created']), r);
    const box = byLabel<HTMLInputElement>(r.container, 'This project only');
    expect(box.checked).toBe(true);
    await r.act(() => box.click());
    expect(types(r.container).length).toBe(3);
  });

  it('says when earlier events could not be loaded', async () => {
    const s = setup({
      eventsReplay: async () => {
        throw new Error('replay refused');
      },
    });
    const r = mount(<ActivityScreen />, s);
    await waitFor(() => expect(r.container.querySelector('[role="alert"]')!.textContent).toContain('replay refused'), r);
  });

  it('filters by family chips, and remembers the chips, the type box and the project box for the project (X51)', async () => {
    forget();
    const replay = async () => [ev(1, 'task.created', { projectId: 'p1' }), ev(2, 'agent.started', { projectId: 'p1' }), ev(3, 'task.failed', { projectId: 'p1' }), ev(4, 'approval.requested', { projectId: 'p1' }), ev(5, 'license.changed')];
    const s = setup({ eventsReplay: replay });
    const r = mount(<ActivityScreen />, s);
    await waitFor(() => expect(r.services.workspace.projectId).toBe('p1'), r);
    await waitFor(() => expect(types(r.container).length).toBe(5), r);
    const chip = (label: string) => Array.from(r.$$('[data-testid="activity-chips"] button.chip')).find((b) => b.textContent === label) as HTMLButtonElement;
    expect(Array.from(r.$$('[data-testid="activity-chips"] button.chip')).map((b) => b.textContent)).toEqual(['Errors', 'Waiting on you', 'Agents', 'Tasks and runs', 'Everything else']);
    await r.act(() => chip('Errors').click());
    expect(types(r.container)).toEqual(['task.failed']);
    expect(chip('Errors').getAttribute('aria-pressed')).toBe('true');
    await r.act(() => chip('Waiting on you').click());
    expect(types(r.container)).toEqual(['approval.requested', 'task.failed']);
    type(byLabel(r.container, 'Filter by type prefix'), 'task.*');
    r.root.flush();
    expect(types(r.container)).toEqual(['task.failed']);
    cleanup();

    // the next visit shows the same filter
    const s2 = setup({ eventsReplay: replay });
    const r2 = mount(<ActivityScreen />, s2);
    await waitFor(() => expect(r2.services.workspace.projectId).toBe('p1'), r2);
    await waitFor(() => expect(types(r2.container)).toEqual(['task.failed']), r2);
    expect(byLabel<HTMLInputElement>(r2.container, 'Filter by type prefix').value).toBe('task.*');
    const pressed = Array.from(r2.$$('[data-testid="activity-chips"] button.chip[aria-pressed="true"]')).map((b) => b.textContent);
    expect(pressed).toEqual(['Errors', 'Waiting on you']);
    // Show all clears the chips
    await r2.act(() => (Array.from(r2.$$('[data-testid="activity-chips"] button.chip')).find((b) => b.textContent === 'Show all') as HTMLButtonElement).click());
    expect(types(r2.container)).toEqual(['task.failed', 'task.created']);
  });

  it('links the scope column to the task, its run and the agent, by key and name, and narrows to one of them from the row menu (X52)', async () => {
    forget();
    const s = setup({
      projectList: async () => [{ id: 'p1', name: 'gocalc', key: 'GOC' } as Project],
      eventsReplay: async () => [
        ev(1, 'task.created', { projectId: 'p1', taskId: 't1' }),
        ev(2, 'agent.started', { projectId: 'p1', taskId: 't1', runId: 'run_0123456789abcdef', agentId: 'a1' }),
        ev(3, 'task.created', { projectId: 'p1', taskId: 't2' }),
      ],
      agentList: async () => [{ id: 'a1', name: 'Pay' }] as never,
      taskList: async () => [{ id: 't1', seq: 41, title: 'Fix it' }] as never,
      taskCount: async () => ({ total: 1 }) as never,
      roomList: async () => [],
      roomUnread: async () => [],
      needsYouList: async () => [],
      presenceList: async () => [],
    });
    s.project.start();
    const r = mount(<ActivityScreen />, s);
    await waitFor(() => expect(types(r.container).length).toBe(3), r);
    const scope = () => r.$('[data-type="agent.started"] td.scope')!;
    await waitFor(() => expect(scope().textContent).toContain('GOC-41'), r);
    const links = Array.from(scope().querySelectorAll('a')).map((a) => [a.textContent, a.getAttribute('href')]);
    expect(links).toEqual([
      ['GOC-41', '/task/t1'],
      ['Pay', '/team/a1'],
      ['run_0123456…', '/task/t1/run/run_0123456789abcdef'],
    ]);
    // a task the list does not hold still shows its id's start
    expect(Array.from(r.$$('[data-type="task.created"] td.scope a')).map((a) => a.textContent)).toEqual(['t2', 'GOC-41']);
    await fromMenu(r, r.$('[data-type="agent.started"] [data-testid="event-more"]') as HTMLElement, 'Only this task');
    await waitFor(() => expect(types(r.container)).toEqual(['agent.started', 'task.created']), r);
    expect(r.$('[data-testid="activity-scope"]')!.textContent).toContain('task GOC-41');
    await r.act(() => (r.$('[data-testid="activity-scope"] button') as HTMLButtonElement).click());
    expect(types(r.container).length).toBe(3);
  });

  it('takes a run from the address and remembers it (X55)', async () => {
    forget();
    const replay = async () => [ev(1, 'task.created', { projectId: 'p1', taskId: 't1' }), ev(2, 'agent.started', { projectId: 'p1', taskId: 't1', runId: 'r1' }), ev(3, 'agent.started', { projectId: 'p1', taskId: 't1', runId: 'r2' })];
    const s = setup({ eventsReplay: replay });
    const r = mount(<ActivityScreen run="r1" task="t1" />, s);
    await waitFor(() => expect(r.services.workspace.projectId).toBe('p1'), r);
    await waitFor(() => expect(types(r.container)).toEqual(['agent.started']), r);
    expect(r.$('[data-type="agent.started"] td.seq')!.textContent).toBe('2');
    expect(r.$('[data-testid="activity-scope"]')!.textContent).toContain('run r1');
    cleanup();
    const r2 = mount(<ActivityScreen />, setup({ eventsReplay: replay }));
    await waitFor(() => expect(r2.services.workspace.projectId).toBe('p1'), r2);
    await waitFor(() => expect(types(r2.container)).toEqual(['agent.started']), r2);
  });

  it('fetches a cut payload whole from the core with Show all, with Copy as JSON (X54)', async () => {
    forget();
    const big = { diff: 'z'.repeat(5000) };
    const asked: [number, unknown][] = [];
    const s = setup({
      eventsReplay: async (after, opts) => {
        asked.push([after, opts]);
        if (opts?.limit === 1) return [ev(7, 'task.diff', { projectId: 'p1', data: big })];
        return [ev(7, 'task.diff', { projectId: 'p1', data: big })];
      },
    });
    const r = mount(<ActivityScreen />, s);
    await waitFor(() => expect(types(r.container)).toEqual(['task.diff']), r);
    await r.act(() => (r.$('[data-type="task.diff"] td.data button') as HTMLButtonElement).click());
    const detail = await waitFor(() => r.$('[data-testid="event-detail"]')!, r);
    expect(detail.querySelector('pre')!.textContent!.length).toBeLessThan(2200);
    const showAll = Array.from(detail.querySelectorAll('button')).find((b) => b.textContent === 'Show whole payload') as HTMLButtonElement;
    await r.act(() => showAll.click());
    await waitFor(() => expect(r.$('[data-testid="event-detail"] pre')!.textContent).toBe(JSON.stringify(big, null, 2)), r);
    expect(asked.at(-1)).toEqual([6, { limit: 1 }]);
    expect(r.$('[data-testid="event-detail"]')!.textContent).not.toContain('Cut here');
    expect(Array.from(r.$$('[data-testid="event-detail"] button')).map((b) => b.textContent)).toContain('Copy as JSON');
  });

  it('says a failed whole payload under its own row only, and its button is not named like the chip that clears the families (X54 review)', async () => {
    forget();
    const big = { diff: 'z'.repeat(5000) };
    const s = setup({
      eventsReplay: async (_after, opts) => {
        if (opts?.limit === 1) throw new Error('the core is busy');
        return [ev(7, 'task.diff', { projectId: 'p1', data: big }), ev(9, 'task.diff', { projectId: 'p1', data: big })];
      },
    });
    const r = mount(<ActivityScreen />, s);
    await waitFor(() => expect(types(r.container)).toEqual(['task.diff', 'task.diff']), r);
    for (const b of Array.from(r.$$('td.data button')) as HTMLButtonElement[]) await r.act(() => b.click());
    await waitFor(() => expect(r.$$('[data-testid="event-detail"]')).toHaveLength(2), r);
    const detailOf = (seq: number) => Array.from(r.$$('[data-testid="event-row"]')).find((x) => x.querySelector('td.seq')!.textContent === String(seq))!.nextElementSibling as HTMLElement;
    const whole7 = Array.from(detailOf(7).querySelectorAll('button')).find((b) => b.textContent === 'Show whole payload') as HTMLButtonElement;
    await r.act(() => whole7.click());
    await waitFor(() => expect(detailOf(7).textContent).toContain('the core is busy'), r);
    expect(detailOf(9).textContent).not.toContain('the core is busy');
    expect(detailOf(9).querySelector('[role="alert"]')).toBe(null);
    // with a family chip pressed there is one Show all (the chip) and the payload buttons are named for the payload
    await r.act(() => (Array.from(r.$$('[data-testid="activity-chips"] button.chip')).find((b) => b.textContent === 'Tasks and runs') as HTMLButtonElement).click());
    expect(Array.from(r.$$('button')).filter((b) => b.textContent === 'Show all')).toHaveLength(1);
  });

  it('reads a scoped run from the core when it is older than the recent events the page holds, and says so when it has none (X55 review)', async () => {
    forget();
    const asked: [number, unknown][] = [];
    // the latest events of the workspace are another task's; the run's events are older, in the second page of its task
    const page1 = Array.from({ length: 1000 }, (_, i) => ev(i + 1, 'agent.output', { projectId: 'p1', taskId: 't1', runId: 'r0' }));
    const page2 = [ev(1001, 'agent.started', { projectId: 'p1', taskId: 't1', runId: 'r1' }), ev(1002, 'agent.completed', { projectId: 'p1', taskId: 't1', runId: 'r1' })];
    const s = setup({
      eventsReplay: async (after, opts) => {
        asked.push([after, opts]);
        if (opts?.taskId === 't1') return after === 0 ? page1 : after === 1000 ? page2 : [];
        if (opts?.taskId) return [];
        return [ev(9000, 'task.created', { projectId: 'p1', taskId: 't9' })];
      },
    });
    const r = mount(<ActivityScreen run="r1" task="t1" />, s);
    await waitFor(() => expect(types(r.container)).toEqual(['agent.completed', 'agent.started']), r);
    expect(asked.filter(([, o]) => (o as { taskId?: string }).taskId)).toEqual([
      [0, { taskId: 't1', limit: 1000 }],
      [1000, { taskId: 't1', limit: 1000 }],
    ]);
    // the store's ring is not filled with them: they are the page's own
    expect(s.store.getSnapshot().events.some((e) => e.runId === 'r1')).toBe(false);
    cleanup();

    // a run the core has nothing of: the page says so, not that events will appear
    forget();
    const s2 = setup({ eventsReplay: async (_a, opts) => (opts?.taskId ? [] : [ev(9000, 'task.created', { projectId: 'p1', taskId: 't9' })]) });
    const r2 = mount(<ActivityScreen run="r7" task="t7" />, s2);
    await waitFor(() => expect(r2.$('[data-testid="activity-scope-empty"]')?.textContent ?? '').toContain('None of the events the core has for run r7'), r2);
    expect(r2.container.textContent).not.toContain('Events appear here as the core records them');
  });

  it('applies the scope from the address to the project open then, not to a project chosen later (X51 review)', async () => {
    forget();
    const s = setup({
      projectList: async () => [{ id: 'p1', name: 'gocalc' } as Project, { id: 'p2', name: 'other' } as Project],
      eventsReplay: async () => [ev(1, 'agent.started', { projectId: 'p1', taskId: 't1', runId: 'r1' }), ev(2, 'task.created', { projectId: 'p2', taskId: 't2' })],
    });
    const r = mount(<ActivityScreen run="r1" task="t1" />, s);
    await waitFor(() => expect(r.$('[data-testid="activity-scope"]')!.textContent).toContain('run r1'), r);
    await r.act(() => s.workspace.selectProject('p2'));
    await waitFor(() => expect(types(r.container)).toEqual(['task.created']), r);
    expect(r.$('[data-testid="activity-scope"]')).toBe(null);
    expect(JSON.parse(localStorage.getItem('drydock.activity.filter.p2') ?? '{}').scope ?? {}).toEqual({});
    expect(JSON.parse(localStorage.getItem('drydock.activity.filter.p1')!).scope).toEqual({ taskId: 't1', runId: 'r1' });
  });
});

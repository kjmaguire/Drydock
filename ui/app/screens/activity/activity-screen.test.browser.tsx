import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { DrydockEvent, Project } from '../../api/types.ts';
import { byLabel, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { ActivityScreen } from './activity-screen.tsx';

afterEach(cleanup);

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
    // the tooltip has the stored preview (2 KB at most) and says the rest is not here
    const title = cell.getAttribute('title')!;
    expect(title.length).toBeGreaterThan(2000);
    expect(title.length).toBeLessThan(2200);
    expect(title).toContain('cut here');
    expect(r.$('[data-type="agent.created"] td.data')!.getAttribute('title')).toBe('{"name":"Pay"}');
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
});

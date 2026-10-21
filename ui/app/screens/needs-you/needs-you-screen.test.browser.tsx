import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { NeedsYouItem } from '../../api/types.ts';
import { cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { NeedsYouScreen } from './needs-you-screen.tsx';

afterEach(cleanup);

function servicesWith(items: NeedsYouItem[]) {
  const s = testServices({
    projectList: async () => [{ id: 'p1', name: 'gocalc' } as never],
    needsYouList: async () => items,
    agentList: async () => [{ id: 'a1', name: 'Ada', role: 'backend' } as never],
    roomList: async () => [],
    roomUnread: async () => [],
    presenceList: async () => [],
    taskList: async () => [],
  });
  s.workspace.start();
  s.project.start();
  return s;
}

describe('NeedsYouScreen', () => {
  it('lists each item as a link to where it is decided, with the agent and a hint', async () => {
    const s = servicesWith([
      { key: 'k1', kind: 'approval', title: 'Budget limit reached', severity: 1, taskId: 't1', agentId: 'a1', createdAt: '2026-01-01T00:00:00Z' } as NeedsYouItem,
      { key: 'k2', kind: 'question', title: 'Which port?', severity: 2, roomId: 'r9', createdAt: '2026-01-01T00:00:00Z' } as NeedsYouItem,
    ]);
    const r = mount(<NeedsYouScreen />, s);
    const items = await waitFor(() => {
      const li = r.$$('[data-testid="needs-item"]');
      if (li.length !== 2) throw new Error(`have ${li.length}`);
      return li;
    }, r);
    expect(items[0]!.getAttribute('data-kind')).toBe('approval');
    expect(items[0]!.querySelector('a')!.getAttribute('href')).toBe('/task/t1');
    expect(items[0]!.textContent).toContain('Ada');
    expect(items[0]!.querySelector('[data-testid="needs-hint"]')!.textContent).toContain('raise the limit');
    expect(items[1]!.querySelector('a')!.getAttribute('href')).toBe('/room/r9');
    expect(r.$('h1')!.textContent).toContain('Needs you');
  });

  it("shows what a task's review is about before it is opened: who, the change, checks and verdict (UI review P1-1)", async () => {
    const s = servicesWith([
      {
        key: 'k1', kind: 'review', title: 'Review and merge: Fix rounding', severity: 2, taskId: 't1', createdAt: '2026-01-01T00:00:00Z',
        context: { agent: 'a1', hasDiff: true, files: 1, insertions: 4, deletions: 1, checksPassed: 3, checksTotal: 4, verdict: 'SHIP' },
      } as NeedsYouItem,
    ]);
    const r = mount(<NeedsYouScreen />, s);
    const line = await waitFor(() => {
      const el = r.$('[data-testid="needs-context"]');
      if (!el) throw new Error('no context yet');
      return el;
    }, r);
    expect(line.textContent).toBe('1 file +4 \u22121· 3/4 checks passed· reviewer: SHIP');
    // the assignee is shown as the item's agent
    expect(r.$('[data-testid="needs-item"] .meta')!.textContent).toContain('Ada');
    const tones = Array.from(line.children).map((c) => c.getAttribute('data-tone'));
    expect(tones).toEqual([null, 'err', 'ok']);
  });

  it('says plainly when nothing is waiting', async () => {
    const r = mount(<NeedsYouScreen />, servicesWith([]));
    await waitFor(() => {
      if (!r.container.textContent?.includes('Nothing is waiting for you')) throw new Error('not yet');
    }, r);
    expect(r.$$('[data-testid="needs-item"]').length).toBe(0);
  });

  it('re-reads when an approval event arrives', async () => {
    let items: NeedsYouItem[] = [];
    const s = servicesWith([]);
    s.client.needsYouList = async () => items;
    const r = mount(<NeedsYouScreen />, s);
    await waitFor(() => {
      if (!r.container.textContent?.includes('Nothing is waiting')) throw new Error('not yet');
    }, r);
    items = [{ key: 'k3', kind: 'review', title: 'Review the change', severity: 2, taskId: 't2', createdAt: '2026-01-01T00:00:00Z' } as NeedsYouItem];
    s.emit({ type: 'approval.requested', projectId: 'p1' });
    await waitFor(() => {
      if (r.$$('[data-testid="needs-item"]').length !== 1) throw new Error('not yet');
    }, r);
  });
});

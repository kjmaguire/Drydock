import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { MemoryEntry } from '../../api/extTypes.ts';
import { byLabel, byText, cleanup, mount, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
import { MemoryScreen } from './memory-screen.tsx';

afterEach(cleanup);

function servicesWith(client: Partial<Client>) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as never],
    ...client,
  });
  s.workspace.start();
  return s;
}

const mem = (over: Partial<MemoryEntry>): MemoryEntry => ({
  id: 'm1',
  projectId: 'prj_1',
  kind: 'convention',
  title: 'Use gofmt',
  body: 'Run gofmt <b>always</b>',
  status: 'proposed',
  source: 'agt_1',
  created: 1,
  updated: 1,
  ...over,
});

const cards = async (r: ReturnType<typeof mount>, n: number) =>
  waitFor(() => {
    const c = r.$$('[data-testid="memory"]');
    if (c.length !== n) throw new Error(`have ${c.length}`);
    return c;
  }, r);

describe('MemoryScreen', () => {
  it('proposals are accepted or rejected by buttons; accepted entries are deleted after a confirm', async () => {
    const accepted: string[] = [];
    const rejected: string[] = [];
    const deleted: string[] = [];
    const r = mount(
      <MemoryScreen />,
      servicesWith({
        memoryList: async () => [mem({}), mem({ id: 'm2', title: 'No globals', status: 'accepted' })],
        memoryAccept: async (id) => (accepted.push(id), mem({ status: 'accepted' })),
        memoryReject: async (id) => (rejected.push(id), mem({ status: 'rejected' })),
        memoryDelete: async (id) => void deleted.push(id),
      }),
    );
    let [c0, c1] = await cards(r, 2);
    expect(r.container.querySelector('b')).toBe(null);
    expect(c0!.textContent).toContain('Agents do not see it until you accept it');
    expect(c0!.getAttribute('data-title')).toBe('Use gofmt');
    await r.act(() => byText(c0!, 'button', 'Accept').click());
    await waitFor(() => expect(accepted).toEqual(['m1']), r);
    [c0, c1] = await cards(r, 2);
    await r.act(() => byText(c0!, 'button', 'Reject').click());
    await waitFor(() => expect(rejected).toEqual(['m1']), r);
    [c0, c1] = await cards(r, 2);
    expect(Array.from(c1!.querySelectorAll('button')).some((b) => b.textContent?.trim() === 'Accept')).toBe(false);
    await r.act(() => byText(c1!, 'button', 'Delete').click());
    expect(deleted).toEqual([]);
    await r.act(() => byText(c1!, 'button', 'Yes, delete').click());
    await waitFor(() => expect(deleted).toEqual(['m2']), r);
  });

  it('proposing memory sends kind, title and body', async () => {
    const proposed: unknown[][] = [];
    const r = mount(
      <MemoryScreen />,
      servicesWith({
        memoryList: async () => [],
        memoryPropose: async (...args) => (proposed.push(args), mem({})),
      }),
    );
    await waitFor(() => byText(r.container, 'h2', 'Nothing here.'), r);
    expect(valueOf(byLabel(r.container, 'Kind'))).toBe('convention');
    type(byLabel<HTMLElement>(r.container, 'Kind'), 'decision');
    type(byLabel(r.container, 'Title'), 'Use SQLite');
    type(byLabel<HTMLTextAreaElement>(r.container, 'Body'), 'One writer');
    r.root.flush();
    await r.act(() => byText(r.container, 'button', 'Propose').click());
    await waitFor(() => expect(proposed).toEqual([['prj_1', 'decision', 'Use SQLite', 'One writer']]), r);
    await waitFor(() => expect(byLabel(r.container, 'Title').value).toBe(''), r);
  });

  it('filters by status, searches within it and clears the search', async () => {
    const listed: string[] = [];
    const searched: unknown[][] = [];
    const r = mount(
      <MemoryScreen />,
      servicesWith({
        memoryList: async (_p, status) => (
          listed.push(status ?? ''),
          [
            mem({
              id: 'm1',
              status: 'accepted',
              title: `Listed ${status || 'all'}`,
            }),
          ]
        ),
        memorySearch: async (...args) => (searched.push(args), [mem({ id: 'm9', title: 'Found one', status: 'accepted' })]),
      }),
    );
    await waitFor(() => expect(r.$('[data-testid="memory"]')!.getAttribute('data-title')).toBe('Listed all'), r);
    const show = r.$('[role="group"][aria-label="Show"]')!;
    await r.act(() => byText(show, 'button', 'accepted').click());
    await waitFor(() => expect(r.$('[data-testid="memory"]')!.getAttribute('data-title')).toBe('Listed accepted'), r);
    expect(listed).toEqual(['', 'accepted']);
    type(byLabel(r.container, 'Search memory'), 'gofmt');
    r.root.flush();
    await r.act(() => byText(r.container, 'button', 'Search').click());
    await waitFor(() => expect(r.$('[data-testid="memory"]')!.getAttribute('data-title')).toBe('Found one'), r);
    expect(searched).toEqual([['prj_1', 'gofmt', 'accepted']]);
    await r.act(() => byText(r.container, 'button', 'Clear search').click());
    await waitFor(() => expect(r.$('[data-testid="memory"]')!.getAttribute('data-title')).toBe('Listed accepted'), r);
    expect(byLabel(r.container, 'Search memory').value).toBe('');
  });

  it('says when a search matches nothing', async () => {
    const r = mount(
      <MemoryScreen />,
      servicesWith({
        memoryList: async () => [mem({})],
        memorySearch: async () => [],
      }),
    );
    await cards(r, 1);
    type(byLabel(r.container, 'Search memory'), 'zzz');
    r.root.flush();
    await r.act(() => byText(r.container, 'button', 'Search').click());
    await waitFor(() => byText(r.container, 'h2', 'Nothing matches.'), r);
  });
});

import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { RepoMapResult, RepoMapView } from '../../api/repomapClient.ts';
import { byLabel, byText, cleanup, mount, testServices, type, typeKeys, waitFor } from '../../test/render.tsx';
import { RepoMapScreen } from './repomap-screen.tsx';

afterEach(() => {
  cleanup();
  try {
    localStorage.removeItem('drydock.repomap');
  } catch {
    /* none */
  }
});

const need = <T,>(x: T | null): T => {
  if (!x) throw new Error('not yet');
  return x;
};

// TEST DATA: a map as the core answers repomap.get (internal/core/ext_repomap.go).
const result = (over: Partial<RepoMapResult> = {}): RepoMapResult => ({
  map: 'go/store/store.go:\n⋮\n│func NewStore() *Store {\n⋮\ngo/api/api.go:\n⋮\n│func ServeRequest(s *store.Store, key string) string {\n⋮\n',
  tokens: 40,
  budget: 2048,
  root: '/repo',
  focus: [],
  listed: 30,
  files: 18,
  cached: 0,
  shown: 2,
  defs: 60,
  refs: 90,
  languages: { go: 5, typescript: 2 },
  skipped: { unsupported: 12, vendored: 1, generated: 0, tooLarge: 0, binary: 0, unreadable: 0, linked: 0, overLimit: 0 },
  ranked: [
    { path: 'go/store/store.go', lang: 'go', rank: 0.2579, defs: 4, refs: 9, shown: true },
    { path: 'go/api/api.go', lang: 'go', rank: 0.0111, defs: 2, refs: 6, shown: true },
    { path: 'ts/src/cart.ts', lang: 'typescript', rank: 0.0092, defs: 4, refs: 7, shown: false },
  ],
  elapsedMs: 120,
  ...over,
});

function show(repoMapGet: Client['repoMapGet'], project: Record<string, unknown> = { id: 'p1', name: 'polyglot', hasRepo: true }) {
  const s = testServices({
    projectList: async () => [project] as never,
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({}) as never,
    repoMapGet,
  });
  s.workspace.start();
  return mount(<RepoMapScreen />, s);
}

describe('Repo map page', () => {
  it('shows the ranked map of the repository, then remakes it with the focus and budget typed', async () => {
    const calls: [string, string[], number][] = [];
    const r = show(async (projectId, focus = [], budget = 2048) => {
      calls.push([projectId, focus, budget]);
      return focus.length
        ? { available: true, languages: ['go'], result: result({ focus, budget, ranked: [{ path: 'java/Inventory.java', lang: 'java', rank: 0.3, defs: 3, refs: 1, shown: true, focus: false }, { path: 'java/Shop.java', lang: 'java', rank: 0.2, defs: 2, refs: 5, shown: true, focus: true }] }) }
        : { available: true, languages: ['go'], result: result() };
    });
    const pre = await waitFor(() => {
      const p = r.$('[data-testid="repomap-text"]');
      if (!p) throw new Error('no map yet');
      return p as HTMLElement;
    }, r);
    expect(pre.textContent).toContain('│func NewStore() *Store {');
    expect(pre.textContent!.startsWith('go/store/store.go:')).toBe(true);
    expect(calls[0]).toEqual(['p1', [], 2048]);
    const stats = r.$('[data-testid="repomap-stats"]')!.textContent!;
    expect(stats).toContain('2 of 18 source files in the map, about 40 of 2048 tokens.');
    expect(stats).toContain('Languages: go 5, typescript 2.');
    expect(stats).toContain('Not parsed: 12 in other languages or not code, 1 vendored.');
    const rows = Array.from(r.$$('[data-testid="repomap-ranking"] tbody tr')).map((tr) => Array.from(tr.querySelectorAll('td')).map((td) => td.textContent!.trim()));
    expect(rows[0]).toEqual(['go/store/store.go', 'go', '26%', '4', '9', 'yes', 'Focus']);
    expect(rows[2]![5]).toBe('no');

    // focus typed and a budget chosen: the map is made again with them
    type(byLabel(r.container, 'Focus paths'), 'java/Shop.java, java/');
    r.root.flush();
    (r.$('[data-testid="repomap-make"]') as HTMLButtonElement).click();
    await waitFor(() => expect(calls.length).toBe(2), r);
    expect(calls[1]).toEqual(['p1', ['java/Shop.java', 'java/'], 2048]);
    await waitFor(() => expect(r.$('[data-testid="repomap-stats"]')!.textContent).toContain('Focus: java/Shop.java, java/.'), r);
    expect(r.$('[data-testid="repomap-ranking"] tbody tr:nth-child(2) td')!.textContent).toContain('focus');
    await r.act(() => byText(r.$('[data-testid="repomap-budget"]')!, 'button', '8k').click());
    await waitFor(() => expect(calls.length).toBe(3), r);
    expect(calls[2]).toEqual(['p1', ['java/Shop.java', 'java/'], 8192]);
    expect(r.$('[data-testid="repomap-budget"]')!.getAttribute('data-value')).toBe('8192');
  });

  it('keeps every key typed into the focus box, and makes the map with it (H12)', async () => {
    const calls: [string, string[], number][] = [];
    const r = show(async (projectId, focus = [], budget = 2048) => (calls.push([projectId, focus, budget]), { available: true, languages: ['go'], result: result({ focus, budget }) }));
    await waitFor(() => expect(calls.length).toBe(1), r);
    const focus = await waitFor(() => byLabel<HTMLInputElement>(r.container, 'Focus paths'), r);
    await typeKeys(focus, 'go/store, go/api', r);
    expect(focus.value).toBe('go/store, go/api');
    (r.$('[data-testid="repomap-make"]') as HTMLButtonElement).click();
    await waitFor(() => expect(calls.length).toBe(2), r);
    expect(calls[1]).toEqual(['p1', ['go/store', 'go/api'], 2048]);
  });

  // X412: the focus and budget are kept per project, a ranked file is a focus in one click, and so are a task's files
  it('remembers the focus and budget per project, focuses a ranked file in one click and a task\'s files from a chip', async () => {
    const calls: [string, string[], number][] = [];
    const get: Client['repoMapGet'] = async (projectId, focus = [], budget = 2048) => (calls.push([projectId, focus, budget]), { available: true, languages: ['go'], result: result({ focus, budget }) });
    const r = show(get);
    await waitFor(() => expect(calls.length).toBe(1), r);
    const row = await waitFor(() => need(r.$('[data-testid="repomap-ranking"] tbody tr:nth-child(2)')), r);
    const btn = byLabel<HTMLButtonElement>(row, 'Focus on go/api/api.go');
    expect(btn.getAttribute('aria-pressed')).toBe('false');
    await r.act(() => btn.click());
    await waitFor(() => expect(calls[1]).toEqual(['p1', ['go/api/api.go'], 2048]), r);
    await waitFor(() => expect(byLabel<HTMLButtonElement>(r.container, 'Focus on go/api/api.go').getAttribute('aria-pressed')).toBe('true'), r);
    expect(byLabel<HTMLInputElement>(r.container, 'Focus paths').value).toBe('go/api/api.go');
    await r.act(() => byText(r.$('[data-testid="repomap-budget"]')!, 'button', '4k').click());
    await waitFor(() => expect(calls[2]).toEqual(['p1', ['go/api/api.go'], 4096]), r);
    cleanup();

    // the next visit starts from what the map was last made with
    calls.length = 0;
    const s = testServices({
      projectList: async () => [{ id: 'p1', name: 'polyglot', hasRepo: true }] as never,
      licenseStatus: async () => ({}) as never,
      appInfo: async () => ({}) as never,
      taskList: async () => [
        { id: 't7', projectId: 'p1', seq: 7, title: 'Fix cart', status: 'IN_PROGRESS', updated: 2, files: ['ts/src/cart.ts', 'ts/src/'] },
        { id: 't8', projectId: 'p1', seq: 8, title: 'Done', status: 'COMPLETED', updated: 3, files: ['x.go'] },
      ] as never,
      repoMapGet: get,
    });
    s.workspace.start();
    s.project.start();
    const again = mount(<RepoMapScreen />, s);
    await waitFor(() => expect(calls[0]).toEqual(['p1', ['go/api/api.go'], 4096]), again);
    expect(byLabel<HTMLInputElement>(again.container, 'Focus paths').value).toBe('go/api/api.go');
    const chips = await waitFor(() => need(again.$('[data-testid="repomap-task-chips"]')), again);
    expect(Array.from(chips.querySelectorAll('button')).map((b) => b.textContent!.trim())).toEqual(['Files of task #7']);
    await again.act(() => byText(chips, 'button', 'Files of task #7').click());
    await waitFor(() => expect(calls[1]).toEqual(['p1', ['ts/src/cart.ts', 'ts/src/'], 4096]), again);
  });

  it('says plainly when this build has no parser', async () => {
    const view: RepoMapView = { available: false, languages: [], message: 'this build of Drydock has no repo map: it was built without the C parser (tree-sitter needs CGO; decision D5)' };
    const r = show(async () => view);
    const note = await waitFor(() => {
      const n = r.$('[data-testid="repomap-unavailable"]');
      if (!n) throw new Error('no notice yet');
      return n as HTMLElement;
    }, r);
    expect(note.textContent).toContain('No repo map in this build');
    expect(note.textContent).toContain('built without the C parser');
    expect(r.$('[data-testid="repomap-text"]')).toBeNull();
  });

  it('asks for a repository first, and maps nothing without one', async () => {
    let called = false;
    const r = show(
      async () => {
        called = true;
        return { available: true, languages: [] };
      },
      { id: 'p1', name: 'empty', hasRepo: false },
    );
    await waitFor(() => expect(r.$('[data-testid="repomap-no-repo"]')).not.toBeNull(), r);
    expect(called).toBe(false);
    expect(r.$('[data-testid="repomap-make"]')).toBeNull();
  });

  it('says so when the repository has no source files it reads', async () => {
    const r = show(async () => ({ available: true, languages: ['go', 'rust'], result: result({ map: '', ranked: [], files: 0, shown: 0 }) }));
    await waitFor(() => expect(r.$('[data-testid="repomap-empty"]')!.textContent).toContain('(go, rust)'), r);
    expect(r.$('[data-testid="repomap-ranking"]')).toBeNull();
  });

  it('says the budget was too small, not that there is no code, when files were read but nothing fit', async () => {
    const r = show(async () => ({ available: true, languages: ['go'], result: result({ map: '', files: 12, shown: 0, budget: 256 }) }));
    await waitFor(() => expect(r.$('[data-testid="repomap-empty"]')!.textContent).toContain('12 source files were read'), r);
    expect(r.$('[data-testid="repomap-empty"]')!.textContent).toContain('256 tokens');
    expect(r.$('[data-testid="repomap-empty"]')!.textContent).not.toContain('no source files');
  });

  it('"Double the budget" on the too-small notice makes the map again with twice the budget (X412)', async () => {
    const calls: number[] = [];
    const r = show(async (_p, focus = [], budget = 2048) => (calls.push(budget), { available: true, languages: ['go'], result: result({ map: '', files: 12, shown: 0, focus, budget }) }));
    const btn = (await waitFor(() => need(r.$('[data-testid="repomap-double"]')), r)) as HTMLButtonElement;
    expect(btn.textContent).toContain('Double the budget');
    await r.act(() => btn.click());
    await waitFor(() => expect(calls).toEqual([2048, 4096]), r);
    const again = (await waitFor(() => need(r.$('[data-testid="repomap-empty"] [data-testid="repomap-double"]')), r)) as HTMLButtonElement;
    await r.act(() => again.click());
    await waitFor(() => expect(calls).toEqual([2048, 4096, 8192]), r);
    await waitFor(() => expect(r.$('[data-testid="repomap-double"]')).toBeNull(), r); // 8192 is the most
  });
});

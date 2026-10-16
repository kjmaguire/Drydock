import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { RepoMapResult, RepoMapView } from '../../api/repomapClient.ts';
import { byLabel, cleanup, mount, testServices, type, typeKeys, waitFor } from '../../test/render.tsx';
import { RepoMapScreen } from './repomap-screen.tsx';

afterEach(cleanup);

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
    expect(rows[0]).toEqual(['go/store/store.go', 'go', '26%', '4', '9', 'yes']);
    expect(rows[2]![5]).toBe('no');

    // focus and budget: the map is made again with them (the budget kept inside 256..8192)
    type(byLabel(r.container, 'Focus paths'), 'java/Shop.java, java/');
    type(byLabel(r.container, 'Token budget'), '100000');
    (r.$('[data-testid="repomap-make"]') as HTMLButtonElement).click();
    await waitFor(() => expect(calls.length).toBe(2), r);
    expect(calls[1]).toEqual(['p1', ['java/Shop.java', 'java/'], 8192]);
    await waitFor(() => expect(r.$('[data-testid="repomap-stats"]')!.textContent).toContain('Focus: java/Shop.java, java/.'), r);
    expect(r.$('[data-testid="repomap-ranking"] tbody tr:nth-child(2) td')!.textContent).toContain('focus');
    expect((byLabel(r.container, 'Token budget') as HTMLInputElement).value).toBe('8192');
  });

  it('keeps every key typed into the focus and budget boxes, and makes the map with them (H12)', async () => {
    const calls: [string, string[], number][] = [];
    const r = show(async (projectId, focus = [], budget = 2048) => (calls.push([projectId, focus, budget]), { available: true, languages: ['go'], result: result({ focus, budget }) }));
    await waitFor(() => expect(calls.length).toBe(1), r);
    const focus = await waitFor(() => byLabel<HTMLInputElement>(r.container, 'Focus paths'), r);
    const budget = byLabel<HTMLInputElement>(r.container, 'Token budget');
    await typeKeys(focus, 'go/store, go/api', r);
    await typeKeys(budget, '3', r); // 2048 stays; the keys follow it
    expect(focus.value).toBe('go/store, go/api');
    expect(budget.value).toBe('20483');
    (r.$('[data-testid="repomap-make"]') as HTMLButtonElement).click();
    await waitFor(() => expect(calls.length).toBe(2), r);
    expect(calls[1]).toEqual(['p1', ['go/store', 'go/api'], 8192]);
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
});

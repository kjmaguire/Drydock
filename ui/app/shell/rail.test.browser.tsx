import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { cleanup, mount, testServices, waitFor } from '../test/render.tsx';
import { Rail } from './rail.tsx';

afterEach(cleanup);

function svc() {
  const s = testServices({
    projectList: async () => [{ id: 'p1', name: 'gocalc-approve' }, { id: 'p2', name: 'lib-a' }] as never,
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({}) as never,
  });
  s.workspace.start();
  return s;
}

describe('the project rail', () => {
  it('lists projects as radios (never as buttons a name search could hit) and selects on click', async () => {
    const s = svc();
    const r = mount(<Rail route={{ name: 'home' }} />, s);
    const tiles = await waitFor(() => {
      const x = r.$$('[role="radio"]');
      if (x.length !== 2) throw new Error('not yet');
      return x;
    }, r);
    expect(r.$('[role="radiogroup"]')!.getAttribute('aria-label')).toBe('Projects');
    expect(tiles[0]!.getAttribute('aria-checked')).toBe('true');
    expect(tiles[1]!.getAttribute('aria-checked')).toBe('false');
    await r.act(() => tiles[1]!.click());
    expect(s.workspace.projectId).toBe('p2');
    await waitFor(() => expect(r.$$('[role="radio"]')[1]!.getAttribute('aria-checked')).toBe('true'), r);
  });

  it('moves between projects with the arrow keys', async () => {
    const s = svc();
    const r = mount(<Rail route={{ name: 'home' }} />, s);
    await waitFor(() => expect(r.$$('[role="radio"]').length).toBe(2), r);
    await r.act(() => r.$('[role="radiogroup"]')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })));
    expect(s.workspace.projectId).toBe('p2');
    await r.act(() => r.$('[role="radiogroup"]')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })));
    expect(s.workspace.projectId).toBe('p1');
  });

  it('offers a new project and the app settings, named apart from the sidebar links', async () => {
    const r = mount(<Rail route={{ name: 'home' }} />, svc());
    const names = Array.from(r.$$('a')).map((a) => a.getAttribute('aria-label'));
    expect(names).toEqual(['+ New project', 'App settings']);
  });
});

import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import { cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { WorkspaceHomeScreen } from './workspace-home.tsx';

afterEach(cleanup);

// TEST DOUBLES only: a partial client with two projects, one of which has two items waiting for the human.
function show(client: Partial<Client> = {}) {
  const s = testServices({
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({}) as never,
    projectList: async () => [
      { id: 'p1', name: 'gocalc', repoPath: 'C:\\code\\gocalc', hasRepo: true },
      { id: 'p2', name: 'idea', repoPath: '', hasRepo: false },
    ] as never,
    needsYouList: async (pid: string) => (pid === 'p1' ? [{}, {}] : []) as never,
    ...client,
  });
  s.workspace.start();
  const navigated: string[] = [];
  const r = mount(<WorkspaceHomeScreen onNavigate={(h) => navigated.push(h)} />, s);
  return { r, s, navigated };
}

const tiles = (r: { $$: (s: string) => NodeListOf<Element> }) => Array.from(r.$$('[data-testid="home-project"]')) as HTMLAnchorElement[];

describe('the workspace Home', () => {
  it('lists the projects with where they live and what needs you in each', async () => {
    const { r } = show();
    await waitFor(() => expect(tiles(r)).toHaveLength(2), r);
    const [gocalc, idea] = tiles(r);
    expect(gocalc!.textContent).toContain('gocalc');
    expect(gocalc!.textContent).toContain('C:\\code\\gocalc');
    expect(idea!.textContent).toContain('No repository yet');
    await waitFor(() => expect(gocalc!.querySelector('[data-testid="home-project-needs"]')!.textContent).toBe('2 items need you'), r);
    expect(gocalc!.querySelector('[data-testid="home-project-needs"]')!.getAttribute('data-tone')).toBe('warn');
    expect(idea!.querySelector('[data-testid="home-project-needs"]')!.textContent).toBe('Nothing needs you');
  });

  it("says on one tile that its list could not be read, and still counts the others", async () => {
    const { r } = show({
      needsYouList: async (pid: string) => {
        if (pid === 'p2') throw new Error('core unreachable');
        return [{}] as never;
      },
    });
    await waitFor(() => expect(tiles(r)).toHaveLength(2), r);
    const [gocalc, idea] = tiles(r);
    await waitFor(() => expect(gocalc!.querySelector('[data-testid="home-project-needs"]')!.textContent).toBe('1 item needs you'), r);
    expect(idea!.querySelector('[data-testid="home-project-needs"]')!.textContent).toBe('Could not check what needs you');
  });

  it('opens a project: selects it, then goes to its home', async () => {
    const { r, s, navigated } = show();
    await waitFor(() => expect(tiles(r)).toHaveLength(2), r);
    await r.act(() => tiles(r)[1]!.click());
    expect(s.workspace.projectId).toBe('p2');
    expect(navigated).toEqual(['/']);
  });

  it('links every workspace area: the skill library, agents and MCP servers, chats, connections and the app', async () => {
    const { r } = show();
    await waitFor(() => expect(r.$('[data-testid="home-skills"]')).not.toBeNull(), r);
    const hrefs = Array.from(r.$$('[data-testid^="home-"]'))
      .map((e) => e.getAttribute('href'))
      .filter(Boolean);
    for (const h of ['/library/skills', '/library', '/chats', '/integrations', '/connections', '/providers', '/routing', '/settings', '/backups', '/data', '/license', '/support', '/project/new']) {
      expect([h, hrefs.includes(h)]).toEqual([h, true]);
    }
    expect(r.$('[data-testid="home-skills"]')!.textContent).toContain('Skills');
  });

  it('offers the first project when there is none', async () => {
    const { r } = show({ projectList: async () => [] });
    await waitFor(() => expect(r.$('[data-testid="home-projects"]')!.textContent).toContain('No projects yet.'), r);
    expect(tiles(r)).toHaveLength(0);
    expect(r.$('[data-testid="home-projects"] a[href="/project/new"]')).not.toBeNull();
  });

  it("shows, once, the note the previous page left (a deleted project)", async () => {
    const s = testServices({ licenseStatus: async () => ({}) as never, appInfo: async () => ({}) as never, projectList: async () => [], needsYouList: async () => [] });
    s.ui.flash('gocalc was deleted.');
    s.workspace.start();
    const r = mount(<WorkspaceHomeScreen />, s);
    await waitFor(() => expect(r.$('[data-testid="home-flash"]')!.textContent).toContain('gocalc was deleted.'), r);
    expect(s.ui.takeFlash()).toBeUndefined();
  });
});

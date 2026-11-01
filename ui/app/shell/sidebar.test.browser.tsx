import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../api/client.ts';
import type { LocalView } from '../api/localClient.ts';
import { cleanup, mount, testServices, waitFor } from '../test/render.tsx';
import { NARROW_SIDEBAR, UiModel, wideWindow } from '../state/ui.ts';
import { engineWords, Sidebar } from './sidebar.tsx';

afterEach(() => {
  cleanup();
  try {
    localStorage.removeItem('drydock.sidebar');
  } catch {
    /* no storage */
  }
});

function svc(over: Partial<Client> = {}) {
  const s = testServices({
    projectList: async () => [{ id: 'p', name: 'gocalc' }, { id: 'q', name: 'shop-api' }] as never,
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({}) as never,
    agentList: async () => [],
    roomList: async () => [],
    roomUnread: async () => [],
    presenceList: async () => [],
    taskList: async () => [],
    needsYouList: async () => [],
    ...over,
  });
  s.workspace.start();
  s.project.start();
  return s;
}

const links = (r: { $$: (s: string) => NodeListOf<Element> }) => Array.from(r.$$('nav[aria-label="Main"] a[data-testid]')).map((e) => e.getAttribute('data-testid'));

describe('the sidebar', () => {
  it("on a project's page: six places (decision P140): New, Home, Inbox with its count, Chats, the projects (one line each, P141), then Agents and Settings", async () => {
    const r = mount(<Sidebar route={{ name: 'tasks' }} />, svc());
    await waitFor(() => expect(r.$('[data-testid="nav-project-item"]')?.getAttribute('aria-expanded')).toBe('true'), r);
    expect(r.$('nav[aria-label="Main"]')).not.toBeNull();
    expect(links(r)).toEqual([
      'nav-new',
      'nav-workspace',
      'nav-needs-you',
      'nav-chats',
      'nav-project-item',
      'nav-project-item',
      'nav-library',
      'nav-settings',
    ]);
    // gone from the sidebar: Running and My tasks (tabs of the Inbox), All crews, Security scan, the agent CLIs, Integrations, Models,
    // Skills (Settings or the Agents library), the Guide (Settings > Help & about and the command menu)
    // and the project's old tabs: Work, Crew and Settings are the project's tab bar (decision P141)
    for (const id of ['nav-overview', 'nav-tasks', 'nav-rooms', 'nav-team', 'nav-knowledge', 'nav-running', 'nav-my-tasks', 'nav-crew', 'nav-security-scan', 'nav-side-agents', 'nav-side-integrations', 'nav-side-models', 'nav-library-skills', 'nav-guide-top']) {
      expect([id, r.$(`[data-testid="${id}"]`)]).toEqual([id, null]);
    }
    expect(r.$('[data-testid="brand"]')!.textContent).toBe('Drydock');
    expect(r.$('[data-testid="nav-new"]')!.getAttribute('href')).toBe('/home');
    expect(r.$('[data-testid="nav-new"]')!.textContent).toBe('NewCtrl N');
    expect(r.$('[data-testid="nav-new"]')!.getAttribute('aria-keyshortcuts')).toBe('Control+N');
    expect(r.$('[data-testid="nav-workspace"]')!.textContent).toBe('Home');
    expect(r.$('[data-testid="nav-workspace"]')!.getAttribute('href')).toBe('/home');
    expect(r.$('[data-testid="needs-count"]')!.getAttribute('aria-label')).toBe('0 items need you');
    expect(r.$('[data-testid="nav-needs-you"]')!.textContent).toContain('Inbox');
    expect(r.$('[data-testid="nav-library"]')!.textContent).toBe('Agents');
    expect(r.$('[data-testid="nav-library"]')!.getAttribute('href')).toBe('/library');
    expect(r.$('ul[aria-label="App"] [data-testid="nav-settings"]')!.getAttribute('href')).toBe('/settings');
    // search stays one click away, beside the name
    expect(r.$('button[aria-label="Search and jump (Ctrl+K)"]')).not.toBeNull();
    expect(r.$('[data-testid="stream"]')).not.toBeNull();
    // the open project is marked (its Work is "/"); the other is one line
    const items = r.$$('[data-testid="nav-project-item"]');
    expect(items[0]!.getAttribute('aria-expanded')).toBe('true');
    expect(items[0]!.getAttribute('aria-current')).toBe('true');
    expect(items[0]!.getAttribute('href')).toBe('/');
    expect(items[1]!.getAttribute('aria-expanded')).toBe('false');
    expect(items[1]!.getAttribute('aria-current')).toBeNull();
    expect(r.$$('[aria-current="page"]')).toHaveLength(0);
  });

  it("counts every project's items that need you; the Inbox is current on its tabs and on My tasks", async () => {
    const s = svc({
      // a project's own list is not the count: the workspace's (no project) is
      needsYouList: async (pid: string) => (pid === '' ? [{ key: 'a' }, { key: 'b' }, { key: 'c' }] : [{ key: 'a' }]) as never,
    });
    const r = mount(<Sidebar route={{ name: 'needs-you' }} />, s);
    await waitFor(() => expect(r.$('[data-testid="needs-count"]')!.textContent).toBe('3'), r);
    expect(r.$('[data-testid="needs-count"]')!.getAttribute('aria-label')).toBe('3 items need you');
    expect(r.$('[data-testid="nav-needs-you"]')!.getAttribute('aria-label')).toBe('Inbox: 3 items need you');
    // the Inbox is the workspace's page: no project is open in the sidebar
    expect(r.$('[data-testid="nav-overview"]')).toBeNull();
    expect(r.$('[data-testid="nav-needs-you"]')!.getAttribute('aria-current')).toBe('page');
    await r.act(() => s.ui.setInboxTab('running'));
    expect(r.$('[data-testid="nav-needs-you"]')!.getAttribute('aria-current')).toBe('page');
    cleanup();
    const mine = mount(<Sidebar route={{ name: 'my-tasks' }} />, svc());
    await waitFor(() => expect(mine.$('[data-testid="nav-needs-you"]')!.getAttribute('aria-current')).toBe('page'), mine);
    expect(mine.$$('[aria-current="page"]')).toHaveLength(1);
  });

  it('marks the open project on every page of it, its Work as the page itself (decision P141)', async () => {
    for (const route of [{ name: 'task', id: 't1' }, { name: 'pulls' }, { name: 'repo-map' }, { name: 'room', id: 'r1' }, { name: 'agent', id: 'agt1' }, { name: 'project' }, { name: 'home' }] as const) {
      const r = mount(<Sidebar route={route} />, svc());
      await waitFor(() => expect(r.$('[data-testid="nav-project-item"]')?.getAttribute('aria-expanded')).toBe('true'), r);
      expect([route.name, r.$('[data-testid="nav-project-item"]')!.getAttribute('aria-current')]).toEqual([route.name, route.name === 'home' ? 'page' : 'true']);
      expect(r.$$('[aria-current="page"]').length).toBe(route.name === 'home' ? 1 : 0);
      cleanup();
    }
  });

  it('marks the page current: Chats, Home, New, Agents for the library and every crew, Settings for every section', async () => {
    for (const [route, id] of [
      [{ name: 'chat', id: 'c1' }, 'nav-chats'],
      [{ name: 'workspace' }, 'nav-workspace'],
      [{ name: 'project-new' }, 'nav-new'],
      [{ name: 'library' }, 'nav-library'],
      [{ name: 'library-starters' }, 'nav-library'],
      [{ name: 'library-skills' }, 'nav-library'],
      [{ name: 'crew' }, 'nav-library'],
      // every page that left the sidebar is a section of Settings, under its old address
      [{ name: 'security-scan' }, 'nav-settings'],
      [{ name: 'agents' }, 'nav-settings'],
      [{ name: 'agents', key: 'claude' }, 'nav-settings'],
      [{ name: 'connections' }, 'nav-settings'],
      [{ name: 'integrations', key: 'linear' }, 'nav-settings'],
      [{ name: 'github' }, 'nav-settings'],
      [{ name: 'mcp' }, 'nav-settings'],
      [{ name: 'models' }, 'nav-settings'],
      [{ name: 'providers' }, 'nav-settings'],
      [{ name: 'routing' }, 'nav-settings'],
      [{ name: 'settings-shortcuts' }, 'nav-settings'],
      [{ name: 'settings-about' }, 'nav-settings'],
      [{ name: 'license' }, 'nav-settings'],
    ] as const) {
      const r = mount(<Sidebar route={route} />, svc());
      await waitFor(() => expect(r.$(`[data-testid="${id}"]`)?.getAttribute('aria-current')).toBe('page'), r);
      expect([id, r.$$('[aria-current="page"]').length]).toEqual([id, 1]);
      cleanup();
    }
  });

  it('on a workspace page the projects are lines and no project\'s tabs show', async () => {
    const r = mount(<Sidebar route={{ name: 'library' }} />, svc());
    await waitFor(() => expect(r.$$('[data-testid="nav-project-item"]')).toHaveLength(2), r);
    expect(r.$('[data-testid="nav-overview"]')).toBeNull();
    expect(r.$('[data-testid="nav-chats"]')).not.toBeNull();
  });

  it('Ctrl+N goes to New everywhere but Chats (whose screen makes a new chat), and never from a field', async () => {
    const went: string[] = [];
    const s = svc();
    const ctrlN = (target: EventTarget) => {
      const e = new KeyboardEvent('keydown', { key: 'n', ctrlKey: true, bubbles: true, cancelable: true });
      target.dispatchEvent(e);
      return e;
    };
    const r = mount(<Sidebar route={{ name: 'tasks' }} onNavigate={(h) => went.push(h)} />, s);
    await waitFor(() => expect(r.$('[data-testid="nav-new"]')).not.toBeNull(), r);
    // New is the start screen with the cursor in its box (decision P142)
    const startBox = document.createElement('textarea');
    startBox.dataset.testid = 'start-text';
    document.body.append(startBox);
    expect(ctrlN(document.body).defaultPrevented).toBe(true);
    expect(went).toEqual(['/home']);
    await waitFor(() => expect(document.activeElement).toBe(startBox), r);
    startBox.remove();
    const input = document.createElement('input');
    document.body.append(input);
    expect(ctrlN(input).defaultPrevented).toBe(false);
    input.remove();
    cleanup();
    const chats = mount(<Sidebar route={{ name: 'chats' }} onNavigate={(h) => went.push(h)} />, s);
    await waitFor(() => expect(chats.$('[data-testid="nav-new"]')).not.toBeNull(), chats);
    expect(ctrlN(document.body).defaultPrevented).toBe(false);
    expect(went).toEqual(['/home']);
  });

  it('Models is a section of Settings: the engine line leads to /models, and no project is open there', async () => {
    const v = {
      supported: true,
      runtime: { installed: true, sourceName: 'TurboQuant build', tag: 'b10298-2.0.0', backend: 'vulkan' },
      backends: [{ id: 'vulkan', label: 'Vulkan' }],
      server: { state: 'running' },
    } as unknown as LocalView;
    const r = mount(<Sidebar route={{ name: 'models' }} />, svc({ localGet: async () => v }));
    await waitFor(() => expect(r.$$('[data-testid="nav-project-item"]')).toHaveLength(2), r);
    expect(r.$('[data-testid="nav-overview"]')).toBeNull();
    expect(r.$('[data-testid="nav-settings"]')!.getAttribute('aria-current')).toBe('page');
    await waitFor(() => expect(r.$('[data-testid="engine-card"]')?.getAttribute('href')).toBe('/models'), r);
  });

  it('folds to the rail with its button or "[" and remembers it; a text field keeps the key', async () => {
    const s = svc();
    const r = mount(<Sidebar route={{ name: 'home' }} />, s);
    await waitFor(() => expect(r.$('[data-testid="nav-project-item"]')?.getAttribute('aria-expanded')).toBe('true'), r);
    const aside = r.$('[data-testid="sidebar"]')!;
    expect(aside.getAttribute('data-rail')).toBeNull();
    const fold = r.$('[data-testid="sidebar-fold"]') as HTMLButtonElement;
    expect(fold.getAttribute('aria-label')).toBe('Collapse the sidebar ([)');
    await r.act(() => fold.click());
    expect(aside.getAttribute('data-rail')).toBe('true');
    expect(fold.getAttribute('aria-expanded')).toBe('false');
    expect(localStorage.getItem('drydock.sidebar')).toBe('"rail"');
    // the labels stay the links' names in the rail
    expect(r.$('[data-testid="nav-chats"]')!.getAttribute('title')).toBe('Chats');
    await r.act(() => window.dispatchEvent(new KeyboardEvent('keydown', { key: '[', bubbles: true, cancelable: true })));
    expect(aside.getAttribute('data-rail')).toBeNull();
    const input = document.createElement('input');
    document.body.append(input);
    await r.act(() => input.dispatchEvent(new KeyboardEvent('keydown', { key: '[', bubbles: true, cancelable: true })));
    expect(aside.getAttribute('data-rail')).toBeNull();
    // Ctrl+B folds and unfolds it too, also from a field (it types nothing there), never inside the terminal (P129)
    await r.act(() => input.dispatchEvent(new KeyboardEvent('keydown', { key: 'b', ctrlKey: true, bubbles: true, cancelable: true })));
    expect(aside.getAttribute('data-rail')).toBe('true');
    await r.act(() => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'b', ctrlKey: true, bubbles: true, cancelable: true })));
    expect(aside.getAttribute('data-rail')).toBeNull();
    const term = document.createElement('div');
    term.className = 'xterm';
    document.body.append(term);
    const inTerm = new KeyboardEvent('keydown', { key: 'b', ctrlKey: true, bubbles: true, cancelable: true });
    await r.act(() => term.dispatchEvent(inTerm));
    expect(aside.getAttribute('data-rail')).toBeNull();
    expect(inTerm.defaultPrevented).toBe(false);
    term.remove();
    input.remove();
    // a new model reads the stored choice back
    expect(new UiModel({ media: wideWindow }).sidebarRail).toBe(false);
    localStorage.setItem('drydock.sidebar', '"rail"');
    expect(new UiModel({ media: wideWindow }).sidebarRail).toBe(true);
  });

  it('a narrow window starts folded until the person chooses', () => {
    const narrow = () => ({ matches: true, addEventListener: () => {} });
    expect(new UiModel({ media: narrow }).sidebarRail).toBe(true);
    // a laptop's 1024 to 1199 px counts as narrow for the sidebar (the detail of a list page keeps its room)
    const asked: string[] = [];
    void new UiModel({ media: (q) => (asked.push(q), { matches: false, addEventListener: () => {} }) }).sidebarRail;
    expect(asked).toContain(NARROW_SIDEBAR);
    expect(NARROW_SIDEBAR).toBe('(max-width: 1199px)');
    localStorage.setItem('drydock.sidebar', '"full"');
    expect(new UiModel({ media: narrow }).sidebarRail).toBe(false);
  });

  it("shows the local engine's state and build when one is installed, and nothing otherwise", async () => {
    const v = {
      supported: true,
      runtime: { installed: true, sourceName: 'TurboQuant build', tag: 'b10298-2.0.0', backend: 'vulkan' },
      backends: [{ id: 'vulkan', label: 'Vulkan' }],
      server: { state: 'running' },
    } as unknown as LocalView;
    expect(engineWords(v)).toEqual({ running: true, title: 'Local engine running', sub: 'TurboQuant build b10298 · Vulkan' });
    expect(engineWords({ ...v, server: { state: 'stopped' } } as unknown as LocalView)?.title).toBe('Local engine stopped');
    expect(engineWords({ ...v, runtime: { installed: false } } as unknown as LocalView)).toBeUndefined();
    expect(engineWords(undefined)).toBeUndefined();
    const r = mount(<Sidebar route={{ name: 'home' }} />, svc({ localGet: async () => v }));
    await waitFor(() => expect(r.$('[data-testid="engine-card"]')?.textContent).toContain('Local engine running'), r);
    // one quiet line: the build is in its title
    expect(r.$('[data-testid="engine-card"]')!.getAttribute('title')).toBe('Local engine running: TurboQuant build b10298 · Vulkan');
  });
});

import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../api/client.ts';
import type { LocalView } from '../api/localClient.ts';
import { cleanup, mount, testServices, waitFor } from '../test/render.tsx';
import { UiModel, wideWindow } from '../state/ui.ts';
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
  it("on a project's page: the mark, the switcher, search, Inbox with its count, Running, My tasks, Chats, the projects with the open one's tabs, the workspace's pages", async () => {
    const r = mount(<Sidebar route={{ name: 'tasks' }} />, svc());
    await waitFor(() => expect(r.$('[data-testid="nav-overview"]')).not.toBeNull(), r);
    expect(r.$('nav[aria-label="Main"]')).not.toBeNull();
    expect(links(r)).toEqual([
      'nav-workspace',
      'nav-needs-you',
      'nav-running',
      'nav-my-tasks',
      'nav-chats',
      'nav-project-item',
      'nav-overview',
      'nav-tasks',
      'nav-rooms',
      'nav-team',
      'nav-knowledge',
      'nav-project-item',
      'nav-crew',
      'nav-security-scan',
      'nav-side-connections',
      'nav-side-models',
      'nav-library',
      'nav-settings',
      'nav-guide-top',
    ]);
    expect(r.$('[data-testid="needs-count"]')!.getAttribute('aria-label')).toBe('0 items need you');
    expect(r.$('[data-testid="nav-tasks"]')!.getAttribute('aria-current')).toBe('page');
    expect(r.$('[data-testid="nav-needs-you"]')!.textContent).toContain('Inbox');
    expect(r.$('[data-testid="nav-team"]')!.textContent).toBe('Crew');
    // every project's crew is a workspace page, under Workspace
    expect(r.$('ul[aria-label="Workspace"] [data-testid="nav-crew"]')!.getAttribute('href')).toBe('/crew');
    expect(r.$('[data-testid="nav-crew"]')!.textContent).toBe('Crew');
    expect(r.$('[data-testid="nav-settings"]')).not.toBeNull();
    expect(r.$('[data-testid="nav-guide-top"]')).not.toBeNull();
    expect(r.$('button[aria-label="Switch project"]')!.textContent).toContain('gocalc');
    expect(r.$('button[aria-label="Search and jump (Ctrl+K)"]')).not.toBeNull();
    expect(r.$('[data-testid="stream"]')).not.toBeNull();
    // the open project is the one whose tabs show; the other is one line
    const items = r.$$('[data-testid="nav-project-item"]');
    expect(items[0]!.getAttribute('aria-expanded')).toBe('true');
    expect(items[1]!.getAttribute('aria-expanded')).toBe('false');
    expect(r.$$('[aria-current="page"]')).toHaveLength(1);
  });

  it("counts every project's items that need you and what runs; the Inbox, Running and My tasks are the workspace's pages", async () => {
    const s = svc({
      // a project's own list is not the count: the workspace's (no project) is
      needsYouList: async (pid: string) => (pid === '' ? [{ key: 'a' }, { key: 'b' }, { key: 'c' }] : [{ key: 'a' }]) as never,
      taskSummaries: async () => ({ summaries: [{ taskId: 't1', status: 'IN_PROGRESS' }, { taskId: 't2', status: 'REVIEW' }, { taskId: 't3', status: 'WAITING_HUMAN' }, { taskId: 't4', status: 'COMPLETED' }] as never, total: 4, partial: false }),
      securityScanRunning: async () => [{ id: 's1', name: 'shop-api', engines: [] }] as never,
    });
    const r = mount(<Sidebar route={{ name: 'needs-you' }} />, s);
    await waitFor(() => expect(r.$('[data-testid="needs-count"]')!.textContent).toBe('3'), r);
    expect(r.$('[data-testid="needs-count"]')!.getAttribute('aria-label')).toBe('3 items need you');
    expect(r.$('[data-testid="nav-needs-you"]')!.getAttribute('aria-label')).toBe('Inbox: 3 items need you');
    await waitFor(() => expect(r.$('[data-testid="running-count"]')!.textContent).toBe('3'), r);
    expect(r.$('[data-testid="nav-running"]')!.getAttribute('href')).toBe('/needs-you?tab=running');
    expect(r.$('[data-testid="nav-running"]')!.getAttribute('aria-label')).toBe('Running: 3 tasks and scans run');
    expect(r.$('[data-testid="nav-my-tasks"]')!.getAttribute('href')).toBe('/my-tasks');
    // the Inbox is the workspace's page: no project is open in the sidebar
    expect(r.$('[data-testid="nav-overview"]')).toBeNull();
    expect(r.$('[data-testid="nav-needs-you"]')!.getAttribute('aria-current')).toBe('page');
    expect(r.$('[data-testid="nav-running"]')!.getAttribute('aria-current')).toBeNull();
    // its Running tab lights Running instead
    await r.act(() => s.ui.setInboxTab('running'));
    expect(r.$('[data-testid="nav-running"]')!.getAttribute('aria-current')).toBe('page');
    expect(r.$('[data-testid="nav-needs-you"]')!.getAttribute('aria-current')).toBeNull();
    cleanup();
    const mine = mount(<Sidebar route={{ name: 'my-tasks' }} />, svc());
    await waitFor(() => expect(mine.$('[data-testid="nav-my-tasks"]')!.getAttribute('aria-current')).toBe('page'), mine);
    expect(mine.$$('[aria-current="page"]')).toHaveLength(1);
  });

  it('marks the page current: a tab of the project, Chats, the workspace Crew, Security, Connections, Models on this PC, Settings, the Guide', async () => {
    for (const [route, id] of [
      [{ name: 'task', id: 't1' }, 'nav-tasks'],
      [{ name: 'pulls' }, 'nav-tasks'],
      [{ name: 'repo-map' }, 'nav-knowledge'],
      [{ name: 'room', id: 'r1' }, 'nav-rooms'],
      [{ name: 'agent', id: 'agt1' }, 'nav-team'],
      [{ name: 'project' }, 'nav-overview'],
      [{ name: 'chat', id: 'c1' }, 'nav-chats'],
      [{ name: 'crew' }, 'nav-crew'],
      [{ name: 'security-scan' }, 'nav-security-scan'],
      [{ name: 'connections' }, 'nav-side-connections'],
      [{ name: 'providers' }, 'nav-side-models'],
      [{ name: 'github' }, 'nav-settings'],
      [{ name: 'guide', topic: 'scan' }, 'nav-guide-top'],
      [{ name: 'workspace' }, 'nav-workspace'],
    ] as const) {
      const r = mount(<Sidebar route={route} />, svc());
      await waitFor(() => expect(r.$(`[data-testid="${id}"]`)?.getAttribute('aria-current')).toBe('page'), r);
      expect([id, r.$$('[aria-current="page"]').length]).toEqual([id, 1]);
      cleanup();
    }
  });

  it("on a workspace page the projects are lines and no project's tabs show; the switcher says Home", async () => {
    const r = mount(<Sidebar route={{ name: 'library' }} />, svc());
    await waitFor(() => expect(r.$$('[data-testid="nav-project-item"]')).toHaveLength(2), r);
    expect(r.$('[data-testid="nav-overview"]')).toBeNull();
    expect(r.$('[data-testid="nav-chats"]')).not.toBeNull();
    expect(r.$('button[aria-label="Switch project"]')!.textContent).toContain('Home');
  });

  it('folds to the rail with its button or "[" and remembers it; a text field keeps the key', async () => {
    const s = svc();
    const r = mount(<Sidebar route={{ name: 'home' }} />, s);
    await waitFor(() => expect(r.$('[data-testid="nav-overview"]')).not.toBeNull(), r);
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
    input.remove();
    // a new model reads the stored choice back
    expect(new UiModel({ media: wideWindow }).sidebarRail).toBe(false);
    localStorage.setItem('drydock.sidebar', '"rail"');
    expect(new UiModel({ media: wideWindow }).sidebarRail).toBe(true);
  });

  it('a narrow window starts folded until the person chooses', () => {
    const narrow = () => ({ matches: true, addEventListener: () => {} });
    expect(new UiModel({ media: narrow }).sidebarRail).toBe(true);
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
    expect(r.$('[data-testid="engine-card"]')!.textContent).toContain('TurboQuant build b10298 · Vulkan');
  });
});
